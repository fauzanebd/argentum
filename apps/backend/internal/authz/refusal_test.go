package authz

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/metrics"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// refusalLog is a counter and an audit log in one, that can be made to fail.
type refusalLog struct {
	counts map[string]int
	rows   []*domain.AgentAction
	err    error
}

func (l *refusalLog) RecordAccessRefusal(kind, reason string) {
	if l.counts == nil {
		l.counts = map[string]int{}
	}
	l.counts[kind+"/"+reason]++
}

func (l *refusalLog) Create(_ context.Context, a *domain.AgentAction) error {
	if l.err != nil {
		return l.err
	}
	l.rows = append(l.rows, a)
	return nil
}

func personRefused(kind, id string) Refusal {
	return Refusal{
		Subject: Subject{CompanyID: "co-1", UserID: "u-rina"},
		Kind:    kind, ResourceID: id, Reason: ReasonNotGranted,
		Door: DoorDashboard, Channel: domain.ChannelDashboard,
	}
}

func TestARefusalIsCountedAndWrittenDown(t *testing.T) {
	log := &refusalLog{}
	a := New(&fakeLoader{}).WithCounter(log).WithAudit(log)
	ctx := tenantctx.WithRequestID(context.Background(), "req-7")

	Record(ctx, a, personRefused("agent", "ag-hr"))

	if log.counts["agent/not_granted"] != 1 || len(log.counts) != 1 {
		t.Errorf("counted %v, want agent/not_granted once", log.counts)
	}
	if len(log.rows) != 1 {
		t.Fatalf("wrote %d rows, want 1", len(log.rows))
	}
	row := log.rows[0]
	if row.ToolName != RefusalAuditTool || row.ResultStatus != domain.ActionStatusBlocked {
		t.Errorf("row is %s/%s, want %s/blocked", row.ToolName, row.ResultStatus, RefusalAuditTool)
	}
	if row.CompanyID != "co-1" || row.ActorKind != domain.ActorKindUser || row.ActorRef != "u-rina" {
		t.Errorf("row names %s %s %q, want the person refused", row.CompanyID, row.ActorKind, row.ActorRef)
	}
	if row.AgentID != "ag-hr" || row.ThreadID != "" || row.RequestID != "req-7" || row.Channel != domain.ChannelDashboard {
		t.Errorf("row agent %q thread %q request %q channel %q, want HR, no thread, req-7, the dashboard",
			row.AgentID, row.ThreadID, row.RequestID, row.Channel)
	}
	var args map[string]string
	if err := json.Unmarshal(row.ArgsRedacted, &args); err != nil {
		t.Fatalf("args: %v", err)
	}
	want := map[string]string{"resource_kind": "agent", "resource_id": "ag-hr", "reason": "not_granted", "door": "dashboard"}
	for k, v := range want {
		if args[k] != v {
			t.Errorf("args[%s] = %q, want %q", k, args[k], v)
		}
	}
	if row.ArgsHash == "" {
		t.Error("row has no args hash")
	}
}

func TestARefusalNamesItsConversationAndADoorWithNoPersonNamesItsActor(t *testing.T) {
	log := &refusalLog{}
	a := New(&fakeLoader{}).WithCounter(log).WithAudit(log)

	a.Refused(context.Background(), personRefused(KindConversation, "th-hr"))
	a.Refused(context.Background(), Refusal{
		Subject: Subject{CompanyID: "co-1"}, Kind: "agent", ResourceID: "ag-hr", Reason: ReasonNotCleared,
		Door: DoorWidget, Channel: domain.ChannelWidget, ActorKind: domain.ActorKindEmbed, ActorRef: "visitor-1",
	})

	if len(log.rows) != 2 {
		t.Fatalf("wrote %d rows, want 2", len(log.rows))
	}
	if conv := log.rows[0]; conv.ThreadID != "th-hr" || conv.AgentID != "" {
		t.Errorf("a conversation's refusal has thread %q and agent %q, want the conversation and no agent", conv.ThreadID, conv.AgentID)
	}
	if visitor := log.rows[1]; visitor.ActorKind != domain.ActorKindEmbed || visitor.ActorRef != "visitor-1" {
		t.Errorf("the widget's refusal names %s %q, want the visitor", visitor.ActorKind, visitor.ActorRef)
	}
	if log.counts["conversation/not_granted"] != 1 || log.counts["agent/not_cleared"] != 1 {
		t.Errorf("counted %v", log.counts)
	}
}

