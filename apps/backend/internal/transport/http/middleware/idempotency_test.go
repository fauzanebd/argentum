package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/idempotency"
)

// memStore is the Store an in-process test needs: the middleware's logic is
// what these tests are about, and the Redis half has its own tests against a
// real server in internal/idempotency.
type memStore struct {
	mu      sync.Mutex
	records map[string]*idempotency.Record
	failing bool
}

func newMemStore() *memStore {
	return &memStore{records: map[string]*idempotency.Record{}}
}

func (m *memStore) Begin(_ context.Context, key, bodyHash string) (*idempotency.Record, bool, error) {
	if m.failing {
		return nil, false, errors.New("redis is down")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if rec, ok := m.records[key]; ok {
		copied := *rec
		return &copied, false, nil
	}
	m.records[key] = &idempotency.Record{BodyHash: bodyHash, Status: idempotency.StatusInFlight}
	return nil, true, nil
}

func (m *memStore) Progress(_ context.Context, key string, result json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rec, ok := m.records[key]; ok {
		rec.Result = result
	}
	return nil
}

// Complete and Discard honour cancellation, unlike Begin and Progress, because
// they are the two the middleware calls *after* the handler has returned — the
// point at which a streaming route's request context is routinely already
// dead. A fake that ignored the context would make that whole class of bug
// untestable here, and it is a real one: go-redis refuses a command on a
// cancelled context without touching the socket.
func (m *memStore) Complete(ctx context.Context, key string, status int, result json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if rec, ok := m.records[key]; ok {
		rec.Status = idempotency.StatusCompleted
		rec.HTTPStatus = status
		rec.Result = result
	}
	return nil
}

func (m *memStore) Discard(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.records, key)
	return nil
}

func (m *memStore) get(key string) *idempotency.Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.records[key]
}

// idemRouter stands in for a `/v1` write route. `runs` counts how many times
// the handler body actually executed, which is the property under test in
// every case below: a replay that re-runs the handler has already failed,
// whatever it answers.
func idemRouter(store idempotency.Store, runs *int, opts ...IdempotencyOption) *gin.Engine {
	r := gin.New()
	v1 := r.Group("/v1")
	v1.Use(RequestID())
	v1.Use(func(c *gin.Context) { c.Set("company_id", "co-1") })
	v1.POST("/reports", Idempotency(store, opts...), func(c *gin.Context) {
		*runs++
		DeclareIdempotentResult(c, gin.H{"report_id": "rep-1", "status": "completed"})
		c.JSON(http.StatusCreated, gin.H{"report_id": "rep-1", "status": "completed"})
	})
	return r
}

func postWithKey(t *testing.T, r *gin.Engine, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/reports", strings.NewReader(body))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestReplayReturnsTheSameResponseAndRunsOnce(t *testing.T) {
	store, runs := newMemStore(), 0
	r := idemRouter(store, &runs)

	first := postWithKey(t, r, "k-1", `{"spec":1}`)
	second := postWithKey(t, r, "k-1", `{"spec":1}`)

	if runs != 1 {
		t.Fatalf("handler ran %d times, want 1 — the replay executed the request again", runs)
	}
	if first.Code != second.Code {
		t.Errorf("status %d then %d — a replay must answer what the original answered", first.Code, second.Code)
	}
	if first.Body.String() != second.Body.String() {
		t.Errorf("body %q then %q", first.Body.String(), second.Body.String())
	}
	if first.Header().Get("Idempotent-Replay") != "" {
		t.Error("the original response is marked as a replay")
	}
	if second.Header().Get("Idempotent-Replay") != "true" {
		t.Error("the replayed response is not marked Idempotent-Replay: true")
	}
}

func TestSameKeyWithADifferentBodyIs409(t *testing.T) {
	store, runs := newMemStore(), 0
	r := idemRouter(store, &runs)

	postWithKey(t, r, "k-1", `{"spec":1}`)
	w := postWithKey(t, r, "k-1", `{"spec":2}`)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 — this is the broken retry loop that bills twice", w.Code)
	}
	if got := envelopeOf(t, w).Code; got != "idempotency_key_reuse" {
		t.Errorf("code = %q, want idempotency_key_reuse", got)
	}
	if runs != 1 {
		t.Errorf("handler ran %d times, want 1", runs)
	}
}

