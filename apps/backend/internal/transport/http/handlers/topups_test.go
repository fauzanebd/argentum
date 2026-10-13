package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// RIZ-83: the top-up routes' status codes and the exact sentences the owner
// approved. The service's own rules are internal/app's tests; this is the
// mapping, and what crosses the wire.

// handlerTopups is the smallest TopupRepository that keeps the open-request
// and status rules.
type handlerTopups struct {
	mu   sync.Mutex
	rows map[string]*domain.Topup
	seq  int
}

func (m *handlerTopups) Create(_ context.Context, t *domain.Topup) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.rows {
		if r.Status.Open() && r.CompanyID == t.CompanyID {
			return domain.ErrTopupInProgress
		}
		if r.Status.Open() && r.ExpectedAmountIDR == t.ExpectedAmountIDR {
			return domain.ErrTopupCodeTaken
		}
	}
	m.seq++
	t.ID = fmt.Sprintf("00000000-0000-4000-8000-%012d", m.seq)
	t.CreatedAt, t.Status = time.Now(), domain.TopupStatusAwaitingPayment
	c := *t
	m.rows[t.ID] = &c
	return nil
}

func (m *handlerTopups) get(companyID, id string) (*domain.Topup, error) {
	r, ok := m.rows[id]
	if !ok || (companyID != "" && r.CompanyID != companyID) {
		return nil, domain.ErrNotFound
	}
	return r, nil
}

func (m *handlerTopups) GetForCompany(_ context.Context, companyID, id string) (*domain.Topup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.get(companyID, id)
	if err != nil {
		return nil, err
	}
	c := *r
	return &c, nil
}

func (m *handlerTopups) ListForCompany(_ context.Context, companyID string, _ int) ([]*domain.Topup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []*domain.Topup{}
	for _, r := range m.rows {
		if r.CompanyID == companyID {
			c := *r
			out = append(out, &c)
		}
	}
	return out, nil
}

func (m *handlerTopups) AttachProof(_ context.Context, companyID, id string, p domain.TopupProof) (*domain.Topup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.get(companyID, id)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	payee := p.PayeeID
	r.Status, r.ProofKey, r.ProofContentType, r.ProofSHA256, r.PayeeID, r.ProofUploadedAt =
		domain.TopupStatusInReview, p.Key, p.ContentType, p.SHA256, &payee, &now
	c := *r
	return &c, nil
}

func (m *handlerTopups) Cancel(_ context.Context, companyID, id string) (*domain.Topup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.get(companyID, id)
	if err != nil {
		return nil, err
	}
	if r.Status != domain.TopupStatusAwaitingPayment {
		return nil, domain.ErrTopupNotCancellable
	}
	r.Status = domain.TopupStatusCancelled
	c := *r
	return &c, nil
}

func (m *handlerTopups) ExpireStale(context.Context) (int64, error) { return 0, nil }

func (m *handlerTopups) ListForReview(context.Context) ([]*domain.OperatorTopup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []*domain.OperatorTopup{}
	for _, r := range m.rows {
		if r.Status == domain.TopupStatusInReview {
			out = append(out, &domain.OperatorTopup{Topup: *r, CompanyName: "Toko Segar", FXSource: r.FX.Source,
				FXMarketIDRPerUSDE4: r.FX.MarketIDRPerUSDE4, FXSpreadBP: r.FX.SpreadBP, FXEffectiveIDRPerUSDE4: r.FX.EffectiveIDRPerUSDE4})
		}
	}
	return out, nil
}

func (m *handlerTopups) ListReviewed(context.Context, int) ([]*domain.OperatorTopup, error) {
	return []*domain.OperatorTopup{}, nil
}

func (m *handlerTopups) GetAnyCompany(ctx context.Context, id string) (*domain.Topup, error) {
	return m.GetForCompany(ctx, "", id)
}

func (m *handlerTopups) Approve(_ context.Context, id, reviewer string) (*domain.Topup, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.get("", id)
	if err != nil {
		return nil, 0, err
	}
	if r.Status != domain.TopupStatusInReview {
		return nil, 0, domain.ErrTopupAlreadyReviewed
	}
	r.Status, r.ReviewedBy = domain.TopupStatusApproved, reviewer
	c := *r
	return &c, 120_000 + r.CreditMicroUSD, nil
}

func (m *handlerTopups) Reject(_ context.Context, id, reviewer, reason string) (*domain.Topup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.get("", id)
	if err != nil {
		return nil, err
	}
	if r.Status != domain.TopupStatusInReview {
		return nil, domain.ErrTopupAlreadyReviewed
	}
	r.Status, r.RejectReason = domain.TopupStatusRejected, &reason
	c := *r
	return &c, nil
}

