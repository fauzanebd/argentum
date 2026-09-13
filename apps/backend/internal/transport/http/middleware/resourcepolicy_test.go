package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/domain"
)

// resourceLoader is a grant store behind the real authz.Authorizer rather than a
// fake authorizer, so these tests exercise the rule the product runs instead of
// a second copy of it. It counts loads, because "never asked" is how the order
// of the chain shows up in a test.
type resourceLoader struct {
	// modes: "company/kind/id" → mode
	modes map[string]domain.AccessMode
	// grants: "company/user/kind/id"
	grants map[string]bool
	err    error
	calls  int
	// lastIDs is what the last load was asked about, so a test can prove the id
	// came from the parameter the policy names.
	lastIDs []string
}

func (l *resourceLoader) LoadAccess(_ context.Context, companyID, userID string, kind domain.ResourceKind, ids []string) (map[string]domain.ResourceAccess, error) {
	l.calls++
	l.lastIDs = ids
	if l.err != nil {
		return nil, l.err
	}
	out := map[string]domain.ResourceAccess{}
	for _, id := range ids {
		mode, ok := l.modes[companyID+"/"+string(kind)+"/"+id]
		if !ok {
			continue
		}
		out[id] = domain.ResourceAccess{Mode: mode, Granted: l.grants[companyID+"/"+userID+"/"+string(kind)+"/"+id]}
	}
	return out, nil
}

// resourceChain builds these routes behind Auth → RequireRole →
// RequireCapability → RequireResource, the order cmd/api wires them in:
//
//	GET    /dashboards/:id           member, dashboard by :id
//	DELETE /dashboards/:id           admin,  dashboard by :id
//	POST   /agents/:agentID/speak    member, gated on voice, agent by :agentID
//	GET    /plain/:id                member, no resource
//	GET    /misdeclared/:id          member, "dashboard by :dashboardID" — a
//	                                 parameter the route does not have
func resourceChain(t *testing.T, loader authz.Loader, checker CapabilityChecker) (*gin.Engine, map[string]bool) {
	t.Helper()
	roles := RolePolicy{
		"GET /dashboards/:id":         domain.RoleMember,
		"DELETE /dashboards/:id":      domain.RoleAdmin,
		"POST /agents/:agentID/speak": domain.RoleMember,
		"GET /plain/:id":              domain.RoleMember,
		"GET /misdeclared/:id":        domain.RoleMember,
	}
	caps := CapabilityPolicy{"POST /agents/:agentID/speak": domain.CapabilityVoice}
	resources := ResourcePolicy{
		"GET /dashboards/:id":         {Kind: domain.ResourceKindDashboard, Param: "id"},
		"DELETE /dashboards/:id":      {Kind: domain.ResourceKindDashboard, Param: "id"},
		"POST /agents/:agentID/speak": {Kind: domain.ResourceKindAgent, Param: "agentID"},
		"GET /misdeclared/:id":        {Kind: domain.ResourceKindDashboard, Param: "dashboardID"},
	}
	var authorizer ResourceAuthorizer
	if loader != nil {
		authorizer = authz.New(loader)
	}
	reached := map[string]bool{}
	r := gin.New()
	g := r.Group("")
	g.Use(Auth(newSigner(t)), RequireRole(roles), RequireCapability(caps, checker), RequireResource(resources, authorizer))
	for key := range roles {
		method, path, _ := strings.Cut(key, " ")
		g.Handle(method, path, func(c *gin.Context) {
			reached[key] = true
			// Stands in for a handler's own company-scoped lookup, so the
			// not-found pass-through can be told apart from an allow.
			if c.Param("id") == "missing" || c.Param("agentID") == "missing" {
				c.JSON(http.StatusNotFound, gin.H{"error": "dashboard not found"})
				return
			}
			c.Status(http.StatusOK)
		})
	}
	return r, reached
}

