package app

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
)

// fakeToolMemory is an in-memory ToolMemory. It records what a turn wrote and
// serves it back, which is the whole contract.
type fakeToolMemory struct {
	mu       sync.Mutex
	appended []*domain.Message
	listErr  error
	appendE  error
}

func (f *fakeToolMemory) Append(_ context.Context, m *domain.Message) error {
	if f.appendE != nil {
		return f.appendE
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.appended = append(f.appended, m)
	return nil
}

func (f *fakeToolMemory) ListByThreadRole(_ context.Context, _ string, role domain.MessageRole, limit int) ([]*domain.Message, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.Message
	for _, m := range f.appended {
		if m.Role == role {
			out = append(out, m)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

// ListRecentByThread returns the newest n, oldest-first — the property T-Q7
// exists to establish. The fake honours it so a test cannot pass against a
// stub that is more forgiving than the database.
func (f *fakeToolMemory) ListRecentByThread(_ context.Context, _ string, limit int) ([]*domain.Message, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]*domain.Message(nil), f.appended...)
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func runnerWithToolMemory(t *testing.T, mem ToolMemory, turns int) *ChatRunner {
	t.Helper()
	r, _ := runnerForTurn(t, &directiveLLM{})
	return r.WithToolMemory(mem, turns)
}

// The round trip this ticket exists for: what one turn did reaches the next
// turn's prompt.
func TestPriorWorkRoundTripsThroughToolMemory(t *testing.T) {
	mem := &fakeToolMemory{}
	r := runnerWithToolMemory(t, mem, 3)

	r.rememberToolWork(context.Background(), payloadFor("th-1"), []ToolDigest{
		{Tool: "get_schema", SourceID: "src-1", Tables: []string{"fact_sales"}, Rows: -1},
		{Tool: "run_sql", SourceID: "src-1", Query: "SELECT SUM(sales_amount) FROM fact_sales", Rows: 1},
	})

	if len(mem.appended) != 1 {
		t.Fatalf("wrote %d rows, want exactly one per turn", len(mem.appended))
	}
	if mem.appended[0].Role != domain.MessageRoleTool {
		t.Errorf("row role = %q, want tool", mem.appended[0].Role)
	}

	block := RenderPriorWork(r.priorWork(context.Background(), "th-1"))
	if !strings.Contains(block, "fact_sales") {
		t.Errorf("the schema read did not reach the next turn:\n%s", block)
	}
	if !strings.Contains(block, "SELECT SUM(sales_amount)") {
		t.Errorf("the query did not reach the next turn:\n%s", block)
	}
}

// One row per turn, not one per call. Seven rows between two sentences of
// conversation is the same crowding-out that makes historyLimit a worse number
// than it looks.
func TestToolMemoryWritesOneRowPerTurn(t *testing.T) {
	mem := &fakeToolMemory{}
	r := runnerWithToolMemory(t, mem, 3)

	for i := 0; i < 3; i++ {
		r.rememberToolWork(context.Background(), payloadFor("th-1"), []ToolDigest{
			{Tool: "run_sql", Query: "SELECT 1", Rows: 1},
		})
	}
	if len(mem.appended) != 3 {
		t.Errorf("three turns wrote %d rows, want 3", len(mem.appended))
	}
}

// Zero turns is the write-but-do-not-read setting the feature is measured
// with. Collapsing it into the default would make the comparison unrunnable.
func TestZeroPriorWorkTurnsWritesButDoesNotRead(t *testing.T) {
	mem := &fakeToolMemory{}
	r := runnerWithToolMemory(t, mem, 0)

	r.rememberToolWork(context.Background(), payloadFor("th-1"), []ToolDigest{
		{Tool: "run_sql", Query: "SELECT 1", Rows: 1},
	})
	if len(mem.appended) != 1 {
		t.Fatalf("the write was disabled too: %d rows", len(mem.appended))
	}
	if got := r.priorWork(context.Background(), "th-1"); len(got) != 0 {
		t.Errorf("the read happened anyway: %+v", got)
	}
}

// A negative value means "use the default", which is what a caller with no
// opinion passes.
func TestNegativePriorWorkTurnsUsesTheDefault(t *testing.T) {
	r := runnerWithToolMemory(t, &fakeToolMemory{}, -1)
	if r.priorWorkMax != 3 {
		t.Errorf("priorWorkMax = %d, want the default 3", r.priorWorkMax)
	}
}

// A turn that answered correctly and failed to write its own memory is a turn
// that answered correctly. Failing it here would trade a delivered answer for
// a bookkeeping error.
func TestToolMemoryFailuresDoNotBreakTheTurn(t *testing.T) {
	writeFails := &fakeToolMemory{appendE: context.DeadlineExceeded}
	r := runnerWithToolMemory(t, writeFails, 3)
	r.rememberToolWork(context.Background(), payloadFor("th-1"),
		[]ToolDigest{{Tool: "run_sql", Query: "SELECT 1"}})

	readFails := &fakeToolMemory{listErr: context.DeadlineExceeded}
	r2 := runnerWithToolMemory(t, readFails, 3)
	if got := r2.priorWork(context.Background(), "th-1"); got != nil {
		t.Errorf("a failed read produced %+v, want nothing", got)
	}
}

// A runner with no tool memory writes nothing and reads nothing — the
// behaviour every turn had before this ticket.
func TestWithoutToolMemoryNothingChanges(t *testing.T) {
	r, _ := runnerForTurn(t, &directiveLLM{})
	if got := r.priorWork(context.Background(), "th-1"); got != nil {
		t.Errorf("a runner with no tool memory read %+v", got)
	}
	// Must not panic.
	r.rememberToolWork(context.Background(), payloadFor("th-1"),
		[]ToolDigest{{Tool: "run_sql"}})

	if got := r.withPriorWorkContext(context.Background(), "the question", "th-1"); got != "the question" {
		t.Errorf("the message was modified without tool memory: %q", got)
	}
}

// Deduped across turns as well as within one: a conversation that has read the
// same schema in three consecutive turns should say so once.
func TestPriorWorkDedupesAcrossTurns(t *testing.T) {
	mem := &fakeToolMemory{}
	r := runnerWithToolMemory(t, mem, 5)

	for i := 0; i < 3; i++ {
		r.rememberToolWork(context.Background(), payloadFor("th-1"), []ToolDigest{
			{Tool: "get_schema", SourceID: "src-1", Tables: []string{"fact_sales"}},
		})
	}
	got := r.priorWork(context.Background(), "th-1")
	if len(got) != 1 {
		t.Errorf("the same schema read appears %d times, want 1: %+v", len(got), got)
	}
}

// payloadFor is the minimum a rememberToolWork call needs. Declared here
// rather than reusing a directive-test fixture because what this file cares
// about is the thread id and nothing else.
func payloadFor(threadID string) queue.ChatRunPayload {
	return queue.ChatRunPayload{
		CompanyID: "co-1", ThreadID: threadID, UserMsgID: "msg-1",
		Channel: domain.ChannelDashboard, Message: "q",
	}
}
