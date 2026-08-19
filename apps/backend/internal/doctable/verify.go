package doctable

import (
	"math"
	"strconv"
	"strings"
)

// The arithmetic self-check (T-P5): re-derive what the document states, and
// refuse to publish a table that disagrees with itself.
//
// **T-Q14 is the argument for this file.** A figure 0.078% wrong passed every
// instrument this product had, because the only check available compared a
// stated figure against a returned one and a transcription error is smaller
// than the tolerance any such comparison can carry. A PDF hands us something
// the warehouse never does: the document usually states its own answer. A total
// row, a total column, a percentage column that should sum to 100. Re-derive
// it, and a mismatch is not a rounding argument — it is proof the parse is
// wrong, in the digit-level way a column-boundary error and an OCR error are
// wrong.
//
// **A mismatch is never repaired by trusting the stated total.** The stated
// total may be the misparsed value. The only correct output is a refusal to
// publish and a human looking at the page, which is why VerifyQuarantined is a
// state T-P6 checks rather than a warning T-P7 displays.

// VerifyStatus is what the check concluded about one table.
type VerifyStatus string

const (
	// VerifyUnverified means the document stated no total to check against.
	// It is the common case and it is publishable: most tables are a list of
	// facts, and a list of facts is not wrong for failing to add up.
	VerifyUnverified VerifyStatus = "unverified"
	// VerifyVerified means a stated total was re-derived and matched.
	VerifyVerified VerifyStatus = "verified"
	// VerifyQuarantined means a stated total was re-derived and did not match.
	// The table is kept — the reviewer needs to see it — and cannot be
	// published through any path.
	VerifyQuarantined VerifyStatus = "quarantined"
)

// Verification is the outcome, with enough detail for the sentence T-P7 puts on
// screen: "stated 3.863.405.700, derived 3.860.405.700, difference 3.000.000".
type Verification struct {
	Status VerifyStatus `json:"status"`
	// Detail names both figures and the gap, per failing column. Empty when
	// nothing failed.
	Detail string `json:"detail,omitempty"`
	// Checked is how many column totals were re-derived. Zero with a
	// `verified` status is impossible by construction, and it is the number
	// that says how much the badge is worth: one column checked out of nine is
	// a weaker claim than nine out of nine.
	Checked int `json:"checked"`
}

// Publishable reports whether this table may reach the warehouse. The one
// caller that matters is T-P6, and it asks this rather than comparing strings.
func (v Verification) Publishable() bool { return v.Status != VerifyQuarantined }

// Verify re-derives every total the table states.
//
// Two kinds of total are checked. A **total row** is one this package flagged
// out of the data: its numeric cells must equal the sum of the column above
// them. A **percentage column** must sum to 100 over the data rows. A total
// *column* — a row-wise sum printed as the last column — is deliberately not
// checked here: it needs to know which of the other columns are addends, and a
// table with a budget, an actual and a variance would fail that check while
// being perfectly parsed.
func Verify(t Table) Verification {
	var (
		checked  int
		failures []string
	)

	for _, total := range t.Totals {
		if total.Total == "arithmetic" {
			// Recognised as a total *because* it equalled the sum. Re-deriving it
			// here would be the same arithmetic run twice and reported as
			// independent evidence, which is how an instrument comes to say
			// "verified" about a table nothing checked.
			continue
		}
		for c := range t.Columns {
			col := t.Columns[c]
			if !col.Type.Numeric() {
				continue
			}
			stated, ok := cellValue(total, c)
			if !ok {
				continue
			}
			derived, parts, ok := sumColumn(t.Rows, c)
			if !ok || parts == 0 {
				continue
			}
			checked++
			if agreesWithin(stated, derived, tolerance(t.Rows, c, parts)) {
				continue
			}
			failures = append(failures, mismatch(col, total, c, stated, derived))
		}
	}

	for c := range t.Columns {
		if t.Columns[c].Type != ColumnPercentage {
			continue
		}
		derived, parts, ok := sumColumn(t.Rows, c)
		if !ok || parts < 2 {
			continue
		}
		// A percentage column is only claiming to be a breakdown when it is
		// close to a whole one. A column of growth rates sums to whatever it
		// sums to, and quarantining a table over it would be this check
		// producing exactly the false positive that gets a guardrail narrowed.
		if math.Abs(derived-100) > 5 {
			continue
		}
		checked++
		if agreesWithin(derived, 100, tolerance(t.Rows, c, parts)) {
			continue
		}
		failures = append(failures, "the percentage column "+t.Columns[c].Name+
			" sums to "+formatLike(t.Columns[c], derived)+" rather than 100")
	}

	switch {
	case len(failures) > 0:
		return Verification{
			Status:  VerifyQuarantined,
			Detail:  strings.Join(failures, "; "),
			Checked: checked,
		}
	case checked > 0:
		return Verification{Status: VerifyVerified, Checked: checked}
	default:
		return Verification{Status: VerifyUnverified}
	}
}

