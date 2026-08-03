package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	adaptersmcp "github.com/fauzanebd/argentum/internal/adapters/mcp"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// recordingCaller captures what the tool asked the server to run, and answers
// with whatever the test set.
type recordingCaller struct {
	gotURL   string
	gotName  string
	gotArgs  map[string]any
	gotToken string
	gotMax   int
	result   adaptersmcp.CallResult
	err      error
	calls    int
}

func (c *recordingCaller) CallTool(
	_ context.Context, url string, _ domain.MCPTransport, token, toolName string,
	args map[string]any, maxBytes int,
) (adaptersmcp.CallResult, error) {
	c.calls++
	c.gotURL, c.gotName, c.gotArgs, c.gotToken, c.gotMax = url, toolName, args, token, maxBytes
	return c.result, c.err
}

// meteredCall is one usage row the tool asked for.
type meteredCall struct{ companyID, threadID, serverID, toolName string }

type recordingMeter struct{ calls []meteredCall }

func (m *recordingMeter) RecordMCPCall(_ context.Context, companyID, threadID, serverID, toolName string) {
	m.calls = append(m.calls, meteredCall{companyID, threadID, serverID, toolName})
}

func newTool(caller Caller, guard *callGuard) *Tool {
	return newMeteredTool(caller, guard, &recordingMeter{})
}

func newMeteredTool(caller Caller, guard *callGuard, meter Meter) *Tool {
	return &Tool{
		serverID:  "srv-1",
		rawName:   "search_tickets",
		name:      "mcp__helpdesk__search_tickets",
		desc:      "search",
		caller:    caller,
		meter:     meter,
		url:       "https://mcp.example.com",
		transport: domain.MCPTransportHTTP,
		token:     "tok",
		timeout:   time.Second,
		maxBytes:  1024,
		calls:     guard,
	}
}

// The happy path: the model's JSON becomes the server's arguments, the tenant's
// own name for the tool is what is sent (not the namespaced one), and the text
// comes back verbatim.
func TestExecutePassesRawNameAndArguments(t *testing.T) {
	caller := &recordingCaller{result: adaptersmcp.CallResult{Text: `{"tickets":2}`}}
	tool := newTool(caller, newCallGuard(5))

	out, err := tool.Execute(context.Background(), `{"query":"printer"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != `{"tickets":2}` {
		t.Errorf("out = %q, want the server's text", out)
	}
	if caller.gotName != "search_tickets" {
		t.Errorf("sent tool name %q, want the tenant's raw name, not the namespaced one", caller.gotName)
	}
	if caller.gotArgs["query"] != "printer" {
		t.Errorf("args = %v, want the model's arguments", caller.gotArgs)
	}
	if caller.gotToken != "tok" || caller.gotMax != 1024 {
		t.Errorf("token/max = %q/%d, want the tool's own", caller.gotToken, caller.gotMax)
	}
}

// A tenant tool's own error comes back as a result the model reads, not a Go
// error that fails the turn — but it is marked so it does not read as success.
func TestExecuteSurfacesToolError(t *testing.T) {
	caller := &recordingCaller{result: adaptersmcp.CallResult{Text: "no such ticket", IsError: true}}
	out, err := newTool(caller, newCallGuard(5)).Execute(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("a tool-reported error must not fail the turn: %v", err)
	}
	if !strings.HasPrefix(out, "[tool error]") || !strings.Contains(out, "no such ticket") {
		t.Errorf("out = %q, want a marked tool error carrying the message", out)
	}
}

// A transport failure — a timeout, a 500, an oversized result — is a Go error,
// which the SDK feeds back to the model as a tool result so the agent recovers.
func TestExecuteReturnsErrorOnCallerFailure(t *testing.T) {
	caller := &recordingCaller{err: errors.New("call tool: connect: dial timeout")}
	_, err := newTool(caller, newCallGuard(5)).Execute(context.Background(), `{}`)
	if err == nil {
		t.Fatal("want an error the agent recovers from")
	}
}

// The per-turn call cap is shared across a turn's MCP tools and refuses before
// the call goes out, so a tight loop cannot spend the turn on round trips.
func TestExecuteHonoursTheSharedCallCap(t *testing.T) {
	guard := newCallGuard(1)
	caller := &recordingCaller{result: adaptersmcp.CallResult{Text: "ok"}}
	a, b := newTool(caller, guard), newTool(caller, guard)

	if _, err := a.Execute(context.Background(), `{}`); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := b.Execute(context.Background(), `{}`); err == nil {
		t.Fatal("second call should be refused: the turn's MCP call budget is spent")
	}
	if caller.calls != 1 {
		t.Errorf("caller.calls = %d, want 1 — the refused call must not reach the server", caller.calls)
	}
}

// The meter is the half T-M2 asked for and did not ship: the audit log had the
// call and `usage_events` did not, so an MCP-heavy tenant's spend was invisible
// to the dashboard, to GET /v1/usage and to the credit balance. One row per
// completed round trip, carrying the server and the tenant's own name for the
// tool.
func TestExecuteMetersACompletedCall(t *testing.T) {
	meter := &recordingMeter{}
	caller := &recordingCaller{result: adaptersmcp.CallResult{Text: `{"tickets":2}`}}
	ctx := tenantctx.WithCompanyID(context.Background(), "co-1")
	ctx = tenantctx.WithThreadID(ctx, "th-9")

	if _, err := newMeteredTool(caller, newCallGuard(5), meter).Execute(ctx, `{}`); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if len(meter.calls) != 1 {
		t.Fatalf("meter.calls = %d, want 1", len(meter.calls))
	}
	want := meteredCall{companyID: "co-1", threadID: "th-9", serverID: "srv-1", toolName: "search_tickets"}
	if meter.calls[0] != want {
		t.Errorf("metered %+v, want %+v", meter.calls[0], want)
	}
}

// A tenant tool's own business error still cost a round trip and still occupies
// the turn's context, so it is metered. A transport failure and a call the
// budget refused are not — the same line run_sql draws between a query that ran
// and one that could not.
func TestExecuteMetersOnlyWhatReachedTheServer(t *testing.T) {
	t.Run("tool error is metered", func(t *testing.T) {
		meter := &recordingMeter{}
		caller := &recordingCaller{result: adaptersmcp.CallResult{Text: "no such ticket", IsError: true}}
		if _, err := newMeteredTool(caller, newCallGuard(5), meter).Execute(context.Background(), `{}`); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if len(meter.calls) != 1 {
			t.Errorf("meter.calls = %d, want 1 — the server answered", len(meter.calls))
		}
	})

	t.Run("transport failure is not metered", func(t *testing.T) {
		meter := &recordingMeter{}
		caller := &recordingCaller{err: errors.New("call tool: connect: dial timeout")}
		if _, err := newMeteredTool(caller, newCallGuard(5), meter).Execute(context.Background(), `{}`); err == nil {
			t.Fatal("want an error")
		}
		if len(meter.calls) != 0 {
			t.Errorf("meter.calls = %d, want 0 — nothing reached the server", len(meter.calls))
		}
	})

	t.Run("a refused call is not metered", func(t *testing.T) {
		meter := &recordingMeter{}
		caller := &recordingCaller{result: adaptersmcp.CallResult{Text: "ok"}}
		tool := newMeteredTool(caller, newCallGuard(0), meter)
		// newCallGuard(0) disables the cap, so spend a real one instead.
		tool.calls = &callGuard{remaining: 0}
		if _, err := tool.Execute(context.Background(), `{}`); err == nil {
			t.Fatal("want a refusal")
		}
		if len(meter.calls) != 0 {
			t.Errorf("meter.calls = %d, want 0 — the call never went out", len(meter.calls))
		}
	})
}

// An empty argument string is a call with no arguments, which is legal.
func TestExecuteEmptyInputIsNoArgs(t *testing.T) {
	caller := &recordingCaller{result: adaptersmcp.CallResult{Text: "ok"}}
	if _, err := newTool(caller, newCallGuard(5)).Execute(context.Background(), "  "); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if caller.gotArgs == nil || len(caller.gotArgs) != 0 {
		t.Errorf("args = %v, want an empty map", caller.gotArgs)
	}
}

func TestSlugAndNamespacing(t *testing.T) {
	cases := map[string]string{
		"Helpdesk":     "helpdesk",
		"My CRM v2":    "my_crm_v2",
		"  weird!!  ":  "weird",
		"---":          "x",
		"ALL_CAPS-Ok!": "all_caps-ok",
	}
	for in, want := range cases {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}

	// The reserved prefix is what stops a tenant tool from shadowing ours: a
	// tenant tool literally called run_sql namespaces to something that cannot
	// collide with our run_sql.
	name := namespaced("Helpdesk", "run_sql")
	if name != "mcp__helpdesk__run_sql" {
		t.Errorf("namespaced = %q", name)
	}
	if !strings.HasPrefix(name, NamePrefix) || name == "run_sql" {
		t.Errorf("a tenant run_sql must not shadow ours; got %q", name)
	}
	if len(namespaced(strings.Repeat("x", 200), strings.Repeat("y", 200))) > maxNameLen {
		t.Error("namespaced name must be clipped to the provider ceiling")
	}
}

func TestParamsFromSchema(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"query":{"type":"string","description":"search text"},
			"limit":{"type":"integer","default":10},
			"status":{"type":"string","enum":["open","closed"]},
			"tags":{"type":"array","items":{"type":"string"}}
		},
		"required":["query"]
	}`)
	params := paramsFromSchema(schema)

	if len(params) != 4 {
		t.Fatalf("params = %d, want 4", len(params))
	}
	if !params["query"].Required {
		t.Error("query should be required")
	}
	if params["limit"].Required {
		t.Error("limit should not be required")
	}
	if params["query"].Description != "search text" {
		t.Errorf("query description = %q", params["query"].Description)
	}
	if len(params["status"].Enum) != 2 {
		t.Errorf("status enum = %v, want two values", params["status"].Enum)
	}
	if params["tags"].Items == nil {
		t.Error("array parameter should carry its item type")
	}
	// A missing or unparseable schema degrades to no parameters rather than
	// failing to register the tool.
	if len(paramsFromSchema(nil)) != 0 || len(paramsFromSchema(json.RawMessage(`not json`))) != 0 {
		t.Error("a bad schema should yield no parameters, not a panic")
	}
}
