package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	sdkagent "github.com/Ingenimax/agent-sdk-go/pkg/agent"
	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/Ingenimax/agent-sdk-go/pkg/memory"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/peermemory"
	"github.com/fauzanebd/argentum/internal/queue"
)

// T-N6 at the runner: which turns hold nudge_agent, what a colleague's question
// checks before it runs, and the two ways a colleague's turn ends without an
// answer.

// replyLLM answers every call with one reply, and records what it was asked and
// whether the turn's payload was on the context the tool would read.
type replyLLM struct {
	reply string

	mu      sync.Mutex
	inputs  []string
	sawTurn []queue.ChatRunPayload
}

func (l *replyLLM) Generate(ctx context.Context, prompt string, _ ...interfaces.GenerateOption) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.inputs = append(l.inputs, prompt)
	if p, ok := askingTurnFrom(ctx); ok {
		l.sawTurn = append(l.sawTurn, p)
	}
	return l.reply, nil
}

func (l *replyLLM) GenerateWithTools(ctx context.Context, prompt string, _ []interfaces.Tool, opts ...interfaces.GenerateOption) (string, error) {
	return l.Generate(ctx, prompt, opts...)
}
func (l *replyLLM) GenerateDetailed(context.Context, string, ...interfaces.GenerateOption) (*interfaces.LLMResponse, error) {
	panic("unexpected GenerateDetailed")
}
func (l *replyLLM) GenerateWithToolsDetailed(context.Context, string, []interfaces.Tool, ...interfaces.GenerateOption) (*interfaces.LLMResponse, error) {
	panic("unexpected GenerateWithToolsDetailed")
}
func (l *replyLLM) Name() string            { return "reply-stub" }
func (l *replyLLM) SupportsStreaming() bool { return false }

// seats is a room of two: Ops, the default speaker with no membership row, and
// Finance, added.
func seats() []*domain.ThreadParticipant {
	return []*domain.ThreadParticipant{
		{ThreadID: "th-1", AgentID: "ag-ops", AgentName: "Ops"},
		{ID: "tp-fin", ThreadID: "th-1", AgentID: "ag-fin", AgentName: "Finance"},
	}
}

// roomRunner runs whole turns against a stub model in a room, with Ops and
// Finance both allowed to ask.
func roomRunner(t *testing.T, llm interfaces.LLM, room ParticipantLister) (*ChatRunner, *capturingMessages, *AgentSpec) {
	t.Helper()
	msgs := &capturingMessages{}
	threads := quietThreadRepo{&fakeThreadRepo{latestErr: domain.ErrNotFound}}
	svc := NewThreadService(threads, msgs, nil, nil, ThreadServiceConfig{IdleMinutes: 30, SummaryEveryNTurns: 8})
	var got AgentSpec
	factory := func(spec AgentSpec) (*sdkagent.Agent, error) {
		got = spec
		return sdkagent.NewAgent(
			sdkagent.WithLLM(spec.Primary),
			sdkagent.WithSystemPrompt("You are Argentum."),
			sdkagent.WithMaxIterations(2),
		)
	}
	ops := agentRow("ag-ops", "Ops")
	ops.CanNudge = true
	fin := agentRow("ag-fin", "Finance")
	fin.CanNudge = true
	roster := &fakeRoster{byID: map[string]*domain.Agent{"ag-ops": ops, "ag-fin": fin}, def: ops}
	r := NewChatRunner(svc, msgs, threads, noConnections{}, factory, fixedLLM{llm}, &capturingBus{}, nil, nil, nil, 20).
		WithRoster(roster).
		WithRoom(room, 2)
	return r, msgs, &got
}

// finsQuestion is Ops asking Finance, as the nudge service queues it.
func finsQuestion() queue.ChatRunPayload {
	return queue.ChatRunPayload{
		CompanyID: "co-1", ThreadID: "th-1", Channel: domain.ChannelDashboard, UserMsgID: "msg-1",
		AgentID: "ag-fin", Message: "Was a goods-in posted for SKU 4471?",
		Peer: &queue.PeerOrigin{AgentID: "ag-ops", ParticipantID: "tp-fin", Depth: 1},
	}
}

