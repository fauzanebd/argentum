package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	// Routes that a member is allowed to reach hit a nil service and panic
	// into gin.Recovery(). That is the expected shape of this test — no
	// database is wired — but the stack traces bury the assertions, so the
	// engine's writers go nowhere.
	gin.DefaultWriter = io.Discard
	gin.DefaultErrorWriter = io.Discard
	os.Exit(m.Run())
}

// realRouter builds the production router with every service present but
// unwired. Nothing here reaches a database: the point is the route table and
// the middleware chain, and those are assembled from the same code path the
// binary runs. Services must be non-nil or the optional handler groups
// (dashboards, scheduled tasks, Discord, Lark) never register and the test
// would silently stop covering them.
func realRouter(t *testing.T) *gin.Engine {
	t.Helper()
	signer, err := auth.NewTokenSigner("0123456789abcdef0123456789abcdef", 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}
	return newRouter(&apiDeps{
		cfg: &config.Config{
			Env:         "test",
			CORSOrigins: []string{"*"},
			// Non-empty so the Metabase proxy route registers; the router
			// skips it otherwise and TestUnpolicedPathsAreReal would be
			// checking an exemption that is not in play.
			MetabaseURL: "http://metabase.invalid",
		},
		signer: signer,

		authSvc:      app.NewAuthService(nil, nil, signer),
		teamSvc:      app.NewTeamService(nil, nil),
		apiKeySvc:    app.NewAPIKeyService(nil),
		companySvc:   &app.CompanyService{},
		usageSvc:     &app.UsageService{},
		dashboardSvc: app.NewDashboardService(nil, nil),
		scheduledSvc: app.NewScheduledTaskService(nil, nil, nil, nil),
		discordSvc:   app.NewDiscordService(nil, nil, nil, nil),
		larkSvc:      app.NewLarkService(nil, nil, nil),
	})
}

