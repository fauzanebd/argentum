package app

import (
	"context"
	"strings"
	"sync"
	"testing"

	sdkagent "github.com/Ingenimax/agent-sdk-go/pkg/agent"
	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/Ingenimax/agent-sdk-go/pkg/memory"
	"github.com/Ingenimax/agent-sdk-go/pkg/multitenancy"

	"github.com/fauzanebd/argentum/internal/agentscope"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/guardrails"
	"github.com/fauzanebd/argentum/internal/peermemory"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/taint"
)

// T-N11 at the runner: both paths into a room agent's history — the shared buffer
// a turn finds warm, and hydrateMemory when it is cold — and the prior-work block,
// which carried a colleague's queries by a route that is not memory at all.

// historyLLM reads the memory it is handed exactly as a provider's message-history
// builder does (`pkg/llm/openai/message_history.go`), and records it. What it
// records is the history half of the provider request.
type historyLLM struct {
	mu   sync.Mutex
	seen [][]interfaces.Message
}

func (l *historyLLM) Generate(ctx context.Context, prompt string, opts ...interfaces.GenerateOption) (string, error) {
	return l.GenerateWithTools(ctx, prompt, nil, opts...)
}

func (l *historyLLM) GenerateWithTools(ctx context.Context, _ string, _ []interfaces.Tool, opts ...interfaces.GenerateOption) (string, error) {
	var o interfaces.GenerateOptions
	for _, opt := range opts {
		opt(&o)
	}
	if o.Memory != nil {
		msgs, err := o.Memory.GetMessages(ctx)
		if err != nil {
			return "", err
		}
		l.mu.Lock()
		l.seen = append(l.seen, msgs)
		l.mu.Unlock()
	}
	return "120 units in stock.", nil
}
func (l *historyLLM) GenerateDetailed(context.Context, string, ...interfaces.GenerateOption) (*interfaces.LLMResponse, error) {
	panic("unexpected GenerateDetailed")
}
func (l *historyLLM) GenerateWithToolsDetailed(context.Context, string, []interfaces.Tool, ...interfaces.GenerateOption) (*interfaces.LLMResponse, error) {
	panic("unexpected GenerateWithToolsDetailed")
}
func (l *historyLLM) Name() string            { return "history-stub" }
func (l *historyLLM) SupportsStreaming() bool { return false }

func (l *historyLLM) firstRequest(t *testing.T) []interfaces.Message {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.seen) == 0 {
		t.Fatal("the model was never handed a memory")
	}
	return l.seen[0]
}

// roomTurnCtx is the context ChatRunner.Run builds for the memory: the thread as
// the conversation, the company as the org, and one agent's scope.
func roomTurnCtx(agentID, name string) context.Context {
	ctx := memory.WithConversationID(context.Background(), "th-1")
	ctx = multitenancy.WithOrgID(ctx, "co-1")
	ctx = agentscope.WithScope(ctx, agentscope.Scope{AgentID: agentID, Name: name})
	return taint.With(ctx, taint.New())
}

