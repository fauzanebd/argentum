package doctable

import (
	"strings"
	"time"

	"github.com/fauzanebd/argentum/internal/numparse"
)

// cleanText normalises one cell's text without changing what it says.
//
// PDF text arrives with the spaces a layout engine used rather than the ones a
// writer typed: non-breaking spaces inside figures, thin spaces as thousands
// separators, soft hyphens at line breaks, and a newline wherever the cell
// wrapped. All of them are whitespace to a reader and none of them are to
// `strconv`.
func cleanText(s string) string {
	replacer := strings.NewReplacer(
		" ", " ", // non-breaking space
		" ", " ", // narrow no-break space, used as a thousands separator
		" ", " ", // thin space, same job
		"\u00ad", "", // soft hyphen
		"\n", " ",
		"\r", " ",
		"\t", " ",
		"−", "-", // minus sign, which is not a hyphen
		"–", "-", // en dash, used as a minus and as an empty-cell marker
	)
	s = replacer.Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// reading is one cell read as a number, with everything that was around the
// digits. The context is kept because it decides the column's type: a cell that
// carried "Rp" makes the column currency, a cell that carried "%" makes it a
// percentage, and the count of printed decimals is the tolerance T-P5 compares
// at.
type reading struct {
	value float64
	// decimals is how many digits the document printed after the separator.
	decimals int
	currency string
	percent  bool
	// magnitude is the cell's own scale word — "1,2 juta" — already applied to
	// value. Recorded so the column multiplier is not applied on top of it.
	magnitude float64
}

// currencyTokens are the symbols and codes a figure in this product's world
// carries. Ordered longest-first so "IDR" is not read as "I" plus a stray.
var currencyTokens = []string{
	"IDR", "USD", "SGD", "MYR", "EUR", "GBP", "JPY", "AUD",
	"Rp.", "Rp", "RM", "S$", "US$", "$", "€", "£", "¥",
}

// footnoteMarks are the characters a document hangs a footnote on. A figure
// wearing one is still that figure — `1.234²` is 1,234 with a note, and a
// parser that refuses it turns the whole column to text over a piece of
// typography.
const footnoteMarks = "*†‡§¹²³⁴⁵⁶⁷⁸⁹⁰ªº"

// readCell reads one cell as a number, using the column's decimal separator
// when the column has decided on one.
//
// Everything it strips, it remembers. The alternative — cleaning the string and
// handing the digits to a parser — loses the difference between 1,234 and
// (1,234), which is the difference between a profit and a loss.
func readCell(raw string, dec byte) (reading, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return reading{}, false
	}
	out := reading{magnitude: 1}

	// Accounting negatives come first: the brackets are outside everything else,
	// including the currency symbol — "(Rp 1.234)" and "Rp (1.234)" both occur.
	negative := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		negative = true
		s = strings.TrimSpace(s[1 : len(s)-1])
	}

	for _, token := range currencyTokens {
		if idx := strings.Index(strings.ToUpper(s), strings.ToUpper(token)); idx >= 0 {
			out.currency = token
			s = strings.TrimSpace(s[:idx] + s[idx+len(token):])
			break
		}
	}
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		negative = true
		s = strings.TrimSpace(s[1 : len(s)-1])
	}

	if strings.HasSuffix(s, "%") {
		out.percent = true
		s = strings.TrimSpace(strings.TrimSuffix(s, "%"))
	}

	// A magnitude word inside the cell — "1,2 juta" — is the cell stating its
	// own scale. Read before the footnote trim, because the word is letters and
	// the trim below removes letters.
	for _, word := range numparse.MagnitudeWords() {
		lower := strings.ToLower(s)
		if idx := strings.LastIndex(lower, word); idx >= 0 && idx+len(word) == len(lower) {
			if m, ok := numparse.Magnitude(word); ok {
				out.magnitude = m
				s = strings.TrimSpace(s[:idx])
			}
			break
		}
	}

	s = strings.TrimRight(s, footnoteMarks+" ")
	s = strings.TrimSpace(s)

	// A trailing or leading minus, in either of the two places a document puts
	// it. "1.234-" is an ERP export; "-1.234" is everything else.
	if strings.HasSuffix(s, "-") {
		negative = !negative
		s = strings.TrimSpace(strings.TrimSuffix(s, "-"))
	}
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
		if s[0] == '-' {
			negative = !negative
		}
		s = strings.TrimSpace(s[1:])
	}

	if s == "" || !onlyDigitsAndSeparators(s) {
		return reading{}, false
	}
	// A space that survived to here is a thousands separator — layout engines
	// emit narrow and non-breaking spaces for exactly that job, and cleanText
	// has already turned them into ordinary ones. Removed before parsing rather
	// than taught to the parser: `strconv` has never accepted a space in a
	// number and never should.
	s = strings.ReplaceAll(s, " ", "")

	var (
		v  float64
		ok bool
	)
	if dec == '.' || dec == ',' {
		v, ok = numparse.ParseWithDecimal(s, dec)
	} else {
		v, ok = numparse.Parse(s)
	}
	if !ok {
		return reading{}, false
	}
	out.decimals = printedDecimals(s, dec)
	if negative {
		v = -v
	}
	out.value = v * out.magnitude
	return out, true
}

