package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
)

// The widget's read surface (T-20). Everything here is about one property: a
// visitor of a tenant's site reads their own conversation and nothing else.
// The Gelael pilot had to build this check by hand against `/v1` and would
// have served a colleague's transcript without it, which is why it is pinned
// here rather than left to the enqueuer.

// embedThreadsStub is a ThreadRepository with only the two reads this surface
// makes. Every other method panics, so a route that starts touching the rest of
// the repository fails loudly instead of quietly widening what a browser can
// reach.
type embedThreadsStub struct {
	byID   map[string]*domain.ConversationThread
	latest *domain.ConversationThread
	err    error
}

func (s *embedThreadsStub) GetForCompany(_ context.Context, companyID, id string) (*domain.ConversationThread, error) {
	t, ok := s.byID[id]
	if !ok || t.CompanyID != companyID {
		return nil, domain.ErrNotFound
	}
	return t, nil
}

func (s *embedThreadsStub) LatestForEmbedUser(_ context.Context, companyID, ref string) (*domain.ConversationThread, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.latest == nil || s.latest.CompanyID != companyID || s.latest.EmbedUserRef != ref {
		return nil, domain.ErrNotFound
	}
	return s.latest, nil
}

func (s *embedThreadsStub) Create(context.Context, *domain.ConversationThread) error {
	panic("unexpected Create — the widget opens a thread by sending, not by reading")
}
func (s *embedThreadsStub) GetByID(context.Context, string) (*domain.ConversationThread, error) {
	panic("unexpected GetByID — the widget must scope every lookup by company")
}
func (s *embedThreadsStub) ListPage(context.Context, string, domain.ThreadFilter) ([]*domain.ConversationThread, bool, error) {
	panic("unexpected ListPage")
}
func (s *embedThreadsStub) LatestForPhone(context.Context, string, string) (*domain.ConversationThread, error) {
	panic("unexpected LatestForPhone")
}
func (s *embedThreadsStub) LatestForUser(context.Context, string, string) (*domain.ConversationThread, error) {
	panic("unexpected LatestForUser")
}
func (s *embedThreadsStub) LatestForDiscordUser(context.Context, string, string) (*domain.ConversationThread, error) {
	panic("unexpected LatestForDiscordUser")
}
func (s *embedThreadsStub) LatestForLark(context.Context, string, string) (*domain.ConversationThread, error) {
	panic("unexpected LatestForLark")
}
func (s *embedThreadsStub) LatestForSlackThread(context.Context, string, string, string) (*domain.ConversationThread, error) {
	panic("unexpected LatestForSlackThread")
}
func (s *embedThreadsStub) LatestForSlackUser(context.Context, string, string, string) (*domain.ConversationThread, error) {
	panic("unexpected LatestForSlackUser")
}
func (s *embedThreadsStub) LatestForAPIUser(context.Context, string, string) (*domain.ConversationThread, error) {
	panic("unexpected LatestForAPIUser")
}
func (s *embedThreadsStub) ListByCompany(context.Context, string, int, int) ([]*domain.ConversationThread, error) {
	panic("unexpected ListByCompany")
}
func (s *embedThreadsStub) UpdateSummary(context.Context, string, string, string) error {
	panic("unexpected UpdateSummary")
}
func (s *embedThreadsStub) Touch(context.Context, string, time.Time) error {
	panic("unexpected Touch")
}
func (s *embedThreadsStub) Archive(context.Context, string) error { panic("unexpected Archive") }
func (s *embedThreadsStub) Delete(context.Context, string) error  { panic("unexpected Delete") }

// embedMessagesStub answers one transcript.
type embedMessagesStub struct{ msgs []*domain.Message }