// promoteArithmeticTotal moves an unlabelled total row out of the data.
//
// The label check (`isTotalRow`) catches "TOTAL", "Jumlah" and their relatives,
// and it misses the export that prints its total row with an empty first cell —
// which is common, because the label was a merged cell the parser could not
// carry. That row loaded as data double-counts every aggregate built on the
// table, and it is the one failure family in this package that is loud rather
// than subtle: a dashboard whose revenue is exactly twice the truth.
//
// Three conditions, and each one is there because the looser version was wrong.
// Only the **last** row is considered. Every numeric column has to agree — one
// column agreeing is a coincidence. And every *non-numeric* column in the row
// has to be empty, which is the shape of the case this exists for: the label
// was a merged cell the parser could not carry, so the row arrives with nothing
// in its first column. Without that last condition a column reading 100, 200,
// 300 loses December, because 300 is what 100 and 200 add up to and a genuine
// data row would have been demoted out of the source.
func promoteArithmeticTotal(t *Table) {
	// Four rows: three addends and the total. Two addends and a sum is a shape
	// that occurs by accident in any growing series.
	const minRows = 4
	if len(t.Rows) < minRows {
		return
	}
	last := t.Rows[len(t.Rows)-1]
	body := t.Rows[:len(t.Rows)-1]

	for c := range t.Columns {
		if t.Columns[c].Type.Numeric() {
			continue
		}
		if c < len(last.Cells) && strings.TrimSpace(last.Cells[c].Raw) != "" {
			// It says something. A row that names itself is a row this package
			// reads with the label check, where a wrong answer is a wrong word
			// rather than a wrong sum.
			return
		}
	}

	agreed := 0
	for c := range t.Columns {
		if !t.Columns[c].Type.Numeric() {
			continue
		}
		stated, ok := cellValue(last, c)
		if !ok {
			continue
		}
		derived, parts, ok := sumColumn(body, c)
		if !ok || parts < 2 {
			continue
		}
		if !agreesWithin(stated, derived, tolerance(body, c, parts)) {
			return
		}
		agreed++
	}
	if agreed == 0 {
		return
	}
	last.Total = "arithmetic"
	t.Rows = body
	t.Totals = append(t.Totals, last)
	t.Notes = append(t.Notes,
		"the last row on page "+strconv.Itoa(last.Page)+
			" adds up to the rows above it and was held out of the data as an unlabelled total")
}

func cellValue(row Row, c int) (float64, bool) {
	if c >= len(row.Cells) || row.Cells[c].Num == nil {
		return 0, false
	}
	return *row.Cells[c].Num, true
}

func sumColumn(rows []Row, c int) (sum float64, parts int, ok bool) {
	for _, r := range rows {
		v, has := cellValue(r, c)
		if !has {
			continue
		}
		sum += v
		parts++
	}
	return sum, parts, parts > 0
}

