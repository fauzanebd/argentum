package app

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tools"
)

// T-N6 at the roster: `can_nudge` is off unless an admin turns it on, an edit
// that does not mention it leaves it alone, and nudge_agent is never a checkbox.

func TestNudgingIsOffUnlessTheFormSaysSo(t *testing.T) {
	svc, _, _ := newAgentFixture()

	quiet := mustCreate(t, svc, companyA, AgentInput{Name: "Ops"})
	if quiet.CanNudge {
		t.Error("an agent created without the field may nudge")
	}

	on := true
	asks, err := svc.Create(context.Background(), companyA, AgentInput{Name: "Finance", CanNudge: &on})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !asks.CanNudge {
		t.Error("an agent created with can_nudge may not nudge")
	}
}

func TestAnEditThatOmitsTheFlagLeavesIt(t *testing.T) {
	svc, _, _ := newAgentFixture()
	ctx := context.Background()
	on, off := true, false
	a, err := svc.Create(ctx, companyA, AgentInput{Name: "Finance", CanNudge: &on})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.Update(ctx, companyA, a.ID, AgentInput{Name: "Finance Team"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !got.CanNudge {
		t.Error("renaming the agent from a client that sent no can_nudge switched nudging off")
	}

	got, err = svc.Update(ctx, companyA, a.ID, AgentInput{Name: "Finance Team", CanNudge: &off})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.CanNudge {
		t.Error("can_nudge: false did not switch nudging off")
	}
}

// The registry the API lists carries nudge_agent, because the registry is one
// construction site. The form must not: a tick there would be a second switch
// for one capability, and one that could never do anything.
func TestNudgeIsNeverACheckbox(t *testing.T) {
	_, repo, conns := newAgentFixture()
	svc := NewAgentService(repo, conns, append(slices.Clone(registry), tools.NudgeAgentName))

	if slices.Contains(svc.ToolNames(), tools.NudgeAgentName) {
		t.Error("nudge_agent is in the tool vocabulary")
	}
	for _, o := range svc.CompanyToolOptions(context.Background(), companyA) {
		if o.Name == tools.NudgeAgentName {
			t.Error("nudge_agent is offered as a checkbox")
		}
	}
	_, err := svc.Create(context.Background(), companyA, AgentInput{
		Name: "Ops", AllowedTools: []string{"run_sql", tools.NudgeAgentName},
	})
	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("an allowlist naming nudge_agent = %v, want ErrInvalidInput", err)
	}
}
