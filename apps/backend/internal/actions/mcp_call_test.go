package actions

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	adaptersmcp "github.com/fauzanebd/argentum/internal/adapters/mcp"
	"github.com/fauzanebd/argentum/internal/domain"
)

// T-M4's executing half: what runs after a human approves, and what it refuses.

type fakeMCPStore struct {
	target MCPTarget
	names  []string
	err    error
	lookup string
}

func (s *fakeMCPStore) FindWriteTool(_ context.Context, name string) (MCPTarget, error) {
	s.lookup = name
	if s.err != nil {
		return MCPTarget{}, s.err
	}
	return s.target, nil
}

func (s *fakeMCPStore) ListWriteToolNames(context.Context) ([]string, error) { return s.names, nil }

type fakeMCPCaller struct {
	gotURL, gotToken, gotTool string
	gotArgs                   map[string]any
	gotMax                    int
	res                       adaptersmcp.CallResult
	err                       error
	calls                     int
}

func (c *fakeMCPCaller) CallTool(
	_ context.Context, url string, _ domain.MCPTransport, token, toolName string,
	args map[string]any, maxBytes int,
) (adaptersmcp.CallResult, error) {
	c.calls++
	c.gotURL, c.gotToken, c.gotTool, c.gotArgs, c.gotMax = url, token, toolName, args, maxBytes
	return c.res, c.err
}

func courierTarget() MCPTarget {
	return MCPTarget{
		ServerID: "srv-1", ServerName: "Kirim Cepat", ToolName: "cancel_shipment",
		URL: "https://courier.example/mcp", Transport: domain.MCPTransportHTTP, Token: "t0k3n",
	}
}

func mcpParams(t *testing.T, tool string, args map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"tool": tool, "arguments": args})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return raw
}

func TestMCPCallValidatesShapeOnly(t *testing.T) {
	a := NewMCPCall(&fakeMCPStore{}, &fakeMCPCaller{}, time.Second, 1024)

	if err := a.Validate(mcpParams(t, "mcp__kirim_cepat__cancel_shipment", map[string]any{"id": "SHP-1"})); err != nil {
		t.Errorf("a well-formed proposal was refused: %v", err)
	}
	if err := a.Validate(json.RawMessage(`{"arguments":{}}`)); err == nil {
		t.Error("a proposal naming no tool was accepted")
	}
	if err := a.Validate(json.RawMessage(`not json`)); err == nil {
		t.Error("unparseable parameters were accepted")
	}
	// A tool with no arguments is a legal call, not a malformed proposal.
	if err := a.Validate(json.RawMessage(`{"tool":"mcp__x__y"}`)); err != nil {
		t.Errorf("an argument-less proposal was refused: %v", err)
	}
}

// The approval card's sentence has to be the payload, not a summary of it: an
// approval is only meaningful against what will actually be sent.
func TestMCPCallDescribesTheLiteralPayload(t *testing.T) {
	a := NewMCPCall(&fakeMCPStore{}, &fakeMCPCaller{}, time.Second, 1024)

	got, err := a.Describe(mcpParams(t, "mcp__kirim_cepat__cancel_shipment", map[string]any{
		"shipment_id": "SHP-1042", "reason": "duplicate order",
	}))
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	for _, want := range []string{"mcp__kirim_cepat__cancel_shipment", `"shipment_id":"SHP-1042"`, `"reason":"duplicate order"`} {
		if !strings.Contains(got, want) {
			t.Errorf("description %q is missing %q", got, want)
		}
	}
	if strings.Contains(got, "…") {
		t.Errorf("description truncated the payload: %q", got)
	}
}

func TestMCPCallDescribesAnArgumentLessCall(t *testing.T) {
	a := NewMCPCall(&fakeMCPStore{}, &fakeMCPCaller{}, time.Second, 1024)

	got, err := a.Describe(json.RawMessage(`{"tool":"mcp__ops__sync_now"}`))
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if !strings.Contains(got, "no arguments") {
		t.Errorf("description = %q, want it to say there are no arguments", got)
	}
}

