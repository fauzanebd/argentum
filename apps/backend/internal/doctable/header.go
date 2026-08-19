package doctable

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/fauzanebd/argentum/internal/docparse"
)

// headerJoin is what a multi-row header is joined with. A character no header
// uses, so the join is visible to a reviewer and reversible by a later reader.
const headerJoin = " › "

// stripCaption removes the leading rows that are a title rather than a header.
//
// **This exists because T-P2's gate produced one.** The text strategy swallowed
// the report's own title into the grid and split it across two cells —
// `LAPORAN PENJUA` / `LAN Q4 2024` — and a title that becomes a data row is a
// row of nonsense in a published source. The signal is sparseness: a title
// occupies one or two cells of a four-column grid, where a header and a data row
// fill most of them.
//
// The dropped text is returned rather than discarded. It is the table's most
// likely name, and — because captions are where "dalam jutaan" lives — it is
// also evidence about the columns below it.
func stripCaption(grid [][]string, colCount int) (caption string, rest [][]string, notes []string) {
	cols := colCount
	if w := width(grid); w > cols {
		cols = w
	}
	if cols < 3 {
		// Two columns cannot be sparse in a way that means anything: a
		// label/value table has exactly one filled cell per row by design.
		return "", grid, nil
	}

	var parts []string
	i := 0
	for ; i < len(grid) && i < 2; i++ {
		if !looksLikeCaption(grid[i], cols) {
			break
		}
		parts = append(parts, joinCells(grid[i]))
	}
	if i == 0 {
		return "", grid, nil
	}
	caption = strings.Join(parts, " ")
	notes = append(notes, "dropped a caption row above the table: "+quoteShort(caption))
	return caption, grid[i:], notes
}

// looksLikeCaption reports whether a row is a title rather than a header.
//
// Sparseness alone is not enough, and the case that proves it is a merged
// header: `Q4 2024 | | Q3 2024 | ` fills two cells of four exactly as a split
// title does. What separates them is *where* the filled cells are. A title
// starts at the left margin and runs out — its cells are contiguous from column
// zero — while a merged header cell sits over the columns it spans, so the row
// has a gap in it. Dropping a merged header row as a caption would cost every
// column below it half its name.
func looksLikeCaption(row []string, cols int) bool {
	filled, lastFilled := 0, -1
	contiguous := true
	for i, c := range row {
		if strings.TrimSpace(c) == "" {
			continue
		}
		if i != lastFilled+1 {
			contiguous = false
		}
		lastFilled = i
		filled++
	}
	return filled > 0 && filled*2 <= cols && contiguous
}

// joinCells reassembles a caption the parser split across cells.
//
// The join is bare rather than spaced when the fragments look like halves of one
// word — `LAPORAN PENJUA` + `LAN Q4 2024` is one title cut mid-word by a column
// boundary that does not exist, and gluing it with a space would leave the
// review surface showing a title no reader recognises.
func joinCells(row []string) string {
	var out strings.Builder
	for _, c := range row {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if out.Len() == 0 {
			out.WriteString(c)
			continue
		}
		prev := out.String()
		if brokenWord(prev, c) {
			out.WriteString(c)
			continue
		}
		out.WriteString(" ")
		out.WriteString(c)
	}
	return out.String()
}

// brokenWord reports whether two fragments are one word the grid cut in half:
// the first ends in a letter and the second starts with one, and neither side
// carries the punctuation or the digits a real word boundary usually has.
func brokenWord(left, right string) bool {
	l := []rune(left)
	r := []rune(right)
	if len(l) == 0 || len(r) == 0 {
		return false
	}
	last, first := l[len(l)-1], r[0]
	if !unicode.IsLetter(last) || !unicode.IsLetter(first) {
		return false
	}
	// Both upper-case is the shape a split title has, because titles are set in
	// capitals; a lower-case pair is far more often two ordinary words.
	return unicode.IsUpper(last) && unicode.IsUpper(first)
}

// splitHeader separates the header rows from the body.
//
// A header row is one with no numeric cell in it, and the run of them stops at
// the first row that has one. That is the whole rule, and it is deliberately not
// cleverer: a table whose first data row happens to be all text loses nothing
// but a row of labels, while a rule that guessed from formatting would depend on
// information the parser does not carry.
func splitHeader(grid [][]string, maxHeaderRows int) (header, body [][]string) {
	i := 0
	for ; i < len(grid) && i < maxHeaderRows; i++ {
		if rowHasNumber(grid[i]) {
			break
		}
	}
	if i == len(grid) {
		// Every row read as a header. The last one is the body, because a table
		// with no numbers is still a table — a list of names and statuses is
		// exactly the kind of reference a tenant uploads.
		i = len(grid) - 1
	}
	return grid[:i], grid[i:]
}

func rowHasNumber(row []string) bool {
	for _, c := range row {
		if _, ok := readCell(cleanText(c), 0); ok {
			return true
		}
	}
	return false
}