type fixedFX struct{}

func (fixedFX) FetchUSDIDR(context.Context) (app.FXQuote, error) {
	return app.FXQuote{MarketIDRPerUSDE4: 160_000_000, RateDate: "2026-10-12"}, nil
}

type handlerStore struct{ keys []string }

func (s *handlerStore) UploadKey(_ context.Context, key string, r io.Reader, _ string) (string, error) {
	_, _ = io.Copy(io.Discard, r)
	s.keys = append(s.keys, key)
	return "https://storage.example/" + key, nil
}
func (s *handlerStore) RemoveKey(context.Context, string) error { return nil }
func (s *handlerStore) PresignKey(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://storage.example/" + key + "?sig=1", nil
}

type topupHarness struct {
	r     *gin.Engine
	store *handlerStore
}

// Placeholder payees: the real ones are deploy config, never in this repo.
func newTopupHarness(t *testing.T, payees bool, storage bool) *topupHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	settings := app.TopupSettings{MinIDR: 20_000, MaxIDR: 5_000_000, PresetsIDR: []int64{50_000, 100_000, 250_000}}
	if payees {
		settings.Payees = []domain.TopupPayee{{ID: "bri", Label: "BRI", AccountNumber: "0000000000", AccountName: "Payee Name"}}
	}
	rates := app.NewTopupRates(fixedFX{}, nil, app.TopupRatePolicy{SpreadBP: 500, FallbackIDRPerUSD: 18_000})
	svc := app.NewTopupService(&handlerTopups{rows: map[string]*domain.Topup{}}, rates, settings)
	h := &topupHarness{store: &handlerStore{}}
	if storage {
		svc = svc.WithProofStore(h.store)
	}
	h.r = gin.New()
	h.r.Use(func(c *gin.Context) {
		c.Set("company_id", c.GetHeader("X-Company"))
		c.Set("user_id", "user-1")
	})
	NewTopupsHandler(svc, func(c *gin.Context) { c.Next() }).Register(h.r.Group("/api"))
	return h
}

func (h *topupHarness) do(t *testing.T, method, path, company string, body io.Reader, contentType string) (int, map[string]any, http.Header) {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("X-Company", company)
	w := httptest.NewRecorder()
	h.r.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out, w.Header()
}

func (h *topupHarness) create(t *testing.T, company, body string) (int, map[string]any) {
	t.Helper()
	code, out, _ := h.do(t, http.MethodPost, "/api/topups", company, strings.NewReader(body), "application/json")
	return code, out
}

func proofForm(t *testing.T, payee string, file []byte) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if payee != "" {
		_ = mw.WriteField("payee_id", payee)
	}
	fw, err := mw.CreateFormFile("file", "mutasi.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(file)
	_ = mw.Close()
	return &buf, mw.FormDataContentType()
}

func png(size int) []byte {
	b := make([]byte, size)
	copy(b, "\x89PNG\r\n\x1a\n")
	return b
}

// AC1: only amount_idr is read; the credit and the rate are the server's.
func TestTopupCreateIgnoresEverythingButTheAmount(t *testing.T) {
	h := newTopupHarness(t, true, true)
	code, out := h.create(t, "co-a", `{"amount_idr":150000,"credit_micro_usd":999999999,"fx_effective_idr_per_usd_e4":1,"unique_code":1}`)
	if code != http.StatusCreated {
		t.Fatalf("%d %v", code, out)
	}
	if out["credit_micro_usd"] != float64(8_928_571) || out["amount_idr"] != float64(150_000) || out["status"] != "awaiting_payment" {
		t.Errorf("created %v", out)
	}
	uc, _ := out["unique_code"].(float64)
	if uc < 1 || uc > 999 || out["expected_amount_idr"] != 150_000+uc {
		t.Errorf("code %v, expected amount %v", out["unique_code"], out["expected_amount_idr"])
	}
	// AC28: a tenant's response names no rate, no spread, no key.
	for key := range out {
		if strings.HasPrefix(key, "fx_") || strings.Contains(key, "proof_key") || strings.Contains(key, "sha") {
			t.Errorf("tenant response carries %s", key)
		}
	}
	if code, out := h.create(t, "co-a", `{"amount_idr":50000}`); code != http.StatusConflict || out["error"] != "You already have a top-up in progress." {
		t.Errorf("second request: %d %v", code, out)
	}
}

