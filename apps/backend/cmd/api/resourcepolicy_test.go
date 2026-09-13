package main

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
)

// restrictedEverything is a grant store in which every object of every kind
// exists, is restricted, and is granted to nobody — the store against which a
// route that asks is refused and a route that does not ask is unaffected. It
// counts loads, so "never asked" is an assertion rather than an inference.
type restrictedEverything struct{ loads atomic.Int64 }

func (l *restrictedEverything) LoadAccess(_ context.Context, _, _ string, _ domain.ResourceKind, ids []string) (map[string]domain.ResourceAccess, error) {
	l.loads.Add(1)
	out := make(map[string]domain.ResourceAccess, len(ids))
	for _, id := range ids {
		out[id] = domain.ResourceAccess{Mode: domain.AccessModeRestricted}
	}
	return out, nil
}

// openResourceTickets are the roadmap 12 tickets resourcePending may still name.
// **Strike one when it lands**: every row still keyed to it then fails
// TestEveryResourceRouteIsClassified, which is what makes the pending table a
// list with an end rather than a second, quieter exemption list.
var openResourceTickets = map[string]bool{
	// T-Z4 struck 2026-09-12, when its six rows moved to resourceExempt.
	// T-Z5 struck 2026-09-12, when its five went to resourcePolicy and resourceExempt.
	// T-Z6 struck 2026-09-13, when its sixteen went to resourcePolicy (six) and
	// resourceExempt (ten), and resourcePending emptied.
}

// restrictableIDPlaces is how the classification test recognises a route that
// carries a restrictable id, per kind, in two ways: a parameter directly under
// the kind's collection (`/api/dashboards/:id`), or a parameter whose name says
// which kind it is wherever it sits (`/participants/:agentID`). Parameter names
// are compared lower-cased, because this router spells them both `ID` and `Id`.
//
// A kind with no row here would be invisible to the test, so
// TestEveryResourceKindHasADetector fails until a fifth kind brings its row.
//
// **What this cannot see**, and why the tables in policy.go carry the note: an
// object served under a child's id (`/api/knowledge/tables/:tableId`, a
// document's extracted table), and a kind named at request time
// (`/api/access/:kind/:id`). Neither is expressible as a (kind, param) entry.
var restrictableIDPlaces = map[domain.ResourceKind]struct {
	collections []string
	params      []string
}{
	domain.ResourceKindAgent:      {collections: []string{"/api/agents"}, params: []string{"agentid"}},
	domain.ResourceKindDashboard:  {collections: []string{"/api/dashboards"}, params: []string{"dashboardid"}},
	domain.ResourceKindConnection: {collections: []string{"/api/connections"}, params: []string{"connectionid", "sourceid"}},
	// `/api/knowledge/documents`, not `/api/documents`: the second is reports
	// this product generated, which are not a restrictable kind (T-Z2 §8).
	domain.ResourceKindDocument: {collections: []string{"/api/knowledge/documents"}, params: []string{"documentid"}},
}

type restrictableID struct {
	kind  domain.ResourceKind
	param string
}

// restrictableIDsIn lists every restrictable id a route pattern carries.
func restrictableIDsIn(path string) []restrictableID {
	segs := strings.Split(path, "/")
	var out []restrictableID
	for i, seg := range segs {
		if !strings.HasPrefix(seg, ":") {
			continue
		}
		param := seg[1:]
		parent := strings.Join(segs[:i], "/")
		for _, kind := range domain.AllResourceKinds {
			places := restrictableIDPlaces[kind]
			if slices.Contains(places.collections, parent) || slices.Contains(places.params, strings.ToLower(param)) {
				out = append(out, restrictableID{kind: kind, param: param})
				break
			}
		}
	}
	return out
}

func declaresParam(path, param string) bool {
	return slices.Contains(strings.Split(path, "/"), ":"+param)
}

// resourceTables is the three tables from policy.go, as values, so a test can
// hand the checker a copy with one defect planted in it.
type resourceTables struct {
	policy  middleware.ResourcePolicy
	exempt  map[string]string
	pending map[string]string
}

func currentResourceTables() resourceTables {
	return resourceTables{
		policy:  maps.Clone(resourcePolicy),
		exempt:  maps.Clone(resourceExempt),
		pending: maps.Clone(resourcePending),
	}
}

