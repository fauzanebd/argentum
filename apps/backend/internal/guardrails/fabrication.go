package guardrails

import (
	"fmt"
	"regexp"
	"strings"
)

// TurnEvidence is what a turn actually retrieved, as observed by
// internal/agentbudget. It is passed in rather than read from a context so
// this check stays testable without a running agent.
type TurnEvidence struct {
	// ToolCalls is how many tools executed in the turn.
	ToolCalls int
	// DataCalls is how many of those were data tools (run_sql, query_metric).
	DataCalls int
	// DataRows is the total rows those data tools returned.
	DataRows int
	// EmptyResults counts data calls that succeeded and matched nothing.
	EmptyResults int
	// Exhausted reports whether the turn ran out of budget, and Reason says
	// which dimension ran out.
	Exhausted bool
	Reason    string
}

// grounded reports whether anything in the turn could have produced a figure.
func (e TurnEvidence) grounded() bool { return e.DataRows > 0 }

// CheckFabrication is the output-scope rule of ticket T-16: a reply may not
// state a figure that no tool in this turn produced.
//
// It fires when all three hold:
//
//  1. The reply states a monetary or magnitude figure.
//  2. No data tool returned a single row in this turn.
//  3. The turn did try — at least one tool ran, or the budget ran out.
//
// Condition 3 is what keeps follow-up turns working. "Show that in millions"
// legitimately restates a figure from an earlier turn without running a
// query, and blocking it would break multi-turn conversation to prevent a
// failure that has never been observed there. Both observed fabrications
// (C-1, and the empty-result case in the first eval run) called tools and got
// nothing back, which is exactly what this catches.
//
// What it deliberately does not do is verify provenance — that a figure in
// the reply matches a number in a tool result. Aggregation, rounding and
// magnitude formatting ("Rp 3,86 Miliar" for 3_863_405_700) make that a
// source of false positives, and a guardrail that blocks correct answers gets
// switched off. This is the blunt version, and the ticket says so.
//
// Returns the replacement reply and true when the original must not be sent.
func CheckFabrication(reply string, ev TurnEvidence, userInput string) (string, bool) {
	if strings.TrimSpace(reply) == "" {
		return reply, false
	}
	if ev.grounded() {
		return reply, false
	}
	if ev.ToolCalls == 0 && !ev.Exhausted {
		return reply, false
	}
	if !StatesFigure(reply) {
		return reply, false
	}
	return incompleteAnswer(ev, userInput), true
}

// figurePatterns match the shapes a stated business figure takes in a reply.
// Each is deliberately narrow: bare integers, percentages, years and dates
// are NOT figures here, because refusals legitimately contain them ("I have
// data for July–December 2024", "3 sources are connected").
var figurePatterns = []*regexp.Regexp{
	// Currency-prefixed or -suffixed amounts: "Rp 66.215.000", "$1,234,567.89",
	// "IDR 1.488.000", "1,234.56 USD".
	regexp.MustCompile(`(?i)(rp\.?|idr|usd|sgd|myr|eur|gbp|\$|€|£|¥)\s*-?\d[\d.,]*`),
	regexp.MustCompile(`(?i)\d[\d.,]*\s*(rp\.?|idr|usd|sgd|myr|eur|gbp)\b`),
	// Magnitude words, which the system prompt spends ten lines teaching the
	// agent to use: "Rp 3,86 Miliar", "2.5 million".
	regexp.MustCompile(`(?i)\b\d[\d.,]*\s*(juta|miliar|milyar|triliun|ribu|thousand|million|billion|trillion)\b`),
	// Grouped numbers of four digits or more: "1,348", "3.863.405.700".
	// Years fail this by construction — "2024" carries no separator.
	regexp.MustCompile(`\b\d{1,3}([.,]\d{3})+\b`),
}

// StatesFigure reports whether a reply asserts a monetary or magnitude
// figure. Exported because the eval harness asserts on the same notion of
// "the agent produced a number" that the guardrail blocks on.
func StatesFigure(reply string) bool {
	stripped := stripNonProse(reply)
	for _, re := range figurePatterns {
		if re.MatchString(stripped) {
			return true
		}
	}
	return false
}

// linkOrCode matches markdown links and fenced or inline code. Both carry
// digits that are not claims about the business: a presigned download URL is
// full of them, and a SQL block the agent is explaining may quote literals.
var linkOrCode = regexp.MustCompile("```[\\s\\S]*?```|`[^`]*`|\\[[^\\]]*\\]\\([^)]*\\)|https?://\\S+")

func stripNonProse(s string) string { return linkOrCode.ReplaceAllString(s, " ") }

// incompleteAnswer is what the user gets instead. It says what happened in
// the terms the user asked in — an English question gets an English answer,
// and resolveMessage's Indonesian detection is reused so the reply language
// discipline the system prompt enforces is not broken by the guardrail that
// replaces it.
func incompleteAnswer(ev TurnEvidence, userInput string) string {
	id := looksIndonesian(userInput)

	var cause string
	switch {
	case ev.Exhausted && id:
		cause = "saya kehabisan anggaran langkah untuk giliran ini sebelum kueri selesai"
	case ev.Exhausted:
		cause = "I ran out of steps for this turn before the query completed"
	case ev.EmptyResults > 0 && id:
		cause = "kueri saya berhasil dijalankan tetapi tidak ada baris data yang cocok"
	case ev.EmptyResults > 0:
		cause = "my query ran but matched no rows"
	case ev.DataCalls == 0 && id:
		cause = "saya belum sempat menjalankan kueri apa pun untuk pertanyaan ini"
	case ev.DataCalls == 0:
		cause = "I did not get as far as running a query for this"
	case id:
		cause = "kueri saya tidak mengembalikan data"
	default:
		cause = "my query returned no data"
	}

	if id {
		return fmt.Sprintf(
			"Saya tidak bisa menyelesaikan kueri untuk pertanyaan ini, jadi saya tidak punya angka "+
				"yang bisa saya sampaikan — %s.\n\nSaya tidak akan menyebutkan angka yang tidak berasal "+
				"dari data. Mau saya lanjutkan, atau persempit pertanyaannya (rentang tanggal yang lebih "+
				"pendek, satu metrik saja) supaya bisa saya selesaikan?", cause)
	}
	return fmt.Sprintf(
		"I wasn't able to complete the query for this, so I don't have a figure to give you — %s.\n\n"+
			"I won't quote a number that didn't come from your data. Want me to continue, or narrow "+
			"the question (a shorter date range, a single metric) so I can finish it?", cause)
}
