package app

import (
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// Every currency this product lets a tenant pick has a stated precision.
//
// The two lists live in two packages — the accepted codes here, the decimal
// places in domain — and the failure mode without this test is silent: adding
// a currency to validCurrencies would let a tenant select it, and
// Company.Currency would then return the zero value, so every money figure for
// that tenant would quietly stop being quantised. A currency that is offered
// and not quantised looks exactly like a currency that is not configured.
func TestEverySupportedCurrencyHasAStatedPrecision(t *testing.T) {
	for _, code := range SupportedCurrencies() {
		units, ok := domain.CurrencyMinorUnits(code)
		if !ok {
			t.Errorf("currency %q is offered to tenants and has no stated precision in domain.currencyMinorUnits", code)
			continue
		}
		if units < 0 || units > domain.MaxCurrencyMinorUnits {
			t.Errorf("currency %q has %d decimal places, outside 0..%d", code, units, domain.MaxCurrencyMinorUnits)
		}
	}
}

// And the other direction: a precision stated for a code nobody can pick is
// dead weight that will be read as support.
func TestEveryStatedPrecisionIsForASupportedCurrency(t *testing.T) {
	supported := map[string]bool{}
	for _, c := range SupportedCurrencies() {
		supported[c] = true
	}
	for _, code := range domain.CurrenciesWithStatedPrecision() {
		if !supported[code] {
			t.Errorf("domain states a precision for %q, which no tenant can select", code)
		}
	}
}
