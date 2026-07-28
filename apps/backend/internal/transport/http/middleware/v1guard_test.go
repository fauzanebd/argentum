package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/transport/http/apierr"
)

// guardRouter is the real chain order from cmd/api: the request id first, the
// switch second, the body cap third. A test that installed them in a
// different order would prove nothing about the router that ships.
func guardRouter(enabled bool, maxBody int64) *gin.Engine {
	r := gin.New()
	v1 := r.Group("/v1")
	v1.Use(RequestID())
	v1.Use(Enabled(enabled))
	v1.Use(MaxBodyBytes(maxBody))
	v1.POST("/probe", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	v1.GET("/me", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	return r
}

func envelopeOf(t *testing.T, w *httptest.ResponseRecorder) apierr.Detail {
	t.Helper()
	var body apierr.Body
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not the /v1 envelope: %q", w.Body.String())
	}
	return body.Error
}

func TestDisabledAPIRefusesEveryRouteIncludingMe(t *testing.T) {
	r := guardRouter(false, 1<<20)

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/v1/me"},
		{http.MethodPost, "/v1/probe"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))

			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", w.Code)
			}
			if got := envelopeOf(t, w).Code; got != "api_disabled" {
				t.Errorf("code = %q, want api_disabled", got)
			}
			// Without it a disabled API is hammered by its own consumers'
			// retry loops for as long as it stays disabled.
			if w.Header().Get("Retry-After") == "" {
				t.Error("no Retry-After on a 503")
			}
			// The live gate caught this one: with the switch above
			// RequestID, the response an integrator is most likely to ask
			// about was the one with nothing to quote.
			if w.Header().Get("X-Request-Id") == "" {
				t.Error("no X-Request-Id on a 503")
			}
			if envelopeOf(t, w).RequestID == "" {
				t.Error("no request_id in the 503 envelope")
			}
		})
	}
}

// The switch sits above APIKeyAuth, so it must answer without a credential —
// otherwise "is the API up?" is a question only a key holder can ask.
func TestDisabledAPIAnswersWithoutACredential(t *testing.T) {
	w := httptest.NewRecorder()
	guardRouter(false, 1<<20).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/me", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 for an unauthenticated caller", w.Code)
	}
}

func TestEnabledAPIPassesThrough(t *testing.T) {
	w := httptest.NewRecorder()
	guardRouter(true, 1<<20).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/me", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with the switch on", w.Code)
	}
}

func TestBodyOverTheCapIsRefusedBeforeItIsRead(t *testing.T) {
	body := strings.NewReader(strings.Repeat("x", 2048))
	req := httptest.NewRequest(http.MethodPost, "/v1/probe", body)
	w := httptest.NewRecorder()
	guardRouter(true, 1024).ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
	detail := envelopeOf(t, w)
	if detail.Code != "request_too_large" {
		t.Errorf("code = %q, want request_too_large", detail.Code)
	}
	// The class stays inside the closed vocabulary even though the status
	// does not — a client switching on `type` must not meet a sixth value.
	if detail.Type != apierr.TypeInvalidRequest {
		t.Errorf("type = %q, want invalid_request", detail.Type)
	}
	if detail.RequestID == "" {
		t.Error("no request_id in the envelope — RequestID runs above this middleware")
	}
}

func TestBodyUnderTheCapIsAccepted(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/probe", strings.NewReader(`{"a":1}`))
	w := httptest.NewRecorder()
	guardRouter(true, 1024).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

// A caller can lie about Content-Length, or send none at all. The reader is
// the half that does not take the caller's word for it.
func TestChunkedBodyOverTheCapStillFails(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/probe", strings.NewReader(strings.Repeat("x", 4096)))
	req.ContentLength = -1

	r := gin.New()
	v1 := r.Group("/v1")
	v1.Use(MaxBodyBytes(1024))
	var readErr error
	v1.POST("/probe", func(c *gin.Context) {
		_, readErr = c.GetRawData()
		c.Status(http.StatusOK)
	})

	r.ServeHTTP(httptest.NewRecorder(), req)
	if readErr == nil {
		t.Error("an over-cap chunked body was read in full; MaxBytesReader is not installed")
	}
}
