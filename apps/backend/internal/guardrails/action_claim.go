package guardrails

import (
	"regexp"
	"strings"
)

// Does this reply claim to have *done* something? (T-Q13)
//
// **The hole this fills, in one transcript.** On a thread holding one clean,
// successful `create_dashboard` and no refusal anywhere, the sentence *"Rename
// that dashboard to 'Q4 2024 Sales Review'."* was answered:
//
//	Done — your dashboard is now called **Q4 2024 Sales Review**.
//	The URL stays the same, so any existing links will continue to work.
//
// `agent_actions` held no row for that turn, `tool_calls` was 0, and the stored
// title was unchanged. One turn in three, non-deterministic — and invisible to
// every instrument this product has, because CheckFabrication asks whether the
// turn had *evidence* and CheckGrounding asks whether every *figure* came from
// a tool. This reply contains no figure at all. The claim is an **action**, and
// nothing here checked those.
//
// It is arguably worse than a wrong number: a wrong figure is visible to
// somebody who knows the business, while "Done" about an edit that did not
// happen is invisible until the dashboard is opened — possibly by somebody
// else, possibly next quarter.
//
// **This detects the claim and nothing else.** Whether the turn was entitled to
// make it is the caller's question, because only the caller knows what the turn
// actually called.

// actionClaim is one way a reply says a thing was done, with a name for the
// log. The name is what gets recorded rather than the sentence: a matched
// sentence carries the tenant's own dashboard titles and column names, and an
// instrument is not a reason to put those in a log line.
type actionClaim struct {
	name string
	re   *regexp.Regexp
}

// The patterns. Deliberately narrow — completion language about work, not any
// past tense — because this counts before it ever blocks and a noisy counter is
// one nobody reads.
//
// **Indonesian is here from the first version, not added later.** T-Q3's
// finding was that all three `must_not_call` assertions were written in English
// and the violation duly arrived in Indonesian; the same trap is open here, on
// a product whose primary language is Indonesian.
var actionClaims = []actionClaim{
	// "Done — your dashboard is now called…", the exact shape from the gate.
	{"done-opener", regexp.MustCompile(`(?i)(?:^|\n)\s*\**\s*(done|completed|selesai|beres)\b\s*[—\-–:,!.]`)},
	// "I've updated the dashboard", "we have created the schedule".
	{"i-have-verbed", regexp.MustCompile(`(?i)\b(?:i|we)\s*(?:'ve|’ve| have)\s+(?:just\s+|now\s+|successfully\s+)?` +
		`(created|updated|renamed|deleted|removed|scheduled|saved|added|changed|set up|built)\b`)},
	// "I updated the dashboard" — simple past, first person, still this turn.
	{"i-verbed", regexp.MustCompile(`(?i)\b(?:i|we)\s+(?:just\s+|now\s+|successfully\s+)?` +
		`(created|updated|renamed|deleted|removed|scheduled|saved|added|changed|built)\s+(?:the|your|that|this|a|an)\b`)},
	// "the dashboard has been renamed", "your report has been scheduled".
	// `has not been` cannot match: the negation sits between the two words.
	{"has-been-verbed", regexp.MustCompile(`(?i)\b(?:has|have)\s+been\s+` +
		`(created|updated|renamed|deleted|removed|scheduled|saved|added|changed)\b`)},
	// "your dashboard is now called X" / "is now named" / "is now set to".
	{"is-now", regexp.MustCompile(`(?i)\bis\s+now\s+(called|named|titled|set to|scheduled|updated|renamed)\b`)},
	// Indonesian: "sudah saya ubah", "telah diperbarui", "berhasil dibuat".
	{"sudah-telah", regexp.MustCompile(`(?i)\b(sudah|telah)\s+(saya\s+|kami\s+)?` +
		`(dibuat|diperbarui|diubah|diganti|dihapus|dijadwalkan|disimpan|ditambahkan|ubah|buat|ganti|perbarui)\b`)},
	{"berhasil", regexp.MustCompile(`(?i)\bberhasil\s+` +
		`(dibuat|diperbarui|diubah|diganti|dihapus|dijadwalkan|disimpan|ditambahkan|membuat|mengubah|mengganti|memperbarui|menyimpan|menghapus)\b`)},
}

// priorTurn marks a sentence as being about work from an earlier turn rather
// than this one. "The dashboard I built earlier is still called X" is a true
// statement about the past and counting it would make the instrument wrong in
// the one direction that matters — a counter that fires on honest replies is a
// counter nobody will let block anything later.
var priorTurn = regexp.MustCompile(`(?i)\b(earlier|previously|before|last time|` +
	`in (?:a|the) (?:previous|earlier) (?:turn|message|reply)|sebelumnya|tadi|kemarin)\b`)

// sentenceSplit breaks a reply into sentences for the prior-turn test. Crude on
// purpose: the alternative is a sentence tokeniser, and what this needs is only
// that "I created it earlier" and "I have updated it" do not share a window.
var sentenceSplit = regexp.MustCompile(`[.!?\n]+`)

// ClaimsCompletedAction reports whether the reply says it completed a change,
// and which pattern said so.
//
// The name is for the log and the test. An empty name with false means no
// sentence in the reply claims anything was done.
func ClaimsCompletedAction(reply string) (string, bool) {
	prose := stripNonProse(reply)
	for _, sentence := range sentenceSplit.Split(prose, -1) {
		s := strings.TrimSpace(sentence)
		if s == "" || priorTurn.MatchString(s) {
			continue
		}
		for _, c := range actionClaims {
			// The opener pattern is anchored to the start of a line, which the
			// split has just removed — so it is tested against the sentence with
			// its leading boundary restored.
			if c.re.MatchString("\n" + s) {
				return c.name, true
			}
		}
	}
	return "", false
}
