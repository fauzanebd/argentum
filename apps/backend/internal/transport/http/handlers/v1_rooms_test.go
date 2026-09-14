package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// T-N10: `/v1` sees rooms. A conversation opened with several agents, a
// transcript that says who wrote each message, and a stream that is the
// caller's turn even when a colleague's turn shares the thread.

// --- fixtures ----------------------------------------------------------

// fakeV1Room stands in for app.ThreadParticipantService.
type fakeV1Room struct {
	room     []*domain.ThreadParticipant
	listErr  error
	addErr   error
	capacity int
	added    []string
	addedBy  []string
}

func (f *fakeV1Room) List(context.Context, string, string) ([]*domain.ThreadParticipant, error) {
	return f.room, f.listErr
}

func (f *fakeV1Room) Add(_ context.Context, _, _, agentID, addedBy string) (*domain.ThreadParticipant, error) {
	if f.addErr != nil {
		return nil, f.addErr
	}
	f.added = append(f.added, agentID)
	f.addedBy = append(f.addedBy, addedBy)
	return &domain.ThreadParticipant{AgentID: agentID}, nil
}

func (f *fakeV1Room) Capacity() int { return f.capacity }

// fakeOpener stands in for app.ChatEnqueuer.OpenAPIThread.
type fakeOpener struct {
	in    app.APIThreadInput
	calls int
	err   error
}

func (f *fakeOpener) OpenAPIThread(_ context.Context, in app.APIThreadInput) (*domain.ConversationThread, error) {
	f.calls++
	f.in = in
	if f.err != nil {
		return nil, f.err
	}
	t := apiThread()
	t.AgentID = in.AgentID
	return t, nil
}

// scopedEnqueuer is fakeEnqueuer reporting the agent each turn runs as, which
// is what the handler scopes a stream by.
type scopedEnqueuer struct {
	agentIDs []string
}

func (s *scopedEnqueuer) Enqueue(context.Context, app.ChatInput) (*app.EnqueueResult, error) {
	return &app.EnqueueResult{
		TaskID: "task-1", Thread: apiThread(), UserMsgID: testRunID, AgentIDs: s.agentIDs,
	}, nil
}

func opsAndFinance() []*domain.ThreadParticipant {
	return []*domain.ThreadParticipant{
		{AgentID: "ag-ops", AgentName: "Ops", AddedAt: testAnswerAt},
		{AgentID: "ag-fin", AgentName: "Finance", AddedAt: testAnswerAt},
	}
}

func postThread(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/threads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

type errorEnvelope struct {
	Error struct {
		Type    string `json:"type"`
		Code    string `json:"code"`
		Message string `json:"message"`
		Param   string `json:"param"`
	} `json:"error"`
}

func decodeError(t *testing.T, w *httptest.ResponseRecorder) errorEnvelope {
	t.Helper()
	var e errorEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("error body %q: %v", w.Body.String(), err)
	}
	return e
}

// --- POST /v1/threads --------------------------------------------------

func TestCreatingAThreadOpensARoomAndReturnsIt(t *testing.T) {
	opener := &fakeOpener{}
	room := &fakeV1Room{capacity: 4, room: opsAndFinance()}
	f := newChatFixtureWith(t, 5*time.Second, func(h *V1ChatHandler) { h.WithRooms(opener, room) })

	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, postThread(
		`{"user_ref":" their-user-42 ","agent_id":"ag-ops","participant_ids":["ag-fin"," ","ag-fin","ag-ops"]}`))

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	if opener.in.AgentID != "ag-ops" || opener.in.APIUserRef != "their-user-42" || opener.in.APIKeyID != "key-1" {
		t.Errorf("opener input = %+v, want ag-ops for their-user-42 on key-1", opener.in)
	}
	// A repeat, a blank and the conversation's own agent name nobody new.
	if len(opener.in.ParticipantIDs) != 1 || opener.in.ParticipantIDs[0] != "ag-fin" {
		t.Errorf("participants checked = %v, want [ag-fin]", opener.in.ParticipantIDs)
	}
	if len(room.added) != 1 || room.added[0] != "ag-fin" {
		t.Errorf("added = %v, want [ag-fin]", room.added)
	}
	// No person added it: the key's allowlist was the check, not a user's grants.
	if room.addedBy[0] != "" {
		t.Errorf("added by %q, want nobody", room.addedBy[0])
	}

	var body threadResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body.Object != "thread" || len(body.Participants) != 2 {
		t.Fatalf("body = %+v, want a thread with two participants", body)
	}
	if !body.Participants[0].Default || body.Participants[1].Default {
		t.Errorf("default flags = %v, %v; want Ops alone", body.Participants[0].Default, body.Participants[1].Default)
	}
	if body.Participants[1].Object != "participant" || body.Participants[1].AgentName != "Finance" {
		t.Errorf("participant = %+v", body.Participants[1])
	}
}

