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

// connect completes the handshake and returns a live session, or the reason it
// could not. The URL is checked before anything is dialled — a plainly bad URL
// is an error the caller reads rather than a connection they wait on — and the
// dial-time Control check in Guard.HTTPClient is what actually enforces the
// address rules on every hop.
//
// One transport per session and no reuse across calls: a session carries the
// guard's pinned dialer and the tenant's token, and sharing one between turns
// would mean caching a credentialed connection to an address a redirect could
// have moved. Probe and CallTool both pay one handshake; that is the cost of
// the egress guarantee holding on every request.
func (c *Client) connect(ctx context.Context, url string, transport domain.MCPTransport, token string) (*sdk.ClientSession, error) {
	if !transport.Valid() {
		return nil, fmt.Errorf("unsupported transport %q", transport)
	}
	if err := c.guard.CheckURL(url); err != nil {
		return nil, err
	}

	httpClient := c.guard.HTTPClient(token)
	var t sdk.Transport
	switch transport {
	case domain.MCPTransportSSE:
		t = &sdk.SSEClientTransport{Endpoint: url, HTTPClient: httpClient}
	default:
		t = &sdk.StreamableClientTransport{
			Endpoint:   url,
			HTTPClient: httpClient,
			// One attempt. A probe is a question an admin is waiting on, and a
			// call is one an agent's turn is waiting on; five reconnects against
			// a server that is down turns either into a minute of silence.
			MaxRetries: -1,
			// We ask and they answer; nothing here needs the server to push. The
			// standalone stream is optional in the spec and is one more thing to
			// hang on a server that half-implements it.
			DisableStandaloneSSE: true,
		}
	}

	session, err := sdk.NewClient(&sdk.Implementation{Name: c.name, Version: c.version}, nil).
		Connect(ctx, t, nil)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	return session, nil
}

// Probe connects, completes the handshake, and lists the server's tools.
//
// Everything it returns is the tenant's server's text — a tool description is
// written by whoever runs that server and lands, after approval, in our agent's
// context. Nothing here is trusted; it is stored for a human to read, which is
// what the review screen exists for.
func (c *Client) Probe(ctx context.Context, url string, transport domain.MCPTransport, token string) ([]DiscoveredTool, error) {
	ctx, cancel := c.guard.contextWithTimeout(ctx)
	defer cancel()

	session, err := c.connect(ctx, url, transport, token)
	if err != nil {
		return nil, err
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

// CallResult is one tool call's outcome, as the agent should see it.
//
// IsError is the tenant tool's own business error (MCP puts it inside the
// result, not on the wire, precisely so the model can read it and self-correct)
// — the Text still carries the message. A transport or protocol failure is a Go
// error instead, because the agent cannot self-correct a server that is down.
type CallResult struct {
	Text    string
	IsError bool
}

// CallTool runs one tool on the tenant's server and returns its text (T-M2).
//
// It goes through the same guarded client Probe does, so the pinned dialer, the
// per-hop redirect check and the bearer token all apply to a call exactly as
// they do to discovery — this is the handover T-M1 spelled out: the guard lives
// on the client, so the call path gets it for free as long as it does not build
// an http.Client of its own.
//
// maxBytes bounds the assembled result. A server that answers a tool call with
// 40 MB of JSON is a context-window incident and a bill; past the cap this is a
// Go error the agent recovers from, not a result that reaches the model. The
// bytes are read into memory once before the cap is applied — the SDK owns the
// response body — so the cap is a ceiling on what enters the context, and the
// caller's context deadline is what bounds a server that streams slowly forever.
func (c *Client) CallTool(
	ctx context.Context, url string, transport domain.MCPTransport, token, toolName string,
	args map[string]any, maxBytes int,
) (CallResult, error) {
	session, err := c.connect(ctx, url, transport, token)
	if err != nil {
		return CallResult{}, err
	}
	defer func() { _ = session.Close() }()

	res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: toolName, Arguments: args})
	if err != nil {
		return CallResult{}, fmt.Errorf("call tool %q: %w", toolName, err)
	}

	var b strings.Builder
	for _, content := range res.Content {
		if tc, ok := content.(*sdk.TextContent); ok {
			b.WriteString(tc.Text)
			continue
		}
		// A non-text block (an image, an embedded resource) is described rather
		// than dropped: the model should know something came back it cannot read
		// inline, and a silently empty result reads as "the tool returned
		// nothing" when it did not.
		raw, mErr := content.MarshalJSON()
		if mErr == nil {
			b.Write(raw)
		}
	}
	text := b.String()
	// Structured-only results (Content unset by a server that populated only
	// StructuredContent) still have to reach the model.
	if text == "" && res.StructuredContent != nil {
		if raw, mErr := json.Marshal(res.StructuredContent); mErr == nil {
			text = string(raw)
		}
	}

	if maxBytes > 0 && len(text) > maxBytes {
		return CallResult{}, fmt.Errorf(
			"tool %q returned %d bytes, over the %d-byte limit; ask it for a narrower result",
			toolName, len(text), maxBytes)
	}
	return CallResult{Text: text, IsError: res.IsError}, nil
}
