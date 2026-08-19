package doctable

import (
	"regexp"
	"strings"

	"github.com/fauzanebd/argentum/internal/docparse"
	"github.com/fauzanebd/argentum/internal/numparse"
)

// The header scale word: "in millions", "dalam jutaan Rupiah", "Rp juta".
//
// **It is usually not in the table.** It is a caption above the grid, a note
// below it, or a parenthesis in one column's header — which is why this looks
// at the page and not only at the cells. A missed multiplier is a column that
// is wrong by a factor of a million while every digit in it is right, and it is
// the failure family with no tell: nothing about 3.863 says it means
// 3,863,000,000.
//
// The multiplier is recorded on the column with the phrase that produced it.
// An applied-and-unrecorded multiplier would be unauditable — a reviewer
// looking at the review grid and the warehouse would see two different numbers
// and have nothing to explain the gap.

// scalePhrase matches a scale statement in the three shapes documents use: a
// prepositional phrase ("in millions", "dalam jutaan"), a parenthesised unit
// ("(Rp juta)"), or a unit welded to a currency ("Rp juta", "juta Rupiah").
//
// The preposition, the bracket or the currency is required. Without one of
// them, the bare word "juta" matches a column headed "Penjualan Juta Rasa" —
// a brand name — and the column silently becomes a million times larger.
var scalePhrase = regexp.MustCompile(`(?i)` +
	`(?:` +
	`\b(?:dalam|in|nilai dalam|angka dalam|stated in|expressed in)\s+(?:[a-z]+\s+)??` + // "dalam jutaan", "in millions of"
	`|[\(\[]\s*(?:rp\.?|idr|usd|\$)?\s*` + // "(juta)", "(Rp juta)"
	`|\b(?:rp\.?|idr|usd|\$)\s*` + // "Rp juta"
	`)` +
	`(ribu(?:an)?|juta(?:an)?|miliar(?:an)?|milyar(?:an)?|triliun(?:an)?` +
	`|thousands?|millions?|billions?|trillions?)\b`)

// applyScale finds a scale statement and puts it on the columns it governs.
//
// Scope follows where the phrase was found, and the order is narrowest first: a
// phrase inside a column's own header governs that column alone; one in the
// caption or in the text around the table governs every column that does not
// have its own. A document that says "in millions" once at the top and "in
// thousands" in one column header means exactly that, and reading the second
// as a repeat of the first would be wrong by a factor of a thousand in the one
// column somebody bothered to annotate.
func applyScale(t *Table, page docparse.Page, candidate docparse.Table) {
	for i := range t.Columns {
		if mult, phrase, ok := scaleIn(t.Columns[i].Header); ok {
			t.Columns[i].Multiplier = mult
			t.Columns[i].MultiplierSource = phrase
		}
	}

	mult, phrase, ok := scaleIn(t.Title)
	if !ok {
		mult, phrase, ok = scaleIn(textAround(page, candidate))
	}
	if !ok {
		return
	}
	for i := range t.Columns {
		if t.Columns[i].MultiplierSource != "" {
			continue
		}
		t.Columns[i].Multiplier = mult
		t.Columns[i].MultiplierSource = phrase
	}
}

// scaleIn reads a scale statement out of one piece of text.
func scaleIn(text string) (multiplier float64, phrase string, ok bool) {
	m := scalePhrase.FindStringSubmatch(text)
	if m == nil {
		return 0, "", false
	}
	word := strings.ToLower(m[1])
	// "jutaan" is "juta" wearing the Indonesian suffix and "millions" is
	// "million" wearing the English one. Both are trimmed back to the word the
	// magnitude table knows rather than adding plurals to that table: it is
	// shared with the guardrails, where the words are matched against a reply,
	// and no reply writes "3,86 miliaran".
	for _, candidate := range []string{word, strings.TrimSuffix(word, "an"), strings.TrimSuffix(word, "s")} {
		if mult, found := numparse.Magnitude(candidate); found {
			return mult, strings.TrimSpace(m[0]), true
		}
	}
	return 0, "", false
}

// textAround returns the words printed just above and just below the grid.
//
// Both sides, because the convention splits: an Indonesian report puts *"dalam
// jutaan Rupiah"* under the title above the table, and an English one is as
// likely to print *"(in millions)"* as a note beneath it. Reading only one side
// finds half of them.
func textAround(page docparse.Page, candidate docparse.Table) string {
	if len(candidate.BBox) != 4 {
		return page.Markdown
	}
	top, bottom := candidate.BBox[1], candidate.BBox[3]
	const band = 60.0

	var out []string
	for _, w := range page.Words {
		above := w.Bottom <= top && w.Bottom >= top-band
		below := w.Top >= bottom && w.Top <= bottom+band
		if above || below {
			out = append(out, w.Text)
		}
	}
	if len(out) == 0 {
		return page.Markdown
	}
	return strings.Join(out, " ")
}
