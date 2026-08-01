package domain

import (
	"context"
	"strings"
	"time"
)

// SourceProfile is what one connected database looks like it is for (T-B2).
//
// The tenant already told us what their business is; they told us in DDL. A
// warehouse with `stores`, `skus`, `stock_movements` and `order_items` is a
// retailer, and asking the person who just connected it to type "we are a
// retailer" is asking them to do work we can do. This row is that work's
// output.
//
// It is a draft and only a draft. Nothing here reaches a turn: the agent reads
// CompanyProfile, and a SourceProfile becomes part of one only when a human
// presses Apply (locked decision 2). An inferred profile that silently became
// the agent's view of the business would be a fabrication with a UI.
//
// Everything in it is derived from names the tenant's DBA chose, which are
// untrusted input (locked decision 5) — see BusinessInference's prompt framing
// and the entity validation that drops tables the schema does not have.
type SourceProfile struct {
	ConnectionID string `json:"connection_id"`
	CompanyID    string `json:"company_id"`
	// Industry is the one-line label this source suggests: "grocery retail".
	// Per-source because a company's CRM and its warehouse can imply the same
	// industry from different angles, and the fold picks one.
	Industry string `json:"industry"`
	// Summary is one short paragraph: what this database appears to be for.
	Summary string `json:"summary"`
	// Entities are the tables worth naming, with what a row in each means.
	// Every Table here exists in the introspected schema — see
	// keepKnownEntities for why that check is a security control and not
	// tidiness.
	Entities []SourceEntity `json:"entities"`
	// SchemaFingerprint is the hash of the table and column names this was
	// inferred from. Re-running against an unchanged schema must not spend a
	// second LLM call, and this is the whole mechanism for that.
	SchemaFingerprint string `json:"schema_fingerprint"`
	// Model is which LLM wrote it, recorded because a draft that reads badly is
	// a question about a model version six weeks later.
	Model      string    `json:"model"`
	InferredAt time.Time `json:"inferred_at"`
}

// SourceEntity is one table and what a row in it means to the business.
type SourceEntity struct {
	Table string `json:"table"`
	Means string `json:"means"`
}

// SourceProfileRepository persists per-source inference drafts.
//
// GetByConnection answers ErrNotFound for "this source has never been
// inferred", which every caller treats as "no draft" and none treats as a
// failure: a company that has connected a database and not yet run inference is
// in the state every company starts in.
type SourceProfileRepository interface {
	GetByConnection(ctx context.Context, companyID, connectionID string) (*SourceProfile, error)
	ListByCompany(ctx context.Context, companyID string) ([]*SourceProfile, error)
	Upsert(ctx context.Context, p *SourceProfile) error
}

// Draft limits. They bound what the fold puts in front of a tenant, not what
// the model may return — the service clamps its own output before storing, and
// these are what a *company* draft is allowed to grow to once several sources
// have each contributed.
const (
	// DraftMaxEntities is how many table meanings the context-notes field
	// carries. Twelve lines is a screen; a draft nobody reads to the end is a
	// draft nobody can correct, and the profile block has a 600-token cap
	// waiting for it either way.
	DraftMaxEntities = 12
	// DraftIndustryMax matches the industry field's own storage limit so an
	// applied draft cannot be rejected by the validation that guards the form.
	DraftIndustryMax = 120
)

// DraftFromSources folds every source profile a company has into one draft
// CompanyProfile marked 'inferred'.
//
// A pure function over rows the caller already loaded, because it is the part
// worth testing exhaustively: one source, three sources that disagree about the
// industry, a source whose inference produced nothing. It never touches a
// repository and never writes — the caller decides whether a human has asked
// for this to become the company's profile.
//
// Returns nil when there is nothing to suggest. "No draft" and "an empty draft"
// are different answers, and only the first one should leave the review panel
// off the screen.
func DraftFromSources(companyID string, profiles []*SourceProfile) *CompanyProfile {
	if companyID == "" || len(profiles) == 0 {
		return nil
	}

	var industry string
	var descriptions []string
	var entityLines []string
	var latest time.Time

	for _, p := range profiles {
		if p == nil {
			continue
		}
		// First non-empty industry wins rather than a vote: the sources arrive
		// in the repository's order (default source first), and a company whose
		// warehouse says "grocery retail" while its CRM says "retail" is better
		// served by the more specific first answer than by an argument the
		// tenant then has to referee.
		if industry == "" {
			industry = ClampRunes(strings.TrimSpace(p.Industry), DraftIndustryMax)
		}
		if s := strings.TrimSpace(p.Summary); s != "" {
			descriptions = append(descriptions, s)
		}
		for _, e := range p.Entities {
			table := strings.TrimSpace(e.Table)
			means := strings.TrimSpace(e.Means)
			if table == "" || means == "" {
				continue
			}
			if len(entityLines) >= DraftMaxEntities {
				break
			}
			entityLines = append(entityLines, table+" — "+means)
		}
		if p.InferredAt.After(latest) {
			latest = p.InferredAt
		}
	}

	if industry == "" && len(descriptions) == 0 && len(entityLines) == 0 {
		return nil
	}

	draft := &CompanyProfile{
		CompanyID:   companyID,
		Industry:    industry,
		Description: strings.Join(descriptions, "\n\n"),
		// Fiscal year is not in here and cannot be: it is a policy decision no
		// schema records, and a guess at it would silently change what "last
		// quarter" resolves to in every answer. January is the form's default
		// and the tenant's to change.
		FiscalYearStartMonth: 1,
		Source:               ProfileSourceInferred,
	}
	if len(entityLines) > 0 {
		draft.ContextNotes = "What the main tables mean:\n" + strings.Join(entityLines, "\n")
	}
	if !latest.IsZero() {
		t := latest
		draft.InferredAt = &t
	}
	return draft
}

// ClampRunes cuts on a rune boundary. Table names and model output are both
// UTF-8, and a byte slice through a multi-byte character is how a summary
// arrives at the dashboard with a replacement glyph in it.
//
// Exported because the inference service clamps the same values on the way in
// that the fold clamps on the way out, and two copies of this would be two
// answers to how long an industry label may be.
func ClampRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max]))
}
