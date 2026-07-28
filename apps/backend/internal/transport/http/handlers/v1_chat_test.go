package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/idempotency"
	"github.com/fauzanebd/argentum/internal/transport/eventbus"
	"github.com/fauzanebd/argentum/internal/transport/http/apiv1"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
)

// The `/v1` chat surface (T-A3).
//
// These run against miniredis rather than a hand-written bus, because the
// property under test is what a *subscriber* sees: that the handler is
// subscribed before it waits, that a publish into an empty room is recovered
// from the persisted transcript, and that a heartbeat keeps flowing while
// nothing else does. A fake bus would only assert that this file agrees with
// itself.

// --- fixtures ----------------------------------------------------------

const (
	testCompany  = "co-1"
	testThreadID = "3f7c1f3e-0000-4000-8000-000000000001"
	testRunID    = "msg-user-1"
)

// testAnswerAt is an hour ahead of the wall clock, not a fixed date. The send
// path stamps its window from time.Now() before the turn starts, so a fixture
// answer in the past would sit outside every window and never be found — the
// fixture would be asserting the opposite of what it looks like it asserts.
var testAnswerAt = time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)

func apiThread() *domain.ConversationThread {
	return &domain.ConversationThread{
		ID: testThreadID, CompanyID: testCompany, Channel: domain.ChannelAPI,
		APIUserRef: "their-user-42", Title: "Sales", Summary: "monthly sales",
		LastMessageAt: testAnswerAt.Add(-time.Second), CreatedAt: testAnswerAt.Add(-time.Minute),
	}
}

func assistantMessage() *domain.Message {
	return &domain.Message{
		ID: "3f7c1f3e-0000-4000-8000-0000000000aa", ThreadID: testThreadID,
		Role: domain.MessageRoleAssistant, Content: "IDR 3.863.405.700",
		CreatedAt: testAnswerAt,
	}
}

// fakeEnqueuer stands in for app.ChatEnqueuer.
type fakeEnqueuer struct {
	in  app.ChatInput
	err error
}

func (f *fakeEnqueuer) Enqueue(_ context.Context, in app.ChatInput) (*app.EnqueueResult, error) {
	f.in = in
	if f.err != nil {
		return nil, f.err
	}
	return &app.EnqueueResult{TaskID: "task-1", Thread: apiThread(), UserMsgID: testRunID}, nil
}

// fakeThreads implements domain.ThreadRepository. Only what `/v1` touches is
// real; the rest panics so a change that reaches for one is visible.
type fakeThreads struct {
	thread  *domain.ConversationThread
	err     error
	page    []*domain.ConversationThread
	hasMore bool
	gotFilt domain.ThreadFilter
	deleted []string
}

func (f *fakeThreads) GetForCompany(_ context.Context, _, _ string) (*domain.ConversationThread, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.thread, nil
}

func (f *fakeThreads) ListPage(_ context.Context, _ string, filter domain.ThreadFilter) ([]*domain.ConversationThread, bool, error) {
	f.gotFilt = filter
	return f.page, f.hasMore, f.err
}

func (f *fakeThreads) Delete(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeThreads) Create(context.Context, *domain.ConversationThread) error {
	panic("unexpected Create")
}
func (f *fakeThreads) GetByID(context.Context, string) (*domain.ConversationThread, error) {
	panic("unexpected GetByID — /v1 must scope its lookups by company")
}
func (f *fakeThreads) LatestForPhone(context.Context, string, string) (*domain.ConversationThread, error) {
	panic("unexpected LatestForPhone")
}
func (f *fakeThreads) LatestForUser(context.Context, string, string) (*domain.ConversationThread, error) {
	panic("unexpected LatestForUser")
}
func (f *fakeThreads) LatestForDiscordUser(context.Context, string, string) (*domain.ConversationThread, error) {
	panic("unexpected LatestForDiscordUser")
}
func (f *fakeThreads) LatestForLark(context.Context, string, string) (*domain.ConversationThread, error) {
	panic("unexpected LatestForLark")
}
func (f *fakeThreads) LatestForAPIUser(context.Context, string, string) (*domain.ConversationThread, error) {
	panic("unexpected LatestForAPIUser")
}
func (f *fakeThreads) ListByCompany(context.Context, string, int, int) ([]*domain.ConversationThread, error) {
	panic("unexpected ListByCompany — /v1 pages by cursor")
}
func (f *fakeThreads) UpdateSummary(context.Context, string, string, string) error {
	panic("unexpected UpdateSummary")
}
func (f *fakeThreads) Touch(context.Context, string, time.Time) error { panic("unexpected Touch") }
func (f *fakeThreads) Archive(context.Context, string) error          { panic("unexpected Archive") }

