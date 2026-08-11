package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// T-H3. `Access-Control-Allow-Credentials: true` was sent on every response,
// including ones carrying no Allow-Origin at all. A browser needs the pair, so
// the stray header granted nothing — but the two belong together, and a
// deployment whose CORS_ORIGINS is empty reflects every Origin *and* sends the
// credential permission, which is any site being allowed to read an
// authenticated response. `Validate()` now refuses that combination in
// production; this pins the middleware's half.

func corsRig(origins []string, skip ...string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CORS(origins, skip...))
	r.GET("/api/ping", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/v1/ping", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func corsGet(r *gin.Engine, path, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestCORSPairsCredentialsWithAnAllowedOrigin(t *testing.T) {
	r := corsRig([]string{"https://app.example.com"})

	t.Run("an allowed origin gets both headers", func(t *testing.T) {
		w := corsGet(r, "/api/ping", "https://app.example.com")
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
			t.Errorf("Allow-Origin = %q", got)
		}
		if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
			t.Errorf("Allow-Credentials = %q, want true", got)
		}
		if got := w.Header().Get("Vary"); got != "Origin" {
			t.Errorf("Vary = %q, want Origin", got)
		}
	})

	t.Run("a refused origin gets neither", func(t *testing.T) {
		w := corsGet(r, "/api/ping", "https://evil.example.com")
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("Allow-Origin = %q, want empty", got)
		}
		if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
			t.Errorf("Allow-Credentials = %q, want empty on a refused origin", got)
		}
		// Still varies: a cache that stored the allowed origin's response
		// without this would hand it, headers and all, to the next caller.
		if got := w.Header().Get("Vary"); got != "Origin" {
			t.Errorf("Vary = %q, want Origin", got)
		}
	})
}

// The development behaviour, kept deliberately: a laptop running the dashboard
// on an unexpected port is what an empty list is for. Production cannot boot in
// this state — see TestValidateRequiresCORSOriginsInProduction.
func TestCORSWithNoOriginsConfiguredStillReflects(t *testing.T) {
	w := corsGet(corsRig(nil), "/api/ping", "http://localhost:4321")
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:4321" {
		t.Errorf("Allow-Origin = %q, want the reflected origin", got)
	}
}

// `/v1` authenticates with an API key, and a credential a browser can send is a
// credential that shipped in someone's bundle (T-13).
func TestCORSSkipsThePublicAPI(t *testing.T) {
	w := corsGet(corsRig([]string{"https://app.example.com"}, "/v1"), "/v1/ping", "https://app.example.com")
	for _, h := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials", "Access-Control-Allow-Headers"} {
		if got := w.Header().Get(h); got != "" {
			t.Errorf("%s = %q on a skipped prefix, want empty", h, got)
		}
	}
}