// resourceClassificationProblems is T-Z3's classification, as a function of the
// routes and the tables rather than of the package variables — which is what
// lets every acceptance line below be proven by planting the defect it names and
// watching this report it.
func resourceClassificationProblems(routes []string, tables resourceTables) []string {
	registered := map[string]bool{}
	for _, key := range routes {
		registered[key] = true
	}
	var problems []string
	report := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	for _, key := range slices.Sorted(maps.Keys(tables.policy)) {
		entry := tables.policy[key]
		if !registered[key] {
			report("resourcePolicy lists %s, which the router does not register", key)
			continue
		}
		_, path, _ := strings.Cut(key, " ")
		if !entry.Kind.Valid() {
			report("resourcePolicy serves %s as %q, which is not a resource kind", key, entry.Kind)
		}
		if !declaresParam(path, entry.Param) {
			report("resourcePolicy reads %s's id from :%s, which that route does not declare", key, entry.Param)
			continue
		}
		for _, found := range restrictableIDsIn(path) {
			if found.param == entry.Param && found.kind != entry.Kind {
				report("resourcePolicy calls :%s on %s a %s, but it is a %s's id", entry.Param, key, entry.Kind, found.kind)
			}
		}
	}

	for _, key := range slices.Sorted(maps.Keys(tables.exempt)) {
		if !registered[key] {
			report("resourceExempt lists %s, which the router does not register", key)
		}
		if len(strings.Fields(tables.exempt[key])) < 5 {
			report("resourceExempt lists %s without a reason — an exemption is a decision, and a decision is written down", key)
		}
	}

	for _, key := range slices.Sorted(maps.Keys(tables.pending)) {
		if !registered[key] {
			report("resourcePending lists %s, which the router does not register", key)
		}
		if ticket := tables.pending[key]; !openResourceTickets[ticket] {
			report("resourcePending leaves %s to %q, which is not an open ticket — classify it, or exempt it with a reason", key, ticket)
		}
	}

	for _, key := range routes {
		_, inPolicy := tables.policy[key]
		_, inExempt := tables.exempt[key]
		_, inPending := tables.pending[key]
		var in []string
		if inPolicy {
			in = append(in, "resourcePolicy")
		}
		if inExempt {
			in = append(in, "resourceExempt")
		}
		if inPending {
			in = append(in, "resourcePending")
		}
		if len(in) > 1 {
			report("%s is in %s — one decision per route", key, strings.Join(in, " and "))
		}
		_, path, _ := strings.Cut(key, " ")
		ids := restrictableIDsIn(path)
		if len(ids) > 0 && len(in) == 0 {
			report("route %s carries a %s's id in :%s and has no resource decision — add it to resourcePolicy, or to resourceExempt with the reason", key, ids[0].kind, ids[0].param)
		}
	}
	return problems
}

// authedRouteKeys is every route RequireResource runs in front of: the router's
// own list, less the paths that sit outside the dashboard session entirely.
func authedRouteKeys(t *testing.T) []string {
	t.Helper()
	var keys []string
	for _, ri := range realRouter(t).Routes() {
		if unpolicedPaths[ri.Path] {
			continue
		}
		keys = append(keys, middleware.RouteKey(ri.Method, ri.Path))
	}
	sort.Strings(keys)
	return keys
}

// TestEveryResourceRouteIsClassified is the new arm of
// TestEveryAuthedRouteIsClassified, kept in its own test so a failure says which
// table it is about.
func TestEveryResourceRouteIsClassified(t *testing.T) {
	routes := authedRouteKeys(t)
	for _, problem := range resourceClassificationProblems(routes, currentResourceTables()) {
		t.Error(problem)
	}
	// A detector that matched nothing would pass the loop above vacuously.
	detected := 0
	for _, key := range routes {
		_, path, _ := strings.Cut(key, " ")
		if len(restrictableIDsIn(path)) > 0 {
			detected++
		}
	}
	if detected == 0 {
		t.Fatal("no route carries a restrictable id — the detector has stopped matching the router")
	}
}

