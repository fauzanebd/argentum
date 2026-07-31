package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/transport/http/apiv1"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
)

// `GET /v1/agents` and the `agent_id` it makes usable (T-S5).

// fakeRoster stands in for app.AgentService.
type fakeRoster struct {
	agents []*domain.Agent
	err    error
}

func (f *fakeRoster) List(context.Context, string) ([]*domain.Agent, error) {
	return f.agents, f.err
}

func rosterFixture(t *testing.T, roster V1RosterLister) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/v1")
	v1.Use(middleware.RequestID())
	v1.Use(func(c *gin.Context) {
		c.Set("company_id", testCompany)
		c.Set(middleware.CtxAPIKeyID, "key-1")
	})
	NewV1AgentsHandler(roster).Register(v1)
	return r
}

func listAgents(t *testing.T, r *gin.Engine) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/agents", nil))
	return w
}

func TestListAgentsAnswersInTheStandardPageEnvelope(t *testing.T) {
	r := rosterFixture(t, &fakeRoster{agents: []*domain.Agent{
		{ID: "ag-def", CompanyID: testCompany, Name: "General", Description: "The all-rounder", IsDefault: true, Enabled: true},
		{ID: "ag-fin", CompanyID: testCompany, Name: "Finance", Enabled: true},
	}})

	w := listAgents(t, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var page apiv1.Page[agentResponse]
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Data) != 2 {
		t.Fatalf("data = %+v, want two agents", page.Data)
	}
	if page.HasMore || page.NextCursor != "" {
		t.Errorf("page = %+v, want has_more false and no cursor — the whole roster arrives at once", page)
	}
	first := page.Data[0]
	if first.ID != "ag-def" || first.Object != "agent" || first.Name != "General" || !first.IsDefault {
		t.Errorf("first row = %+v, want the default, first, as an `agent` object", first)
	}
	// The agent with no description must not publish an empty string: a client
	// rendering it would show a blank line where the tenant wrote nothing.
	if strings.Count(w.Body.String(), `"description"`) != 1 {
		t.Errorf("body = %q, want `description` only on the agent that has one", w.Body.String())
	}
}

// The roster row is the tenant's own configuration: a persona in their words, a
// tool allowlist, and the ids of the databases each agent can reach. None of it
// belongs on a machine surface, and the way it gets there is somebody
// serializing domain.Agent because it was already in hand.
func TestListAgentsPublishesNoneOfTheTenantsConfiguration(t *testing.T) {
	r := rosterFixture(t, &fakeRoster{agents: []*domain.Agent{{
		ID: "ag-fin", CompanyID: testCompany, Name: "Finance",
		PersonaPrompt: "You speak in IDR and never guess at a number.",
		AllowedTools:  []string{"run_sql", "get_schema"},
		SourceIDs:     []string{"conn-ledger"},
		Enabled:       true,
		CreatedAt:     time.Now(),
	}}})

	body := listAgents(t, r).Body.String()

	for _, leaked := range []string{"persona", "You speak in IDR", "allowed_tools", "run_sql", "source_ids", "conn-ledger", "company_id"} {
		if strings.Contains(body, leaked) {
			t.Errorf("body = %q leaks %q", body, leaked)
		}
	}
}

// A disabled agent stays in the list. Naming it is a 404, and an integrator
// whose nightly job started failing needs to be able to see that the agent was
// switched off rather than watch an id disappear.
func TestADisabledAgentIsListedAndSaysSo(t *testing.T) {
	r := rosterFixture(t, &fakeRoster{agents: []*domain.Agent{
		{ID: "ag-def", Name: "General", IsDefault: true, Enabled: true},
		{ID: "ag-old", Name: "Retired", Enabled: false},
	}})

	var page apiv1.Page[agentResponse]
	if err := json.Unmarshal(listAgents(t, r).Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Data) != 2 {
		t.Fatalf("data = %+v, want the disabled agent listed too", page.Data)
	}
	if page.Data[1].Enabled {
		t.Errorf("row = %+v, want enabled false", page.Data[1])
	}
}

// An empty roster is `data: []`, never `data: null`. A client that iterates the
// response should not need a null check to read zero rows.
func TestAnEmptyRosterIsAnEmptyArray(t *testing.T) {
	body := listAgents(t, rosterFixture(t, &fakeRoster{})).Body.String()
	if !strings.Contains(body, `"data":[]`) {
		t.Errorf("body = %q, want an empty array", body)
	}
}

// A wiring with no roster answers the envelope with a reason, not a panic and
// not a 404 — an absent route reads to an integrator as a wrong path, which
// sends them to check their URL for something that is a deployment fact.
func TestTheRosterRouteDegradesRatherThanPanicking(t *testing.T) {
	w := listAgents(t, rosterFixture(t, nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "agents_unavailable") {
		t.Errorf("body = %q, want the typed reason", w.Body.String())
	}
}

func TestARosterReadFailureIsAnEnvelopeAndNotAPanic(t *testing.T) {
	w := listAgents(t, rosterFixture(t, &fakeRoster{err: errors.New("the database is down")}))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", w.Code, w.Body.String())
	}
	// The reason the read failed is ours, not the caller's. What they get is a
	// request id to quote.
	if strings.Contains(w.Body.String(), "the database is down") {
		t.Errorf("body = %q repeats an internal failure to a caller", w.Body.String())
	}
}

