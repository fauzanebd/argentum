package app

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/agentbudget"
	"github.com/fauzanebd/argentum/internal/domain"
)

type fakeTurnShapes struct {
	shapes    []domain.TurnShape
	err       error
	company   string
	sinceSeen time.Time
	tools     []string
}

func (f *fakeTurnShapes) TurnShapes(_ context.Context, companyID string, since time.Time, tools []string) ([]domain.TurnShape, error) {
	f.company, f.sinceSeen, f.tools = companyID, since, tools
	return f.shapes, f.err
}

// Acceptance: a turn calling compute and stating only compute's result is
// counted as covered.
func TestATurnStatingOnlyWhatComputeReturnedIsCovered(t *testing.T) {
	got := domain.TallyDerivedFigures(time.Time{}, []domain.TurnShape{
		{Computed: true, Checked: true, Unaccounted: false, Turns: 3},
	})
	if got.Computed != 3 || got.Residue != 0 || got.Composed != 0 {
		t.Errorf("tally = %+v; want 3 computed and nothing else", got)
	}
	if got.UnaccountedPercent() != 0 {
		t.Errorf("unaccounted = %v%%; a covered turn is not unaccounted", got.UnaccountedPercent())
	}
}

// Acceptance: a turn calling compute and then stating an additional derived
// figure is counted separately — the bucket that justifies T-W4.
func TestAFurtherDerivedFigureAfterComputeIsResidue(t *testing.T) {
	got := domain.TallyDerivedFigures(time.Time{}, []domain.TurnShape{
		{Computed: true, Checked: true, Unaccounted: true, Turns: 1},
		{Computed: true, Checked: true, Unaccounted: false, Turns: 3},
		{Computed: false, Checked: true, Unaccounted: true, Turns: 5},
	})
	if got.Residue != 1 || got.Computed != 3 || got.Composed != 5 {
		t.Fatalf("tally = %+v; want residue 1, computed 3, composed 5", got)
	}
	if got.ResiduePercent() != 25 {
		t.Errorf("residue = %v%%; want 25 (1 of 4 compute turns)", got.ResiduePercent())
	}
}

// A turn nobody measured must not land in the number that decides T-W4 — not
// even one that called compute, because without a record "computed and said
// nothing else" and "computed and then divided anyway" look the same.
func TestAnUncheckedTurnIsInNoReplyBucket(t *testing.T) {
	got := domain.TallyDerivedFigures(time.Time{}, []domain.TurnShape{
		{Computed: true, CrossSource: true, Checked: false, Turns: 7},
		{Checked: true, Turns: 2},
	})
	if got.Answered != 9 || got.Checked != 2 || got.Unchecked() != 7 {
		t.Errorf("answered/checked/unchecked = %d/%d/%d; want 9/2/7", got.Answered, got.Checked, got.Unchecked())
	}
	if got.Computed+got.Residue+got.Composed != 0 {
		t.Errorf("tally = %+v; an unchecked turn was classified", got)
	}
	// Cross-source is agent_actions' answer alone, so it counts either way.
	if got.CrossSource != 7 {
		t.Errorf("cross source = %d; want 7", got.CrossSource)
	}
}

func TestPercentagesAreZeroRatherThanUndefinedWithNothingToDivide(t *testing.T) {
	var d domain.DerivedFigures
	if d.UnaccountedPercent() != 0 || d.ResiduePercent() != 0 || d.Unchecked() != 0 {
		t.Errorf("empty figures = %v%% / %v%% / %d", d.UnaccountedPercent(), d.ResiduePercent(), d.Unchecked())
	}
}

// The panel's definition of a turn that asked the data something is
// agentbudget's, not a second list — including compute, which is what makes a
// compute-only follow-up turn countable at all.
func TestTheQueryIsGivenAgentbudgetsDataTools(t *testing.T) {
	repo := &fakeTurnShapes{}
	if _, err := NewDerivedFiguresService(repo).ForCompany(context.Background(), "co-1", 30); err != nil {
		t.Fatalf("ForCompany() = %v", err)
	}
	if len(repo.tools) != len(agentbudget.DataTools()) {
		t.Errorf("tools = %v; want agentbudget's %v", repo.tools, agentbudget.DataTools())
	}
	for _, name := range []string{"run_sql", "query_metric", "search_documents", "compute"} {
		if !slices.Contains(repo.tools, name) {
			t.Errorf("tools = %v; missing %q", repo.tools, name)
		}
	}
	for _, name := range repo.tools {
		if !agentbudget.IsDataTool(name) {
			t.Errorf("%q is passed as a data tool and agentbudget does not think it is one", name)
		}
	}
	// The tenant is the caller's, passed through untouched. The SQL's own
	// `company_id = $1` on both halves is owed a database — live-gate-backlog §7b.
	if repo.company != "co-1" {
		t.Errorf("company = %q; want co-1", repo.company)
	}
}

// Two panels on one page read the same month.
func TestDerivedFiguresShareTheCoverageWindow(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ asked, want int }{
		{0, defaultCoverageDays},
		{7, 7},
		{100000, maxCoverageDays},
	} {
		repo := &fakeTurnShapes{}
		s := NewDerivedFiguresService(repo)
		s.now = func() time.Time { return now }
		rep, err := s.ForCompany(context.Background(), "co-1", tc.asked)
		if err != nil {
			t.Fatalf("ForCompany(%d) = %v", tc.asked, err)
		}
		if rep.Days != tc.want || !repo.sinceSeen.Equal(now.AddDate(0, 0, -tc.want)) {
			t.Errorf("asked %d: days %d since %s; want %d", tc.asked, rep.Days, repo.sinceSeen, tc.want)
		}
		if !rep.Figures.From.Equal(repo.sinceSeen) {
			t.Errorf("from = %s; want the window's start %s", rep.Figures.From, repo.sinceSeen)
		}
	}
}

func TestAFailedTurnShapeReadIsAnError(t *testing.T) {
	repo := &fakeTurnShapes{err: errors.New("database is down")}
	if _, err := NewDerivedFiguresService(repo).ForCompany(context.Background(), "co-1", 30); err == nil {
		t.Error("ForCompany() = nil; a panel showing a made-up zero is worse than one showing an error")
	}
}
