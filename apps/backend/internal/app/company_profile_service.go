package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
)

// The company business profile (T-B1).
//
// One row per company, read by every turn of every agent, written from one
// form. Nothing here renders the block — that is domain.CompanyProfile.
// ContextBlock, so the dashboard's preview and the model's prompt are produced
// by the same code and cannot drift.

const (
	// profileIndustryMax bounds a label: "grocery retail", "3PL logistics".
	// Prose belongs in the description, and an industry that needs a paragraph
	// is a description with the wrong heading.
	profileIndustryMax = 120
	// profileTextMax bounds the two free-text fields on the way into storage.
	// Deliberately far above the 600-token block cap: a tenant who pastes too
	// much gets a truncated block and a warning in the UI, not a rejected save
	// (they may be pasting a draft they are about to trim). The cap here is a
	// storage sanity limit, not the context budget.
	profileTextMax = 20000
)

// CompanyProfileService is the read/write surface behind Settings → Business
// profile.
type CompanyProfileService struct {
	repo domain.CompanyProfileRepository
}

func NewCompanyProfileService(repo domain.CompanyProfileRepository) *CompanyProfileService {
	return &CompanyProfileService{repo: repo}
}

// ProfileInput is one submitted profile. Every field is replaced on save: this
// is a form with four inputs, not a patch API, and a partial write would let a
// client that does not know about a field silently blank it either way.
type ProfileInput struct {
	Industry             string `json:"industry"`
	Description          string `json:"description"`
	ContextNotes         string `json:"context_notes"`
	FiscalYearStartMonth int    `json:"fiscal_year_start_month"`
}

// Get returns the company's profile, or domain.ErrNotFound when it has none.
//
// The absence is passed through rather than smoothed into a zero value,
// because the two callers want different things from it: the HTTP layer
// answers 200 with an empty form, and a turn composes no block at all.
func (s *CompanyProfileService) Get(ctx context.Context, companyID string) (*domain.CompanyProfile, error) {
	return s.repo.GetByCompany(ctx, companyID)
}

// Upsert writes the profile and returns what was stored.
//
// Provenance is decided here and nowhere else (locked decision 2): a tenant
// editing a draft T-B2 inferred leaves the row marked 'inferred_edited', so
// the dashboard can stop calling it a guess and T-B2 can tell an untouched
// guess from words somebody actually chose. A row that was already the
// tenant's own stays 'human'; a first save into nothing is 'human'.
func (s *CompanyProfileService) Upsert(
	ctx context.Context, companyID, userID string, in ProfileInput,
) (*domain.CompanyProfile, error) {
	p, err := s.validated(companyID, in)
	if err != nil {
		return nil, err
	}

	current, err := s.repo.GetByCompany(ctx, companyID)
	switch {
	case err == nil:
		p.Source = editedSource(current.Source)
		// Carried, not cleared: when T-B2 drafted this profile is a fact about
		// the draft, and it stays true after somebody corrects a sentence.
		p.InferredAt = current.InferredAt
	case errors.Is(err, domain.ErrNotFound):
		p.Source = domain.ProfileSourceHuman
	default:
		return nil, err
	}
	p.UpdatedBy = userID

	if err := s.repo.Upsert(ctx, p); err != nil {
		return nil, err
	}
	block, truncated := p.ContextBlock()
	logrus.WithFields(logrus.Fields{
		"company_id": companyID, "source": p.Source,
		"block_chars": len(block), "truncated": truncated,
	}).Info("company profile saved")
	return p, nil
}

// editedSource is the provenance transition, written once. An inferred profile
// somebody edited is neither the guess it was nor text written from scratch,
// and flattening it to 'human' would lose the only signal that says which
// sentences we wrote.
func editedSource(current domain.ProfileSource) domain.ProfileSource {
	switch current {
	case domain.ProfileSourceInferred, domain.ProfileSourceInferredEdited:
		return domain.ProfileSourceInferredEdited
	default:
		return domain.ProfileSourceHuman
	}
}

// validated turns submitted input into a profile, or into the reason it is not
// one.
func (s *CompanyProfileService) validated(companyID string, in ProfileInput) (*domain.CompanyProfile, error) {
	industry := strings.TrimSpace(in.Industry)
	description := strings.TrimSpace(in.Description)
	notes := strings.TrimSpace(in.ContextNotes)
	month := in.FiscalYearStartMonth
	// An omitted month is January rather than an error: the field is a
	// refinement, and a client that only wants to set a description should not
	// have to know what a fiscal year is to do it.
	if month == 0 {
		month = 1
	}
	switch {
	case companyID == "":
		return nil, fmt.Errorf("%w: a company is required", domain.ErrInvalidInput)
	case len([]rune(industry)) > profileIndustryMax:
		return nil, fmt.Errorf("%w: industry must be %d characters or fewer", domain.ErrInvalidInput, profileIndustryMax)
	case len([]rune(description)) > profileTextMax:
		return nil, fmt.Errorf("%w: description must be %d characters or fewer", domain.ErrInvalidInput, profileTextMax)
	case len([]rune(notes)) > profileTextMax:
		return nil, fmt.Errorf("%w: context notes must be %d characters or fewer", domain.ErrInvalidInput, profileTextMax)
	case month < 1 || month > 12:
		return nil, fmt.Errorf("%w: fiscal year start month must be between 1 and 12", domain.ErrInvalidInput)
	}
	return &domain.CompanyProfile{
		CompanyID:            companyID,
		Industry:             industry,
		Description:          description,
		ContextNotes:         notes,
		FiscalYearStartMonth: month,
	}, nil
}
