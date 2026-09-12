package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/domain"
)

// fakeChecker holds grants as "company/user/capability" and counts how often it
// was asked, because "the checker was never consulted" is itself an assertion
// here: it is how the order of the chain shows up in a test.
type fakeChecker struct {
	held  map[string]bool
	err   error
	calls int
}

func (f *fakeChecker) Has(_ context.Context, companyID, userID string, c domain.Capability) (bool, error) {
	f.calls++
	if f.err != nil {
		return false, f.err
	}
	return f.held[companyID+"/"+userID+"/"+string(c)], nil
}

// capabilityChain builds the three routes this file needs behind Auth →
// RequireRole → RequireCapability, the order cmd/api wires them in:
//
//	POST /speak        member route, gated on voice
//	POST /admin-speak  admin route, gated on voice
//	GET  /plain        member route, gated on nothing
func capabilityChain(t *testing.T, checker CapabilityChecker) (*gin.Engine, map[string]bool) {
	t.Helper()
	roles := RolePolicy{
		"POST /speak":       domain.RoleMember,
		"POST /admin-speak": domain.RoleAdmin,
		"GET /plain":        domain.RoleMember,
	}
	caps := CapabilityPolicy{
		"POST /speak":       domain.CapabilityVoice,
		"POST /admin-speak": domain.CapabilityVoice,
	}
	reached := map[string]bool{}
	r := gin.New()
	g := r.Group("")
	g.Use(Auth(newSigner(t)), RequireRole(roles), RequireCapability(caps, checker))
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/speak"},
		{http.MethodPost, "/admin-speak"},
		{http.MethodGet, "/plain"},
	} {
		key := route.method + " " + route.path
		g.Handle(route.method, route.path, func(c *gin.Context) {
			reached[key] = true
			c.Status(http.StatusOK)
		})
	}
	return r, reached
}

func serveAs(t *testing.T, r *gin.Engine, method, path, userID, role string) *httptest.ResponseRecorder {
	t.Helper()
	token, err := newSigner(t).IssueAccessToken(userID, "co-1", role)
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestRequireCapability(t *testing.T) {
	cases := []struct {
		name       string
		method     string
		path       string
		role       string
		holdsVoice bool
		wantStatus int
		wantCalls  int
	}{
		{"a member without the grant is refused", http.MethodPost, "/speak", "member", false, http.StatusForbidden, 1},
		// Decision 4, and the assertion that proves it was implemented rather
		// than described: the admin's rank does not stand in for the grant.
		{"an admin without the grant is refused too", http.MethodPost, "/speak", "admin", false, http.StatusForbidden, 1},
		{"a member holding the grant reaches the route", http.MethodPost, "/speak", "member", true, http.StatusOK, 1},
		{"an admin holding the grant reaches the route", http.MethodPost, "/speak", "admin", true, http.StatusOK, 1},
		{"an ungated route never asks", http.MethodGet, "/plain", "member", false, http.StatusOK, 0},
		// A capability only adds. The member holds voice and the route wants
		// voice, but the role table refused them first — and refused them
		// without the grant store ever being read.
		{"a grant cannot admit a caller the role table refused", http.MethodPost, "/admin-speak", "member", true, http.StatusForbidden, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checker := &fakeChecker{held: map[string]bool{}}
			if tc.holdsVoice {
				checker.held["co-1/user-1/voice"] = true
			}
			r, reached := capabilityChain(t, checker)

			w := serveAs(t, r, tc.method, tc.path, "user-1", tc.role)
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.wantStatus, w.Body.String())
			}
			if got := reached[tc.method+" "+tc.path]; got != (tc.wantStatus == http.StatusOK) {
				t.Errorf("handler reached = %v, want %v", got, tc.wantStatus == http.StatusOK)
			}
			if checker.calls != tc.wantCalls {
				t.Errorf("checker consulted %d times, want %d", checker.calls, tc.wantCalls)
			}
		})
	}
}

// The refusal names the capability, so the dashboard can say which grant to ask
// for. A body that said only "forbidden" would be indistinguishable from the
// role table's refusal.
func TestRequireCapabilityRefusalNamesTheCapability(t *testing.T) {
	r, _ := capabilityChain(t, &fakeChecker{held: map[string]bool{}})
	w := serveAs(t, r, http.MethodPost, "/speak", "user-1", "member")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"capability":"voice"`) {
		t.Errorf("refusal body %s does not name the capability", w.Body.String())
	}
}

// A grant store that cannot be read refuses, and says to retry rather than
// claiming the caller lacks a grant.
func TestRequireCapabilityFailsClosedWhenTheCheckFails(t *testing.T) {
	r, reached := capabilityChain(t, &fakeChecker{err: errors.New("connection refused")})
	w := serveAs(t, r, http.MethodPost, "/speak", "user-1", "admin")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", w.Code, w.Body.String())
	}
	if reached["POST /speak"] {
		t.Error("the handler ran although the capability check failed")
	}
}

// A wiring that never built the checker must not turn every gated route open.
func TestRequireCapabilityRefusesWithNoChecker(t *testing.T) {
	r, reached := capabilityChain(t, nil)
	w := serveAs(t, r, http.MethodPost, "/speak", "user-1", "admin")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if reached["POST /speak"] {
		t.Error("the handler ran with no checker wired")
	}
}

// Wired without Auth in front — no user on the context — a gated route refuses,
// and the checker is not asked about an empty identity.
func TestRequireCapabilityRefusesWhenAuthDidNotRun(t *testing.T) {
	checker := &fakeChecker{held: map[string]bool{"//voice": true}}
	reached := false
	r := gin.New()
	r.POST("/speak", RequireCapability(CapabilityPolicy{"POST /speak": domain.CapabilityVoice}, checker), func(c *gin.Context) {
		reached = true
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/speak", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if reached {
		t.Error("the handler ran with no identity on the context")
	}
	if checker.calls != 0 {
		t.Errorf("checker consulted %d times for an empty identity, want 0", checker.calls)
	}
}
