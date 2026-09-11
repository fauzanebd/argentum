package app

import (
	"sort"
	"strings"
	"unicode"

	"github.com/fauzanebd/argentum/internal/domain"
)

// Addressing is who a message in a room is for (T-N3).
//
// **It is decided by parsing, not by a model.** A router LLM picking the
// speaker is a new place a turn goes silently to the wrong persona with the
// wrong sources, it costs a light-tier call on every message, and this
// repository has already watched a classifier prompt edit fail to do what it
// said (08-social-carousel-roadmap.md, the 2026-08-14 edit). An `@` is
// unambiguous, free, and the user's own decision.
//
// Hermes Agent, the only shipped implementation of this feature, reached the
// same conclusion and left it in a comment — "never parallel, no LLM router"
// (research/06-hermes-multi-agent.md §2).
type Addressing struct {
	// AgentIDs is who should answer, in the order they were addressed. Empty
	// means nobody was named and the thread's default speaker answers, which is
	// what every message before this ticket did.
	AgentIDs []string
	// Unknown is a name that was @-addressed and is not in the room. Non-empty
	// refuses the whole message: a turn must not silently reach fewer agents
	// than the user asked for, and it must not silently enlarge the room
	// either.
	Unknown []string
	// Cleaned is the message with the recognised `@` tokens removed. It is what
	// the model sees. The *original* is what gets persisted — a transcript
	// should read as what the person typed.
	Cleaned string
}

// reservedHandles address every participant at once.
//
// Free, and what a user of any group chat will type. Without them the message
// reaches exactly one agent and nothing says why. Taken from Hermes' roster
// (hosted_room_discussion.py:300-301); AgentService refuses to create or rename
// an agent to either, so a tenant cannot shadow one.
var reservedHandles = map[string]bool{"all": true, "everyone": true}

// IsReservedHandle reports whether a name would collide with `@all`/`@everyone`.
// Exported because the roster is what has to enforce it, at create and rename.
func IsReservedHandle(name string) bool {
	return reservedHandles[strings.ToLower(strings.TrimSpace(name))]
}

// ParseAddressing decides who a message is for.
//
// **A pure function** of (text, participants): no repository, no context, no
// clock. Hermes' room orchestrator is pure over a durable log for the same
// reason (plan_next_task) — a routing decision that can be replayed is one that
// can be tested, and one that cannot drift between the API process and the
// worker.
//
// Matching is case-insensitive and longest-name-first, so a room holding
// "Finance" and "Finance Team" resolves `@Finance Team` to the longer one
// rather than to the shorter one plus a stray word.
//
// **An unrecognised `@` is not an error. It is text.** Somebody's handle, an
// email address, a price. Only a name that matches nothing at all *and* looks
// like it was meant to address someone would be ambiguous, and this function
// does not guess: the sole refusal is a name that matches a participant of
// *another* room or a roster agent not in this one, which the caller detects by
// comparing against the roster — see Unknown, which this function fills only
// from the candidates it was given.
func ParseAddressing(text string, participants []*domain.ThreadParticipant) Addressing {
	out := Addressing{Cleaned: text}
	if len(participants) == 0 || !strings.Contains(text, "@") {
		return out
	}

	// Longest first, so "Finance Team" is tried before "Finance".
	names := make([]*domain.ThreadParticipant, len(participants))
	copy(names, participants)
	sort.SliceStable(names, func(i, j int) bool {
		return len(names[i].AgentName) > len(names[j].AgentName)
	})

	seen := map[string]bool{}
	cleaned := text
	// Walk every `@` occurrence left to right so AgentIDs comes out in the
	// order the user addressed them.
	for _, tok := range atTokens(text) {
		if h := matchedHandle(tok); h != "" {
			for _, p := range participants {
				if !seen[p.AgentID] {
					seen[p.AgentID] = true
					out.AgentIDs = append(out.AgentIDs, p.AgentID)
				}
			}
			cleaned = removeToken(cleaned, "@"+tok[:len(h)])
			continue
		}
		for _, p := range names {
			if p.AgentName == "" || !matchesName(tok, p.AgentName) {
				continue
			}
			if !seen[p.AgentID] {
				seen[p.AgentID] = true
				out.AgentIDs = append(out.AgentIDs, p.AgentID)
			}
			cleaned = removeToken(cleaned, "@"+tok[:len(p.AgentName)])
			break
		}
	}

	// collapseSpaces is business_inference.go's, reused rather than
	// reimplemented. It squeezes runs of spaces and leaves newlines alone,
	// which is what a multi-line message wants — strings.Fields would flatten
	// a paragraph into one line.
	out.Cleaned = strings.TrimSpace(collapseSpaces(cleaned))
	if out.Cleaned == "" {
		// "@Finance" alone addressed somebody and said nothing. Leave the
		// original so validate() refuses an empty message, which is what it
		// already does today and the right answer here too.
		out.Cleaned = text
	}
	return out
}

// atTokens returns the text following each `@`, up to the end of a plausible
// name — letters, digits, spaces and the few separators an agent name may hold.
//
// It deliberately over-reads: "Finance Team what happened" is returned whole and
// the caller prefix-matches a participant out of it. Under-reading at the first
// space would make a two-word agent name unaddressable, which is the common
// case ("Finance Team", "Customer Support").
func atTokens(text string) []string {
	var out []string
	for i := 0; i < len(text); i++ {
		if text[i] != '@' {
			continue
		}
		// An `@` inside a word is an email address or a handle, not addressing.
		if i > 0 && !isBoundary(rune(text[i-1])) {
			continue
		}
		j := i + 1
		for j < len(text) && isNameRune(rune(text[j])) {
			j++
		}
		if j > i+1 {
			out = append(out, text[i+1:j])
		}
	}
	return out
}

// matchesName reports whether tok begins with name, at a word boundary.
//
// The boundary check is what stops an agent called "Ops" from answering
// "@opsummary". atTokens over-reads to the end of a plausible name — it has to,
// or "Finance Team" would be unaddressable — so every match here is a prefix
// match, and a prefix match without a boundary matches the wrong word.
//
// A trailing space counts as a boundary, which is how "Finance" matches inside
// "@Finance Team". Longest-name-first ordering is what then picks the right one.
func matchesName(tok, name string) bool {
	if len(tok) < len(name) {
		return false
	}
	if !strings.EqualFold(tok[:len(name)], name) {
		return false
	}
	return len(tok) == len(name) || tok[len(name)] == ' '
}

// matchedHandle returns the reserved handle tok begins with, or "".
func matchedHandle(tok string) string {
	for h := range reservedHandles {
		if matchesName(tok, h) {
			return h
		}
	}
	return ""
}

func isBoundary(r rune) bool {
	return unicode.IsSpace(r) || r == '(' || r == '[' || r == '"'
}

func isNameRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == '-' || r == '_' || r == '&'
}

// removeToken drops the first occurrence of tok from s.
//
// The tokens are stripped from what the model sees, exactly as
// lark.StripMentions does (internal/lark/mention.go:28-33) and for the reason
// its comment gives: the addressing is routing metadata, and leaving it in the
// prompt teaches the model that `@` is something it should produce.
func removeToken(s, tok string) string {
	i := strings.Index(s, tok)
	if i < 0 {
		return s
	}
	return s[:i] + s[i+len(tok):]
}