func TestNudgeIsOfferedOnlyWhereBothGatesHold(t *testing.T) {
	ops := agentRow("ag-ops", "Ops")
	ops.CanNudge = true
	quiet := agentRow("ag-fin", "Finance")
	person := queue.ChatRunPayload{CompanyID: "co-1", ThreadID: "th-1"}
	peerAt := func(depth int) queue.ChatRunPayload {
		p := person
		p.Peer = &queue.PeerOrigin{AgentID: "ag-fin", Depth: depth}
		return p
	}

	for _, tc := range []struct {
		name  string
		room  *stubParticipants
		agent *domain.Agent
		p     queue.ChatRunPayload
		want  bool
		reads int
	}{
		{"may nudge, room of two", &stubParticipants{rows: seats()}, ops, person, true, 1},
		{"may not nudge — no read at all", &stubParticipants{rows: seats()}, quiet, person, false, 0},
		{"may nudge, room of one", &stubParticipants{rows: seats()[:1]}, ops, person, false, 1},
		{"unscoped turn", &stubParticipants{rows: seats()}, nil, person, false, 0},
		{"a hop that may still ask", &stubParticipants{rows: seats()}, ops, peerAt(1), true, 1},
		{"a hop whose ask could only be refused", &stubParticipants{rows: seats()}, ops, peerAt(2), false, 0},
		{"a room that cannot be read", &stubParticipants{err: errors.New("down")}, ops, person, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := (&ChatRunner{}).WithRoom(tc.room, 2)
			if got := r.offersNudge(context.Background(), tc.p, tc.agent); got != tc.want {
				t.Errorf("offersNudge = %v, want %v", got, tc.want)
			}
			if tc.room.reads != tc.reads {
				t.Errorf("room read %d time(s), want %d", tc.room.reads, tc.reads)
			}
		})
	}
	if (&ChatRunner{}).offersNudge(context.Background(), person, ops) {
		t.Error("a runner with no room wired offered nudge_agent")
	}
}

// The acceptance line through a whole turn: the same agent, allowed to nudge, is
// not offered the tool in a room of one and is in a room of two.
func TestATurnIsOfferedNudgeOnlyInARoomOfMoreThanOne(t *testing.T) {
	person := queue.ChatRunPayload{
		CompanyID: "co-1", ThreadID: "th-1", Channel: domain.ChannelDashboard,
		AgentID: "ag-ops", Message: "What happened to SKU 4471's stock?", UserMsgID: "msg-1",
	}
	for _, tc := range []struct {
		name string
		room []*domain.ThreadParticipant
		want bool
	}{{"room of one", seats()[:1], false}, {"room of two", seats(), true}} {
		r, _, spec := roomRunner(t, &replyLLM{reply: "Stock shows 0 since Tuesday."}, &stubParticipants{rows: tc.room})
		if err := r.Run(context.Background(), person); err != nil {
			t.Fatalf("%s: Run: %v", tc.name, err)
		}
		if spec.Nudge != tc.want {
			t.Errorf("%s: AgentSpec.Nudge = %v, want %v", tc.name, spec.Nudge, tc.want)
		}
	}
}