// Only a refusal is recorded, and only in the vocabulary: the counter is a
// scraped series and its labels must be bounded by code.
func TestOnlyARefusalInTheVocabularyIsRecorded(t *testing.T) {
	log := &refusalLog{}
	a := New(&fakeLoader{}).WithCounter(log).WithAudit(log)
	cases := []Refusal{
		{Subject: Subject{CompanyID: "co-1"}, Kind: "agent", ResourceID: "x", Reason: ReasonOpen},
		{Subject: Subject{CompanyID: "co-1"}, Kind: "agent", ResourceID: "x", Reason: ReasonGranted},
		{Subject: Subject{CompanyID: "co-1"}, Kind: "agent", ResourceID: "x", Reason: ReasonNotFound},
		{Subject: Subject{CompanyID: "co-1"}, Kind: "agent", ResourceID: "x", Reason: "because"},
		{Subject: Subject{CompanyID: "co-1"}, Kind: "report", ResourceID: "x", Reason: ReasonNotGranted},
		{Subject: Subject{CompanyID: "co-1"}, Kind: "", ResourceID: "x", Reason: ReasonNotGranted},
	}
	for _, r := range cases {
		a.Refused(context.Background(), r)
	}
	if len(log.counts) != 0 || len(log.rows) != 0 {
		t.Errorf("recorded %v and %d rows for things that are not refusals", log.counts, len(log.rows))
	}
}

// The refusal has already happened; a log that is down changes nothing about it
// and does not cost it its count.
func TestAnAuditLogThatFailsLeavesTheRefusalCounted(t *testing.T) {
	log := &refusalLog{err: errors.New("connection refused")}
	a := New(&fakeLoader{}).WithCounter(log).WithAudit(log)
	a.Refused(context.Background(), personRefused("dashboard", "dash-payroll"))
	if log.counts["dashboard/not_granted"] != 1 {
		t.Errorf("counted %v, want the refusal counted although it could not be written down", log.counts)
	}
}

func TestRecordAsksOnlyWhatCanRecord(t *testing.T) {
	var unwired *Authorizer
	for _, decider := range []any{nil, struct{}{}, unwired, &fakeLoader{}} {
		Record(context.Background(), decider, personRefused("agent", "ag-hr"))
	}
}

// New counts on the collector /metrics serves, so no process that builds an
// authorizer can forget to wire a counter.
func TestANewAuthorizerCountsOnTheProcessCollector(t *testing.T) {
	read := func() int64 {
		return metrics.Default().GetSnapshot().Domain.AccessRefusals["document"]["creator_removed"]
	}
	before := read()
	New(&fakeLoader{}).Refused(context.Background(), Refusal{
		Subject: Subject{CompanyID: "co-1"}, Kind: "document", ResourceID: "doc-1", Reason: ReasonCreatorRemoved, Door: DoorJob,
	})
	if got := read() - before; got != 1 {
		t.Errorf("the process collector moved by %d, want 1", got)
	}
}

func TestDoorOfEveryChannel(t *testing.T) {
	cases := map[domain.Channel]Door{
		domain.ChannelDashboard: DoorDashboard,
		"":                      DoorDashboard,
		domain.ChannelAPI:       DoorAPIKey,
		domain.ChannelWidget:    DoorWidget,
		domain.ChannelWhatsApp:  DoorChannel,
		domain.ChannelDiscord:   DoorChannel,
		domain.ChannelLark:      DoorChannel,
		domain.ChannelSlack:     DoorChannel,
		domain.ChannelEmail:     DoorJob,
	}
	for channel, want := range cases {
		if got := DoorOf(channel); got != want {
			t.Errorf("DoorOf(%q) = %s, want %s", channel, got, want)
		}
	}
}
