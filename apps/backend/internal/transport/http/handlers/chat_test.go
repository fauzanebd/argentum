package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
)

// The status codes the dashboard chat answers a rejected agent pick with
// (T-S3). Which picks are rejected is decided in internal/app and tested
// there; this is the other half — that a refusal reaches the browser as 404
// and not as the 400 every enqueue error used to become.

func TestChatFailStatusCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name string
		err  error
		want int
		body string
	}{
		{
			// The acceptance item: another company's agent id, an id that
			// never existed, and a disabled agent all arrive here as one
			// error, and all three have to read the same to the caller.
			name: "a refused agent pick",
			err:  fmt.Errorf("%w: no such agent", domain.ErrNotFound),
			want: http.StatusNotFound,
			body: "no such agent",
		},
		{
			name: "changing a conversation's agent",
			err:  fmt.Errorf("%w: a conversation cannot change agent", domain.ErrInvalidInput),
			want: http.StatusBadRequest,
			body: "cannot change agent",
		},
		{
			// T-Z4: the conversation's own agent, restricted since. Says why,
			// and names the agent.
			name: "an agent the person may no longer talk to",
			err:  fmt.Errorf("%w: HR", app.ErrAgentRestricted),
			want: http.StatusForbidden,
			body: "has restricted this agent and has not granted it to you: HR",
		},
		{
			name: "a new conversation with no agent open to the person",
			err:  app.ErrNoAgentAvailable,
			want: http.StatusForbidden,
			body: "no agent in this workspace is open to you",
		},
		{
			// A grant read that failed is a retry, and the database's own
			// message does not reach the browser.
			name: "an access check that could not be made",
			err:  fmt.Errorf("%w: %w", app.ErrAccessCheckFailed, errors.New("pq: connection refused")),
			want: http.StatusServiceUnavailable,
			body: "could not check access to that agent; try again",
		},
		{
			// Everything the enqueue path could already fail with keeps the
			// status it had before this ticket. A 500 leaking out of here as a
			// 404 would send an admin looking for a row rather than a log.
			name: "any other enqueue failure",
			err:  errors.New("thread does not belong to company"),
			want: http.StatusBadRequest,
			body: "does not belong",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/chat", nil)

			chatFail(c, tc.err)

			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), tc.body) {
				t.Errorf("body = %s, want it to mention %q", rec.Body, tc.body)
			}
		})
	}
}

// A 404 must not name what it refused. "no such agent" is the whole message
// for all three refusals, so a caller cannot tell an id that is real and
// somebody else's from one that is neither.
func TestARefusedPickRevealsNothing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/chat", nil)

	chatFail(c, fmt.Errorf("%w: no such agent", domain.ErrNotFound))

	for _, leak := range []string{"disabled", "company", "enabled"} {
		if strings.Contains(strings.ToLower(rec.Body.String()), leak) {
			t.Errorf("the refusal mentions %q, which distinguishes it from the others: %s", leak, rec.Body)
		}
	}
}

// ---- RIZ-26: ids in a request body ----------------------------------------
//
// The path ids are RequireWellFormedIDs' (cmd/api). These are the three routes
// whose ids arrive in the body instead, where no middleware can read them:
// `POST /chat`, `POST /threads` and `POST /threads/:id/participants`. Each pair
// below sends `x` and then a well-formed id that names nothing, through the
// real enqueuer and participant service over fakes that answer `x` the way
// Postgres does, and the two answers must be identical.

// errPQInvalidUUID is what Postgres answers a non-uuid compared with a uuid column,
// as lib/pq prints it — the text that used to reach the browser.
var errPQInvalidUUID = errors.New(`pq: invalid input syntax for type uuid: "x"`)

const (
	bodyIDThread  = "7d0b1c2e-3f4a-4b5c-8d6e-7f8091a2b3c4" // co-1's dashboard thread
	bodyIDUnknown = "0e9d8c7b-6a5f-4e3d-9c2b-1a0f9e8d7c6b" // names nothing
)

// bodyIDStore is the thread table, the roster and the room at once. It counts
// what the request did: every id that reached it malformed (the 22P02 the
// handler must now prevent), and every write.
type bodyIDStore struct {
	domain.ThreadRepository
	domain.ThreadParticipantRepository
	malformed, threadsCreated, participantsAdded, enqueued int
}

func (s *bodyIDStore) lookup(id string) error {
	if !middleware.WellFormedID(id) {
		s.malformed++
		return errPQInvalidUUID
	}
	if id == bodyIDThread {
		return nil
	}
	return domain.ErrNotFound
}

func (s *bodyIDStore) GetByID(_ context.Context, id string) (*domain.ConversationThread, error) {
	if err := s.lookup(id); err != nil {
		return nil, err
	}
	return &domain.ConversationThread{ID: id, CompanyID: "co-1", Channel: domain.ChannelDashboard}, nil
}

func (s *bodyIDStore) GetForCompany(ctx context.Context, _, id string) (*domain.ConversationThread, error) {
	return s.GetByID(ctx, id)
}

func (s *bodyIDStore) Create(_ context.Context, t *domain.ConversationThread) error {
	s.threadsCreated++
	t.ID = bodyIDThread
	return nil
}

func (s *bodyIDStore) Add(context.Context, string, *domain.ThreadParticipant) error {
	s.participantsAdded++
	return nil
}

// bodyIDRoster is the agent table: no agent exists, and `x` is a 22P02.
type bodyIDRoster struct{ store *bodyIDStore }

func (r bodyIDRoster) GetDefault(context.Context, string) (*domain.Agent, error) {
	return nil, domain.ErrNotFound
}

