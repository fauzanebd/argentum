package app

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/fauzanebd/argentum/internal/domain"
)

// dataStarterTemplates are the questions the untemplated default agent offers
// on a new chat (RIZ-76), one table name each, filled in this order.
//
// Fixed sentences rather than model output: the tables come from SourceProfile
// entities, which keepKnownEntities has already checked against the
// introspected schema, so every question names a table that exists — and a
// list built by code costs nothing, cannot drift from what was reviewed, and is
// the same on every load. "Most recent records" assumes a date column some
// tables do not have; the agent says so and gives an overview, and if pilots
// find that weak the fix is this copy, not a prompt.
var dataStarterTemplates = []string{
	"Give me an overview of our %s data.",
	"What are the most recent records in %s?",
	"What stands out in %s?",
}

// genericStarterQuestions are what a workspace is offered before any source
// has been inferred: connected, but the business inference has not written a
// profile yet (or never will, on a deployment with no light model). They are
// about the schema rather than the business, because the schema is the one
// thing the agent can read for certain on the first turn.
var genericStarterQuestions = []string{
	"What data can you see in my workspace?",
	"Which tables matter most for my business, and what is in them?",
	"Summarize what my main database is about.",
}

// starterTableMaxRunes bounds a table name that may appear in a question. A
// name longer than this is not one a person would recognise in a pill, and the
// pill is one line of the composer.
const starterTableMaxRunes = 60

// DataStarterQuestions builds the default agent's starter questions from the
// company's source profiles (RIZ-76): up to three, each naming one inferred
// table, in the order the profiles arrive — the repository's, default source
// first, so the source the tenant called the main one is the one asked about.
//
// A pure function over rows the caller already loaded, for the same reason
// DraftFromSources is one: the cases worth testing are the edges (a duplicate
// table across two sources, a name nobody should see in a button), and none of
// them needs a database.
//
// Never returns nil. With no usable table it returns the generic set, because
// a workspace that has connected a source but not been inferred yet still has
// something worth asking, and an empty composer is the dead end this exists to
// remove.
func DataStarterQuestions(profiles []*domain.SourceProfile) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(dataStarterTemplates))
	for _, p := range profiles {
		if p == nil {
			continue
		}
		for _, e := range p.Entities {
			if len(out) == len(dataStarterTemplates) {
				return out
			}
			table := strings.TrimSpace(e.Table)
			if !presentableTableName(table) || seen[table] {
				continue
			}
			seen[table] = true
			out = append(out, fmt.Sprintf(dataStarterTemplates[len(out)], table))
		}
	}
	if len(out) == 0 {
		// A copy, so a caller appending to or editing what it was handed cannot
		// change what the next workspace is offered.
		return append([]string(nil), genericStarterQuestions...)
	}
	return out
}

// presentableTableName is whether a table name may be put in front of a person
// inside a sentence. The name is the tenant DBA's text and untrusted (locked
// decision 5 of T-B2): React renders it as text, so this is not about markup —
// it is about a newline or a control character turning a one-line pill into
// something that reads as two questions, or as a question we did not write.
func presentableTableName(s string) bool {
	if s == "" || !utf8.ValidString(s) || utf8.RuneCountInString(s) > starterTableMaxRunes {
		return false
	}
	for _, r := range s {
		// unicode.IsControl covers \n, \r and \t; the line and paragraph
		// separators are not controls but break a line all the same.
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return false
		}
	}
	return true
}