func newResourceLoader() *resourceLoader {
	return &resourceLoader{
		modes: map[string]domain.AccessMode{
			"co-1/dashboard/open-1":  domain.AccessModeOpen,
			"co-1/dashboard/payroll": domain.AccessModeRestricted,
			"co-1/agent/hr":          domain.AccessModeRestricted,
			// The same id in another company, restricted there and granted
			// there: nothing about it reaches a co-1 caller.
			"co-2/dashboard/theirs": domain.AccessModeRestricted,
		},
		grants: map[string]bool{},
	}
}

func TestRequireResource(t *testing.T) {
	cases := []struct {
		name       string
		method     string
		path       string
		role       string
		grant      string // "kind/id" granted to user-1 in co-1, if any
		wantStatus int
		wantLoads  int
	}{
		{"an open dashboard admits a member with no grant", http.MethodGet, "/dashboards/open-1", "member", "", http.StatusOK, 1},
		{"a restricted dashboard refuses a member with no grant", http.MethodGet, "/dashboards/payroll", "member", "", http.StatusForbidden, 1},
		// Decision 4, through the real chain: rank is not a grant.
		{"a restricted dashboard refuses an admin with no grant", http.MethodGet, "/dashboards/payroll", "admin", "", http.StatusForbidden, 1},
		{"a restricted dashboard admits a member holding the grant", http.MethodGet, "/dashboards/payroll", "member", "dashboard/payroll", http.StatusOK, 1},
		{"an admin route refuses an ungranted admin too", http.MethodDelete, "/dashboards/payroll", "admin", "", http.StatusForbidden, 1},
		// Not found passes through to the handler, whose own 404 stands.
		{"a missing dashboard reaches the handler's not-found", http.MethodGet, "/dashboards/missing", "member", "", http.StatusNotFound, 1},
		{"another company's dashboard is not found, not refused", http.MethodGet, "/dashboards/theirs", "member", "", http.StatusOK, 1},
		{"a route with no resource never asks", http.MethodGet, "/plain/payroll", "member", "", http.StatusOK, 0},
		// The order, each link proven by the one behind it never being read.
		{"the role table refuses before a grant is read", http.MethodDelete, "/dashboards/payroll", "member", "dashboard/payroll", http.StatusForbidden, 0},
		{"a missing capability refuses before a grant is read", http.MethodPost, "/agents/hr/speak", "member", "agent/hr", http.StatusForbidden, 0},
		// The one-line fail-closed branch for a policy entry that names a
		// parameter its route does not declare.
		{"a misdeclared parameter refuses and asks nothing", http.MethodGet, "/misdeclared/payroll", "admin", "dashboard/payroll", http.StatusForbidden, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loader := newResourceLoader()
			if tc.grant != "" {
				loader.grants["co-1/user-1/"+tc.grant] = true
				// The same person holds the same grant in the other company.
				// It must change nothing for a co-1 request.
				loader.grants["co-2/user-1/dashboard/theirs"] = true
			}
			r, reached := resourceChain(t, loader, &fakeChecker{held: map[string]bool{}})

			w := serveAs(t, r, tc.method, tc.path, "user-1", tc.role)
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.wantStatus, w.Body.String())
			}
			if loader.calls != tc.wantLoads {
				t.Errorf("grant store read %d times, want %d", loader.calls, tc.wantLoads)
			}
			wantReached := tc.wantStatus == http.StatusOK || tc.wantStatus == http.StatusNotFound
			key := routeKeyFor(tc.method, tc.path)
			if reached[key] != wantReached {
				t.Errorf("handler reached = %v, want %v", reached[key], wantReached)
			}
		})
	}
}

