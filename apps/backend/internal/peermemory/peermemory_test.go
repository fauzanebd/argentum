package peermemory

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/Ingenimax/agent-sdk-go/pkg/memory"
	"github.com/Ingenimax/agent-sdk-go/pkg/multitenancy"
	"github.com/alicebob/miniredis/v2"

	"github.com/fauzanebd/argentum/internal/agentscope"
	"github.com/fauzanebd/argentum/internal/guardrails"
	"github.com/fauzanebd/argentum/internal/taint"
)

// T-N11. Every test writes turns the way the SDK writes them — the composed prompt
// as the user message, a tool-calling iteration as an empty assistant message with
// calls, a tool result, then the reply — and reads them back as the providers do.

// turnCtx is one agent's turn on thread th-1 of company co-1. An empty id is an
// unscoped turn.
func turnCtx(agentID, name string) (context.Context, *taint.Tracker) {
	ctx := memory.WithConversationID(context.Background(), "th-1")
	ctx = multitenancy.WithOrgID(ctx, "co-1")
	if agentID != "" {
		ctx = agentscope.WithScope(ctx, agentscope.Scope{AgentID: agentID, Name: name})
	}
	tr := taint.New()
	return taint.With(ctx, tr), tr
}

const (
	finQuestion = "What did invoice-4471 charge us?"
	finPrompt   = "[System context: Sources: finance_dw (id src-fin).]\n\n" + finQuestion
	finReply    = "Invoice 4471 charged Rp 12.500.000, due in 30 days."
	opsQuestion = "Do we have stock for SKU 4471?"
)

// writeTurn writes one turn through the view, as the streaming path does.
func writeTurn(t *testing.T, mem interfaces.Memory, ctx context.Context, question, prompt, reply, callID string) {
	t.Helper()
	ctx = WithQuestion(ctx, question)
	for _, msg := range []interfaces.Message{
		{Role: interfaces.MessageRoleUser, Content: prompt},
		{Role: interfaces.MessageRoleAssistant, ToolCalls: []interfaces.ToolCall{{ID: callID, Name: "run_sql", Arguments: `{"sql":"SELECT 1"}`}}},
		{Role: interfaces.MessageRoleTool, Content: guardrails.Fence("run_sql result", `{"rows":[{"total":12500000}]}`), ToolCallID: callID, Metadata: map[string]interface{}{"tool_name": "run_sql"}},
		{Role: interfaces.MessageRoleAssistant, Content: reply},
	} {
		if err := mem.AddMessage(ctx, msg); err != nil {
			t.Fatalf("AddMessage: %v", err)
		}
	}
}

func read(t *testing.T, mem interfaces.Memory, ctx context.Context) []interfaces.Message {
	t.Helper()
	msgs, err := mem.GetMessages(ctx)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	return msgs
}

// "A single-agent thread's provider request is byte-identical to today's." The
// view returns exactly what the buffer holds, stamps included — and a provider
// reads Role, Content, ToolCalls and ToolCallID, none of which a stamp touches.
func TestASingleAgentReadsItsHistoryExactlyAsWritten(t *testing.T) {
	inner := memory.NewConversationBuffer()
	mem := Wrap(inner)
	ctx, tr := turnCtx("ag-fin", "Finance")
	writeTurn(t, mem, ctx, finQuestion, finPrompt, finReply, "call-1")
	writeTurn(t, mem, ctx, "And the one before?", "[System context: …]\n\nAnd the one before?", "Invoice 4470 was Rp 9.000.000.", "call-2")

	raw, _ := inner.GetMessages(ctx)
	if got := read(t, mem, ctx); !reflect.DeepEqual(got, raw) {
		t.Fatalf("a single agent's history was rewritten:\n got %+v\nwant %+v", got, raw)
	}
	if tr.Any() {
		t.Fatalf("reading its own history tainted the turn: %v", tr.Kinds())
	}
}