// resolveHeader merges the header rows into one name per column.
//
// Two things happen here that a naive join does not do. A blank cell inherits
// the last non-blank one to its left on the same row, which is how a merged
// header spanning three columns — `Q4 2024` over `Actual | Budget | Var` —
// reaches all three of them. And a fragment already present in the inherited
// prefix is not repeated, so a header that was not merged at all does not
// become "Revenue › Revenue".
func resolveHeader(headerRows [][]string, cols int) []Column {
	out := make([]Column, cols)
	for c := 0; c < cols; c++ {
		var parts []string
		for _, row := range headerRows {
			text := ""
			if c < len(row) {
				text = cleanText(row[c])
			}
			if text == "" {
				text = spanLeft(row, c)
			}
			if text == "" {
				continue
			}
			if len(parts) > 0 && parts[len(parts)-1] == text {
				continue
			}
			parts = append(parts, text)
		}
		out[c] = Column{Header: strings.Join(parts, headerJoin), Multiplier: 1}
	}
	return out
}

// spanLeft returns the nearest non-empty cell to the left, which is what a
// merged header cell looks like once a parser has flattened it: the text lands
// in the first column of the span and the rest are empty.
func spanLeft(row []string, c int) string {
	for i := c - 1; i >= 0; i-- {
		if i >= len(row) {
			continue
		}
		if v := cleanText(row[i]); v != "" {
			return v
		}
	}
	return ""
}

// captionAbove looks for a title in the words printed just above the grid.
//
// The parser gives every word a rectangle, so "just above" is a real question
// rather than a guess about markdown order. The band is narrow — a caption sits
// against its table, and text half a page up is a different paragraph.
func captionAbove(page docparse.Page, candidate docparse.Table) string {
	if len(candidate.BBox) != 4 || len(page.Words) == 0 {
		return ""
	}
	top := candidate.BBox[1]
	const band = 40.0

	var lines []struct {
		top  float64
		text []string
	}
	for _, w := range page.Words {
		if w.Bottom > top || w.Bottom < top-band {
			continue
		}
		placed := false
		for i := range lines {
			if absf(lines[i].top-w.Top) < 4 {
				lines[i].text = append(lines[i].text, w.Text)
				placed = true
				break
			}
		}
		if !placed {
			lines = append(lines, struct {
				top  float64
				text []string
			}{top: w.Top, text: []string{w.Text}})
		}
	}
	if len(lines) == 0 {
		return ""
	}
	// The line closest to the table is the caption. Anything above it belongs to
	// whatever came before.
	best := 0
	for i := range lines {
		if lines[i].top > lines[best].top {
			best = i
		}
	}
	return strings.TrimSpace(strings.Join(lines[best].text, " "))
}

func absf(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// totalWords are the first-cell labels that mark a row as the document's own
// answer rather than one of its facts. Indonesian and English, in the first
// version rather than after a gate finds the check English-only — the T-Q3
// lesson, and this deployment's documents are mostly Indonesian.
var totalWords = []string{
	"total", "totals", "grand total", "subtotal", "sub total", "sum",
	"jumlah", "total keseluruhan", "subjumlah", "jumlah total", "grand-total",
}

// isTotalRow reports whether a row states a total.
//
// Only the label is read here. The arithmetic half — a row whose numbers equal
// the sum of the rows above it — is T-P5's, and it needs the typed values this
// row does not have yet. Both exist because either alone misses: an unlabelled
// total row is common in exports, and a row labelled "Total Penjualan" that is
// genuinely one product line among many is not.
func isTotalRow(row Row, cols []Column) bool {
	for i, cell := range row.Cells {
		text := strings.ToLower(strings.TrimSpace(cell.Raw))
		if text == "" {
			continue
		}
		if i < len(cols) && cols[i].Type.Numeric() {
			continue
		}
		for _, w := range totalWords {
			if text == w || strings.HasPrefix(text, w+" ") || strings.HasSuffix(text, " "+w) {
				return true
			}
		}
		// Only the first labelled cell is examined: "Jakarta" in column 1 and
		// "total" in a later free-text column is a note, not a total row.
		return false
	}
	return false
}

var nonName = regexp.MustCompile(`[^a-z0-9]+`)

// columnName derives the warehouse identifier from the header.
//
// Tenant-supplied text becomes a SQL identifier here, so the rule is
// allow-listed rather than escaped: quoting is enough to make a name *safe* and
// not enough to make it *legible*, and this name reaches a model through
// `get_schema`. A header that survives to nothing usable falls back to its
// position, which is ugly and unambiguous.
func columnName(header string, index int) string {
	s := strings.ToLower(strings.TrimSpace(header))
	s = strings.ReplaceAll(s, "›", " ")
	s = nonName.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if s == "" {
		return "col_" + strconv.Itoa(index+1)
	}
	if s[0] >= '0' && s[0] <= '9' {
		// A leading digit is legal in a quoted identifier and confusing
		// everywhere else, including in the SQL a model writes.
		s = "c_" + s
	}
	const maxName = 40
	if len(s) > maxName {
		s = strings.Trim(s[:maxName], "_")
	}
	return s
}

// dedupeNames makes the names unique in place. Two columns headed "Actual"
// under different quarters resolve to the same slug, and a CREATE TABLE with a
// repeated column name fails at publish — which is a bad place to discover it.
func dedupeNames(cols []Column) {
	seen := map[string]int{}
	for i := range cols {
		name := cols[i].Name
		if n, ok := seen[name]; ok {
			n++
			seen[name] = n
			cols[i].Name = name + "_" + strconv.Itoa(n)
			continue
		}
		seen[name] = 1
	}
}

func quoteShort(s string) string {
	const max = 60
	s = strings.TrimSpace(s)
	if len(s) > max {
		s = s[:max] + "…"
	}
	return "\"" + s + "\""
}

func trimFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