// --- the field the list makes usable -----------------------------------

func TestAgentIDReachesTheEnqueuer(t *testing.T) {
	f := newChatFixture(t, 5*time.Second)
	f.messages.persist(assistantMessage())

	w := f.send(t, sendRequest(t, "application/json",
		`{"message":"revenue?","user_ref":"u","agent_id":" ag-fin "}`), nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	// Trimmed on the way in. A copy-pasted id with a trailing space is a 404
	// that reads as "the agent does not exist", which sends an integrator to
	// look at the roster rather than at their own string.
	if f.enq.in.AgentID != "ag-fin" {
		t.Errorf("AgentID = %q, want the trimmed id", f.enq.in.AgentID)
	}
}

// Unknown, deleted, disabled and another tenant's are one answer: 404. A 403
// would confirm the row exists to somebody guessing uuids across tenants, which
// is the whole vulnerability — the status code is the oracle.
func TestAnAgentThisCompanyCannotUseIs404(t *testing.T) {
	f := newChatFixture(t, 5*time.Second)
	// Wrapped as the enqueue path wraps it, so errors.Is has to see through the
	// layer rather than the test asserting against a bare sentinel.
	f.enq.err = fmt.Errorf("resolve thread: %w", app.ErrAgentNotFound)

	w := f.send(t, sendRequest(t, "application/json",
		`{"message":"revenue?","user_ref":"u","agent_id":"ag-theirs"}`), nil)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "agent_not_found") {
		t.Errorf("body = %q, want agent_not_found", body)
	}
	// The parameter is `agent_id` and not `thread_id`: both refusals are 404s
	// wrapping domain.ErrNotFound, and a caller sent to the wrong field goes
	// looking for a bug that is not there.
	if !strings.Contains(body, `"param":"agent_id"`) {
		t.Errorf("body = %q, want param agent_id", body)
	}
	if strings.Contains(body, "thread_not_found") {
		t.Errorf("body = %q answers a bad agent as a bad thread", body)
	}
}

// The same refusal on the report door, with the same code and the same param.
// A report is an agent turn that ends in a document; an integrator should not
// have to learn the vocabulary twice.
func TestTheReportDoorRefusesABadAgentTheSameWay(t *testing.T) {
	f := newReportFixture(t)
	f.enq.err = fmt.Errorf("resolve thread: %w", app.ErrAgentNotFound)

	w := f.create(t, `{"prompt":"revenue","user_ref":"u","agent_id":"ag-theirs"}`)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "agent_not_found") ||
		!strings.Contains(w.Body.String(), `"param":"agent_id"`) {
		t.Errorf("body = %q, want agent_not_found on agent_id", w.Body.String())
	}
	// And the row it had already written is closed. A report whose turn never
	// started must not sit `queued` for a poller that will never see it move.
	if f.reports.completed != domain.APIReportFailed {
		t.Errorf("report status = %q, want failed", f.reports.completed)
	}
}

func TestTheReportDoorPassesTheAgentThrough(t *testing.T) {
	f := newReportFixture(t)

	if w := f.create(t, `{"prompt":"revenue","user_ref":"u","agent_id":"ag-fin"}`); w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", w.Code, w.Body.String())
	}
	if f.enq.in.AgentID != "ag-fin" {
		t.Errorf("AgentID = %q, want ag-fin", f.enq.in.AgentID)
	}
}

// A pick that disagrees with the conversation is a 400 naming `agent_id`, not a
// silently dropped field. A client that believed it had switched agents while
// every answer still came from the old one has no way to notice.
func TestNamingADifferentAgentOnAThreadIsRefused(t *testing.T) {
	f := newChatFixture(t, 5*time.Second)
	f.enq.err = fmt.Errorf("resolve thread: %w", app.ErrAgentChange)

	w := f.send(t, sendRequest(t, "application/json",
		`{"message":"and December?","thread_id":"th-1","agent_id":"ag-fin"}`), nil)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "agent_mismatch") || !strings.Contains(body, `"param":"agent_id"`) {
		t.Errorf("body = %q, want agent_mismatch on agent_id", body)
	}
	if strings.Contains(body, "invalid_thread") {
		t.Errorf("body = %q sends the caller to fix the wrong field", body)
	}
}

// T-A1's idempotency records ids, not payloads — so "the same request" is the
// bytes. A retry that changed the agent is a different request under a reused
// key, which is a bug in the caller's retry loop and has to be told so rather
// than answered with the first agent's turn.
func TestTheSameKeyWithADifferentAgentIs409(t *testing.T) {
	f := newChatFixture(t, 5*time.Second)
	f.messages.persist(assistantMessage())

	first := sendRequest(t, "application/json", `{"message":"revenue?","user_ref":"u","agent_id":"ag-fin"}`)
	if w := f.send(t, first, nil); w.Code != http.StatusOK {
		t.Fatalf("first status = %d: %s", w.Code, w.Body.String())
	}
	f.enq.in = app.ChatInput{}

	// Same Idempotency-Key — sendRequest always sets `k-1` — and one field
	// changed.
	w := f.send(t, sendRequest(t, "application/json", `{"message":"revenue?","user_ref":"u","agent_id":"ag-ops"}`), nil)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", w.Code, w.Body.String())
	}
	if f.enq.in.Message != "" {
		t.Errorf("a second turn was enqueued under a reused key: %+v", f.enq.in)
	}
}