// onlyDigitsAndSeparators is the gate that keeps a label out of a numeric
// column. After the strips above, anything that is not a digit or a separator
// means this cell is not a number — and by the rule in the package doc, one
// such cell makes the whole column text.
func onlyDigitsAndSeparators(s string) bool {
	digits := 0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '.' || r == ',' || r == ' ':
		default:
			return false
		}
	}
	return digits > 0
}

// printedDecimals counts the digits the document printed after the decimal
// separator, which is not the same as the precision of the parsed float: a
// document that prints 3.863.405.700 has printed zero decimals, and that is the
// precision T-P5 holds its arithmetic to.
func printedDecimals(s string, dec byte) int {
	if dec == 0 {
		// No column decision, so the same structural rule the parser uses: the
		// rightmost separator is the decimal one only when what follows it is
		// not a group of three.
		dots, commas := strings.Count(s, "."), strings.Count(s, ",")
		switch {
		case dots > 0 && commas > 0:
			if strings.LastIndex(s, ".") > strings.LastIndex(s, ",") {
				dec = '.'
			} else {
				dec = ','
			}
		case dots == 1 && commas == 0:
			dec = '.'
		case commas == 1 && dots == 0:
			dec = ','
		default:
			return 0
		}
	}
	i := strings.LastIndexByte(s, dec)
	if i < 0 {
		return 0
	}
	n := len(s) - i - 1
	if n == 3 && strings.Count(s, string(dec)) > 1 {
		// The last of several identical separators is a group, not a point.
		return 0
	}
	// A single separator with exactly three digits after it and a short run
	// before it is a group too — the same rule numparse.Parse applies to decide
	// the *value*, applied here to decide the *precision*. Without it "1.200"
	// reports three decimal places, the column types as decimal rather than
	// integer, and T-P5 then compares its totals at a thousandth of a rupiah.
	// Found by T-P13's corpus on five of eight born-digital fixtures.
	if n == 3 && i >= 1 && i <= 3 {
		return 0
	}
	return n
}

// voteDecimal decides which character this column uses as its decimal point.
//
// **Per column, never per cell.** A cell reading "1.234" carries no evidence at
// all; the column carrying "1.234" and "12,50" carries plenty. Deciding per
// cell is the documented way to end up with a column where three values are a
// thousand times the rest — and those three will be the ones somebody quotes,
// because they are the largest.
//
// A column with no evidence either way returns 0, which sends every cell
// through the structural reading rather than a guess dressed as a decision.
func voteDecimal(raws []string) byte {
	dots, commas := 0, 0
	for _, raw := range raws {
		s := strings.TrimSpace(raw)
		nDot, nComma := strings.Count(s, "."), strings.Count(s, ",")
		switch {
		case nDot > 0 && nComma > 0:
			if strings.LastIndex(s, ".") > strings.LastIndex(s, ",") {
				dots++
			} else {
				commas++
			}
		case nDot > 1:
			// Repeated dots group, so if this column has a decimal point at all
			// it is the comma.
			commas++
		case nComma > 1:
			dots++
		case nDot == 1:
			if trailing(s, '.') != 3 {
				dots++
			}
		case nComma == 1:
			if trailing(s, ',') != 3 {
				commas++
			}
		}
	}
	switch {
	case dots > commas:
		return '.'
	case commas > dots:
		return ','
	default:
		return 0
	}
}

func trailing(s string, sep byte) int {
	i := strings.LastIndexByte(s, sep)
	if i < 0 {
		return -1
	}
	return len(s) - i - 1
}