// fakeMessages implements domain.MessageRepository.
//
// The answer is behind a flag rather than simply present, because *when* it
// becomes readable is the thing several of these tests are about: a turn that
// has not answered yet must leave the stream waiting, and the same lookup a
// moment later must find it. A fixture that always answered would make every
// stream terminate on its first line.
type fakeMessages struct {
	mu        sync.Mutex
	latest    *domain.Message
	answer    *domain.Message
	persisted bool
	page      []*domain.Message
	gotFilt   domain.MessageFilter
}

// persist makes the answer readable, as the worker does before it publishes
// `final`.
func (f *fakeMessages) persist(m *domain.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answer, f.persisted, f.latest = m, true, m
}

// latest is the newest message of any role, which is how the attach route
// decides whether a thread is settled or mid-turn.
func (f *fakeMessages) LatestByThread(_ context.Context, _ string) (*domain.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.latest == nil {
		return nil, domain.ErrNotFound
	}
	return f.latest, nil
}

// The `since` bound is honoured rather than ignored, because it is the whole
// of what the attach path gets wrong when it gets it wrong: an answer one
// microsecond before the window is an answer the caller never receives.
func (f *fakeMessages) LatestAssistantSince(_ context.Context, _ string, since time.Time) (*domain.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.persisted || f.answer == nil || f.answer.CreatedAt.Before(since) {
		return nil, domain.ErrNotFound
	}
	return f.answer, nil
}

func (f *fakeMessages) ListPageByThread(_ context.Context, _ string, filter domain.MessageFilter) ([]*domain.Message, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotFilt = filter
	page := f.page
	f.page = nil
	return page, false, nil
}

func (f *fakeMessages) Append(context.Context, *domain.Message) error { panic("unexpected Append") }
func (f *fakeMessages) ListByThread(context.Context, string, int, int) ([]*domain.Message, error) {
	panic("unexpected ListByThread — /v1 pages by cursor")
}
func (f *fakeMessages) CountByThread(context.Context, string) (int, error) {
	panic("unexpected CountByThread")
}

// fakeUsage reports a fixed turn cost.
type fakeUsage struct{ err error }

func (f fakeUsage) SummaryByThread(context.Context, string, string, time.Time, time.Time) (*domain.UsageSummary, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &domain.UsageSummary{TotalTokensIn: 4210, TotalTokensOut: 318, TotalCostUSD: 0.0241}, nil
}

// --- harness -----------------------------------------------------------

type chatFixture struct {
	router   *gin.Engine
	enq      *fakeEnqueuer
	threads  *fakeThreads
	messages *fakeMessages
	rdb      *redis.Client
	store    idempotency.Store
	scopes   []domain.Scope
}

func newChatFixture(t *testing.T, syncTimeout time.Duration) *chatFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)

	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	f := &chatFixture{
		enq:      &fakeEnqueuer{},
		threads:  &fakeThreads{thread: apiThread()},
		messages: &fakeMessages{},
		rdb:      rdb,
		store:    idempotency.NewRedisStore(rdb),
		scopes:   []domain.Scope{domain.ScopeWriteChat, domain.ScopeReadThreads},
	}

	r := gin.New()
	v1 := r.Group("/v1")
	v1.Use(middleware.RequestID())
	v1.Use(func(c *gin.Context) {
		c.Set("company_id", testCompany)
		c.Set(middleware.CtxAPIKeyID, "key-1")
		c.Set(middleware.CtxAPIKeyScopes, f.scopes)
	})
	h := NewV1ChatHandler(f.enq, f.threads, f.messages, fakeUsage{}, rdb, f.store, syncTimeout)
	// The production interval is 15 seconds. A test that waited one out would
	// add fifteen seconds to every run to observe a two-character write.
	h.heartbeat = 20 * time.Millisecond
	h.Register(v1)
	f.router = r
	return f
}

