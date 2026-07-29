package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/transport/http/apiv1"
	"github.com/fauzanebd/argentum/openapi"
)

// The spec is a promise, and this file is what makes it one (T-A4).
//
// A published spec that has drifted from the routes is worse than no spec at
// all, because integrators trust it — the sprint's risk register names this
// exactly, and the mitigation it names is this test. It is the same shape as
// the design tokens' drift job and T-04's policy table: one truth, checked
// against the code in **both** directions.

// specOperations reads the spec once per test, failing loudly rather than
// skipping. A spec that will not parse must not let these tests pass by
// finding nothing to compare.
func specOperations(t *testing.T) []openapi.Operation {
	t.Helper()
	ops, err := openapi.Operations()
	if err != nil {
		t.Fatalf("read the spec: %v", err)
	}
	if len(ops) == 0 {
		t.Fatal("the spec declares no operations; this test would pass vacuously")
	}
	return ops
}

// TestEveryV1RouteIsSpecced is the first direction: a route that nobody wrote
// down.
//
// This is the failure that matters most, because it is silent. A route ships,
// an integrator never learns it exists, and the SDKs — whose types are
// generated from this document — cannot call it at all.
func TestEveryV1RouteIsSpecced(t *testing.T) {
	specced := map[string]bool{}
	for _, op := range specOperations(t) {
		gin, err := openapi.GinPath(op.Path)
		if err != nil {
			t.Fatalf("spec path %q: %v", op.Path, err)
		}
		specced[op.Method+" "+gin] = true
	}

	for _, key := range v1Routes(t) {
		if !specced[key] {
			t.Errorf("%s is registered but has no entry in openapi/v1.yaml — "+
				"add it, or an integrator has no way to know it exists", key)
		}
	}
}

// TestEverySpecEntryIsARoute is the other direction: a promise nobody keeps.
//
// A caller who reads this document and writes a client against an operation
// that was deleted gets a 404 and no explanation. Deleting a route is
// deliberate; leaving its entry here is an oversight, and this is what turns
// the second into a red build rather than a support ticket.
func TestEverySpecEntryIsARoute(t *testing.T) {
	registered := map[string]bool{}
	for _, key := range v1Routes(t) {
		registered[key] = true
	}

	for _, op := range specOperations(t) {
		gin, err := openapi.GinPath(op.Path)
		if err != nil {
			t.Fatalf("spec path %q: %v", op.Path, err)
		}
		if !registered[op.Method+" "+gin] {
			t.Errorf("openapi/v1.yaml declares %s (operationId %q) but no such route is registered",
				op.Key(), op.ID)
		}
	}
}

// TestSpecScopeIsTheScopeTheRouterEnforces checks the half of the contract a
// path-and-method diff cannot see.
//
// `x-argentum-scope` is what an integrator reads when deciding which scopes to
// mint a key with, and a wrong value there sends them to production with a key
// that cannot do the job — or, worse, one that can do more than they intended.
// Neither is visible in a route table, so this asserts it behaviourally, in
// both directions: the named scope is **sufficient** (a key holding only it is
// not refused) and **necessary** (a key holding every other scope is).
func TestSpecScopeIsTheScopeTheRouterEnforces(t *testing.T) {
	for _, op := range specOperations(t) {
		if op.Scope == "" {
			continue
		}
		scope := domain.Scope(op.Scope)
		if !scope.Valid() {
			t.Errorf("%s: x-argentum-scope %q is not a scope this system issues", op.Key(), op.Scope)
			continue
		}
		gin, err := openapi.GinPath(op.Path)
		if err != nil {
			t.Fatalf("spec path %q: %v", op.Path, err)
		}

		t.Run(op.Key(), func(t *testing.T) {
			sufficient := routerWithDeps(t, func(d *apiDeps) {
				d.apiKeyAuth = scopelessKey{scopes: []domain.Scope{scope}}
			})
			if code := statusOf(sufficient, specRequest(t, op.Method, gin)); code == http.StatusForbidden {
				t.Errorf("a key holding only %s was refused — the spec names a scope that does not open this route", scope)
			}

			var others []domain.Scope
			for _, s := range domain.AllScopes {
				if s != scope {
					others = append(others, s)
				}
			}
			necessary := routerWithDeps(t, func(d *apiDeps) {
				d.apiKeyAuth = scopelessKey{scopes: others}
			})
			if code := statusOf(necessary, specRequest(t, op.Method, gin)); code != http.StatusForbidden {
				t.Errorf("status = %d for a key holding every scope except %s, want 403 — "+
					"the route is gated on something other than what the spec names", code, scope)
			}
		})
	}
}

