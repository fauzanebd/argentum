package handlers

import (
	"context"
	"encoding/json"
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
	"github.com/fauzanebd/argentum/internal/docgen"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/idempotency"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/report/video"
	"github.com/fauzanebd/argentum/internal/report/videoplan"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
)

// `POST /v1/reports/render` with `format: "mp4"` (T-V3).
//
// The door never waits for a video: it takes minutes in another process, so a
// synchronous window that a PDF measured in milliseconds is not a window a
// video can fit in at any setting. What is asserted here is that the 202
// arrives without a render being attempted, and that the four ways this
// request can be wrong are each answered before any work is queued.

type fakeRenderQueue struct {
	mu   sync.Mutex
	jobs []queue.ReportRenderPayload
	err  error
}

func (f *fakeRenderQueue) EnqueueReportRender(_ context.Context, p queue.ReportRenderPayload) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	f.jobs = append(f.jobs, p)
	return "task-1", nil
}

func (f *fakeRenderQueue) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.jobs)
}

// budgetOf answers one fixed verdict. The interface is V1BudgetReader, the
// same one `/v1/me` reads through.
type fixedBudget struct{ state app.BudgetState }

func (b fixedBudget) CheckBudget(context.Context, string) (app.BudgetState, error) {
	return b.state, nil
}

type videoFixture struct {
	router  *gin.Engine
	queue   *fakeRenderQueue
	reports *fakeAPIReports
}

// newVideoFixture builds the render door with a video-capable generator.
//
// The generator's render client points at a URL nothing is listening on, and
// that is deliberate: every test here asserts the handler answers *before*
// anything renders, so a request that reached the client would hang rather
// than pass quietly.
func newVideoFixture(t *testing.T, opts ...func(*V1ReportsHandler)) *videoFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)

	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	f := &videoFixture{queue: &fakeRenderQueue{}, reports: &fakeAPIReports{}}
	gen := docgen.New(nil, nil, nil, nil, nil, time.Hour).
		WithVideo(video.New(video.Options{BaseURL: "http://127.0.0.1:1"}), videoplan.Limits{})

	r := gin.New()
	v1 := r.Group("/v1")
	v1.Use(middleware.RequestID())
	v1.Use(func(c *gin.Context) {
		c.Set("company_id", testCompany)
		c.Set(middleware.CtxAPIKeyID, "key-1")
		c.Set(middleware.CtxAPIKeyScopes, []domain.Scope{domain.ScopeWriteReports, domain.ScopeReadDocuments})
	})
	h := NewV1ReportsHandler(gen, f.reports, nil, nil, f.queue, rdb,
		idempotency.NewRedisStore(rdb), time.Minute, false)
	for _, opt := range opts {
		opt(h)
	}
	h.Register(v1)
	f.router = r
	return f
}

// videoSpec is an analytical report — a KPI row and prose — which is what the
// format requires. Kept beside the tests rather than in a helper package: the
// thing being tested is partly *which* documents are acceptable.
const videoSpec = `{
  "format": "mp4",
  "title": "June review",
  "content": {"sections": [
    {"type": "kpi_row", "items": [{"label": "Revenue", "value": {"v": 4012118800, "fmt": "currency"}}]},
    {"type": "paragraph", "text": "Revenue closed June 3.9% above May, the third consecutive month of growth, and all of the gain came from the North region where two enterprise accounts renewed early."}
  ]}
}`

func (f *videoFixture) render(t *testing.T, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/reports/render", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", t.Name())
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

