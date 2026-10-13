package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// RIZ-89 R7–R9 at the handler: the operator's queue, approve once, 409 the
// second time, 404 for nothing, and the shapes the page reads.

const (
	pendingID = "00000000-0000-4000-8000-000000000001"
	missingID = "00000000-0000-4000-8000-999999999999"
)

// handlerGrants holds one request and decides it the way the repository's
// guard does: once.
type handlerGrants struct {
	mu  sync.Mutex
	req *domain.CreditGrantRequest
}

func (m *handlerGrants) InsertIfAbsent(context.Context, *domain.CreditGrantRequest) (bool, error) {
	return false, errors.New("not under test")
}
func (m *handlerGrants) GetForCompany(context.Context, string) (*domain.CreditGrantRequest, error) {
	return nil, domain.ErrNotFound
}

func (m *handlerGrants) row(status domain.CreditGrantStatus) *domain.OperatorFreeCredit {
	return &domain.OperatorFreeCredit{
		CreditGrantRequest: *m.req, CompanyName: "Toko Segar", RequestedByEmail: "dina@tokosegar.id",
		HasDataSource: true, SameIPSignups7d: 2,
	}
}

func (m *handlerGrants) ListPending(context.Context) ([]*domain.OperatorFreeCredit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.req.Status != domain.CreditGrantStatusPending {
		return []*domain.OperatorFreeCredit{}, nil
	}
	return []*domain.OperatorFreeCredit{m.row(m.req.Status)}, nil
}

func (m *handlerGrants) ListReviewed(context.Context, int) ([]*domain.OperatorFreeCredit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.req.Status == domain.CreditGrantStatusPending {
		return []*domain.OperatorFreeCredit{}, nil
	}
	return []*domain.OperatorFreeCredit{m.row(m.req.Status)}, nil
}

func (m *handlerGrants) decide(id string, to domain.CreditGrantStatus) (*domain.CreditGrantRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id != m.req.ID {
		return nil, domain.ErrNotFound
	}
	if m.req.Status != domain.CreditGrantStatusPending {
		return nil, domain.ErrCreditGrantAlreadyReviewed
	}
	now := time.Now()
	m.req.Status, m.req.ReviewedAt = to, &now
	cp := *m.req
	return &cp, nil
}

func (m *handlerGrants) Approve(_ context.Context, id, _ string) (*domain.CreditGrantApproval, error) {
	g, err := m.decide(id, domain.CreditGrantStatusApproved)
	if err != nil {
		return nil, err
	}
	return &domain.CreditGrantApproval{Request: g, AddedMicroUSD: g.AmountMicroUSD, BalanceMicroUSD: g.AmountMicroUSD}, nil
}

func (m *handlerGrants) Reject(_ context.Context, id, _ string) (*domain.CreditGrantRequest, error) {
	return m.decide(id, domain.CreditGrantStatusRejected)
}

func newFreeCreditRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	repo := &handlerGrants{req: &domain.CreditGrantRequest{
		ID: pendingID, CompanyID: "co-1", Status: domain.CreditGrantStatusPending, AmountMicroUSD: 500_000,
		CreatedAt: time.Now(), SignupIP: "203.0.113.7", RequestedBy: "user-9",
	}}
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user_id", "user-op") })
	NewFreeCreditHandler(app.NewFreeCreditService(repo), func(c *gin.Context) { c.Next() }).Register(r.Group("/api"))
	return r
}