// TestSpecPublicOperationsAreKeyless is the same check for the one operation
// that declares `security: []`. A spec entry that says "no credential needed"
// while the route answers 401 is the first thing an integrator tries and the
// first thing that fails.
func TestSpecPublicOperationsAreKeyless(t *testing.T) {
	r := realRouter(t)
	found := 0
	for _, op := range specOperations(t) {
		if !op.Public {
			continue
		}
		found++
		gin, err := openapi.GinPath(op.Path)
		if err != nil {
			t.Fatalf("spec path %q: %v", op.Path, err)
		}
		t.Run(op.Key(), func(t *testing.T) {
			// No Authorization header at all — the state an integrator is in
			// when they read the spec for the first time.
			if code := statusOf(r, specRequest(t, op.Method, gin)); code != http.StatusOK {
				t.Errorf("status = %d without a credential, want 200 — the spec says this operation is public", code)
			}
		})
	}
	if found == 0 {
		t.Fatal("no public operation in the spec; TestSpecPublicOperationsAreKeyless is checking nothing")
	}
}

// TestSpecVersionMatchesTheContractVersion pins `info.version` to what
// `GET /v1/me` reports as `api_version`. An integrator comparing the two and
// finding a difference has no way to know which one is lying.
func TestSpecVersionMatchesTheContractVersion(t *testing.T) {
	got, err := openapi.Version()
	if err != nil {
		t.Fatalf("read the spec: %v", err)
	}
	if got != apiv1.Version {
		t.Errorf("openapi info.version = %q, apiv1.Version = %q", got, apiv1.Version)
	}
}

// TestOpenAPIRouteServesTheSpec is the route's own test: the bytes on the wire
// are the embedded document, they parse as JSON, and they arrive without a
// credential.
func TestOpenAPIRouteServesTheSpec(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/v1/openapi.json", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	w := httptest.NewRecorder()
	realRouter(t).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	// A browser tool reading a keyless document is the reason this one route
	// carries a CORS header where the rest of `/v1` deliberately carries none.
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}

	var doc struct {
		OpenAPI string `json:"openapi"`
		Info    struct {
			Version string `json:"version"`
		} `json:"info"`
		Paths map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("the served spec is not JSON: %v", err)
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.1") {
		t.Errorf("openapi = %q, want a 3.1.x document", doc.OpenAPI)
	}
	if doc.Info.Version != apiv1.Version {
		t.Errorf("served info.version = %q, want %q", doc.Info.Version, apiv1.Version)
	}
	if len(doc.Paths) == 0 {
		t.Error("the served spec declares no paths")
	}
}

// TestSpecOperationIDsAreUnique keeps the generated clients honest. Both SDKs
// and the Postman collection name their methods from `operationId`, so a
// duplicate silently overwrites one operation with another.
func TestSpecOperationIDsAreUnique(t *testing.T) {
	seen := map[string]string{}
	var ids []string
	for _, op := range specOperations(t) {
		if op.ID == "" {
			t.Errorf("%s has no operationId; the SDKs name their methods from it", op.Key())
			continue
		}
		if prev, dup := seen[op.ID]; dup {
			t.Errorf("operationId %q is used by both %s and %s", op.ID, prev, op.Key())
		}
		seen[op.ID] = op.Key()
		ids = append(ids, op.ID)
	}
	sort.Strings(ids)
	if slices.Contains(ids, "") {
		t.Error("an empty operationId reached the id list")
	}
}

// specRequest builds a request a middleware chain can be measured with. The
// body is a bare object and the idempotency header is always present: this
// file is testing which credential a route accepts, and a 400 for a missing
// header would answer a question nobody asked.
func specRequest(t *testing.T, method, path string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, concreteURL(path), strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer arg_test_key")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "k-1")
	return req
}