// TestResourceClassificationCatchesEachDefect is T-Z3's acceptance, one planted
// defect per line. The real tables produce no problem (the test above), so each
// problem here is attributable to the one change its case made.
func TestResourceClassificationCatchesEachDefect(t *testing.T) {
	routes := authedRouteKeys(t)
	// A route with exactly one decision, which the cases below move or double.
	// It was pending until T-Z6 put it in resourcePolicy, and before that the
	// cases used the dashboard read until T-Z5 classified it; resourcePending is
	// empty now, so there is no pending route left to point at.
	const decidedRead = "GET /api/knowledge/documents/:id"
	if _, ok := resourcePolicy[decidedRead]; !ok {
		t.Fatalf("%s is no longer in resourcePolicy; point these cases at a route that is", decidedRead)
	}

	cases := []struct {
		name   string
		plant  func(routes []string, tables *resourceTables) []string
		expect string
	}{
		{
			name: "an entry naming a route that does not exist",
			plant: func(routes []string, tables *resourceTables) []string {
				tables.policy["GET /api/dashboards/:id/nowhere"] = middleware.ResourceRoute{Kind: domain.ResourceKindDashboard, Param: "id"}
				return routes
			},
			expect: "resourcePolicy lists GET /api/dashboards/:id/nowhere, which the router does not register",
		},
		{
			name: "an entry naming a param the route does not declare",
			plant: func(routes []string, tables *resourceTables) []string {
				tables.policy[decidedRead] = middleware.ResourceRoute{Kind: domain.ResourceKindDocument, Param: "documentID"}
				return routes
			},
			expect: "from :documentID, which that route does not declare",
		},
		{
			name: "an entry calling one kind's id another kind",
			plant: func(routes []string, tables *resourceTables) []string {
				tables.policy[decidedRead] = middleware.ResourceRoute{Kind: domain.ResourceKindAgent, Param: "id"}
				return routes
			},
			expect: "a agent, but it is a document's id",
		},
		{
			name: "a new route with :id under a restrictable collection",
			plant: func(routes []string, _ *resourceTables) []string {
				return append(slices.Clone(routes), "GET /api/dashboards/:id/export")
			},
			expect: "route GET /api/dashboards/:id/export carries a dashboard's id in :id and has no resource decision",
		},
		{
			name: "a new route naming a restrictable id anywhere in its path",
			plant: func(routes []string, _ *resourceTables) []string {
				return append(slices.Clone(routes), "POST /api/threads/:id/handoff/:agentID")
			},
			expect: "route POST /api/threads/:id/handoff/:agentID carries a agent's id in :agentID",
		},
		{
			name: "an exemption without a comment",
			plant: func(routes []string, tables *resourceTables) []string {
				delete(tables.policy, decidedRead)
				tables.exempt[decidedRead] = ""
				return routes
			},
			expect: "resourceExempt lists GET /api/knowledge/documents/:id without a reason",
		},
		{
			name: "an exemption whose comment is not a reason",
			plant: func(routes []string, tables *resourceTables) []string {
				delete(tables.policy, decidedRead)
				tables.exempt[decidedRead] = "fine"
				return routes
			},
			expect: "resourceExempt lists GET /api/knowledge/documents/:id without a reason",
		},
		{
			name: "a pending row left to a ticket that already shipped",
			plant: func(routes []string, tables *resourceTables) []string {
				// T-Z6, the ticket that just shipped: striking it from
				// openResourceTickets is what this case proves took effect.
				delete(tables.policy, decidedRead)
				tables.pending[decidedRead] = "T-Z6"
				return routes
			},
			expect: `resourcePending leaves GET /api/knowledge/documents/:id to "T-Z6", which is not an open ticket`,
		},
		{
			name: "one route given two decisions",
			plant: func(routes []string, tables *resourceTables) []string {
				// Moved out of the policy so the two planted decisions are the only
				// two; the pending row also names a closed ticket, which is a second
				// problem this case does not need to be the only one reported.
				delete(tables.policy, decidedRead)
				tables.exempt[decidedRead] = "planted here to be in two tables at once"
				tables.pending[decidedRead] = "T-Z6"
				return routes
			},
			expect: "GET /api/knowledge/documents/:id is in resourceExempt and resourcePending",
		},
		{
			// A route already served through the policy that is also exempted — the
			// shape a later edit takes when somebody "fixes" a 403 by exempting it.
			name: "a gated route exempted as well",
			plant: func(routes []string, tables *resourceTables) []string {
				tables.exempt["GET /api/dashboards/:id/data"] = "planted to prove a policy entry cannot be exempted too"
				return routes
			},
			expect: "GET /api/dashboards/:id/data is in resourcePolicy and resourceExempt",
		},
		{
			name: "an exemption for a route that is gone",
			plant: func(routes []string, tables *resourceTables) []string {
				tables.exempt["DELETE /api/dashboards/:id/pins/:pinID"] = "a route that was renamed and left its exemption behind"
				return routes
			},
			expect: "resourceExempt lists DELETE /api/dashboards/:id/pins/:pinID, which the router does not register",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tables := currentResourceTables()
			planted := tc.plant(routes, &tables)
			problems := resourceClassificationProblems(planted, tables)
			for _, p := range problems {
				if strings.Contains(p, tc.expect) {
					return
				}
			}
			t.Errorf("no problem reported containing %q; got %q", tc.expect, problems)
		})
	}
}

