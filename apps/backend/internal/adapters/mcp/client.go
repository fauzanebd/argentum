package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/fauzanebd/argentum/internal/domain"
)

// Client connects to a tenant's MCP server and asks it what it can do.
//
// One method in this ticket — Probe — because discovery is explicit rather than
// per turn (locked decision 6). Fetching the tool list on every question would
// add a network round trip to every question and would let the tenant's server
// change what our agent can do without anybody looking at the change. Calling a
// tool is T-M2.
type Client struct {
	guard Guard
	// name and version identify us to the tenant's server in the MCP
	// handshake. Their logs are the first place somebody looks when a token
	// starts making requests, and "unknown client" is a bad thing to find.
	name    string
	version string
}

// clientVersion is what we tell the tenant's server we are. It is a constant
// rather than a build stamp because the binary's version says nothing useful to
// the operator of somebody else's MCP server; what they want in their log is a
// name they can search for and a number that moves when our client's behaviour
// does. Bump it when this package's protocol behaviour changes.
const clientVersion = "1"

// NewClient builds the client. The guard is not optional and not defaulted:
// this package makes outbound requests to addresses a tenant typed, and a
// zero-value guard that happened to allow everything would be a footgun with
// no visible trigger.
func NewClient(guard Guard) *Client {
	return &Client{guard: guard, name: "argentum", version: clientVersion}
}

// DiscoveredTool is one entry from tools/list, before anybody has reviewed it.
type DiscoveredTool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// CheckURL is the guard's save-time refusal, re-exported so a service can ask
// the question without holding a Guard of its own. One guard per process,
// built from config in bootstrap: a caller that could construct its own is a
// caller that could construct a permissive one.
//
// It is the *resolved* check, because a save is where a name that answers with
// a private address has to be caught — see Guard.CheckResolvedURL for why the
// string check alone was not enough.
func (c *Client) CheckURL(raw string) error { return c.guard.CheckResolvedURL(raw) }

// AllowsInsecureHTTP reports whether this deployment accepts a plaintext http
// MCP URL. The form asks so its own sentence matches the rule the save will
// apply — "must be https" beside a deployment that accepts http is a hint that
// costs an admin a support ticket.
func (c *Client) AllowsInsecureHTTP() bool { return c.guard.AllowInsecureHTTP || c.guard.AllowPrivate }

// Probe connects, completes the handshake, and lists the server's tools.
//
// Everything it returns is the tenant's server's text — a tool description is
// written by whoever runs that server and lands, after approval, in our agent's
// context. Nothing here is trusted; it is stored for a human to read, which is
// what the review screen exists for.
func (c *Client) Probe(ctx context.Context, url string, transport domain.MCPTransport, token string) ([]DiscoveredTool, error) {
	if !transport.Valid() {
		return nil, fmt.Errorf("unsupported transport %q", transport)
	}
	// Checked before anything is dialled, so a plainly bad URL is an error the
	// admin reads rather than a connection attempt they wait for. The dial-time
	// check in Guard.HTTPClient is what actually enforces this.
	if err := c.guard.CheckURL(url); err != nil {
		return nil, err
	}

	ctx, cancel := c.guard.contextWithTimeout(ctx)
	defer cancel()

	httpClient := c.guard.HTTPClient(token)
	var t sdk.Transport
	switch transport {
	case domain.MCPTransportSSE:
		t = &sdk.SSEClientTransport{Endpoint: url, HTTPClient: httpClient}
	default:
		t = &sdk.StreamableClientTransport{
			Endpoint:   url,
			HTTPClient: httpClient,
			// One attempt. A probe is a question an admin is waiting on, and
			// five reconnects against a server that is down turns Save into a
			// minute of silence.
			MaxRetries: -1,
			// We ask and they answer; nothing in discovery needs the server to
			// push. The standalone stream is optional in the spec and is one
			// more thing to hang on a server that half-implements it.
			DisableStandaloneSSE: true,
		}
	}

	session, err := sdk.NewClient(&sdk.Implementation{Name: c.name, Version: c.version}, nil).
		Connect(ctx, t, nil)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = session.Close() }()

	var out []DiscoveredTool
	res, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}
	for _, tool := range res.Tools {
		if tool == nil || strings.TrimSpace(tool.Name) == "" {
			continue
		}
		schema := json.RawMessage(`{}`)
		if tool.InputSchema != nil {
			if raw, err := json.Marshal(tool.InputSchema); err == nil {
				schema = raw
			}
		}
		out = append(out, DiscoveredTool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: schema,
		})
	}
	return out, nil
}
