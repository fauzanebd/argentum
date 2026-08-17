package domain

import (
	"context"
	"strings"
	"time"
)

// SuggestionPick is one reader clicking one next-step chip (T-U13).
//
// It is an event, not a verdict. message_feedback replaces a row when the same
// actor rates the same answer again, because a person has one opinion of an
// answer at a time; a reader who clicks two of the three chips has told us both
// were worth clicking, and there is no version of that which should overwrite
// the other.
//
// This is the only evidence the next-step feature works. It costs one
// light-model call per answered turn and a row of buttons under every answer,
// and the pick rate is what says whether either is earning its place — the
// ticket's own rule is that under about 5% after a real week, the feature should
// be cut rather than tuned.
type SuggestionPick struct {
	ID        string `json:"id"`
	CompanyID string `json:"company_id"`
	MessageID string `json:"message_id"`
	// Index is the chip's 0-based position as it was rendered.
	Index int `json:"idx"`
	// Recommended records whether the chosen chip was the one the agent marked.
	// The comparison the whole table exists to make.
	Recommended bool `json:"recommended"`
	// Label is the chip's text at the moment it was pressed, copied rather than
	// referenced — the suggestion lives in the message's metadata and a spec
	// migration could change it.
	Label     string    `json:"label,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// SuggestionLabelMaxChars caps the stored label. It matches the 48 characters a
// chip is truncated to server-side, plus room for the ellipsis, so a stored
// label is what was on the button rather than a longer string a client invented.
const SuggestionLabelMaxChars = 64

// Normalize trims the label to the cap and squares up whitespace.
func (p *SuggestionPick) Normalize() {
	p.Label = strings.TrimSpace(p.Label)
	if r := []rune(p.Label); len(r) > SuggestionLabelMaxChars {
		p.Label = string(r[:SuggestionLabelMaxChars])
	}
}

// SuggestionPickRepository persists picks and reports the rate.
type SuggestionPickRepository interface {
	Append(ctx context.Context, p *SuggestionPick) error
	// SummaryByCompany counts picks and the answers that offered chips over a
	// window, which is what a pick rate is a ratio of.
	SummaryByCompany(ctx context.Context, companyID string, from, to time.Time) (*SuggestionPickSummary, error)
}

// SuggestionPickSummary is the roll-up: how many answers offered suggestions,
// and how many were acted on.
type SuggestionPickSummary struct {
	// Offered is the number of assistant messages in the window that carried at
	// least one suggestion.
	Offered int `json:"offered"`
	// Picked is the number of those messages that had at least one chip clicked.
	// Not the number of picks: two clicks on one answer is one answer acted on,
	// and a rate whose numerator counts clicks can exceed 1.
	Picked int `json:"picked"`
	// Picks is the raw event count, which is the number that says whether people
	// take more than one suggestion per answer.
	Picks int `json:"picks"`
	// RecommendedPicks is how many of those chose the marked chip. Against Picks
	// it answers the one design question the `recommended` flag poses: does
	// marking one change what people click?
	RecommendedPicks int `json:"recommended_picks"`
}

// PickRate is Picked over Offered, 0 when nothing was offered.
//
// Computed rather than stored so no two consumers can disagree about the
// denominator — it is over answers that OFFERED a suggestion, not over turns.
// Every answered turn is not a chance to click: a turn that asked a clarifying
// question, a report turn and a watcher briefing all deliberately carry none.
func (s SuggestionPickSummary) PickRate() float64 {
	if s.Offered == 0 {
		return 0
	}
	return float64(s.Picked) / float64(s.Offered)
}
