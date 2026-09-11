package app

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
)

// T-N3's integration half: one user message becomes N turns, and the two ways
// an `@` can fail need different answers.

// stubRoom is a RoomReader over fixed slices.
type stubRoom struct {
	participants []*domain.ThreadParticipant
	agents       []*domain.Agent
	partErr      error
	agentsErr    error
	agentCalls   int
}

func (s *stubRoom) ListParticipants(context.Context, string, string) ([]*domain.ThreadParticipant, error) {
	return s.participants, s.partErr
}

func (s *stubRoom) ListAgents(context.Context, string) ([]*domain.Agent, error) {
	s.agentCalls++
	return s.agents, s.agentsErr
}

func threeAgentRoom() *stubRoom {
	return &stubRoom{
		participants: []*domain.ThreadParticipant{
			{AgentID: "ag-fin", AgentName: "Finance"},
			{AgentID: "ag-ops", AgentName: "Ops"},
		},
		agents: []*domain.Agent{
			{ID: "ag-fin", Name: "Finance", Enabled: true},
			{ID: "ag-ops", Name: "Ops", Enabled: true},
			{ID: "ag-legal", Name: "Legal", Enabled: true},
		},
	}
}

func addressingInput(msg string) ChatInput {
	return ChatInput{Channel: domain.ChannelDashboard, CompanyID: "co-1", UserID: "u-1", Message: msg}
}

func thread1() *domain.ConversationThread {
	return &domain.ConversationThread{ID: "th-1", CompanyID: "co-1", AgentID: "ag-fin"}
}

func TestAnUnaddressedMessageResolvesToNobodyInParticular(t *testing.T) {
	enq := (&ChatEnqueuer{}).WithRoom(threeAgentRoom())

	got, err := enq.resolveAddressing(context.Background(),
		addressingInput("what happened to margin?"), thread1())

	if err != nil {
		t.Fatalf("resolveAddressing: %v", err)
	}
	if len(got.AgentIDs) != 0 {
		t.Errorf("AgentIDs = %v, want none — the caller turns this into the thread's agent", got.AgentIDs)
	}
}

func TestTwoAddressedAgentsResolveToTwo(t *testing.T) {
	enq := (&ChatEnqueuer{}).WithRoom(threeAgentRoom())

	got, err := enq.resolveAddressing(context.Background(),
		addressingInput("@Ops @Finance what happened?"), thread1())

	if err != nil {
		t.Fatalf("resolveAddressing: %v", err)
	}
	if len(got.AgentIDs) != 2 || got.AgentIDs[0] != "ag-ops" || got.AgentIDs[1] != "ag-fin" {
		t.Errorf("AgentIDs = %v, want [ag-ops ag-fin]", got.AgentIDs)
	}
	if got.Cleaned != "what happened?" {
		t.Errorf("Cleaned = %q, want the @ tokens stripped", got.Cleaned)
	}
}

// The refusal that matters. Legal is on the roster and not in the room: the
// user addressed a real agent, and answering as the default speaker would be
// the "the answer came from the wrong agent" failure T-S3 refused to ship.
func TestARosterAgentNotInTheRoomIsRefused(t *testing.T) {
	enq := (&ChatEnqueuer{}).WithRoom(threeAgentRoom())

	_, err := enq.resolveAddressing(context.Background(),
		addressingInput("@Legal is this contract fine?"), thread1())

	if !errors.Is(err, ErrAgentNotInRoom) {
		t.Fatalf("resolveAddressing = %v, want ErrAgentNotInRoom", err)
	}
	if !contains(err.Error(), "Legal") {
		t.Errorf("refusal %q does not name the agent", err)
	}
}

// A name nobody has is ordinary text and must not refuse anything.
func TestAnUnknownHandleIsText(t *testing.T) {
	enq := (&ChatEnqueuer{}).WithRoom(threeAgentRoom())

	got, err := enq.resolveAddressing(context.Background(),
		addressingInput("@notanagent what happened?"), thread1())

	if err != nil {
		t.Fatalf("resolveAddressing: %v", err)
	}
	if len(got.AgentIDs) != 0 {
		t.Errorf("AgentIDs = %v, want none", got.AgentIDs)
	}
	if got.Cleaned != "@notanagent what happened?" {
		t.Errorf("Cleaned = %q, want the text unchanged", got.Cleaned)
	}
}

// A disabled agent is not a refusal either: it is not reachable, so addressing
// it is text. The alternative tells a user to add an agent they cannot add.
func TestADisabledRosterAgentIsText(t *testing.T) {
	room := threeAgentRoom()
	room.agents[2].Enabled = false
	enq := (&ChatEnqueuer{}).WithRoom(room)

	_, err := enq.resolveAddressing(context.Background(),
		addressingInput("@Legal is this contract fine?"), thread1())

	if err != nil {
		t.Errorf("resolveAddressing = %v, want no error for a disabled agent", err)
	}
}

