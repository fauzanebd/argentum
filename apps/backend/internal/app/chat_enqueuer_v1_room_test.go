package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// T-N10 on the enqueue path. A `/v1` caller has no `@`, so it addresses a room
// with the field it already had — `agent_id` — and opens one before its first
// question with `POST /v1/threads`.

// opsRoom is a conversation over the API whose own agent is Ops and which
// Finance has joined. Legal is on the roster and not in the room.
func opsRoom() *stubRoom {
	return &stubRoom{participants: []*domain.ThreadParticipant{
		{AgentID: "ag-ops", AgentName: "Ops"},
		{AgentID: "ag-fin", AgentName: "Finance"},
	}}
}

func apiRoomFixture(t *testing.T, room *stubRoom) (*ChatEnqueuer, *recordingEnqueuer, *fakeThreadRepo, *stubDefaultAgent) {
	t.Helper()
	q := &recordingEnqueuer{}
	repo := &fakeThreadRepo{byID: map[string]*domain.ConversationThread{
		"th-api": {
			ID: "th-api", CompanyID: "co-1", Channel: domain.ChannelAPI,
			APIUserRef: "their-user-42", AgentID: "ag-ops", LastMessageAt: time.Now(),
		},
	}}
	svc := NewThreadService(quietThreadRepo{repo}, stubMessages{}, nil, nil, ThreadServiceConfig{
		IdleMinutes: 30, SummaryEveryNTurns: 8,
	})
	roster := rosterWith(
		&domain.Agent{ID: "ag-ops", CompanyID: "co-1", Name: "Ops", Enabled: true},
		&domain.Agent{ID: "ag-fin", CompanyID: "co-1", Name: "Finance", Enabled: true},
		&domain.Agent{ID: "ag-legal", CompanyID: "co-1", Name: "Legal", Enabled: true},
	)
	enq := NewChatEnqueuer(svc, stubMessages{}, fakeCompanies{}, q).WithRoster(roster)
	if room != nil {
		enq = enq.WithRoom(room)
	}
	return enq, q, repo, roster
}

func apiSend(agentID, msg string) ChatInput {
	return ChatInput{
		Channel: domain.ChannelAPI, CompanyID: "co-1", ThreadID: "th-api",
		AgentID: agentID, Message: msg, APIKeyID: "key-1",
	}
}

// The ticket's `agent_id`, in a room: it names who answers. And the `@` in the
// text is text — neither parsed nor stripped — which is the acceptance item
// "POST /v1/chat does not parse @ from message text" on the path that runs.
func TestAnAgentIDNamingAParticipantAddressesIt(t *testing.T) {
	enq, q, _, _ := apiRoomFixture(t, opsRoom())

	res, err := enq.Enqueue(context.Background(), apiSend("ag-fin", "@Ops was a goods-in posted for SKU 4471?"))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if len(q.payloads) != 1 {
		t.Fatalf("enqueued %d turns, want 1", len(q.payloads))
	}
	if q.payloads[0].AgentID != "ag-fin" {
		t.Errorf("agent = %q, want ag-fin — the participant the caller named", q.payloads[0].AgentID)
	}
	if q.payloads[0].Message != "@Ops was a goods-in posted for SKU 4471?" {
		t.Errorf("message = %q, want the text untouched: /v1 does not read @", q.payloads[0].Message)
	}
	if len(res.AddressedAgentIDs) != 1 || res.AddressedAgentIDs[0] != "ag-fin" {
		t.Errorf("AddressedAgentIDs = %v, want [ag-fin]", res.AddressedAgentIDs)
	}
	if len(res.AgentIDs) != 1 || res.AgentIDs[0] != "ag-fin" {
		t.Errorf("AgentIDs = %v, want [ag-fin] — what the handler scopes the stream by", res.AgentIDs)
	}
}

