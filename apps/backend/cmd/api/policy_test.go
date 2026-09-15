package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/authz"
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
	return routerWith(t)
}

// routerWith is realRouter with the config open to a test that needs a
// different one — the `/v1` kill switch is the only setting so far whose
// whole point is what happens when it is off.
func routerWith(t *testing.T, tweaks ...func(*config.Config)) *gin.Engine {
	t.Helper()
	signer, err := auth.NewTokenSigner("0123456789abcdef0123456789abcdef", 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}
	cfg := &config.Config{
		Env:         "test",
		CORSOrigins: []string{"*"},
		// The kill switch defaults to off in a zero-value Config, and off
		// answers 503 before authentication runs — every /v1 assertion in
		// this package would then be testing the switch rather than the
		// thing it names. The switch has its own test.
		APIV1Enabled: true,
	}
	for _, tweak := range tweaks {
		tweak(cfg)
	}
	return newRouter(testDeps(cfg, signer))
}

// routerWithDeps is routerWith for a test that has to change a dependency
// rather than a setting — so far only the `/v1` authenticator, which is the
// only way to send a request carrying a credential without a database to issue
// one from.
func routerWithDeps(t *testing.T, tweak func(*apiDeps)) *gin.Engine {
	t.Helper()
	signer, err := auth.NewTokenSigner("0123456789abcdef0123456789abcdef", 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}
	d := testDeps(&config.Config{
		Env:          "test",
		CORSOrigins:  []string{"*"},
		APIV1Enabled: true,
	}, signer)
	tweak(d)
	return newRouter(d)
}

func testDeps(cfg *config.Config, signer *auth.TokenSigner) *apiDeps {
	return &apiDeps{
		cfg:    cfg,
		signer: signer,

		authSvc:      app.NewAuthService(nil, nil, signer),
		teamSvc:      app.NewTeamService(nil, nil),
		apiKeySvc:    app.NewAPIKeyService(nil),
		agentSvc:     app.NewAgentService(nil, nil, nil),
		companySvc:   &app.CompanyService{},
		usageSvc:     &app.UsageService{},
		scheduledSvc: app.NewScheduledTaskService(nil, nil, nil, nil),
		discordSvc:   app.NewDiscordService(nil, nil, nil, nil),
		larkSvc:      app.NewLarkService(nil, nil, nil),
		slackSvc:     app.NewSlackService(nil, nil, nil),
		// A grant store that has granted nothing to anybody. That makes every
		// role assertion in this file a second assertion as well (T-Z1): a route
		// that had quietly started asking for a capability would 403 the admin
		// in TestGatedRoutesRejectMembers and the member in
		// TestMemberRoutesAdmitMembers.
		capabilitySvc: app.NewCapabilityService(&noCapabilities{}, nil),
		// A grant store in which every object is restricted and nobody holds a
		// grant (T-Z3), for the same double duty: a route that had quietly
		// started asking about the object in its path would 403 both roles.
		resourceAuthz: authz.New(&restrictedEverything{}),
		// A transcriber that answers, so the voice route registers (T-W7) and
		// every classification test here sees it. Without one the route is not
		// registered at all — TestVoiceRouteIsAbsentWithoutAProvider.
		voiceSvc: app.NewVoiceService(answeringTranscriber{}, nil, nil, nil, 0, 0),
		// And a synthesiser, for the same reason (T-W8) —
		// TestSpokenAnswerRouteIsAbsentWithoutASynthesizer.
		spokenSvc: spokenService(speakingSynth{}),
	}
}

// noCapabilities is a capability store in which nobody holds anything. It
// counts reads, so a test can tell "refused for lack of a grant" apart from
// "never asked".
type noCapabilities struct{ lists atomic.Int64 }

func (n *noCapabilities) ListForUser(context.Context, string, string) ([]domain.CapabilityGrant, error) {
	n.lists.Add(1)
	return []domain.CapabilityGrant{}, nil
}

func (n *noCapabilities) Grant(context.Context, string, string, domain.Capability, string) error {
	return nil
}