// The roster listing is off the hot path: it is read only when an `@` matched
// no participant.
func TestTheRosterIsNotReadForAnOrdinaryMessage(t *testing.T) {
	room := threeAgentRoom()
	enq := (&ChatEnqueuer{}).WithRoom(room)

	if _, err := enq.resolveAddressing(context.Background(),
		addressingInput("what happened to margin?"), thread1()); err != nil {
		t.Fatalf("resolveAddressing: %v", err)
	}
	if _, err := enq.resolveAddressing(context.Background(),
		addressingInput("@Ops what happened?"), thread1()); err != nil {
		t.Fatalf("resolveAddressing: %v", err)
	}

	if room.agentCalls != 0 {
		t.Errorf("roster read %d times for messages that needed no disambiguation", room.agentCalls)
	}
}

// A room of one cannot be addressed: there is nobody else to pick.
func TestARoomOfOneIsNotAddressable(t *testing.T) {
	enq := (&ChatEnqueuer{}).WithRoom(&stubRoom{
		participants: []*domain.ThreadParticipant{{AgentID: "ag-fin", AgentName: "Finance"}},
	})

	got, err := enq.resolveAddressing(context.Background(),
		addressingInput("@Finance what happened?"), thread1())

	if err != nil {
		t.Fatalf("resolveAddressing: %v", err)
	}
	if len(got.AgentIDs) != 0 || got.Cleaned != "@Finance what happened?" {
		t.Errorf("got %+v, want the message untouched", got)
	}
}

// Addressing is a dashboard affordance. /v1 and the widget carry an explicit
// agent_id; a machine caller's message text is not a place to look for routing.
func TestAddressingIsDashboardOnly(t *testing.T) {
	enq := (&ChatEnqueuer{}).WithRoom(threeAgentRoom())

	for _, ch := range []domain.Channel{
		domain.ChannelAPI, domain.ChannelWidget, domain.ChannelSlack,
		domain.ChannelDiscord, domain.ChannelLark, domain.ChannelWhatsApp,
	} {
		in := addressingInput("@Ops what happened?")
		in.Channel = ch
		got, err := enq.resolveAddressing(context.Background(), in, thread1())
		if err != nil {
			t.Fatalf("%s: %v", ch, err)
		}
		if len(got.AgentIDs) != 0 {
			t.Errorf("%s addressed %v; only the dashboard parses @", ch, got.AgentIDs)
		}
		if got.Cleaned != "@Ops what happened?" {
			t.Errorf("%s stripped the @ from a message it does not route", ch)
		}
	}
}

// An enqueuer with no room behaves exactly as it did before this ticket.
func TestNoRoomMeansNoAddressing(t *testing.T) {
	enq := &ChatEnqueuer{}

	got, err := enq.resolveAddressing(context.Background(),
		addressingInput("@Ops what happened?"), thread1())

	if err != nil {
		t.Fatalf("resolveAddressing: %v", err)
	}
	if len(got.AgentIDs) != 0 || got.Cleaned != "@Ops what happened?" {
		t.Errorf("got %+v, want the message untouched", got)
	}
}

// A failed membership read degrades to today's behaviour rather than refusing
// the turn. Context makes an answer better; it is never what makes one possible.
func TestAFailedParticipantLookupDoesNotRefuseTheTurn(t *testing.T) {
	enq := (&ChatEnqueuer{}).WithRoom(&stubRoom{partErr: errors.New("db is down")})

	got, err := enq.resolveAddressing(context.Background(),
		addressingInput("@Ops what happened?"), thread1())

	if err != nil {
		t.Fatalf("resolveAddressing = %v, want the turn to proceed", err)
	}
	if len(got.AgentIDs) != 0 {
		t.Errorf("AgentIDs = %v, want none", got.AgentIDs)
	}
}

// So does a failed roster read — it can only downgrade a refusal into text.
func TestAFailedRosterLookupDoesNotRefuseTheTurn(t *testing.T) {
	room := threeAgentRoom()
	room.agentsErr = errors.New("db is down")
	enq := (&ChatEnqueuer{}).WithRoom(room)

	_, err := enq.resolveAddressing(context.Background(),
		addressingInput("@Legal is this fine?"), thread1())

	if err != nil {
		t.Errorf("resolveAddressing = %v, want the turn to proceed", err)
	}
}

// --- the fan-out ------------------------------------------------------------

// recordingEnqueuer captures every payload the fan-out produces.
type recordingEnqueuer struct {
	payloads []queue.ChatRunPayload
	failAt   int // 1-based; 0 never fails
}

func (r *recordingEnqueuer) EnqueueChatRun(_ context.Context, p queue.ChatRunPayload) (string, error) {
	if r.failAt > 0 && len(r.payloads)+1 == r.failAt {
		return "", errors.New("redis is down")
	}
	r.payloads = append(r.payloads, p)
	return fmt.Sprintf("task-%d", len(r.payloads)), nil
}