// The flagship of the ticket: a spec in, a 202 out, a job queued, and no
// connection held open.
func TestVideoRenderAnswers202AndQueues(t *testing.T) {
	f := newVideoFixture(t)

	w := f.render(t, videoSpec, nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body["object"] != "report" || body["id"] == "" {
		t.Fatalf("a caller cannot collect this: %s", w.Body.String())
	}
	if body["format"] != "mp4" {
		t.Errorf("format = %v, want mp4", body["format"])
	}
	if f.queue.count() != 1 {
		t.Fatalf("%d jobs queued, want 1", f.queue.count())
	}
	if got := f.queue.jobs[0].Spec.Format; got != "mp4" {
		t.Errorf("queued spec format = %q", got)
	}
	if f.queue.jobs[0].ReportID == "" {
		t.Error("the job carries no report id; nothing could mark it complete")
	}
}

// `Accept: video/mp4` is refused rather than honoured four minutes later.
//
// The alternative — hold the connection and stream the bytes — is what the
// other four formats do, and it is exactly wrong here: the caller has to write
// the asynchronous collection path anyway, and finding that out after a
// four-minute timeout is the worst way to learn it.
func TestVideoRefusesInlineBytes(t *testing.T) {
	f := newVideoFixture(t)

	w := f.render(t, videoSpec, map[string]string{"Accept": "video/mp4"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{"async_format", "/v1/reports"} {
		if !strings.Contains(body, want) {
			t.Errorf("the refusal does not name %q: %s", want, body)
		}
	}
	if f.queue.count() != 0 {
		t.Error("a refused request queued work")
	}
}

// A record is refused before anything is queued, and the refusal is the spec's
// own — the one that names the way out.
func TestVideoRefusesANonAnalyticalSpec(t *testing.T) {
	f := newVideoFixture(t)

	const invoice = `{"format":"mp4","title":"Invoice","content":{"sections":[
	  {"type":"key_value","items":[{"label":"Invoice","value":"INV-1042"}]}
	]}}`
	w := f.render(t, invoice, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if f.queue.count() != 0 {
		t.Fatal("an invoice was queued as a video")
	}
	if !strings.Contains(w.Body.String(), "pdf") {
		t.Errorf("the refusal does not say what to do instead: %s", w.Body.String())
	}
}

// A tenant at zero credits is refused **before** the job exists. After it, the
// render pod has already been committed to minutes of work nobody can pay for
// — which is the unbounded spend T-03 exists to stop.
func TestVideoRefusesAnExhaustedTenant(t *testing.T) {
	f := newVideoFixture(t, func(h *V1ReportsHandler) {
		h.WithBudget(fixedBudget{state: app.BudgetState{
			Verdict: app.BudgetExhausted, Enforced: true,
		}})
	})

	w := f.render(t, videoSpec, nil)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402: %s", w.Code, w.Body.String())
	}
	if f.queue.count() != 0 {
		t.Fatal("an exhausted tenant queued a render")
	}
	if f.reports.created != nil {
		t.Error("a refused request left a report row behind")
	}
}

// With no render service configured, the format is unavailable rather than
// broken — and the message says the others still work, because an integrator
// reading "unavailable" needs to know whether to change their format or their
// deployment.
func TestVideoIsRefusedWhenUnconfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	q := &fakeRenderQueue{}
	// The same generator, with no video client on it.
	gen := docgen.New(nil, nil, nil, nil, nil, time.Hour)
	r := gin.New()
	v1 := r.Group("/v1")
	v1.Use(middleware.RequestID())
	v1.Use(func(c *gin.Context) {
		c.Set("company_id", testCompany)
		c.Set(middleware.CtxAPIKeyID, "key-1")
		c.Set(middleware.CtxAPIKeyScopes, []domain.Scope{domain.ScopeWriteReports})
	})
	NewV1ReportsHandler(gen, &fakeAPIReports{}, nil, nil, q, rdb,
		idempotency.NewRedisStore(rdb), time.Minute, false).Register(v1)

	req := httptest.NewRequest(http.MethodPost, "/v1/reports/render", strings.NewReader(videoSpec))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", t.Name())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "format_unavailable") {
		t.Errorf("body = %s", w.Body.String())
	}
	if q.count() != 0 {
		t.Error("an unconfigured deployment queued a render")
	}
}
