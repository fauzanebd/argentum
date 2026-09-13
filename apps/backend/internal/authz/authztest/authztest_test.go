package authztest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/domain"
)

// The table checks itself. What the product does is each package's probe to
// find out; that something is written down for every kind, every door and every
// package — and that it is written down coherently — is this file's.

// "Adding a fifth resource kind requires exactly one new row in the table": the
// row is required, and so is one for each derived kind this track hides.
func TestEveryKindAPersonCanBeRefusedHasARow(t *testing.T) {
	rows := map[string]bool{}
	for _, row := range Table {
		if rows[row.Kind] {
			t.Errorf("kind %q has two rows", row.Kind)
		}
		rows[row.Kind] = true
	}
	want := []string{authz.KindConversation, KindGeneratedDocument, KindPendingAction}
	for _, kind := range domain.AllResourceKinds {
		want = append(want, string(kind))
	}
	for _, kind := range want {
		if !rows[kind] {
			t.Errorf("kind %q has no row in authztest.Table; write down what every door does with one", kind)
		}
	}
}

// "A door nobody decided about is a door that is open" (roadmap 12 §2): every
// row has a surface on every door, or a sentence saying why it has none.
func TestEveryRowDecidesEveryDoor(t *testing.T) {
	for _, row := range Table {
		for _, door := range authz.AllDoors {
			surfaced := slices.ContainsFunc(row.Surfaces, func(s Surface) bool { return s.Door == door })
			why, unprobed := row.Unprobed[door]
			switch {
			case surfaced && unprobed:
				t.Errorf("%s on the %s door has a surface and a reason it has none", row.Kind, door)
			case !surfaced && !unprobed:
				t.Errorf("%s says nothing about the %s door — add a surface, or say in Unprobed why there is none", row.Kind, door)
			case unprobed && len(strings.Fields(why)) < 5:
				t.Errorf("%s on the %s door is unprobed with %q, which is not a reason", row.Kind, door, why)
			}
		}
		for door := range row.Unprobed {
			if !slices.Contains(authz.AllDoors, door) {
				t.Errorf("%s names a door %q that authz does not have", row.Kind, door)
			}
		}
	}
}

func TestEverySurfaceSaysHowItRefusesOrWhyItAsksNothing(t *testing.T) {
	packages := []string{PackageApp, PackageTools, PackageHandlers, PackageAPI}
	reasons := []authz.Reason{authz.ReasonNotGranted, authz.ReasonNotCleared, authz.ReasonNotOnKey, authz.ReasonCreatorRemoved}
	seen := map[string]bool{}
	for _, row := range Table {
		for _, s := range row.Surfaces {
			key := Key(row.Kind, s.Door, s.Name)
			if seen[key] {
				t.Errorf("%q is listed twice", key)
			}
			seen[key] = true
			if !slices.Contains(packages, s.Package) {
				t.Errorf("%q runs in %q, which is not a package this suite knows", key, s.Package)
			}
			if s.Outcome.refuses() != (s.Reason != "") {
				t.Errorf("%q is %s with reason %q: a surface that refuses says what a refusal is recorded as, and one that never refuses has nothing to record", key, s.Outcome.Name, s.Reason)
			}
			if s.Reason != "" && !slices.Contains(reasons, s.Reason) {
				t.Errorf("%q records %q, which is not a refusal reason", key, s.Reason)
			}
			if s.Outcome == Unasked && len(strings.Fields(s.Why)) < 5 {
				t.Errorf("%q asks nothing and does not say why", key)
			}
			if s.AdminOnly && s.Door != authz.DoorDashboard {
				t.Errorf("%q is admin-only on the %s door, which has no role table", key, s.Door)
			}
		}
	}
}

// A package the table gives surfaces to has to run them, or they are
// expectations nothing checks. Read from the source, because nothing in this
// package can see whether another package's test ran.
func TestEveryPackageTheTableNamesRunsItsSurfaces(t *testing.T) {
	constants := map[string]string{
		PackageApp:      "PackageApp",
		PackageTools:    "PackageTools",
		PackageHandlers: "PackageHandlers",
		PackageAPI:      "PackageAPI",
	}
	named := map[string]bool{}
	for _, row := range Table {
		for _, s := range row.Surfaces {
			named[s.Package] = true
		}
	}
	for pkg := range named {
		// This package is apps/backend/internal/authz/authztest.
		files, err := filepath.Glob(filepath.Join("..", "..", "..", filepath.FromSlash(pkg), "*_test.go"))
		if err != nil || len(files) == 0 {
			t.Errorf("found no test files for %s (%v); the path from this package is wrong", pkg, err)
			continue
		}
		call := "authztest.Run(t, authztest." + constants[pkg]
		runs := false
		for _, f := range files {
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read %s: %v", f, err)
			}
			if strings.Contains(string(src), call) {
				runs = true
				break
			}
		}
		if !runs {
			t.Errorf("authztest.Table gives %s surfaces, and no test there calls %s, …)", pkg, call)
		}
	}
}

func TestCellsAreTheEightCombinations(t *testing.T) {
	cells := Cells()
	if len(cells) != 8 {
		t.Fatalf("%d cells, want 8", len(cells))
	}
	seen := map[Cell]bool{}
	for _, c := range cells {
		seen[c] = true
	}
	if len(seen) != 8 {
		t.Errorf("%d distinct cells, want 8", len(seen))
	}
	if (Cell{Role: domain.RoleAdmin}).Person() == (Cell{Role: domain.RoleMember}).Person() {
		t.Error("the admin and the member are the same person, so a grant to one would be read as the other's")
	}
}

// Admits reads each cell from its own field. Two outcomes no surface uses, so a
// field read for the wrong cell cannot pass by agreeing with a real one.
func TestAnOutcomeAnswersEachCellFromItsOwnField(t *testing.T) {
	onlyUngranted := Outcome{Open: true, OpenGranted: false, Restricted: true, RestrictedGranted: false}
	onlyRestricted := Outcome{Open: false, OpenGranted: false, Restricted: true, RestrictedGranted: true}
	for _, c := range Cells() {
		if got := onlyUngranted.Admits(c); got != !c.Granted {
			t.Errorf("%s: an outcome admitting only the ungranted says %v", c, got)
		}
		if got := onlyRestricted.Admits(c); got != c.Restricted {
			t.Errorf("%s: an outcome admitting only restricted objects says %v", c, got)
		}
	}
}
