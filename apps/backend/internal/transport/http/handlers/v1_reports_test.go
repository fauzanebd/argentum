package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/idempotency"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
)

// What `POST /v1/reports` enqueues (T-A2b).
//
// The subject is one boundary: the caller's prompt is what the input
// guardrails inspect, and Argentum's own instructions for the turn are not.
// T-A2 sent both as one user message, and `semantic_prompt_injection` refused
// four of five live turns — correctly, because an instruction block is what it
// is written to catch. There is no way to observe that from the outside (the
// classifier is an LLM, and its verdict is not deterministic), so what is
// asserted here is the shape of what leaves the handler.

// fakeAPIReports implements domain.APIReportRepository. Only the create-side
// calls are real; a read the create path should never make panics.
type fakeAPIReports struct {
	mu       sync.Mutex
	created  *domain.APIReport
	threadID string
	// completed is the terminal status a refused enqueue wrote. Recorded
	// rather than panicked on (T-S5): closing the row is what keeps a job
	// whose turn never started from sitting `queued` forever, and a fake that
	// panicked there would make the refusal paths untestable.
	completed domain.APIReportStatus
}

func (f *fakeAPIReports) Create(_ context.Context, r *domain.APIReport) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r.ID = "3f7c1f3e-0000-4000-8000-0000000000re"
	r.CreatedAt = time.Now()
	f.created = r
	return nil
}

func (f *fakeAPIReports) AttachThread(_ context.Context, _, threadID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.threadID = threadID
	return nil
}

func (f *fakeAPIReports) GetForCompany(context.Context, string, string) (*domain.APIReport, error) {
	panic("unexpected GetForCompany")
}
func (f *fakeAPIReports) Get(context.Context, string) (*domain.APIReport, error) {
	panic("unexpected Get")
}
func (f *fakeAPIReports) MarkRunning(context.Context, string) error { panic("unexpected MarkRunning") }
func (f *fakeAPIReports) Complete(_ context.Context, _ string, status domain.APIReportStatus, _, _ string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completed = status
	return nil
}

type reportFixture struct {
	router  *gin.Engine
	enq     *fakeEnqueuer
	reports *fakeAPIReports
}

func newReportFixture(t *testing.T) *reportFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)

	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	f := &reportFixture{enq: &fakeEnqueuer{}, reports: &fakeAPIReports{}}

	r := gin.New()
	v1 := r.Group("/v1")
	v1.Use(middleware.RequestID())
	v1.Use(func(c *gin.Context) {
		c.Set("company_id", testCompany)
		c.Set(middleware.CtxAPIKeyID, "key-1")
		c.Set(middleware.CtxAPIKeyScopes, []domain.Scope{domain.ScopeWriteReports, domain.ScopeReadDocuments})
	})
	NewV1ReportsHandler(nil, f.reports, nil, f.enq, nil, rdb, idempotency.NewRedisStore(rdb), time.Second, false).
		Register(v1)
	f.router = r
	return f
}

func (f *reportFixture) create(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/reports", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// A different key per call: the middleware requires one, and replaying a
	// key would answer from the record rather than reaching the handler.
	req.Header.Set("Idempotency-Key", t.Name())
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

// The property T-A2b fixed: what the guardrails see is what the caller sent.
func TestReportPromptIsEnqueuedWithoutTheDirective(t *testing.T) {
	f := newReportFixture(t)

	const prompt = "Total sales by month for the last six months, with a bar chart."
	w := f.create(t, `{"prompt":`+quote(prompt)+`,"user_ref":"their-user-42","format":"pdf"}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", w.Code, w.Body.String())
	}

	if got := f.enq.in.Message; got != prompt {
		t.Errorf("the enqueued message is not the caller's prompt:\n  got:  %q\n  want: %q", got, prompt)
	}
	// Named clauses rather than the whole string: this asserts the directive
	// still says what T-A2 found it has to say, without pinning its wording.
	for _, clause := range []string{
		"generate_document",
		"spec_version=2",
		"Do not call create_visualization",
	} {
		if !strings.Contains(f.enq.in.Directive, clause) {
			t.Errorf("directive does not mention %q:\n%s", clause, f.enq.in.Directive)
		}
	}
	if strings.Contains(f.enq.in.Directive, prompt) {
		t.Error("the directive carries the caller's prompt; the two must travel separately")
	}
}

// The failure that produced this ticket, asserted as the thing it was: an
// instruction block inside a user message. If a future edit folds the
// directive back into the prompt, this is what fails.
func TestNoInstructionBlockTravelsInTheUserMessage(t *testing.T) {
	f := newReportFixture(t)

	w := f.create(t, `{"prompt":"Monthly revenue summary","user_ref":"their-user-42"}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", w.Code, w.Body.String())
	}

	msg := f.enq.in.Message
	for _, marker := range []string{"REPORT REQUEST", "You MUST", "Do not", "generate_document"} {
		if strings.Contains(msg, marker) {
			t.Errorf("the user message contains %q — an input guardrail reads this as an instruction override:\n%s", marker, msg)
		}
	}
}

// The format reaches the directive, because the tool call it names is the
// deliverable. `spec_version=2` is PDF and PPTX only — the v2 spec is a
// document layout, and there is no such thing for a CSV.
func TestDirectiveNamesTheRequestedFormat(t *testing.T) {
	for _, tc := range []struct {
		format      string
		wantVersion bool
	}{
		{"pdf", true},
		{"pptx", true},
		{"csv", false},
		{"xlsx", false},
	} {
		t.Run(tc.format, func(t *testing.T) {
			f := newReportFixture(t)
			w := f.create(t, `{"prompt":"Monthly revenue","user_ref":"u-1","format":"`+tc.format+`"}`)
			if w.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want 202: %s", w.Code, w.Body.String())
			}
			if !strings.Contains(f.enq.in.Directive, "format="+tc.format) {
				t.Errorf("directive does not name format=%s:\n%s", tc.format, f.enq.in.Directive)
			}
			if got := strings.Contains(f.enq.in.Directive, "spec_version=2"); got != tc.wantVersion {
				t.Errorf("spec_version=2 present = %v, want %v for %s", got, tc.wantVersion, tc.format)
			}
		})
	}
}

// quote is enough JSON string escaping for a fixture prompt.
func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }
