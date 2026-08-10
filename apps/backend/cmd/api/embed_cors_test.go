package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// The CORS preflight for the embed surface (T-19/T-20).
//
// This exists because the browser gate found it missing on 2026-08-10, after
// the entire mint matrix had already passed over HTTP. The gap between those
// two facts is the whole lesson: **`curl` does not preflight**. Every
// transcript in `embed-auth.md` §5 sent its POST directly, so a surface that no
// browser could reach looked completely healthy.
//
// The mechanism is worth stating too, because it is not obvious: gin runs group
// middleware only for routes that **exist**. `OPTIONS /api/embed/session` was
// never registered, so it fell through to the 404 handler — which no group
// wraps — and the response carried no `Access-Control-Allow-Origin` at all.
func TestEmbedPreflightIsAnswered(t *testing.T) {
	r := embedRouterForTest(t)

	paths := []string{
		"/api/embed/session",
		"/api/embed/session/refresh",
		"/api/embed/config",
		"/api/embed/chat",
		"/api/embed/threads/current",
		"/api/embed/threads/th-1/messages",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodOptions, path, nil)
			req.Header.Set("Origin", "https://intranet.acme.com")
			req.Header.Set("Access-Control-Request-Method", "POST")
			req.Header.Set("Access-Control-Request-Headers", "content-type,authorization")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204 — a preflight that 404s blocks every browser request behind it", w.Code)
			}
			if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://intranet.acme.com" {
				t.Errorf("Allow-Origin = %q, want the caller's origin reflected", got)
			}
			// Without credentials, deliberately: the embed surface carries a
			// bearer token and no cookie, and reflection plus credentials is the
			// combination that would make it unsafe.
			if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
				t.Errorf("Allow-Credentials = %q, want it absent", got)
			}
			if got := w.Header().Get("Access-Control-Allow-Headers"); got == "" {
				t.Error("no Allow-Headers, so a request sending Authorization is blocked")
			}
			if got := w.Header().Get("Vary"); got != "Origin" {
				t.Errorf("Vary = %q, want Origin — a shared cache must not serve one tenant's header to another", got)
			}
		})
	}
}

// The real POST still carries the header, since a preflight only licenses the
// request that follows it.
func TestEmbedPostCarriesCORSHeaders(t *testing.T) {
	r := embedRouterForTest(t)

	req := httptest.NewRequest(http.MethodPost, "/api/embed/session", nil)
	req.Header.Set("Origin", "https://intranet.acme.com")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://intranet.acme.com" {
		t.Errorf("Allow-Origin = %q on the POST, want the origin reflected — a browser cannot read a refusal without it", got)
	}
}

// embedRouterForTest builds the real router with the embed surface switched on.
func embedRouterForTest(t *testing.T) *gin.Engine {
	t.Helper()
	return routerWithDeps(t, func(d *apiDeps) {
		d.cfg.EmbedEnabled = true
	})
}
