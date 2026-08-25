package app

import (
	"context"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
)

// orderedBus records the sequence of things that happened, alongside the
// completer below, so the ordering invariant can be asserted rather than read.
type orderedBus struct{ seq *[]string }

func (b orderedBus) Publish(_ string, evt ChatEvent) error {
	*b.seq = append(*b.seq, "publish:"+evt.Type)
	return nil
}

func (b orderedBus) PublishOutbound(OutboundEvent) error {
	*b.seq = append(*b.seq, "outbound")
	return nil
}

type recordingCompleter struct {
	seq    *[]string
	calls  int
	report string
	thread string
	docID  string
	err    error
}

func (c *recordingCompleter) CompleteReport(_ context.Context, reportID, threadID, docID string, runErr error) {
	*c.seq = append(*c.seq, "complete")
	c.calls++
	c.report, c.thread, c.docID, c.err = reportID, threadID, docID, runErr
}

type stubMessages struct{}

func (stubMessages) Append(_ context.Context, m *domain.Message) error {
	m.ID = "msg-1"
	return nil
}
func (stubMessages) ListByThread(context.Context, string, int, int) ([]*domain.Message, error) {
	return nil, nil
}
func (stubMessages) ListPageByThread(context.Context, string, domain.MessageFilter) ([]*domain.Message, bool, error) {
	return nil, false, nil
}
func (stubMessages) LatestByThread(context.Context, string) (*domain.Message, error) {
	return nil, domain.ErrNotFound
}
func (stubMessages) LatestAssistantSince(context.Context, string, time.Time) (*domain.Message, error) {
	return nil, domain.ErrNotFound
}
func (stubMessages) CountByThread(context.Context, string) (int, error) { return 1, nil }

// quietThreadRepo is fakeThreadRepo without the panics. The shared one asserts
// that a resolver test never touches a thread; completeWith legitimately does,
// on every turn, so this file needs its own.
type quietThreadRepo struct{ *fakeThreadRepo }

func (quietThreadRepo) Touch(context.Context, string, time.Time) error { return nil }

// runnerForCompletion builds the smallest ChatRunner that can run completeWith:
// a thread service that persists a message and a bus that records events.
func runnerForCompletion(seq *[]string, completer APIReportCompleter) *ChatRunner {
	threads := quietThreadRepo{&fakeThreadRepo{latestErr: domain.ErrNotFound}}
	svc := NewThreadService(threads, stubMessages{}, nil, nil, ThreadServiceConfig{
		IdleMinutes: 30, SummaryEveryNTurns: 8,
	})
	r := NewChatRunner(svc, stubMessages{}, threads, nil, nil, nil,
		orderedBus{seq: seq}, nil, nil, nil, 20)
	if completer != nil {
		r = r.WithAPIReports(completer)
	}
	return r
}

// The invariant the SSE bridge is built on: the report row is terminal
// **before** `final` is published.
//
// A client streaming `GET /v1/reports/:id/events` sees `final` and immediately
// re-reads the report. If completion happened after the publish there would be
// a window — small, real, and impossible to reproduce on demand — in which a
// finished report reports itself as still running and the stream closes on it.
// Ordering it this way is what lets the bridge be a forwarder rather than a
// poll loop, so the ordering is the contract and this test is the contract's
// enforcement.
func TestReportCompletesBeforeTheFinalEventIsPublished(t *testing.T) {
	var seq []string
	completer := &recordingCompleter{seq: &seq}
	r := runnerForCompletion(&seq, completer)

	r.completeWith(context.Background(), queue.ChatRunPayload{
		CompanyID: "co-1", ThreadID: "th-1", Channel: domain.ChannelAPI,
		APIReportID: "rep-1", UserMsgID: "msg-0",
	}, "here is your report", 0, 0, time.Second, nil, "")

	if completer.calls != 1 {
		t.Fatalf("CompleteReport called %d times, want 1", completer.calls)
	}
	if completer.report != "rep-1" || completer.thread != "th-1" {
		t.Errorf("completed (%q, %q), want (rep-1, th-1)", completer.report, completer.thread)
	}
	if completer.err != nil {
		t.Errorf("runErr = %v, want nil on the success path", completer.err)
	}

	completeAt, publishAt := -1, -1
	for i, step := range seq {
		if step == "complete" && completeAt < 0 {
			completeAt = i
		}
		if step == "publish:final" && publishAt < 0 {
			publishAt = i
		}
	}
	if completeAt < 0 || publishAt < 0 {
		t.Fatalf("sequence = %v, want both a completion and a final event", seq)
	}
	if completeAt > publishAt {
		t.Errorf("sequence = %v — the report completed after `final`, which reopens the race "+
			"the SSE bridge relies on being closed", seq)
	}
}

// A turn that carries no report id must not touch the completer, and a turn
// that carries one on a runner without a completer must not panic — that is
// the ordinary shape of every non-API channel and of a stack with no `/v1`.
func TestCompleteWithLeavesNonReportTurnsAlone(t *testing.T) {
	var seq []string
	completer := &recordingCompleter{seq: &seq}
	r := runnerForCompletion(&seq, completer)

	r.completeWith(context.Background(), queue.ChatRunPayload{
		CompanyID: "co-1", ThreadID: "th-1", Channel: domain.ChannelDashboard,
	}, "hello", 0, 0, 0, nil, "")

	if completer.calls != 0 {
		t.Errorf("CompleteReport called on a dashboard turn")
	}

	var seq2 []string
	bare := runnerForCompletion(&seq2, nil)
	bare.completeWith(context.Background(), queue.ChatRunPayload{
		CompanyID: "co-1", ThreadID: "th-1", Channel: domain.ChannelAPI, APIReportID: "rep-1",
	}, "hello", 0, 0, 0, nil, "")
}

// The `api` channel's empty case in completeWith is deliberate and load-bearing
// (T-A1): delivery already happened, because the caller is holding the HTTP
// response open. An outbound send here would be a second copy of an answer the
// caller already has.
func TestAPIChannelSendsNothingOutbound(t *testing.T) {
	var seq []string
	r := runnerForCompletion(&seq, nil)

	r.completeWith(context.Background(), queue.ChatRunPayload{
		CompanyID: "co-1", ThreadID: "th-1", Channel: domain.ChannelAPI,
	}, "here is your answer", 0, 0, 0, nil, "")

	for _, step := range seq {
		if step == "outbound" {
			t.Fatalf("sequence = %v — an api turn published an outbound event", seq)
		}
	}
}
