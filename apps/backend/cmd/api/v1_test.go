package main

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
)

// A syntactically valid Argentum key that no database will ever recognise.
// It is used against `/api` routes, which must refuse it before anything
// looks it up.
const wellFormedAPIKey = "arg_0123456789_QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVphYmNkZQ"

// v1Routes returns every registered route under /v1.
func v1Routes(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, ri := range realRouter(t).Routes() {
		if strings.HasPrefix(ri.Path, "/v1") {
			out = append(out, middleware.RouteKey(ri.Method, ri.Path))
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		t.Fatal("no /v1 routes are registered; this test would pass vacuously")
	}
	return out
}

// TestV1RejectsADashboardSession is half of phase 1c's exit criterion: the two
// authorities do not cross. A dashboard JWT is a person's session and carries
// a role; a /v1 route wants a key and its scopes. Accepting the JWT here would
// make "which routes can a browser reach?" unanswerable.
func TestV1RejectsADashboardSession(t *testing.T) {
	r := realRouter(t)
	signer, err := auth.NewTokenSigner("0123456789abcdef0123456789abcdef", 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}
	token, err := signer.IssueAccessToken("user-1", "co-1", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	for _, key := range v1Routes(t) {
		method, path, _ := strings.Cut(key, " ")
		t.Run(key, func(t *testing.T) {
			req, err := http.NewRequest(method, concreteURL(path), nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			req.Header.Set("Authorization", "Bearer "+token)
			if code := statusOf(r, req); code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401 — an admin's dashboard token reached a /v1 route", code)
			}
		})
	}
}

// TestDashboardRejectsAnAPIKey is the other half. A key is a machine
// credential with no role, and every /api route is role-gated, so admitting
// one would mean admitting a caller the policy table cannot classify.
func TestDashboardRejectsAnAPIKey(t *testing.T) {
	r := realRouter(t)

	var keys []string
	for key := range apiPolicy {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		method, path, _ := strings.Cut(key, " ")
		t.Run(key, func(t *testing.T) {
			req, err := http.NewRequest(method, concreteURL(path), nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			req.Header.Set("Authorization", "Bearer "+wellFormedAPIKey)
			if code := statusOf(r, req); code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401 — an API key reached a dashboard route", code)
			}
		})
	}
}

// TestV1EmitsNoCORSHeaders pins what the live gate found: CORS is installed on
// the engine, above every group, so /v1 inherited the dashboard's permissive
// headers — and with CORS_ORIGINS unset that middleware echoes any Origin.
// A key usable from a web page is a key that shipped in somebody's bundle.
func TestV1EmitsNoCORSHeaders(t *testing.T) {
	r := realRouter(t)
	req, err := http.NewRequest(http.MethodGet, "/v1/me", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Origin", "https://attacker.example")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	for _, h := range []string{
		"Access-Control-Allow-Origin",
		"Access-Control-Allow-Credentials",
		"Access-Control-Allow-Headers",
		"Access-Control-Allow-Methods",
	} {
		if got := w.Header().Get(h); got != "" {
			t.Errorf("/v1 sent %s: %q — a browser must not be able to use an API key", h, got)
		}
	}

	// The dashboard still gets them, or this test would pass by breaking CORS
	// everywhere.
	apiReq, err := http.NewRequest(http.MethodGet, "/api/users/me", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	apiReq.Header.Set("Origin", "https://dashboard.example")
	apiW := httptest.NewRecorder()
	r.ServeHTTP(apiW, apiReq)
	// Allow-Credentials rather than Allow-Origin: the latter is only echoed
	// for an origin on the allowlist, and this router's allowlist is "*"
	// literally, so a made-up origin would not get one either way.
	if apiW.Header().Get("Access-Control-Allow-Credentials") == "" {
		t.Error("/api lost its CORS headers")
	}
}

// TestV1RoutesAreNotRoleGated keeps the exemption in unpolicedPaths honest.
// A /v1 route listed in apiPolicy would be one whose access decision is
// written in a table that never runs on it — the worst kind of stale
// documentation, because it reads like a control.
func TestV1RoutesAreNotRoleGated(t *testing.T) {
	for _, key := range v1Routes(t) {
		if role, listed := apiPolicy[key]; listed {
			t.Errorf("%s is classified %q in apiPolicy, but RequireRole never runs on /v1", key, role)
		}
	}
}

// TestV1RoutesAreExempted is the inverse: a new /v1 route that nobody added to
// unpolicedPaths would be reported as unclassified by
// TestEveryAuthedRouteIsClassified, which is a confusing way to learn about a
// real omission. This says the quiet part instead.
func TestV1RoutesAreExempted(t *testing.T) {
	for _, ri := range realRouter(t).Routes() {
		if strings.HasPrefix(ri.Path, "/v1") && !unpolicedPaths[ri.Path] {
			t.Errorf("%s is not in unpolicedPaths; add it with the scope that gates it", ri.Path)
		}
	}
}
