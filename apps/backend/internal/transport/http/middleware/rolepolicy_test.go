package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/domain"
)

// policed builds a router whose one route sits behind Auth + RequireRole, so a
// test can drive the pair the way the API wires them.
func policed(t *testing.T, policy RolePolicy, method, path string) (*gin.Engine, *bool) {
	t.Helper()
	reached := false
	signer := newSigner(t)
	r := gin.New()
	g := r.Group("")
	g.Use(Auth(signer), RequireRole(policy))
	g.Handle(method, path, func(c *gin.Context) {
		reached = true
		c.Status(http.StatusOK)
	})
	return r, &reached
}

func TestRequireRole(t *testing.T) {
	policy := RolePolicy{
		"GET /open":     domain.RoleMember,
		"DELETE /gated": domain.RoleAdmin,
	}

	cases := []struct {
		name       string
		method     string
		path       string
		role       string
		wantStatus int
	}{
		{"member reaches a member route", http.MethodGet, "/open", "member", http.StatusOK},
		{"admin reaches a member route", http.MethodGet, "/open", "admin", http.StatusOK},
		{"member is refused an admin route", http.MethodDelete, "/gated", "member", http.StatusForbidden},
		{"admin reaches an admin route", http.MethodDelete, "/gated", "admin", http.StatusOK},
		// A token minted with a role the product does not define — a stale
		// JWT after a rename, say — grants nothing rather than defaulting.
		{"an unknown role is refused everywhere", http.MethodGet, "/open", "owner", http.StatusForbidden},
		{"an empty role is refused everywhere", http.MethodGet, "/open", "", http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, reached := policed(t, policy, tc.method, tc.path)
			signer := newSigner(t)
			token, err := signer.IssueAccessToken("user-1", "co-1", tc.role)
			if err != nil {
				t.Fatalf("IssueAccessToken: %v", err)
			}
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.wantStatus, w.Body.String())
			}
			if *reached != (tc.wantStatus == http.StatusOK) {
				t.Errorf("handler reached = %v, want %v", *reached, tc.wantStatus == http.StatusOK)
			}
		})
	}
}

// A route the policy does not mention is denied even to an admin: the point of
// the table is that access is a decision someone made, and silence is not one.
func TestRequireRoleDeniesUnlistedRoutes(t *testing.T) {
	r, reached := policed(t, RolePolicy{}, http.MethodGet, "/unlisted")
	signer := newSigner(t)
	token, err := signer.IssueAccessToken("user-1", "co-1", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/unlisted", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if *reached {
		t.Error("the handler ran on an unlisted route")
	}
}

// RequireRole reads the role Auth puts on the context. Wired without Auth in
// front of it — a plausible mistake when adding a new group — it must deny,
// including on member routes.
func TestRequireRoleDeniesWhenAuthDidNotRun(t *testing.T) {
	reached := false
	r := gin.New()
	r.GET("/open", RequireRole(RolePolicy{"GET /open": domain.RoleMember}), func(c *gin.Context) {
		reached = true
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/open", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if reached {
		t.Error("the handler ran with no role on the context")
	}
}

// The key has to be gin's registered pattern, not the requested URL — get that
// wrong and every parameterised route falls through to the deny branch.
func TestRouteKeyUsesTheRegisteredPattern(t *testing.T) {
	policy := RolePolicy{"DELETE /things/:id": domain.RoleMember}
	r, reached := policed(t, policy, http.MethodDelete, "/things/:id")
	signer := newSigner(t)
	token, err := signer.IssueAccessToken("user-1", "co-1", "member")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/things/abc-123", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if !*reached {
		t.Error("the handler was not reached")
	}
}
