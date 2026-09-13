package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// T-Z9, decision 12: "every grant change is audited" — and what happens when the
// audit log cannot take the row, which depends on which way the change goes.

// accessAuditLog is the audit log, which a test can take down.
type accessAuditLog struct {
	rows []*domain.AgentAction
	err  error
}

func (l *accessAuditLog) Create(_ context.Context, a *domain.AgentAction) error {
	if l.err != nil {
		return l.err
	}
	l.rows = append(l.rows, a)
	return nil
}

func (l *accessAuditLog) tools() []string {
	out := make([]string, 0, len(l.rows))
	for _, r := range l.rows {
		out = append(out, r.ToolName)
	}
	return out
}

func auditArgs(t *testing.T, row *domain.AgentAction) map[string]any {
	t.Helper()
	var args map[string]any
	if err := json.Unmarshal(row.ArgsRedacted, &args); err != nil {
		t.Fatalf("audit args: %v: %s", err, row.ArgsRedacted)
	}
	return args
}

// "A grant change writes an audit row naming both users": the admin as the
// actor, the person in the arguments — for a grant, a revoke and both flips.
func TestAResourceGrantChangeIsAuditedNamingBothPeople(t *testing.T) {
	store := newFakeResourceGrants()
	log := &accessAuditLog{}
	svc := NewResourceAccessService(store, log)
	ctx := context.Background()

	if err := svc.Grant(ctx, "co-1", "admin-1", "member-1", "agent", "ours"); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if _, err := svc.SetAccessMode(ctx, "co-1", "admin-1", "agent", "ours", "restricted"); err != nil {
		t.Fatalf("restrict: %v", err)
	}
	if err := svc.Revoke(ctx, "co-1", "admin-1", "member-1", "agent", "ours"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := svc.SetAccessMode(ctx, "co-1", "admin-1", "agent", "ours", "open"); err != nil {
		t.Fatalf("re-open: %v", err)
	}

	want := []string{AccessGrantAudit, AccessModeAudit, AccessRevokeAudit, AccessModeAudit}
	if got := log.tools(); len(got) != len(want) {
		t.Fatalf("rows %v, want %v", got, want)
	}
	for i, row := range log.rows {
		if row.ToolName != want[i] || row.ActorKind != domain.ActorKindUser || row.ActorRef != "admin-1" ||
			row.CompanyID != "co-1" || row.AgentID != "ours" || row.ResultStatus != domain.ActionStatusOK {
			t.Errorf("row %d = %s by %s %q in %q on agent %q (%s), want %s by user admin-1 in co-1 on ours",
				i, row.ToolName, row.ActorKind, row.ActorRef, row.CompanyID, row.AgentID, row.ResultStatus, want[i])
		}
		args := auditArgs(t, row)
		if args["resource_kind"] != "agent" || args["resource_id"] != "ours" {
			t.Errorf("row %d args %v, want the agent named", i, args)
		}
	}
	for _, i := range []int{0, 2} {
		if got := auditArgs(t, log.rows[i])["user_id"]; got != "member-1" {
			t.Errorf("%s names %v, want the person it was for, member-1", log.rows[i].ToolName, got)
		}
	}
	restrict := auditArgs(t, log.rows[1])
	if restrict["access_mode"] != "restricted" || restrict["previous_access_mode"] != "open" {
		t.Errorf("the restriction's row says %v", restrict)
	}
}

// A change that changes nothing is not a change, and writes no row: granting
// what is held, revoking what is not, flipping to the mode it already has.
func TestAnAccessChangeThatChangesNothingWritesNoRow(t *testing.T) {
	store := newFakeResourceGrants()
	log := &accessAuditLog{}
	svc := NewResourceAccessService(store, log)
	ctx := context.Background()

	for range 2 {
		if err := svc.Grant(ctx, "co-1", "admin-1", "member-1", "dashboard", "ours"); err != nil {
			t.Fatalf("Grant: %v", err)
		}
	}
	if err := svc.Revoke(ctx, "co-1", "admin-1", "admin-1", "dashboard", "ours"); err != nil {
		t.Fatalf("Revoke of what is not held: %v", err)
	}
	if _, err := svc.SetAccessMode(ctx, "co-1", "admin-1", "dashboard", "ours", "open"); err != nil {
		t.Fatalf("open an open dashboard: %v", err)
	}
	if got := log.tools(); len(got) != 1 || got[0] != AccessGrantAudit {
		t.Errorf("rows %v, want the one grant", got)
	}
	// Another company's resource is refused as the write would refuse it, and
	// nothing is recorded about it.
	if err := svc.Grant(ctx, "co-1", "admin-1", "member-1", "dashboard", "theirs"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a grant on another company's dashboard: %v, want not found", err)
	}
	if len(log.rows) != 1 {
		t.Errorf("%d rows after a refused grant, want 1", len(log.rows))
	}
}

// An opening nobody can review is undone: the grant and the re-open.
func TestAnAccessOpeningThatCannotBeRecordedIsUndone(t *testing.T) {
	store := newFakeResourceGrants()
	log := &accessAuditLog{err: errors.New("audit log unavailable")}
	svc := NewResourceAccessService(store, log)
	ctx := context.Background()

	if err := svc.Grant(ctx, "co-1", "admin-1", "member-1", "agent", "ours"); err == nil {
		t.Error("a grant that could not be recorded reported success")
	}
	if _, held := store.grants["member-1/agent/ours"]; held {
		t.Error("a grant that could not be recorded was left in place")
	}

	store.modes[domain.ResourceKindAgent]["ours"] = domain.AccessModeRestricted
	if _, err := svc.SetAccessMode(ctx, "co-1", "admin-1", "agent", "ours", "open"); err == nil {
		t.Error("a re-open that could not be recorded reported success")
	}
	if got := store.modes[domain.ResourceKindAgent]["ours"]; got != domain.AccessModeRestricted {
		t.Errorf("a re-open that could not be recorded left the agent %s, want restricted", got)
	}
}

// A closing is never held back by its record: the revoke and the restriction
// stand, and the caller is told they succeeded, because they did.
func TestAnAccessClosingThatCannotBeRecordedStands(t *testing.T) {
	store := newFakeResourceGrants()
	log := &accessAuditLog{}
	svc := NewResourceAccessService(store, log)
	ctx := context.Background()
	if err := svc.Grant(ctx, "co-1", "admin-1", "member-1", "agent", "ours"); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	log.err = errors.New("audit log unavailable")
	if err := svc.Revoke(ctx, "co-1", "admin-1", "member-1", "agent", "ours"); err != nil {
		t.Errorf("a revoke whose record could not be written: %v, want it to stand", err)
	}
	if _, held := store.grants["member-1/agent/ours"]; held {
		t.Error("the revoke did not take effect")
	}
	if _, err := svc.SetAccessMode(ctx, "co-1", "admin-1", "agent", "ours", "restricted"); err != nil {
		t.Errorf("a restriction whose record could not be written: %v, want it to stand", err)
	}
	if got := store.modes[domain.ResourceKindAgent]["ours"]; got != domain.AccessModeRestricted {
		t.Errorf("the agent is %s, want restricted", got)
	}
}

// Capabilities, by the same two rules.
func TestACapabilityChangeIsAuditedAndAnUnrecordedGrantIsUndone(t *testing.T) {
	store := newFakeCapabilityStore()
	log := &accessAuditLog{}
	svc := NewCapabilityService(store, log)
	ctx := context.Background()

	for range 2 {
		if err := svc.Grant(ctx, "co-1", "u-1", "u-2", "voice"); err != nil {
			t.Fatalf("Grant: %v", err)
		}
	}
	for range 2 {
		if err := svc.Revoke(ctx, "co-1", "u-1", "u-2", "voice"); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
	}
	if got := log.tools(); len(got) != 2 || got[0] != CapabilityGrantAudit || got[1] != CapabilityRevokeAudit {
		t.Fatalf("rows %v, want one grant and one revoke", got)
	}
	for _, row := range log.rows {
		args := auditArgs(t, row)
		if row.ActorRef != "u-1" || args["user_id"] != "u-2" || args["capability"] != "voice" || row.AgentID != "" {
			t.Errorf("%s names actor %q, args %v, agent %q; want u-1 granting u-2 voice", row.ToolName, row.ActorRef, args, row.AgentID)
		}
	}

	log.err = errors.New("audit log unavailable")
	if err := svc.Grant(ctx, "co-1", "u-1", "u-2", "voice"); err == nil {
		t.Error("a capability grant that could not be recorded reported success")
	}
	if has, err := svc.Has(ctx, "co-1", "u-2", domain.CapabilityVoice); err != nil || has {
		t.Errorf("Has = %v, %v after an unrecorded grant, want it undone", has, err)
	}

	log.err = nil
	if err := svc.Grant(ctx, "co-1", "u-1", "u-2", "voice"); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	log.err = errors.New("audit log unavailable")
	if err := svc.Revoke(ctx, "co-1", "u-1", "u-2", "voice"); err != nil {
		t.Errorf("a capability revoke whose record could not be written: %v, want it to stand", err)
	}
	if has, _ := svc.Has(ctx, "co-1", "u-2", domain.CapabilityVoice); has {
		t.Error("the revoke did not take effect")
	}
}