func serveFreeCredit(r http.Handler, method, path string) (int, map[string]any) {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestFreeCreditOperatorRoutes(t *testing.T) {
	r := newFreeCreditRouter(t)

	code, out := serveFreeCredit(r, http.MethodGet, "/api/operator/free-credit?view=review")
	rows, _ := out["requests"].([]any)
	if code != http.StatusOK || len(rows) != 1 {
		t.Fatalf("queue: %d %v", code, out)
	}
	row := rows[0].(map[string]any)
	if row["company_name"] != "Toko Segar" || row["requested_by_email"] != "dina@tokosegar.id" || row["status"] != "pending" ||
		row["amount_micro_usd"] != float64(500_000) || row["has_data_source"] != true || row["same_ip_signups_7d"] != float64(2) {
		t.Errorf("queue row: %v", row)
	}
	// The address is personal data and the signal is the count.
	for _, key := range []string{"signup_ip", "SignupIP", "requested_by", "reviewed_by"} {
		if _, leaked := row[key]; leaked {
			t.Errorf("queue row carries %q: %v", key, row)
		}
	}
	if code, _ := serveFreeCredit(r, http.MethodGet, "/api/operator/free-credit?view=everything"); code != http.StatusBadRequest {
		t.Errorf("unknown view: %d", code)
	}

	code, out = serveFreeCredit(r, http.MethodPost, "/api/operator/free-credit/"+pendingID+"/approve")
	if code != http.StatusOK || out["added_micro_usd"] != float64(500_000) || out["balance_micro_usd"] != float64(500_000) {
		t.Fatalf("approve: %d %v", code, out)
	}
	if req, _ := out["request"].(map[string]any); req["status"] != "approved" {
		t.Errorf("approved request: %v", out["request"])
	}

	// AC6: the second approve, and a reject after it, are the 409 sentence.
	for _, path := range []string{"/approve", "/reject"} {
		code, out = serveFreeCredit(r, http.MethodPost, "/api/operator/free-credit/"+pendingID+path)
		if code != http.StatusConflict || out["error"] != "This request was already reviewed." {
			t.Errorf("%s after approve: %d %v", path, code, out)
		}
	}
	for _, path := range []string{"/api/operator/free-credit/" + missingID + "/approve", "/api/operator/free-credit/not-a-uuid/reject"} {
		if code, out := serveFreeCredit(r, http.MethodPost, path); code != http.StatusNotFound || out["error"] != "no such request" {
			t.Errorf("%s: %d %v", path, code, out)
		}
	}

	code, out = serveFreeCredit(r, http.MethodGet, "/api/operator/free-credit?view=reviewed")
	if rows, _ := out["requests"].([]any); code != http.StatusOK || len(rows) != 1 {
		t.Errorf("reviewed: %d %v", code, out)
	}
	code, out = serveFreeCredit(r, http.MethodGet, "/api/operator/free-credit")
	if rows, _ := out["requests"].([]any); code != http.StatusOK || rows == nil || len(rows) != 0 {
		t.Errorf("an empty queue is [], not null: %d %v", code, out)
	}
}

func TestFreeCreditReject(t *testing.T) {
	r := newFreeCreditRouter(t)
	code, out := serveFreeCredit(r, http.MethodPost, "/api/operator/free-credit/"+pendingID+"/reject")
	if req, _ := out["request"].(map[string]any); code != http.StatusOK || req["status"] != "rejected" {
		t.Fatalf("reject: %d %v", code, out)
	}
	if code, out := serveFreeCredit(r, http.MethodPost, "/api/operator/free-credit/"+pendingID+"/approve"); code != http.StatusConflict {
		t.Errorf("approve after reject: %d %v", code, out)
	}
}

// Without the gate every route is refused; without the service, past the gate,
// it is a 503 rather than a 404.
func TestFreeCreditRoutesWithoutAServiceOrAGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	closed := gin.New()
	NewFreeCreditHandler(nil, nil).Register(closed.Group("/api"))
	open := gin.New()
	NewFreeCreditHandler(nil, func(c *gin.Context) { c.Next() }).Register(open.Group("/api"))
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/operator/free-credit"},
		{http.MethodPost, "/api/operator/free-credit/" + pendingID + "/approve"},
		{http.MethodPost, "/api/operator/free-credit/" + pendingID + "/reject"},
	} {
		if code, out := serveFreeCredit(closed, c.method, c.path); code != http.StatusForbidden || out["error"] != "operators only" {
			t.Errorf("%s %s with no gate: %d %v", c.method, c.path, code, out)
		}
		if code, _ := serveFreeCredit(open, c.method, c.path); code != http.StatusServiceUnavailable {
			t.Errorf("%s %s with no service: %d", c.method, c.path, code)
		}
	}
}

// R12 at the dashboard's door: the 402 carries the refusing state's sentence,
// and a bare sentinel still gets today's.
func TestDashboardRefusalsSayWhy(t *testing.T) {
	pending := &app.BudgetRefusal{State: app.BudgetState{Verdict: app.BudgetExhausted, FreeCredit: app.FreeCreditStatePending}}
	for _, tc := range []struct {
		err  error
		want string
	}{
		{pending, app.FreeCreditPendingMessage},
		{domain.ErrInsufficientCredits, app.CreditsExhaustedMessage},
	} {
		gin.SetMode(gin.TestMode)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		voiceFail(c, tc.err)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if w.Code != http.StatusPaymentRequired || out["error"] != tc.want {
			t.Errorf("voiceFail(%v) = %d %v, want 402 %q", tc.err, w.Code, out, tc.want)
		}

		// `/v1` and the widget never say it (AC12).
		w = httptest.NewRecorder()
		c, _ = gin.CreateTestContext(w)
		embedChatFail(c, tc.err)
		out = nil
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if w.Code != http.StatusPaymentRequired || out["error"] != app.CreditsExhaustedMessage {
			t.Errorf("embedChatFail(%v) = %d %v, want today's sentence", tc.err, w.Code, out)
		}
	}
}
