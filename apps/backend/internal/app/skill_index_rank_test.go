package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// T-K5 on the composition side, and the property under test in every case here
// is *when* the ranking runs rather than how well it ranks.
//
// How well it ranks is pgvector's business and the provider's. Whether an
// embedding round trip happens on a turn that had nothing to gain from one is
// this project's, because the index lives in the cached system-prompt prefix
// and an order that moves with the question is an order that invalidates it.

// rankingSkillRepo answers the ranked call with the reverse of its rows, which
// is enough to tell a ranked composition from an alphabetical one without
// owning a vector space.
type rankingSkillRepo struct {
	*fakeSkillRepo
	rankedCalls int
	rankErr     error
}

func newRankingRepo(skills ...*domain.Skill) *rankingSkillRepo {
	return &rankingSkillRepo{fakeSkillRepo: newFakeSkillRepo(skills...)}
}

func (r *rankingSkillRepo) ListEnabledForIndex(_ context.Context, companyID string) ([]*domain.Skill, error) {
	return r.sorted(companyID), nil
}

func (r *rankingSkillRepo) ListEnabledRankedForIndex(
	_ context.Context, companyID string, _ []float32,
) ([]*domain.Skill, error) {
	r.rankedCalls++
	if r.rankErr != nil {
		return nil, r.rankErr
	}
	rows := r.sorted(companyID)
	out := make([]*domain.Skill, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		out = append(out, rows[i])
	}
	return out, nil
}

// sorted is `lower(name)`, which is the order 069's index gives and the order
// T-K3 truncates in. Spelled out here because the fake's map iteration is not.
func (r *rankingSkillRepo) sorted(companyID string) []*domain.Skill {
	rows := r.list(companyID, true)
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && strings.ToLower(rows[j].Name) < strings.ToLower(rows[j-1].Name); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
	return rows
}

// countingVector is the lazy accessor a real turn passes in, with a counter on
// it. The count is the assertion in the first test and the guard in the rest.
func countingVector(vec []float32, calls *int) func() []float32 {
	return func() []float32 {
		*calls++
		return vec
	}
}

func rankFixture(companyID string, names ...string) []*domain.Skill {
	out := make([]*domain.Skill, 0, len(names))
	for i, n := range names {
		out = append(out, &domain.Skill{
			ID:        string(rune('a' + i)),
			CompanyID: companyID,
			Name:      n,
			WhenToUse: "The user asks about " + n + ".",
			Body:      "Do the thing.",
			Enabled:   true,
			Source:    domain.SkillSourceTenant,
		})
	}
	return out
}

// **The cost case, and the one this whole design turns on.** A tenant under the
// bound loses nothing to truncation, so a ranker could only reorder lines the
// model was going to see anyway — and reordering them would move the system
// prompt, which is the cached prefix. Nothing may ask for a vector here.
func TestTheIndexIsNotRankedWhileEverythingFits(t *testing.T) {
	repo := newRankingRepo(rankFixture("co-1", "Alpha", "Beta", "Gamma")...)
	r := (&ChatRunner{}).WithSkills(repo, 20, 4000)

	calls := 0
	block := r.skillIndex(context.Background(), "co-1", nil, countingVector([]float32{0.1}, &calls))

	if calls != 0 {
		t.Errorf("the question was embedded %d times on a turn that dropped nothing", calls)
	}
	if repo.rankedCalls != 0 {
		t.Errorf("the ranked query ran %d times below the bound", repo.rankedCalls)
	}
	if !strings.Contains(block, "- Alpha") || !strings.Contains(block, "- Gamma") {
		t.Errorf("the index lost a skill it had room for:\n%s", block)
	}
	if strings.Index(block, "- Alpha") > strings.Index(block, "- Gamma") {
		t.Errorf("the order moved without a ranking:\n%s", block)
	}
}

