package guardrails

import (
	"math"
	"testing"
)

// The exact gap this check exists for. CheckFabrication passes this reply
// completely — a data tool ran, rows came back, the magnitudes agree — and the
// number is still not the number the query returned.
func TestAFigureThatIsNotTheOneReturnedIsUngrounded(t *testing.T) {
	returned := []float64{3863405700}
	rep := CheckGrounding("Total sales were roughly 4,100,000,000 last month.", returned)

	if !rep.Checked {
		t.Fatal("the comparison did not run")
	}
	if rep.Clean() {
		t.Errorf("4.1 billion was accepted against a returned 3,863,405,700: %+v", rep)
	}
}

func TestTheFigureThatWasReturnedIsGrounded(t *testing.T) {
	returned := []float64{3863405700, 310}
	rep := CheckGrounding("Total sales were IDR 3,863,405,700 across 310 transactions.", returned)
	if !rep.Clean() {
		t.Errorf("the returned figure was reported as ungrounded: %+v", rep.Ungrounded)
	}
}

// The system prompt REQUIRES this rendering for Indonesian replies. Without
// magnitude matching, every correctly formatted Indonesian answer would report
// as ungrounded and the instrument would be useless on half the traffic.
func TestMagnitudeRenderingIsGrounded(t *testing.T) {
	returned := []float64{3863405700}
	for _, reply := range []string{
		"Total penjualan Rp 3,86 Miliar.",
		"Total sales were Rp 3.86 billion.",
		"Sales reached 3,863.41 million.",
	} {
		if rep := CheckGrounding(reply, returned); !rep.Clean() {
			t.Errorf("%q reported ungrounded figures %+v", reply, rep.Ungrounded)
		}
	}
}

// An analyst's reply legitimately contains arithmetic over what was returned.
// Reporting those would drown the real signal.
func TestSimpleDerivationsAreGrounded(t *testing.T) {
	returned := []float64{3863405700, 3708552300}
	// The difference between the two months, which the agent is asked to state.
	rep := CheckGrounding("December beat November by 154,853,400.", returned)
	if !rep.Clean() {
		t.Errorf("a difference of two returned values was reported: %+v", rep.Ungrounded)
	}

	sum := CheckGrounding("Together the two months came to 7,571,958,000.", returned)
	if !sum.Clean() {
		t.Errorf("a sum of two returned values was reported: %+v", sum.Ungrounded)
	}
}

// Years, quarters, list positions and "the top 10" are the overwhelming
// majority of false positives, and none of them is a figure anyone fabricates.
func TestSmallIntegersAreNotTreatedAsFigures(t *testing.T) {
	returned := []float64{3863405700}
	rep := CheckGrounding(
		"In Q4 2024 the top 5 channels drove sales of IDR 3,863,405,700 over 31 days.", returned)
	if !rep.Clean() {
		t.Errorf("small integers were reported as ungrounded figures: %+v", rep.Ungrounded)
	}
}

// A year that closes a sentence arrives from the regex with the full stop
// attached — `2024.` rather than `2024` — and the year filter reads a token's
// punctuation to tell a calendar year from a decimal quantity. Until 2026-08-14
// it was handed the untrimmed token while the parser got a trimmed one, so it
// judged every sentence-final year to be a quantity and reported it.
//
// The case above hid this by writing the year mid-sentence. It was found on the
// third case of an eval run, where the reply below produced
// `ungrounded="[2024]"` — and the noise matters more than it looks: this
// instrument exists to *count* the wrong-but-nonempty rate, and almost every
// reply about a time window ends a sentence with a year.
func TestASentenceFinalYearIsStillAYear(t *testing.T) {
	returned := []float64{1348}
	rep := CheckGrounding(
		"We have **1,348** sales transactions in total, covering the period "+
			"from 1 July 2024 to 31 December 2024.", returned)
	if !rep.Clean() {
		t.Errorf("a sentence-final year was reported as an ungrounded figure: %+v", rep.Ungrounded)
	}

	// The trade the filter documents must survive the fix: a grouped or
	// fractioned number is a quantity even when it looks like a year, and
	// trimming only the outer punctuation leaves both of those intact.
	quantities := CheckGrounding("The two lines came to 2,024 and 2024.50 units.", []float64{99})
	if len(quantities.Ungrounded) != 2 {
		t.Errorf("a grouped or fractioned number stopped being treated as a quantity: %+v", quantities.Ungrounded)
	}
}

