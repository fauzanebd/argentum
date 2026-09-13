package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/authz/authztest"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
)

// T-Z9's negative suite, for the routes resourcePolicy gates: the real router,
// the real role, capability and resource tables, and the real authorizer over
// the cell's grant store, as each cell's person.
//
// Here the surface's name is its route, so the probe for every cmd/api surface
// is the same request built from it. What that cannot make circular is the
// outcome: a route whose policy entry is removed, moved to resourceExempt or
// pointed at the wrong kind is refused or admitted against the table, and fails.
func TestTheNegativeSuite(t *testing.T) {
	probes := map[string]authztest.Probe{}
	for _, row := range authztest.Table {
		for _, s := range row.Surfaces {
			if s.Package != authztest.PackageAPI {
				continue
			}
			route, gated := resourcePolicy[s.Name]
			switch {
			case !gated:
				t.Errorf("authztest.Table names %q as a gated route, and resourcePolicy has no entry for it", s.Name)
				continue
			case string(route.Kind) != row.Kind:
				t.Errorf("resourcePolicy serves %q as a %s, and authztest.Table lists it under %s", s.Name, route.Kind, row.Kind)
				continue
			}
			probes[authztest.Key(row.Kind, s.Door, s.Name)] = routeProbe(s.Name, route)
		}
	}
	authztest.Run(t, authztest.PackageAPI, probes)
}

// routeProbe requests a route for its cell's object. Past the check each handler
// has no service in these deps and answers something other than 403 — which is
// the proof the request got through; a 403 is a refusal, by grant or by role.
func routeProbe(key string, route middleware.ResourceRoute) authztest.Probe {
	method, pattern, _ := strings.Cut(key, " ")
	return func(t *testing.T, c authztest.Cell, rec *authztest.Recorder) bool {
		const target = "obj-1"
		r := routerWithDeps(t, func(d *apiDeps) {
			d.resourceAuthz = rec.Authorizer(authztest.World{Kind: route.Kind, Target: target, Cell: c})
		})
		req, err := http.NewRequest(method, concreteURL(strings.ReplaceAll(pattern, ":"+route.Param, target)), nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+tokenFor(t, c.Person(), string(c.Role)))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code != http.StatusForbidden
	}
}
