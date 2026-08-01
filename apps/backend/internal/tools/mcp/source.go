package mcptools

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/agentbudget"
	"github.com/fauzanebd/argentum/internal/agentscope"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tools"
)

// errNoCipher is the state where a server has a stored token but the provider
// was built without a cipher to read it — a wiring bug, surfaced as the server
// being unavailable rather than called unauthenticated.
var errNoCipher = errors.New("no cipher configured to read the stored token")

// ServerStore is the persistence this provider reads: the company's servers and
// each one's reviewed tools. Both come from domain.MCPServerRepository; narrowed
// here to the two reads a turn makes, because a provider that could also write
// the review state is a provider that could be asked to.
type ServerStore interface {
	ListByCompany(ctx context.Context, companyID string) ([]*domain.MCPServer, error)
	ListTools(ctx context.Context, serverID string) ([]*domain.MCPServerTool, error)
}

// Cipher decrypts a stored bearer token. Satisfied by *crypto.DSNCipher, the
// same envelope the CRUD service stores it under.
type Cipher interface {
	Decrypt(blob []byte) (string, error)
}

// Caps bound one turn's MCP usage. They are deployment config, not per-tenant:
// a runaway is a runaway whoever owns the server.
type Caps struct {
	// CallTimeout bounds one tool call. Zero leaves only the client's dial and
	// handshake timeouts, which is usually not what an operator wants.
	CallTimeout time.Duration
	// MaxResponseBytes caps one result before it enters the context. Zero
	// disables the cap.
	MaxResponseBytes int
	// MaxCallsPerTurn caps how many MCP calls one turn may make across all its
	// MCP tools. Zero disables this backstop and leaves T-16's budget as the
	// only bound.
	MaxCallsPerTurn int
}

// Source builds the MCP tools one turn may call (T-M2). One per process, built
// in bootstrap; CompanyTools is called once per turn by ChatRunner, after the
// scope is on the context.
//
// The tools it returns are already wrapped — budget-guarded and audited — so a
// caller cannot get an unbounded or unaudited MCP tool out of this type. That is
// the ticket's "wrapping only the static half is the bug" made structural: the
// static half is wrapped in bootstrap and this half is wrapped here, with the
// same two decorators in the same order, and neither half can skip it.
type Source struct {
	store    ServerStore
	cipher   Cipher
	caller   Caller
	recorder tools.ActionAuditor
	caps     Caps
}

// NewSource wires the provider. recorder is the audit sink every MCP call's row
// is written to; a nil recorder leaves the tools unaudited exactly as
// tools.WithAuditAll does, which is only the shape a process with no control DB
// takes.
func NewSource(store ServerStore, cipher Cipher, caller Caller, recorder tools.ActionAuditor, caps Caps) *Source {
	return &Source{store: store, cipher: cipher, caller: caller, recorder: recorder, caps: caps}
}

// CompanyTools returns the MCP tools this turn's agent may call, wrapped and
// ready to hand to the factory.
//
// It reads the turn's scope from the context: **empty MCPServerIDs means none**
// (locked decision 5), so an unscoped turn, the eval harness, or an agent with
// no binding all take the fast path and get nil — byte-for-byte the tool list
// this product had before T-M2. Nothing is cached across turns: a server
// disabled, deleted, or whose tool drifted since the last turn is simply absent
// from the next one's list, which is what "removed mid-session, gone on the next
// turn" means and why there is never a stale tool to call.
func (s *Source) CompanyTools(ctx context.Context, companyID string) []interfaces.Tool {
	if s == nil {
		return nil
	}
	scope := agentscope.FromContext(ctx)
	if len(scope.MCPServerIDs) == 0 {
		return nil
	}

	servers, err := s.store.ListByCompany(ctx, companyID)
	if err != nil {
		logrus.WithError(err).WithField("company_id", companyID).
			Warn("mcp: could not list servers for the turn; the agent runs without MCP tools")
		return nil
	}

	guard := newCallGuard(s.caps.MaxCallsPerTurn)
	seen := map[string]bool{}
	var raw []interfaces.Tool

	for _, srv := range servers {
		// A binding is the only thing that reaches a server (empty means none),
		// and a disabled server reaches nobody.
		if !srv.Enabled || !scope.AllowsMCPServer(srv.ID) {
			continue
		}
		token, err := s.decrypt(srv)
		if err != nil {
			// A server whose token we cannot read is one we cannot call. Skip it
			// rather than call it unauthenticated, which would leak that the URL
			// exists to a server the tenant meant to gate.
			logrus.WithError(err).WithFields(logrus.Fields{
				"company_id": companyID, "server_id": srv.ID,
			}).Warn("mcp: could not read a server's token; its tools are unavailable this turn")
			continue
		}
		toolRows, err := s.store.ListTools(ctx, srv.ID)
		if err != nil {
			logrus.WithError(err).WithField("server_id", srv.ID).
				Warn("mcp: could not list a server's tools; skipping it this turn")
			continue
		}
		for _, tr := range toolRows {
			// Three gates, all required (T-M1 handover): approved says an admin
			// read it, read_only says what it does (T-M4 is what relaxes that),
			// and not-drifted says the text has not changed since the admin read
			// it. Any one false and the tool is not offered.
			if !tr.Approved || !tr.ReadOnly || tr.Drifted() {
				continue
			}
			name := s.uniqueName(seen, srv.Name, tr.ToolName)
			raw = append(raw, &Tool{
				serverID:   srv.ID,
				serverName: srv.Name,
				rawName:    tr.ToolName,
				name:       name,
				desc:       tr.Description,
				params:     paramsFromSchema(tr.InputSchema),
				caller:     s.caller,
				url:        srv.URL,
				transport:  srv.Transport,
				token:      token,
				timeout:    s.caps.CallTimeout,
				maxBytes:   s.caps.MaxResponseBytes,
				calls:      guard,
			})
		}
	}

	if len(raw) == 0 {
		return nil
	}

	// The same two decorators, in the same order, as bootstrap wraps the static
	// registry with: the budget guard inside (so a refusal is what the audit
	// wrapper sees and records as blocked), the audit recorder outside. An MCP
	// tool is bounded and audited exactly as run_sql is.
	return tools.WithAuditAll(agentbudget.GuardAll(raw), s.recorder)
}

// decrypt reads a server's bearer token. No token is not an error: a server
// open to anyone who knows its URL is a supported configuration.
func (s *Source) decrypt(srv *domain.MCPServer) (string, error) {
	if len(srv.AuthEncrypted) == 0 {
		return "", nil
	}
	if s.cipher == nil {
		return "", errNoCipher
	}
	return s.cipher.Decrypt(srv.AuthEncrypted)
}

// uniqueName namespaces a tool and resolves the rare collision — two servers
// whose slugs match on a tool of the same name, or two names that truncated to
// the same 64 characters — by suffixing. Dispatch is by exact name, so two
// tools sharing one would make one uncallable; a suffix keeps both reachable
// while the audit row's server id keeps them distinguishable.
func (s *Source) uniqueName(seen map[string]bool, serverName, toolName string) string {
	name := namespaced(serverName, toolName)
	if !seen[name] {
		seen[name] = true
		return name
	}
	for i := 2; ; i++ {
		suffix := "_" + strconv.Itoa(i)
		trimmed := name
		if len(trimmed)+len(suffix) > maxNameLen {
			trimmed = trimmed[:maxNameLen-len(suffix)]
		}
		candidate := trimmed + suffix
		if !seen[candidate] {
			seen[candidate] = true
			return candidate
		}
	}
}
