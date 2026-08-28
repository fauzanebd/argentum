package skill

import (
	"strings"
	"unicode/utf8"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
)

// The index is the always-on half of progressive disclosure (T-K3): one
// `name — when_to_use` line per skill in the system prompt, and no bodies. A
// tenant with thirty procedures pays thirty lines a turn rather than thirty
// procedures, and the model opens what it judges applies with `load_skill`.
//
// **It goes in the system prompt rather than in the user message**, which is a
// departure from where the last five context blocks went and is deliberate: the
// index is per-agent and stable, so it belongs inside the cached prefix, while
// the prepended blocks are per-turn by nature. A body pulled into the system
// prompt would invalidate that prefix on every turn that used a different
// skill, which is the accidental way to make this feature cost more than it
// saves.

// The bounds, and the reason there are two.
//
// **`DefaultIndexMaxChars` is the one that matters.** Twenty lines is not a
// size: at T-K1's caps a line is 265 characters, so "20 lines" is a
// 5,662-character ceiling nobody wrote down — against the ≈44,000-character
// prompt floor, a 12% rise in fixed per-turn cost arriving as a default.
// Counting lines and calling it bounded is the metric catalog's mistake at one
// remove; a bound in the wrong unit is not a bound.
//
// The line bound stays because it is the one a tenant can reason about while
// typing, and because whichever binds first should bind.
//
// **6,000 was the number this ticket specified, and it could never bind.**
// Header (342) + 20 lines × 266 = 5,662, so no index inside the line bound
// could reach 6,000 characters: the character bound would have been dead
// configuration, and the ticket's own acceptance case — "satisfies the line
// bound and breaches the character one" — was unreachable. That is the failure
// this pair of bounds was introduced to prevent, arriving inside the fix for
// it. 4,000 is chosen against the two measurements that exist: a realistic
// index of twenty 172-character lines is 3,802 characters and is untouched,
// and the pathological one at the caps is 5,662 and is cut. It states the
// ceiling in the unit that is charged — ≈1,000 tokens on every turn.
const (
	DefaultIndexMaxLines = 20
	DefaultIndexMaxChars = 4000
)

// Header is the block's own heading, and the sentence that tells the model what
// to do with it. It names `load_skill` because an index whose entries cannot be
// opened is a list of procedures the agent can see and cannot read — which is
// worse than the feature's absence, since it looks like a bug in the model.
const Header = "## Procedures this workspace has written down\n\n" +
	"Each line is a procedure an administrator of this workspace wrote, and when they said it applies. " +
	"You are not shown the steps here. When one of them fits what is being asked, call `load_skill` with " +
	"its exact name to read it, then follow it. If none fits, work normally and do not mention them.\n\n"

// Compose renders the index for one turn, and reports what it had to leave out.
//
// `allowed` decides which skills this agent is offered — the caller passes
// `agent.AllowsSkill`, whose empty binding means *every* enabled company skill.
// Nil means unrestricted, which is what an unscoped turn (the eval harness, a
// channel with no agent row) already gets everywhere else.
//
// **Truncation is deterministic and by the caller's order**, which the
// repository fixes as `lower(name)`. A tenant over either bound must lose the
// same skills every turn: an order that moved would change the cached prefix
// from turn to turn, and would make "why did it stop using that procedure"
// unanswerable.
//
// Returns the empty string when there is nothing to say — and that is a
// property with a test, not an implementation detail. The block must not exist
// for a company with no skills, or every existing tenant's `prompt_sha256`
// moves on the day this ships.
func Compose(skills []*domain.Skill, allowed func(skillID string) bool, maxLines, maxChars int) (string, []string) {
	if maxLines <= 0 {
		maxLines = DefaultIndexMaxLines
	}
	if maxChars <= 0 {
		maxChars = DefaultIndexMaxChars
	}

	var (
		lines   []string
		dropped []string
		chars   = utf8.RuneCountInString(Header)
	)
	for _, s := range skills {
		if s == nil || !s.Enabled {
			continue
		}
		if allowed != nil && !allowed(s.ID) {
			// Not a truncation: this agent was never offered it, so it is not
			// something the tenant should be told they lost.
			continue
		}
		line := s.IndexLine()
		cost := utf8.RuneCountInString(line) + 1 // the newline it rides on
		if len(lines) >= maxLines || chars+cost > maxChars {
			dropped = append(dropped, s.Name)
			continue
		}
		lines = append(lines, line)
		chars += cost
	}

	if len(lines) == 0 {
		// Every skill dropped is still an empty block — and still worth the
		// caller's Warn, which is why `dropped` is returned rather than logged
		// here.
		return "", dropped
	}
	return Header + strings.Join(lines, "\n"), dropped
}

// Lines reports how many procedures a composed block offers.
//
// Counted off the rendered block rather than off the input, because what a
// settings screen has to show is what the prompt carries: a skill filtered out
// by an agent binding, one dropped at a bound, and one nobody enabled all look
// identical from the input side and none of them is a line.
func Lines(block string) int {
	n := 0
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "- ") {
			n++
		}
	}
	return n
}

// LogDropped reports what did not fit, at Warn.
//
// **Still Warn after T-K5, because ranking changed which procedures a tenant
// loses and not that they lose some.** The ranker promotes what the question
// matches; the twenty-first line is still not offered, and a tenant who has
// outgrown the bound needs to know that from something other than a procedure
// quietly ceasing to be used. T-K6 puts the same fact on the settings screen,
// where somebody is actually looking.
func LogDropped(companyID, agentID string, dropped []string, maxLines, maxChars int) {
	if len(dropped) == 0 {
		return
	}
	logrus.WithFields(logrus.Fields{
		"company_id":     companyID,
		"agent_id":       agentID,
		"dropped_count":  len(dropped),
		"dropped":        dropped,
		"max_lines":      maxLines,
		"max_chars":      maxChars,
		"what_this_cost": "these skills are not offered to this agent this turn and the model cannot load what it is not shown",
	}).Warn("skill index: over the bound; some procedures were left out")
}
