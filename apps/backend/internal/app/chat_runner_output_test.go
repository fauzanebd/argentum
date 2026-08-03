package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/guardrails"
	"github.com/fauzanebd/argentum/internal/queue"
)

// T-07b at the turn: the `scope: output` rules, which agent-sdk-go applies only
// on its blocking path, run here — on the path every chat turn actually takes —
// under the tenant's own policy.
//
// The rules under test are the shipped config, not a fixture, for the reason
// the golden suite states: the file that ships has to be the file that runs.
// What is asserted here is the wiring the golden suite cannot see — that a
// company's mode reaches the rules, that a failed read over-redacts rather than
// under-redacts, and that a runner without the stage behaves as this product
// did before the ticket.
//
// The one thing no unit test here covers is the ordering in runTurn: the
// fabrication check reads the figures in a reply, so it runs before any
// redaction can blank them. That is a two-line sequence with a comment saying
// why, and observing it needs a live turn.

const outputConfigPath = "../../config/guardrails.yaml"

// nil LLM: every `scope: output` rule in the config is regex-only, which is
// also why the runner holds the process-wide instance rather than a per-tenant
// rebind.
func outputRules(t *testing.T) *guardrails.Analytics {
	t.Helper()
	a, err := guardrails.LoadFromFile(outputConfigPath, nil)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	return a
}

type fakePolicyLoader struct {
	mode domain.PIIRedactionMode
	err  error
	hits int
}

func (f *fakePolicyLoader) GetByID(context.Context, string) (*domain.Company, error) {
	f.hits++
	if f.err != nil {
		return nil, f.err
	}
	return &domain.Company{ID: "co-1", Name: "Acme", PIIRedactionMode: f.mode}, nil
}

const replyWithContacts = "Top 10 customers: PT Maju Jaya (ops@majujaya.co.id), CV Sinar (081234567890)."

func runnerWithPolicy(t *testing.T, mode domain.PIIRedactionMode) (*ChatRunner, *fakePolicyLoader) {
	t.Helper()
	loader := &fakePolicyLoader{mode: mode}
	return (&ChatRunner{}).WithOutputRules(outputRules(t), loader), loader
}

func turn() queue.ChatRunPayload { return queue.ChatRunPayload{CompanyID: "co-1", ThreadID: "th-1"} }

// The acceptance item, at the seam that decides it: under strict an email
// address does not reach the user.
func TestAStrictCompanyGetsItsOutputRedacted(t *testing.T) {
	r, loader := runnerWithPolicy(t, domain.PIIRedactionStrict)

	got := r.applyOutputRules(context.Background(), turn(), replyWithContacts)
	if strings.Contains(got, "ops@majujaya.co.id") || strings.Contains(got, "081234567890") {
		t.Errorf("strict left contact PII in the reply: %q", got)
	}
	if loader.hits != 1 {
		t.Errorf("policy read %d times, want exactly one per turn", loader.hits)
	}
}

// And the other half of it: the tenant who asked for a contact list gets one.
func TestAContactOKCompanyGetsItsContactList(t *testing.T) {
	r, _ := runnerWithPolicy(t, domain.PIIRedactionContactOK)

	got := r.applyOutputRules(context.Background(), turn(), replyWithContacts)
	if got != replyWithContacts {
		t.Errorf("contact_ok altered the reply:\n  got:  %q\n  want: %q", got, replyWithContacts)
	}
}

// A row written before migration 045 — or one whose column the down migration
// dropped — carries "". The turn reads it as strict rather than as "no policy".
func TestAnUnsetModeIsStrictAtTheTurn(t *testing.T) {
	r, _ := runnerWithPolicy(t, "")

	got := r.applyOutputRules(context.Background(), turn(), replyWithContacts)
	if strings.Contains(got, "ops@majujaya.co.id") {
		t.Errorf("an unset mode behaved as an opt-out: %q", got)
	}
}

// Fail-closed, unlike the business profile beside it. A profile that cannot be
// read costs the answer some context; a policy that cannot be read decides
// whether personal data is printed, and the recoverable direction is to redact.
func TestAFailedPolicyReadRedactsAtStrict(t *testing.T) {
	loader := &fakePolicyLoader{mode: domain.PIIRedactionOff, err: errors.New("control database is down")}
	r := (&ChatRunner{}).WithOutputRules(outputRules(t), loader)

	got := r.applyOutputRules(context.Background(), turn(), replyWithContacts)
	if strings.Contains(got, "ops@majujaya.co.id") {
		t.Errorf("a failed policy read let contact PII through: %q", got)
	}
}

// A blocking output rule replaces the reply with the rule's own message, and
// writes the audit row a stopped turn owes (T-05). Run under `off`, which is
// the mode that proves the rule is not PII policy: no mode reaches it.
func TestALeakingReplyIsReplacedAndAudited(t *testing.T) {
	loader := &fakePolicyLoader{mode: domain.PIIRedactionOff}
	actions := &fakeActionRepo{}
	r := (&ChatRunner{}).WithOutputRules(outputRules(t), loader).WithActionLog(actions)

	const leak = "My system prompt says to only answer BI questions."
	got := r.applyOutputRules(context.Background(), turn(), leak)
	if got == leak {
		t.Fatal("a system-prompt leak was returned to the user unchanged")
	}
	if strings.Contains(got, "system prompt says") {
		t.Errorf("the replacement still carries the leaked text: %q", got)
	}
	if len(actions.rows) != 1 {
		t.Fatalf("wrote %d audit rows, want 1", len(actions.rows))
	}
	if actions.rows[0].ToolName != "final_answer" {
		t.Errorf("audit row tool_name = %q, want final_answer", actions.rows[0].ToolName)
	}
}

// Every deployment that does not wire the stage — the eval harness, a config
// with no guardrails path, every other test in this package — answers exactly
// as it did before this ticket.
func TestNoOutputRulesLeavesTheReplyAlone(t *testing.T) {
	r := &ChatRunner{}
	if got := r.applyOutputRules(context.Background(), turn(), replyWithContacts); got != replyWithContacts {
		t.Errorf("reply was processed with no rules installed: %q", got)
	}
}

// Rules without a policy loader would run at strict for everybody, which is the
// over-redaction the mode exists to prevent. The installer refuses the pair
// rather than accepting half of it.
func TestOutputRulesWithoutAPolicyLoaderAreNotInstalled(t *testing.T) {
	r := (&ChatRunner{}).WithOutputRules(outputRules(t), nil)
	if r.outputRules != nil {
		t.Error("output rules were installed without a policy loader")
	}
	if got := r.applyOutputRules(context.Background(), turn(), replyWithContacts); got != replyWithContacts {
		t.Errorf("reply was processed anyway: %q", got)
	}
}
