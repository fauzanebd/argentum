package app

import (
	"context"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// countingMessages is a MessageRepository whose only interesting property is
// how many messages it says the thread holds.
type countingMessages struct {
	stubMessages
	count int
}

func (c countingMessages) CountByThread(context.Context, string) (int, error) {
	return c.count, nil
}

type summaryThreads struct {
	quietThreadRepo
	summary string
}

func (s summaryThreads) GetByID(context.Context, string) (*domain.ConversationThread, error) {
	return &domain.ConversationThread{ID: "th-1", Summary: s.summary}, nil
}

func runnerWithSummary(t *testing.T, count int, summary string) *ChatRunner {
	t.Helper()
	r, _ := runnerForTurn(t, &directiveLLM{})
	r.messages = countingMessages{count: count}
	r.threadRepo = summaryThreads{
		quietThreadRepo: quietThreadRepo{&fakeThreadRepo{latestErr: domain.ErrNotFound}},
		summary:         summary,
	}
	r.historyLimit = 20
	return r
}

// The whole point: a conversation longer than the memory window gets the part
// it can no longer see.
func TestALongThreadCarriesItsSummary(t *testing.T) {
	r := runnerWithSummary(t, 40, "The user is working out why December margins fell.")
	got := r.withThreadSummaryContext(context.Background(), "and by region?", "th-1")

	if !strings.Contains(got, "December margins fell") {
		t.Errorf("the summary did not reach the turn:\n%s", got)
	}
	if !strings.Contains(got, "and by region?") {
		t.Error("the user's own question was lost")
	}
}

// A thread inside the window is already fully in memory. Pasting a summary of
// messages the model can read verbatim spends context restating them, less
// accurately.
func TestAShortThreadIsLeftAlone(t *testing.T) {
	r := runnerWithSummary(t, 6, "The user is working out why December margins fell.")
	const q = "and by region?"
	if got := r.withThreadSummaryContext(context.Background(), q, "th-1"); got != q {
		t.Errorf("a short thread got a summary block:\n%s", got)
	}
}

// Exactly at the limit is still fully in memory — the boundary belongs on the
// "leave it alone" side, or every thread of exactly twenty messages pays for a
// block that tells it nothing.
func TestTheBoundaryIsInclusive(t *testing.T) {
	r := runnerWithSummary(t, 20, "a summary")
	const q = "question"
	if got := r.withThreadSummaryContext(context.Background(), q, "th-1"); got != q {
		t.Error("a thread exactly at the window got a summary block")
	}
	r = runnerWithSummary(t, 21, "a summary")
	if got := r.withThreadSummaryContext(context.Background(), q, "th-1"); got == q {
		t.Error("a thread one past the window got no summary block")
	}
}

// An empty summary column is the ordinary state of a thread whose refresh has
// not run yet. It must add nothing rather than an empty frame.
func TestAnEmptySummaryAddsNothing(t *testing.T) {
	r := runnerWithSummary(t, 40, "   ")
	const q = "question"
	if got := r.withThreadSummaryContext(context.Background(), q, "th-1"); got != q {
		t.Errorf("an empty summary produced a block:\n%s", got)
	}
}

// The summary is prose about a conversation, and prose about an analytics
// conversation contains numbers. Nothing in it is tool-derived evidence for
// this turn, so the block has to say so — otherwise this feature becomes a new
// way to state a figure no tool returned, which is the worst failure this
// product has.
func TestTheSummaryBlockForbidsQuotingItsFigures(t *testing.T) {
	r := runnerWithSummary(t, 40, "Revenue was 3.8 billion in December.")
	got := r.withThreadSummaryContext(context.Background(), "q", "th-1")
	if !strings.Contains(got, "re-run a query for any number") {
		t.Errorf("the block does not stop its own figures being quoted:\n%s", got)
	}
}

// A runner with no thread repository — or a store that cannot count — skips
// the block rather than failing the turn.
func TestSummaryContextIsSkippedWhenItCannotBeRead(t *testing.T) {
	r, _ := runnerForTurn(t, &directiveLLM{})
	r.threadRepo = nil
	const q = "question"
	if got := r.withThreadSummaryContext(context.Background(), q, "th-1"); got != q {
		t.Error("a runner with no thread repo still tried to add a summary")
	}

	r2 := runnerWithSummary(t, 40, "a summary")
	r2.messages = stubMessages{} // no CountByThread
	if got := r2.withThreadSummaryContext(context.Background(), q, "th-1"); got != q {
		t.Error("a store that cannot count still produced a block")
	}
}
