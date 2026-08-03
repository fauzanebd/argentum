package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// The roster's tool picker, and the silent un-scoping it used to cause (T-M3
// follow-up).
//
// `/api/agents` built the picker from the static registry alone, so no checkbox
// existed for a namespaced MCP name — while `filterTools` applies an agent's
// allowlist to the *combined* slice. An admin who narrowed an agent's tools in
// the dashboard therefore saved a list with no MCP name in it and un-scoped that
// agent from every MCP tool it was bound to, silently. The API half already
// worked, which is what made it invisible: a namespaced name in `allowed_tools`
// was accepted, stored, and called.

type fakeMCPLister struct {
	servers   []*domain.MCPServer
	tools     map[string][]*domain.MCPServerTool
	listErr   error
	toolsErr  error
	listCalls int
}

func (f *fakeMCPLister) ListByCompany(_ context.Context, companyID string) ([]*domain.MCPServer, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []*domain.MCPServer
	for _, s := range f.servers {
		if s.CompanyID == companyID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeMCPLister) ListTools(_ context.Context, serverID string) ([]*domain.MCPServerTool, error) {
	if f.toolsErr != nil {
		return nil, f.toolsErr
	}
	return f.tools[serverID], nil
}

// approvedTool is a tool an admin reviewed and approved, with a digest that
// matches its own text — the shape Drifted() reports false for.
func approvedTool(serverID, name, desc string) *domain.MCPServerTool {
	schema := json.RawMessage(`{"type":"object"}`)
	return &domain.MCPServerTool{
		ServerID: serverID, ToolName: name, Description: desc, InputSchema: schema,
		ReadOnly: true, Approved: true,
		ApprovedDigest: domain.MCPToolDigest(desc, schema),
	}
}

func toolNames(opts []AgentToolOption) []string {
	out := make([]string, 0, len(opts))
	for _, o := range opts {
		out = append(out, o.Name)
	}
	return out
}

func TestCompanyToolOptionsOffersTheTenantsOwnTools(t *testing.T) {
	svc, _, _ := newAgentFixture()
	mcp := &fakeMCPLister{
		servers: []*domain.MCPServer{{ID: "srv-1", CompanyID: companyA, Name: "Kirim Cepat", Enabled: true}},
		tools: map[string][]*domain.MCPServerTool{
			"srv-1": {approvedTool("srv-1", "quote_shipping", "Quote a shipment. Takes an origin and a destination.")},
		},
	}
	svc = svc.WithMCPServers(mcp)

	opts := svc.CompanyToolOptions(context.Background(), companyA)
	names := toolNames(opts)

	if len(names) != len(registry)+1 {
		t.Fatalf("options = %v, want the registry plus one MCP tool", names)
	}
	last := opts[len(opts)-1]
	if last.Name != "mcp__kirim_cepat__quote_shipping" {
		t.Errorf("name = %q, want the name the turn will dispatch on", last.Name)
	}
	if last.MCPServerName != "Kirim Cepat" {
		t.Errorf("server name = %q, want it carried so the form can group by it", last.MCPServerName)
	}
	// The name the picker offers has to be one normalizeTools accepts, or
	// ticking the box produces a 400 on save.
	if _, err := svc.normalizeTools([]string{last.Name}); err != nil {
		t.Errorf("the picker offered a name the service refuses: %v", err)
	}
}

// The same three gates the turn-time provider applies. A checkbox for a tool
// the turn would refuse to build is a checkbox that scopes an agent to nothing.
func TestCompanyToolOptionsAppliesTheSameGates(t *testing.T) {
	unapproved := approvedTool("srv-1", "unapproved", "x")
	unapproved.Approved = false
	write := approvedTool("srv-1", "cancel_shipment", "Cancel it.")
	write.ReadOnly = false
	drifted := approvedTool("srv-1", "drifted", "as approved")
	drifted.Description = "rewritten after approval"

	svc, _, _ := newAgentFixture()
	svc = svc.WithMCPServers(&fakeMCPLister{
		servers: []*domain.MCPServer{
			{ID: "srv-1", CompanyID: companyA, Name: "Helpdesk", Enabled: true},
			{ID: "srv-off", CompanyID: companyA, Name: "Disabled", Enabled: false},
		},
		tools: map[string][]*domain.MCPServerTool{
			"srv-1":   {unapproved, write, drifted},
			"srv-off": {approvedTool("srv-off", "search", "Search.")},
		},
	})

	names := toolNames(svc.CompanyToolOptions(context.Background(), companyA))
	for _, unwanted := range []string{"unapproved", "cancel_shipment", "drifted", "search"} {
		for _, got := range names {
			if got == "mcp__helpdesk__"+unwanted || got == "mcp__disabled__"+unwanted {
				t.Errorf("%q is offered; it should be gated out", got)
			}
		}
	}
	if len(names) != len(registry) {
		t.Errorf("options = %v, want only the static registry", names)
	}
}

// A tenant's servers are their own. Another company's tools are not offered.
func TestCompanyToolOptionsIsCompanyScoped(t *testing.T) {
	svc, _, _ := newAgentFixture()
	svc = svc.WithMCPServers(&fakeMCPLister{
		servers: []*domain.MCPServer{{ID: "srv-b", CompanyID: companyB, Name: "Theirs", Enabled: true}},
		tools: map[string][]*domain.MCPServerTool{
			"srv-b": {approvedTool("srv-b", "search", "Search.")},
		},
	})

	if got := toolNames(svc.CompanyToolOptions(context.Background(), companyA)); len(got) != len(registry) {
		t.Errorf("company A sees %v, want only the static registry", got)
	}
}

// Losing the MCP checkboxes is bad; failing the roster screen because an MCP
// read failed is worse. Both failure paths degrade to what the picker showed
// before this existed.
func TestCompanyToolOptionsDegradesWhenMCPReadsFail(t *testing.T) {
	t.Run("server list fails", func(t *testing.T) {
		svc, _, _ := newAgentFixture()
		svc = svc.WithMCPServers(&fakeMCPLister{listErr: errors.New("control database is down")})
		if got := toolNames(svc.CompanyToolOptions(context.Background(), companyA)); len(got) != len(registry) {
			t.Errorf("options = %v, want the static registry", got)
		}
	})

	t.Run("tool list fails", func(t *testing.T) {
		svc, _, _ := newAgentFixture()
		svc = svc.WithMCPServers(&fakeMCPLister{
			servers:  []*domain.MCPServer{{ID: "srv-1", CompanyID: companyA, Name: "Helpdesk", Enabled: true}},
			toolsErr: errors.New("timeout"),
		})
		if got := toolNames(svc.CompanyToolOptions(context.Background(), companyA)); len(got) != len(registry) {
			t.Errorf("options = %v, want the static registry", got)
		}
	})

	t.Run("no lister wired", func(t *testing.T) {
		svc, _, _ := newAgentFixture()
		if got := toolNames(svc.CompanyToolOptions(context.Background(), companyA)); len(got) != len(registry) {
			t.Errorf("options = %v, want the static registry", got)
		}
	})
}
