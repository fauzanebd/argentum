package format

import (
	"math"
	"testing"
	"time"
)

func idOpts() Options  { return Options{Locale: LocaleID, Currency: "IDR", Decimals: AutoDecimals} }
func enOpts() Options  { return Options{Locale: LocaleEN, Currency: "USD", Decimals: AutoDecimals} }
func barOpts() Options { return Options{Locale: LocaleEN, Decimals: AutoDecimals} }

func TestCurrencyConventions(t *testing.T) {
	cases := []struct {
		name string
		v    float64
		o    Options
		want string
	}{
		{"rupiah has no minor unit", 3863405700, idOpts(), "Rp 3.863.405.700"},
		{"rupiah negative keeps the sign in front", -1234567, idOpts(), "-Rp 1.234.567"},
		{"dollars take two places and no space", 2400, enOpts(), "$2,400.00"},
		{"dollars round rather than truncate", 1234.567, enOpts(), "$1,234.57"},
		{"a letter symbol takes a space", 1234, Options{Locale: LocaleEN, Currency: "MYR", Decimals: AutoDecimals}, "RM 1,234.00"},
		{"an unknown code prints as the code", 1200, Options{Locale: LocaleEN, Currency: "XAF", Decimals: AutoDecimals}, "XAF 1,200.00"},
		{"no currency prints a bare number", 1200, barOpts(), "1,200"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Currency(c.v, c.o); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestPercentPrecision(t *testing.T) {
	cases := []struct {
		v    float64
		o    Options
		want string
	}{
		{12.5, enOpts(), "12.5%"},
		{12.5, idOpts(), "12,5%"},
		{-6.24, idOpts(), "-6,2%"},
		// Below one percent, one decimal place throws away the whole signal:
		// a damage rate of 0.42% is not "0.4%".
		{0.42, enOpts(), "0.42%"},
		{0.09, idOpts(), "0,09%"},
	}
	for _, c := range cases {
		if got := Percent(c.v, c.o); got != c.want {
			t.Errorf("Percent(%v): got %q, want %q", c.v, got, c.want)
		}
	}
}

func TestCompactIgnoresCurrencyDecimals(t *testing.T) {
	// The regression this pins: rupiah declares zero decimal places, and a
	// compact form that honoured that rendered 3_863_405_700 as "Rp 4 Miliar".
	o := idOpts()
	o.Compact = true
	if got := Currency(3863405700, o); got != "Rp 3,86 Miliar" {
		t.Errorf("got %q, want %q", got, "Rp 3,86 Miliar")
	}

	e := enOpts()
	e.Compact = true
	// Precision follows the magnitude: two significant decimals below ten,
	// one below a hundred, none above. "$14.3M" is what a KPI card wants.
	if got := Currency(14280000, e); got != "$14.3M" {
		t.Errorf("got %q, want %q", got, "$14.3M")
	}

	// Below the threshold the exact figure is kept: "0,4 Juta" reads as
	// evasion where "400.000" reads as an answer.
	small := idOpts()
	small.Compact = true
	if got := Currency(400000, small); got != "Rp 400.000" {
		t.Errorf("got %q, want %q", got, "Rp 400.000")
	}
}

// The package's central claim: whatever it writes, it can read back. If this
// fails, the eval comparator (T-01) and the report renderer (T-R2) have
// started disagreeing about what a number is.
func TestFormattedValuesParseBack(t *testing.T) {
	values := []float64{0, 1, 1234, -1234, 1234.56, 3863405700, 14280000}
	for _, locale := range []Locale{LocaleID, LocaleEN} {
		for _, currency := range []string{"IDR", "USD", ""} {
			for _, compact := range []bool{false, true} {
				o := Options{Locale: locale, Currency: currency, Decimals: AutoDecimals, Compact: compact}
				for _, v := range values {
					s := Currency(v, o)
					n, ok := Parse(s)
					if !ok {
						t.Errorf("Parse(%q) failed (locale=%s currency=%q compact=%v)", s, locale, currency, compact)
						continue
					}
					if !Matches(n, v, 0.01) {
						t.Errorf("round trip lost the value: %v -> %q -> %v", v, s, n.Value)
					}
				}
			}
		}
	}
}

func TestDates(t *testing.T) {
	when := time.Date(2026, time.July, 27, 9, 30, 0, 0, time.UTC)

	if got := Date(when, idOpts()); got != "27 Juli 2026" {
		t.Errorf("id long: got %q", got)
	}
	if got := Date(when, enOpts()); got != "27 July 2026" {
		t.Errorf("en long: got %q", got)
	}

	short := idOpts()
	short.ShortDate = true
	if got := Date(when, short); got != "27 Jul 2026" {
		t.Errorf("id short: got %q", got)
	}
	// The footer stamp is never abbreviated, whatever the caller asked for.
	if got := DateTime(when, short); got != "27 Juli 2026 09:30" {
		t.Errorf("datetime: got %q", got)
	}

	aug := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	if got := Date(aug, short); got != "1 Agu 2026" {
		t.Errorf("id short August: got %q", got)
	}
}

func TestValueReformatsStringsTheModelWrote(t *testing.T) {
	o := idOpts()
	cases := []struct {
		in   any
		kind Kind
		want string
	}{
		// The model pre-formatted it in the wrong convention; the column wins.
		{"1,234,567", KindNumber, "1.234.567"},
		{"Rp 1.234.567", KindCurrency, "Rp 1.234.567"},
		// A bare number in a percent column is a percentage, not a fraction.
		{"12.5", KindPercent, "12,5%"},
		{12.5, KindPercent, "12,5%"},
		// A cell that carries its own currency keeps the currency — relabelling
		// it would be a factual error — but not its separators: those belong
		// to the document, and one table using both "." and "," as a decimal
		// point is harder to read than either convention is on its own.
		{"$99.50", KindCurrency, "$99,50"},
		// Unparseable text is passed through, never dropped.
		{"n/a", KindNumber, "n/a"},
		{nil, KindNumber, ""},
		{"2026-07-27", KindDate, "27 Juli 2026"},
	}
	for _, c := range cases {
		if got := Value(c.in, c.kind, o); got != c.want {
			t.Errorf("Value(%v, %s): got %q, want %q", c.in, c.kind, got, c.want)
		}
	}
}

func TestInferKind(t *testing.T) {
	cases := []struct {
		name   string
		values []any
		want   Kind
	}{
		{"currency strings", []any{"Rp 1.000", "Rp 2.000"}, KindCurrency},
		{"percent strings", []any{"3,1%", "-6,2%"}, KindPercent},
		{"dates", []any{"2026-01-01", "2026-02-01"}, KindDate},
		{"measurements", []any{1234.5, 9876.0}, KindNumber},
		{"mixed text and numbers stays text", []any{"Widget", 3}, KindText},
		// Small integers are ids, years and counts far more often than they are
		// measurements, and grouping a year into "2.026" is worse than leaving
		// the column alone.
		{"small integers stay text", []any{1, 2, 3}, KindText},
		{"years stay text", []any{2024, 2025, 2026}, KindText},
		{"empty stays text", []any{nil, ""}, KindText},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := InferKind(c.values); got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}

func TestInferDecimals(t *testing.T) {
	if got := InferDecimals([]any{1.0, 2.0, 3.0}); got != 0 {
		t.Errorf("all-integer column: got %d, want 0", got)
	}
	if got := InferDecimals([]any{1.0, 2.5}); got != 2 {
		t.Errorf("mixed column: got %d, want 2", got)
	}
}

// TestColumnDecimals is the materiality rule stated as cases. The two that
// matter most are adjacent on purpose: the same shape of data — whole amounts,
// no cents anywhere — keeps its cents on an invoice and loses them on a
// revenue table, and the only thing separating them is magnitude.
func TestColumnDecimals(t *testing.T) {
	cases := []struct {
		name     string
		values   []any
		kind     Kind
		currency string
		want     int
	}{
		{
			name:     "nine-figure revenue drops a cents field that was only ever zeroes",
			values:   []any{486000000.0, 401000000.0, 254000000.0},
			kind:     KindCurrency,
			currency: "USD",
			want:     0,
		},
		{
			name:     "an invoice's round dollars keep their cents",
			values:   []any{2400.0, 720.0, 240.0},
			kind:     KindCurrency,
			currency: "USD",
			want:     2,
		},
		{
			name:     "one small line in a large column keeps cents on the whole column",
			values:   []any{486000000.0, 40.0},
			kind:     KindCurrency,
			currency: "USD",
			want:     2,
		},
		{
			name:     "cents in the data are never dropped, however large the column",
			values:   []any{486000000.5, 401000000.0},
			kind:     KindCurrency,
			currency: "USD",
			want:     2,
		},
		{
			name:     "rupiah never carries a subunit, whole or not",
			values:   []any{3863405700.0, 268431200.0},
			kind:     KindCurrency,
			currency: "IDR",
			want:     0,
		},
		{
			name:     "a fractional rupiah average is still capped at the currency's minor units",
			values:   []any{1234.5, 998.25},
			kind:     KindCurrency,
			currency: "IDR",
			want:     0,
		},
		{
			name:     "an unknown code is assumed to have cents",
			values:   []any{486000000.0},
			kind:     KindCurrency,
			currency: "XAF",
			want:     0,
		},
		{
			name:     "no currency means no minor-unit question to answer",
			values:   []any{486000000.0},
			kind:     KindCurrency,
			currency: "",
			want:     0,
		},
		{
			name:   "percent keeps the automatic count for its sub-1% rule",
			values: []any{0.42, 12.5},
			kind:   KindPercent,
			want:   AutoDecimals,
		},
		{
			name:   "a plain number column is unaffected",
			values: []any{1.0, 2.5},
			kind:   KindNumber,
			want:   2,
		},
		{
			name:     "an empty column keeps the currency's minor units",
			values:   nil,
			kind:     KindCurrency,
			currency: "USD",
			want:     2,
		},
		{
			name:     "strings the model pre-formatted are read, not ignored",
			values:   []any{"$486,000,000", "$401,000,000"},
			kind:     KindCurrency,
			currency: "USD",
			want:     0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ColumnDecimals(c.values, c.kind, c.currency); got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}

// TestColumnDecimalsReachesTheRenderedString is the same rule one level up: the
// count is only worth anything if Currency honours it, and Currency is the
// function that used to overwrite it.
func TestColumnDecimalsReachesTheRenderedString(t *testing.T) {
	large := []any{486000000.0, 401000000.0}
	o := enOpts()
	o.Decimals = ColumnDecimals(large, KindCurrency, "USD")
	if got := Currency(486000000, o); got != "$486,000,000" {
		t.Errorf("large whole column: got %q, want %q", got, "$486,000,000")
	}

	invoice := []any{2400.0, 720.0}
	o = enOpts()
	o.Decimals = ColumnDecimals(invoice, KindCurrency, "USD")
	if got := Currency(2400, o); got != "$2,400.00" {
		t.Errorf("invoice column: got %q, want %q", got, "$2,400.00")
	}
}

func TestNonFiniteRendersAsADash(t *testing.T) {
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if got := Decimal(v, enOpts()); got != "—" {
			t.Errorf("Decimal(%v): got %q", v, got)
		}
	}
}
