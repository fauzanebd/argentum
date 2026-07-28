package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

func newSigner(t *testing.T) *auth.TokenSigner {
	t.Helper()
	s, err := auth.NewTokenSigner(testSecret, 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}
	return s
}

// protectedRouter builds a router whose single route records what the
// middleware chain put on the context, so a test can assert both the status
// and the tenant identity that reached the handler.
func protectedRouter(t *testing.T, signer *auth.TokenSigner, extra ...gin.HandlerFunc) (*gin.Engine, *seen) {
	t.Helper()
	got := &seen{}
	r := gin.New()
	chain := append([]gin.HandlerFunc{Auth(signer)}, extra...)
	chain = append(chain, func(c *gin.Context) {
		got.reached = true
		got.ginUserID, _ = c.Get("user_id")
		got.ginCompanyID, _ = c.Get("company_id")
		got.ginRole, _ = c.Get("role")
		got.ctxUserID = tenantctx.UserID(c.Request.Context())
		got.ctxCompanyID = tenantctx.CompanyID(c.Request.Context())
		c.Status(http.StatusOK)
	})
	r.GET("/protected", chain...)
	return r, got
}

type seen struct {
	reached      bool
	ginUserID    any
	ginCompanyID any
	ginRole      any
	ctxUserID    string
	ctxCompanyID string
}