func (s *embedMessagesStub) ListByThread(context.Context, string, int, int) ([]*domain.Message, error) {
	return s.msgs, nil
}
func (s *embedMessagesStub) Append(context.Context, *domain.Message) error {
	panic("unexpected Append — a read route must not write")
}
func (s *embedMessagesStub) LatestByThread(context.Context, string) (*domain.Message, error) {
	panic("unexpected LatestByThread")
}
func (s *embedMessagesStub) ListPageByThread(context.Context, string, domain.MessageFilter) ([]*domain.Message, bool, error) {
	panic("unexpected ListPageByThread")
}
func (s *embedMessagesStub) LatestAssistantSince(context.Context, string, time.Time) (*domain.Message, error) {
	panic("unexpected LatestAssistantSince")
}
func (s *embedMessagesStub) DeleteByThread(context.Context, string) error {
	panic("unexpected DeleteByThread")
}
func (s *embedMessagesStub) CountByThread(context.Context, string) (int, error) {
	panic("unexpected CountByThread")
}

// embedRouter mounts the handler behind a fake session, so the tests exercise
// the same context keys middleware.EmbedAuth sets.
func embedRouter(h *EmbedChatHandler, companyID, ref string) *gin.Engine {
	r := gin.New()
	g := r.Group("/api/embed", func(c *gin.Context) {
		c.Set("company_id", companyID)
		c.Set(middleware.CtxEmbedUserRef, ref)
		c.Set(middleware.CtxEmbedKeyID, "ek-1")
		c.Next()
	})
	h.Register(g)
	return r
}

func TestEmbedTranscriptIsScopedToTheSessionsVisitor(t *testing.T) {
	mine := &domain.ConversationThread{
		ID: "th-mine", CompanyID: "co-1", Channel: domain.ChannelWidget, EmbedUserRef: "emp_812",
	}
	colleague := &domain.ConversationThread{
		ID: "th-theirs", CompanyID: "co-1", Channel: domain.ChannelWidget, EmbedUserRef: "emp_999",
	}
	// A staff conversation in the same workspace. Same company, same id space,
	// and a widget must not be able to read one.
	staff := &domain.ConversationThread{
		ID: "th-staff", CompanyID: "co-1", Channel: domain.ChannelDashboard, UserID: "user-1",
	}
	// And another tenant's.
	foreign := &domain.ConversationThread{
		ID: "th-other", CompanyID: "co-2", Channel: domain.ChannelWidget, EmbedUserRef: "emp_812",
	}

	threads := &embedThreadsStub{byID: map[string]*domain.ConversationThread{
		"th-mine": mine, "th-theirs": colleague, "th-staff": staff, "th-other": foreign,
	}}
	msgs := &embedMessagesStub{msgs: []*domain.Message{
		{ID: "m-1", Role: domain.MessageRoleUser, Content: "revenue?"},
	}}
	r := embedRouter(NewEmbedChatHandler(nil, threads, msgs, nil), "co-1", "emp_812")

	cases := []struct {
		name, id string
		want     int
	}{
		{"my own conversation", "th-mine", http.StatusOK},
		{"a colleague's, by id", "th-theirs", http.StatusNotFound},
		{"a staff conversation", "th-staff", http.StatusNotFound},
		{"another tenant's", "th-other", http.StatusNotFound},
		{"one that does not exist", "th-nope", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/embed/threads/"+tc.id+"/messages", nil))
			if w.Code != tc.want {
				t.Errorf("status = %d, want %d (body %s)", w.Code, tc.want, w.Body.String())
			}
			// Every refusal is the same 404 with the same body: a visitor must
			// not be able to tell "no such thread" from "not yours", or the
			// route enumerates the workspace.
			if tc.want == http.StatusNotFound && w.Body.String() != `{"error":"no such conversation"}` {
				t.Errorf("body = %s, want the one indistinguishable refusal", w.Body.String())
			}
		})
	}
}

