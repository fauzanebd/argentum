package guardrails

import (
	"math"
	"testing"
)

// The case T-W1 was built for, seen from the instrument's side. Revenue and
// cost came back from two queries and the margin was divided in the sentence.
// Before T-W3 the report said nothing at all about it: 18.42 is under the
// thousand cut-off, so it was never even extracted.
func TestAMarginDividedInTheSentenceIsAnUngroundedPercent(t *testing.T) {
	returned := []float64{4182330000, 3411900000}
	rep := CheckGrounding("Gross margin last month was 18.42%.", returned)

	if len(rep.UngroundedPercents) != 1 || rep.UngroundedPercents[0] != 18.42 {
		t.Fatalf("ungrounded percents = %v; want [18.42]", rep.UngroundedPercents)
	}
	// And the rate T-Q11 has been reporting since August does not move: the
	// percentages are recorded for T-W3 and read by nothing else.
	if !rep.Clean() {
		t.Errorf("Clean() = false; the percentage fields must not change what Clean reports: %+v", rep)
	}
}

// The same sentence, on a turn where `compute` returned the margin. The tool
// pays "computed" back as a string, which CollectNumbers parses, so the figure
// arrives here as either the percentage or the fraction.
func TestAPercentageAToolReturnedIsGrounded(t *testing.T) {
	for _, tc := range []struct {
		name     string
		returned []float64
	}{
		{"as the percentage", []float64{4182330000, 3411900000, 18.42}},
		{"as the fraction it renders", []float64{4182330000, 3411900000, 0.1842}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := CheckGrounding("Gross margin last month was 18.42%.", tc.returned)
			if len(rep.StatedPercents) != 1 {
				t.Fatalf("stated percents = %v; want one", rep.StatedPercents)
			}
			if len(rep.UngroundedPercents) != 0 {
				t.Errorf("ungrounded percents = %v; want none", rep.UngroundedPercents)
			}
		})
	}
}

// A ratio of two returned values is forgiven for sums and differences and must
// not be here — dividing is the arithmetic `compute` exists to take away.
func TestARatioOfTwoReturnedValuesIsNotForgiven(t *testing.T) {
	// 50 / 200 = 25%, and both operands were returned.
	rep := CheckGrounding("Conversion was 25% this week.", []float64{50, 200})
	if len(rep.UngroundedPercents) != 1 {
		t.Errorf("ungrounded percents = %v; a ratio divided in the sentence must be reported", rep.UngroundedPercents)
	}
}

// The tolerance is the precision the reply wrote. One percent relative would
// call "18%" of a returned 18.4211 a different number; the exact tolerance
// would call "18.42%" one.
func TestAPercentIsHeldToThePrecisionItWasWrittenAt(t *testing.T) {
	returned := []float64{18.4211}
	cases := []struct {
		reply    string
		grounded bool
	}{
		{"Margin was 18%.", true},
		{"Margin was 18.4%.", true},
		{"Margin was 18.42%.", true},
		{"Margin was 19%.", false},
		{"Margin was 18.5%.", false},
	}
	for _, tc := range cases {
		rep := CheckGrounding(tc.reply, returned)
		if got := len(rep.UngroundedPercents) == 0; got != tc.grounded {
			t.Errorf("%q: grounded = %v; want %v (ungrounded %v)", tc.reply, got, tc.grounded, rep.UngroundedPercents)
		}
	}
}

// Indonesian is the production language: a decimal comma and "persen".
func TestIndonesianPercentagesAreRead(t *testing.T) {
	returned := []float64{0.1842, 5}
	if rep := CheckGrounding("Margin kotor bulan lalu 18,42 persen.", returned); len(rep.StatedPercents) != 1 || len(rep.UngroundedPercents) != 0 {
		t.Errorf("18,42 persen against a returned 0.1842: stated %v, ungrounded %v", rep.StatedPercents, rep.UngroundedPercents)
	}
	rep := CheckGrounding("Penjualan naik 12,5% dibanding bulan lalu.", returned)
	if len(rep.UngroundedPercents) != 1 || rep.UngroundedPercents[0] != 12.5 {
		t.Errorf("12,5%% matches nothing returned: ungrounded %v", rep.UngroundedPercents)
	}
}

// Checked's rule applies to percentages too: with nothing returned there is
// nothing to compare against, and every percentage would be noise.
func TestNoReturnedNumbersMeansNoPercentComparison(t *testing.T) {
	rep := CheckGrounding("Margin was 18%.", nil)
	if rep.Checked || rep.StatedPercents != nil || rep.UngroundedPercents != nil {
		t.Errorf("report = %+v; want an unchecked, empty report", rep)
	}
}

func TestHalfStepFollowsTheWrittenDecimals(t *testing.T) {
	cases := []struct {
		v    float64
		want float64
	}{
		{18, 0.5},
		{18.4, 0.05},
		{18.42, 0.005},
		{0.1842, 0.00005},
	}
	for _, tc := range cases {
		if got := halfStepOf(tc.v); math.Abs(got-tc.want) > 1e-12 {
			t.Errorf("halfStepOf(%v) = %v; want %v", tc.v, got, tc.want)
		}
	}
}