func do(r *gin.Engine, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAuthAcceptsAValidAccessToken(t *testing.T) {
	signer := newSigner(t)
	r, got := protectedRouter(t, signer)

	raw, err := signer.IssueAccessToken("user-1", "co-1", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+raw)

	if w := do(r, req); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if !got.reached {
		t.Fatal("the handler was not reached")
	}
	if got.ginUserID != "user-1" || got.ginCompanyID != "co-1" || got.ginRole != "admin" {
		t.Errorf("gin context = (%v, %v, %v), want (user-1, co-1, admin)", got.ginUserID, got.ginCompanyID, got.ginRole)
	}
	// The request context is the half that matters downstream: every tool
	// resolves its tenant connection from tenantctx, not from gin.
	if got.ctxUserID != "user-1" || got.ctxCompanyID != "co-1" {
		t.Errorf("tenantctx = (%q, %q), want (user-1, co-1)", got.ctxUserID, got.ctxCompanyID)
	}
}

// A refresh token is long-lived and stored in a cookie; if it authenticated an
// API route, a stolen cookie would be a week-long session rather than a
// 15-minute one. The claim carries `typ` for exactly this check.
func TestAuthRejectsARefreshTokenOnAnAccessRoute(t *testing.T) {
	signer := newSigner(t)
	r, got := protectedRouter(t, signer)

	raw, err := signer.IssueRefreshToken("user-1", "co-1", "admin")
	if err != nil {
		t.Fatalf("IssueRefreshToken: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+raw)

	w := do(r, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if got.reached {
		t.Error("the handler ran with a refresh token")
	}
	if body := w.Body.String(); body == "" {
		t.Error("no error body")
	}
}

func TestAuthRejectsBadTokens(t *testing.T) {
	signer := newSigner(t)
	other, err := auth.NewTokenSigner("ffffffffffffffffffffffffffffffff", time.Minute, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}
	foreign, err := other.IssueAccessToken("attacker", "co-2", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	cases := []struct {
		name   string
		header string
	}{
		{"no header", ""},
		{"bearer with nothing after it", "Bearer "},
		{"not a bearer scheme", "Basic dXNlcjpwYXNz"},
		// Scheme matching is prefix-based and case-sensitive; a lowercase
		// "bearer" falls through to the query/cookie path and ends up as a
		// missing token, not as an accepted one.
		{"lowercase scheme", "bearer sometoken"},
		{"garbage token", "Bearer not-a-jwt"},
		{"foreign signature", "Bearer " + foreign},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, got := protectedRouter(t, signer)
			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			if w := do(r, req); w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", w.Code)
			}
			if got.reached {
				t.Error("the handler ran")
			}
		})
	}
}

// The WebSocket upgrade cannot set an Authorization header, so the token
// arrives as ?at=. That path has to be exactly as strict as the header one —
// it is the same credential in a place that ends up in access logs.
func TestExtractTokenFallbackOrder(t *testing.T) {
	signer := newSigner(t)
	access, err := signer.IssueAccessToken("user-1", "co-1", "member")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	refresh, err := signer.IssueRefreshToken("user-1", "co-1", "member")
	if err != nil {
		t.Fatalf("IssueRefreshToken: %v", err)
	}

	t.Run("query parameter is accepted", func(t *testing.T) {
		r, got := protectedRouter(t, signer)
		req := httptest.NewRequest(http.MethodGet, "/protected?at="+access, nil)
		if w := do(r, req); w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if got.ctxCompanyID != "co-1" {
			t.Errorf("company = %q, want co-1", got.ctxCompanyID)
		}
	})

	t.Run("query parameter is not a way around the type check", func(t *testing.T) {
		r, _ := protectedRouter(t, signer)
		req := httptest.NewRequest(http.MethodGet, "/protected?at="+refresh, nil)
		if w := do(r, req); w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
	})

	t.Run("cookie is accepted", func(t *testing.T) {
		r, _ := protectedRouter(t, signer)
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.AddCookie(&http.Cookie{Name: "at", Value: access})
		if w := do(r, req); w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
	})

	t.Run("the header wins over the query parameter", func(t *testing.T) {
		r, got := protectedRouter(t, signer)
		req := httptest.NewRequest(http.MethodGet, "/protected?at="+refresh, nil)
		req.Header.Set("Authorization", "Bearer "+access)
		if w := do(r, req); w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if !got.reached {
			t.Error("the handler was not reached")
		}
	})

	t.Run("the query parameter wins over the cookie", func(t *testing.T) {
		r, _ := protectedRouter(t, signer)
		req := httptest.NewRequest(http.MethodGet, "/protected?at="+access, nil)
		req.AddCookie(&http.Cookie{Name: "at", Value: refresh})
		if w := do(r, req); w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
	})
}

// AdminOnly is no longer how the API gates anything — T-04 moved that to
// RequireRole and a policy table, because per-route middleware cannot be read
// back out of a built router by any test. AdminOnly stays for one-off routes
// registered outside a policed group, so its behaviour stays pinned here.
func TestAdminOnly(t *testing.T) {
	signer := newSigner(t)

	cases := []struct {
		role       string
		wantStatus int
	}{
		{"admin", http.StatusOK},
		{"member", http.StatusForbidden},
		{"owner", http.StatusForbidden},
		{"", http.StatusForbidden},
		{"Admin", http.StatusForbidden}, // exact match, not case-folded
	}
	for _, tc := range cases {
		name := tc.role
		if name == "" {
			name = "(empty)"
		}
		t.Run(name, func(t *testing.T) {
			r, got := protectedRouter(t, signer, AdminOnly())
			raw, err := signer.IssueAccessToken("user-1", "co-1", tc.role)
			if err != nil {
				t.Fatalf("IssueAccessToken: %v", err)
			}
			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			req.Header.Set("Authorization", "Bearer "+raw)

			if w := do(r, req); w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tc.wantStatus)
			}
			if got.reached != (tc.wantStatus == http.StatusOK) {
				t.Errorf("handler reached = %v, want %v", got.reached, tc.wantStatus == http.StatusOK)
			}
		})
	}
}

// AdminOnly reads the role off the gin context, which only Auth sets. Applied
// without Auth in front of it, it must deny rather than admit — the failure
// mode of a misordered chain has to be closed.
func TestAdminOnlyDeniesWhenAuthDidNotRun(t *testing.T) {
	reached := false
	r := gin.New()
	r.GET("/admin", AdminOnly(), func(c *gin.Context) {
		reached = true
		c.Status(http.StatusOK)
	})

	w := do(r, httptest.NewRequest(http.MethodGet, "/admin", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if reached {
		t.Error("the handler ran with no role on the context")
	}
}