// send issues a request and returns once the handler has finished. onLive runs
// after the handler has subscribed to the thread channel, which is when a
// publish is guaranteed to be seen — the same guarantee the worker gets from
// having a subscriber at all.
func (f *chatFixture) send(t *testing.T, req *http.Request, onLive func()) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.router.ServeHTTP(w, req)
	}()
	if onLive != nil {
		f.waitForSubscriber(t)
		onLive()
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler did not finish within 5s")
	}
	return w
}

// waitForSubscriber blocks until the handler's SUBSCRIBE is live. Without it
// every streaming test is a race against a goroutine, and Redis pub/sub keeps
// nothing for a subscriber that was not there yet.
func (f *chatFixture) waitForSubscriber(t *testing.T) {
	t.Helper()
	ch := eventbus.ChannelFor(testThreadID)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		counts, err := f.rdb.PubSubNumSub(context.Background(), ch).Result()
		if err == nil && counts[ch] > 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("no subscriber appeared on the thread channel")
}

func (f *chatFixture) publish(t *testing.T, evt app.ChatEvent) {
	t.Helper()
	raw, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if err := f.rdb.Publish(context.Background(), eventbus.ChannelFor(testThreadID), raw).Err(); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func sendRequest(t *testing.T, accept, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "k-1")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	return req
}

// --- SSE parsing -------------------------------------------------------

type sseFrame struct {
	ID    string
	Event string
	Data  string
}

// parseSSE reads a whole stream back into frames. Comments (the heartbeat) are
// counted separately, because a client never sees them as events and neither
// should an assertion.
func parseSSE(t *testing.T, body string) ([]sseFrame, int) {
	t.Helper()
	var frames []sseFrame
	comments := 0
	for _, block := range strings.Split(body, "\n\n") {
		if strings.TrimSpace(block) == "" {
			continue
		}
		var fr sseFrame
		isEvent := false
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, ": "):
				comments++
			case strings.HasPrefix(line, "id: "):
				fr.ID = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				fr.Event = strings.TrimPrefix(line, "event: ")
				isEvent = true
			case strings.HasPrefix(line, "data: "):
				fr.Data = strings.TrimPrefix(line, "data: ")
			}
		}
		if isEvent {
			frames = append(frames, fr)
		}
	}
	return frames, comments
}

func frameOf(t *testing.T, frames []sseFrame, name string) sseFrame {
	t.Helper()
	for _, fr := range frames {
		if fr.Event == name {
			return fr
		}
	}
	t.Fatalf("no %q frame in %+v", name, frames)
	return sseFrame{}
}

// --- tests -------------------------------------------------------------