// tolerance is what "identical" means for this column, and it is not the
// grounding check's one percent.
//
// It is derived from what the document *printed*. If every part in the column
// is a multiple of a thousand, the document rounded to thousands, and a total
// computed before that rounding can legitimately differ by up to half a
// thousand per part. If the parts carry two decimals, the same argument gives a
// tolerance of half a cent per part. Both cases are real, and a fixed epsilon
// gets one of them wrong: too tight and every rounded report quarantines, too
// loose and the single-digit corruption this check exists for walks through.
func tolerance(rows []Row, c, parts int) float64 {
	unit := roundingUnit(rows, c)
	// Half a unit per part, plus a floor for floating-point noise on values in
	// the billions — a float64 sum of ten such numbers is not exact and the
	// check is about the document, not about IEEE 754.
	return float64(parts)*unit/2 + 1e-6
}

// roundingUnit is the coarsest power of ten every value in the column is a
// multiple of, capped so that a column of round numbers cannot excuse an
// arbitrarily large error.
//
// A column of 3.377.718.500 / 3.708.552.300 / 3.863.405.700 is all multiples of
// 100, so the unit is 100 and three parts buy 150 of slack — which is why the
// 3,000,000 corruption T-Q14 found quarantines here. A column printed to the
// nearest million would buy 1.5m of slack over three rows, and that is the
// document's own precision rather than this check being generous.
func roundingUnit(rows []Row, c int) float64 {
	unit := 1.0
	// Sub-unit precision first: if anything carries decimals, the unit is the
	// smallest place any value uses.
	smallest := 0
	any := false
	for _, r := range rows {
		v, has := cellValue(r, c)
		if !has {
			continue
		}
		any = true
		if d := decimalsOf(v); d > smallest {
			smallest = d
		}
	}
	if !any {
		return unit
	}
	if smallest > 0 {
		return math.Pow(10, -float64(smallest))
	}

	const maxUnit = 1e6
	for unit < maxUnit {
		next := unit * 10
		allMultiples := true
		for _, r := range rows {
			v, has := cellValue(r, c)
			if !has {
				continue
			}
			if math.Mod(math.Abs(v), next) > 1e-6 {
				allMultiples = false
				break
			}
		}
		if !allMultiples {
			break
		}
		unit = next
	}
	return unit
}

// decimalsOf is how many decimal places a value actually uses, up to four.
// Beyond that it is floating-point residue rather than a document's precision.
func decimalsOf(v float64) int {
	v = math.Abs(v)
	for d := 0; d <= 4; d++ {
		scaled := v * math.Pow(10, float64(d))
		if math.Abs(scaled-math.Round(scaled)) < 1e-6 {
			return d
		}
	}
	return 4
}

func agreesWithin(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// mismatch is the sentence a reviewer reads. It names both figures and the
// difference, in the column's own convention, because "the totals do not match"
// is a sentence nobody can act on and "stated 3.863.405.700, derived
// 3.860.405.700, difference 3.000.000" points at the digit.
func mismatch(col Column, total Row, c int, stated, derived float64) string {
	raw := ""
	if c < len(total.Cells) {
		raw = strings.TrimSpace(total.Cells[c].Raw)
	}
	statedText := formatLike(col, stated)
	if raw != "" {
		// The document's own rendering, when there is one. It is what somebody
		// comparing this against the page will be looking at.
		statedText = raw
	}
	return "column " + col.Name + ": stated " + statedText +
		", derived " + formatLike(col, derived) +
		", difference " + formatLike(col, math.Abs(stated-derived))
}

// formatLike renders a figure the way this column's document did: the column's
// own decimal separator, grouped with the other one, at the precision the
// document printed.
func formatLike(col Column, v float64) string {
	dec, group := ",", "."
	if col.Decimal == '.' || col.Decimal == 0 {
		dec, group = ".", ","
	}
	text := strconv.FormatFloat(v, 'f', col.Precision, 64)
	sign := ""
	if strings.HasPrefix(text, "-") {
		sign, text = "-", text[1:]
	}
	whole, frac := text, ""
	if i := strings.IndexByte(text, '.'); i >= 0 {
		whole, frac = text[:i], text[i+1:]
	}
	var out strings.Builder
	for i, digit := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			out.WriteString(group)
		}
		out.WriteRune(digit)
	}
	if frac != "" {
		out.WriteString(dec)
		out.WriteString(frac)
	}
	return sign + out.String()
}
