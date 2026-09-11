package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

type fakeCoverageRepo struct {
	cov       domain.MetricCoverage
	covErr    error
	top       []domain.AdHocQuestion
	topErr    error
	topCalls  int
	sinceSeen time.Time
}

func (f *fakeCoverageRepo) Coverage(_ context.Context, _ string, since time.Time) (domain.MetricCoverage, error) {
	f.sinceSeen = since
	return f.cov, f.covErr
}

func (f *fakeCoverageRepo) TopAdHocQuestions(_ context.Context, _ string, _ time.Time, _ int) ([]domain.AdHocQuestion, error) {
	f.topCalls++
	return f.top, f.topErr
}

func TestPercentCountsAMixedTurnAsCovered(t *testing.T) {
	cases := []struct {
		name string
		c    domain.MetricCoverage
		want float64
	}{
		{"all certified", domain.MetricCoverage{Certified: 10}, 100},
		{"all ad hoc", domain.MetricCoverage{AdHoc: 10}, 0},
		{"half", domain.MetricCoverage{Certified: 5, AdHoc: 5}, 50},
		{"mixed counts as covered", domain.MetricCoverage{Mixed: 5, AdHoc: 5}, 50},
		{"nothing answered", domain.MetricCoverage{NoData: 9}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.Percent(); got != tc.want {
				t.Errorf("Percent() = %v; want %v", got, tc.want)
			}
		})
	}
}

// A turn that never asked the data anything says nothing about whether the
// registry covers the questions people ask, so it must stay out of the
// denominator — otherwise a tenant who uses the product for chart edits reads
// as badly covered.
func TestNoDataTurnsAreNotInTheDenominator(t *testing.T) {
	c := domain.MetricCoverage{Certified: 1, AdHoc: 1, NoData: 98}
	if got := c.Answered(); got != 2 {
		t.Errorf("Answered() = %d; want 2", got)
	}
	if got := c.Percent(); got != 50 {
		t.Errorf("Percent() = %v; want 50", got)
	}
}

func TestTheWindowIsClampedAtBothEnds(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		asked int
		want  int
	}{
		{0, defaultCoverageDays},
		{-5, defaultCoverageDays},
		{7, 7},
		{100000, maxCoverageDays},
	}
	for _, tc := range cases {
		repo := &fakeCoverageRepo{cov: domain.MetricCoverage{Certified: 1}}
		s := NewMetricCoverageService(repo)
		s.now = func() time.Time { return now }
		rep, err := s.ForCompany(context.Background(), "co-1", tc.asked)
		if err != nil {
			t.Fatalf("ForCompany(%d) = %v", tc.asked, err)
		}
		if rep.Days != tc.want {
			t.Errorf("asked %d days, got window %d; want %d", tc.asked, rep.Days, tc.want)
		}
		if want := now.AddDate(0, 0, -tc.want); !repo.sinceSeen.Equal(want) {
			t.Errorf("since = %s; want %s", repo.sinceSeen, want)
		}
	}
}

// A tenant at full coverage should not pay for a query that can only return an
// empty list.
func TestTheAdHocListIsNotQueriedWhenThereIsNothingToList(t *testing.T) {
	repo := &fakeCoverageRepo{cov: domain.MetricCoverage{Certified: 12, Mixed: 3}}
	s := NewMetricCoverageService(repo)
	rep, err := s.ForCompany(context.Background(), "co-1", 30)
	if err != nil {
		t.Fatalf("ForCompany() = %v", err)
	}
	if repo.topCalls != 0 {
		t.Errorf("top calls = %d; want 0 with no ad-hoc turns", repo.topCalls)
	}
	if rep.AdHocTop == nil {
		t.Error("ad_hoc_top is nil; an empty list serialises as [] and nil serialises as null")
	}
}

// The number is the point; the list explains it. Losing the list must not lose
// the number.
func TestAFailedAdHocListStillReturnsTheCounts(t *testing.T) {
	repo := &fakeCoverageRepo{
		cov:    domain.MetricCoverage{Certified: 2, AdHoc: 8},
		topErr: errors.New("statement timeout"),
	}
	s := NewMetricCoverageService(repo)
	rep, err := s.ForCompany(context.Background(), "co-1", 30)
	if err != nil {
		t.Fatalf("ForCompany() = %v; the counts must survive a failed second query", err)
	}
	if rep.Coverage.AdHoc != 8 || rep.Coverage.Percent() != 20 {
		t.Errorf("counts = %+v", rep)
	}
	if len(rep.AdHocTop) != 0 {
		t.Errorf("ad_hoc_top = %v; want empty", rep.AdHocTop)
	}
}

func TestAFailedCountIsAnError(t *testing.T) {
	repo := &fakeCoverageRepo{covErr: errors.New("database is down")}
	s := NewMetricCoverageService(repo)
	if _, err := s.ForCompany(context.Background(), "co-1", 30); err == nil {
		t.Error("ForCompany() = nil; a panel showing a made-up zero is worse than one showing an error")
	}
}
