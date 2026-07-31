package domain

import (
	"strings"
	"testing"
)

// T-B1's rendering rules. They are tested at this layer because two callers
// depend on producing the same bytes — the turn that composes the system
// prompt and the dashboard panel that promises the tenant it is showing them
// exactly what the agent reads.

func TestAnEmptyProfileRendersNoBlock(t *testing.T) {
	for name, p := range map[string]*CompanyProfile{
		"no profile at all":     nil,
		"a row with no content": {CompanyID: "co-1", FiscalYearStartMonth: 1},
		"whitespace only":       {CompanyID: "co-1", Industry: "  ", Description: "\n", ContextNotes: " \t"},
	} {
		t.Run(name, func(t *testing.T) {
			block, truncated := p.ContextBlock()
			if block != "" {
				t.Errorf("block = %q, want empty — an empty profile must leave the prompt untouched", block)
			}
			if truncated {
				t.Error("truncated = true for an empty profile")
			}
		})
	}
}

func TestTheBlockCarriesWhatTheTenantWrote(t *testing.T) {
	p := &CompanyProfile{
		Industry:             "Grocery retail",
		Description:          "38 stores across Java.",
		ContextNotes:         "Basket size means items per order.",
		FiscalYearStartMonth: 4,
	}
	block, truncated := p.ContextBlock()
	if truncated {
		t.Fatal("a three-line profile was truncated")
	}
	for _, want := range []string{
		"Grocery retail", "38 stores across Java.", "Basket size means items per order.", "April",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("block = %q, want it to contain %q", block, want)
		}
	}
}

// A fiscal year starting in January is the default and the common case. Saying
// so on every turn of every agent is tokens for nothing; any other month
// changes what "last quarter" resolves to and is worth its cost.
func TestJanuaryIsNotWorthATurnLine(t *testing.T) {
	january := &CompanyProfile{Description: "We sell things.", FiscalYearStartMonth: 1}
	block, _ := january.ContextBlock()
	if strings.Contains(block, "Fiscal year") {
		t.Errorf("block = %q, want no fiscal line for a January fiscal year", block)
	}

	october := &CompanyProfile{Description: "We sell things.", FiscalYearStartMonth: 10}
	block, _ = october.ContextBlock()
	if !strings.Contains(block, "October") {
		t.Errorf("block = %q, want the fiscal line for a non-January year", block)
	}
}

// The cap is the whole point of rendering this centrally: the profile is
// tenant-editable text that joins the system prompt on every turn, so it is
// both a cost multiplier nobody sees a meter for and a way to push the real
// rules out of a context window.
func TestAnEssayIsCappedAndSaysSo(t *testing.T) {
	p := &CompanyProfile{
		Description:  strings.Repeat("a", 10000),
		ContextNotes: strings.Repeat("b", 10000),
	}
	block, truncated := p.ContextBlock()
	if !truncated {
		t.Fatal("a 20,000-character profile was not reported as truncated")
	}
	if got := len([]rune(block)); got > companyContextMaxChars {
		t.Errorf("block = %d chars, want at most %d (%d tokens)",
			got, companyContextMaxChars, CompanyContextMaxTokens)
	}
	if !strings.HasSuffix(block, truncationMarker) {
		t.Error("the cut is invisible; a truncated block must say it was truncated")
	}
	// The tenant's own text still leads it — the cap takes the tail, never the
	// beginning, so the most important thing they wrote is what survives.
	if !strings.Contains(block, "What this business does: aaa") {
		t.Error("the description did not survive the cap")
	}
}

// The marker is inside the budget rather than added to it: a cap that can be
// exceeded by the sentence announcing the cap is not a cap.
func TestTheMarkerCountsAgainstTheCap(t *testing.T) {
	p := &CompanyProfile{Description: strings.Repeat("x", companyContextMaxChars+50)}
	block, truncated := p.ContextBlock()
	if !truncated {
		t.Fatal("want truncated")
	}
	if got := len([]rune(block)); got > companyContextMaxChars {
		t.Errorf("block = %d chars including the marker, want at most %d", got, companyContextMaxChars)
	}
}

// Cutting mid-rune would put a replacement character into the system prompt of
// every turn a Japanese or Indonesian tenant takes.
func TestTheCutLandsOnARuneBoundary(t *testing.T) {
	p := &CompanyProfile{Description: strings.Repeat("東京の店舗", 1000)}
	block, truncated := p.ContextBlock()
	if !truncated {
		t.Fatal("want truncated")
	}
	if strings.ContainsRune(block, '�') {
		t.Error("the block contains a replacement character; the cut split a rune")
	}
}