// The ticket's first acceptance item, and the whole of what a colleague is allowed
// to see of another agent's turn: the person's question and the reply, fenced.
func TestAColleaguesTurnArrivesAsTheQuestionAndTheFencedReply(t *testing.T) {
	mem := Wrap(memory.NewConversationBuffer())
	finCtx, _ := turnCtx("ag-fin", "Finance")
	writeTurn(t, mem, finCtx, finQuestion, finPrompt, finReply, "call-1")

	opsCtx, tr := turnCtx("ag-ops", "Ops")
	got := read(t, mem, opsCtx)

	if len(got) != 2 {
		t.Fatalf("Ops read %d messages, want the question and the reply:\n%+v", len(got), got)
	}
	if got[0].Role != interfaces.MessageRoleUser || got[0].Content != finQuestion {
		t.Errorf("first = %s %q, want the person's words", got[0].Role, got[0].Content)
	}
	if got[1].Role != interfaces.MessageRoleUser || got[1].Content != guardrails.FencePeer("Finance", finReply) {
		t.Errorf("second = %s %q, want Finance's reply fenced under its name, in the user turn", got[1].Role, got[1].Content)
	}
	if src := tr.Sources(taint.KindAgent); len(src) != 1 || src[0] != "Finance" {
		t.Errorf("agent taint sources = %v, want [Finance]", src)
	}
	// The decision recorded in the package comment: history carries the fact that
	// a colleague wrote to this turn, not what the colleague had read.
	if got := taint.Join(tr.Kinds()); got != "agent" {
		t.Errorf("Ops' taint = %q, want only \"agent\"", got)
	}
}

// The scope leak: Finance's source catalog and its query rows. Neither may reach
// an agent whose allowlist is not Finance's.
func TestAColleaguesPromptAndToolResultsAreNotRead(t *testing.T) {
	mem := Wrap(memory.NewConversationBuffer())
	finCtx, _ := turnCtx("ag-fin", "Finance")
	writeTurn(t, mem, finCtx, finQuestion, finPrompt, finReply, "call-1")

	opsCtx, _ := turnCtx("ag-ops", "Ops")
	for _, msg := range read(t, mem, opsCtx) {
		if strings.Contains(msg.Content, "finance_dw") || strings.Contains(msg.Content, "12500000") {
			t.Errorf("Ops read Finance's context or rows: %q", msg.Content)
		}
		if msg.Role == interfaces.MessageRoleTool || len(msg.ToolCalls) > 0 {
			t.Errorf("Ops read Finance's tool plumbing: %+v", msg)
		}
	}
}

// Interleaved: the reader keeps its own tool calls with their results, in order —
// a tool message separated from the call it answers is a request the provider
// rejects, so dropping a colleague's must not split the reader's own.
func TestTheReadersOwnTurnsKeepTheirToolPairs(t *testing.T) {
	mem := Wrap(memory.NewConversationBuffer())
	finCtx, _ := turnCtx("ag-fin", "Finance")
	opsCtx, _ := turnCtx("ag-ops", "Ops")
	writeTurn(t, mem, finCtx, finQuestion, finPrompt, finReply, "call-fin")
	writeTurn(t, mem, opsCtx, opsQuestion, "[System context: Sources: ops_db.]\n\n"+opsQuestion, "120 units in stock.", "call-ops")

	got := read(t, mem, opsCtx)
	roles := make([]string, len(got))
	for i, m := range got {
		roles[i] = string(m.Role)
	}
	want := []string{"user", "user", "user", "assistant", "tool", "assistant"}
	if !reflect.DeepEqual(roles, want) {
		t.Fatalf("roles = %v, want %v", roles, want)
	}
	if got[3].ToolCalls[0].ID != "call-ops" || got[4].ToolCallID != "call-ops" {
		t.Errorf("Ops' own call and result are not paired: %+v / %+v", got[3], got[4])
	}
	if !strings.Contains(got[2].Content, "ops_db") {
		t.Errorf("Ops' own composed prompt was rewritten: %q", got[2].Content)
	}
}

// @Finance @Ops: one person message, written into the buffer once by each turn.
func TestAQuestionAddressedToTwoAgentsIsReadOnce(t *testing.T) {
	mem := Wrap(memory.NewConversationBuffer())
	finCtx, _ := turnCtx("ag-fin", "Finance")
	opsCtx, _ := turnCtx("ag-ops", "Ops")
	const q = "@Finance @Ops is SKU 4471 paid for and in stock?"
	add := func(ctx context.Context, msg interfaces.Message) {
		if err := mem.AddMessage(WithQuestion(ctx, q), msg); err != nil {
			t.Fatal(err)
		}
	}
	add(opsCtx, interfaces.Message{Role: interfaces.MessageRoleUser, Content: "[System context: ops]\n\n" + q})
	add(finCtx, interfaces.Message{Role: interfaces.MessageRoleUser, Content: "[System context: fin]\n\n" + q})

	count := 0
	for _, m := range read(t, mem, opsCtx) {
		if strings.Contains(m.Content, q) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("the question appears %d times in Ops' view, want 1", count)
	}
}

