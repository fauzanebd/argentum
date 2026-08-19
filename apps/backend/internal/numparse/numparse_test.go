package numparse

import (
	"math"
	"testing"
)

// The union of the two test sets this package was promoted out of. Both are
// kept verbatim in intent, because the promotion's whole risk is that one
// caller's behaviour changed on the way in — `guardrails.parseFigure` read
// "1234.000" as 1,234,000 and `guardrails.parseLoose` read it as 1234, and
// exactly one of those can survive. The loose reading wins: it is the one with
// a defect behind it (a driver rendering DECIMAL(…,3)), and the one whose
// mistake is bounded — reading a group as a decimal is off by a thousand,
// reading a decimal as a group is off by a thousand *and* looks plausible.
func TestParse(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want float64
		ok   bool
	}{
		{"21,23", 21.23, true}, // ID magnitude: "Rp 21,23 Miliar"
		{"21.23", 21.23, true}, // EN magnitude: "Rp 21.23 billion"
		{"12.462.599,03", 12462599.03, true},
		{"12,462,599.03", 12462599.03, true},
		{"3.863.405.700", 3863405700, true},
		{"3,863,405,700", 3863405700, true},
		{"12.500", 12500, true},  // ID thousands — a group is 1–3 digits then 3
		{"1,234", 1234, true},    // EN thousands, same shape
		{"1234.000", 1234, true}, // a driver rendering DECIMAL(…,3), not 1.234m
		{"3863405700.00", 3863405700, true},
		{"1.234,56", 1234.56, true},
		{"1,234.56", 1234.56, true},
		{"1.23", 1.23, true},
		{"12,3456", 12.3456, true},
		{"1348", 1348, true},
		{"", 0, false},
		{"abc", 0, false},
	} {
		got, ok := Parse(tc.raw)
		if ok != tc.ok {
			t.Errorf("Parse(%q) ok = %v, want %v", tc.raw, ok, tc.ok)
			continue
		}
		if ok && math.Abs(got-tc.want) > 0.0001 {
			t.Errorf("Parse(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

// ParseWithDecimal is the entry point a column uses once it has voted, and the
// point of it is that the *same string* reads differently depending on what the
// column decided. If these two rows ever agree, the column vote is doing
// nothing and T-P4's per-column decision is decoration.
func TestParseWithDecimalObeysTheColumn(t *testing.T) {
	if v, ok := ParseWithDecimal("1.234", ','); !ok || v != 1234 {
		t.Errorf("with ',' as the decimal, \"1.234\" = %v (%v), want 1234", v, ok)
	}
	if v, ok := ParseWithDecimal("1.234", '.'); !ok || v != 1.234 {
		t.Errorf("with '.' as the decimal, \"1.234\" = %v (%v), want 1.234", v, ok)
	}
	if v, ok := ParseWithDecimal("1.234.567,89", ','); !ok || math.Abs(v-1234567.89) > 1e-6 {
		t.Errorf("ID full form = %v (%v), want 1234567.89", v, ok)
	}
	if v, ok := ParseWithDecimal("1,234,567.89", '.'); !ok || math.Abs(v-1234567.89) > 1e-6 {
		t.Errorf("EN full form = %v (%v), want 1234567.89", v, ok)
	}
	// An unusable separator hands the decision back rather than guessing with it.
	if v, ok := ParseWithDecimal("3.863.405.700", 'x'); !ok || v != 3863405700 {
		t.Errorf("fallback = %v (%v), want the structural reading", v, ok)
	}
	if _, ok := ParseWithDecimal("", '.'); ok {
		t.Error("an empty cell parsed as a number")
	}
}

func TestMagnitudes(t *testing.T) {
	for word, want := range map[string]float64{
		"juta": 1e6, "Juta": 1e6, " MILIAR ": 1e9, "milyar": 1e9,
		"thousand": 1e3, "trillion": 1e12, "triliun": 1e12,
	} {
		if got, ok := Magnitude(word); !ok || got != want {
			t.Errorf("Magnitude(%q) = %v (%v), want %v", word, got, ok, want)
		}
	}
	if _, ok := Magnitude("gazillion"); ok {
		t.Error("an invented magnitude word was recognised")
	}
	en, id, ok := CanonicalMagnitude(1e9)
	if !ok || en != "billion" || id != "miliar" {
		t.Errorf("CanonicalMagnitude(1e9) = %q/%q (%v)", en, id, ok)
	}
	if _, _, ok := CanonicalMagnitude(1e5); ok {
		t.Error("a multiplier no word names was given canonical spellings")
	}
}

// The alternation order is load-bearing for any caller building a regexp out of
// this list: "miliar" before "milyar" is irrelevant, but a short word that is a
// prefix of a long one would match first and swallow the rest.
func TestMagnitudeWordsAreOrderedLongestFirst(t *testing.T) {
	words := MagnitudeWords()
	if len(words) != 9 {
		// Nine, not eight: Indonesian spells the billion twice — "miliar" and
		// "milyar" — and both are in real replies.
		t.Fatalf("got %d magnitude words, want 9", len(words))
	}
	for i := 1; i < len(words); i++ {
		if len(words[i]) > len(words[i-1]) {
			t.Fatalf("word %q (len %d) follows %q (len %d); the order is not longest-first",
				words[i], len(words[i]), words[i-1], len(words[i-1]))
		}
	}
	// Deterministic across calls, which map iteration would not be.
	again := MagnitudeWords()
	for i := range words {
		if words[i] != again[i] {
			t.Fatalf("the word order changed between calls at %d: %q then %q", i, words[i], again[i])
		}
	}
}