// With the capability held, the request gets past RequireCapability and the
// agent's grant is the only thing left — so the resource check really is the
// link after it, reading the id from the parameter the policy names.
func TestRequireResourceRunsAfterACapabilityIsHeld(t *testing.T) {
	loader := newResourceLoader()
	checker := &fakeChecker{held: map[string]bool{"co-1/user-1/voice": true}}
	r, _ := resourceChain(t, loader, checker)

	if w := serveAs(t, r, http.MethodPost, "/agents/hr/speak", "user-1", "member"); w.Code != http.StatusForbidden {
		t.Fatalf("voice held, agent not granted: status = %d, want 403", w.Code)
	}
	if loader.calls != 1 || len(loader.lastIDs) != 1 || loader.lastIDs[0] != "hr" {
		t.Fatalf("grant store read %d times with %v, want once with [hr]", loader.calls, loader.lastIDs)
	}
	loader.grants["co-1/user-1/agent/hr"] = true
	if w := serveAs(t, r, http.MethodPost, "/agents/hr/speak", "user-1", "member"); w.Code != http.StatusOK {
		t.Fatalf("voice held and agent granted: status = %d, want 200", w.Code)
	}
}

// The refusal names the kind. A body that said only "forbidden" would be
// indistinguishable from the role table's refusal, and T-Z7 needs to say which
// grant to ask for.
func TestRequireResourceRefusalNamesTheKind(t *testing.T) {
	r, _ := resourceChain(t, newResourceLoader(), &fakeChecker{held: map[string]bool{}})
	w := serveAs(t, r, http.MethodGet, "/dashboards/payroll", "user-1", "member")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"resource_kind":"dashboard"`) {
		t.Errorf("refusal body %s does not name the resource kind", w.Body.String())
	}
}

// A grant store that cannot be read refuses — as a 503 to retry, and never as
// an allow, including on a resource that is in fact open.
func TestRequireResourceFailsClosedWhenTheCheckFails(t *testing.T) {
	loader := newResourceLoader()
	loader.err = errors.New("connection refused")
	r, reached := resourceChain(t, loader, &fakeChecker{held: map[string]bool{}})
	w := serveAs(t, r, http.MethodGet, "/dashboards/open-1", "user-1", "admin")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", w.Code, w.Body.String())
	}
	if reached["GET /dashboards/:id"] {
		t.Error("the handler ran although the access check failed")
	}
}

// A wiring that never built the authorizer must not open every listed route.
func TestRequireResourceRefusesWithNoAuthorizer(t *testing.T) {
	r, reached := resourceChain(t, nil, &fakeChecker{held: map[string]bool{}})
	if w := serveAs(t, r, http.MethodGet, "/dashboards/open-1", "user-1", "admin"); w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if reached["GET /dashboards/:id"] {
		t.Error("the handler ran with no authorizer wired")
	}
	// A route outside the policy is unaffected by the missing authorizer.
	if w := serveAs(t, r, http.MethodGet, "/plain/x", "user-1", "member"); w.Code != http.StatusOK {
		t.Errorf("unlisted route: status = %d, want 200", w.Code)
	}
}

// Wired without Auth in front, a listed route refuses and the store is not asked
// about an empty identity.
func TestRequireResourceRefusesWhenAuthDidNotRun(t *testing.T) {
	loader := newResourceLoader()
	reached := false
	r := gin.New()
	policy := ResourcePolicy{"GET /dashboards/:id": {Kind: domain.ResourceKindDashboard, Param: "id"}}
	r.GET("/dashboards/:id", RequireResource(policy, authz.New(loader)), func(c *gin.Context) {
		reached = true
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/dashboards/open-1", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if reached {
		t.Error("the handler ran with no identity on the context")
	}
	if loader.calls != 0 {
		t.Errorf("grant store read %d times for an empty identity, want 0", loader.calls)
	}
}

// routeKeyFor maps a concrete test path back to the pattern it was registered
// under, so a case can look up whether its handler ran.
func routeKeyFor(method, path string) string {
	switch {
	case strings.HasPrefix(path, "/dashboards/"):
		return method + " /dashboards/:id"
	case strings.HasPrefix(path, "/agents/"):
		return method + " /agents/:agentID/speak"
	case strings.HasPrefix(path, "/plain/"):
		return method + " /plain/:id"
	default:
		return method + " /misdeclared/:id"
	}
}
