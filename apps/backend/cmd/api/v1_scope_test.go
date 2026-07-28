package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
)

// scopelessKey authenticates every request as a real key belonging to a real
// company, holding no scopes at all.
//
// It exists because the property under test cannot be read out of a built
// router: gin's RouteInfo exposes the final handler, not the chain, so unlike
// T-04's role table there is nothing to diff. The only proof available is
// behavioural — send a credential that can do nothing and watch every route
// refuse it.
type scopelessKey struct{ scopes []domain.Scope }

func (k scopelessKey) Authenticate(context.Context, string) (*domain.APIKey, error) {
	return &domain.APIKey{
		ID:        "key-1",
		CompanyID: "co-1",
		Name:      "test",
		Scopes:    k.scopes,
	}, nil
}

// unscopedV1Routes are the `/v1` routes that must remain reachable by a key
// with no scopes. There is exactly one, and it is deliberate: `/v1/me` is what
// an integrator calls to find out *which* scopes their key has, and gating it
// on one of them would make a key with none undiagnosable.
var unscopedV1Routes = map[string]bool{
	"GET /v1/me": true,
}

// TestEveryV1RouteNamesAScope is the guard for the risk the sprint register
// calls "a /v1 route ships without a scope on it" (T-13, first real call site
// in T-A2).
//
// Deny by default is not a property of the middleware — RequireScope only
// denies when it is present. A route that forgets it reaches every key the
// tenant has ever minted, silently, and no amount of reading the router
// catches that. So: authenticate as a key with an empty scope set, hit every
// route, and require a refusal.
func TestEveryV1RouteNamesAScope(t *testing.T) {
	r := routerWithDeps(t, func(d *apiDeps) {
		d.apiKeyAuth = scopelessKey{}
	})

	for _, key := range v1Routes(t) {
		if unscopedV1Routes[key] {
			continue
		}
		method, path, _ := strings.Cut(key, " ")
		t.Run(key, func(t *testing.T) {
			req, err := http.NewRequest(method, concreteURL(path), nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			req.Header.Set("Authorization", "Bearer arg_test_key")
			if code := statusOf(r, req); code != http.StatusForbidden {
				t.Errorf("status = %d, want 403 — a key with no scopes reached this route, "+
					"which means it carries no RequireScope", code)
			}
		})
	}
}

// TestUnscopedV1RoutesAreReal keeps the exemption above from rotting into a
// list of paths that no longer exist — the same failure mode T-04's stale-entry
// check exists for.
func TestUnscopedV1RoutesAreReal(t *testing.T) {
	registered := map[string]bool{}
	for _, key := range v1Routes(t) {
		registered[key] = true
	}
	for key := range unscopedV1Routes {
		if !registered[key] {
			t.Errorf("%s is exempted from the scope requirement but is not a route", key)
		}
	}
}

// TestReportScopesAreDistinct pins the split the ticket asks for: producing a
// document and reading one back are different capabilities, so a key minted to
// fetch yesterday's reports cannot spend a turn generating a new one.
func TestReportScopesAreDistinct(t *testing.T) {
	readOnly := routerWithDeps(t, func(d *apiDeps) {
		d.apiKeyAuth = scopelessKey{scopes: []domain.Scope{domain.ScopeReadDocuments}}
	})

	for _, key := range []string{"POST /v1/reports", "POST /v1/reports/render"} {
		method, path, _ := strings.Cut(key, " ")
		t.Run(key, func(t *testing.T) {
			req, err := http.NewRequest(method, concreteURL(path), strings.NewReader(`{}`))
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			req.Header.Set("Authorization", "Bearer arg_test_key")
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "k-1")
			if code := statusOf(readOnly, req); code != http.StatusForbidden {
				t.Errorf("status = %d, want 403 — read:documents must not write a report", code)
			}
		})
	}

	writeOnly := routerWithDeps(t, func(d *apiDeps) {
		d.apiKeyAuth = scopelessKey{scopes: []domain.Scope{domain.ScopeWriteReports}}
	})
	req, err := http.NewRequest(http.MethodGet, "/v1/documents", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer arg_test_key")
	if code := statusOf(writeOnly, req); code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 — write:reports must not list documents", code)
	}
}

// TestIdempotencyIsRequiredOnBothDoors is the acceptance item T-A1 recorded as
// tested-not-live because `/v1` had no POST route to exercise it. It has one
// now: a write that spends money without an Idempotency-Key is a
// duplicate-billing bug waiting for its first network blip, so the header is
// mandatory rather than honoured-if-present.
func TestIdempotencyIsRequiredOnBothDoors(t *testing.T) {
	r := routerWithDeps(t, func(d *apiDeps) {
		d.apiKeyAuth = scopelessKey{scopes: []domain.Scope{domain.ScopeWriteReports}}
	})

	for _, path := range []string{"/v1/reports", "/v1/reports/render"} {
		t.Run(path, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			req.Header.Set("Authorization", "Bearer arg_test_key")
			req.Header.Set("Content-Type", "application/json")
			if code := statusOf(r, req); code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 without an Idempotency-Key", code)
			}
		})
	}
}

// compile-time proof that the seam takes what the middleware takes.
var _ middleware.APIKeyAuthenticator = scopelessKey{}
