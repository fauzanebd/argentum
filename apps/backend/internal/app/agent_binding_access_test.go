package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/domain"
)

// T-Z8, decision 8: "Binding a restricted agent to a channel is allowed,
// requires an explicit acknowledgement on the form that says anyone who can post
// here can use this agent, and is recorded on the audit log."

// bindingAuditLog is the audit log an acknowledgement is written to.
type bindingAuditLog struct {
	rows []*domain.AgentAction
	err  error
}

func (a *bindingAuditLog) Create(_ context.Context, row *domain.AgentAction) error {
	if a.err != nil {
		return a.err
	}
	a.rows = append(a.rows, row)
	return nil
}

// restrictedBindings is a company with HR restricted and Ops open.
func restrictedBindings() (*AgentBindingService, *fakeBindingRepo, *agentGrants, *bindingAuditLog) {
	repo := &fakeBindingRepo{}
	grants := newAgentGrants("ag-hr", "ag-ops")
	grants.restricted["ag-hr"] = true
	audit := &bindingAuditLog{}
	svc := bindingService(repo,
		&domain.Agent{ID: "ag-hr", CompanyID: "co-1", Name: "HR", Enabled: true},
		&domain.Agent{ID: "ag-ops", CompanyID: "co-1", Name: "Ops", Enabled: true},
	).WithAccess(authz.New(grants), audit)
	svc.now = func() time.Time { return time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC) }
	return svc, repo, grants, audit
}

func hrOnSlack(ack bool) BindingInput {
	return BindingInput{AgentID: "ag-hr", Channel: "slack", ExternalID: "C07PEOPLE", AcknowledgeRestricted: ack}
}

func TestBindingARestrictedAgentWithoutTheAcknowledgementIsRefused(t *testing.T) {
	svc, repo, _, audit := restrictedBindings()

	_, err := svc.Create(context.Background(), "co-1", "admin-1", hrOnSlack(false))
	if !errors.Is(err, ErrBindingNeedsAcknowledgement) || !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("Create = %v, want ErrBindingNeedsAcknowledgement as a 400", err)
	}
	if !strings.Contains(err.Error(), "anyone who can post here can use it") {
		t.Errorf("error = %q, want the decision's words so the admin knows what they are being asked", err)
	}
	if len(repo.created) != 0 || len(audit.rows) != 0 {
		t.Errorf("a refused binding wrote %d bindings and %d audit rows", len(repo.created), len(audit.rows))
	}
}

func TestWithTheAcknowledgementTheAuditRowNamesTheAdmin(t *testing.T) {
	svc, repo, _, audit := restrictedBindings()

	b, err := svc.Create(context.Background(), "co-1", "admin-1", hrOnSlack(true))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if b.RestrictedAcknowledgedBy != "admin-1" || b.RestrictedAcknowledgedAt == nil || len(repo.created) != 1 {
		t.Errorf("binding = %+v, want it stored with the acknowledgement and its admin", b)
	}
	if len(audit.rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(audit.rows))
	}
	row := audit.rows[0]
	if row.ActorKind != domain.ActorKindUser || row.ActorRef != "admin-1" ||
		row.ToolName != "agent_binding.acknowledge_restricted" || row.AgentID != "ag-hr" ||
		row.Channel != domain.ChannelSlack || row.CompanyID != "co-1" {
		t.Errorf("audit row = %+v, want the admin, the agent, the channel and the company", row)
	}
	args := string(row.ArgsRedacted)
	for _, want := range []string{RestrictedBindingAcknowledgement, "bind-1", "C07PEOPLE"} {
		if !strings.Contains(args, want) {
			t.Errorf("audit args %s do not carry %q", args, want)
		}
	}
}