// A message naming nobody goes to the conversation's own agent, `@` or not.
func TestAnAPIMessageNamingNobodyGoesToTheConversationsOwnAgent(t *testing.T) {
	enq, q, _, _ := apiRoomFixture(t, opsRoom())

	res, err := enq.Enqueue(context.Background(), apiSend("", "@Finance how many SKUs are below reorder level?"))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if len(q.payloads) != 1 || q.payloads[0].AgentID != "ag-ops" {
		t.Fatalf("payloads = %+v, want one turn for ag-ops", q.payloads)
	}
	if len(res.AddressedAgentIDs) != 0 {
		t.Errorf("AddressedAgentIDs = %v, want none — an @ in /v1 text addresses nobody", res.AddressedAgentIDs)
	}
	if len(res.AgentIDs) != 1 || res.AgentIDs[0] != "ag-ops" {
		t.Errorf("AgentIDs = %v, want [ag-ops]", res.AgentIDs)
	}
}

// The T-S5 refusal survives for everything that is not a participant: an agent
// the room does not hold, a room of one, and a deployment with no room wired.
// "A /v1 caller that ignores participants sees no change."
func TestAnAgentIDOutsideTheRoomIsStillAChangeOfAgent(t *testing.T) {
	cases := map[string]struct {
		room  *stubRoom
		agent string
	}{
		"on the roster, not in the room": {room: opsRoom(), agent: "ag-legal"},
		"a room of one":                  {room: &stubRoom{participants: []*domain.ThreadParticipant{{AgentID: "ag-ops"}}}, agent: "ag-fin"},
		"no room wired":                  {room: nil, agent: "ag-fin"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			enq, q, _, _ := apiRoomFixture(t, tc.room)

			_, err := enq.Enqueue(context.Background(), apiSend(tc.agent, "and December?"))

			if !errors.Is(err, ErrAgentChange) {
				t.Fatalf("Enqueue = %v, want ErrAgentChange", err)
			}
			if len(q.payloads) != 0 {
				t.Errorf("enqueued %d turn(s) for a refused call", len(q.payloads))
			}
		})
	}
}

// A room that cannot be read refuses a message that named an agent. The
// dashboard's `@` degrades to the default speaker on the same failure because
// an unmatched `@` is text; an explicit field is not, and answering it as
// another agent is the failure T-S3 refused to ship.
func TestAnAddressedAPIMessageIsRefusedWhenTheRoomCannotBeRead(t *testing.T) {
	enq, q, _, _ := apiRoomFixture(t, &stubRoom{partErr: errors.New("db is down")})

	_, err := enq.Enqueue(context.Background(), apiSend("ag-fin", "and December?"))

	if err == nil || errors.Is(err, ErrAgentChange) {
		t.Fatalf("Enqueue = %v, want the read failure, not an agent change", err)
	}
	if len(q.payloads) != 0 {
		t.Errorf("enqueued %d turn(s) against a room nobody could read", len(q.payloads))
	}
}

// A key limited to named agents addresses only those: a participant off its
// list is the same not-found as any pick off it (T-Z8).
func TestAKeyLimitedToNamedAgentsCannotAddressAnotherParticipant(t *testing.T) {
	enq, q, _, _ := apiRoomFixture(t, opsRoom())
	in := apiSend("ag-fin", "and December?")
	in.KeyAgentIDs = []string{"ag-ops"}

	_, err := enq.Enqueue(context.Background(), in)

	if !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("Enqueue = %v, want ErrAgentNotFound", err)
	}
	if len(q.payloads) != 0 {
		t.Errorf("enqueued %d turn(s) for an agent the key may not use", len(q.payloads))
	}
}

// --- POST /v1/threads -------------------------------------------------------

func TestOpeningAnAPIRoomPinsItsOwnAgentAndWritesOneConversation(t *testing.T) {
	enq, _, repo, _ := apiRoomFixture(t, nil)

	thread, err := enq.OpenAPIThread(context.Background(), APIThreadInput{
		CompanyID: "co-1", APIUserRef: "their-user-42", AgentID: "ag-ops",
		ParticipantIDs: []string{"ag-fin"}, APIKeyID: "key-1",
	})
	if err != nil {
		t.Fatalf("OpenAPIThread: %v", err)
	}

	if len(repo.created) != 1 {
		t.Fatalf("created %d conversations, want 1", len(repo.created))
	}
	if thread.AgentID != "ag-ops" || thread.Channel != domain.ChannelAPI || thread.APIUserRef != "their-user-42" {
		t.Errorf("thread = %+v, want an api conversation for their-user-42 pinned to ag-ops", thread)
	}
}

