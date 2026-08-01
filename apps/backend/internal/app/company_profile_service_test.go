package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// T-B1's write rules. Provenance is the one with teeth: T-B2 drafts profiles,
// and an inferred profile the tenant has never read must stay distinguishable
// from words somebody actually chose (locked decision 2).

// fakeProfileRepo is one row per company, in memory.
type fakeProfileRepo struct {
	rows map[string]*domain.CompanyProfile
	err  error
}

func newProfileRepo() *fakeProfileRepo {
	return &fakeProfileRepo{rows: map[string]*domain.CompanyProfile{}}
}

func (f *fakeProfileRepo) GetByCompany(_ context.Context, companyID string) (*domain.CompanyProfile, error) {
	if f.err != nil {
		return nil, f.err
	}
	p, ok := f.rows[companyID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	copied := *p
	return &copied, nil
}

func (f *fakeProfileRepo) Upsert(_ context.Context, p *domain.CompanyProfile) error {
	if f.err != nil {
		return f.err
	}
	copied := *p
	f.rows[p.CompanyID] = &copied
	return nil
}

func profileSvc() (*CompanyProfileService, *fakeProfileRepo) {
	repo := newProfileRepo()
	return NewCompanyProfileService(repo), repo
}

// suggestingSvc is the same service with T-B2's half wired: the drafts
// inference wrote, and the credit check that explains their absence.
func suggestingSvc(verdict BudgetVerdict) (*CompanyProfileService, *fakeProfileRepo, *fakeSourceProfileRepo) {
	repo := newProfileRepo()
	sources := newSourceProfileRepo()
	svc := NewCompanyProfileService(repo).WithSuggestions(sources, fakeBudget{verdict: verdict})
	return svc, repo, sources
}

func storeDraft(sources *fakeSourceProfileRepo, connID string) {
	sources.rows[connID] = &domain.SourceProfile{
		ConnectionID: connID,
		CompanyID:    "co-1",
		Industry:     "grocery retail",
		Summary:      "A chain of shops selling packaged goods.",
		Entities:     []domain.SourceEntity{{Table: "stores", Means: "one shop"}},
		InferredAt:   time.Now(),
	}
}

// The suggestion is a draft and stays one until somebody presses Apply
// (locked decision 2).
func TestAnInferredDraftIsNotWrittenUntilApplied(t *testing.T) {
	svc, repo, sources := suggestingSvc(BudgetOK)
	storeDraft(sources, "conn-1")

	sug, err := svc.Suggest(context.Background(), "co-1")
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	switch {
	case sug.Draft == nil:
		t.Fatal("no draft offered for a company whose source was described")
	case sug.Sources != 1:
		t.Errorf("sources = %d, want 1", sug.Sources)
	case repo.rows["co-1"] != nil:
		t.Error("reading the suggestion wrote a profile")
	}

	p, err := svc.ApplySuggestion(context.Background(), "co-1", "user-1")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	switch {
	case repo.rows["co-1"] == nil:
		t.Fatal("apply wrote nothing")
	case p.Source != domain.ProfileSourceInferred:
		t.Errorf("source = %q, want %q — an applied guess is still a guess", p.Source, domain.ProfileSourceInferred)
	case p.InferredAt == nil:
		t.Error("no inferred_at survived the apply")
	case p.UpdatedBy != "user-1":
		t.Errorf("updated_by = %q, want the admin who applied it", p.UpdatedBy)
	}
}

// The tenant's own words win over a guess. A stale tab, or a second admin,
// must not be able to replace a description somebody typed.
func TestApplyRefusesToOverwriteWordsSomebodyChose(t *testing.T) {
	svc, repo, sources := suggestingSvc(BudgetOK)
	storeDraft(sources, "conn-1")
	repo.rows["co-1"] = &domain.CompanyProfile{
		CompanyID:   "co-1",
		Description: "We run 38 grocery stores across Java.",
		Source:      domain.ProfileSourceHuman,
	}

	_, err := svc.ApplySuggestion(context.Background(), "co-1", "user-1")
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if repo.rows["co-1"].Description != "We run 38 grocery stores across Java." {
		t.Error("the tenant's description was overwritten by a draft")
	}
}

// Re-applying over a profile that is still an untouched guess is allowed: a
// re-scan after a schema change is exactly when a tenant wants the newer draft.
func TestApplyReplacesAnUntouchedGuess(t *testing.T) {
	svc, repo, sources := suggestingSvc(BudgetOK)
	storeDraft(sources, "conn-1")
	repo.rows["co-1"] = &domain.CompanyProfile{
		CompanyID:   "co-1",
		Description: "an older guess",
		Source:      domain.ProfileSourceInferred,
	}

	p, err := svc.ApplySuggestion(context.Background(), "co-1", "user-1")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if p.Description == "an older guess" {
		t.Error("the newer draft did not replace the stale one")
	}
}

// Nothing to apply is a 404-shaped answer, not an empty profile.
func TestApplyWithoutADraftWritesNothing(t *testing.T) {
	svc, repo, _ := suggestingSvc(BudgetOK)
	_, err := svc.ApplySuggestion(context.Background(), "co-1", "user-1")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if repo.rows["co-1"] != nil {
		t.Error("an empty profile was written for a company with no draft")
	}
}

// A company at zero balance gets silence from inference; the panel needs to be
// able to say why rather than looking broken.
func TestAnExhaustedBalanceIsReportedRatherThanSilent(t *testing.T) {
	svc, _, _ := suggestingSvc(BudgetExhausted)
	sug, err := svc.Suggest(context.Background(), "co-1")
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	switch {
	case sug.Draft != nil:
		t.Error("a draft appeared for a company that never ran inference")
	case !sug.CreditsExhausted:
		t.Error("the empty panel does not say the balance is why")
	}
}

// A deployment without inference wired keeps the T-B1 form exactly as it was.
func TestSuggestionsAreOptional(t *testing.T) {
	svc, _ := profileSvc()
	sug, err := svc.Suggest(context.Background(), "co-1")
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	if sug.Draft != nil || sug.Sources != 0 || sug.CreditsExhausted {
		t.Errorf("suggestion = %+v, want an empty one", sug)
	}
	if _, err := svc.ApplySuggestion(context.Background(), "co-1", "user-1"); err == nil {
		t.Error("apply succeeded on a deployment with no inference")
	}
}

func TestAFirstSaveIsTheTenantsOwnWords(t *testing.T) {
	svc, repo := profileSvc()
	p, err := svc.Upsert(context.Background(), "co-1", "user-1", ProfileInput{
		Industry: " Grocery retail ", Description: "38 stores.", FiscalYearStartMonth: 4,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	switch {
	case p.Source != domain.ProfileSourceHuman:
		t.Errorf("source = %q, want %q", p.Source, domain.ProfileSourceHuman)
	case p.Industry != "Grocery retail":
		t.Errorf("industry = %q, want it trimmed", p.Industry)
	case p.UpdatedBy != "user-1":
		t.Errorf("updated_by = %q, want the saving admin", p.UpdatedBy)
	case repo.rows["co-1"] == nil:
		t.Error("nothing was stored")
	}
}

// The transition this table exists for: a tenant correcting T-B2's draft
// leaves it 'inferred_edited', so the dashboard stops calling it a guess and
// the inference knows not to overwrite words somebody chose.
func TestEditingAnInferredProfileMarksItEdited(t *testing.T) {
	svc, repo := profileSvc()
	inferredAt := time.Now().Add(-24 * time.Hour)
	repo.rows["co-1"] = &domain.CompanyProfile{
		CompanyID: "co-1", Description: "guessed from the schema",
		Source: domain.ProfileSourceInferred, InferredAt: &inferredAt,
	}

	p, err := svc.Upsert(context.Background(), "co-1", "user-1",
		ProfileInput{Description: "actually we are a 3PL"})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if p.Source != domain.ProfileSourceInferredEdited {
		t.Errorf("source = %q, want %q", p.Source, domain.ProfileSourceInferredEdited)
	}
	// Carried, not cleared: when the draft was made stays true after somebody
	// corrects a sentence in it.
	if p.InferredAt == nil || !p.InferredAt.Equal(inferredAt) {
		t.Errorf("inferred_at = %v, want it carried over as %v", p.InferredAt, inferredAt)
	}

	// A second edit stays 'inferred_edited' — it never decays into "written
	// from scratch", because part of it still was not.
	p, err = svc.Upsert(context.Background(), "co-1", "user-1",
		ProfileInput{Description: "a 3PL with 4 warehouses"})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if p.Source != domain.ProfileSourceInferredEdited {
		t.Errorf("source = %q after a second edit, want %q", p.Source, domain.ProfileSourceInferredEdited)
	}
}

func TestEditingAHumanProfileStaysHuman(t *testing.T) {
	svc, repo := profileSvc()
	repo.rows["co-1"] = &domain.CompanyProfile{
		CompanyID: "co-1", Description: "typed by an admin", Source: domain.ProfileSourceHuman,
	}
	p, err := svc.Upsert(context.Background(), "co-1", "user-2", ProfileInput{Description: "still typed"})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if p.Source != domain.ProfileSourceHuman {
		t.Errorf("source = %q, want %q", p.Source, domain.ProfileSourceHuman)
	}
}

// One company's save must not reach another's row, and one company's read must
// not see another's words — this text becomes the system prompt of every agent
// the tenant runs.
func TestOneCompanyCannotSeeAnothersProfile(t *testing.T) {
	svc, _ := profileSvc()
	ctx := context.Background()
	if _, err := svc.Upsert(ctx, "co-1", "user-1", ProfileInput{Description: "co-1's business"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := svc.Get(ctx, "co-2"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("get for another company = %v, want ErrNotFound", err)
	}

	if _, err := svc.Upsert(ctx, "co-2", "user-9", ProfileInput{Description: "co-2's business"}); err != nil {
		t.Fatalf("upsert co-2: %v", err)
	}
	p, err := svc.Get(ctx, "co-1")
	if err != nil {
		t.Fatalf("get co-1: %v", err)
	}
	if p.Description != "co-1's business" {
		t.Errorf("co-1's description = %q after co-2 saved; the rows are not separate", p.Description)
	}
}

// The essay is accepted and the block is what gets cut. A tenant pasting a
// draft they are about to trim should not meet a rejected save.
func TestAnEssayIsStoredAndTruncatedOnlyInTheBlock(t *testing.T) {
	svc, _ := profileSvc()
	long := strings.Repeat("a", 20000)
	p, err := svc.Upsert(context.Background(), "co-1", "user-1", ProfileInput{Description: long})
	if err != nil {
		t.Fatalf("upsert of a 20,000-character profile: %v", err)
	}
	if p.Description != long {
		t.Errorf("stored description = %d chars, want all %d", len(p.Description), len(long))
	}
	block, truncated := p.ContextBlock()
	if !truncated || len([]rune(block)) > domain.CompanyContextMaxTokens*4 {
		t.Errorf("block = %d chars, truncated = %v; want it capped", len([]rune(block)), truncated)
	}
}

func TestTheProfileIsValidated(t *testing.T) {
	svc, _ := profileSvc()
	cases := map[string]ProfileInput{
		"an industry that is really a description": {Industry: strings.Repeat("x", profileIndustryMax+1)},
		"a description past the storage limit":     {Description: strings.Repeat("x", profileTextMax+1)},
		"notes past the storage limit":             {ContextNotes: strings.Repeat("x", profileTextMax+1)},
		"a thirteenth month":                       {FiscalYearStartMonth: 13},
		"a negative month":                         {FiscalYearStartMonth: -1},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.Upsert(context.Background(), "co-1", "user-1", in); !errors.Is(err, domain.ErrInvalidInput) {
				t.Errorf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

// An omitted month is January, not a validation error: a client that only
// wants to set a description should not have to know what a fiscal year is.
func TestAnOmittedFiscalMonthIsJanuary(t *testing.T) {
	svc, _ := profileSvc()
	p, err := svc.Upsert(context.Background(), "co-1", "user-1", ProfileInput{Description: "we sell things"})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if p.FiscalYearStartMonth != 1 {
		t.Errorf("fiscal month = %d, want 1", p.FiscalYearStartMonth)
	}
}