// Execute sends exactly what was approved, to the server the name resolved to.
func TestMCPCallExecutesAgainstTheResolvedServer(t *testing.T) {
	store := &fakeMCPStore{target: courierTarget()}
	caller := &fakeMCPCaller{res: adaptersmcp.CallResult{Text: "cancelled"}}
	a := NewMCPCall(store, caller, time.Second, 4096)

	raw, err := a.Execute(context.Background(), mcpParams(t, "mcp__kirim_cepat__cancel_shipment", map[string]any{"shipment_id": "SHP-1042"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if caller.calls != 1 {
		t.Fatalf("server calls = %d, want exactly 1", caller.calls)
	}
	if caller.gotTool != "cancel_shipment" {
		t.Errorf("called %q, want the tenant's own tool name", caller.gotTool)
	}
	if caller.gotURL != "https://courier.example/mcp" || caller.gotToken != "t0k3n" {
		t.Errorf("called %q with token %q", caller.gotURL, caller.gotToken)
	}
	if caller.gotArgs["shipment_id"] != "SHP-1042" {
		t.Errorf("arguments sent = %v, want the approved ones", caller.gotArgs)
	}
	if caller.gotMax != 4096 {
		t.Errorf("response cap = %d, want the configured 4096", caller.gotMax)
	}

	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result["server_id"] != "srv-1" || result["result"] != "cancelled" {
		t.Errorf("ledger result = %v", result)
	}
}

// The gates are re-read at execute time, not trusted from propose time: a
// proposal is approvable for a day, and a tool un-approved or re-classified in
// the meantime must not run because it was legal yesterday.
func TestMCPCallRefusesAToolThatIsNoLongerRunnable(t *testing.T) {
	store := &fakeMCPStore{err: domain.ErrNotFound}
	caller := &fakeMCPCaller{}
	a := NewMCPCall(store, caller, time.Second, 1024)

	_, err := a.Execute(context.Background(), mcpParams(t, "mcp__kirim_cepat__cancel_shipment", nil))
	if err == nil {
		t.Fatal("a tool that no longer resolves was executed")
	}
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("err = %v, want an invalid-input error the approver can read", err)
	}
	if caller.calls != 0 {
		t.Errorf("the server was called %d times for an unresolvable tool", caller.calls)
	}
}

// A tool that answers unhappily is a recorded outcome, not a failed execution —
// the same line http_action draws on a 4xx.
func TestMCPCallRecordsAToolError(t *testing.T) {
	store := &fakeMCPStore{target: courierTarget()}
	caller := &fakeMCPCaller{res: adaptersmcp.CallResult{IsError: true, Text: "shipment already delivered"}}
	a := NewMCPCall(store, caller, time.Second, 1024)

	raw, err := a.Execute(context.Background(), mcpParams(t, "mcp__kirim_cepat__cancel_shipment", nil))
	if err != nil {
		t.Fatalf("a business error failed the execution: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result["is_error"] != true || result["result"] != "shipment already delivered" {
		t.Errorf("ledger result = %v, want the tool's own error recorded", result)
	}
}

// A transport failure is a failed execution: nothing happened on the far end
// that the ledger should record as done.
func TestMCPCallFailsOnATransportError(t *testing.T) {
	store := &fakeMCPStore{target: courierTarget()}
	caller := &fakeMCPCaller{err: errors.New("egress blocked: 10.0.0.5 is a private address")}
	a := NewMCPCall(store, caller, time.Second, 1024)

	if _, err := a.Execute(context.Background(), mcpParams(t, "mcp__kirim_cepat__cancel_shipment", nil)); err == nil {
		t.Fatal("a refused call was recorded as a success")
	}
}

func TestMCPCallTurnOptionsListsWriteTools(t *testing.T) {
	store := &fakeMCPStore{names: []string{"mcp__kirim_cepat__cancel_shipment"}}
	a := NewMCPCall(store, &fakeMCPCaller{}, time.Second, 1024)

	got, err := a.TurnOptions(context.Background())
	if err != nil {
		t.Fatalf("TurnOptions: %v", err)
	}
	if len(got) != 1 || got[0] != "mcp__kirim_cepat__cancel_shipment" {
		t.Errorf("options = %v", got)
	}
}

// The registry resolves the kind by the same constant the proposing tool names.
// Two spellings would be a proposal nothing can execute.
func TestMCPCallKindIsRegistrable(t *testing.T) {
	a := NewMCPCall(&fakeMCPStore{}, &fakeMCPCaller{}, time.Second, 1024)
	r := NewRegistry(a)

	got, ok := r.Get(MCPCallKind)
	if !ok {
		t.Fatalf("registry does not hold %q; it holds %v", MCPCallKind, r.Kinds())
	}
	if got.Kind() != "mcp_call" {
		t.Errorf("kind = %q", got.Kind())
	}
}