// AC22 over HTTP.
func TestTopupCreateAmountRefusals(t *testing.T) {
	h := newTopupHarness(t, true, true)
	for _, c := range []struct {
		body, want string
	}{
		{`{"amount_idr":19000}`, "The minimum top-up is Rp 20.000."},
		{`{"amount_idr":5001000}`, "The maximum top-up is Rp 5.000.000."},
		{`{"amount_idr":150500}`, "Enter an amount in whole thousands of rupiah."},
		{`{"amount_idr":"150000"}`, "Enter an amount in whole thousands of rupiah."},
		{`{}`, "Enter an amount in whole thousands of rupiah."},
		{`not json`, "Enter an amount in whole thousands of rupiah."},
	} {
		if code, out := h.create(t, "co-a", c.body); code != http.StatusBadRequest || out["error"] != c.want {
			t.Errorf("%s: %d %v, want 400 %q", c.body, code, out, c.want)
		}
	}
	for _, edge := range []string{"20000", "5000000"} {
		code, out := h.create(t, "co-a", `{"amount_idr":`+edge+`}`)
		if code != http.StatusCreated {
			t.Fatalf("%s refused: %d %v", edge, code, out)
		}
		if code, _, _ := h.do(t, http.MethodPost, "/api/topups/"+out["id"].(string)+"/cancel", "co-a", nil, ""); code != http.StatusOK {
			t.Fatalf("cancel: %d", code)
		}
	}
}

// AC4 / AC5 / AC12 over HTTP.
func TestTopupProofRoutes(t *testing.T) {
	h := newTopupHarness(t, true, true)
	_, created := h.create(t, "co-a", `{"amount_idr":100000}`)
	id := created["id"].(string)
	proof := "/api/topups/" + id + "/proof"

	for _, c := range []struct {
		name, payee string
		file        []byte
		code        int
		msg         string
	}{
		{"7 MB", "bri", png(7 << 20), http.StatusRequestEntityTooLarge, "That file is larger than 5 MB. Upload a smaller screenshot."},
		{"text renamed .png", "bri", []byte("not an image at all"), http.StatusUnsupportedMediaType, "Upload a PNG, JPG, WebP or PDF file."},
		{"unknown payee", "xyz", png(1 << 10), http.StatusBadRequest, "Choose the account you paid to."},
	} {
		body, ct := proofForm(t, c.payee, c.file)
		if code, out, _ := h.do(t, http.MethodPost, proof, "co-a", body, ct); code != c.code || out["error"] != c.msg {
			t.Errorf("%s: %d %v, want %d %q", c.name, code, out, c.code, c.msg)
		}
	}
	if len(h.store.keys) != 0 {
		t.Fatalf("a refused proof was stored: %v", h.store.keys)
	}

	// Another workspace's id is not found.
	body, ct := proofForm(t, "bri", png(1<<10))
	if code, _, _ := h.do(t, http.MethodPost, proof, "co-b", body, ct); code != http.StatusNotFound {
		t.Errorf("B's proof on A's top-up: %d", code)
	}

	body, ct = proofForm(t, "bri", png(300<<10))
	code, out, _ := h.do(t, http.MethodPost, proof, "co-a", body, ct)
	if code != http.StatusOK || out["status"] != "in_review" || out["payee_id"] != "bri" {
		t.Fatalf("upload: %d %v", code, out)
	}
	if len(h.store.keys) != 1 || !strings.HasPrefix(h.store.keys[0], "topups/co-a/"+id+"/") {
		t.Errorf("stored %v", h.store.keys)
	}

	// The list carries no key and no URL.
	req := httptest.NewRequest(http.MethodGet, "/api/topups", nil)
	req.Header.Set("X-Company", "co-a")
	w := httptest.NewRecorder()
	h.r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "topups/co-a") || strings.Contains(w.Body.String(), "http") ||
		strings.Contains(w.Body.String(), "fx_") {
		t.Errorf("list: %d %s", w.Code, w.Body)
	}

	if code, out, _ := h.do(t, http.MethodPost, "/api/topups/"+id+"/cancel", "co-a", nil, ""); code != http.StatusConflict ||
		out["error"] != "Only a top-up waiting for payment can be cancelled." {
		t.Errorf("cancel in review: %d %v", code, out)
	}

	// Operator: view the proof, approve once, then 409.
	code, out, hdr := h.do(t, http.MethodGet, "/api/operator/topups/"+id+"/proof", "", nil, "")
	if code != http.StatusOK || out["expires_in"] != float64(300) || out["content_type"] != "image/png" || hdr.Get("Cache-Control") != "no-store" {
		t.Errorf("proof link: %d %v %v", code, out, hdr)
	}
	code, out, _ = h.do(t, http.MethodGet, "/api/operator/topups?view=review", "", nil, "")
	if list, _ := out["topups"].([]any); code != http.StatusOK || len(list) != 1 {
		t.Fatalf("queue: %d %v", code, out)
	} else if row := list[0].(map[string]any); row["fx_effective_idr_per_usd_e4"] != float64(168_000_000) || row["fx_spread_bp"] != float64(500) ||
		row["payee_label"] != "BRI" {
		t.Errorf("the operator's row lacks the frozen rate: %v", row)
	}
	if code, _, _ := h.do(t, http.MethodGet, "/api/operator/topups?view=everything", "", nil, ""); code != http.StatusBadRequest {
		t.Errorf("unknown view: %d", code)
	}
	code, out, _ = h.do(t, http.MethodPost, "/api/operator/topups/"+id+"/reject", "", strings.NewReader(`{"reason":"  "}`), "application/json")
	if code != http.StatusBadRequest || out["error"] != "Write a reason the customer will see." {
		t.Errorf("empty reason: %d %v", code, out)
	}
	code, out, _ = h.do(t, http.MethodPost, "/api/operator/topups/"+id+"/approve", "", nil, "")
	if code != http.StatusOK || out["balance_micro_usd"] != float64(120_000+5_952_380) {
		t.Errorf("approve: %d %v", code, out)
	}
	code, out, _ = h.do(t, http.MethodPost, "/api/operator/topups/"+id+"/approve", "", nil, "")
	if code != http.StatusConflict || out["error"] != "This top-up was already reviewed." {
		t.Errorf("second approve: %d %v", code, out)
	}
	if code, _, _ := h.do(t, http.MethodPost, "/api/operator/topups/00000000-0000-4000-8000-999999999999/approve", "", nil, ""); code != http.StatusNotFound {
		t.Errorf("approve of nothing: %d", code)
	}
}

