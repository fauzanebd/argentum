package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// A kind added to the vocabulary without a row in resourceTables would compile,
// pass every authz test against fakes, and fail at the first real request with
// "is not a resource kind" — from the repository, in production. This is the
// join the fakes cannot see.
func TestEveryResourceKindHasATable(t *testing.T) {
	for _, kind := range domain.AllResourceKinds {
		if _, err := tableFor(kind); err != nil {
			t.Errorf("resource kind %q has no table: %v — add it to resourceTables and to 084's successor", kind, err)
		}
	}
	if len(resourceTables) != len(domain.AllResourceKinds) {
		t.Errorf("resourceTables has %d entries for %d kinds; a table for a kind the domain does not offer is unreachable", len(resourceTables), len(domain.AllResourceKinds))
	}
}

// An unknown kind is refused before any statement is built — and before the
// repository touches its database, which is nil here and would panic.
func TestUnknownResourceKindNeverReachesTheDatabase(t *testing.T) {
	r := &ResourceGrantRepo{}
	ctx := context.Background()
	const bad domain.ResourceKind = "folder"
	checks := map[string]error{
		"Grant":  r.Grant(ctx, "co", "u", bad, "id", "admin"),
		"Revoke": r.Revoke(ctx, "co", "u", bad, "id"),
	}
	_, err := r.SetAccessMode(ctx, "co", bad, "id", domain.AccessModeOpen)
	checks["SetAccessMode"] = err
	_, err = r.LoadAccess(ctx, "co", "u", bad, []string{"id"})
	checks["LoadAccess"] = err
	_, err = r.View(ctx, "co", bad, "id")
	checks["View"] = err
	_, err = r.ListViews(ctx, "co", bad)
	checks["ListViews"] = err
	for name, err := range checks {
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("%s with an unknown kind = %v, want ErrInvalidInput", name, err)
		}
	}
}

// Every kind has a name on the admin's read (T-Z5), and only a dashboard has a
// door a restriction must close: its share links. A second kind gaining one is a
// decision — roadmap 12 leaves a generated document's links to the owner
// (access-grants §13d) — so it fails here until somebody changes this test on
// purpose.
func TestEveryResourceKindIsNamedAndOnlyADashboardClosesDoorsOnRestrict(t *testing.T) {
	for _, kind := range domain.AllResourceKinds {
		table, _ := tableFor(kind)
		if !strings.Contains(table.name, "r.") {
			t.Errorf("resource kind %q names its resources with %q, which reads nothing off the row aliased r", kind, table.name)
		}
		if closes := table.closeOnRestrict != ""; closes != (kind == domain.ResourceKindDashboard) {
			t.Errorf("resource kind %q closes something on restrict = %v, want %v", kind, closes, kind == domain.ResourceKindDashboard)
		}
	}
	dash, _ := tableFor(domain.ResourceKindDashboard)
	for _, must := range []string{"company_id = $1", "dashboard_id = $2", "revoked_at IS NULL", "expires_at > now()"} {
		if !strings.Contains(dash.closeOnRestrict, must) {
			t.Errorf("a dashboard's revoke on restrict lacks %q:\n%s", must, dash.closeOnRestrict)
		}
	}
}

// Nothing to ask is not a query.
func TestLoadAccessWithNoIdsDoesNotQuery(t *testing.T) {
	got, err := (&ResourceGrantRepo{}).LoadAccess(context.Background(), "co", "u", domain.ResourceKindAgent, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("LoadAccess(no ids) = (%v, %v), want an empty map and no database", got, err)
	}
}
