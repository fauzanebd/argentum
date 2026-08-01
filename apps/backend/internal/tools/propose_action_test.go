package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

type fakeProposer struct {
	gotKind   string
	gotParams string
	res       *ProposeActionResult
	err       error
}

func (p *fakeProposer) ProposeAction(_ context.Context, in ProposeActionInput) (*ProposeActionResult, error) {
	p.gotKind = in.Kind
	p.gotParams = string(in.Params)
	if p.err != nil {
		return nil, p.err
	}
	return p.res, nil
}

// The proposal is audited by the same decorator every tool call goes through, so
// "every proposal appears in agent_actions" is true without propose_action
// knowing the audit log exists — which is the whole point of T-05's decorator.
func TestProposeActionTool_ProposesAndIsAudited(t *testing.T) {
	prop := &fakeProposer{res: &ProposeActionResult{
		InvocationID: "inv-1", ActionKind: "send_message", Status: "proposed",
		RequiresApproval: true, Description: "send a test message",
	}}
	auditor := &fakeAuditor{}
	tool := WithAudit(NewProposeActionTool(prop), auditor)

	out, err := tool.Execute(turnCtx(),
		`{"action_kind":"send_message","params":{"channel":"whatsapp","target_ref":"+62","body":"hi"}}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Delegated to the service with the parsed kind and params.
	if prop.gotKind != "send_message" {
		t.Fatalf("proposer kind = %q, want send_message", prop.gotKind)
	}
	var params map[string]string
	if err := json.Unmarshal([]byte(prop.gotParams), &params); err != nil {
		t.Fatalf("params not forwarded as JSON: %v", err)
	}
	if params["body"] != "hi" {
		t.Fatalf("forwarded params = %v, want body=hi", params)
	}

	// The tool's result carries the invocation id back to the agent.
	var res ProposeActionResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("tool output not JSON: %v", err)
	}
	if res.InvocationID != "inv-1" {
		t.Fatalf("invocation id = %q, want inv-1", res.InvocationID)
	}

	// One audit row, named propose_action, recorded ok.
	row := auditor.only(t)
	if row.ToolName != "propose_action" {
		t.Fatalf("audit tool_name = %q, want propose_action", row.ToolName)
	}
	if row.ResultStatus != domain.ActionStatusOK {
		t.Fatalf("audit status = %q, want ok", row.ResultStatus)
	}
	if row.CompanyID != "co-1" || row.ThreadID != "th-1" {
		t.Fatalf("audit scope = %s/%s, want co-1/th-1", row.CompanyID, row.ThreadID)
	}
}

// A deployment with no action service wired reports it, rather than pretending.
func TestProposeActionTool_NotConfigured(t *testing.T) {
	tool := NewProposeActionTool(nil)
	if _, err := tool.Execute(turnCtx(), `{"action_kind":"send_message","params":{}}`); err == nil {
		t.Fatal("want an error when no proposer is configured, got nil")
	}
}
