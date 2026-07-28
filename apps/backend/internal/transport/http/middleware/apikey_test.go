package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
	"github.com/fauzanebd/argentum/internal/transport/http/apierr"
)

// fakeAuthenticator resolves exactly one token. Everything else is refused
// the way the real service refuses it — one error for every kind of broken
// credential.
type fakeAuthenticator struct {
	token string
	key   *domain.APIKey
	err   error // when set, returned for the matching token too
}

func (f *fakeAuthenticator) Authenticate(_ context.Context, token string) (*domain.APIKey, error) {
	if f.err != nil {
		return nil, f.err
	}
	if token != f.token {
		return nil, domain.ErrUnauthorized
	}
	return f.key, nil
}

func keyWith(scopes ...domain.Scope) *domain.APIKey {
	return &domain.APIKey{
		ID:        "key-1",
		CompanyID: "co-1",
		Name:      "CI",
		Scopes:    scopes,
	}
}

// scopedRouter builds a router with one route per scope in the vocabulary,
// each gated on its own scope, plus an ungated route behind the same auth.
func scopedRouter(a APIKeyAuthenticator) *gin.Engine {
	r := gin.New()
	v1 := r.Group("/v1")
	v1.Use(APIKeyAuth(a))
	v1.GET("/me", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	for _, s := range domain.AllScopes {
		v1.GET(probePath(s), RequireScope(s), func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})
	}
	return r
}

// probePath turns a scope into a route. The colon is stripped because gin
// reads ":" as the start of a path parameter — a detail of the test harness
// only: no real /v1 route puts a scope name in its path.
func probePath(s domain.Scope) string {
	return "/probe/" + strings.ReplaceAll(string(s), ":", "-")
}

func call(r *gin.Engine, path, authorization string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestRequireScopeMatrix is T-13's gate: every scope in the vocabulary, held
// and not held. A key reaches exactly the route its scope names and is
// refused 403 on all the others — which is what "deny by default" has to mean
// when the check is per-key rather than per-role and no table can enumerate it.
func TestRequireScopeMatrix(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, held := range domain.AllScopes {
		t.Run("key holds "+string(held), func(t *testing.T) {
			r := scopedRouter(&fakeAuthenticator{token: "arg_aaaaaaaaaa_secret", key: keyWith(held)})

			for _, want := range domain.AllScopes {
				w := call(r, "/v1"+probePath(want), "Bearer arg_aaaaaaaaaa_secret")
				switch {
				case want == held && w.Code != http.StatusOK:
					t.Errorf("%s: got %d, want 200 — the key holds this scope", want, w.Code)
				case want != held && w.Code != http.StatusForbidden:
					t.Errorf("%s: got %d, want 403 — the key does not hold this scope", want, w.Code)
				}
			}
		})
	}
}

// TestRequireScopeMultiScopeKey covers the ordinary case a matrix of
// single-scope keys cannot: a key carrying several capabilities reaches all of
// them and nothing else.
func TestRequireScopeMultiScopeKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := scopedRouter(&fakeAuthenticator{
		token: "arg_aaaaaaaaaa_secret",
		key:   keyWith(domain.ScopeReadUsage, domain.ScopeWriteChat),
	})
	const auth = "Bearer arg_aaaaaaaaaa_secret"

	for _, s := range []domain.Scope{domain.ScopeReadUsage, domain.ScopeWriteChat} {
		if w := call(r, "/v1"+probePath(s), auth); w.Code != http.StatusOK {
			t.Errorf("%s: got %d, want 200", s, w.Code)
		}
	}
	for _, s := range []domain.Scope{domain.ScopeReadAudit, domain.ScopeWriteActions} {
		if w := call(r, "/v1"+probePath(s), auth); w.Code != http.StatusForbidden {
			t.Errorf("%s: got %d, want 403", s, w.Code)
		}
	}
}

// TestScopelessKeyReachesNothingScoped is the shape of a key whose scopes were
// somehow lost: it authenticates, and every gated route refuses it.
func TestScopelessKeyReachesNothingScoped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := scopedRouter(&fakeAuthenticator{token: "arg_aaaaaaaaaa_secret", key: keyWith()})
	const auth = "Bearer arg_aaaaaaaaaa_secret"

	if w := call(r, "/v1/me", auth); w.Code != http.StatusOK {
		t.Errorf("/v1/me: got %d, want 200 — the key is valid", w.Code)
	}
	for _, s := range domain.AllScopes {
		if w := call(r, "/v1"+probePath(s), auth); w.Code != http.StatusForbidden {
			t.Errorf("%s: got %d, want 403", s, w.Code)
		}
	}
}