// Adding a resource kind must make the detector look for it, or every route for
// the new kind would pass classification by being invisible.
func TestEveryResourceKindHasADetector(t *testing.T) {
	for _, kind := range domain.AllResourceKinds {
		places, ok := restrictableIDPlaces[kind]
		if !ok || (len(places.collections) == 0 && len(places.params) == 0) {
			t.Errorf("resource kind %q has no row in restrictableIDPlaces; the classification test cannot see its routes", kind)
		}
	}
}

// TestAuthedChainOrder is the acceptance line "Auth → RequireRole →
// RequireCapability → RequireResource → rate limiter", read off the chain the
// router installs. Handler names come from the runtime; each link is a closure
// its factory returns, so the factory's name is inside the closure's.
//
// The rate limiter only exists with a Redis client, so this test hands it one
// that is never dialled: go-redis connects on first use, and nothing here uses
// it.
func TestAuthedChainOrder(t *testing.T) {
	signer, err := auth.NewTokenSigner("0123456789abcdef0123456789abcdef", 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}
	d := testDeps(&config.Config{Env: "test"}, signer)
	d.rdb = redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { _ = d.rdb.Close() })

	want := []string{"Auth", "RequireRole", "RequireCapability", "RequireResource", "limitBy"}
	chain := authedChain(d)
	names := make([]string, len(chain))
	for i, h := range chain {
		names[i] = runtime.FuncForPC(reflect.ValueOf(h).Pointer()).Name()
	}
	if len(names) != len(want) {
		t.Fatalf("the authenticated chain has %d links, want %d:\n%s", len(names), len(want), strings.Join(names, "\n"))
	}
	for i, link := range want {
		if !strings.Contains(names[i], "."+link+".") {
			t.Errorf("link %d is %s, want %s", i, names[i], link)
		}
	}
}