// An acknowledgement recorded ahead of a restriction would be one nobody made
// about it, so an open agent's binding stores none, whatever the form sent.
func TestAnOpenAgentIsBoundWithoutAskingAndRecordsNoAcknowledgement(t *testing.T) {
	svc, repo, _, audit := restrictedBindings()

	b, err := svc.Create(context.Background(), "co-1", "admin-1",
		BindingInput{AgentID: "ag-ops", Channel: "discord", ExternalID: "118", AcknowledgeRestricted: true})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if b.RestrictedAcknowledgedAt != nil || len(audit.rows) != 0 || len(repo.created) != 1 {
		t.Errorf("binding %+v with %d audit rows; want it bound, unacknowledged, unaudited", b, len(audit.rows))
	}
}

// No acknowledgement without its record: a binding whose row could not be
// written is taken back.
func TestAnAcknowledgementThatCannotBeRecordedBindsNothing(t *testing.T) {
	svc, repo, _, audit := restrictedBindings()
	audit.err = errors.New("audit store down")

	if _, err := svc.Create(context.Background(), "co-1", "admin-1", hrOnSlack(true)); err == nil {
		t.Fatal("Create succeeded without its audit row")
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != "bind-1" {
		t.Errorf("deleted = %v, want the binding taken back", repo.deleted)
	}
}

func TestAnAccessCheckThatFailsBindsNothing(t *testing.T) {
	svc, repo, grants, _ := restrictedBindings()
	grants.err = errors.New("control DB down")

	if _, err := svc.Create(context.Background(), "co-1", "admin-1", hrOnSlack(true)); !errors.Is(err, ErrAccessCheckFailed) {
		t.Fatalf("Create = %v, want ErrAccessCheckFailed", err)
	}
	if len(repo.created) != 0 {
		t.Errorf("created %d bindings on a check that could not be made", len(repo.created))
	}
}

// A binding made before the restriction is silent until an admin acknowledges
// it, and acknowledging it is audited once.
func TestAcknowledgingABindingMadeBeforeTheRestriction(t *testing.T) {
	svc, repo, _, audit := restrictedBindings()
	repo.list = []*domain.AgentChannelBinding{
		{ID: "bind-7", CompanyID: "co-1", AgentID: "ag-hr", AgentName: "HR", Channel: domain.ChannelWhatsApp, ExternalID: "+62812"},
	}

	b, err := svc.Acknowledge(context.Background(), "co-1", "admin-1", "bind-7")
	if err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	if b.RestrictedAcknowledgedBy != "admin-1" || len(repo.acknowledged) != 1 || len(audit.rows) != 1 {
		t.Fatalf("binding %+v, %d acknowledged, %d audit rows; want one of each, naming admin-1",
			b, len(repo.acknowledged), len(audit.rows))
	}

	// Again, on the now-acknowledged row: nothing new is written.
	at := time.Now()
	repo.list[0].RestrictedAcknowledgedAt, repo.list[0].RestrictedAcknowledgedBy = &at, "admin-1"
	if _, err := svc.Acknowledge(context.Background(), "co-1", "admin-2", "bind-7"); err != nil {
		t.Fatalf("second Acknowledge: %v", err)
	}
	if len(repo.acknowledged) != 1 || len(audit.rows) != 1 {
		t.Errorf("a second press wrote %d acknowledgements and %d audit rows, want no more", len(repo.acknowledged), len(audit.rows))
	}
}

func TestAnAcknowledgementNeedsARestrictedAgentAndTheCompanysBinding(t *testing.T) {
	svc, repo, _, audit := restrictedBindings()
	repo.list = []*domain.AgentChannelBinding{
		{ID: "bind-ops", CompanyID: "co-1", AgentID: "ag-ops", AgentName: "Ops", Channel: domain.ChannelDiscord},
	}

	if _, err := svc.Acknowledge(context.Background(), "co-1", "admin-1", "bind-ops"); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("acknowledging an open agent's binding = %v, want ErrConflict", err)
	}
	if _, err := svc.Acknowledge(context.Background(), "co-1", "admin-1", "bind-theirs"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("acknowledging a binding this company does not have = %v, want ErrNotFound", err)
	}
	if len(audit.rows) != 0 {
		t.Errorf("refused acknowledgements wrote %d audit rows", len(audit.rows))
	}
}
