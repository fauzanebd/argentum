package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// embedRouter builds a chain of EmbedAuth plus a recording handler.
func embedRouter(t *testing.T, signer *auth.TokenSigner) (*gin.Engine, *embedSeen) {
	t.Helper()
	got := &embedSeen{}
	r := gin.New()
	r.GET("/api/embed/probe", EmbedAuth(signer), func(c *gin.Context) {
		got.reached = true
		got.companyID = c.GetString("company_id")
		got.userRef = EmbedUserRef(c)
		got.keyID = c.GetString(CtxEmbedKeyID)
		_, got.hasUserID = c.Get("user_id")
		_, got.hasRole = c.Get("role")
		got.ctxCompanyID = tenantctx.CompanyID(c.Request.Context())
		got.actorKind, got.actorRef = tenantctx.Actor(c.Request.Context())
		c.Status(http.StatusOK)
	})
	return r, got
}

type embedSeen struct {
	reached             bool
	companyID           string
	userRef             string
	keyID               string
	hasUserID, hasRole  bool
	ctxCompanyID        string
	actorKind, actorRef string
}

func TestEmbedAuthAdmitsAndAnnotates(t *testing.T) {
	signer := newSigner(t)
	tok, err := signer.IssueEmbedToken("company-1", "emp_812", "key-1", 15*time.Minute)
	if err != nil {
		t.Fatalf("IssueEmbedToken: %v", err)
	}

	r, got := embedRouter(t, signer)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/embed/probe", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !got.reached {
		t.Fatal("handler not reached")
	}
	if got.companyID != "company-1" || got.userRef != "emp_812" || got.keyID != "key-1" {
		t.Errorf("context = %+v, want the session's identity", got)
	}
	if got.ctxCompanyID != "company-1" {
		t.Errorf("tenantctx company = %q, want company-1 — the tools read this one", got.ctxCompanyID)
	}
	if got.actorKind != string(domain.ActorKindEmbed) || got.actorRef != "emp_812" {
		t.Errorf("actor = (%q, %q), want (embed, emp_812) so T-05 attributes the turn",
			got.actorKind, got.actorRef)
	}

	// The two absences are the point of the middleware.
	if got.hasUserID {
		t.Error("EmbedAuth set user_id; a handler reading it would attribute a stranger's turn to a real account")
	}
	if got.hasRole {
		t.Error("EmbedAuth set role; RequireRole would then admit a website visitor to staff routes")
	}
}

func TestEmbedAuthAcceptsQueryToken(t *testing.T) {
	// A browser cannot set a header on a WebSocket upgrade, which is why the
	// dashboard's stream route takes `?at=`. The embed stream (T-20) needs the
	// same exemption under a different name.
	signer := newSigner(t)
	tok, err := signer.IssueEmbedToken("company-1", "emp_812", "key-1", 15*time.Minute)
	if err != nil {
		t.Fatalf("IssueEmbedToken: %v", err)
	}
	r, got := embedRouter(t, signer)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/embed/probe?et="+tok, nil))

	if w.Code != http.StatusOK || !got.reached {
		t.Fatalf("status = %d, reached = %v, want 200 and true", w.Code, got.reached)
	}
	if got.userRef != "emp_812" {
		t.Errorf("user_ref = %q", got.userRef)
	}
}

func TestEmbedAuthRefuses(t *testing.T) {
	signer := newSigner(t)
	other, err := auth.NewTokenSigner("ffffffffffffffffffffffffffffffff", 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}

	access, err := signer.IssueAccessToken("user-1", "company-1", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	refresh, err := signer.IssueRefreshToken("user-1", "company-1", "admin")
	if err != nil {
		t.Fatalf("IssueRefreshToken: %v", err)
	}
	expired, err := signer.IssueEmbedToken("company-1", "emp_812", "key-1", -time.Minute)
	if err != nil {
		t.Fatalf("IssueEmbedToken: %v", err)
	}
	foreign, err := other.IssueEmbedToken("company-1", "emp_812", "key-1", 15*time.Minute)
	if err != nil {
		t.Fatalf("IssueEmbedToken: %v", err)
	}

	cases := []struct{ name, token string }{
		{"no token", ""},
		{"garbage", "not-a-jwt"},
		// The acceptance criterion, from the other side: a staff session is not
		// an embed session, so a leaked dashboard token cannot be spent here.
		{"dashboard access token", access},
		{"dashboard refresh token", refresh},
		{"expired embed session", expired},
		{"embed token from another deployment", foreign},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, got := embedRouter(t, signer)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/embed/probe", nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			r.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", w.Code)
			}
			if got.reached {
				t.Error("the handler ran anyway")
			}
		})
	}
}

// TestEmbedTokenIsRefusedByDashboardAuth is the acceptance criterion in the
// direction that matters most: an embed session must not reach `/api/threads`
// or anything the role policy guards.
func TestEmbedTokenIsRefusedByDashboardAuth(t *testing.T) {
	signer := newSigner(t)
	tok, err := signer.IssueEmbedToken("company-1", "emp_812", "key-1", 15*time.Minute)
	if err != nil {
		t.Fatalf("IssueEmbedToken: %v", err)
	}

	// The real chain a dashboard route runs: Auth, then the role gate. The
	// route is this file's shared `/protected` fixture rather than a literal
	// `/api/threads`, because what is under test is the chain and not the path.
	r, got := protectedRouter(t, signer, RequireRole(RolePolicy{
		"GET /protected": domain.RoleAdmin,
	}))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 — an embed session reached a dashboard route", w.Code)
	}
	if got.reached {
		t.Fatal("an embed session reached a dashboard handler")
	}
}

func TestEmbedAuthWithoutAVerifierIsUnavailable(t *testing.T) {
	// A deployment with embedding unwired must answer a typed refusal rather
	// than panicking on a nil interface.
	r := gin.New()
	r.GET("/api/embed/probe", EmbedAuth(nil), func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/embed/probe", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}