func (n *noCapabilities) Revoke(context.Context, string, string, domain.Capability) error {
	return nil
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
				// A capability-gated route refuses an admin who was not granted
				// it, which is the point of it; TestCapabilityGatedRoutesRefuseAnAdminWithoutAGrant
				// owns that half.
				if _, gated := capabilityPolicy[key]; gated {
					continue
				}
				// Likewise a route that serves a restricted object: every object
				// in these deps is restricted and ungranted, and
				// TestResourceGatedRoutesRefuseAnAdminWithoutAGrant owns that half.
				if _, gated := resourcePolicy[key]; gated {
					continue
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
		// A member route behind a capability is a member route a member
		// without the grant cannot reach. That is not a role-table failure.
		if _, gated := capabilityPolicy[key]; gated {
			continue
		}
		// And a member route that serves a restricted object is one a member
		// without a grant cannot reach, which is not a role-table failure either.
		if _, gated := resourcePolicy[key]; gated {
			continue
		}
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

// TestEveryCapabilityGateIsClassified keeps capabilityPolicy a refinement of
// apiPolicy rather than a second route list. A capability narrows a route the
// role table already decided, so an entry the role table does not know is
// either stale or a route that skipped its role decision — and apiPolicy is the
// table the router is diffed against, so being in it is also being real.
func TestEveryCapabilityGateIsClassified(t *testing.T) {
	for key, capability := range capabilityPolicy {
		if _, ok := apiPolicy[key]; !ok {
			t.Errorf("capabilityPolicy gates %s, which apiPolicy does not classify", key)
		}
		if !capability.Valid() {
			t.Errorf("capabilityPolicy gates %s on %q, which is not a capability", key, capability)
		}
	}
}

// TestExistingRoutesAskForNoCapability is T-Z1's "every route that exists today
// behaves identically", held in two ways.
//
// The pinned three are the routes the day-one vocabulary tempts somebody to
// gate. 083 granted nobody anything, so gating one without a backfill locks the
// whole company out of it on the next request — the reasoning is beside
// capabilityPolicy, and this is what makes somebody read it first.
//
// The sweep is the rest: every classified route, as an admin, against a store
// that has granted nothing, must not consult it. The grant routes themselves
// are excluded because reading the store is their job.
func TestExistingRoutesAskForNoCapability(t *testing.T) {
	tempting := map[string]domain.Capability{
		"GET /api/company/data/export":  domain.CapabilityExportData,
		"POST /api/actions/:id/approve": domain.CapabilityApproveActions,
		"POST /api/actions/:id/reject":  domain.CapabilityApproveActions,
	}
	for key, capability := range tempting {
		if _, ok := apiPolicy[key]; !ok {
			t.Errorf("%s is no longer a route; update this list rather than deleting the assertion", key)
		}
		if _, gated := capabilityPolicy[key]; gated {
			t.Errorf("%s now requires %q, which 083 granted to nobody — ship the backfill that grants it to whoever does this today, then move it out of this list", key, capability)
		}
	}

	store := &noCapabilities{}
	r := routerWithDeps(t, func(d *apiDeps) { d.capabilitySvc = app.NewCapabilityService(store, nil) })
	signer, err := auth.NewTokenSigner("0123456789abcdef0123456789abcdef", 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}
	token, err := signer.IssueAccessToken("user-1", "co-1", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	for key := range apiPolicy {
		if _, gated := capabilityPolicy[key]; gated || strings.Contains(key, "/capabilities") {
			continue
		}
		method, path, _ := strings.Cut(key, " ")
		req, err := http.NewRequest(method, concreteURL(path), nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		statusOf(r, req)
	}
	if n := store.lists.Load(); n != 0 {
		t.Errorf("routes outside capabilityPolicy read the grant store %d times, want 0", n)
	}
}

// TestCapabilityGatedRoutesRefuseAnAdminWithoutAGrant is decision 4 against the
// real router: an admin with no grant is refused every capability-gated route.
// Nothing is gated yet, so this skips until voice's routes arrive (T-W7) — and
// from then on every new gate is covered without a new test.
func TestCapabilityGatedRoutesRefuseAnAdminWithoutAGrant(t *testing.T) {
	if len(capabilityPolicy) == 0 {
		t.Skip("no route asks for a capability yet; roadmap 11's T-W7 adds the first")
	}
	r := realRouter(t)
	signer, err := auth.NewTokenSigner("0123456789abcdef0123456789abcdef", 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}
	token, err := signer.IssueAccessToken("user-1", "co-1", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	for key := range capabilityPolicy {
		method, path, _ := strings.Cut(key, " ")
		req, err := http.NewRequest(method, concreteURL(path), nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if code := statusOf(r, req); code != http.StatusForbidden {
			t.Errorf("%s: admin with no grant got %d, want 403", key, code)
		}
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
