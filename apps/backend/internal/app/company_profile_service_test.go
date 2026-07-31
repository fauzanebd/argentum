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
