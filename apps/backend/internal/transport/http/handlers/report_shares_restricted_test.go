package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
)

// T-Z13 at the routes: minting a link on a document from a restricted agent's
// conversation is a 409 with the reason, and a live link on one is listed as
// paused rather than as working.

// shutConversations is app.ShareConversations over a set of conversations that
// hold a restricted agent.
type shutConversations struct {
	shut map[string]bool
	err  error
}

func (s shutConversations) OpenToEveryone(_ context.Context, _, threadID string) (bool, error) {
	return !s.shut[threadID], s.err
}

func sharesRouter(conv shutConversations) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("company_id", "co-1")
		c.Set("user_id", "admin-1")
		c.Set("role", "admin")
	})
	// linkedShares panics on Insert, which is the signal a refused mint wrote.
	svc := app.NewReportShareService(&linkedShares{}, newFourDocuments(), nil, nil).WithConversations(conv)
	NewReportShareHandler(svc).Register(r.Group("/api"))
	return r
}

func listedShares(t *testing.T, w *httptest.ResponseRecorder) []shareResponse {
	t.Helper()
	var body struct {
		Shares []shareResponse `json:"shares"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v: %s", err, w.Body.String())
	}
	return body.Shares
}

func TestADocumentFromARestrictedConversationIsNotSharedAndItsLinksArePaused(t *testing.T) {
	r := sharesRouter(shutConversations{shut: map[string]bool{"th-hidden": true}})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/documents/doc-hidden/shares", nil))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "restricted agent") {
		t.Errorf("minting: %d %s, want a 409 saying why", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/documents/doc-hidden/shares", nil))
	if got := listedShares(t, w); len(got) != 1 || !got[0].Live || !got[0].Paused {
		t.Errorf("listing a restricted conversation's document: %+v, want its live link paused", got)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/documents/doc-open/shares", nil))
	if got := listedShares(t, w); len(got) != 1 || got[0].Paused || strings.Contains(w.Body.String(), "paused") {
		t.Errorf("listing an open conversation's document: %s, want no pause", w.Body.String())
	}
}

func TestAShareRouteWhoseCheckFailsMintsAndListsNothing(t *testing.T) {
	r := sharesRouter(shutConversations{err: errors.New("connection refused")})
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(method, "/api/documents/doc-hidden/shares", nil))
		if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "connection refused") {
			t.Errorf("%s: %d %s, want a 503 without the cause", method, w.Code, w.Body.String())
		}
	}
}
