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
func (s *embedMessagesStub) LatestAssistantSince(context.Context, string, time.Time, domain.AnswerScope) (*domain.Message, error) {
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

// A visitor of a tenant's website reads their own conversation. They do not
// read the *work* behind it.
//
// `ListByThread` returns every row of a thread with every column: the tool-role
// rows T-Q6 writes as the agent's memory, and the `tool_calls`, `metadata` and
// token counts on the assistant row. A tool digest carries the truncated SQL,
// the `source_id` and the table names the turn touched (app.BuildToolDigest),
// so handing the raw slice to a browser on a page we do not control publishes
// the tenant's warehouse vocabulary to anyone who opens a network tab.
//
// This is T-D13's finding in a second costume — there a public share link
// served the panel SQL, here a widget transcript serves the query log — and the
// same answer applies: the route projects, and what it projects is what the
// widget's generated type describes.
func TestEmbedTranscriptCarriesNoToolWork(t *testing.T) {
	const sql = "SELECT sum(f.sales_amount) FROM fact_sales f JOIN dim_date d ON f.date_id = d.date_id"
	thread := &domain.ConversationThread{
		ID: "th-mine", CompanyID: "co-1", Channel: domain.ChannelWidget, EmbedUserRef: "emp_812",
	}
	msgs := &embedMessagesStub{msgs: []*domain.Message{
		{ID: "m-1", ThreadID: "th-mine", Role: domain.MessageRoleUser, Content: "revenue last month?"},
		{
			ID: "m-2", ThreadID: "th-mine", Role: domain.MessageRoleTool,
			Content:  `[{"tool":"run_sql","query":"` + sql + `","source_id":"src-9f1","rows":1}]`,
			Metadata: map[string]interface{}{"kind": "tool_digest", "tool_calls": 1},
		},
		{
			ID: "m-3", ThreadID: "th-mine", Role: domain.MessageRoleAssistant,
			Content:   "Revenue last month was Rp3.8B.",
			ToolCalls: map[string]interface{}{"run_sql": map[string]interface{}{"sql": sql}},
			Metadata:  map[string]interface{}{"source_id": "src-9f1"},
			TokensIn:  4211, TokensOut: 87,
		},
	}}
	threads := &embedThreadsStub{
		byID:   map[string]*domain.ConversationThread{"th-mine": thread},
		latest: thread,
	}
	r := embedRouter(NewEmbedChatHandler(nil, threads, msgs, nil), "co-1", "emp_812")

	// Both read routes, because they assemble the transcript separately and a
	// projection applied to one of them is the defect this test is about.
	for _, path := range []string{"/api/embed/threads/current", "/api/embed/threads/th-mine/messages"} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
			}
			body := w.Body.String()
			for _, forbidden := range []string{
				"SELECT sum", "fact_sales", "dim_date", // the warehouse's vocabulary
				"src-9f1",     // which source answered
				"tool_digest", // that a memory row exists at all
				`"tool_calls"`, `"metadata"`, `"tokens_in"`, `"tokens_out"`,
			} {
				if strings.Contains(body, forbidden) {
					t.Errorf("transcript carries %q, which a visitor of a tenant's website has no business reading:\n%s",
						forbidden, body)
				}
			}
			// And the assistant's answer, which is the whole point, is still there.
			if !strings.Contains(body, "Revenue last month was") {
				t.Errorf("the answer itself is missing from %s", body)
			}
		})
	}
}

// The shape of one transcript row, pinned key by key.
//
// `embedwire.Message` is a projection of `domain.Message`, and the failure mode
// of a projection is that somebody adds a field to the source and it arrives
// here for free. That is exactly how `tool_calls` and the token counts reached
// a visitor's browser in the first place, so the assertion is the *whole* key
// set rather than a list of the ones we currently mind.
func TestEmbedTranscriptRowCarriesFourFields(t *testing.T) {
	thread := &domain.ConversationThread{
		ID: "th-mine", CompanyID: "co-1", Channel: domain.ChannelWidget, EmbedUserRef: "emp_812",
	}
	msgs := &embedMessagesStub{msgs: []*domain.Message{
		{ID: "m-1", ThreadID: "th-mine", Role: domain.MessageRoleUser, Content: "revenue?", TokensIn: 12},
	}}
	r := embedRouter(NewEmbedChatHandler(nil,
		&embedThreadsStub{byID: map[string]*domain.ConversationThread{"th-mine": thread}},
		msgs, nil), "co-1", "emp_812")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/embed/threads/th-mine/messages", nil))

	var body struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, w.Body.String())
	}
	if len(body.Messages) != 1 {
		t.Fatalf("got %d messages, want 1 (%s)", len(body.Messages), w.Body.String())
	}
	want := map[string]bool{"id": true, "role": true, "content": true, "created_at": true}
	for k := range body.Messages[0] {
		if !want[k] {
			t.Errorf("transcript row carries %q — a field reached the widget without anybody deciding it should", k)
		}
		delete(want, k)
	}
	for k := range want {
		t.Errorf("transcript row is missing %q", k)
	}
}

