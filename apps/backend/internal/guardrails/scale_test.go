package guardrails

import (
	"strings"
	"testing"
)

// The two observed defects, verbatim. Both passed T-16's check — the figure is
// real and tool-derived — and both told the reader a number a thousand times
// smaller than the one beside it.
func TestCheckScaleCorrectsTheObservedCases(t *testing.T) {
	cases := []struct {
		name  string
		reply string
		want  string
	}{
		{
			name:  "the watcher briefing, 2026-08-02",
			reply: "Revenue for December closed at $3,863,405,700 (approximately $3.86 million).",
			want:  "$3.86 billion",
		},
		{
			name:  "Indonesian, and the unit stays Indonesian",
			reply: "Total penjualan Desember Rp 3.863.405.700 (sekitar Rp 3,86 juta).",
			want:  "3,86 miliar",
		},
		{
			name:  "no brackets, and the capitalisation is carried",
			reply: "December revenue was 3,863,405,700 — approximately 3.86 Million.",
			want:  "3.86 Billion",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			out, fixes := CheckScale(tt.reply)
			if len(fixes) != 1 {
				t.Fatalf("fixes = %v, want exactly one", fixes)
			}
			if !strings.Contains(out, tt.want) {
				t.Errorf("out = %q, want it to contain %q", out, tt.want)
			}
		})
	}
}

// A restatement that is right is left alone, including the rounding it exists
// for. 3.86 billion for 3,863,405,700 is 0.09% off and is what the system
// prompt asks the agent to write.
func TestCheckScaleLeavesCorrectRestatementsAlone(t *testing.T) {
	for _, reply := range []string{
		"Revenue for December closed at $3,863,405,700 (approximately $3.86 billion).",
		"Total penjualan Desember Rp 3.863.405.700 (sekitar Rp 3,86 miliar).",
		"We booked 1,250,000 in fees (about 1.25 million).",
		"Headcount is 1,348 across three sites.",
		"I could not complete the query, so I have no figure for you.",
	} {
		out, fixes := CheckScale(reply)
		if len(fixes) != 0 {
			t.Errorf("%q was corrected to %q; it is already right", reply, out)
		}
		if out != reply {
			t.Errorf("out = %q, want the reply unchanged", out)
		}
	}
}

// The bar for rewriting a word the model wrote: the correction has to be
// derivable from the reply itself. When the two numbers genuinely disagree — a
// different failure, and not one arithmetic repairs — nothing is touched.
func TestCheckScaleWillNotInventAgreement(t *testing.T) {
	reply := "Revenue was $3,863,405,700 (approximately $7.42 million)."
	out, fixes := CheckScale(reply)
	if len(fixes) != 0 {
		t.Errorf("fixes = %v, want none: no unit makes 7.42 agree with 3.86bn", fixes)
	}
	if out != reply {
		t.Errorf("out = %q, want the reply unchanged", out)
	}
}

// Two wrong restatements in one reply are both corrected, and each keeps its own
// language.
func TestCheckScaleCorrectsEveryRestatement(t *testing.T) {
	reply := "Revenue 3,863,405,700 (approximately 3.86 million) and costs 1,200,000,000 " +
		"(approximately 1.2 thousand)."
	out, fixes := CheckScale(reply)
	if len(fixes) != 2 {
		t.Fatalf("fixes = %d, want 2", len(fixes))
	}
	if !strings.Contains(out, "3.86 billion") || !strings.Contains(out, "1.2 billion") {
		t.Errorf("out = %q, want both restatements corrected", out)
	}
}

// The parser is the part most likely to be quietly wrong, because "3.863" means
// two different numbers in the two locales this product serves.
func TestParseFigure(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"3,863,405,700", 3863405700, true},
		{"3.863.405.700", 3863405700, true},
		{"3.86", 3.86, true},
		{"3,86", 3.86, true},
		{"1,234.56", 1234.56, true},
		{"1.234,56", 1234.56, true},
		{"1.234", 1234, true}, // one separator, three digits after: a group
		{"1.23", 1.23, true},  // one separator, two digits after: a decimal
		{"12,3456", 12.3456, true},
		{"1348", 1348, true},
		{"", 0, false},
		{"abc", 0, false},
	}
	for _, tt := range cases {
		got, ok := parseFigure(tt.in)
		if ok != tt.ok {
			t.Errorf("parseFigure(%q) ok = %v, want %v", tt.in, ok, tt.ok)
			continue
		}
		if ok && got != tt.want {
			t.Errorf("parseFigure(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
