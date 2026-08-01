package mcptools

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	adaptersmcp "github.com/fauzanebd/argentum/internal/adapters/mcp"
	"github.com/fauzanebd/argentum/internal/agentbudget"
	"github.com/fauzanebd/argentum/internal/agentscope"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

type fakeStore struct {
	servers map[string][]*domain.MCPServer     // companyID -> servers
	tools   map[string][]*domain.MCPServerTool // serverID -> tools
}

func (s *fakeStore) ListByCompany(_ context.Context, companyID string) ([]*domain.MCPServer, error) {
	return s.servers[companyID], nil
}
func (s *fakeStore) ListTools(_ context.Context, serverID string) ([]*domain.MCPServerTool, error) {
	return s.tools[serverID], nil
}

type fakeCipher struct{}

func (fakeCipher) Decrypt(b []byte) (string, error) { return "decrypted-" + string(b), nil }

type fakeRecorder struct {
	mu   sync.Mutex
	rows []*domain.AgentAction
}

func (r *fakeRecorder) Create(_ context.Context, a *domain.AgentAction) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, a)
	return nil
}

// approvedTool builds a discovered tool with a digest that matches its own text,
// so it is not drifted. read_only and approved as told.
func approvedTool(id, name string, approved, readOnly bool) *domain.MCPServerTool {
	desc := "does " + name
	schema := json.RawMessage(`{"type":"object"}`)
	t := &domain.MCPServerTool{
		ID: id, ToolName: name, Description: desc, InputSchema: schema,
		Approved: approved, ReadOnly: readOnly,
	}
	if approved {
		t.ApprovedDigest = domain.MCPToolDigest(desc, schema)
	}
	return t
}

// driftedTool is approved and read-only but its text no longer matches the
// digest it was approved under.
func driftedTool(id, name string) *domain.MCPServerTool {
	t := approvedTool(id, name, true, true)
	t.ApprovedDigest = "stale-hash-from-before-the-description-changed"
	return t
}

func toolNames(list []interfaces.Tool) []string {
	out := make([]string, 0, len(list))
	for _, t := range list {
		out = append(out, t.Name())
	}
	sort.Strings(out)
	return out
}

func newSource(store ServerStore, caller Caller, rec *fakeRecorder) *Source {
	return NewSource(store, fakeCipher{}, caller, rec, Caps{
		CallTimeout: time.Second, MaxResponseBytes: 1024, MaxCallsPerTurn: 10,
	})
}

// The empty-binding fast path: a turn with no MCP scope, and a turn whose scope
// carries no server ids, both get nil — byte-for-byte the tool list before this
// ticket. This is what keeps the eval harness and every unscoped turn unchanged.
func TestCompanyToolsEmptyScopeReturnsNil(t *testing.T) {
	src := newSource(&fakeStore{}, &recordingCaller{}, &fakeRecorder{})

	if got := src.CompanyTools(context.Background(), "co-1"); got != nil {
		t.Errorf("unscoped turn got %d tools, want nil", len(got))
	}
	ctx := agentscope.WithScope(context.Background(), agentscope.Scope{AgentID: "a1"})
	if got := src.CompanyTools(ctx, "co-1"); got != nil {
		t.Errorf("empty-binding turn got %d tools, want nil", len(got))
	}
}

// The heart of the ticket: only an approved, read-only, non-drifted tool on an
// enabled, bound server is offered. An unapproved one, a write one, a drifted
// one, a tool on a disabled server, and every tool on an unbound server are all
// absent.
func TestCompanyToolsFiltersByScopeAndReview(t *testing.T) {
	store := &fakeStore{
		servers: map[string][]*domain.MCPServer{
			"co-1": {
				{ID: "srv-bound", CompanyID: "co-1", Name: "Helpdesk", URL: "https://a", Transport: domain.MCPTransportHTTP, Enabled: true},
				{ID: "srv-disabled", CompanyID: "co-1", Name: "Old", URL: "https://b", Transport: domain.MCPTransportHTTP, Enabled: false},
				{ID: "srv-unbound", CompanyID: "co-1", Name: "CRM", URL: "https://c", Transport: domain.MCPTransportHTTP, Enabled: true},
			},
		},
		tools: map[string][]*domain.MCPServerTool{
			"srv-bound": {
				approvedTool("t1", "search_tickets", true, true), // offered
				approvedTool("t2", "close_ticket", false, false), // unapproved
				approvedTool("t3", "delete_ticket", true, false), // approved but not read-only
				driftedTool("t4", "list_agents"),                 // drifted
			},
			"srv-disabled": {approvedTool("t5", "legacy", true, true)},
			"srv-unbound":  {approvedTool("t6", "lookup", true, true)},
		},
	}
	src := newSource(store, &recordingCaller{}, &fakeRecorder{})

	// Bind only the enabled Helpdesk and the disabled Old server. The unbound
	// CRM is not in scope; the disabled Old is in scope but off.
	ctx := agentscope.WithScope(context.Background(), agentscope.Scope{
		AgentID: "a1", MCPServerIDs: []string{"srv-bound", "srv-disabled"},
	})

	got := toolNames(src.CompanyTools(ctx, "co-1"))
	want := []string{"mcp__helpdesk__search_tickets"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("offered tools = %v, want %v", got, want)
	}
}

