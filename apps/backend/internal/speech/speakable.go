package speech

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// MaxSpokenChars bounds a spoken answer: about a minute and a half read aloud.
// An answer is summarised for the ear, not recited, and a reduction longer than
// this is a model that recited it.
const MaxSpokenChars = 1200

// NotSpeakableError is a reduction Speakable would not hand a synthesiser.
type NotSpeakableError struct {
	Reason string
}

func (e *NotSpeakableError) Error() string { return "the spoken text is not speakable: " + e.Reason }

var (
	speakCode     = regexp.MustCompile("`")
	speakURL      = regexp.MustCompile(`(?i)\bhttps?://|\bwww\.`)
	speakSQL      = regexp.MustCompile(`\bSELECT\b[\s\S]*\bFROM\b`)
	speakRule     = regexp.MustCompile(`^[\s|:\-]*-{3,}[\s|:\-]*$`)
	speakLink     = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	speakCitation = regexp.MustCompile(`\[[^\]]*\]`)
	speakHeading  = regexp.MustCompile(`^\s{0,3}#{1,6}\s*`)
	speakBullet   = regexp.MustCompile(`^\s*(?:[-*+•]|\d{1,2}[.)])\s+`)
	speakQuote    = regexp.MustCompile(`^\s*>\s?`)
	speakEmphasis = regexp.MustCompile(`\*\*|__|~~|\*`)
	speakTag      = regexp.MustCompile(`</?[A-Za-z][^>]*>`)
	speakSpace    = regexp.MustCompile(`\s+`)
)

// Speakable makes a model's spoken reduction safe to synthesise, or refuses it
// (T-W8).
//
// **The model is asked for plain sentences, and this is what makes that true.**
// A reduction that recites a table reads "pipe gudang pipe stok pipe" aloud, and
// a request is advice to a model, not a property of its output — the lesson
// NextStep's "at most one recommended" rule was learned on. So what cannot be
// said is decided here, deterministically, whatever the model wrote.
//
// Two kinds of fix, and the line between them is whether the words survive:
//
//   - **Decoration is removed**: emphasis, headings, bullets, list numbers,
//     block quotes, a markdown link's target, a bracketed citation. What is left
//     is the sentence the decoration was on. Each line becomes a sentence, so a
//     list read aloud pauses between its items.
//   - **Structure is refused**: a table row, a table's rule, code, SQL, a URL.
//     None of those has a spoken form that is still the same content, and
//     rewriting one into prose would be this function writing a second reduction
//     nobody checked. The written answer stands alone (decision 15).
//
// The hyphen in "kira-kira" and a dash between clauses are left alone: they are
// how a sentence is written, not a table.
func Speakable(text string) (string, error) {
	switch {
	case speakCode.MatchString(text):
		return "", &NotSpeakableError{Reason: "it contains code"}
	case speakURL.MatchString(text):
		return "", &NotSpeakableError{Reason: "it contains a link"}
	case speakSQL.MatchString(text):
		return "", &NotSpeakableError{Reason: "it contains SQL"}
	}
	var sentences []string
	for _, line := range strings.Split(text, "\n") {
		if strings.Count(line, "|") >= 2 || speakRule.MatchString(line) {
			return "", &NotSpeakableError{Reason: "it contains a table"}
		}
		line = speakLink.ReplaceAllString(line, "$1")
		line = speakCitation.ReplaceAllString(line, "")
		line = speakHeading.ReplaceAllString(line, "")
		line = speakQuote.ReplaceAllString(line, "")
		line = speakBullet.ReplaceAllString(line, "")
		line = speakEmphasis.ReplaceAllString(line, "")
		line = speakTag.ReplaceAllString(line, "")
		line = strings.TrimSpace(speakSpace.ReplaceAllString(line, " "))
		if line == "" {
			continue
		}
		if last, _ := utf8.DecodeLastRuneInString(line); !strings.ContainsRune(".!?:;,…", last) {
			line += "."
		}
		sentences = append(sentences, line)
	}
	out := strings.Join(sentences, " ")
	switch {
	case strings.Contains(out, "|"):
		return "", &NotSpeakableError{Reason: "it contains a table"}
	case out == "":
		return "", &NotSpeakableError{Reason: "there is nothing to say"}
	case utf8.RuneCountInString(out) > MaxSpokenChars:
		return "", &NotSpeakableError{Reason: "it is too long to listen to"}
	}
	return out, nil
}
