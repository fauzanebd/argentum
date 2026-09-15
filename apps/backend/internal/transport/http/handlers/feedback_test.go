package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	logrustest "github.com/sirupsen/logrus/hooks/test"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// fbBrokenStore is a feedback table whose every statement fails with the
// driver's own sentence.
type fbBrokenStore struct{ err error }

func (s fbBrokenStore) Upsert(context.Context, *domain.MessageFeedback) error { return s.err }
func (s fbBrokenStore) GetByMessage(context.Context, string, string) ([]*domain.MessageFeedback, error) {
	return nil, s.err
}
func (s fbBrokenStore) ListByCompany(context.Context, string, bool, int, int) ([]*domain.MessageFeedback, error) {
	return nil, s.err
}
func (s fbBrokenStore) ListWithContext(context.Context, string, bool, int, int) ([]*domain.FeedbackWithContext, error) {
	return nil, s.err
}
func (s fbBrokenStore) Summarize(context.Context, string, time.Time, time.Time) (domain.FeedbackSummary, error) {
	return domain.FeedbackSummary{}, s.err
}
func (s fbBrokenStore) NegativeMessageIDs(context.Context, string, []string) (map[string]bool, error) {
	return nil, s.err
}

// fbAnswer is a message lookup that finds one answer.
type fbAnswer struct{}

func (fbAnswer) GetForCompany(context.Context, string, string) (*domain.Message, error) {
	return &domain.Message{ID: "msg-1", ThreadID: "th-1", Role: domain.MessageRoleAssistant}, nil
}

// A failure on any feedback route answers a sentence and never the driver's.
//
// Found on 2026-09-14, beside T-W8's gate: `GET /api/messages/x/feedback`
// answered `pq: invalid input syntax for type uuid: "x" (22P02)` as the body of a
// 500, because feedbackFail's last branch wrote err.Error(). The malformed id is
// fixed in the repository; this is the half that would quote the next database
// failure too — a dropped connection names the host it could not reach. The
// operator still gets the error, in the log.
func TestFeedbackFailureNeverQuotesTheDatabase(t *testing.T) {
	hook := logrustest.NewGlobal()
	defer hook.Reset()

	const driver = `pq: invalid input syntax for type uuid: "x" (22P02)`
	svc := app.NewFeedbackService(fbBrokenStore{err: errors.New(driver)}, fbAnswer{})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api")
	g.Use(func(c *gin.Context) {
		c.Set("company_id", "co-1")
		c.Set("user_id", "u-1")
		c.Set("role", "admin")
	})
	NewFeedbackHandler(svc).Register(g)

	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/messages/msg-1/feedback", ""},
		{http.MethodPost, "/api/messages/msg-1/feedback", `{"rating":1}`},
		{http.MethodGet, "/api/feedback", ""},
		{http.MethodGet, "/api/feedback/summary", ""},
	} {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusInternalServerError {
			t.Errorf("%s %s: status %d, want 500: %s", c.method, c.path, w.Code, w.Body.String())
		}
		if body := w.Body.String(); strings.Contains(body, "pq:") || strings.Contains(body, "22P02") {
			t.Errorf("%s %s: the response quotes the database: %s", c.method, c.path, body)
		}
	}

	logged := 0
	for _, e := range hook.AllEntries() {
		if err, ok := e.Data["error"].(error); ok && strings.Contains(err.Error(), "22P02") && e.Data["company_id"] == "co-1" {
			logged++
		}
	}
	if logged != 4 {
		t.Errorf("%d of 4 failures logged with their error and company; the operator must still see what happened", logged)
	}
}
