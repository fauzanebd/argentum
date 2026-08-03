package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/actions"
	"github.com/fauzanebd/argentum/internal/agentscope"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tools"
)

// T-M4 at the turn: a write tool is offered, and calling it proposes.
//
// The property that carries the ticket is negative and has to be asserted as
// one: the tenant's server is not reached. Everything else — the proposal's
// parameters, the sentence the model relays — is downstream of that.

type recordingProposer struct {
	gotKind   string
	gotParams json.RawMessage
	calls     int
	err       error
}

func (p *recordingProposer) ProposeAction(_ context.Context, in tools.ProposeActionInput) (*tools.ProposeActionResult, error) {
	p.calls++
	p.gotKind, p.gotParams = in.Kind, in.Params
	if p.err != nil {
		return nil, p.err
	}
	return &tools.ProposeActionResult{
		InvocationID:     "inv-1",
		ActionKind:       in.Kind,
		Status:           string(domain.InvocationProposed),
		RequiresApproval: true,
		Message:          "I've proposed this action and it needs approval before it runs.",
	}, nil
}

func writeToolStore() *fakeStore {
	return &fakeStore{
		servers: map[string][]*domain.MCPServer{
			"co-1": {{
				ID: "srv-1", CompanyID: "co-1", Name: "Kirim Cepat",
				URL: "https://courier.example", Transport: domain.MCPTransportHTTP, Enabled: true,
			}},
		},
		tools: map[string][]*domain.MCPServerTool{
			"srv-1": {
				approvedTool("t1", "quote_shipping", true, true),   // read
				approvedTool("t2", "cancel_shipment", true, false), // write
			},
		},
	}
}

// byName picks one tool out of a turn's list. The list is wrapped — budget
// guard, then audit decorator — so this is also the only way a test reaches the
// tool the model would actually dispatch on.
func byName(t *testing.T, list []interfaces.Tool, name string) interfaces.Tool {
	t.Helper()
	for _, tool := range list {
		if tool.Name() == name {
			return tool
		}
	}
	t.Fatalf("no tool named %q in %v", name, toolNames(list))
	return nil
}

func boundCtx() context.Context {
	return agentscope.WithScope(context.Background(), agentscope.Scope{
		AgentID: "a1", MCPServerIDs: []string{"srv-1"},
	})
}

// Without a proposer the write tool is withheld — the behaviour the 2026-08-02
// gate photographed, and the correct one for a deployment with no action
// framework. This is the fallback T-M4 must not turn into a hole.
func TestAWriteToolIsWithheldWithoutAProposer(t *testing.T) {
	src := newSource(writeToolStore(), &recordingCaller{}, &fakeRecorder{})

	got := toolNames(src.CompanyTools(boundCtx(), "co-1"))
	if len(got) != 1 || got[0] != "mcp__kirim_cepat__quote_shipping" {
		t.Errorf("offered tools = %v, want the read tool alone", got)
	}
}

func TestAWriteToolIsOfferedWithAProposer(t *testing.T) {
	src := newSource(writeToolStore(), &recordingCaller{}, &fakeRecorder{}).WithProposer(&recordingProposer{})

	got := toolNames(src.CompanyTools(boundCtx(), "co-1"))
	want := []string{"mcp__kirim_cepat__cancel_shipment", "mcp__kirim_cepat__quote_shipping"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("offered tools = %v, want %v", got, want)
	}
}