func TestReplayOfAnInFlightRequestIs409WithTheIDsItHas(t *testing.T) {
	store, runs := newMemStore(), 0
	r := idemRouter(store, &runs)

	// The original request is still running: its record exists, in flight,
	// carrying what the handler declared early.
	key := idempotency.Key("co-1", "k-1")
	if _, _, err := store.Begin(context.Background(), key, bodyHashOf(`{"spec":1}`)); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := store.Progress(context.Background(), key, json.RawMessage(`{"thread_id":"th-1"}`)); err != nil {
		t.Fatalf("Progress: %v", err)
	}

	w := postWithKey(t, r, "k-1", `{"spec":1}`)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	if runs != 0 {
		t.Fatalf("handler ran %d times — a mid-flight retry started a second turn", runs)
	}
	var body struct {
		Error    struct{ Code string }
		InFlight struct {
			ThreadID string `json:"thread_id"`
		} `json:"in_flight"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code != "request_in_flight" {
		t.Errorf("code = %q, want request_in_flight", body.Error.Code)
	}
	// Without the id the caller has nothing to poll and no reason not to
	// retry again in a second.
	if body.InFlight.ThreadID != "th-1" {
		t.Errorf("in_flight.thread_id = %q, want th-1", body.InFlight.ThreadID)
	}
}

func TestAFailedRequestForgetsItsKey(t *testing.T) {
	store := newMemStore()
	runs := 0
	r := gin.New()
	v1 := r.Group("/v1")
	v1.Use(func(c *gin.Context) { c.Set("company_id", "co-1") })
	v1.POST("/reports", Idempotency(store), func(c *gin.Context) {
		runs++
		if runs == 1 {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "boom"})
			return
		}
		c.JSON(http.StatusCreated, gin.H{"report_id": "rep-1"})
	})

	if w := postWithKey(t, r, "k-1", `{"spec":1}`); w.Code != http.StatusInternalServerError {
		t.Fatalf("first status = %d, want 500", w.Code)
	}
	// Retrying after a 500 is exactly what a well-behaved client does next.
	// A key that survived the failure would refuse it for 24 hours.
	if w := postWithKey(t, r, "k-1", `{"spec":1}`); w.Code != http.StatusCreated {
		t.Fatalf("retry after a failure = %d, want 201", w.Code)
	}
	if runs != 2 {
		t.Errorf("handler ran %d times, want 2", runs)
	}
}

// The exception to the rule above, and the reason it is an exception: a 504
// from `POST /v1/chat`'s synchronous door means the *wait* ran out, not the
// turn. That turn is still running and still being billed, so forgetting the
// key would let the retry a 504 invites start a second one (T-A3).
func TestARetainedRecordSurvivesAFailedResponse(t *testing.T) {
	store := newMemStore()
	runs := 0
	r := gin.New()
	v1 := r.Group("/v1")
	v1.Use(func(c *gin.Context) { c.Set("company_id", "co-1") })
	v1.POST("/reports", Idempotency(store), func(c *gin.Context) {
		runs++
		DeclareIdempotentResult(c, gin.H{"thread_id": "th-1", "run_id": "msg-1"})
		RetainIdempotentRecord(c)
		c.JSON(http.StatusGatewayTimeout, gin.H{"error": "still running"})
	})

	if w := postWithKey(t, r, "k-1", `{"m":1}`); w.Code != http.StatusGatewayTimeout {
		t.Fatalf("first status = %d, want 504", w.Code)
	}
	rec := store.get(idempotency.Key("co-1", "k-1"))
	if rec == nil {
		t.Fatal("the record was discarded — a retry would start a second turn")
	}
	if rec.Status != idempotency.StatusCompleted || !strings.Contains(string(rec.Result), "th-1") {
		t.Fatalf("record = %+v, want a completed record carrying the ids", rec)
	}
	postWithKey(t, r, "k-1", `{"m":1}`)
	if runs != 1 {
		t.Errorf("handler ran %d times, want 1", runs)
	}
}

// A streaming route ends when the client hangs up, which cancels the request
// context. The bookkeeping for work that has already run must not be abandoned
// with it: a record left in_flight refuses every retry for the full 24-hour
// TTL, for a turn that finished minutes ago (T-A3).
func TestTheRecordIsCompletedEvenWhenTheClientHangsUp(t *testing.T) {
	store := newMemStore()
	r := gin.New()
	v1 := r.Group("/v1")
	v1.Use(func(c *gin.Context) { c.Set("company_id", "co-1") })
	v1.POST("/reports", Idempotency(store), func(c *gin.Context) {
		DeclareIdempotentResult(c, gin.H{"thread_id": "th-1"})
		// What a disconnected SSE handler leaves behind.
		ctx, cancel := context.WithCancel(c.Request.Context())
		cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Status(http.StatusOK)
	})

	postWithKey(t, r, "k-1", `{"m":1}`)

	rec := store.get(idempotency.Key("co-1", "k-1"))
	if rec == nil || rec.Status != idempotency.StatusCompleted {
		t.Fatalf("record = %+v, want completed — a hung-up stream stranded its key", rec)
	}
}

func TestMissingKeyIsRefusedOnlyWhereItIsRequired(t *testing.T) {
	t.Run("required", func(t *testing.T) {
		store, runs := newMemStore(), 0
		w := postWithKey(t, idemRouter(store, &runs, IdempotencyRequired()), "", `{"spec":1}`)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", w.Code)
		}
		if got := envelopeOf(t, w).Code; got != "idempotency_key_required" {
			t.Errorf("code = %q, want idempotency_key_required", got)
		}
		if runs != 0 {
			t.Errorf("handler ran without the key it requires")
		}
	})

	t.Run("optional", func(t *testing.T) {
		store, runs := newMemStore(), 0
		w := postWithKey(t, idemRouter(store, &runs), "", `{"spec":1}`)

		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 — the header is optional here", w.Code)
		}
		if runs != 1 {
			t.Errorf("handler ran %d times, want 1", runs)
		}
	})
}

// A Redis outage must not take the write surface down with it. The cost —
// a retry during the outage can duplicate — is stated in the middleware.
func TestAnUnavailableStoreDoesNotFailTheRequest(t *testing.T) {
	store := newMemStore()
	store.failing = true
	runs := 0

	w := postWithKey(t, idemRouter(store, &runs), "k-1", `{"spec":1}`)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 — a Redis hiccup must not refuse writes", w.Code)
	}
	if runs != 1 {
		t.Errorf("handler ran %d times, want 1", runs)
	}
}

// The record is the ids, never the payload. This is what keeps Redis from
// becoming a document store, and it is an acceptance item in its own right.
func TestARecordStaysSmallWhateverTheResponseWeighs(t *testing.T) {
	store := newMemStore()
	r := gin.New()
	v1 := r.Group("/v1")
	v1.Use(func(c *gin.Context) { c.Set("company_id", "co-1") })
	v1.POST("/reports", Idempotency(store), func(c *gin.Context) {
		DeclareIdempotentResult(c, gin.H{"document_id": "doc-1", "status": "completed"})
		// A rendered PDF, in the shape a caller would actually receive it.
		c.Data(http.StatusOK, "application/pdf", []byte(strings.Repeat("%PDF", 10*1024*1024/4)))
	})

	postWithKey(t, r, "k-1", `{"spec":1}`)

	rec := store.get(idempotency.Key("co-1", "k-1"))
	if rec == nil {
		t.Fatal("no record was written")
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(raw) > idempotency.MaxRecordBytes {
		t.Errorf("record is %d bytes after a 10 MB response, cap is %d", len(raw), idempotency.MaxRecordBytes)
	}
}

// A replayer exists for the two routes whose response cannot be rebuilt by
// echoing JSON: a download URL has to be re-presigned, and an SSE stream has
// to be re-attached to.
func TestACustomReplayerTakesOver(t *testing.T) {
	store, runs := newMemStore(), 0
	called := false
	r := idemRouter(store, &runs, IdempotencyReplayWith(func(c *gin.Context, rec *idempotency.Record) bool {
		called = true
		c.JSON(http.StatusOK, gin.H{"url": "https://storage.example/fresh-signature"})
		return true
	}))

	postWithKey(t, r, "k-1", `{"spec":1}`)
	w := postWithKey(t, r, "k-1", `{"spec":1}`)

	if !called {
		t.Fatal("the replayer was not consulted")
	}
	if runs != 1 {
		t.Errorf("handler ran %d times, want 1", runs)
	}
	if !strings.Contains(w.Body.String(), "fresh-signature") {
		t.Errorf("body = %q, want the replayer's response", w.Body.String())
	}
}

func TestAnOverlongKeyIsRefused(t *testing.T) {
	store, runs := newMemStore(), 0
	w := postWithKey(t, idemRouter(store, &runs), strings.Repeat("k", maxIdempotencyKeyLen+1), `{"spec":1}`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if got := envelopeOf(t, w).Code; got != "idempotency_key_too_long" {
		t.Errorf("code = %q, want idempotency_key_too_long", got)
	}
}

// bodyHashOf mirrors what the middleware hashes, so a test can plant a record
// that a subsequent request is supposed to match.
func bodyHashOf(body string) string {
	req := httptest.NewRequest(http.MethodPost, "/v1/reports", strings.NewReader(body))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	raw, _ := readAndRestoreBody(c)
	return bodyHash(raw)
}
