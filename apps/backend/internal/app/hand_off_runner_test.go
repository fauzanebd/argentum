package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	sdkagent "github.com/Ingenimax/agent-sdk-go/pkg/agent"
	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/Ingenimax/agent-sdk-go/pkg/memory"

	"github.com/fauzanebd/argentum/internal/agentbudget"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/guardrails"
	"github.com/fauzanebd/argentum/internal/peermemory"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/tools"
)

// T-N7 at the runner: a turn that hands its question on ends on the hand-off, a
// turn handed one reads the person's words with the reason fenced, and only a
// turn holding the person's question is offered the tool at all.

// handingLLM hands the question on through the real tool and then answers it
// anyway — or fails — ignoring the result: the model the runner has to make
// harmless, rather than the one the tool result asks for.
type handingLLM struct {
	agent, reason string
	answer        string
	err           error

	mu      sync.Mutex
	results []string
}

func (l *handingLLM) Generate(ctx context.Context, prompt string, opts ...interfaces.GenerateOption) (string, error) {
	return l.GenerateWithTools(ctx, prompt, nil, opts...)
}

func (l *handingLLM) GenerateWithTools(ctx context.Context, _ string, ts []interfaces.Tool, _ ...interfaces.GenerateOption) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, t := range ts {
		if t.Name() != tools.HandOffAgentName {
			continue
		}
		args, _ := json.Marshal(map[string]string{"agent": l.agent, "reason": l.reason})
		res, err := t.Execute(ctx, string(args))
		if err != nil {
			return "", err
		}
		l.results = append(l.results, res)
	}
	return l.answer, l.err
}
func (l *handingLLM) GenerateDetailed(context.Context, string, ...interfaces.GenerateOption) (*interfaces.LLMResponse, error) {
	panic("unexpected GenerateDetailed")
}
func (l *handingLLM) GenerateWithToolsDetailed(context.Context, string, []interfaces.Tool, ...interfaces.GenerateOption) (*interfaces.LLMResponse, error) {
	panic("unexpected GenerateWithToolsDetailed")
}
func (l *handingLLM) Name() string            { return "handing-stub" }
func (l *handingLLM) SupportsStreaming() bool { return false }

// handOffRunner runs whole turns in nudgeFixture's room, with the real service
// behind the tool and the real thread service writing what it writes.
func handOffRunner(t *testing.T, llm interfaces.LLM) (*ChatRunner, *capturingMessages, *capturingBus, *nudgeRoom) {
	t.Helper()
	f := nudgeFixture(t, agentbudget.Ceilings{})
	msgs := &capturingMessages{}
	bus := &capturingBus{}
	threads := quietThreadRepo{&fakeThreadRepo{latestErr: domain.ErrNotFound}}
	svc := NewThreadService(threads, msgs, nil, nil, ThreadServiceConfig{IdleMinutes: 30, SummaryEveryNTurns: 8})
	f.svc.notes = svc
	f.svc.SetBus(bus)
	factory := func(spec AgentSpec) (*sdkagent.Agent, error) {
		// WithRequirePlanApproval(false), as newAgentFactory sets it: an agent holding
		// a tool otherwise asks the model for an execution plan before its first call.
		opts := []sdkagent.Option{
			sdkagent.WithLLM(spec.Primary), sdkagent.WithSystemPrompt("You are Argentum."), sdkagent.WithMaxIterations(2),
			sdkagent.WithRequirePlanApproval(false),
		}
		if spec.HandOff {
			opts = append(opts, sdkagent.WithTools(tools.NewHandOffAgentTool(f.svc)))
		}
		return sdkagent.NewAgent(opts...)
	}
	r := NewChatRunner(svc, msgs, threads, noConnections{}, factory, fixedLLM{llm}, bus, nil, nil, nil, 20).
		WithRoster(f.roster).
		WithRoom(f.room, 2)
	return r, msgs, bus, f
}