// Over the bound, the alphabet is no longer an acceptable way to decide which
// procedures a tenant keeps: "Gamma" is invisible on every turn forever because
// of its first letter.
func TestTheIndexIsRankedOnceItHasToDropSomething(t *testing.T) {
	repo := newRankingRepo(rankFixture("co-1", "Alpha", "Beta", "Gamma")...)
	r := (&ChatRunner{}).WithSkills(repo, 2, 4000)

	calls := 0
	block := r.skillIndex(context.Background(), "co-1", nil, countingVector([]float32{0.1}, &calls))

	if calls != 1 {
		t.Errorf("the question was embedded %d times, want exactly one", calls)
	}
	if repo.rankedCalls != 1 {
		t.Errorf("the ranked query ran %d times, want one", repo.rankedCalls)
	}
	// The fake ranks in reverse, so the survivors are the two the alphabet
	// would have dropped. That inversion is the whole point of the ticket.
	if !strings.Contains(block, "- Gamma") {
		t.Errorf("the ranked index dropped the skill the ranking put first:\n%s", block)
	}
	if strings.Contains(block, "- Alpha") {
		t.Errorf("the ranked index kept the skill the ranking put last:\n%s", block)
	}
}

// A tenant with no embedding credentials is over the bound and has nothing to
// rank with. They must still get an index — a worse order is better than no
// procedures at all, and the alphabetical one is what T-K3 shipped.
func TestARankingWithNoVectorKeepsTheAlphabeticalIndex(t *testing.T) {
	repo := newRankingRepo(rankFixture("co-1", "Alpha", "Beta", "Gamma")...)
	r := (&ChatRunner{}).WithSkills(repo, 2, 4000)

	calls := 0
	block := r.skillIndex(context.Background(), "co-1", nil, countingVector(nil, &calls))

	if calls != 1 {
		t.Errorf("the vector was asked for %d times, want one attempt", calls)
	}
	if repo.rankedCalls != 0 {
		t.Errorf("the ranked query ran with no vector to rank against")
	}
	if !strings.Contains(block, "- Alpha") || strings.Contains(block, "- Gamma") {
		t.Errorf("this is not the alphabetical block:\n%s", block)
	}
}

// The same guarantee one layer down: the ranked query itself failing is not a
// turn without procedures.
func TestARankingErrorKeepsTheAlphabeticalIndex(t *testing.T) {
	repo := newRankingRepo(rankFixture("co-1", "Alpha", "Beta", "Gamma")...)
	repo.rankErr = errors.New("pgvector unavailable")
	r := (&ChatRunner{}).WithSkills(repo, 2, 4000)

	calls := 0
	block := r.skillIndex(context.Background(), "co-1", nil, countingVector([]float32{0.1}, &calls))

	if !strings.Contains(block, "- Alpha") || !strings.Contains(block, "- Beta") {
		t.Errorf("a failed ranking cost the tenant their index:\n%s", block)
	}
}

// The binding is a permission and the ranking is a preference, so the ranking
// must not be able to promote a skill this agent was never offered.
func TestRankingCannotPromoteASkillOutsideTheAgentsBinding(t *testing.T) {
	rows := rankFixture("co-1", "Alpha", "Beta", "Gamma")
	repo := newRankingRepo(rows...)
	// One line, so the ranker actually runs: at two the bound skills both fit
	// and nothing is dropped, which would make this test pass without ever
	// reaching the code it names.
	r := (&ChatRunner{}).WithSkills(repo, 1, 4000)

	// `Gamma` is what the fake ranker puts first, and this agent is not bound
	// to it.
	agent := &domain.Agent{ID: "agent-1", SkillIDs: []string{rows[0].ID, rows[1].ID}}
	calls := 0
	block := r.skillIndex(context.Background(), "co-1", agent, countingVector([]float32{0.1}, &calls))

	if repo.rankedCalls != 1 {
		t.Fatalf("the ranked query ran %d times; this test is not exercising the ranker", repo.rankedCalls)
	}
	if strings.Contains(block, "- Gamma") {
		t.Errorf("the ranking promoted a skill outside the binding:\n%s", block)
	}
	if !strings.Contains(block, "- Beta") {
		t.Errorf("the highest-ranked bound skill is missing:\n%s", block)
	}
}