// The tool reads the turn it runs in off the context; the model's call is where
// that context has to have reached.
func TestTheToolCanReadTheTurnItRunsIn(t *testing.T) {
	llm := &replyLLM{reply: "Stock shows 0 since Tuesday."}
	r, _, _ := roomRunner(t, llm, &stubParticipants{rows: seats()})
	if err := r.Run(context.Background(), queue.ChatRunPayload{
		CompanyID: "co-1", ThreadID: "th-1", Channel: domain.ChannelDashboard,
		AgentID: "ag-ops", Message: "What happened to SKU 4471's stock?", UserMsgID: "msg-7",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(llm.sawTurn) == 0 || llm.sawTurn[0].UserMsgID != "msg-7" {
		t.Fatalf("the model call's context carried turns %+v, want msg-7's", llm.sawTurn)
	}
}

func TestAQuestionWhoseRecipientLeftIsNotRun(t *testing.T) {
	for _, tc := range []struct {
		name string
		room []*domain.ThreadParticipant
	}{
		{"removed from the room", seats()[:1]},
		{"the seat now holds another agent", []*domain.ThreadParticipant{
			seats()[0], {ID: "tp-fin", ThreadID: "th-1", AgentID: "ag-hr", AgentName: "People"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			llm := &replyLLM{reply: "One GRN on Tuesday."}
			r, msgs, _ := roomRunner(t, llm, &stubParticipants{rows: tc.room})

			if err := r.Run(context.Background(), finsQuestion()); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if len(llm.inputs) != 0 {
				t.Fatal("the model was called for a question whose recipient had left")
			}
			if len(msgs.appended) != 1 || msgs.appended[0].Metadata[RoomEventKey] != RoomEventWithdrawn {
				t.Fatalf("room lines = %+v, want one withdrawn line", msgs.appended)
			}
			want := `Finance left this conversation before answering the question from Ops: "Was a goods-in posted for SKU 4471?"`
			if msgs.appended[0].Content != want {
				t.Errorf("line = %q\nwant %q", msgs.appended[0].Content, want)
			}
			if msgs.appended[0].AgentID != "" {
				t.Errorf("the withdrawn line was written as agent %q — nobody ran", msgs.appended[0].AgentID)
			}
		})
	}
}

func TestAQuestionIsRunWhileItsSeatHolds(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    func() queue.ChatRunPayload
		room []*domain.ThreadParticipant
		ran  bool
	}{
		{"an added member still there", finsQuestion, seats(), true},
		{"the default speaker still speaking", func() queue.ChatRunPayload {
			p := finsQuestion()
			p.AgentID, p.Peer.AgentID, p.Peer.ParticipantID = "ag-ops", "ag-fin", ""
			return p
		}, seats(), true},
		{"the default speaker replaced", func() queue.ChatRunPayload {
			p := finsQuestion()
			p.AgentID, p.Peer.AgentID, p.Peer.ParticipantID = "ag-ops", "ag-fin", ""
			return p
		}, []*domain.ThreadParticipant{{ThreadID: "th-1", AgentID: "ag-fin", AgentName: "Finance"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			llm := &replyLLM{reply: "One GRN on Tuesday, 200 units."}
			r, _, _ := roomRunner(t, llm, &stubParticipants{rows: tc.room})
			if err := r.Run(context.Background(), tc.p()); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if ran := len(llm.inputs) > 0; ran != tc.ran {
				t.Errorf("ran = %v, want %v", ran, tc.ran)
			}
		})
	}
}

func TestAColleagueWithNothingToAddSettles(t *testing.T) {
	llm := &replyLLM{reply: "**PASS**"}
	r, msgs, _ := roomRunner(t, llm, &stubParticipants{rows: seats()})

	if err := r.Run(context.Background(), finsQuestion()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(msgs.appended) != 1 {
		t.Fatalf("appended %d messages, want 1", len(msgs.appended))
	}
	got := msgs.appended[0]
	if got.Content != "Finance had nothing to add to the question from Ops." {
		t.Errorf("the settle reads %q", got.Content)
	}
	if got.Metadata[RoomEventKey] != RoomEventSettle || got.AgentID != "ag-fin" {
		t.Errorf("the settle = %+v, want a settle line written as Finance", got)
	}
	if !strings.Contains(llm.inputs[0], "reply with exactly PASS") {
		t.Error("a colleague's question was not offered the pass")
	}
}

// The sentinel is a colleague's, and only on a colleague's question. A person who
// is answered "PASS" was answered.
func TestAPersonsTurnIsNeitherOfferedNorReadAsAPass(t *testing.T) {
	llm := &replyLLM{reply: "PASS"}
	r, msgs, _ := roomRunner(t, llm, &stubParticipants{rows: seats()})

	if err := r.Run(context.Background(), queue.ChatRunPayload{
		CompanyID: "co-1", ThreadID: "th-1", Channel: domain.ChannelDashboard,
		AgentID: "ag-ops", Message: "Which exam code did the auditor say to use?", UserMsgID: "msg-1",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(llm.inputs[0], "PASS") {
		t.Error("a person's turn was offered the pass")
	}
	if len(msgs.appended) != 1 || msgs.appended[0].Content != "PASS" || isRoomEvent(msgs.appended[0].Metadata) {
		t.Errorf("a person's answer was read as a pass: %+v", msgs.appended)
	}
}

func TestThePassSentinelSurvivesDecorationAndNothingElse(t *testing.T) {
	for reply, want := range map[string]bool{
		"PASS": true, "pass": true, " **PASS**. ": true, "`PASS`": true, `"Pass!"`: true,
		"PASS — nothing from Finance's side": false, "I'll pass on that": false, "": false,
	} {
		if got := isPass(reply); got != want {
			t.Errorf("isPass(%q) = %v, want %v", reply, got, want)
		}
	}
}

// A room's own lines are the product's sentences. Replayed, "→ Finance: …" is
// something the agent reads as having once written — and learns to type.
func TestARoomLineIsNeverReplayedIntoHistory(t *testing.T) {
	r := &ChatRunner{historyLimit: 20, messages: messagesWithRows{rows: []*domain.Message{
		{ID: "u1", Role: domain.MessageRoleUser, Content: "We're short on SKU 4471, what happened?"},
		{ID: "a1", Role: domain.MessageRoleAssistant, Content: "→ Finance: was a goods-in posted?",
			AgentID: "ag-ops", Metadata: map[string]any{RoomEventKey: RoomEventNudge}},
		{ID: "a2", Role: domain.MessageRoleAssistant, Content: "Stock shows 0 since Tuesday; I asked Finance.", AgentID: "ag-ops"},
		{ID: "a3", Role: domain.MessageRoleAssistant, Content: "Finance had nothing to add to the question from Ops.",
			AgentID: "ag-fin", Metadata: map[string]any{RoomEventKey: RoomEventSettle}},
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
		t.Fatalf("replayed %d messages, want the person's and Ops' answer: %+v", len(got), got)
	}
	for _, m := range got {
		if strings.Contains(m.Content, "→ Finance") || strings.Contains(m.Content, "nothing to add") {
			t.Errorf("a room line was replayed: %q", m.Content)
		}
	}
}