func fanOutFixture(t *testing.T, room *stubRoom) (*ChatEnqueuer, *recordingEnqueuer) {
	t.Helper()
	enqueuer := &recordingEnqueuer{}
	threads := quietThreadRepo{&fakeThreadRepo{
		byID: map[string]*domain.ConversationThread{"th-1": thread1()},
	}}
	svc := NewThreadService(threads, stubMessages{}, nil, nil, ThreadServiceConfig{
		IdleMinutes: 30, SummaryEveryNTurns: 8,
	})
	// fakeCompanies rather than nil: Enqueue reads the company for the agent's
	// name-and-currency context, and production always wires one.
	enq := NewChatEnqueuer(svc, stubMessages{}, fakeCompanies{}, enqueuer).
		WithRoster(&stubDefaultAgent{agent: &domain.Agent{ID: "ag-fin"}}).
		WithRoom(room)
	return enq, enqueuer
}

func dashboardSend(msg string) ChatInput {
	in := addressingInput(msg)
	in.ThreadID = "th-1"
	return in
}

// The ticket's central claim: one user message, N turns, one UserMsgID.
func TestOneMessageAddressingTwoAgentsProducesTwoTurns(t *testing.T) {
	enq, q := fanOutFixture(t, threeAgentRoom())

	res, err := enq.Enqueue(context.Background(), dashboardSend("@Ops @Finance what happened?"))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if len(q.payloads) != 2 {
		t.Fatalf("enqueued %d turns, want 2", len(q.payloads))
	}
	if q.payloads[0].AgentID != "ag-ops" || q.payloads[1].AgentID != "ag-fin" {
		t.Errorf("agents = %q, %q; want ag-ops then ag-fin, the order addressed",
			q.payloads[0].AgentID, q.payloads[1].AgentID)
	}
	if q.payloads[0].UserMsgID != q.payloads[1].UserMsgID {
		t.Error("two turns of one message carry different UserMsgIDs; ChatEvent.JobID would not group them")
	}
	for i, p := range q.payloads {
		if p.Message != "what happened?" {
			t.Errorf("payload %d message = %q, want the @ tokens stripped", i, p.Message)
		}
	}
	if len(res.TaskIDs) != 2 || res.TaskID != res.TaskIDs[0] {
		t.Errorf("TaskIDs = %v, TaskID = %q", res.TaskIDs, res.TaskID)
	}
	if len(res.AddressedAgentIDs) != 2 {
		t.Errorf("AddressedAgentIDs = %v, want two", res.AddressedAgentIDs)
	}
}

// The negative that matters most: an ordinary message is byte-identical to
// what it produced before this ticket.
func TestAnUnaddressedMessageStillProducesExactlyOneTurn(t *testing.T) {
	enq, q := fanOutFixture(t, threeAgentRoom())

	res, err := enq.Enqueue(context.Background(), dashboardSend("what happened to margin?"))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if len(q.payloads) != 1 {
		t.Fatalf("enqueued %d turns, want 1", len(q.payloads))
	}
	if q.payloads[0].AgentID != "ag-fin" {
		t.Errorf("agent = %q, want the thread's own agent", q.payloads[0].AgentID)
	}
	if q.payloads[0].Message != "what happened to margin?" {
		t.Errorf("message = %q, want it unchanged", q.payloads[0].Message)
	}
	if len(res.AddressedAgentIDs) != 0 {
		t.Errorf("AddressedAgentIDs = %v, want none", res.AddressedAgentIDs)
	}
}

// @all in a two-agent room is two turns.
func TestAddressingEveryoneProducesOneTurnPerParticipant(t *testing.T) {
	enq, q := fanOutFixture(t, threeAgentRoom())

	if _, err := enq.Enqueue(context.Background(), dashboardSend("@all thoughts?")); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if len(q.payloads) != 2 {
		t.Fatalf("enqueued %d turns, want 2 — one per participant", len(q.payloads))
	}
}

// A refused `@` enqueues nothing at all. The room is unchanged and no turn is
// billed for a message that did not reach who it named.
func TestARefusedAddressEnqueuesNothing(t *testing.T) {
	enq, q := fanOutFixture(t, threeAgentRoom())

	_, err := enq.Enqueue(context.Background(), dashboardSend("@Legal is this fine?"))

	if !errors.Is(err, ErrAgentNotInRoom) {
		t.Fatalf("Enqueue = %v, want ErrAgentNotInRoom", err)
	}
	if len(q.payloads) != 0 {
		t.Errorf("enqueued %d turns for a refused message, want 0", len(q.payloads))
	}
}

// A queue failure partway through reports how far it got. The turns already
// queued are real turns the user asked for and keep running.
func TestAPartialFanOutSaysSo(t *testing.T) {
	enq, q := fanOutFixture(t, threeAgentRoom())
	q.failAt = 2

	_, err := enq.Enqueue(context.Background(), dashboardSend("@Ops @Finance what happened?"))

	if err == nil {
		t.Fatal("Enqueue = nil error, want the partial failure reported")
	}
	if !contains(err.Error(), "1 of 2") {
		t.Errorf("err = %q, want it to say how many agents were reached", err)
	}
	if len(q.payloads) != 1 {
		t.Errorf("enqueued %d turns, want the first to have survived", len(q.payloads))
	}
}
