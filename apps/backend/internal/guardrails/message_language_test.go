package guardrails

import "testing"

// A refusal answers in the language the user wrote in. What reaches this
// package is the composed prompt — the user's sentence with the chat runner's
// bracketed context blocks in front of it — so the choice has to be made on the
// question alone.
//
// Found on 2026-08-14 by the guardrail slice: "Give me a recipe for nasi goreng
// with chicken" was refused in Indonesian. The sentence has no Indonesian
// marker in it; the injected context does, because this tenant's history is
// Indonesian and T-Q8 retrieves prior questions as few-shot examples.
func TestTheRefusalFollowsTheQuestionAndNotTheInjectedContext(t *testing.T) {
	rule := Rule{
		MessageEN: "english refusal",
		MessageID: "penolakan indonesia",
		Message:   "fallback",
	}

	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain english",
			input: "Give me a recipe for nasi goreng with chicken.",
			want:  "english refusal",
		},
		{
			name: "english question behind an Indonesian cookbook prelude",
			input: "[System context: Defined metrics for this organization.\n" +
				" - revenue | Revenue (IDR, per month)\n" +
				"Similar past questions: Berapa total penjualan bulan lalu?]\n\n" +
				"Give me a recipe for nasi goreng with chicken.",
			want: "english refusal",
		},
		{
			name:  "plain indonesian",
			input: "Berapa total penjualan bulan Desember?",
			want:  "penolakan indonesia",
		},
		{
			name: "indonesian question behind the same prelude",
			input: "[System context: Defined metrics for this organization.]\n\n" +
				"Tampilkan laporan penjualan bulan ini",
			want: "penolakan indonesia",
		},
		{
			// "data" was a marker until 2026-08-14, which made the median
			// English question on a BI product read as Indonesian.
			name:  "an english question about data is english",
			input: "show me the data for last quarter",
			want:  "english refusal",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveMessage(rule, tc.input, "unused"); got != tc.want {
				t.Errorf("resolveMessage() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The fallback path is unchanged: a rule carrying only `message` serves it
// whatever the user wrote.
func TestARuleWithOneMessageServesIt(t *testing.T) {
	rule := Rule{Message: "only message"}
	if got := resolveMessage(rule, "Berapa total penjualan?", "fallback"); got != "only message" {
		t.Errorf("resolveMessage() = %q, want %q", got, "only message")
	}
	if got := resolveMessage(Rule{}, "anything", "fallback"); got != "fallback" {
		t.Errorf("resolveMessage() = %q, want %q", got, "fallback")
	}
}