// personsTurn is the person's message to Ops on the dashboard, and nothing else.
func personsTurn() queue.ChatRunPayload {
	return queue.ChatRunPayload{
		CompanyID: "co-1", ThreadID: "th-1", UserID: "user-1", Channel: domain.ChannelDashboard,
		AgentID: "ag-ops", Message: writeOffs, UserMsgID: "msg-1",
	}
}

// handedToFinance is that message, as HandOff queues it for Finance.
func handedToFinance(depth int) queue.ChatRunPayload {
	p := personsTurn()
	p.AgentID = "ag-fin"
	p.Peer = &queue.PeerOrigin{
		AgentID: "ag-ops", ParticipantID: "tp-fin", Depth: depth, HandOff: &queue.HandOff{Reason: becauseLedger},
	}
	return p
}

func finals(bus *capturingBus) []ChatEvent {
	var out []ChatEvent
	for _, e := range bus.events {
		if e.Type == "final" || e.Type == "error" {
			out = append(out, e)
		}
	}
	return out
}

// "The handing agent's reply says it handed off and attempts no answer" — with a
// model that attempts one. The room reads the hand-off, the `final` closes Ops'
// turn on it, and the figure the model wrote afterwards goes nowhere.
func TestAHandedOffTurnEndsOnTheHandOffAndPublishesNothingItWroteAfter(t *testing.T) {
	llm := &handingLLM{agent: "Finance", reason: becauseLedger, answer: "We wrote off Rp 3.200.000 for SKU 4471."}
	r, msgs, bus, f := handOffRunner(t, llm)

	if err := r.Run(context.Background(), personsTurn()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(llm.results) != 1 || !strings.Contains(llm.results[0], `"handed_off":true`) {
		t.Fatalf("the hand-off did not go through: %v", llm.results)
	}
	if len(msgs.appended) != 1 {
		t.Fatalf("wrote %d messages, want the hand-off alone: %+v", len(msgs.appended), msgs.appended)
	}
	reply := msgs.appended[0]
	if reply.Content != "Passed to Finance: "+becauseLedger || reply.AgentID != "ag-ops" || !isHandOff(reply.Metadata) {
		t.Errorf("Ops' reply = %+v, want the hand-off, as Ops", reply)
	}
	ends := finals(bus)
	if len(ends) != 1 || ends[0].Type != "final" || ends[0].Content != reply.Content || ends[0].AgentID != "ag-ops" {
		t.Errorf("the turn ended with %+v, want one final carrying the hand-off", ends)
	}
	for _, e := range bus.events {
		if strings.Contains(e.Content, "3.200.000") {
			t.Errorf("the answer the handing model wrote anyway was published as %q", e.Type)
		}
	}
	if len(f.queue.payloads) != 1 || f.queue.payloads[0].Message != writeOffs || f.queue.payloads[0].AgentID != "ag-fin" {
		t.Errorf("queued %+v, want Finance handed the person's words", f.queue.payloads)
	}
}

// A model that fails after the colleague's turn was queued has still handed the
// question over. The person reads the hand-off, not an error for a turn that did
// what it was for.
func TestAHandedOffTurnWhoseModelThenFailsStillEndsOnTheHandOff(t *testing.T) {
	llm := &handingLLM{agent: "Finance", reason: becauseLedger, err: errors.New("provider dropped the connection")}
	r, msgs, bus, _ := handOffRunner(t, llm)

	if err := r.Run(context.Background(), personsTurn()); err != nil {
		t.Fatalf("Run: %v — a turn that had handed off was retried", err)
	}
	ends := finals(bus)
	if len(ends) != 1 || ends[0].Type != "final" || len(msgs.appended) != 1 {
		t.Errorf("ended with %+v after writing %d messages, want one final on the hand-off", ends, len(msgs.appended))
	}
}

// "The handed-off agent receives the user's original wording, fenced, with the
// reason attached" — built with one change, argued at peerMessage: the reason is
// fenced under the handing agent's name, and the person's words, which no model
// wrote, arrive as a person's words do.
func TestAHandedOffQuestionArrivesInThePersonsWordsWithTheReasonFenced(t *testing.T) {
	llm := &replyLLM{reply: "Rp 3.200.000 was written off for SKU 4471 in Q2."}
	r, msgs, _ := roomRunner(t, llm, &stubParticipants{rows: seats()})

	if err := r.Run(context.Background(), handedToFinance(1)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	in := llm.inputs[0]
	if !strings.Contains(in, guardrails.FencePeer("Ops", becauseLedger)+"\n\n"+writeOffs) {
		t.Errorf("Finance did not read Ops' reason fenced and the person's words after it:\n%s", in)
	}
	if strings.Contains(in, guardrails.FencePeer("Ops", writeOffs)) {
		t.Error("the person's words were fenced as Ops'")
	}
	if !strings.Contains(in, "passed the person's question to you") || strings.Contains(in, "reply with exactly PASS") {
		t.Errorf("a handed-off question was framed as a colleague's question:\n%s", in)
	}
	if len(msgs.appended) != 1 || msgs.appended[0].AgentID != "ag-fin" || isRoomEvent(msgs.appended[0].Metadata) {
		t.Errorf("Finance's answer = %+v, want an ordinary answer as Finance", msgs.appended)
	}
}

func TestOnlyATurnHoldingThePersonsQuestionIsOfferedTheHandOff(t *testing.T) {
	for _, tc := range []struct {
		name           string
		p              queue.ChatRunPayload
		nudge, handOff bool
	}{
		{"the person's own message", personsTurn(), true, true},
		{"a colleague's question", finsQuestion(), true, false},
		{"the person's question, handed on", handedToFinance(1), true, true},
		{"handed on at the last hop", handedToFinance(2), false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _, spec := roomRunner(t, &replyLLM{reply: "Checked."}, &stubParticipants{rows: seats()})
			if err := r.Run(context.Background(), tc.p); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if spec.Nudge != tc.nudge || spec.HandOff != tc.handOff {
				t.Errorf("Nudge, HandOff = %v, %v; want %v, %v", spec.Nudge, spec.HandOff, tc.nudge, tc.handOff)
			}
		})
	}
}

// The hand-off is the product's sentence, stored as an answer. Replayed, "Passed to
// Finance" is something Ops reads as having written — and learns to write instead
// of handing the question over.
func TestAHandOffIsNeverReplayedIntoHistory(t *testing.T) {
	r := &ChatRunner{historyLimit: 20, messages: messagesWithRows{rows: []*domain.Message{
		{ID: "u1", Role: domain.MessageRoleUser, Content: writeOffs},
		{ID: "a1", Role: domain.MessageRoleAssistant, Content: "Passed to Finance: " + becauseLedger,
			AgentID: "ag-ops", Metadata: map[string]any{HandedOffToKey: "ag-fin"}},
		{ID: "a2", Role: domain.MessageRoleAssistant, Content: "Rp 3.200.000 was written off.", AgentID: "ag-fin", AgentName: "Finance"},
	}}}
	agent, err := sdkagent.NewAgent(
		sdkagent.WithLLM(&historyLLM{}),
		sdkagent.WithMemory(peermemory.Wrap(memory.NewConversationBuffer())),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := roomTurnCtx("ag-ops", "Ops")

	if err := r.hydrateMemory(ctx, agent, queue.ChatRunPayload{ThreadID: "th-1", UserMsgID: "u2"}); err != nil {
		t.Fatalf("hydrate: %v", err)
	}
	got, err := agent.GetMemory().GetMessages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("replayed %d messages, want the person's and Finance's: %+v", len(got), got)
	}
	for _, m := range got {
		if strings.Contains(m.Content, "Passed to Finance") {
			t.Errorf("the hand-off was replayed: %q", m.Content)
		}
	}
}