// TestRoutesOutsideResourcePolicyReadNoGrant is "every route that exists today
// behaves identically", held the way T-Z1 held it: every classified route, as an
// admin, against a store in which everything is restricted, and the store is
// never read by a route the policy does not list. The access routes read
// through the admin's service, not the authorizer, so they need no exclusion.
func TestRoutesOutsideResourcePolicyReadNoGrant(t *testing.T) {
	store := &restrictedEverything{}
	r := routerWithDeps(t, func(d *apiDeps) { d.resourceAuthz = authz.New(store) })
	token := adminToken(t)
	for key := range apiPolicy {
		if _, listed := resourcePolicy[key]; listed {
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
	if n := store.loads.Load(); n != 0 {
		t.Errorf("routes outside resourcePolicy read the grant store %d times, want 0", n)
	}
}

// TestResourceGatedRoutesRefuseAnAdminWithoutAGrant is decision 4 against the
// real router, for every entry resourcePolicy will ever hold. It skips while the
// table is empty, like its capability sibling, and from the first entry T-Z4 or
// T-Z5 adds it covers every one without a new test.
func TestResourceGatedRoutesRefuseAnAdminWithoutAGrant(t *testing.T) {
	if len(resourcePolicy) == 0 {
		t.Skip("no route asks about a resource yet; T-Z4 → T-Z6 add the first (resourcePending)")
	}
	store := &restrictedEverything{}
	r := routerWithDeps(t, func(d *apiDeps) { d.resourceAuthz = authz.New(store) })
	token := adminToken(t)
	for key := range resourcePolicy {
		if _, gated := capabilityPolicy[key]; gated {
			// Refused one link earlier, for a different reason; that is
			// TestCapabilityGatedRoutesRefuseAnAdminWithoutAGrant's.
			continue
		}
		method, path, _ := strings.Cut(key, " ")
		req, err := http.NewRequest(method, concreteURL(path), nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if code := statusOf(r, req); code != http.StatusForbidden {
			t.Errorf("%s: admin with no grant on a restricted %s got %d, want 403", key, resourcePolicy[key].Kind, code)
		}
	}
}

func adminToken(t *testing.T) string {
	t.Helper()
	return tokenFor(t, "user-1", "admin")
}

func tokenFor(t *testing.T, userID, role string) string {
	t.Helper()
	signer, err := auth.NewTokenSigner("0123456789abcdef0123456789abcdef", 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}
	token, err := signer.IssueAccessToken(userID, "co-1", role)
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	return token
}

// dashboardGrants is one company's dashboards as 084 would answer for them:
// "payroll" restricted and granted to Rina only, "sales" open, and nothing else
// exists. It counts loads, so a refusal that never asked is visible.
type dashboardGrants struct{ loads atomic.Int64 }

func (l *dashboardGrants) LoadAccess(_ context.Context, companyID, userID string, kind domain.ResourceKind, ids []string) (map[string]domain.ResourceAccess, error) {
	l.loads.Add(1)
	out := map[string]domain.ResourceAccess{}
	if companyID != "co-1" || kind != domain.ResourceKindDashboard {
		return out, nil
	}
	for _, id := range ids {
		switch id {
		case "payroll":
			out[id] = domain.ResourceAccess{Mode: domain.AccessModeRestricted, Granted: userID == "u-rina"}
		case "sales":
			out[id] = domain.ResourceAccess{Mode: domain.AccessModeOpen}
		}
	}
	return out, nil
}

// TestADashboardGrantOpensItsReadDataAndLinks is T-Z5's first acceptance line —
// "a restricted dashboard 403s on read and on /data without a grant" — against
// the real router and policy, for a member, the granted member and an admin.
// Past the check, each route reaches its handler, which answers 503 here because
// no dashboard service is wired: that 503 is the proof the request got through.
func TestADashboardGrantOpensItsReadDataAndLinks(t *testing.T) {
	store := &dashboardGrants{}
	r := routerWithDeps(t, func(d *apiDeps) { d.resourceAuthz = authz.New(store) })

	call := func(token, method, path string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(method, path, nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code, w.Body.String()
	}

	budi, rina, dewi := tokenFor(t, "u-budi", "member"), tokenFor(t, "u-rina", "member"), tokenFor(t, "u-dewi", "admin")
	for _, path := range []string{"/api/dashboards/payroll", "/api/dashboards/payroll/data"} {
		for who, token := range map[string]string{"a member without the grant": budi, "an admin without the grant": dewi} {
			code, body := call(token, http.MethodGet, path)
			if code != http.StatusForbidden || !strings.Contains(body, `"resource_kind":"dashboard"`) {
				t.Errorf("GET %s as %s: %d %s, want a 403 naming the kind", path, who, code, body)
			}
		}
		if code, body := call(rina, http.MethodGet, path); code != http.StatusServiceUnavailable {
			t.Errorf("GET %s as the granted member: %d %s, want past the check", path, code, body)
		}
		open := strings.Replace(path, "payroll", "sales", 1)
		if code, body := call(budi, http.MethodGet, open); code != http.StatusServiceUnavailable {
			t.Errorf("GET %s, an open dashboard, as a member: %d %s, want past the check", open, code, body)
		}
		// Not found passes through to the handler's own answer (T-Z3 §9c): an id
		// that is no dashboard of this company is not a 403 that confirms it.
		missing := strings.Replace(path, "payroll", "nope", 1)
		if code, body := call(budi, http.MethodGet, missing); code == http.StatusForbidden {
			t.Errorf("GET %s, no such dashboard: %d %s, want the handler's answer, not a refusal", missing, code, body)
		}
	}

	// The links: admin by the role table, then the grant.
	if code, _ := call(dewi, http.MethodGet, "/api/dashboards/payroll/shares"); code != http.StatusForbidden {
		t.Errorf("listing a restricted dashboard's links as an admin without the grant: %d, want 403", code)
	}
	// Revoking and minting never ask: revoking can only close a door, and a mint
	// on a restricted dashboard is refused whoever asks (resourceExempt).
	before := store.loads.Load()
	call(dewi, http.MethodDelete, "/api/dashboards/payroll/shares/sh-1")
	call(dewi, http.MethodPost, "/api/dashboards/payroll/shares")
	call(dewi, http.MethodDelete, "/api/dashboards/payroll")
	if after := store.loads.Load(); after != before {
		t.Errorf("revoke, mint and delete read the grant store %d times, want 0", after-before)
	}
}