// typeColumn decides one column's type over every cell in it.
//
// The order matters. Dates are tried first because "31/12/2024" reads as a
// number to nobody but is a plausible string to everything; numbers second; and
// text is what is left, which is the answer whenever a single cell refuses.
func typeColumn(col *Column, rows []Row, index int) {
	var raws []string
	for _, r := range rows {
		if index >= len(r.Cells) {
			continue
		}
		if v := strings.TrimSpace(r.Cells[index].Raw); v != "" {
			raws = append(raws, v)
		}
	}
	if len(raws) == 0 {
		col.Type = ColumnText
		return
	}

	if allDates(raws) {
		col.Type = ColumnDate
		return
	}

	dec := voteDecimal(raws)
	var (
		anyPercent  bool
		anyCurrency string
		precision   int
		fractional  bool
	)
	for _, raw := range raws {
		read, ok := readCell(raw, dec)
		if !ok {
			// One unparseable cell makes the column text. Stated as a note
			// rather than silently: a reviewer looking at a text column full of
			// numbers deserves to know which cell did it.
			col.Type = ColumnText
			col.Decimal = 0
			return
		}
		if read.percent {
			anyPercent = true
		}
		if read.currency != "" && anyCurrency == "" {
			anyCurrency = read.currency
		}
		if read.decimals > precision {
			precision = read.decimals
		}
		if read.value != float64(int64(read.value)) {
			fractional = true
		}
	}

	col.Decimal = dec
	col.Precision = precision
	col.Currency = anyCurrency
	switch {
	case anyPercent:
		col.Type = ColumnPercentage
	case anyCurrency != "":
		col.Type = ColumnCurrency
	case precision == 0 && !fractional:
		col.Type = ColumnInteger
	default:
		col.Type = ColumnDecimal
	}
}

// valueRow fills in the typed values for one row, applying the column's
// multiplier.
//
// A cell that stated its own scale word is not multiplied again. "1,2 juta" in
// a column headed "dalam jutaan" is a document being redundant, not a document
// asking for 1.2 trillion — and the double application is exactly the class of
// error this package exists to prevent.
func valueRow(cols []Column, row *Row) {
	for i := range row.Cells {
		if i >= len(cols) {
			break
		}
		col := cols[i]
		cell := &row.Cells[i]
		cell.Num = nil
		cell.Date = ""
		raw := strings.TrimSpace(cell.Raw)
		if raw == "" {
			continue
		}
		switch {
		case col.Type == ColumnDate:
			if d, ok := parseDate(raw); ok {
				cell.Date = d.Format("2006-01-02")
			}
		case col.Type.Numeric():
			read, ok := readCell(raw, col.Decimal)
			if !ok {
				continue
			}
			v := read.value
			if read.magnitude == 1 && col.Multiplier != 0 {
				v *= col.Multiplier
			}
			cell.Num = &v
		}
	}
}

// dateLayouts are the renderings a document uses, most specific first.
//
// Day-first before month-first, deliberately: this deployment's documents are
// Indonesian and European, where 03/04 is the third of April. A corpus that
// proves otherwise gets a per-column override in the review surface (T-P7)
// rather than a cleverer guess here.
var dateLayouts = []string{
	"2006-01-02", "2006/01/02", "02/01/2006", "02-01-2006", "02.01.2006",
	"2 January 2006", "02 January 2006", "January 2006", "Jan 2006",
	"2 Jan 2006", "02 Jan 2006", "2006-01", "01/2006",
}

// indonesianMonths lets a date written in the product's primary language parse
// with the standard library, by rewriting the month into the English name Go
// knows. Ordered so that a long name is replaced before its own prefix.
var indonesianMonths = []struct{ id, en string }{
	{"januari", "January"}, {"februari", "February"}, {"maret", "March"},
	{"april", "April"}, {"mei", "May"}, {"juni", "June"}, {"juli", "July"},
	{"agustus", "August"}, {"september", "September"}, {"oktober", "October"},
	{"november", "November"}, {"desember", "December"},
	{"agt", "Aug"}, {"agu", "Aug"}, {"okt", "Oct"}, {"des", "Dec"}, {"nop", "Nov"},
}

func parseDate(raw string) (time.Time, bool) {
	s := strings.TrimSpace(raw)
	lower := strings.ToLower(s)
	for _, m := range indonesianMonths {
		if strings.Contains(lower, m.id) {
			i := strings.Index(lower, m.id)
			s = s[:i] + m.en + s[i+len(m.id):]
			break
		}
	}
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// allDates reports whether every cell in the column is a date. Every, for the
// same reason a numeric column needs every: a date column with one label in it
// is a text column, and reading it as dates would drop the label.
func allDates(raws []string) bool {
	for _, raw := range raws {
		if _, ok := parseDate(raw); !ok {
			return false
		}
	}
	return len(raws) > 0
}