// An Indonesian decimal comma is a decimal comma, not a thousands separator
// with the digits shuffled. Found on 2026-08-14: the reply "Rp 21,23 Miliar"
// was read as 2123 — a figure no tool returned, on the two cases whose answers
// are written in the product's primary language, on both models scored that
// day.
func TestAnIndonesianDecimalIsNotAFourDigitInteger(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want float64
	}{
		{"21,23", 21.23}, // ID magnitude: "Rp 21,23 Miliar"
		{"21.23", 21.23}, // EN magnitude: "Rp 21.23 billion"
		{"12.462.599,03", 12462599.03},
		{"12,462,599.03", 12462599.03},
		{"3.863.405.700", 3863405700},
		{"3,863,405,700", 3863405700},
		{"12.500", 12500},  // ID thousands — a group is 1–3 digits then 3
		{"1,234", 1234},    // EN thousands, same shape
		{"1234.000", 1234}, // a driver rendering DECIMAL(…,3), not 1.234m
		{"3863405700.00", 3863405700},
	} {
		got, ok := parseLoose(tc.raw)
		if !ok {
			t.Errorf("parseLoose(%q) refused a number it should read", tc.raw)
			continue
		}
		if math.Abs(got-tc.want) > 0.001 {
			t.Errorf("parseLoose(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}

	// The end-to-end shape: the magnitude sentence is a rendering of the figure
	// the tool returned, so nothing is ungrounded.
	rep := CheckGrounding(
		"Total penjualan Anda **Rp 21.231.619.600** (sekitar **Rp 21,23 Miliar**).",
		[]float64{21231619600})
	if !rep.Clean() {
		t.Errorf("an Indonesian magnitude rendering was reported as ungrounded: %+v", rep.Ungrounded)
	}
}

// A turn whose tools returned nothing numeric cannot be checked, and saying
// every figure is ungrounded would be noise. CheckFabrication is the gate for
// that case and already covers it.
func TestNoReturnedNumbersMeansNoComparison(t *testing.T) {
	rep := CheckGrounding("Sales were 3,863,405,700.", nil)
	if rep.Checked {
		t.Error("the comparison claimed to have run with nothing to compare against")
	}
	if !rep.Clean() {
		t.Error("an unrunnable comparison reported failures")
	}
}

// Download URLs are full of digits and a SQL block may quote literals. Neither
// is a claim about the business.
func TestLinksAndCodeAreNotProse(t *testing.T) {
	returned := []float64{42000}
	rep := CheckGrounding(
		"Here is the file: [report](https://example.com/x/99887766?e=12345678) and the query was "+
			"`SELECT 987654321 FROM t`. The total was 42,000.", returned)
	if !rep.Clean() {
		t.Errorf("digits from a link or a code span were treated as figures: %+v", rep.Ungrounded)
	}
}

func TestCollectNumbersWalksAResultPayload(t *testing.T) {
	payload := map[string]any{
		"columns":   []any{"channel", "total"},
		"row_count": 2,
		"rows": []any{
			map[string]any{"channel": "Online", "total": float64(1500000)},
			// Drivers render DECIMAL as a string, which is every money column
			// in this product's demo schema.
			map[string]any{"channel": "In-Store", "total": "2500000.50"},
		},
	}
	got := CollectNumbers(payload, 100)

	want := map[float64]bool{1500000: false, 2500000.50: false}
	for _, v := range got {
		if _, ok := want[v]; ok {
			want[v] = true
		}
	}
	for v, found := range want {
		if !found {
			t.Errorf("CollectNumbers missed %v (got %v)", v, got)
		}
	}
}

func TestCollectNumbersIsBounded(t *testing.T) {
	rows := make([]any, 0, 500)
	for i := 0; i < 500; i++ {
		rows = append(rows, map[string]any{"v": float64(i + 10000)})
	}
	if got := CollectNumbers(map[string]any{"rows": rows}, 50); len(got) > 50 {
		t.Errorf("CollectNumbers returned %d values, cap was 50", len(got))
	}
}
