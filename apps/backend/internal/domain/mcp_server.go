package domain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// The tenant's own MCP server (T-M1): their ticketing system, their CRM, their
// internal ops API, registered so their agents can call its tools.
//
// This is Argentum as the *client*. T-14 is the same protocol pointed the other
// way — we serve our tools to their agent — and the two share no code. The
// one-line test for which is which is who holds the credential: there, their
// agent holds an Argentum API key; here, we hold their token, which is why
// AuthEncrypted exists.
//
// An MCP server is a source of tools, not a source of rows (locked decision 1).
// It is deliberately not a DBConnection: there is no SQL to execute and no
// schema to introspect, and a driver that synthesised both would put a lie in
// the abstraction run_sql's safety rests on.

// MCPTransport is how we reach the server. HTTP only, forever: stdio would mean
// spawning the tenant's process inside our worker, which is arbitrary code
// execution wearing a config field (locked decision 3).
type MCPTransport string

const (
	// MCPTransportHTTP is the streamable HTTP transport (2025-03-26 spec).
	MCPTransportHTTP MCPTransport = "http"
	// MCPTransportSSE is the older HTTP+SSE transport, still what most
	// deployed servers speak.
	MCPTransportSSE MCPTransport = "sse"
)

// Valid reports whether t is a transport this release speaks. Anything else —
// "stdio" most of all — is a rejected save rather than a runtime surprise.
func (t MCPTransport) Valid() bool {
	return t == MCPTransportHTTP || t == MCPTransportSSE
}

// MCPServer is one registered server.
//
// AuthEncrypted never leaves the backend: it is `json:"-"` for the same reason
// DBConnection.DSNEncrypted is, and the read routes additionally report only
// whether a token is set. A credential a read route returns is a credential any
// admin session leak hands over.
type MCPServer struct {
	ID          string       `json:"id"`
	CompanyID   string       `json:"company_id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	URL         string       `json:"url"`
	Transport   MCPTransport `json:"transport"`

	AuthEncrypted []byte `json:"-"`
	// HasAuth is what the dashboard renders instead of the token: "a token is
	// set" is the only fact about it a browser needs, and it is the fact an
	// admin editing the row is actually looking for.
	HasAuth bool `json:"has_auth"`

	Enabled bool `json:"enabled"`
	// LastProbedAt and ProbeError are the last discovery attempt. A failure is
	// a saved row with a reason, not a rejected save — a server that is down at
	// 4pm is not a configuration error.
	LastProbedAt *time.Time `json:"last_probed_at,omitempty"`
	ProbeError   string     `json:"probe_error"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MCPServerTool is one discovered tool, and the review an admin gave it.
//
// Nothing here is callable until Approved is true. That is the opposite of the
// agent roster's rule, where an empty allowlist means every tool, and it is
// deliberate: there the failure is an agent that cannot answer a question, and
// here it is a write against a system we do not own.
type MCPServerTool struct {
	ID          string `json:"id"`
	ServerID    string `json:"server_id"`
	ToolName    string `json:"tool_name"`
	Description string `json:"description"`
	// InputSchema is the tool's JSON Schema, as the server gave it. Stored raw
	// because it is what T-M2 hands the model, and rewriting it here would mean
	// two descriptions of one tool's arguments.
	InputSchema json.RawMessage `json:"input_schema"`
	// ReadOnly is the admin's classification, not the server's claim. A server
	// that described `delete_everything` as read-only would be believed
	// otherwise, and the tenant's admin is the only party with standing to say
	// what a tool does to their own system.
	ReadOnly bool `json:"read_only"`
	Approved bool `json:"approved"`
	// ApprovedDigest is MCPToolDigest at the moment of approval. Discovery
	// compares against it: a server that rewrites a tool's description after
	// approval has changed the text that enters the agent's context, and that
	// shows as drift rather than being adopted silently.
	ApprovedDigest string    `json:"approved_digest"`
	DiscoveredAt   time.Time `json:"discovered_at"`
}

// Drifted reports whether an approved tool's text has changed since it was
// approved.
//
// False for a tool nobody has approved: there is nothing to have drifted from,
// and flagging an unreviewed tool as "changed" would bury the case that matters
// in the case that does not.
func (t *MCPServerTool) Drifted() bool {
	if t == nil || !t.Approved || t.ApprovedDigest == "" {
		return false
	}
	return MCPToolDigest(t.Description, t.InputSchema) != t.ApprovedDigest
}

// MCPToolDigest hashes what an admin actually reviewed: the description and the
// argument schema.
//
// Not the name — a renamed tool is a different row, keyed by (server, name) —
// and not the read-only flag, which is our classification rather than the
// server's text.
func MCPToolDigest(description string, schema json.RawMessage) string {
	h := sha256.New()
	h.Write([]byte(description))
	h.Write([]byte{0})
	h.Write(schema)
	return hex.EncodeToString(h.Sum(nil))
}

// MCPServerRepository is the persistence contract.
//
// Every method takes a company id beside the server id, unlike
// ConnectionRepository's GetByID: this table is reached from an admin-only CRUD
// surface where the id is a bare uuid in a URL, and a repository that can be
// asked for another company's row is one call away from a handler that forgets
// to check.
type MCPServerRepository interface {
	Create(ctx context.Context, s *MCPServer) error
	GetByID(ctx context.Context, companyID, id string) (*MCPServer, error)
	ListByCompany(ctx context.Context, companyID string) ([]*MCPServer, error)
	Update(ctx context.Context, s *MCPServer) error
	Delete(ctx context.Context, companyID, id string) error
	// RecordProbe stores the outcome of a discovery attempt without touching
	// anything the admin typed.
	RecordProbe(ctx context.Context, companyID, id string, at time.Time, probeErr string) error

	// ListTools returns one server's discovered tools, in a stable order.
	ListTools(ctx context.Context, serverID string) ([]*MCPServerTool, error)
	// ReplaceTools writes what discovery found, preserving the review state of
	// tools that are still there and dropping the ones that are gone. It is one
	// method rather than an upsert plus a delete because a discovery that
	// half-applied would leave a tool list nobody reviewed.
	ReplaceTools(ctx context.Context, serverID string, tools []*MCPServerTool) error
	// SetToolReview records an admin's decision on one tool.
	SetToolReview(ctx context.Context, serverID, toolID string, approved, readOnly bool, digest string) error
}
