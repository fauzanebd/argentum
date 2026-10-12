package main

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
)

// RIZ-26: a path id that cannot be an id answers 404 on every `/api` route, and
// a route added later cannot quietly opt out by naming its parameter something
// the tables have never seen.

// idTables is policy.go's three tables as values, so a test can plant a defect
// in a copy and watch the checker report it — resourceTables' arrangement.
type idTables struct {
	uuid         []string
	exemptParams map[string]string
	exemptRoutes map[string]string
}

func currentIDTables() idTables {
	return idTables{
		uuid:         slices.Clone(uuidParams),
		exemptParams: maps.Clone(exemptIDParams),
		exemptRoutes: maps.Clone(wellFormedIDExempt),
	}
}

// routeParams is every `:param` a pattern declares, in order. A `*catchall` is
// not one: the only catch-all under `/api` is the widget's CORS preflight,
// which sits on a group the middleware does not run on and carries no id.
func routeParams(path string) []string {
	var out []string
	for _, seg := range strings.Split(path, "/") {
		if strings.HasPrefix(seg, ":") {
			out = append(out, seg[1:])
		}
	}
	return out
}

// idClassificationProblems is R7 as a function of the routes and the tables.
// routes are every route the router registers, as RouteKey; only `/api` ones
// must be classified, and the rest are what makes `token` and `app_id` real.
func idClassificationProblems(routes []string, tables idTables) []string {
	var problems []string
	report := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	registered := map[string]bool{}
	declared := map[string]bool{}
	for _, key := range routes {
		registered[key] = true
		_, path, _ := strings.Cut(key, " ")
		for _, p := range routeParams(path) {
			declared[p] = true
		}
	}

	for _, name := range tables.uuid {
		if _, both := tables.exemptParams[name]; both {
			report(":%s is in uuidParams and exemptIDParams — it is a uuid or it is not", name)
		}
		if !declared[name] {
			report("uuidParams lists :%s, which no route declares", name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(tables.exemptParams)) {
		if !declared[name] {
			report("exemptIDParams lists :%s, which no route declares", name)
		}
		if len(strings.Fields(tables.exemptParams[name])) < 3 {
			report("exemptIDParams lists :%s without saying what it carries instead", name)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(tables.exemptRoutes)) {
		if !registered[key] {
			report("wellFormedIDExempt lists %s, which the router does not register", key)
			continue
		}
		if len(strings.Fields(tables.exemptRoutes[key])) < 5 {
			report("wellFormedIDExempt lists %s without a reason — an exemption is a decision, and a decision is written down", key)
		}
		_, path, _ := strings.Cut(key, " ")
		if !slices.ContainsFunc(routeParams(path), func(p string) bool { return slices.Contains(tables.uuid, p) }) {
			report("wellFormedIDExempt lists %s, which has no uuid parameter to exempt", key)
		}
	}

	for _, key := range routes {
		_, path, _ := strings.Cut(key, " ")
		if !strings.HasPrefix(path, "/api/") {
			continue
		}
		if _, exempt := tables.exemptRoutes[key]; exempt {
			continue
		}
		for _, p := range routeParams(path) {
			if slices.Contains(tables.uuid, p) {
				continue
			}
			if _, ok := tables.exemptParams[p]; ok {
				continue
			}
			report("route %s has :%s, which is neither in uuidParams nor in exemptIDParams — decide whether it carries a row id", key, p)
		}
	}
	return problems
}

func allRouteKeys(t *testing.T) []string {
	t.Helper()
	var keys []string
	for _, ri := range realRouter(t).Routes() {
		keys = append(keys, middleware.RouteKey(ri.Method, ri.Path))
	}
	slices.Sort(keys)
	return keys
}

// TestEveryIDParamIsClassified is R7 against the real router.
func TestEveryIDParamIsClassified(t *testing.T) {
	routes := allRouteKeys(t)
	for _, problem := range idClassificationProblems(routes, currentIDTables()) {
		t.Error(problem)
	}
	// A walk that saw no parameter would pass the loop above vacuously.
	if len(uuidRoutes(routes)) < 50 {
		t.Fatalf("only %d `/api` routes carry a uuid parameter; the walk has stopped seeing the router", len(uuidRoutes(routes)))
	}
}

// TestIDClassificationCatchesEachDefect is AC8 and Q3, held permanently rather
// than in a scratch run: each case plants one defect and the checker names it.
func TestIDClassificationCatchesEachDefect(t *testing.T) {
	routes := allRouteKeys(t)
	cases := []struct {
		name   string
		plant  func(routes []string, tables *idTables) []string
		expect string
	}{
		{
			name: "a new route with a parameter nobody classified",
			plant: func(routes []string, _ *idTables) []string {
				return append(slices.Clone(routes), "GET /api/widgets/:widgetId")
			},
			expect: "route GET /api/widgets/:widgetId has :widgetId, which is neither in uuidParams nor in exemptIDParams",
		},
		{
			name: "a new parameter beside a classified one",
			plant: func(routes []string, _ *idTables) []string {
				return append(slices.Clone(routes), "DELETE /api/agents/:id/notes/:noteRef")
			},
			expect: "route DELETE /api/agents/:id/notes/:noteRef has :noteRef",
		},
		{
			name: "a name in both tables",
			plant: func(routes []string, tables *idTables) []string {
				tables.exemptParams["runID"] = "planted to be in both tables at once"
				return routes
			},
			expect: ":runID is in uuidParams and exemptIDParams",
		},
		{
			name: "an exempt route that is gone",
			plant: func(routes []string, tables *idTables) []string {
				tables.exemptRoutes["DELETE /api/teams/users/:id"] = "a platform that was removed and left its exemption behind"
				return routes
			},
			expect: "wellFormedIDExempt lists DELETE /api/teams/users/:id, which the router does not register",
		},
		{
			name: "an exemption without a reason",
			plant: func(routes []string, tables *idTables) []string {
				tables.exemptRoutes["DELETE /api/slack/users/:id"] = "slack"
				return routes
			},
			expect: "wellFormedIDExempt lists DELETE /api/slack/users/:id without a reason",
		},
		{
			name: "an exempt parameter no route uses",
			plant: func(routes []string, tables *idTables) []string {
				tables.exemptParams["slug"] = "a human-readable name for a thing"
				return routes
			},
			expect: "exemptIDParams lists :slug, which no route declares",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tables := currentIDTables()
			planted := tc.plant(routes, &tables)
			problems := idClassificationProblems(planted, tables)
			for _, p := range problems {
				if strings.Contains(p, tc.expect) {
					return
				}
			}
			t.Errorf("no problem reported containing %q; got %q", tc.expect, problems)
		})
	}
}

// uuidRoutes are the `/api` routes the middleware refuses `x` on: at least one
// uuid parameter, and not exempt.
func uuidRoutes(routes []string) []string {
	var out []string
	for _, key := range routes {
		_, path, _ := strings.Cut(key, " ")
		if !strings.HasPrefix(path, "/api/") {
			continue
		}
		if _, exempt := wellFormedIDExempt[key]; exempt {
			continue
		}
		if slices.ContainsFunc(routeParams(path), func(p string) bool { return slices.Contains(uuidParams, p) }) {
			out = append(out, key)
		}
	}
	return out
}

// exemptValue is a value each exempt parameter really takes, so a route that
// has one beside a uuid is refused for the uuid and nothing else.
var exemptValue = map[string]string{
	"kind":       "agent",
	"capability": "voice",
	"page":       "1",
	"phone":      "+6281200000000",
	"token":      "tok_abc",
	"app_id":     "cli_abc",
}

// grantsEverything is a capability store in which everybody holds every
// capability, so the two voice routes are refused — or not — by the id rather
// than by RequireCapability one link earlier.
type grantsEverything struct{}

func (grantsEverything) ListForUser(_ context.Context, _, userID string) ([]domain.CapabilityGrant, error) {
	out := make([]domain.CapabilityGrant, 0, len(domain.AllCapabilities))
	for _, c := range domain.AllCapabilities {
		out = append(out, domain.CapabilityGrant{UserID: userID, Capability: c})
	}
	return out, nil
}
func (grantsEverything) Grant(context.Context, string, string, domain.Capability, string) error {
	return nil
}
func (grantsEverything) Revoke(context.Context, string, string, domain.Capability) error { return nil }

func embedToken(t *testing.T) string {
	t.Helper()
	signer, err := auth.NewTokenSigner("0123456789abcdef0123456789abcdef", 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}
	tok, err := signer.IssueEmbedToken("co-1", "emp_812", "key-1", 15*time.Minute)
	if err != nil {
		t.Fatalf("IssueEmbedToken: %v", err)
	}
	return tok
}

// TestAMalformedIDIsNotFoundOnEveryRoute is R8 and AC1/AC9: every `/api` route
// with a uuid parameter, `x` in each one, as an admin (or the widget's session,
// on its own group), answers the plain 404 and reaches nothing.
//
// "Reaches nothing" is held two ways. The grant store counts loads and must
// count none, which is RequireResource never asking — the link that used to
// turn `x` into a 503. And every service in these deps is unwired, so a
// handler that ran would answer 500 or 503, not this exact body.
func TestAMalformedIDIsNotFoundOnEveryRoute(t *testing.T) {
	store := &restrictedEverything{}
	r := routerWithDeps(t, func(d *apiDeps) {
		d.cfg.EmbedEnabled = true
		d.resourceAuthz = authz.New(store)
		d.capabilitySvc = app.NewCapabilityService(grantsEverything{}, nil)
	})
	admin, widget := adminToken(t), embedToken(t)

	routes := uuidRoutes(allRouteKeys(t))
	for _, key := range routes {
		method, pattern, _ := strings.Cut(key, " ")
		t.Run(key, func(t *testing.T) {
			url := concreteURLWith(pattern, func(param string) string {
				if v, ok := exemptValue[param]; ok {
					return v
				}
				return "x"
			})
			req := httptest.NewRequest(method, url, nil)
			if strings.HasPrefix(pattern, "/api/embed/") {
				req.Header.Set("Authorization", "Bearer "+widget)
			} else {
				req.Header.Set("Authorization", "Bearer "+admin)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusNotFound || w.Body.String() != `{"error":"not found"}` {
				t.Errorf("%s %s: %d %s, want 404 {\"error\":\"not found\"}", method, url, w.Code, w.Body)
			}
		})
	}
	if n := store.loads.Load(); n != 0 {
		t.Errorf("the grant store was read %d times for ids that name nothing, want 0", n)
	}
	// The ticket's own examples, so a change to the walk cannot drop them
	// without this saying which.
	for _, key := range []string{
		"GET /api/agents/:id", "GET /api/dashboards/:id/shares", "GET /api/actions/:id",
		"GET /api/scheduled-tasks/:id", "POST /api/connections/:id/test", "POST /api/connections/:id/rescan",
		"GET /api/embed/threads/:id/messages",
	} {
		if !slices.Contains(routes, key) {
			t.Errorf("%s is not in the walk", key)
		}
	}
}

// TestAMalformedIDKeepsTheRefusalsAheadOfIt is AC5: the 401 and 403 a caller
// got before RIZ-26 are what they get now, `x` or not.
func TestAMalformedIDKeepsTheRefusalsAheadOfIt(t *testing.T) {
	r := routerWithDeps(t, func(d *apiDeps) { d.cfg.EmbedEnabled = true })
	member := tokenFor(t, "user-2", "member")
	cases := []struct {
		name, method, path, token string
		want                      int
	}{
		{"no session on a member route", http.MethodGet, "/api/agents/x", "", http.StatusUnauthorized},
		{"no session on an admin route", http.MethodDelete, "/api/api-keys/x", "", http.StatusUnauthorized},
		{"a member on an admin route", http.MethodDelete, "/api/api-keys/x", member, http.StatusForbidden},
		// And past those two, the id: a member on a member route gets the 404.
		{"a member on a member route", http.MethodGet, "/api/agents/x", member, http.StatusNotFound},
		{"a member on a route with two ids", http.MethodGet, "/api/scheduled-tasks/x/runs/x", member, http.StatusNotFound},
		{"no widget session", http.MethodGet, "/api/embed/threads/x/messages", "", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			if code := statusOf(r, req); code != tc.want {
				t.Errorf("%s %s: %d, want %d", tc.method, tc.path, code, tc.want)
			}
		})
	}
}

// TestWellFormedAndExemptIDsPassThrough is AC6 and AC7 at the router: a valid
// id in any spelling Postgres accepts, an exempt parameter's ordinary value and
// an exempt route's platform id all get past the check. What the handler then
// answers is its own business — every service here is unwired — so the
// assertion is only that the answer is not the middleware's.
func TestWellFormedAndExemptIDsPassThrough(t *testing.T) {
	store := &restrictedEverything{}
	r := routerWithDeps(t, func(d *apiDeps) {
		d.resourceAuthz = authz.New(store)
		d.capabilitySvc = app.NewCapabilityService(grantsEverything{}, nil)
	})
	admin := adminToken(t)
	upper := strings.ToUpper(anyUUID)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/agents/" + anyUUID},
		{http.MethodGet, "/api/agents/" + upper},
		{http.MethodGet, "/api/agents/%7B" + anyUUID + "%7D"},
		{http.MethodGet, "/api/agents/" + strings.ReplaceAll(anyUUID, "-", "")},
		{http.MethodGet, "/api/access/agent"},
		{http.MethodGet, "/api/access/agent/" + anyUUID},
		{http.MethodGet, "/api/documents/" + anyUUID + "/pages/1"},
		{http.MethodGet, "/api/users/" + anyUUID + "/capabilities"},
		{http.MethodPut, "/api/users/" + anyUUID + "/capabilities/voice"},
		{http.MethodPut, "/api/actions/config/agent"},
		{http.MethodDelete, "/api/phones/+6281200000000"},
		{http.MethodDelete, "/api/discord/users/80351110224678912"},
		{http.MethodDelete, "/api/lark/users/ou_7d8a6e6df7621556ce0d21922b676706"},
		{http.MethodDelete, "/api/slack/users/U024BE7LH"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+admin)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == http.StatusNotFound && w.Body.String() == `{"error":"not found"}` {
			t.Errorf("%s %s was refused as a malformed id", tc.method, tc.path)
		}
	}
	// The restricted dashboard is still refused by grant — the link after this
	// one — for a well-formed id, which is what proves it ran.
	req := httptest.NewRequest(http.MethodGet, "/api/dashboards/"+upper, nil)
	req.Header.Set("Authorization", "Bearer "+admin)
	if code := statusOf(r, req); code != http.StatusForbidden {
		t.Errorf("GET /api/dashboards/<uppercase uuid> as an admin with no grant: %d, want the resource link's 403", code)
	}
}