// TestAPIKeyAuthRejections walks the credential shapes an Authorization
// header can carry. Every one of them is a 401 carrying the same code: the
// caller learns that the credential is unusable and nothing about why.
func TestAPIKeyAuthRejections(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name          string
		authorization string
		authErr       error
		wantStatus    int
		wantType      apierr.Type
		wantCode      string
	}{
		{"no header", "", nil, http.StatusUnauthorized, apierr.TypeAuthentication, "missing_api_key"},
		{"empty bearer", "Bearer ", nil, http.StatusUnauthorized, apierr.TypeAuthentication, "missing_api_key"},
		{"wrong scheme", "Basic YWJjOjEyMw==", nil, http.StatusUnauthorized, apierr.TypeAuthentication, "missing_api_key"},
		{"a dashboard JWT", "Bearer eyJhbGciOiJIUzI1NiJ9.e30.sig", nil, http.StatusUnauthorized, apierr.TypeAuthentication, "invalid_api_key"},
		{"unknown key", "Bearer arg_bbbbbbbbbb_nope", nil, http.StatusUnauthorized, apierr.TypeAuthentication, "invalid_api_key"},
		{"revoked or expired", "Bearer arg_aaaaaaaaaa_secret", domain.ErrUnauthorized, http.StatusUnauthorized, apierr.TypeAuthentication, "invalid_api_key"},
		{"store unreachable", "Bearer arg_aaaaaaaaaa_secret", errors.New("control db is down"), http.StatusInternalServerError, apierr.TypeServer, "auth_unavailable"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := scopedRouter(&fakeAuthenticator{
				token: "arg_aaaaaaaaaa_secret",
				key:   keyWith(domain.ScopeReadUsage),
				err:   tc.authErr,
			})
			w := call(r, "/v1/me", tc.authorization)
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.wantStatus, w.Body.String())
			}
			var body apierr.Body
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("response is not the /v1 envelope: %v (%s)", err, w.Body.String())
			}
			if body.Error.Type != tc.wantType {
				t.Errorf("type = %q, want %q", body.Error.Type, tc.wantType)
			}
			if body.Error.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", body.Error.Code, tc.wantCode)
			}
			if body.Error.Message == "" {
				t.Error("the envelope carries no message")
			}
		})
	}
}

// TestAPIKeyAuthSetsAuditIdentity is what makes T-05's rows attribute a tool
// call to an integration rather than to a person who was not there. The actor
// rides the *request* context, because the thing that writes those rows is a
// tool decorator several packages away.
func TestAPIKeyAuthSetsAuditIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var gotCompany, gotKind, gotRef, gotGinCompany, gotKeyID string
	var roleSet bool

	r := gin.New()
	v1 := r.Group("/v1")
	v1.Use(APIKeyAuth(&fakeAuthenticator{token: "arg_aaaaaaaaaa_secret", key: keyWith(domain.ScopeReadUsage)}))
	v1.GET("/me", func(c *gin.Context) {
		ctx := c.Request.Context()
		gotCompany = tenantctx.CompanyID(ctx)
		gotKind, gotRef = tenantctx.Actor(ctx)
		gotGinCompany = c.GetString("company_id")
		gotKeyID = c.GetString(CtxAPIKeyID)
		_, roleSet = c.Get("role")
		c.Status(http.StatusOK)
	})

	if w := call(r, "/v1/me", "Bearer arg_aaaaaaaaaa_secret"); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if gotCompany != "co-1" || gotGinCompany != "co-1" {
		t.Errorf("company = %q / %q, want co-1 on both contexts", gotCompany, gotGinCompany)
	}
	if gotKind != string(domain.ActorKindAPIKey) {
		t.Errorf("actor kind = %q, want api_key", gotKind)
	}
	if gotRef != "key-1" {
		t.Errorf("actor ref = %q, want the key id", gotRef)
	}
	if gotKeyID != "key-1" {
		t.Errorf("api_key_id = %q, want key-1", gotKeyID)
	}
	// No role, ever. RequireRole refuses an unrecognised role, so a /v1 group
	// that accidentally picked up the dashboard's policy middleware would fail
	// closed rather than admitting a key as a member.
	if roleSet {
		t.Error("APIKeyAuth set a role; a machine credential has scopes, not a role")
	}
}

// TestRequireScopeWithoutAuthFailsClosed covers a misordered chain directly:
// RequireScope with no APIKeyAuth ahead of it must refuse, not fall through.
func TestRequireScopeWithoutAuthFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reached := false

	r := gin.New()
	r.GET("/v1/oops", RequireScope(domain.ScopeReadUsage), func(c *gin.Context) {
		reached = true
		c.Status(http.StatusOK)
	})

	if w := call(r, "/v1/oops", "Bearer arg_aaaaaaaaaa_secret"); w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if reached {
		t.Error("the handler ran without an authenticated key")
	}
}

// TestAPIKeyAuthUnconfigured is the deployment where no key service was wired:
// a 500 that says so, not a panic and not an open door.
func TestAPIKeyAuthUnconfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/v1/me", APIKeyAuth(nil), func(c *gin.Context) { c.Status(http.StatusOK) })

	w := call(r, "/v1/me", "Bearer arg_aaaaaaaaaa_secret")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

// TestAPIKeyIsHeaderOnly pins the deliberate difference from Auth: the
// dashboard accepts its token from a query parameter and a cookie because a
// browser cannot set a header on a WebSocket upgrade. Neither applies to a
// machine caller, and both are how a credential ends up in an access log.
func TestAPIKeyIsHeaderOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := scopedRouter(&fakeAuthenticator{token: "arg_aaaaaaaaaa_secret", key: keyWith(domain.ScopeReadUsage)})

	if w := call(r, "/v1/me?at=arg_aaaaaaaaaa_secret", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("query parameter: got %d, want 401", w.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.AddCookie(&http.Cookie{Name: "at", Value: "arg_aaaaaaaaaa_secret"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("cookie: got %d, want 401", w.Code)
	}
}
