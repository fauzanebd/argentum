package guardrails

import (
	"fmt"
	"strings"
)

// Staleness verdict strings, as internal/freshness writes them. Duplicated
// rather than imported for the same reason agentbudget duplicates them: this
// package sits below internal/freshness in the graph and the constants are
// pinned on the other side by a test.
const (
	verdictStale = "stale"
)

// CheckStaleness appends a dated notice to a reply whose evidence came from a
// source that has not loaded recently (T-F2).
//
// **This is the one thing on the freshness path the model is not trusted with.**
// The tool payload already carries the currency block, and a well-behaved model
// will phrase it better than this function does. But the failure this feature
// exists to prevent — a confident, grounded, correctly-cited answer off a
// warehouse whose load failed — is exactly the case where the model has no
// reason to doubt itself, and the guidance in a tool result is advice a model
// may decline to take. CheckFabrication's argument, one layer out: what the
// product's trustworthiness rests on is stated by the product.
//
// It **appends**; it never replaces. A stale answer is still the answer — the
// figures are real, they are just older than the reader may assume — so
// withholding it would be the refusal roadmap decision 5 rules out. That makes
// this the gentlest of the four output guards, and deliberately so: the other
// three replace a reply that must not be sent, and this one adds a sentence to
// a reply that must be.
//
// Returns the amended reply and true when it was amended.
func CheckStaleness(reply string, ev TurnEvidence) (string, bool) {
	if ev.Freshness != verdictStale {
		return reply, false
	}
	if strings.TrimSpace(reply) == "" {
		// An empty reply is CheckEmptyReply's business, and appending a
		// freshness note to nothing produces an answer that is only a caveat.
		return reply, false
	}
	// A model that already said it does not get told twice. Cheap and
	// deliberately shallow — this looks for the date the notice would carry,
	// which is the specific claim being duplicated, not for the general subject
	// of freshness. A reply that says "the data may be out of date" without
	// saying *when* has not made the statement this guard exists to make.
	if asOf := asOfDate(ev.FreshnessNote); asOf != "" && strings.Contains(reply, asOf) {
		return reply, false
	}
	note := ev.FreshnessNote
	if note == "" {
		note = "the data behind this answer has not been refreshed recently"
	}
	return strings.TrimRight(reply, " \n\t") + "\n\n" + fmt.Sprintf("_Note: %s. Figures for the most recent period may be incomplete._", note), true
}

// asOfDate pulls the `YYYY-MM-DD` out of the note internal/freshness composes,
// which is the one substring worth testing a reply against.
func asOfDate(note string) string {
	for i := 0; i+10 <= len(note); i++ {
		s := note[i : i+10]
		if isDate(s) {
			return s
		}
	}
	return ""
}

func isDate(s string) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return false
	}
	for i, r := range s {
		if i == 4 || i == 7 {
			continue
		}
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