// A returned tool is wrapped exactly as the static registry is: executing it
// writes an audit row naming the server (audit decorator present) and the token
// reaches the caller decrypted (the provider read it). Together they prove the
// tool is a real, bounded, audited call and not a bare closure.
func TestCompanyToolsReturnsWrappedAuditedTools(t *testing.T) {
	store := &fakeStore{
		servers: map[string][]*domain.MCPServer{
			"co-1": {{
				ID: "srv-9", CompanyID: "co-1", Name: "Helpdesk", URL: "https://a",
				Transport: domain.MCPTransportHTTP, Enabled: true, AuthEncrypted: []byte("blob"),
			}},
		},
		tools: map[string][]*domain.MCPServerTool{
			"srv-9": {approvedTool("t1", "search_tickets", true, true)},
		},
	}
	caller := &recordingCaller{result: adaptersmcp.CallResult{Text: "ok"}}
	rec := &fakeRecorder{}
	src := newSource(store, caller, rec)

	ctx := agentscope.WithScope(tenantctx.WithCompanyID(context.Background(), "co-1"),
		agentscope.Scope{AgentID: "a1", MCPServerIDs: []string{"srv-9"}})
	ctx = agentbudget.WithTracker(ctx, agentbudget.New(agentbudget.Default()))

	list := src.CompanyTools(ctx, "co-1")
	if len(list) != 1 {
		t.Fatalf("tools = %d, want 1", len(list))
	}
	if _, err := list[0].Execute(ctx, `{"query":"x"}`); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if caller.gotToken != "decrypted-blob" {
		t.Errorf("caller token = %q, want the provider to have decrypted it", caller.gotToken)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.rows) != 1 {
		t.Fatalf("audit rows = %d, want 1 (the tool is audited)", len(rec.rows))
	}
	if rec.rows[0].MCPServerID != "srv-9" {
		t.Errorf("audit mcp_server_id = %q, want srv-9", rec.rows[0].MCPServerID)
	}
}

// The budget guard is the inner wrapper: with the turn's tool-call budget
// exhausted, the tool refuses before the call goes out and never reaches the
// server. This is T-M2's "refused before the MCP call goes out" acceptance.
func TestCompanyToolsAreBudgetGuarded(t *testing.T) {
	store := &fakeStore{
		servers: map[string][]*domain.MCPServer{
			"co-1": {{ID: "srv-9", CompanyID: "co-1", Name: "Helpdesk", URL: "https://a", Transport: domain.MCPTransportHTTP, Enabled: true}},
		},
		tools: map[string][]*domain.MCPServerTool{"srv-9": {approvedTool("t1", "search_tickets", true, true)}},
	}
	caller := &recordingCaller{result: adaptersmcp.CallResult{Text: "ok"}}
	src := newSource(store, caller, &fakeRecorder{})

	ctx := agentscope.WithScope(tenantctx.WithCompanyID(context.Background(), "co-1"),
		agentscope.Scope{AgentID: "a1", MCPServerIDs: []string{"srv-9"}})
	// A budget of one tool call: the first spends it, the second is refused by
	// the guard before the call goes out.
	ctx = agentbudget.WithTracker(ctx, agentbudget.New(agentbudget.Budget{
		MaxToolCalls: 1, MaxIterations: 8, MaxTokens: 1_000_000, Wall: time.Hour,
	}))

	tool := src.CompanyTools(ctx, "co-1")[0]
	if _, err := tool.Execute(ctx, `{}`); err != nil {
		t.Fatalf("first call: %v", err)
	}
	out, err := tool.Execute(ctx, `{}`)
	if err != nil {
		t.Fatalf("a refusal is a result, not an error: %v", err)
	}
	if !agentbudget.IsRefusal(out) {
		t.Errorf("out = %q, want a budget refusal on the second call", out)
	}
	if caller.calls != 1 {
		t.Errorf("caller.calls = %d, want 1 — the refused call must not reach the server", caller.calls)
	}
}