// The ticket, as one assertion: calling the write tool records a proposal and
// does not reach the tenant's server.
func TestCallingAWriteToolProposesInsteadOfCalling(t *testing.T) {
	caller := &recordingCaller{}
	proposer := &recordingProposer{}
	src := newSource(writeToolStore(), caller, &fakeRecorder{}).WithProposer(proposer)

	tool := byName(t, src.CompanyTools(boundCtx(), "co-1"), "mcp__kirim_cepat__cancel_shipment")
	out, err := tool.Run(context.Background(), `{"shipment_id":"SHP-1042","reason":"duplicate"}`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if caller.calls != 0 {
		t.Errorf("the tenant's server was called %d times; a write tool must only propose", caller.calls)
	}
	if proposer.calls != 1 {
		t.Fatalf("proposals recorded = %d, want 1", proposer.calls)
	}
	if proposer.gotKind != actions.MCPCallKind {
		t.Errorf("proposed kind = %q, want %q", proposer.gotKind, actions.MCPCallKind)
	}
	if !strings.Contains(out, "needs approval") {
		t.Errorf("the model was told %q, which does not say a human has to approve", out)
	}

	// The proposal names the tool by the same namespaced name the model called,
	// and carries the arguments verbatim — the approval card renders these, and
	// the executor runs off them, so this is where "what was approved is what is
	// sent" starts.
	var got struct {
		Tool      string         `json:"tool"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(proposer.gotParams, &got); err != nil {
		t.Fatalf("decode proposal params: %v (%s)", err, proposer.gotParams)
	}
	if got.Tool != "mcp__kirim_cepat__cancel_shipment" {
		t.Errorf("proposal tool = %q", got.Tool)
	}
	if got.Arguments["shipment_id"] != "SHP-1042" || got.Arguments["reason"] != "duplicate" {
		t.Errorf("proposal arguments = %v, want the ones the model passed", got.Arguments)
	}
}

// A refused proposal — the kind not enabled for this workspace — comes back as a
// tool error the model can relay, not as a failed turn.
func TestARefusedProposalIsATooResult(t *testing.T) {
	proposer := &recordingProposer{err: errors.New(`the "mcp_call" action is not enabled for this workspace`)}
	src := newSource(writeToolStore(), &recordingCaller{}, &fakeRecorder{}).WithProposer(proposer)

	tool := byName(t, src.CompanyTools(boundCtx(), "co-1"), "mcp__kirim_cepat__cancel_shipment")
	_, err := tool.Run(context.Background(), `{}`)
	if err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Errorf("err = %v, want the reason the proposal was refused", err)
	}
}

// The per-turn MCP call cap bounds round trips to a tenant's server. A proposal
// makes none, so it must not spend one — otherwise a turn that proposes a write
// loses a read it was entitled to.
func TestAProposalDoesNotSpendTheMCPCallBudget(t *testing.T) {
	caller := &recordingCaller{}
	src := NewSource(writeToolStore(), fakeCipher{}, caller, &fakeRecorder{}, &recordingMeter{}, Caps{
		MaxResponseBytes: 1024, MaxCallsPerTurn: 1,
	}).WithProposer(&recordingProposer{})

	list := src.CompanyTools(boundCtx(), "co-1")
	write := byName(t, list, "mcp__kirim_cepat__cancel_shipment")
	read := byName(t, list, "mcp__kirim_cepat__quote_shipping")

	if _, err := write.Run(context.Background(), `{}`); err != nil {
		t.Fatalf("propose: %v", err)
	}
	if _, err := read.Run(context.Background(), `{}`); err != nil {
		t.Fatalf("the read tool was refused after a proposal spent the turn's one call: %v", err)
	}
	if caller.calls != 1 {
		t.Errorf("server calls = %d, want 1 (the read only)", caller.calls)
	}
}

// The model has to be told, or it reports the ticket as filed the moment the
// call returns.
func TestAWriteToolSaysItNeedsApproval(t *testing.T) {
	src := newSource(writeToolStore(), &recordingCaller{}, &fakeRecorder{}).WithProposer(&recordingProposer{})

	tool := byName(t, src.CompanyTools(boundCtx(), "co-1"), "mcp__kirim_cepat__cancel_shipment")
	desc := tool.Description()
	if !strings.Contains(desc, "does cancel_shipment") {
		t.Errorf("description dropped the tenant's own text: %q", desc)
	}
	if !strings.Contains(desc, "do not report it as done") {
		t.Errorf("description does not tell the model to hold: %q", desc)
	}
}