// Every agent is checked before the row is written, so a refused room leaves no
// conversation in `GET /v1/threads`. And the refusal says which field was
// wrong: a participant is not the conversation's own agent.
func TestARefusedParticipantLeavesNoConversationBehind(t *testing.T) {
	cases := map[string]struct {
		participants []string
		keyAgents    []string
		disabled     bool
	}{
		"an agent that does not exist": {participants: []string{"ag-fin", "ag-nope"}},
		"another company's agent":      {participants: []string{"ag-theirs"}},
		"a disabled agent":             {participants: []string{"ag-fin"}, disabled: true},
		"an agent off the key's list":  {participants: []string{"ag-fin"}, keyAgents: []string{"ag-ops"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			enq, _, repo, roster := apiRoomFixture(t, nil)
			roster.byID["co-2/ag-theirs"] = &domain.Agent{ID: "ag-theirs", CompanyID: "co-2", Enabled: true}
			if tc.disabled {
				roster.byID["co-1/ag-fin"].Enabled = false
			}

			_, err := enq.OpenAPIThread(context.Background(), APIThreadInput{
				CompanyID: "co-1", APIUserRef: "u", AgentID: "ag-ops",
				ParticipantIDs: tc.participants, KeyAgentIDs: tc.keyAgents,
			})

			if !errors.Is(err, ErrParticipantNotFound) {
				t.Fatalf("OpenAPIThread = %v, want ErrParticipantNotFound", err)
			}
			if !errors.Is(err, domain.ErrNotFound) {
				t.Error("ErrParticipantNotFound no longer wraps domain.ErrNotFound")
			}
			if len(repo.created) != 0 {
				t.Errorf("created %d conversation(s) for a refused room", len(repo.created))
			}
		})
	}
}

// A bad `agent_id` is about `agent_id`, not about the participants.
func TestAnUnknownOwnAgentIsNotAParticipantRefusal(t *testing.T) {
	enq, _, repo, _ := apiRoomFixture(t, nil)

	_, err := enq.OpenAPIThread(context.Background(), APIThreadInput{
		CompanyID: "co-1", APIUserRef: "u", AgentID: "ag-nope", ParticipantIDs: []string{"ag-fin"},
	})

	if !errors.Is(err, ErrAgentNotFound) || errors.Is(err, ErrParticipantNotFound) {
		t.Fatalf("OpenAPIThread = %v, want ErrAgentNotFound and not ErrParticipantNotFound", err)
	}
	if len(repo.created) != 0 {
		t.Errorf("created %d conversation(s)", len(repo.created))
	}
}

// `POST /v1/chat`'s rule for a key limited to named agents, at the other door
// that opens a conversation: naming none, when the default is not on the list,
// is refused before anything is written.
func TestAKeyLimitedToNamedAgentsMustNameOneToOpenAConversation(t *testing.T) {
	enq, _, repo, roster := apiRoomFixture(t, nil)
	roster.agent = &domain.Agent{ID: "ag-ops", CompanyID: "co-1", Enabled: true}

	_, err := enq.OpenAPIThread(context.Background(), APIThreadInput{
		CompanyID: "co-1", APIUserRef: "u", KeyAgentIDs: []string{"ag-fin"},
	})

	if !errors.Is(err, ErrAgentNotAllowed) {
		t.Fatalf("OpenAPIThread = %v, want ErrAgentNotAllowed", err)
	}
	if len(repo.created) != 0 {
		t.Errorf("created %d conversation(s)", len(repo.created))
	}
}

func TestOpeningAnAPIConversationNeedsAUserRef(t *testing.T) {
	enq, _, repo, _ := apiRoomFixture(t, nil)

	_, err := enq.OpenAPIThread(context.Background(), APIThreadInput{CompanyID: "co-1"})

	if !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("OpenAPIThread = %v, want ErrInvalidInput", err)
	}
	if len(repo.created) != 0 {
		t.Errorf("created %d conversation(s)", len(repo.created))
	}
}
