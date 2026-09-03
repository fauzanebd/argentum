package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/domain"
)

// The vocabulary the dashboard renders, and the sentence beside each checkbox.
//
// `GET /api-keys/scopes` exists so a scope added on the backend reaches the UI
// without a second edit. What nothing checked was the other half of that
// promise: `scopeDescription` is a map, a missing key yields "", and the
// endpoint served the scope anyway. `read:data` and `write:visualizations` were
// offered with a blank description from the day `T-14` added them until the
// 2026-08-16 gate read the endpoint by hand.
//
// So this asserts the join rather than the map: every scope the product issues
// must arrive with a sentence, and the test names the ones that do not.

func TestEveryScopeHasADescription(t *testing.T) {
	for _, s := range domain.AllScopes {
		if strings.TrimSpace(scopeDescription[s]) == "" {
			t.Errorf("scope %q is offered to a tenant with no description; add one to scopeDescription", s)
		}
	}
}

// And the same thing over the wire, because the map being complete is not the
// claim — the response being complete is. A future refactor that filters,
// caches or reshapes the payload has to keep it true.
func TestScopesEndpointDescribesEveryScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &APIKeysHandler{}
	r.GET("/api-keys/scopes", h.scopes)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api-keys/scopes", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var body struct {
		Scopes []struct {
			Scope       domain.Scope `json:"scope"`
			Description string       `json:"description"`
			Writes      bool         `json:"writes"`
		} `json:"scopes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Scopes) != len(domain.AllScopes) {
		t.Fatalf("served %d scopes, want %d", len(body.Scopes), len(domain.AllScopes))
	}
	for _, s := range body.Scopes {
		if strings.TrimSpace(s.Description) == "" {
			t.Errorf("scope %q served with an empty description", s.Scope)
		}
		if want := strings.HasPrefix(string(s.Scope), "write:"); s.Writes != want {
			t.Errorf("scope %q: writes = %v, want %v", s.Scope, s.Writes, want)
		}
	}
}

// A description can be non-empty and wrong, and one was for a fortnight.
//
// `write:visualizations` told every admin who ticked it *"Create a Metabase
// chart or dashboard. It writes to Metabase"* — a sentence that survived T-D15
// deleting Metabase and T-D11 re-pointing the scope at `create_dashboard`. The
// two tests above both passed throughout: the map had a key and the endpoint
// served a string, and neither asks whether the string is true.
//
// Naming a system the product does not run is the one wrongness a test can
// check without duplicating the map into an assertion. It is narrow on purpose
// — the general problem is unautomatable — and it is the exact shape that
// happened.
func TestNoScopeDescriptionNamesADecommissionedSystem(t *testing.T) {
	// Systems this product used to run and no longer does. Add to this list
	// when the next one is removed; the strings are what an admin would read.
	decommissioned := []string{"Metabase", "metabase"}
	for s, desc := range scopeDescription {
		for _, name := range decommissioned {
			if strings.Contains(desc, name) {
				t.Errorf("scope %q is described to a tenant as %q, which names %s — this deployment does not run it",
					s, desc, name)
			}
		}
	}
}