// The warm path, end to end through Run: Finance's turn is already in the shared
// buffer, and the Analyst is addressed next. The history the model is handed
// carries Finance's reply fenced in the user turn, and none of Finance's prompt,
// calls or rows.
func TestAColleaguesTurnReachesTheModelFencedOnAWarmBuffer(t *testing.T) {
	shared := peermemory.Wrap(memory.NewConversationBuffer())
	finCtx := peermemory.WithQuestion(roomTurnCtx("ag-fin", "Finance"), "What did invoice-4471 charge us?")
	for _, msg := range []interfaces.Message{
		{Role: interfaces.MessageRoleUser, Content: "[System context: Sources: finance_dw.]\n\nWhat did invoice-4471 charge us?"},
		{Role: interfaces.MessageRoleAssistant, ToolCalls: []interfaces.ToolCall{{ID: "call-fin", Name: "run_sql", Arguments: "{}"}}},
		{Role: interfaces.MessageRoleTool, Content: `{"rows":[{"total":12500000}]}`, ToolCallID: "call-fin"},
		{Role: interfaces.MessageRoleAssistant, Content: "Invoice 4471 charged Rp 12.500.000."},
	} {
		if err := shared.AddMessage(finCtx, msg); err != nil {
			t.Fatal(err)
		}
	}

	llm := &historyLLM{}
	r, _ := runnerForTurn(t, llm)
	r.agentFactory = func(spec AgentSpec) (*sdkagent.Agent, error) {
		return sdkagent.NewAgent(
			sdkagent.WithLLM(spec.Primary),
			sdkagent.WithMemory(shared),
			sdkagent.WithSystemPrompt("You are Argentum."),
			sdkagent.WithMaxIterations(2),
		)
	}
	runner, _ := rosterFixture()
	r.WithRoster(runner.roster)

	if err := r.Run(context.Background(), queue.ChatRunPayload{
		CompanyID: "co-1", ThreadID: "th-1", Channel: domain.ChannelAPI, AgentID: "ag-def",
		Message: "@Analyst is that invoice paid?", UserMsgID: "msg-2",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	history := llm.firstRequest(t)
	var fenced bool
	for _, m := range history {
		if m.Role == interfaces.MessageRoleTool || len(m.ToolCalls) > 0 {
			t.Errorf("the model was handed Finance's tool plumbing: %+v", m)
		}
		if strings.Contains(m.Content, "finance_dw") || strings.Contains(m.Content, "12500000") {
			t.Errorf("the model was handed Finance's context or rows: %q", m.Content)
		}
		if m.Role == interfaces.MessageRoleAssistant && strings.Contains(m.Content, "12.500.000") {
			t.Errorf("Finance's reply arrived as the Analyst's own assistant message: %q", m.Content)
		}
		if m.Role == interfaces.MessageRoleUser && m.Content == guardrails.FencePeer("Finance", "Invoice 4471 charged Rp 12.500.000.") {
			fenced = true
		}
	}
	if !fenced {
		t.Fatalf("Finance's reply is not in the history fenced under its name:\n%+v", history)
	}
}

// messagesWithRows serves a thread's rows to hydration's fallback read.
type messagesWithRows struct {
	stubMessages
	rows []*domain.Message
}

func (m messagesWithRows) ListByThread(context.Context, string, int, int) ([]*domain.Message, error) {
	return m.rows, nil
}

// The cold path. hydrateMemory replays Finance's row inside the Analyst's turn, and
// must attribute it to Finance — from `messages.agent_id` — or the view reads it as
// the Analyst's own.
func TestHydrationAttributesEachReplyToTheAgentThatWroteIt(t *testing.T) {
	r := &ChatRunner{historyLimit: 20, messages: messagesWithRows{rows: []*domain.Message{
		{ID: "u1", Role: domain.MessageRoleUser, Content: "What did invoice-4471 charge us?"},
		{ID: "a1", Role: domain.MessageRoleAssistant, Content: "Rp 12.500.000.", AgentID: "ag-fin", AgentName: "Finance"},
		{ID: "a2", Role: domain.MessageRoleAssistant, Content: "I checked the ledger too.", AgentID: "ag-def", AgentName: "Analyst"},
	}}}
	agent, err := sdkagent.NewAgent(
		sdkagent.WithLLM(&historyLLM{}),
		sdkagent.WithMemory(peermemory.Wrap(memory.NewConversationBuffer())),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := roomTurnCtx("ag-def", "Analyst")

	if err := r.hydrateMemory(ctx, agent, queue.ChatRunPayload{ThreadID: "th-1", UserMsgID: "u2"}); err != nil {
		t.Fatalf("hydrate: %v", err)
	}
	got, err := agent.GetMemory().GetMessages(ctx)
	if err != nil || len(got) != 3 {
		t.Fatalf("GetMessages = %d, %v", len(got), err)
	}
	if got[0].Role != interfaces.MessageRoleUser || got[0].Content != "What did invoice-4471 charge us?" {
		t.Errorf("the person's message = %+v, want it as written", got[0])
	}
	if got[1].Role != interfaces.MessageRoleUser || got[1].Content != guardrails.FencePeer("Finance", "Rp 12.500.000.") {
		t.Errorf("Finance's replayed reply = %+v, want it fenced under Finance", got[1])
	}
	if got[2].Role != interfaces.MessageRoleAssistant || got[2].Content != "I checked the ledger too." {
		t.Errorf("the Analyst's own replayed reply = %+v, want it as its own", got[2])
	}
	if !taint.Has(ctx, taint.KindAgent) {
		t.Error("reading a replayed colleague's reply did not record agent taint")
	}
}

// The prior-work block is our own framing — "work already done in THIS
// conversation, reuse it" — and before T-N11 it handed the Analyst Finance's SQL
// against Finance's source. A row with no agent predates the stamp, and no room.
func TestPriorWorkLeavesOutAColleaguesQueries(t *testing.T) {
	mem := &fakeToolMemory{}
	for _, row := range []struct{ agent, sql string }{
		{"ag-fin", "SELECT * FROM finance.ledger"},
		{"ag-def", "SELECT count(*) FROM sales"},
		{"", "SELECT 'legacy'"},
	} {
		mem.appended = append(mem.appended, &domain.Message{
			Role: domain.MessageRoleTool, AgentID: row.agent,
			Content: EncodeDigests([]ToolDigest{{Tool: "run_sql", Query: row.sql, Rows: 1}}),
		})
	}
	r := runnerWithToolMemory(t, mem, 5)

	block := RenderPriorWork(r.priorWork(roomTurnCtx("ag-def", "Analyst"), "th-1"))
	if strings.Contains(block, "finance.ledger") {
		t.Errorf("the Analyst's prior-work block carries Finance's query:\n%s", block)
	}
	for _, want := range []string{"FROM sales", "legacy"} {
		if !strings.Contains(block, want) {
			t.Errorf("the prior-work block lost %q:\n%s", want, block)
		}
	}
	// And a turn with no agent reads all of it, as before.
	if all := RenderPriorWork(r.priorWork(context.Background(), "th-1")); !strings.Contains(all, "finance.ledger") {
		t.Error("an unscoped turn lost prior work")
	}
}

func TestToolWorkIsWrittenUnderTheAgentThatDidIt(t *testing.T) {
	mem := &fakeToolMemory{}
	r := runnerWithToolMemory(t, mem, 5)

	r.rememberToolWork(roomTurnCtx("ag-fin", "Finance"), queue.ChatRunPayload{ThreadID: "th-1", UserMsgID: "msg-1"},
		[]ToolDigest{{Tool: "run_sql", Query: "SELECT 1", Rows: 1}})

	if len(mem.appended) != 1 || mem.appended[0].AgentID != "ag-fin" {
		t.Fatalf("digest rows = %+v, want one under ag-fin", mem.appended)
	}
}
