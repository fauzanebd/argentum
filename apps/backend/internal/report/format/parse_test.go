package format

import (
	"math"
	"testing"
)

func TestParseNumber(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		// Bare
		{"42", 42, true},
		{"-17", -17, true},
		{"3.86", 3.86, true},
		{"0", 0, true},

		// English grouping
		{"1,234", 1234, true},
		{"1,234,567", 1234567, true},
		{"$1,234,567.89", 1234567.89, true},
		{"1,234.5", 1234.5, true},

		// Indonesian grouping
		{"1.234", 1234, true},
		{"3.863.405.700", 3863405700, true},
		{"Rp 3.863.405.700", 3863405700, true},
		{"Rp1.234.567,89", 1234567.89, true},
		{"1,5", 1.5, true},

		// Magnitude suffixes, Indonesian
		{"Rp 3,86 Miliar", 3.86e9, true},
		{"Rp 66,22 Juta", 66.22e6, true},
		{"1,2 Triliun", 1.2e12, true},
		{"850 ribu", 850e3, true},

		// Magnitude suffixes, English
		{"$1.2K", 1200, true},
		{"3.86 billion", 3.86e9, true},
		{"1.5M", 1.5e6, true},
		{"2B", 2e9, true},

		// Percent
		{"12.5%", 12.5, true},
		{"12,5%", 12.5, true},

		// Not numbers
		{"", 0, false},
		{"total sales", 0, false},
		{"Rp", 0, false},
	}

	for _, c := range cases {
		got, ok := ParseNumber(c.in)
		if ok != c.ok {
			t.Errorf("ParseNumber(%q) ok = %v, want %v", c.in, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if math.Abs(got-c.want) > math.Abs(c.want)*1e-9+1e-9 {
			t.Errorf("ParseNumber(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// The suffix table contains both "m" (million) and "miliar" (billion).
// Getting this wrong is a 1000x error in a currency the product's first
// customers actually use, so it gets its own test.
func TestMagnitudeSuffixPrefersLongestMatch(t *testing.T) {
	got, ok := ParseNumber("Rp 2,5 miliar")
	if !ok {
		t.Fatal("ParseNumber failed on 'Rp 2,5 miliar'")
	}
	if got != 2.5e9 {
		t.Errorf("miliar parsed as %v, want 2.5e9 (matched 'm' instead of 'miliar'?)", got)
	}
}

func TestExtractNumbersFromProse(t *testing.T) {
	reply := `Total Sales for December 2024: **Rp 3,86 Miliar**

Broken down by channel:
| Channel | Sales |
| --- | --- |
| In-Store | Rp 2.320.000.000 |
| Online | Rp 1.543.405.700 |

That is a 4.2% increase over November.`

	nums := ExtractNumbers(reply)
	if len(nums) < 5 {
		t.Fatalf("expected at least 5 numbers, got %d: %+v", len(nums), nums)
	}

	var sawHeadline, sawInStore, sawPercent bool
	for _, n := range nums {
		switch {
		case n.Value == 3.86e9 && n.Magnitude == 1e9:
			sawHeadline = true
		case n.Value == 2320000000:
			sawInStore = true
		case n.IsPercent && n.Value == 4.2:
			sawPercent = true
		}
	}
	if !sawHeadline {
		t.Error("did not recover the headline 'Rp 3,86 Miliar'")
	}
	if !sawInStore {
		t.Error("did not recover the in-store table cell")
	}
	if !sawPercent {
		t.Error("did not recover the percentage")
	}
}

func TestExtractNumbersIgnoresYearsInWords(t *testing.T) {
	// "December 2024" should still yield 2024 — the comparator filters by
	// value, not by whether a number looks like a year. What must NOT
	// happen is a crash or a mangled value.
	nums := ExtractNumbers("Total for December 2024 was 1.5M")
	if len(nums) != 2 {
		t.Fatalf("got %d numbers, want 2: %+v", len(nums), nums)
	}
	if nums[0].Value != 2024 {
		t.Errorf("first number = %v, want 2024", nums[0].Value)
	}
	if nums[1].Value != 1.5e6 {
		t.Errorf("second number = %v, want 1.5e6", nums[1].Value)
	}
}

// The exact failure from the T-00 smoke test (environment-notes C-1): a
// placeholder figure reported against a true value 3,100x larger. The
// comparator must call this wrong under every tolerance a golden case would
// plausibly use.
func TestMatchesRejectsTheSmokeTestFabrication(t *testing.T) {
	const trueValue = 3863405700.0

	fabricated, ok := Parse("$1,234,567.89")
	if !ok {
		t.Fatal("could not parse the fabricated figure")
	}
	for _, tol := range []float64{0.001, 0.01, 0.05, 0.5} {
		if Matches(fabricated, trueValue, tol) {
			t.Errorf("fabricated value accepted at tolerance %v", tol)
		}
	}
}

func TestMatchesAcceptsReadableRounding(t *testing.T) {
	const trueValue = 3863405700.0

	rounded, ok := Parse("Rp 3,86 Miliar")
	if !ok {
		t.Fatal("could not parse the rounded figure")
	}
	// 3.86e9 is 0.14% off — inside a 1% tolerance on its own merits.
	if !Matches(rounded, trueValue, 0.01) {
		t.Error("readable rounded answer rejected at 1% tolerance")
	}

	// And a case where only the magnitude slack saves it. 3_864_999_000
	// rounds to "3,86 Miliar" — correct to every digit the suffix form can
	// express — but it is 0.13% off, which a 0.01% tolerance would reject
	// on relative error alone.
	if !Matches(rounded, 3_864_999_000, 0.0001) {
		t.Error("suffix-limited precision rejected despite magnitude slack")
	}

	// The slack is half a unit of the last digit, not a blank cheque:
	// "3,87 Miliar" is a wrong rounding of 3,863,405,700 and must fail.
	wrongRounding, _ := Parse("Rp 3,87 Miliar")
	if Matches(wrongRounding, trueValue, 0.0001) {
		t.Error("magnitude slack accepted a value outside half a unit of the last digit")
	}
}

func TestMatchesZero(t *testing.T) {
	zero, _ := Parse("0")
	if !Matches(zero, 0, 0.01) {
		t.Error("0 should match 0")
	}
	nonZero, _ := Parse("5")
	if Matches(nonZero, 0, 0.01) {
		t.Error("5 should not match 0")
	}
}
