package domain

import (
	"strings"
	"testing"
	"time"
)

// The fold is the part of T-B2 a tenant actually reads: several sources, one
// suggestion. Tested here rather than through the service because none of it
// needs a repository, and the cases that matter are the awkward ones.

func sourceProfile(conn, industry, summary string, entities ...SourceEntity) *SourceProfile {
	return &SourceProfile{
		ConnectionID: conn,
		CompanyID:    "co-1",
		Industry:     industry,
		Summary:      summary,
		Entities:     entities,
		InferredAt:   time.Now(),
	}
}

func TestNoSourcesMeansNoSuggestion(t *testing.T) {
	if d := DraftFromSources("co-1", nil); d != nil {
		t.Errorf("draft = %+v, want nil — an empty panel is not a suggestion", d)
	}
	if d := DraftFromSources("", []*SourceProfile{sourceProfile("c1", "retail", "shops")}); d != nil {
		t.Error("a draft was folded without a company to fold it for")
	}
}

// Inference that produced nothing is not a suggestion either — otherwise the
// panel appears with an Apply button that would write an empty profile.
func TestASourceThatDescribedNothingIsNotASuggestion(t *testing.T) {
	if d := DraftFromSources("co-1", []*SourceProfile{sourceProfile("c1", "", "")}); d != nil {
		t.Errorf("draft = %+v, want nil", d)
	}
}

func TestOneSourceBecomesTheDraft(t *testing.T) {
	d := DraftFromSources("co-1", []*SourceProfile{
		sourceProfile("c1", "grocery retail", "A chain of shops.",
			SourceEntity{Table: "stores", Means: "one shop"},
			SourceEntity{Table: "skus", Means: "one sellable product"}),
	})
	if d == nil {
		t.Fatal("no draft")
	}
	switch {
	case d.Industry != "grocery retail":
		t.Errorf("industry = %q", d.Industry)
	case d.Description != "A chain of shops.":
		t.Errorf("description = %q", d.Description)
	case !strings.Contains(d.ContextNotes, "stores — one shop"):
		t.Errorf("context notes lost the entities: %q", d.ContextNotes)
	case d.Source != ProfileSourceInferred:
		t.Errorf("source = %q, want it marked as a guess", d.Source)
	case d.FiscalYearStartMonth != 1:
		t.Errorf("fiscal month = %d; no schema records a fiscal year", d.FiscalYearStartMonth)
	case d.InferredAt == nil:
		t.Error("no inferred_at; the panel cannot say when we guessed")
	}
}

// The repository returns the default source first, and the first non-empty
// industry wins. A tenant should not have to referee an argument between their
// warehouse and their CRM.
func TestTheFirstSourceNamesTheIndustry(t *testing.T) {
	d := DraftFromSources("co-1", []*SourceProfile{
		sourceProfile("c1", "", "A CRM with no obvious industry."),
		sourceProfile("c2", "grocery retail", "A warehouse of shop data."),
		sourceProfile("c3", "logistics", "A fleet database."),
	})
	if d.Industry != "grocery retail" {
		t.Errorf("industry = %q, want the first non-empty one", d.Industry)
	}
	if !strings.Contains(d.Description, "CRM") || !strings.Contains(d.Description, "fleet") {
		t.Errorf("every source's summary should survive the fold: %q", d.Description)
	}
}

// The block has a 600-token cap waiting downstream; a draft that arrives
// already too long is one the tenant has to trim before they can use it.
func TestTheFoldBoundsWhatItPutsInFrontOfATenant(t *testing.T) {
	var many []SourceEntity
	for i := 0; i < 40; i++ {
		many = append(many, SourceEntity{Table: "t" + string(rune('a'+i%26)) + string(rune('0'+i/26)), Means: "a row"})
	}
	d := DraftFromSources("co-1", []*SourceProfile{
		sourceProfile("c1", strings.Repeat("x", 400), "A business.", many...),
	})
	lines := strings.Count(d.ContextNotes, "\n")
	switch {
	case lines > DraftMaxEntities:
		t.Errorf("entity lines = %d, want at most %d", lines, DraftMaxEntities)
	case len([]rune(d.Industry)) > DraftIndustryMax:
		t.Errorf("industry = %d runes, want at most %d", len([]rune(d.Industry)), DraftIndustryMax)
	}
}

// An entity with a table but no meaning describes nothing, and would render as
// a bare table name in a list headed "what the main tables mean".
func TestHalfAnEntityIsDropped(t *testing.T) {
	d := DraftFromSources("co-1", []*SourceProfile{
		sourceProfile("c1", "retail", "Shops.",
			SourceEntity{Table: "stores", Means: ""},
			SourceEntity{Table: "", Means: "one shop"},
			SourceEntity{Table: "skus", Means: "one product"}),
	})
	if strings.Count(d.ContextNotes, "\n") != 1 {
		t.Errorf("context notes = %q, want only the complete entity", d.ContextNotes)
	}
}