// The eval harness and a company whose roster failed to load run unscoped, and
// read what they always read.
func TestAnUnscopedReaderSeesEverythingAsWritten(t *testing.T) {
	inner := memory.NewConversationBuffer()
	mem := Wrap(inner)
	finCtx, _ := turnCtx("ag-fin", "Finance")
	writeTurn(t, mem, finCtx, finQuestion, finPrompt, finReply, "call-1")

	bare, tr := turnCtx("", "")
	raw, _ := inner.GetMessages(bare)
	if got := read(t, mem, bare); !reflect.DeepEqual(got, raw) {
		t.Fatal("an unscoped reader's view was rewritten")
	}
	if tr.Any() {
		t.Fatal("an unscoped read tainted the turn")
	}
}

// A buffer written before this package carries no stamps. Production has never
// run a room (it runs 1.6.0), so every such buffer is one agent's, and it is read
// exactly as before.
func TestUnstampedHistoryIsReadAsBefore(t *testing.T) {
	inner := memory.NewConversationBuffer()
	mem := Wrap(inner)
	ctx, _ := turnCtx("ag-ops", "Ops")
	if err := inner.AddMessage(ctx, interfaces.Message{Role: interfaces.MessageRoleAssistant, Content: "an old reply"}); err != nil {
		t.Fatal(err)
	}
	got := read(t, mem, ctx)
	if len(got) != 1 || got[0].Role != interfaces.MessageRoleAssistant || got[0].Content != "an old reply" {
		t.Fatalf("an unstamped message was rewritten: %+v", got)
	}
}

// Hydration replays Finance's rows inside Ops' turn. The explicit stamp must win
// over the scope, or every replayed reply becomes Ops' own.
func TestAnExplicitStampIsNotOverwrittenByTheWritingTurn(t *testing.T) {
	mem := Wrap(memory.NewConversationBuffer())
	opsCtx, _ := turnCtx("ag-ops", "Ops")
	if err := mem.AddMessage(opsCtx, Stamp(interfaces.Message{Role: interfaces.MessageRoleAssistant, Content: finReply}, "ag-fin", "Finance")); err != nil {
		t.Fatal(err)
	}
	// And a person's message, replayed unattributed, reads as written.
	if err := mem.AddMessage(opsCtx, Stamp(interfaces.Message{Role: interfaces.MessageRoleUser, Content: finQuestion}, "", "")); err != nil {
		t.Fatal(err)
	}
	got := read(t, mem, opsCtx)
	if len(got) != 2 || got[0].Content != guardrails.FencePeer("Finance", finReply) || got[1].Content != finQuestion {
		t.Fatalf("view = %+v", got)
	}
}

// "Proven on both paths" — and the warm one is Redis, which JSON-encodes every
// message. A stamp that did not survive the round trip would be no stamp at all.
func TestTheViewHoldsOverRealRedisMemory(t *testing.T) {
	mr := miniredis.RunT(t)
	inner, err := memory.NewRedisMemoryFromConfig(memory.RedisConfig{URL: mr.Addr()})
	if err != nil {
		t.Fatalf("redis memory: %v", err)
	}
	mem := Wrap(inner)
	finCtx, _ := turnCtx("ag-fin", "Finance")
	writeTurn(t, mem, finCtx, finQuestion, finPrompt, finReply, "call-1")

	opsCtx, tr := turnCtx("ag-ops", "Ops")
	got := read(t, mem, opsCtx)
	if len(got) != 2 || got[0].Content != finQuestion || got[1].Content != guardrails.FencePeer("Finance", finReply) {
		t.Fatalf("over Redis, Ops read %+v", got)
	}
	if !tr.Has(taint.KindAgent) {
		t.Error("over Redis, the read did not record agent taint")
	}
	finCtx2, _ := turnCtx("ag-fin", "Finance")
	if own := read(t, mem, finCtx2); len(own) != 4 {
		t.Errorf("over Redis, Finance read %d of its own 4 messages", len(own))
	}
}

// Hydration type-asserts ConversationMemory to decide whether the buffer is warm.
// Hiding it would re-hydrate every turn on top of what is there.
func TestTheWrapperKeepsConversationMemory(t *testing.T) {
	if Wrap(nil) != nil {
		t.Fatal("Wrap(nil) is a non-nil memory — the SDK would call methods on it")
	}
	mem := Wrap(memory.NewConversationBuffer())
	conv, ok := mem.(interfaces.ConversationMemory)
	if !ok {
		t.Fatal("the wrapper hides ConversationMemory")
	}
	finCtx, _ := turnCtx("ag-fin", "Finance")
	writeTurn(t, mem, finCtx, finQuestion, finPrompt, finReply, "call-1")
	opsCtx, _ := turnCtx("ag-ops", "Ops")
	raw, err := conv.GetConversationMessages(opsCtx, "th-1")
	if err != nil || len(raw) != 4 {
		t.Fatalf("GetConversationMessages = %d, %v; want the raw 4, unrewritten", len(raw), err)
	}
}