// AC5's last arm: no object storage.
func TestTopupProofWithoutStorage(t *testing.T) {
	h := newTopupHarness(t, true, false)
	_, created := h.create(t, "co-a", `{"amount_idr":100000}`)
	body, ct := proofForm(t, "bri", png(1<<10))
	code, out, _ := h.do(t, http.MethodPost, "/api/topups/"+created["id"].(string)+"/proof", "co-a", body, ct)
	if code != http.StatusServiceUnavailable || out["error"] != "Proof upload is not available right now. Try again later." {
		t.Errorf("%d %v", code, out)
	}
}

// AC13: no payees, nothing to request.
func TestTopupOff(t *testing.T) {
	h := newTopupHarness(t, false, true)
	code, out, _ := h.do(t, http.MethodGet, "/api/topups/options", "co-a", nil, "")
	if payees, _ := out["payees"].([]any); code != http.StatusOK || out["enabled"] != false || payees == nil || len(payees) != 0 {
		t.Errorf("options: %d %v", code, out)
	}
	if code, out := h.create(t, "co-a", `{"amount_idr":100000}`); code != http.StatusServiceUnavailable ||
		out["error"] != "Top-up is not available right now. Please try again later." {
		t.Errorf("create: %d %v", code, out)
	}

	on := newTopupHarness(t, true, true)
	_, opts, _ := on.do(t, http.MethodGet, "/api/topups/options", "co-a", nil, "")
	if opts["enabled"] != true || opts["preview_idr_per_usd_e4"] != float64(168_000_000) || opts["step_idr"] != float64(1000) {
		t.Errorf("options on: %v", opts)
	}
	for key := range opts {
		if strings.Contains(key, "market") || strings.Contains(key, "spread") {
			t.Errorf("tenant options carry %s", key)
		}
	}
}

// A deployment without the service answers, rather than 404ing.
func TestTopupRoutesWithoutAService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	NewTopupsHandler(nil, nil).Register(r.Group("/api"))
	for _, c := range []struct {
		method, path string
		code         int
	}{
		{http.MethodGet, "/api/topups/options", http.StatusOK},
		{http.MethodPost, "/api/topups", http.StatusServiceUnavailable},
		{http.MethodGet, "/api/topups", http.StatusServiceUnavailable},
		{http.MethodGet, "/api/operator/topups", http.StatusForbidden},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(c.method, c.path, nil))
		if w.Code != c.code {
			t.Errorf("%s %s: %d, want %d", c.method, c.path, w.Code, c.code)
		}
	}
}