// The ticket's first acceptance item: an SSE turn streams deltas and ends with
// `final` carrying the message and the usage.
func TestSSETurnStreamsDeltasAndEndsWithFinal(t *testing.T) {
	f := newChatFixture(t, 5*time.Second)

	w := f.send(t, sendRequest(t, "text/event-stream", `{"message":"sales last month?","user_ref":"their-user-42"}`), func() {
		f.publish(t, app.ChatEvent{Type: "started", JobID: testRunID, Timestamp: testAnswerAt})
		f.publish(t, app.ChatEvent{Type: "delta", Content: "IDR "})
		f.publish(t, app.ChatEvent{Type: "delta", Content: "3.863.405.700"})
		// Persisted before `final` is published, which is the order
		// ChatRunner.completeWith writes them in — and the reason the final
		// frame can carry a real message id.
		f.messages.persist(assistantMessage())
		f.publish(t, app.ChatEvent{Type: "final", JobID: testRunID, Content: "IDR 3.863.405.700"})
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	frames, _ := parseSSE(t, w.Body.String())

	var deltas []string
	for _, fr := range frames {
		if fr.Event == "delta" {
			var d struct {
				Content string `json:"content"`
			}
			if err := json.Unmarshal([]byte(fr.Data), &d); err != nil {
				t.Fatalf("delta payload: %v", err)
			}
			deltas = append(deltas, d.Content)
		}
	}
	if strings.Join(deltas, "") != "IDR 3.863.405.700" {
		t.Errorf("deltas = %q, want the answer streamed in pieces", deltas)
	}

	final := frameOf(t, frames, "final")
	var turn turnResponse
	if err := json.Unmarshal([]byte(final.Data), &turn); err != nil {
		t.Fatalf("final payload: %v", err)
	}
	if turn.Message.Content != "IDR 3.863.405.700" || turn.Message.ID != assistantMessage().ID {
		t.Errorf("final message = %+v, want the persisted assistant message", turn.Message)
	}
	if turn.ThreadID != testThreadID || turn.RunID != testRunID {
		t.Errorf("final ids = (%q, %q), want the thread and run", turn.ThreadID, turn.RunID)
	}
	if turn.Usage == nil || turn.Usage.TokensIn != 4210 || turn.Usage.CostUSD != 0.0241 {
		t.Errorf("usage = %+v, want the turn's own cost", turn.Usage)
	}
	// The frame's id is the resume point, and it has to be a cursor this API
	// can read back — a client returns it verbatim as Last-Event-ID.
	if _, id, err := apiv1.DecodeCursor(final.ID); err != nil || id != assistantMessage().ID {
		t.Errorf("final id = %q, want a cursor naming the assistant message", final.ID)
	}
}

// The ticket's second acceptance item: the sync door answers what the stream
// answered. Both doors read the same persisted message, which is what makes
// that true by construction rather than by coincidence.
func TestTheSyncDoorAnswersTheSameTurn(t *testing.T) {
	f := newChatFixture(t, 5*time.Second)

	w := f.send(t, sendRequest(t, "application/json", `{"message":"sales last month?","user_ref":"their-user-42"}`), func() {
		f.messages.persist(assistantMessage())
		f.publish(t, app.ChatEvent{Type: "final", JobID: testRunID, Content: "IDR 3.863.405.700"})
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var turn turnResponse
	if err := json.Unmarshal(w.Body.Bytes(), &turn); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if turn.Object != "turn" || turn.Message.Content != "IDR 3.863.405.700" {
		t.Errorf("body = %+v, want the turn's answer", turn)
	}
	if turn.Usage == nil || turn.Usage.TokensOut != 318 {
		t.Errorf("usage = %+v, want the turn's own cost", turn.Usage)
	}
	// The turn arrived on the api channel, attributed to the caller's own user
	// reference — without it the spend is billed to the company with nothing in
	// usage/by-user to say who spent it.
	if f.enq.in.Channel != domain.ChannelAPI || f.enq.in.APIUserRef != "their-user-42" {
		t.Errorf("enqueued %+v, want an api turn carrying the user_ref", f.enq.in)
	}
	if f.enq.in.APIKeyID != "key-1" {
		t.Errorf("api_key_id = %q, want the credential that started the turn", f.enq.in.APIKeyID)
	}
}

// A turn can finish between the enqueue and the SUBSCRIBE. Redis keeps nothing
// for a subscriber that was not there, so without the transcript check the
// caller would wait for a `final` that was published into an empty room — on
// the fastest turns, which are the ones the sync door is for.
func TestATurnThatFinishedBeforeTheSubscriptionIsStillDelivered(t *testing.T) {
	f := newChatFixture(t, 5*time.Second)
	f.messages.persist(assistantMessage())

	// Nothing is ever published.
	w := f.send(t, sendRequest(t, "application/json", `{"message":"hi","user_ref":"u"}`), nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the answer was already persisted: %s", w.Code, w.Body.String())
	}
	var turn turnResponse
	if err := json.Unmarshal(w.Body.Bytes(), &turn); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if turn.Message.ID != assistantMessage().ID {
		t.Errorf("message = %+v, want the persisted answer", turn.Message)
	}
}

// The ticket's seventh acceptance item. A 504 is the wait running out, not the
// turn — so it carries the ids that make the turn collectable, and it must not
// forget its idempotency key, or the retry it invites starts a second billed
// turn.
func TestASyncCallOverTheTimeoutIs504AndKeepsItsKey(t *testing.T) {
	f := newChatFixture(t, 60*time.Millisecond)

	w := f.send(t, sendRequest(t, "application/json", `{"message":"a long one","user_ref":"u"}`), nil)

	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504: %s", w.Code, w.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		InFlight struct {
			ThreadID string `json:"thread_id"`
			RunID    string `json:"run_id"`
		} `json:"in_flight"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code != "turn_in_progress" {
		t.Errorf("code = %q, want turn_in_progress", body.Error.Code)
	}
	if body.InFlight.ThreadID != testThreadID || body.InFlight.RunID != testRunID {
		t.Errorf("in_flight = %+v, want the ids needed to resume", body.InFlight)
	}

	rec, first, err := f.store.Begin(context.Background(), idempotency.Key(testCompany, "k-1"), "any-hash")
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if first || rec == nil {
		t.Fatal("the 504 discarded its idempotency key — a retry would start a second billed turn")
	}
}

// A retry under the same key, after the original finished, is answered from the
// transcript rather than by running the turn again. This is the replayer the
// idempotency middleware exists to call: a streamed answer has no bytes to
// store, so a replay means re-deriving.
func TestARetriedSendReplaysTheAnswerWithoutASecondTurn(t *testing.T) {
	f := newChatFixture(t, 5*time.Second)
	f.messages.persist(assistantMessage())
	body := `{"message":"sales last month?","user_ref":"their-user-42"}`

	if w := f.send(t, sendRequest(t, "application/json", body), nil); w.Code != http.StatusOK {
		t.Fatalf("first status = %d: %s", w.Code, w.Body.String())
	}
	calls := f.enq.in
	f.enq.in = app.ChatInput{}

	w := f.send(t, sendRequest(t, "application/json", body), nil)

	if w.Code != http.StatusOK {
		t.Fatalf("replay status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Idempotent-Replay") != "true" {
		t.Error("the replay is not marked Idempotent-Replay: true")
	}
	if f.enq.in.Message != "" {
		t.Errorf("the replay enqueued a second turn: %+v", f.enq.in)
	}
	if calls.Message == "" {
		t.Fatal("the original never enqueued anything")
	}
	var turn turnResponse
	if err := json.Unmarshal(w.Body.Bytes(), &turn); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if turn.Message.Content != assistantMessage().Content {
		t.Errorf("replay body = %+v, want the same answer", turn)
	}
}

// Last-Event-ID resumes from the persisted transcript. Deltas are not in it —
// they exist nowhere but the connection that carried them — so what comes back
// is the messages, which is the part that was real.
func TestResumeReplaysThePersistedMessagesAfterLastEventID(t *testing.T) {
	f := newChatFixture(t, 5*time.Second)
	f.messages.persist(assistantMessage())
	missed := &domain.Message{
		ID: "3f7c1f3e-0000-4000-8000-0000000000bb", ThreadID: testThreadID,
		Role: domain.MessageRoleUser, Content: "and the month before?",
		CreatedAt: testAnswerAt.Add(-30 * time.Second),
	}
	f.messages.page = []*domain.Message{missed}

	lastSeen := apiv1.EncodeCursor(testAnswerAt.Add(-time.Minute), "3f7c1f3e-0000-4000-8000-00000000000f")
	req := httptest.NewRequest(http.MethodGet, "/v1/threads/"+testThreadID+"/events", nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Last-Event-ID", lastSeen)

	w := f.send(t, req, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	frames, _ := parseSSE(t, w.Body.String())
	replayed := frameOf(t, frames, "message")
	var msg messageResponse
	if err := json.Unmarshal([]byte(replayed.Data), &msg); err != nil {
		t.Fatalf("message payload: %v", err)
	}
	if msg.ID != missed.ID {
		t.Errorf("replayed %+v, want the message the client missed", msg)
	}
	// The resume asked for everything after the client's own position, not from
	// the top: a resume that replays the whole thread is a resume that costs
	// more than the reconnect it is covering.
	if f.messages.gotFilt.CursorID != "3f7c1f3e-0000-4000-8000-00000000000f" {
		t.Errorf("resumed from %+v, want the cursor the client sent", f.messages.gotFilt)
	}
}

// Attaching to a settled thread delivers the answer and closes.
//
// The thread row here carries the skew the live gate found: `last_message_at`
// is written by the API's clock and `created_at` by Postgres's, and they land
// microseconds apart in the wrong direction. Deciding "has this turn answered?"
// by comparing those two columns held the connection open on every settled
// thread — for an answer that was already in the database.
func TestAttachingToASettledThreadDeliversTheAnswer(t *testing.T) {
	f := newChatFixture(t, 5*time.Second)
	answer := assistantMessage()
	f.messages.persist(answer)
	f.threads.thread = apiThread()
	f.threads.thread.LastMessageAt = answer.CreatedAt.Add(130 * time.Microsecond)

	req := httptest.NewRequest(http.MethodGet, "/v1/threads/"+testThreadID+"/events", nil)
	req.Header.Set("Accept", "text/event-stream")
	w := f.send(t, req, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	frames, _ := parseSSE(t, w.Body.String())
	final := frameOf(t, frames, "final")
	var turn turnResponse
	if err := json.Unmarshal([]byte(final.Data), &turn); err != nil {
		t.Fatalf("final payload: %v", err)
	}
	if turn.Message.ID != answer.ID {
		t.Errorf("final = %+v, want the answer already in the transcript", turn.Message)
	}
}

// An attach that cannot bound the turn's usage window reports no usage rather
// than zeros. Zeros would say the turn was free, which is never true.
func TestAZeroUsageWindowIsOmittedRatherThanPublished(t *testing.T) {
	f := newChatFixture(t, 5*time.Second)
	f.messages.persist(assistantMessage())

	req := httptest.NewRequest(http.MethodGet, "/v1/threads/"+testThreadID+"/events", nil)
	req.Header.Set("Accept", "text/event-stream")

	// A router of its own, around a usage reader that reports an empty window.
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/v1")
	v1.Use(func(c *gin.Context) {
		c.Set("company_id", testCompany)
		c.Set(middleware.CtxAPIKeyScopes, f.scopes)
	})
	NewV1ChatHandler(f.enq, f.threads, f.messages, emptyUsage{}, f.rdb, f.store, time.Second).Register(v1)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	frames, _ := parseSSE(t, w.Body.String())
	final := frameOf(t, frames, "final")
	if strings.Contains(final.Data, `"usage"`) {
		t.Errorf("final = %s, want no usage block for an empty window", final.Data)
	}
}

// emptyUsage reports a window with nothing in it.
type emptyUsage struct{}

func (emptyUsage) SummaryByThread(context.Context, string, string, time.Time, time.Time) (*domain.UsageSummary, error) {
	return &domain.UsageSummary{}, nil
}

// The other half: a thread whose newest message is the question has not
// answered yet, so the stream stays open for it rather than replaying the
// previous turn's answer as if it were this one's.
func TestAttachingMidTurnWaitsForTheAnswerToThisQuestion(t *testing.T) {
	f := newChatFixture(t, 5*time.Second)
	question := &domain.Message{
		ID: "3f7c1f3e-0000-4000-8000-0000000000cc", ThreadID: testThreadID,
		Role: domain.MessageRoleUser, Content: "and the month before?",
		CreatedAt: testAnswerAt.Add(time.Minute),
	}
	answer := assistantMessage()
	answer.CreatedAt = question.CreatedAt.Add(time.Second)
	f.messages.mu.Lock()
	f.messages.latest = question
	f.messages.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/v1/threads/"+testThreadID+"/events", nil)
	req.Header.Set("Accept", "text/event-stream")

	w := f.send(t, req, func() {
		// Only now does the turn answer, exactly as the worker would.
		f.messages.persist(answer)
		f.publish(t, app.ChatEvent{Type: "final", JobID: question.ID, Content: "IDR 3.863.405.700"})
	})

	frames, _ := parseSSE(t, w.Body.String())
	final := frameOf(t, frames, "final")
	var turn turnResponse
	if err := json.Unmarshal([]byte(final.Data), &turn); err != nil {
		t.Fatalf("final payload: %v", err)
	}
	// The run id is the question's own id — what ChatEvent carries as JobID —
	// so a caller who attached before any event arrived sees the same string
	// the events use.
	if turn.RunID != question.ID {
		t.Errorf("run_id = %q, want the question's id", turn.RunID)
	}
}

// A Last-Event-ID this API did not issue is refused before the stream starts.
// After the 200 an SSE response commits to, there is no status left to say it
// with — and a stream that silently drops the resume looks like a stream that
// lost the messages.
func TestABadLastEventIDIsRefusedBeforeTheStreamOpens(t *testing.T) {
	f := newChatFixture(t, 5*time.Second)

	req := httptest.NewRequest(http.MethodGet, "/v1/threads/"+testThreadID+"/events", nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Last-Event-ID", "not-a-cursor!!")

	w := f.send(t, req, nil)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid_cursor") {
		t.Errorf("body = %q, want invalid_cursor", w.Body.String())
	}
}

// The stream carries what is happening, not what was queried. Tool arguments
// are the SQL the agent ran against the tenant's warehouse; the place for that
// is T-05's audit log, redacted on the way in and admin-only.
func TestToolFramesCarryTheNameAndNotTheArguments(t *testing.T) {
	f := newChatFixture(t, 5*time.Second)

	w := f.send(t, sendRequest(t, "text/event-stream", `{"message":"hi","user_ref":"u"}`), func() {
		f.publish(t, app.ChatEvent{Type: "tool_call", ToolCall: &app.ToolCallEvent{
			Name:      "run_sql",
			Arguments: map[string]interface{}{"query": "SELECT secret FROM payroll"},
		}})
		f.messages.persist(assistantMessage())
		f.publish(t, app.ChatEvent{Type: "final", Content: "done"})
	})

	body := w.Body.String()
	if !strings.Contains(body, `"tool":"run_sql"`) {
		t.Errorf("body = %q, want the tool's name", body)
	}
	if strings.Contains(body, "payroll") {
		t.Error("the stream leaked a tool's arguments")
	}
}

// A heartbeat is a comment, so it costs an integrator no code — and it is what
// keeps an idle proxy from closing a stream that is merely thinking.
func TestAnIdleStreamSendsHeartbeats(t *testing.T) {
	f := newChatFixture(t, 5*time.Second)

	req := httptest.NewRequest(http.MethodGet, "/v1/threads/"+testThreadID+"/events", nil)
	req.Header.Set("Accept", "text/event-stream")
	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.router.ServeHTTP(w, req)
	}()
	f.waitForSubscriber(t)

	time.Sleep(120 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the stream did not end when the client hung up")
	}

	_, comments := parseSSE(t, w.Body.String())
	if comments == 0 {
		t.Error("no heartbeat in an idle stream — an idle proxy would close it silently")
	}
}

// A client that hangs up ends the read and nothing else. The other half of this
// — that the turn finishes and the answer persists — is the worker's, and is
// the live gate's to show.
func TestAClientHangupEndsTheStreamAndNotTheTurn(t *testing.T) {
	f := newChatFixture(t, 5*time.Second)

	req := httptest.NewRequest(http.MethodGet, "/v1/threads/"+testThreadID+"/events", nil)
	req.Header.Set("Accept", "text/event-stream")
	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.router.ServeHTTP(w, req)
	}()
	f.waitForSubscriber(t)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the handler kept reading after the client disconnected")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d — a disconnect is the ordinary end of a stream, not an error", w.Code)
	}
}

// The ticket's fifth acceptance item, and the reason `user_ref` is enforced
// rather than trusted: the key belongs to the company, so the only thing that
// makes one of a tenant's users unable to read another's conversation is us
// holding the caller to the reference they named.
func TestAnotherUserRefCannotReadTheThread(t *testing.T) {
	f := newChatFixture(t, 5*time.Second)

	for _, path := range []string{
		"/v1/threads/" + testThreadID + "?user_ref=someone-else",
		"/v1/threads/" + testThreadID + "/messages?user_ref=someone-else",
	} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			f.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404: %s", w.Code, w.Body.String())
			}
			// 404 rather than 403: a 403 confirms the thread exists, which is
			// the enumeration this check is closing.
			if !strings.Contains(w.Body.String(), "thread_not_found") {
				t.Errorf("body = %q, want thread_not_found", w.Body.String())
			}
		})
	}
}

// A dashboard thread is not addressable from `/v1`. A machine credential
// reading the conversations of named people who have their own sessions is a
// leaked key reading the staff's chat history.
func TestADashboardThreadIsNotVisibleOverV1(t *testing.T) {
	f := newChatFixture(t, 5*time.Second)
	f.threads.thread = &domain.ConversationThread{
		ID: testThreadID, CompanyID: testCompany, Channel: domain.ChannelDashboard,
		UserID: "user-1",
	}

	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/threads/"+testThreadID, nil))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", w.Code, w.Body.String())
	}
}

func TestListThreadsIsScopedToTheAPIChannel(t *testing.T) {
	f := newChatFixture(t, 5*time.Second)
	f.threads.page = []*domain.ConversationThread{apiThread()}
	f.threads.hasMore = true

	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/threads?user_ref=their-user-42&limit=1", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if f.threads.gotFilt.Channel != domain.ChannelAPI {
		t.Errorf("filter channel = %q, want api", f.threads.gotFilt.Channel)
	}
	if f.threads.gotFilt.APIUserRef != "their-user-42" || f.threads.gotFilt.Limit != 1 {
		t.Errorf("filter = %+v, want the caller's own narrowing", f.threads.gotFilt)
	}

	var page apiv1.Page[threadResponse]
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].UserRef != "their-user-42" {
		t.Fatalf("page = %+v", page)
	}
	// has_more with a next_cursor, or the caller has no way to ask for the
	// rest of a list they have been told exists.
	if !page.HasMore || page.NextCursor == "" {
		t.Errorf("page = %+v, want a cursor for the next one", page)
	}
	// A thread response is not the domain struct: it must not publish the
	// phone number and Lark keys that belong to other channels.
	if strings.Contains(w.Body.String(), "phone_number") || strings.Contains(w.Body.String(), "lark_") {
		t.Errorf("body = %q leaks another channel's identity fields", w.Body.String())
	}
}

// A refused turn is a typed 402, not a 500: a programmatic caller retries a
// 500, and retrying a turn the tenant cannot pay for is a loop that stops only
// when somebody notices.
func TestACreditRefusalIs402(t *testing.T) {
	f := newChatFixture(t, 5*time.Second)
	f.enq.err = domain.ErrInsufficientCredits

	w := f.send(t, sendRequest(t, "application/json", `{"message":"hi","user_ref":"u"}`), nil)

	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "credits_exhausted") {
		t.Errorf("body = %q, want credits_exhausted", w.Body.String())
	}
}

func TestAThreadFromAnotherSurfaceIsRefusedOnSend(t *testing.T) {
	f := newChatFixture(t, 5*time.Second)
	// Wrapped exactly as ChatEnqueuer wraps it. errors.Is has to see through
	// both layers, or a caller who sent a fixable request gets a 500.
	f.enq.err = fmt.Errorf("resolve thread: %w: thread was not started over the API", domain.ErrInvalidInput)

	w := f.send(t, sendRequest(t, "application/json", `{"message":"hi","thread_id":"th-9"}`), nil)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid_thread") {
		t.Errorf("body = %q, want invalid_thread", w.Body.String())
	}
}

func TestSendRequiresAMessageAndAnIdentity(t *testing.T) {
	cases := []struct {
		name, body, wantCode string
	}{
		{"no message", `{"user_ref":"u"}`, "message_required"},
		{"blank message", `{"message":"   ","user_ref":"u"}`, "message_required"},
		{"no identity", `{"message":"hi"}`, "user_ref_required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newChatFixture(t, 5*time.Second)
			w := f.send(t, sendRequest(t, "application/json", tc.body), nil)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tc.wantCode) {
				t.Errorf("body = %q, want %s", w.Body.String(), tc.wantCode)
			}
		})
	}
}

func TestDeleteRemovesTheThread(t *testing.T) {
	f := newChatFixture(t, 5*time.Second)

	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/v1/threads/"+testThreadID, nil))

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", w.Code, w.Body.String())
	}
	if len(f.threads.deleted) != 1 || f.threads.deleted[0] != testThreadID {
		t.Errorf("deleted = %v, want the one thread", f.threads.deleted)
	}
}

// compile-time proof the fakes match the contracts the handler takes.
var (
	_ V1ChatEnqueuer           = (*fakeEnqueuer)(nil)
	_ domain.ThreadRepository  = (*fakeThreads)(nil)
	_ domain.MessageRepository = (*fakeMessages)(nil)
	_ V1TurnUsageReader        = fakeUsage{}
)