// An empty transcript is `[]`, never `null`.
//
// The same rule `suggested_prompts` carries (T-23 §4a) and the same reason: a
// client reading `.length` off a missing or null key gets a TypeError instead
// of zero. It is asserted here because the transcript is now a projection, and
// a projection of nothing is the case a `make([]T, 0)` gets forgotten in.
func TestEmbedEmptyTranscriptIsAnArray(t *testing.T) {
	thread := &domain.ConversationThread{
		ID: "th-mine", CompanyID: "co-1", Channel: domain.ChannelWidget, EmbedUserRef: "emp_812",
	}
	// Nothing but the agent's own memory: every row is filtered out, which is
	// the case a nil slice would survive.
	msgs := &embedMessagesStub{msgs: []*domain.Message{
		{ID: "m-1", Role: domain.MessageRoleTool, Content: `[{"tool":"run_sql"}]`},
	}}
	r := embedRouter(NewEmbedChatHandler(nil,
		&embedThreadsStub{byID: map[string]*domain.ConversationThread{"th-mine": thread}},
		msgs, nil), "co-1", "emp_812")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/embed/threads/th-mine/messages", nil))
	if got := w.Body.String(); got != `{"messages":[]}` {
		t.Errorf("body = %s, want an empty array", got)
	}
}

// `GET /config` answers an envelope, and `agents` is a sibling of `config`.
//
// This is the shape the widget's generated type describes, and it is the one
// the widget read wrongly for a month: `greeting` and `suggested_prompts` are
// one level down, and a client that reads them off the top gets `undefined` for
// both without failing anywhere. Pinned here because the defect was invisible
// on both sides — the server was right, the client was wrong, and nothing
// compared them.
func TestEmbedConfigIsAnEnvelopeWithAgentsBesideIt(t *testing.T) {
	h := NewEmbedChatHandler(nil, &embedThreadsStub{}, &embedMessagesStub{}, &embedRosterStub{
		agents: []*domain.Agent{
			{ID: "ag-1", Name: "Support", Enabled: true, IsDefault: true},
			{ID: "ag-2", Name: "Draft", Enabled: false},
		},
	})
	r := embedRouter(h.WithConfig(&embedConfigStub{cfg: &domain.WidgetConfig{
		Greeting:         "Tanya soal stok",
		SuggestedPrompts: []string{"Stok hari ini"},
	}}), "co-1", "emp_812")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/embed/config", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", w.Code, w.Body.String())
	}

	var body struct {
		Config struct {
			Greeting         string   `json:"greeting"`
			SuggestedPrompts []string `json:"suggested_prompts"`
		} `json:"config"`
		Agents []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			IsDefault bool   `json:"is_default"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, w.Body.String())
	}
	if body.Config.Greeting != "Tanya soal stok" {
		t.Errorf("greeting = %q, want the tenant's own — it is one level down, under `config`", body.Config.Greeting)
	}
	if len(body.Config.SuggestedPrompts) != 1 {
		t.Errorf("suggested_prompts = %v, want the tenant's one", body.Config.SuggestedPrompts)
	}
	// A disabled agent is not offered, and the roster is a sibling of the
	// config rather than a field of it.
	if len(body.Agents) != 1 || body.Agents[0].ID != "ag-1" || !body.Agents[0].IsDefault {
		t.Errorf("agents = %+v, want only the enabled one, beside `config`", body.Agents)
	}
}

// embedConfigStub answers one tenant's stored widget configuration.
type embedConfigStub struct{ cfg *domain.WidgetConfig }

func (s *embedConfigStub) GetWidgetConfig(context.Context, string) (*domain.WidgetConfig, error) {
	return s.cfg, nil
}
func (s *embedConfigStub) SaveWidgetConfig(context.Context, string, *domain.WidgetConfig) error {
	panic("unexpected SaveWidgetConfig — a read route must not write")
}

// embedRosterStub is the roster the picker is built from.
type embedRosterStub struct{ agents []*domain.Agent }

func (s *embedRosterStub) List(context.Context, string) ([]*domain.Agent, error) {
	return s.agents, nil
}
