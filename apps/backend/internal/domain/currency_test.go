package domain

import (
	"strings"
	"testing"
)

func TestCurrencyPrecision(t *testing.T) {
	for _, tc := range []struct {
		code  string
		units int
	}{
		{"IDR", 0}, {"JPY", 0}, {"KRW", 0}, {"VND", 0}, {"CLP", 0},
		{"USD", 2}, {"EUR", 2}, {"SGD", 2}, {"idr", 0}, {" USD ", 2},
	} {
		got, ok := CurrencyMinorUnits(tc.code)
		if !ok {
			t.Errorf("CurrencyMinorUnits(%q) is unknown", tc.code)
			continue
		}
		if got != tc.units {
			t.Errorf("CurrencyMinorUnits(%q) = %d, want %d", tc.code, got, tc.units)
		}
	}
	// An unknown code quantises nothing rather than assuming two: guessing two
	// is the dollar assumption this table exists to end.
	if _, ok := CurrencyMinorUnits("XYZ"); ok {
		t.Error("an unknown currency reported a precision")
	}
}

func TestCompanyCurrency(t *testing.T) {
	four := 4
	twenty := 20
	negative := -1
	for _, tc := range []struct {
		name  string
		co    *Company
		want  Currency
		isSet bool
	}{
		{"rupiah", &Company{DefaultCurrency: "IDR"}, Currency{"IDR", 0, RoundingHalfUp}, true},
		{"dollars", &Company{DefaultCurrency: "USD"}, Currency{"USD", 2, RoundingHalfUp}, true},
		{"banker's", &Company{DefaultCurrency: "USD", CurrencyRounding: RoundingHalfEven}, Currency{"USD", 2, RoundingHalfEven}, true},
		{"override", &Company{DefaultCurrency: "USD", CurrencyMinorUnits: &four}, Currency{"USD", 4, RoundingHalfUp}, true},
		// A nonsense override is ignored rather than obeyed: it would turn
		// every money figure into a twenty-place decimal.
		{"absurd override", &Company{DefaultCurrency: "USD", CurrencyMinorUnits: &twenty}, Currency{"USD", 2, RoundingHalfUp}, true},
		{"negative override", &Company{DefaultCurrency: "USD", CurrencyMinorUnits: &negative}, Currency{"USD", 2, RoundingHalfUp}, true},
		// The "behaves exactly as today" paths.
		{"unset", &Company{}, Currency{}, false},
		{"unknown code", &Company{DefaultCurrency: "XYZ"}, Currency{}, false},
		{"nil company", nil, Currency{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.co.Currency()
			if got != tc.want {
				t.Errorf("Currency() = %+v, want %+v", got, tc.want)
			}
			if got.IsSet() != tc.isSet {
				t.Errorf("IsSet() = %v, want %v", got.IsSet(), tc.isSet)
			}
		})
	}
}

// A company with no currency adds no line, which is what keeps its prompt
// byte-identical to the one it had before this feature.
func TestCurrencyLine(t *testing.T) {
	if got := (Currency{}).Line(); got != "" {
		t.Errorf("an unset currency rendered %q, want nothing", got)
	}
	idr := (&Company{DefaultCurrency: "IDR"}).Currency().Line()
	if !strings.Contains(idr, "NO decimal places") || !strings.Contains(idr, "away from zero") {
		t.Errorf("the rupiah line does not state its convention: %q", idr)
	}
	usd := (&Company{DefaultCurrency: "USD", CurrencyRounding: RoundingHalfEven}).Currency().Line()
	if !strings.Contains(usd, "2 decimal places") || !strings.Contains(usd, "banker's rounding") {
		t.Errorf("the dollar line does not state its convention: %q", usd)
	}
}

func TestRoundingModeValidity(t *testing.T) {
	for _, m := range []RoundingMode{"", RoundingHalfUp, RoundingHalfEven} {
		if !m.Valid() {
			t.Errorf("RoundingMode(%q) should be valid", m)
		}
	}
	if RoundingMode("floor").Valid() {
		t.Error("an unknown rounding mode was accepted")
	}
	if RoundingMode("").OrDefault() != RoundingHalfUp {
		t.Error("an unstated convention must resolve to half-up, which is what every existing answer already did")
	}
}
