package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/transport/http/apierr"
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

// keylessV1Routes carry no credential at all. There is exactly one, and it is
// the published contract (T-A4): an integrator reads the spec before they have
// a key, so requiring one would mean asking us for a credential in order to
// evaluate whether the API is worth integrating.
//
// It is a list rather than a special case inside each test because the two
// properties below — "a dashboard token is refused" and "every response is an
// error envelope" — are true of every *other* route, and an exemption that is
// written down once is one a reviewer can audit.
var keylessV1Routes = map[string]bool{
	"GET /v1/openapi.json": true,
}

// TestKeylessV1RoutesAreReal keeps that exemption from rotting into a list of
// paths that no longer exist, exactly as TestUnscopedV1RoutesAreReal does for
// the scope exemption.
func TestKeylessV1RoutesAreReal(t *testing.T) {
	registered := map[string]bool{}
	for _, key := range v1Routes(t) {
		registered[key] = true
	}
	for key := range keylessV1Routes {
		if !registered[key] {
			t.Errorf("%s is exempted from key authentication but is not a route", key)
		}
	}
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
		if keylessV1Routes[key] {
			// Nothing to refuse: this route reads no credential, so a dashboard
			// token is not accepted here so much as ignored.
			continue
		}
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
	// An origin the test router actually allowlists. It used to be a made-up
	// one, because Allow-Credentials was sent unconditionally and Allow-Origin
	// only for a match — T-H3 pairs the two, so the assertion now has to use an
	// origin that would really be allowed. (This router's list is the literal
	// string "*", which this middleware matches literally and not as a
	// wildcard.)
	apiReq.Header.Set("Origin", "*")
	apiW := httptest.NewRecorder()
	r.ServeHTTP(apiW, apiReq)
	if apiW.Header().Get("Access-Control-Allow-Origin") == "" ||
		apiW.Header().Get("Access-Control-Allow-Credentials") == "" {
		t.Error("/api lost its CORS headers")
	}
}

// TestV1KillSwitchCoversEveryRoute is the operator's half of the contract: one
// environment variable takes the public surface off the air without touching
// the dashboard. It runs above authentication, so an integrator can ask "is it
// up?" and get the answer.
func TestV1KillSwitchCoversEveryRoute(t *testing.T) {
	disabled := routerWith(t, func(cfg *config.Config) { cfg.APIV1Enabled = false })

	for _, key := range v1Routes(t) {
		method, path, _ := strings.Cut(key, " ")
		t.Run(key, func(t *testing.T) {
			req, err := http.NewRequest(method, concreteURL(path), nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			if code := statusOf(disabled, req); code != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want 503 with API_V1_ENABLED=false", code)
			}
		})
	}

	// A 503 is a support conversation waiting to happen, so it carries an id
	// like every other response. RequestID sits above the switch for exactly
	// this; the live gate is what found it sitting below.
	req, err := http.NewRequest(http.MethodGet, "/v1/me", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	w := httptest.NewRecorder()
	disabled.ServeHTTP(w, req)
	if w.Header().Get("X-Request-Id") == "" {
		t.Error("no X-Request-Id on the kill switch's 503")
	}

	// And the dashboard is untouched, or the switch is an outage rather than
	// a kill switch.
	dashboardReq, err := http.NewRequest(http.MethodGet, "/api/users/me", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if code := statusOf(disabled, dashboardReq); code == http.StatusServiceUnavailable {
		t.Error("/api answered 503 — the /v1 kill switch reached the dashboard")
	}
}

// TestV1AlwaysAnswersWithARequestID pins the support path: every response
// carries an id, including the 401 an integrator with a bad key gets, which
// is the response they are most likely to be asking about.
func TestV1AlwaysAnswersWithARequestID(t *testing.T) {
	r := realRouter(t)

	for _, key := range v1Routes(t) {
		method, path, _ := strings.Cut(key, " ")
		t.Run(key, func(t *testing.T) {
			req, err := http.NewRequest(method, concreteURL(path), nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Header().Get("X-Request-Id") == "" {
				t.Fatalf("no X-Request-Id on a %d response", w.Code)
			}
			if keylessV1Routes[key] {
				// This one succeeds without a credential, so it answers with the
				// contract rather than with an envelope. The id above is the
				// part of this test that applies to it.
				return
			}
			var body apierr.Body
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("response is not the /v1 envelope: %q", w.Body.String())
			}
			// The id in the body and the id in the header have to be the
			// same string, or a caller pasting one of them into a support
			// message hands us the wrong one.
			if body.Error.RequestID != w.Header().Get("X-Request-Id") {
				t.Errorf("envelope request_id %q, header %q",
					body.Error.RequestID, w.Header().Get("X-Request-Id"))
			}
		})
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
