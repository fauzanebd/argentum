package app

import "github.com/fauzanebd/argentum/internal/guardrails"

// groundingKey is where a turn's grounding verdict rides on the assistant
// message's metadata (T-W3).
const groundingKey = "grounding"

// withGroundingRecord adds a turn's grounding verdict to the metadata its reply
// is stored with (T-W3).
//
// **Before this, the verdict existed for the length of one log line.**
// CheckGrounding has run on every streamed turn since T-Q9, and its answer went
// to a Warn line, a process counter and a span — none of which joins to
// anything. Tool outputs are not stored either, so "how many turns stated a
// figure no tool returned" had no stored answer for any turn this deployment
// has ever run, and T-W3's query needed one.
//
// Metadata rather than a column: the column exists, both marshal sites exist,
// and T-Q10's suggestions already ride it without a migration. Neither the
// embed surface nor `/v1` serialises it, so a verdict about a reply does not
// reach a widget visitor's browser.
//
// `turn` is the user message id, which is what `agent_actions.message_id`
// holds. Nothing else in the schema joins a reply to the tool calls behind it —
// T-F4 had to approximate the same link with a LATERAL — so the record carries
// the key itself.
//
// The ungrounded *values* are stored, not only their count, so somebody opening
// a flagged reply can see which figure was flagged without re-running an
// extractor they cannot see. Stated figures are counts: every one of them is in
// the reply text already.
//
// **A nil or unchecked report leaves meta exactly as it was.** Unchecked is not
// clean: a turn whose tools returned no numbers was not measured, and writing a
// zero for it would put it in the denominator as a success.
func withGroundingRecord(meta map[string]any, turnID string, rep *guardrails.GroundingReport) map[string]any {
	if rep == nil || !rep.Checked || turnID == "" {
		return meta
	}
	if meta == nil {
		meta = map[string]any{}
	}
	meta[groundingKey] = map[string]any{
		"turn":                turnID,
		"stated":              len(rep.Stated),
		"ungrounded":          floatsOrEmpty(rep.Ungrounded),
		"stated_percents":     len(rep.StatedPercents),
		"ungrounded_percents": floatsOrEmpty(rep.UngroundedPercents),
	}
	return meta
}

// floatsOrEmpty keeps a nil slice from marshalling as JSON null. The read side
// counts these arrays, and `jsonb_array_length` raises on a scalar rather than
// returning zero.
func floatsOrEmpty(v []float64) []float64 {
	if v == nil {
		return []float64{}
	}
	return v
}