// TestEveryAuthedRouteIsClassified is the reason the policy is a table rather
// than an AdminOnly() call sprinkled through each handler's Register: gin's
// RouteInfo exposes the final handler, not the chain, so no test can read
// per-route gating back out of a built router. With the decision in a map, the
// router's own route list can be diffed against it — in both directions.
//
// Adding a route without an access decision fails here. So does deleting or
// renaming one and leaving its entry behind, which is what stops the policy
// from rotting into a list of paths that no longer exist.
func TestEveryAuthedRouteIsClassified(t *testing.T) {
	r := realRouter(t)

	seen := map[string]bool{}
	for _, ri := range r.Routes() {
		if unpolicedPaths[ri.Path] {
			continue
		}
		key := middleware.RouteKey(ri.Method, ri.Path)
		seen[key] = true
		role, ok := apiPolicy[key]
		if !ok {
			t.Errorf("route %s has no entry in apiPolicy — decide whether it is admin or member", key)
			continue
		}
		if !role.Valid() {
			t.Errorf("route %s is classified %q, which is not a role", key, role)
		}
	}

	var stale []string
	for key := range apiPolicy {
		if !seen[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	for _, key := range stale {
		t.Errorf("apiPolicy lists %s, which the router does not register", key)
	}
}

// TestUnpolicedPathsAreReal keeps the escape hatch honest: an entry that no
// longer matches a route is an exemption nobody is watching, and the next
// route to reuse that path would inherit it.
func TestUnpolicedPathsAreReal(t *testing.T) {
	r := realRouter(t)
	registered := map[string]bool{}
	for _, ri := range r.Routes() {
		registered[ri.Path] = true
	}
	for path := range unpolicedPaths {
		if !registered[path] {
			t.Errorf("unpolicedPaths exempts %s, which the router does not register", path)
		}
	}
}

// TestTicketGatedRoutesAreAdmin pins T-04 step 1 literally. The policy is
// wider than this list on purpose (POST /api/connections and the two /test
// routes are gated too); this test asserts the ticket's own enumeration is a
// subset, so a later loosening cannot quietly drop one of the nine findings
// S-1 and S-2 named.
func TestTicketGatedRoutesAreAdmin(t *testing.T) {
	ticket := []string{
		"PUT /api/connections/:id/dsn",
		"DELETE /api/connections/:id",
		"PUT /api/settings",
		"POST /api/phones",
		"DELETE /api/phones/:phone",
		"PUT /api/discord",
		"DELETE /api/discord",
		"POST /api/discord/users",
		"DELETE /api/discord/users/:id",
		"PUT /api/lark",
		"DELETE /api/lark",
		"POST /api/lark/users",
		"DELETE /api/lark/users/:id",
		"DELETE /api/scheduled-tasks/:id",
		"GET /api/users",
		"POST /api/users/invite",
		"PATCH /api/users/:id",
		"DELETE /api/users/:id",
	}
	for _, key := range ticket {
		if got := apiPolicy[key]; got != domain.RoleAdmin {
			t.Errorf("%s is %q, want admin", key, got)
		}
	}
}

// TestGatedRoutesRejectMembers is the ticket's gate, run against the real
// router: every admin route × {admin, member}. A member must get 403 on all of
// them, and an admin must not — "not 403" rather than "200" because these
// handlers have no database behind them here, so reaching one is the signal.
func TestGatedRoutesRejectMembers(t *testing.T) {
	r := realRouter(t)
	signer, err := auth.NewTokenSigner("0123456789abcdef0123456789abcdef", 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}

	var keys []string
	for key, role := range apiPolicy {
		if role == domain.RoleAdmin {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		t.Fatal("apiPolicy gates nothing")
	}

	for _, key := range keys {
		method, path, _ := strings.Cut(key, " ")
		t.Run(key, func(t *testing.T) {
			for _, role := range []string{"member", "admin"} {
				token, err := signer.IssueAccessToken("user-1", "co-1", role)
				if err != nil {
					t.Fatalf("IssueAccessToken: %v", err)
				}
				req, err := http.NewRequest(method, concreteURL(path), nil)
				if err != nil {
					t.Fatalf("NewRequest: %v", err)
				}
				req.Header.Set("Authorization", "Bearer "+token)

				code := statusOf(r, req)
				if role == "member" && code != http.StatusForbidden {
					t.Errorf("member got %d, want 403", code)
				}
				if role == "admin" && code == http.StatusForbidden {
					t.Errorf("admin got 403 on a route they are allowed to call")
				}
			}
		})
	}
}

// TestMemberRoutesAdmitMembers is the other half: a role gate that denied
// everything would pass the test above. Members must actually reach the
// product.
func TestMemberRoutesAdmitMembers(t *testing.T) {
	r := realRouter(t)
	signer, err := auth.NewTokenSigner("0123456789abcdef0123456789abcdef", 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}
	token, err := signer.IssueAccessToken("user-1", "co-1", "member")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	var keys []string
	for key, role := range apiPolicy {
		if role == domain.RoleMember {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	for _, key := range keys {
		method, path, _ := strings.Cut(key, " ")
		t.Run(key, func(t *testing.T) {
			req, err := http.NewRequest(method, concreteURL(path), nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			req.Header.Set("Authorization", "Bearer "+token)
			if code := statusOf(r, req); code == http.StatusForbidden {
				t.Errorf("member got 403 on a member route")
			}
		})
	}
}

// TestUnknownAuthedRouteIsDenied covers the fail-closed branch directly: a
// route inside the policed group with no policy entry must be refused. The
// classification test above should mean this never happens in the real router,
// so this is what proves the runtime behaviour if it ever does.
func TestUnknownAuthedRouteIsDenied(t *testing.T) {
	signer, err := auth.NewTokenSigner("0123456789abcdef0123456789abcdef", 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}
	reached := false
	r := gin.New()
	g := r.Group("/api")
	g.Use(middleware.Auth(signer), middleware.RequireRole(apiPolicy))
	g.GET("/brand-new-feature", func(c *gin.Context) {
		reached = true
		c.Status(http.StatusOK)
	})

	token, err := signer.IssueAccessToken("user-1", "co-1", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, "/api/brand-new-feature", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	if code := statusOf(r, req); code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — an unclassified route must fail closed", code)
	}
	if reached {
		t.Error("the handler ran on a route with no policy entry")
	}
}

// statusOf serves one request and returns its status. newRouter installs
// gin.Recovery(), so a handler that dereferences one of the nil services above
// answers 500 — distinct from the 403 every assertion here is about.
func statusOf(r *gin.Engine, req *http.Request) int {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

// concreteURL substitutes a value for every :param and *catchall so a
// registered pattern can be requested. The value never matters — every route
// under test aborts in middleware or fails on a nil service long before the id
// is read.
func concreteURL(pattern string) string {
	segs := strings.Split(pattern, "/")
	for i, s := range segs {
		if strings.HasPrefix(s, ":") || strings.HasPrefix(s, "*") {
			segs[i] = "x"
		}
	}
	return strings.Join(segs, "/")
}
