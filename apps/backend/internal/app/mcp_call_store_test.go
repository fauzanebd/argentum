package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// The gate T-M4 runs at approval time. Everything here is the inverse of the
// read path's: a tool is runnable through mcp_call only if it is approved, NOT
// read-only, not drifted, and on an enabled server the company owns.

type stubMCPRepo struct {
	domain.MCPServerRepository // unused methods panic if ever called

	servers map[string][]*domain.MCPServer
	tools   map[string][]*domain.MCPServerTool
	err     error
}

func (r *stubMCPRepo) ListByCompany(_ context.Context, companyID string) ([]*domain.MCPServer, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.servers[companyID], nil
}

func (r *stubMCPRepo) ListTools(_ context.Context, serverID string) ([]*domain.MCPServerTool, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.tools[serverID], nil
}

type stubMCPCipher struct{}

func (stubMCPCipher) Encrypt(plain string) ([]byte, error) { return []byte(plain), nil }
func (stubMCPCipher) Decrypt(blob []byte) (string, error)  { return "decrypted-" + string(blob), nil }

func reviewedTool(name string, approved, readOnly bool) *domain.MCPServerTool {
	desc := "does " + name
	schema := json.RawMessage(`{"type":"object"}`)
	t := &domain.MCPServerTool{
		ID: name, ToolName: name, Description: desc, InputSchema: schema,
		Approved: approved, ReadOnly: readOnly, DiscoveredAt: time.Now(),
	}
	if approved {
		t.ApprovedDigest = domain.MCPToolDigest(desc, schema)
	}
	return t
}

func courierRepo() *stubMCPRepo {
	return &stubMCPRepo{
		servers: map[string][]*domain.MCPServer{
			"co-1": {
				{ID: "srv-1", CompanyID: "co-1", Name: "Kirim Cepat", URL: "https://courier.example",
					Transport: domain.MCPTransportHTTP, Enabled: true, AuthEncrypted: []byte("sealed")},
				{ID: "srv-off", CompanyID: "co-1", Name: "Old Courier", URL: "https://old.example",
					Transport: domain.MCPTransportHTTP, Enabled: false},
			},
		},
		tools: map[string][]*domain.MCPServerTool{
			"srv-1": {
				reviewedTool("quote_shipping", true, true),   // read — not ours
				reviewedTool("cancel_shipment", true, false), // the one
				reviewedTool("purge_account", false, false),  // unapproved write
			},
			"srv-off": {reviewedTool("legacy_cancel", true, false)},
		},
	}
}

func storeCtx() context.Context {
	return tenantctx.WithCompanyID(context.Background(), "co-1")
}

func TestFindWriteToolResolvesAnApprovedWrite(t *testing.T) {
	s := NewMCPCallStore(courierRepo(), stubMCPCipher{})

	got, err := s.FindWriteTool(storeCtx(), "mcp__kirim_cepat__cancel_shipment")
	if err != nil {
		t.Fatalf("FindWriteTool: %v", err)
	}
	if got.ServerID != "srv-1" || got.ToolName != "cancel_shipment" {
		t.Errorf("resolved %+v", got)
	}
	if got.Token != "decrypted-sealed" {
		t.Errorf("token = %q, want the decrypted one", got.Token)
	}
}

// Each gate, one at a time, all through the public method — the point is that
// none of them can be skipped by naming the tool directly.
func TestFindWriteToolRefusesEverythingElse(t *testing.T) {
	s := NewMCPCallStore(courierRepo(), stubMCPCipher{})

	cases := map[string]string{
		"a read-only tool is not this action's":      "mcp__kirim_cepat__quote_shipping",
		"an unapproved write is not runnable":        "mcp__kirim_cepat__purge_account",
		"a tool on a disabled server is unreachable": "mcp__old_courier__legacy_cancel",
		"a name nobody registered":                   "mcp__kirim_cepat__invent_a_tool",
	}
	for why, name := range cases {
		if _, err := s.FindWriteTool(storeCtx(), name); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", why, err)
		}
	}
}

// Drift is the gate that catches a server rewriting a tool after an admin
// approved it — the text that reached the human is not the text on the server.
func TestFindWriteToolRefusesADriftedTool(t *testing.T) {
	repo := courierRepo()
	repo.tools["srv-1"][1].ApprovedDigest = "digest-from-before-the-description-changed"
	s := NewMCPCallStore(repo, stubMCPCipher{})

	if _, err := s.FindWriteTool(storeCtx(), "mcp__kirim_cepat__cancel_shipment"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a drifted tool resolved: %v", err)
	}
}

// No tenant on the context is not "look in every company" — it is nothing
// found. The approval endpoint always sets one; a caller that does not has no
// business resolving a tool.
func TestFindWriteToolNeedsATenant(t *testing.T) {
	s := NewMCPCallStore(courierRepo(), stubMCPCipher{})

	if _, err := s.FindWriteTool(context.Background(), "mcp__kirim_cepat__cancel_shipment"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound with no tenant on the context", err)
	}
}

// A server whose token cannot be read is not called unauthenticated.
func TestFindWriteToolFailsRatherThanCallWithoutAToken(t *testing.T) {
	s := NewMCPCallStore(courierRepo(), nil)

	_, err := s.FindWriteTool(storeCtx(), "mcp__kirim_cepat__cancel_shipment")
	if err == nil || errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want the missing-cipher wiring error", err)
	}
}

func TestListWriteToolNamesOffersOnlyRunnableWrites(t *testing.T) {
	s := NewMCPCallStore(courierRepo(), stubMCPCipher{})

	got, err := s.ListWriteToolNames(storeCtx())
	if err != nil {
		t.Fatalf("ListWriteToolNames: %v", err)
	}
	if len(got) != 1 || got[0] != "mcp__kirim_cepat__cancel_shipment" {
		t.Errorf("names = %v, want the one approved write", got)
	}
}