// The ceiling is checked against the whole room before anything is opened, and
// the conversation's own agent takes a seat whether it is named or not.
func TestARoomLargerThanTheCeilingIsRefusedBeforeAnythingOpens(t *testing.T) {
	for name, body := range map[string]string{
		"own agent named":   `{"user_ref":"u","agent_id":"ag-ops","participant_ids":["ag-fin","ag-hr"]}`,
		"own agent default": `{"user_ref":"u","participant_ids":["ag-fin","ag-hr"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			opener := &fakeOpener{}
			room := &fakeV1Room{capacity: 2}
			f := newChatFixtureWith(t, 5*time.Second, func(h *V1ChatHandler) { h.WithRooms(opener, room) })

			w := httptest.NewRecorder()
			f.router.ServeHTTP(w, postThread(body))

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
			}
			if e := decodeError(t, w); e.Error.Code != "too_many_participants" || e.Error.Param != "participant_ids" {
				t.Errorf("error = %+v, want too_many_participants on participant_ids", e.Error)
			}
			if !strings.Contains(w.Body.String(), "2 agents") {
				t.Errorf("body %q does not name the limit", w.Body.String())
			}
			if opener.calls != 0 || len(room.added) != 0 {
				t.Errorf("opened %d conversation(s) and added %v for a refused room", opener.calls, room.added)
			}
		})
	}
}

// Each refusal names the field to go and fix.
func TestARefusedRoomNamesTheFieldThatWasWrong(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		status     int
		code, para string
	}{
		{"a participant", app.ErrParticipantNotFound, http.StatusNotFound, "participant_not_found", "participant_ids"},
		{"its own agent", app.ErrAgentNotFound, http.StatusNotFound, "agent_not_found", "agent_id"},
		{"off the key's list", app.ErrAgentNotAllowed, http.StatusForbidden, "agent_not_allowed", "agent_id"},
		{"the database", errors.New("connection reset"), http.StatusInternalServerError, "open_failed", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			room := &fakeV1Room{capacity: 4}
			f := newChatFixtureWith(t, 5*time.Second, func(h *V1ChatHandler) {
				h.WithRooms(&fakeOpener{err: tc.err}, room)
			})

			w := httptest.NewRecorder()
			f.router.ServeHTTP(w, postThread(`{"user_ref":"u","agent_id":"ag-ops","participant_ids":["ag-fin"]}`))

			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
			if e := decodeError(t, w); e.Error.Code != tc.code || e.Error.Param != tc.para {
				t.Errorf("error = %+v, want %s on %q", e.Error, tc.code, tc.para)
			}
			if len(room.added) != 0 {
				t.Errorf("added %v to a conversation that was never opened", room.added)
			}
			if strings.Contains(w.Body.String(), "connection reset") {
				t.Error("the database's own error reached the caller")
			}
		})
	}
}

// An agent disabled between the check and the insert: the conversation exists
// by then, and the caller is told which one.
func TestAParticipantLostWhileOpeningNamesTheConversationLeftBehind(t *testing.T) {
	room := &fakeV1Room{capacity: 4, addErr: app.ErrAgentDisabled}
	f := newChatFixtureWith(t, 5*time.Second, func(h *V1ChatHandler) { h.WithRooms(&fakeOpener{}, room) })

	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, postThread(`{"user_ref":"u","agent_id":"ag-ops","participant_ids":["ag-fin"]}`))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", w.Code, w.Body.String())
	}
	if e := decodeError(t, w); e.Error.Code != "participant_not_found" || !strings.Contains(e.Error.Message, testThreadID) {
		t.Errorf("error = %+v, want participant_not_found naming %s", e.Error, testThreadID)
	}
}

func TestCreatingAThreadNeedsAUserRef(t *testing.T) {
	opener := &fakeOpener{}
	f := newChatFixtureWith(t, 5*time.Second, func(h *V1ChatHandler) { h.WithRooms(opener, &fakeV1Room{capacity: 4}) })

	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, postThread(`{"agent_id":"ag-ops","user_ref":"   "}`))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if e := decodeError(t, w); e.Error.Code != "user_ref_required" || e.Error.Param != "user_ref" {
		t.Errorf("error = %+v, want user_ref_required", e.Error)
	}
	if opener.calls != 0 {
		t.Error("a conversation was opened without a user_ref")
	}
}

// A deployment without rooms wired answers typed errors rather than panicking,
// and one with an opener but no membership still opens a conversation of one.
func TestCreatingAThreadDegradesWithWhatIsWired(t *testing.T) {
	t.Run("nothing wired", func(t *testing.T) {
		f := newChatFixture(t, 5*time.Second)
		w := httptest.NewRecorder()
		f.router.ServeHTTP(w, postThread(`{"user_ref":"u"}`))
		if e := decodeError(t, w); e.Error.Code != "rooms_unavailable" {
			t.Errorf("error = %+v, want rooms_unavailable", e.Error)
		}
	})
	t.Run("no membership, a room asked for", func(t *testing.T) {
		opener := &fakeOpener{}
		f := newChatFixtureWith(t, 5*time.Second, func(h *V1ChatHandler) { h.WithRooms(opener, nil) })
		w := httptest.NewRecorder()
		f.router.ServeHTTP(w, postThread(`{"user_ref":"u","participant_ids":["ag-fin"]}`))
		if e := decodeError(t, w); e.Error.Code != "rooms_unavailable" {
			t.Errorf("error = %+v, want rooms_unavailable", e.Error)
		}
		if opener.calls != 0 {
			t.Error("a conversation was opened for a room this deployment cannot hold")
		}
	})
	t.Run("no membership, a conversation of one", func(t *testing.T) {
		f := newChatFixtureWith(t, 5*time.Second, func(h *V1ChatHandler) { h.WithRooms(&fakeOpener{}, nil) })
		w := httptest.NewRecorder()
		f.router.ServeHTTP(w, postThread(`{"user_ref":"u","agent_id":"ag-ops"}`))
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "participants") {
			t.Errorf("body %s carries participants nobody could read", w.Body.String())
		}
	})
}

// --- reads -------------------------------------------------------------

func TestAThreadReadCarriesItsRoom(t *testing.T) {
	room := &fakeV1Room{room: opsAndFinance()}
	f := newChatFixtureWith(t, 5*time.Second, func(h *V1ChatHandler) { h.WithRooms(&fakeOpener{}, room) })
	f.threads.thread.AgentID = "ag-ops"

	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/threads/"+testThreadID, nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var body threadResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if len(body.Participants) != 2 || body.Participants[0].AgentID != "ag-ops" || !body.Participants[0].Default {
		t.Errorf("participants = %+v, want Ops (default) then Finance", body.Participants)
	}
}

// A failed membership read drops the field, not the read. And a deployment
// with no rooms answers exactly what it answered before them.
func TestAThreadReadWithoutItsRoomIsStillTheThread(t *testing.T) {
	for name, configure := range map[string]func(*V1ChatHandler){
		"the room could not be read": func(h *V1ChatHandler) {
			h.WithRooms(&fakeOpener{}, &fakeV1Room{listErr: errors.New("db is down")})
		},
		"no rooms wired": nil,
	} {
		t.Run(name, func(t *testing.T) {
			f := newChatFixtureWith(t, 5*time.Second, configure)

			w := httptest.NewRecorder()
			f.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/threads/"+testThreadID, nil))

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "participants") {
				t.Errorf("body %s, want no participants field", w.Body.String())
			}
		})
	}
}

// The transcript says who wrote each message, and which assistant rows are a
// room's own lines rather than answers.
func TestATranscriptSaysWhoWroteEachMessageAndWhichAreRoomLines(t *testing.T) {
	f := newChatFixture(t, 5*time.Second)
	f.messages.page = []*domain.Message{
		{ID: "m1", Role: domain.MessageRoleUser, Content: "short on SKU 4471?", CreatedAt: testAnswerAt},
		{
			ID: "m2", Role: domain.MessageRoleAssistant, Content: "→ Finance: was a goods-in posted?",
			AgentID: "ag-ops", AgentName: "Ops", CreatedAt: testAnswerAt,
			Metadata: map[string]interface{}{app.RoomEventKey: app.RoomEventNudge},
		},
		{
			ID: "m3", Role: domain.MessageRoleAssistant, Content: "Finance had nothing to add to the question from Ops.",
			AgentID: "ag-fin", AgentName: "Finance", CreatedAt: testAnswerAt,
			Metadata: map[string]interface{}{app.RoomEventKey: app.RoomEventSettle},
		},
		{
			ID: "m4", Role: domain.MessageRoleAssistant, Content: "No goods-in was posted.",
			AgentID: "ag-ops", AgentName: "Ops", CreatedAt: testAnswerAt,
			Metadata: map[string]interface{}{"next_steps": []interface{}{}},
		},
	}

	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/threads/"+testThreadID+"/messages", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var page struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Data) != 4 {
		t.Fatalf("page = %s (%v)", w.Body.String(), err)
	}
	msgs := make([]messageResponse, len(page.Data))
	for i, raw := range page.Data {
		if err := json.Unmarshal(raw, &msgs[i]); err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
	}
	// The person's message names no agent, and says so by leaving the fields out.
	if strings.Contains(string(page.Data[0]), "agent_id") || strings.Contains(string(page.Data[0]), "room_event") {
		t.Errorf("user message = %s, want no agent and no room_event", page.Data[0])
	}
	if msgs[1].RoomEvent != "nudge" || msgs[1].AgentName != "Ops" {
		t.Errorf("nudge line = %+v", msgs[1])
	}
	if msgs[2].RoomEvent != "settle" || msgs[2].AgentID != "ag-fin" {
		t.Errorf("settle line = %+v", msgs[2])
	}
	// Other metadata is not a room line.
	if msgs[3].RoomEvent != "" || msgs[3].AgentID != "ag-ops" || msgs[3].AgentName != "Ops" {
		t.Errorf("answer = %+v, want Ops' answer with no room_event", msgs[3])
	}
}

// --- the stream --------------------------------------------------------

// colleagueFinishesFirst publishes a room's turns the way T-N6 produces them.
// The caller's agent, Ops, starts and asks Finance. Finance — whose turn is
// short — asks Ops something back and answers before Ops does. All of it on the
// same channel, under the same job id.
//
// The turn Finance asked for runs as Ops, so it carries the caller's own agent
// id. Only `asked_by` tells it from the caller's turn, which is why the stream
// is scoped by that and not by agent (T-N10's risk 2).
//
// The pause is what makes the ordering deterministic: without it the handler
// could read Ops' persisted answer while handling a colleague's `final`, and a
// handler that forwarded that `final` would pass by luck.
func colleagueFinishesFirst(t *testing.T, f *chatFixture, answer *domain.Message) {
	t.Helper()
	f.publish(t, app.ChatEvent{Type: "started", JobID: testRunID, AgentID: "ag-ops", AgentName: "Ops", Timestamp: testAnswerAt})
	f.publish(t, app.ChatEvent{Type: "started", JobID: testRunID, AgentID: "ag-fin", AgentName: "Finance", AskedBy: "ag-ops", Timestamp: testAnswerAt})
	f.publish(t, app.ChatEvent{Type: "started", JobID: testRunID, AgentID: "ag-ops", AgentName: "Ops", AskedBy: "ag-fin", Timestamp: testAnswerAt})
	f.publish(t, app.ChatEvent{Type: "delta", JobID: testRunID, AgentID: "ag-ops", AgentName: "Ops", AskedBy: "ag-fin", Content: "asked back: 12 units short"})
	f.publish(t, app.ChatEvent{Type: "final", JobID: testRunID, AgentID: "ag-ops", AgentName: "Ops", AskedBy: "ag-fin", Content: "asked back: 12 units short"})
	f.publish(t, app.ChatEvent{Type: "delta", JobID: testRunID, AgentID: "ag-fin", AgentName: "Finance", AskedBy: "ag-ops", Content: "GRN-118 was posted"})
	f.publish(t, app.ChatEvent{Type: "final", JobID: testRunID, AgentID: "ag-fin", AgentName: "Finance", AskedBy: "ag-ops", Content: "GRN-118 was posted"})
	time.Sleep(50 * time.Millisecond)
	f.publish(t, app.ChatEvent{Type: "delta", JobID: testRunID, AgentID: "ag-ops", AgentName: "Ops", Content: "IDR 3.863.405.700"})
	f.messages.persist(answer)
	f.publish(t, app.ChatEvent{Type: "final", JobID: testRunID, AgentID: "ag-ops", AgentName: "Ops", Content: "IDR 3.863.405.700"})
}

func opsAnswer() *domain.Message {
	m := assistantMessage()
	m.AgentID, m.AgentName = "ag-ops", "Ops"
	return m
}

const roomSend = `{"message":"we're short on SKU 4471 — what happened?","thread_id":"` + testThreadID + `","agent_id":"ag-ops"}`

func TestARoomStreamIsTheCallersTurnAndNotAColleagues(t *testing.T) {
	f := newChatFixtureWith(t, 5*time.Second, func(h *V1ChatHandler) {
		h.chat = &scopedEnqueuer{agentIDs: []string{"ag-ops"}}
	})

	w := f.send(t, sendRequest(t, "text/event-stream", roomSend), func() {
		colleagueFinishesFirst(t, f, opsAnswer())
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "GRN-118") || strings.Contains(w.Body.String(), "Finance") {
		t.Fatalf("the colleague's turn reached the caller's stream:\n%s", w.Body.String())
	}
	// The turn Finance asked for runs as Ops — the caller's own agent id — and is
	// still not the caller's turn.
	if strings.Contains(w.Body.String(), "asked back") || strings.Contains(w.Body.String(), "asked_by") {
		t.Fatalf("the turn Finance asked of Ops reached the caller's stream:\n%s", w.Body.String())
	}
	frames, _ := parseSSE(t, w.Body.String())
	started := 0
	for _, fr := range frames {
		if fr.Event == "started" {
			started++
		}
		if fr.Event == "started" || fr.Event == "delta" {
			var d struct {
				AgentID   string `json:"agent_id"`
				AgentName string `json:"agent_name"`
			}
			if err := json.Unmarshal([]byte(fr.Data), &d); err != nil || d.AgentID != "ag-ops" || d.AgentName != "Ops" {
				t.Errorf("%s frame %s, want it to say Ops is speaking", fr.Event, fr.Data)
			}
		}
	}
	if started != 1 {
		t.Errorf("%d started frames, want 1", started)
	}

	var turn turnResponse
	if err := json.Unmarshal([]byte(frameOf(t, frames, "final").Data), &turn); err != nil {
		t.Fatalf("final payload: %v", err)
	}
	if turn.Message.AgentID != "ag-ops" || turn.Message.Content != "IDR 3.863.405.700" {
		t.Errorf("final message = %+v, want Ops' answer", turn.Message)
	}
	if len(f.messages.gotScopes) == 0 {
		t.Error("no answer was looked up")
	}
	for _, s := range f.messages.gotScopes {
		if s != domain.OwnAnswer {
			t.Errorf("an answer was looked up with scope %v; a sent turn looks up its own", s)
		}
	}
}

func TestTheSyncDoorWaitsForTheCallersAgentInARoom(t *testing.T) {
	f := newChatFixtureWith(t, 5*time.Second, func(h *V1ChatHandler) {
		h.chat = &scopedEnqueuer{agentIDs: []string{"ag-ops"}}
	})

	w := f.send(t, sendRequest(t, "application/json", roomSend), func() {
		colleagueFinishesFirst(t, f, opsAnswer())
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var turn turnResponse
	if err := json.Unmarshal(w.Body.Bytes(), &turn); err != nil {
		t.Fatalf("body: %v", err)
	}
	if turn.Message.AgentID != "ag-ops" || turn.Message.Content != "IDR 3.863.405.700" {
		t.Errorf("answer = %+v, want Ops' — not the colleague's that finished first", turn.Message)
	}
}

// A turn queued for an agent that is deleted before it runs runs as the
// workspace default (ChatRunner.resolveAgent), so every frame and the answer
// carry the default's id. Scoping the stream by the agent the turn was sent to
// would never match either: the stream would hang until the caller hung up, and
// the synchronous door would answer 504 for a turn that had answered.
func TestATurnWhoseAgentWasDeletedStillEndsWithItsAnswer(t *testing.T) {
	for name, accept := range map[string]string{
		"streamed":    "text/event-stream",
		"synchronous": "application/json",
	} {
		t.Run(name, func(t *testing.T) {
			f := newChatFixtureWith(t, 2*time.Second, func(h *V1ChatHandler) {
				h.chat = &scopedEnqueuer{agentIDs: []string{"ag-gone"}}
			})
			answer := assistantMessage()
			answer.AgentID, answer.AgentName = "ag-def", "Default"

			w := f.send(t, sendRequest(t, accept, roomSend), func() {
				f.publish(t, app.ChatEvent{Type: "started", JobID: testRunID, AgentID: "ag-def", AgentName: "Default", Timestamp: testAnswerAt})
				f.publish(t, app.ChatEvent{Type: "delta", JobID: testRunID, AgentID: "ag-def", AgentName: "Default", Content: "IDR 3.863.405.700"})
				f.messages.persist(answer)
				f.publish(t, app.ChatEvent{Type: "final", JobID: testRunID, AgentID: "ag-def", AgentName: "Default", Content: "IDR 3.863.405.700"})
			})

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
			}
			body := w.Body.String()
			if accept == "text/event-stream" {
				frames, _ := parseSSE(t, body)
				body = frameOf(t, frames, "final").Data
			}
			var turn turnResponse
			if err := json.Unmarshal([]byte(body), &turn); err != nil {
				t.Fatalf("turn: %v", err)
			}
			if turn.Message.Content != "IDR 3.863.405.700" || turn.Message.AgentID != "ag-def" {
				t.Errorf("answer = %+v, want the default's answer", turn.Message)
			}
		})
	}
}

// A retried send replays through the original's doors, and in a room it must be
// scoped as the original was. The idempotency record stores ids, not the scope,
// so the replay path has to set it itself.
func TestARetriedSendInARoomIsScopedAsTheOriginal(t *testing.T) {
	f := newChatFixtureWith(t, 5*time.Second, func(h *V1ChatHandler) {
		h.chat = &scopedEnqueuer{agentIDs: []string{"ag-ops"}}
	})

	first := f.send(t, sendRequest(t, "application/json", roomSend), func() {
		f.messages.persist(opsAnswer())
		f.publish(t, app.ChatEvent{Type: "final", JobID: testRunID, AgentID: "ag-ops", AgentName: "Ops", Content: "IDR 3.863.405.700"})
	})
	if first.Code != http.StatusOK {
		t.Fatalf("first send: status = %d: %s", first.Code, first.Body.String())
	}
	f.messages.mu.Lock()
	f.messages.gotScopes = nil
	f.messages.mu.Unlock()

	replay := f.send(t, sendRequest(t, "application/json", roomSend), nil)

	if replay.Code != http.StatusOK || replay.Header().Get("Idempotent-Replay") != "true" {
		t.Fatalf("replay: status = %d, replay header %q: %s", replay.Code, replay.Header().Get("Idempotent-Replay"), replay.Body.String())
	}
	f.messages.mu.Lock()
	defer f.messages.mu.Unlock()
	if len(f.messages.gotScopes) == 0 {
		t.Fatal("the replay looked no answer up")
	}
	for _, s := range f.messages.gotScopes {
		if s != domain.OwnAnswer {
			t.Errorf("the replay looked an answer up with scope %v; it is the caller's own turn", s)
		}
	}
}

// A 504's in-flight body names the agent, so a caller told to attach knows whose
// answer it is waiting for.
func TestAPendingTurnInARoomNamesItsAgent(t *testing.T) {
	f := newChatFixtureWith(t, 30*time.Millisecond, func(h *V1ChatHandler) {
		h.chat = &scopedEnqueuer{agentIDs: []string{"ag-ops"}}
	})

	w := f.send(t, sendRequest(t, "application/json", roomSend), nil)

	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504: %s", w.Code, w.Body.String())
	}
	var body pendingBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body.InFlight.AgentID != "ag-ops" {
		t.Errorf("in_flight = %+v, want agent_id ag-ops", body.InFlight)
	}
}
