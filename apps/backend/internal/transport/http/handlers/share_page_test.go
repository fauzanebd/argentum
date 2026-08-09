package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// `GET /share/:token` (T-V4) — the one route in this codebase that
// authenticates nobody.
//
// The handler's job is small and the properties are all about what it must
// *not* do: leak which of four failures happened, let anything cache a page
// that can be revoked, or invite a crawler to index a link somebody emailed.

func shareRouter(h *ShareHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h.Register(r.Group("/share"))
	return r
}

func getShare(t *testing.T, r *gin.Engine, token string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/share/"+token, nil))
	return w
}

// A deployment with no object storage stored no plans, so no token can open
// anything. The visitor gets the same 404 a wrong token gets — our
// configuration is not something to tell a stranger about.
func TestAShareOnADeploymentWithoutStorageIsANotFound(t *testing.T) {
	w := getShare(t, shareRouter(NewShareHandler(nil)), "anything")

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", w.Code, w.Body.String())
	}
	if strings.Contains(strings.ToLower(w.Body.String()), "storage") {
		t.Errorf("the refusal describes our deployment to a stranger: %s", w.Body.String())
	}
}

// Both headers are load-bearing rather than hygiene.
//
// `no-store` is what makes revocation mean anything: a CDN or a corporate
// proxy holding a copy would keep serving a link that has been taken back,
// from a machine we cannot reach. `noindex` is because a link in an email is a
// link a crawler eventually follows.
func TestASharePageIsNeitherCachedNorIndexed(t *testing.T) {
	w := getShare(t, shareRouter(NewShareHandler(nil)), "anything")

	if got := w.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store — a cached page outlives its revocation", got)
	}
	if got := w.Header().Get("X-Robots-Tag"); !strings.Contains(got, "noindex") {
		t.Errorf("X-Robots-Tag = %q, want noindex", got)
	}
}

// The headers go out on the refusal too. A 404 that is cacheable is a 404 that
// keeps being served after the share is fixed, and one that is indexable
// publishes the shape of the URL space.
func TestTheRefusalCarriesTheSameHeaders(t *testing.T) {
	w := getShare(t, shareRouter(NewShareHandler(nil)), "definitely-not-a-token")

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if w.Header().Get("Cache-Control") == "" || w.Header().Get("X-Robots-Tag") == "" {
		t.Error("a refusal went out without the headers the success path carries")
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %s", w.Body.String())
	}
	// The message must say nothing about which of the four failures it was.
	msg, _ := body["error"].(string)
	for _, leak := range []string{"expired", "revoked", "unknown", "deleted"} {
		if strings.Contains(strings.ToLower(msg), leak) {
			t.Errorf("the refusal names the failure (%q), turning the route into an oracle: %s", leak, msg)
		}
	}
}
