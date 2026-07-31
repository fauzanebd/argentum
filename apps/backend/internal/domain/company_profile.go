package domain

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// CompanyProfile is what business this workspace is, in the tenant's own words
// (T-B1).
//
// The agent already reads every table name through get_schema, and table names
// describe structure, not meaning. Nothing in `orders`, `stores` and
// `stock_movements` says a row in `stores` is a shop with a manager and a rent
// bill, or that a stock-out is the number the operations lead is actually
// asking about. This row is where that is written down once, for every agent —
// a company fact belongs to the company, not to five personas that drift five
// ways.
//
// It is facts, never instructions. The persona carries instructions and is
// kept in a separate block for exactly that reason (locked decision 1): a
// stale fact is corrected in one place for every agent, a wrong instruction is
// corrected on the agent that carries it.
//
// A company with no row is the ordinary case and produces no block at all —
// same rule as the roster's empty allowlist, for the same reason.
type CompanyProfile struct {
	CompanyID string `json:"company_id"`
	// Industry is the one-line label: "grocery retail", "logistics", "SaaS".
	Industry string `json:"industry"`
	// Description is what the business does. The block's substance.
	Description string `json:"description"`
	// ContextNotes is free-form: markets, seasonality, what "good" looks like.
	// One field rather than six, because we do not yet know which six.
	ContextNotes string `json:"context_notes"`
	// FiscalYearStartMonth is 1-12. It changes what "last quarter" means, which
	// is the single most common way an analytics answer is right about the
	// numbers and wrong about the period.
	FiscalYearStartMonth int `json:"fiscal_year_start_month"`
	// Source is provenance: who wrote this. T-B2 infers profiles, and an
	// inferred profile the tenant has never looked at must be distinguishable
	// from their own words — in the dashboard, and to the ticket that fills it
	// in (locked decision 2).
	Source ProfileSource `json:"source"`
	// InferredAt is when T-B2 last drafted this. Nil for a profile nobody
	// inferred.
	InferredAt *time.Time `json:"inferred_at,omitempty"`
	// UpdatedBy is the admin who last saved it, or empty once that user is
	// deleted — the column is ON DELETE SET NULL, because a departed admin
	// must not take the company's profile with them.
	UpdatedBy string    `json:"updated_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ProfileSource is how a profile came to say what it says.
type ProfileSource string

const (
	// ProfileSourceHuman is a profile the tenant typed.
	ProfileSourceHuman ProfileSource = "human"
	// ProfileSourceInferred is T-B2's draft, applied but not yet edited.
	ProfileSourceInferred ProfileSource = "inferred"
	// ProfileSourceInferredEdited is a draft the tenant has since corrected.
	// It is a third value rather than a flip to "human" because "they read our
	// guess and kept most of it" and "they wrote this from scratch" are
	// different facts about the same text.
	ProfileSourceInferredEdited ProfileSource = "inferred_edited"
)

// CompanyContextMaxTokens caps the rendered block. The profile is
// tenant-editable text that joins the system prompt on every turn of every
// agent and every channel: uncapped, it is both a cost multiplier the person
// typing it never sees a meter for and a way to push the real instructions out
// of a context window.
//
// 600 tokens is roughly a page — long enough to describe a business, short
// enough that nobody pastes their annual report into it.
const CompanyContextMaxTokens = 600

// companyContextMaxChars is the cap in the unit the code can actually measure.
// This repo carries no tokeniser — the budget guard counts what the provider
// reports, after the fact — so the block is capped on the four-characters-per
// -token approximation the ticket specifies. It errs long for English prose
// and short for CJK, and both errors are absorbed by the cap being a
// safety limit rather than an accounting figure.
const companyContextMaxChars = CompanyContextMaxTokens * 4

// truncationMarker is what a tenant sees in the preview when their profile did
// not fit. Visible on purpose: a prompt fragment silently cut in half is a
// prompt fragment nobody can debug.
const truncationMarker = "\n\n[… truncated: this profile is longer than the agent's context allowance]"

// ContextBlock renders the profile as the agent reads it, and reports whether
// the cap cut it short.
//
// One function for both the turn and the dashboard preview: "show the tenant
// exactly what the agent sees" is only true if the same code produces both
// strings. The framing that says this section describes and does not instruct
// is added where the system prompt is composed (bootstrap.frameCompanyContext)
// — that sentence is Argentum's, not the tenant's, and it belongs beside the
// persona's frame rather than inside the tenant's data.
//
// An empty profile renders an empty block, which composes to a byte-identical
// system prompt (locked decision 7).
func (p *CompanyProfile) ContextBlock() (string, bool) {
	if p == nil {
		return "", false
	}
	industry := strings.TrimSpace(p.Industry)
	description := strings.TrimSpace(p.Description)
	notes := strings.TrimSpace(p.ContextNotes)
	fiscal := p.fiscalLine()
	if industry == "" && description == "" && notes == "" && fiscal == "" {
		return "", false
	}

	var b strings.Builder
	if industry != "" {
		b.WriteString("Industry: " + industry + "\n")
	}
	if description != "" {
		b.WriteString("What this business does: " + description + "\n")
	}
	if fiscal != "" {
		b.WriteString(fiscal + "\n")
	}
	if notes != "" {
		b.WriteString("Other context: " + notes + "\n")
	}
	return capBlock(strings.TrimRight(b.String(), "\n"))
}

// fiscalLine is emitted only for a fiscal year that does not start in January.
// The default is January, most businesses use it, and a line every turn pays
// for that says "the year starts when the year starts" is tokens for nothing.
// A month that is not January changes what "last quarter" resolves to, and is
// worth every token it costs.
func (p *CompanyProfile) fiscalLine() string {
	if p.FiscalYearStartMonth < 2 || p.FiscalYearStartMonth > 12 {
		return ""
	}
	return "Fiscal year starts in " + time.Month(p.FiscalYearStartMonth).String() +
		" (calendar month " + strconv.Itoa(p.FiscalYearStartMonth) + ")"
}

// capBlock enforces CompanyContextMaxTokens, marker included, and cuts on a
// rune boundary. The marker is inside the budget rather than added to it:
// a cap that can be exceeded by the thing announcing the cap is not a cap.
func capBlock(s string) (string, bool) {
	r := []rune(s)
	if len(r) <= companyContextMaxChars {
		return s, false
	}
	keep := max(companyContextMaxChars-len([]rune(truncationMarker)), 0)
	return strings.TrimRight(string(r[:keep]), " \n\t") + truncationMarker, true
}

// CompanyProfileRepository is the persistence contract for the profile.
//
// Both methods take a company id and nothing else that could be mistaken for
// one. GetByCompany answers ErrNotFound for "this company has no profile",
// which every caller treats as "no block" and none treats as a failure — a
// turn must never stop because a settings row is absent.
type CompanyProfileRepository interface {
	GetByCompany(ctx context.Context, companyID string) (*CompanyProfile, error)
	Upsert(ctx context.Context, p *CompanyProfile) error
}