func TestEmbedCurrentThread(t *testing.T) {
	t.Run("a visitor who has never typed gets an empty state, not an error", func(t *testing.T) {
		r := embedRouter(NewEmbedChatHandler(nil, &embedThreadsStub{}, &embedMessagesStub{}, nil),
			"co-1", "emp_812")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/embed/threads/current", nil))

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 — an empty state is not a failure", w.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["thread"] != nil {
			t.Errorf("thread = %v, want null", body["thread"])
		}
	})

	t.Run("a returning visitor gets their conversation and its transcript", func(t *testing.T) {
		threads := &embedThreadsStub{latest: &domain.ConversationThread{
			ID: "th-mine", CompanyID: "co-1", Channel: domain.ChannelWidget,
			EmbedUserRef: "emp_812", Title: "Revenue",
		}}
		msgs := &embedMessagesStub{msgs: []*domain.Message{
			{ID: "m-1", Role: domain.MessageRoleUser, Content: "revenue?"},
			{ID: "m-2", Role: domain.MessageRoleAssistant, Content: "IDR 1.2bn."},
		}}
		r := embedRouter(NewEmbedChatHandler(nil, threads, msgs, nil), "co-1", "emp_812")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/embed/threads/current", nil))

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		var body struct {
			Thread struct {
				ID       string `json:"id"`
				Messages []struct {
					Content string `json:"content"`
				} `json:"messages"`
			} `json:"thread"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.Thread.ID != "th-mine" {
			t.Errorf("thread id = %q", body.Thread.ID)
		}
		if len(body.Thread.Messages) != 2 {
			t.Errorf("got %d messages, want the transcript", len(body.Thread.Messages))
		}
	})

	t.Run("another visitor's ref resolves nothing", func(t *testing.T) {
		threads := &embedThreadsStub{latest: &domain.ConversationThread{
			ID: "th-mine", CompanyID: "co-1", Channel: domain.ChannelWidget, EmbedUserRef: "emp_812",
		}}
		r := embedRouter(NewEmbedChatHandler(nil, threads, &embedMessagesStub{}, nil), "co-1", "emp_999")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/embed/threads/current", nil))

		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body["thread"] != nil {
			t.Errorf("thread = %v, want null — that conversation is somebody else's", body["thread"])
		}
	})
}

// The response the widget renders itself from carries no tenant data: a page
// source is a public place, and a visitor reading the network tab must not
// learn the workspace's credit position or an agent's tool allowlist.
//
// The forbidden-key search walks nested objects, because T-23 moved the
// settings under a `config` key and a check that only read the top level would
// have stopped looking exactly where the new fields went.
func TestEmbedConfigLeaksNothing(t *testing.T) {
	r := embedRouter(NewEmbedChatHandler(nil, &embedThreadsStub{}, &embedMessagesStub{}, nil),
		"co-1", "emp_812")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/embed/config", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	forbidden := []string{
		"credits", "balance_usd", "connections", "tools", "system_prompt",
		"dsn", "persona", "sources", "allowed_origins", "secret",
	}
	var walk func(prefix string, node map[string]any)
	walk = func(prefix string, node map[string]any) {
		for k, v := range node {
			for _, f := range forbidden {
				if k == f {
					t.Errorf("config carries %q%s, which a visitor of a tenant's website has no business reading", k, prefix)
				}
			}
			if child, ok := v.(map[string]any); ok {
				walk(prefix+" (under "+k+")", child)
			}
		}
	}
	walk("", body)

	// A deployment with no config store still answers something renderable:
	// the defaults are applied on read, so an unconfigured tenant and a tenant
	// who chose our defaults get the same widget.
	cfg, ok := body["config"].(map[string]any)
	if !ok {
		t.Fatalf("no config object in %s", w.Body.String())
	}
	if cfg["greeting"] != domain.DefaultWidgetGreeting {
		t.Errorf("greeting = %v, want the default so the empty state renders", cfg["greeting"])
	}
	if _, ok := cfg["suggested_prompts"]; !ok {
		t.Error("no suggested_prompts key — the widget would render undefined")
	}
}

// A deployment with no queue answers a typed 503 rather than panicking on a nil
// enqueuer — the same degradation every other unconfigured surface makes.
func TestEmbedSendWithoutAQueueIsUnavailable(t *testing.T) {
	r := embedRouter(NewEmbedChatHandler(nil, &embedThreadsStub{}, &embedMessagesStub{}, nil),
		"co-1", "emp_812")
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/embed/chat", strings.NewReader(`{"message":"revenue?"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}