func (r bodyIDRoster) GetByID(_ context.Context, _, id string) (*domain.Agent, error) {
	if err := r.store.lookup(id); err != nil {
		return nil, err
	}
	return nil, domain.ErrNotFound
}

func (s *bodyIDStore) EnqueueChatRun(context.Context, queue.ChatRunPayload) (string, error) {
	s.enqueued++
	return "task-1", nil
}

func bodyIDRouter(t *testing.T) (*gin.Engine, *bodyIDStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := &bodyIDStore{}
	roster := bodyIDRoster{store: store}
	threads := app.NewThreadService(store, nil, nil, nil, app.ThreadServiceConfig{})
	enq := app.NewChatEnqueuer(threads, nil, nil, store).WithRoster(roster)
	members := app.NewThreadParticipantService(store, store, roster, 0)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("company_id", "co-1")
		c.Set("user_id", "u-1")
		c.Set("role", "member")
		c.Next()
	})
	NewChatHandler(enq, store, nil).WithParticipants(members).Register(r.Group(""))
	return r, store
}

// TestAMalformedBodyIDAnswersAsAnUnknownOne is R5, R6, AC3, AC4 and Q5.
func TestAMalformedBodyIDAnswersAsAnUnknownOne(t *testing.T) {
	cases := []struct {
		name, path string
		body       func(id string) string
		// wantStatus pins what both answers are, so a change to what an
		// unknown id answers is a decision this test makes somebody take.
		wantStatus int
		wantBody   string
	}{
		{
			name: "POST /chat into a thread", path: "/chat",
			body:       func(id string) string { return `{"message":"hi","thread_id":"` + id + `"}` },
			wantStatus: http.StatusNotFound, wantBody: `{"error":"no such agent"}`,
		},
		{
			name: "POST /chat opening one with an agent", path: "/chat",
			body:       func(id string) string { return `{"message":"hi","agent_id":"` + id + `"}` },
			wantStatus: http.StatusNotFound, wantBody: `{"error":"no such agent"}`,
		},
		{
			name: "POST /threads/:id/participants", path: "/threads/" + bodyIDThread + "/participants",
			body:       func(id string) string { return `{"agent_id":"` + id + `"}` },
			wantStatus: http.StatusNotFound, wantBody: `{"error":"not found"}`,
		},
		{
			name: "POST /threads with an agent", path: "/threads",
			body:       func(id string) string { return `{"agent_id":"` + id + `"}` },
			wantStatus: http.StatusNotFound, wantBody: `{"error":"no such agent"}`,
		},
		{
			name: "POST /threads with a participant", path: "/threads",
			body:       func(id string) string { return `{"participant_ids":["` + id + `"]}` },
			wantStatus: http.StatusNotFound, wantBody: `{"error":"not found"}`,
		},
	}
	send := func(r *gin.Engine, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, unknownStore := bodyIDRouter(t)
			unknown := send(r, tc.path, tc.body(bodyIDUnknown))
			if unknown.Code != tc.wantStatus || unknown.Body.String() != tc.wantBody {
				t.Fatalf("a well-formed unknown id: %d %s, want %d %s", unknown.Code, unknown.Body, tc.wantStatus, tc.wantBody)
			}

			for _, bad := range []string{"x", "123", "urn:uuid:" + bodyIDUnknown} {
				r, store := bodyIDRouter(t)
				got := send(r, tc.path, tc.body(bad))
				if got.Code != unknown.Code || got.Body.String() != unknown.Body.String() {
					t.Errorf("%q: %d %s, want what an unknown id gets: %d %s", bad, got.Code, got.Body, unknown.Code, unknown.Body)
				}
				if strings.Contains(got.Body.String(), "pq:") {
					t.Errorf("%q: the body names the driver: %s", bad, got.Body)
				}
				if store.malformed != 0 {
					t.Errorf("%q reached a query %d times", bad, store.malformed)
				}
				if store.threadsCreated+store.participantsAdded+store.enqueued != 0 {
					t.Errorf("%q wrote something: %d threads, %d participants, %d turns",
						bad, store.threadsCreated, store.participantsAdded, store.enqueued)
				}
			}
			// The unknown id is allowed to have written a thread (the
			// participant case does, then refuses); nothing more.
			if unknownStore.participantsAdded+unknownStore.enqueued != 0 {
				t.Errorf("an unknown id wrote %d participants and %d turns", unknownStore.participantsAdded, unknownStore.enqueued)
			}
		})
	}
}

// The checks only ever stand in for a refusal: a well-formed id that exists,
// and the empty values the services have their own answers for, go through.
func TestWellFormedAndEmptyBodyIDsReachTheServices(t *testing.T) {
	r, store := bodyIDRouter(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"message":"hi","thread_id":"`+strings.ToUpper(bodyIDThread)+`"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	// The fake only knows the lower-case spelling, so the upper-case one
	// reaching it and being looked up is the proof it was let through.
	if w.Code != http.StatusNotFound || store.malformed != 0 {
		t.Errorf("an upper-case uuid: %d %s, malformed lookups %d", w.Code, w.Body, store.malformed)
	}

	r, _ = bodyIDRouter(t)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/threads/"+bodyIDThread+"/participants", strings.NewReader(`{"agent_id":""}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "agent_id is required") {
		t.Errorf("an empty agent_id: %d %s, want the service's own 400", w.Code, w.Body)
	}

	r, _ = bodyIDRouter(t)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"message":"  ","thread_id":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "message required") {
		t.Errorf("a blank message with a malformed thread: %d %s, want the enqueuer's own 400", w.Code, w.Body)
	}
}
