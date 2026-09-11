package domain

import (
	"strconv"
	"strings"
)

// Currency is how much precision a money figure has, and which way it rounds
// when it has to lose some (T-W2).
//
// **Why this is not a formatting concern.** Exact arithmetic over the wrong
// convention is exactly wrong. `T-W1` made `compute` produce a figure no model
// retyped; this decides how many decimal places that figure is entitled to. A
// rupiah amount carrying two decimals is a dollar assumption that has already
// gone wrong somewhere upstream, and a half-cent that rounds the wrong way is
// the difference a reconciliation exists to find.
//
// **Stated, not assumed.** The three fields are separate and named because
// each answers a different question — which currency, how many places, and
// which way at the midpoint — and the last of those is an accounting policy
// rather than a fact about the money. It is rendered into the prompt as a fact
// so a tenant reading an answer can see which convention produced it.
type Currency struct {
	// Code is ISO 4217 — "IDR", "USD". Empty means this company has no
	// currency configured, and nothing is quantised.
	Code string `json:"code"`
	// MinorUnits is how many decimal places the currency has: 0 for rupiah and
	// yen, 2 for dollars and euros.
	MinorUnits int `json:"minor_units"`
	// Rounding is what happens at exactly half a minor unit.
	Rounding RoundingMode `json:"rounding"`
}

// IsSet reports whether this company has a currency to quantise against.
func (c Currency) IsSet() bool { return c.Code != "" }

// Line is the sentence the prompt carries, so the convention a figure was
// produced under is visible rather than implied.
//
// Emitted for every configured currency rather than only for the unusual ones
// — unlike CompanyProfile.fiscalLine, which stays quiet about a January fiscal
// year. The asymmetry is deliberate: "the year starts when the year starts" is
// the reader's own assumption confirmed, while "money here has no decimal
// places" is the thing a model trained mostly on dollars will otherwise get
// wrong.
func (c Currency) Line() string {
	if !c.IsSet() {
		return ""
	}
	var b strings.Builder
	b.WriteString("Money is " + c.Code + ", written with ")
	switch c.MinorUnits {
	case 0:
		b.WriteString("NO decimal places")
	case 1:
		b.WriteString("1 decimal place")
	default:
		b.WriteString(strconv.Itoa(c.MinorUnits) + " decimal places")
	}
	b.WriteString(". A money figure from `compute` is already rounded ")
	if c.Rounding.OrDefault() == RoundingHalfEven {
		b.WriteString("to the nearest even digit at a half (banker's rounding)")
	} else {
		b.WriteString("away from zero at a half")
	}
	b.WriteString("; do not round it again.")
	return b.String()
}

// RoundingMode is what a money figure does at exactly half a minor unit.
type RoundingMode string

const (
	// RoundingHalfUp rounds a half away from zero: 2.5 → 3, -2.5 → -3. What a
	// person expects and what a spreadsheet does.
	RoundingHalfUp RoundingMode = "half_up"
	// RoundingHalfEven rounds a half to the nearest even digit: 2.5 → 2,
	// 3.5 → 4. Banker's rounding, which several accounting standards require
	// because it does not drift upward over a long ledger.
	RoundingHalfEven RoundingMode = "half_even"
)

// Valid reports whether the mode is one this product knows. The empty string
// is valid and means "not stated" — see OrDefault.
func (m RoundingMode) Valid() bool {
	switch m {
	case "", RoundingHalfUp, RoundingHalfEven:
		return true
	default:
		return false
	}
}

// OrDefault resolves an unstated convention to half-up.
//
// A default is unavoidable here — something has to happen at 2.5 — so the
// choice is between a default nobody can see and one that is written down.
// This is the written-down one: half-up is what every existing answer in this
// product already did, so an unstated policy changes nothing, and the resolved
// value is rendered into the prompt by Line() rather than left implicit.
func (m RoundingMode) OrDefault() RoundingMode {
	if m == RoundingHalfEven {
		return RoundingHalfEven
	}
	return RoundingHalfUp
}

// currencyMinorUnits is how many decimal places each supported currency has.
//
// **IDR is 0 here and ISO 4217 says 2, and that is deliberate.** The sen was
// withdrawn from circulation decades ago; Indonesian prices, invoices and
// ledgers are whole rupiah, and this product's own reports have rendered them
// that way since T-R2. Writing 2 because a standard says so would produce
// `Rp 1.234.567,00` on every figure — a dollar's shape wearing a rupiah's
// name. The same practice-over-standard call is *not* made for any other
// currency in this table.
//
// Every code here is one internal/app's validCurrencies accepts; the parity is
// asserted by a test, so adding a currency there fails until its precision is
// stated here.
var currencyMinorUnits = map[string]int{
	// Zero-decimal by ISO 4217.
	"JPY": 0, "KRW": 0, "VND": 0, "CLP": 0,
	// Zero-decimal in practice — see the note above.
	"IDR": 0,
	// Two-decimal, which is everything else this product accepts.
	"USD": 2, "EUR": 2, "GBP": 2, "CNY": 2, "SGD": 2, "MYR": 2, "THB": 2,
	"PHP": 2, "INR": 2, "AUD": 2, "NZD": 2, "CAD": 2, "CHF": 2, "HKD": 2,
	"TWD": 2, "BRL": 2, "MXN": 2, "ZAR": 2, "AED": 2, "SAR": 2, "SEK": 2,
	"NOK": 2, "DKK": 2, "PLN": 2, "TRY": 2, "RUB": 2, "COP": 2, "ARS": 2,
	"PEN": 2, "EGP": 2, "NGN": 2, "KES": 2, "GHS": 2, "BDT": 2, "PKR": 2,
	"LKR": 2, "MMK": 2, "KHR": 2, "LAK": 2,
}

// CurrencyMinorUnits returns a currency's decimal places, and false for a code
// this product does not know. An unknown code quantises nothing, because
// guessing two would be the dollar assumption this file exists to end.
func CurrencyMinorUnits(code string) (int, bool) {
	n, ok := currencyMinorUnits[strings.ToUpper(strings.TrimSpace(code))]
	return n, ok
}

// MaxCurrencyMinorUnits bounds an admin's override. Four is past every real
// currency — the most any ISO 4217 entry carries is three — and stops a typo
// from turning every money figure into a twenty-place decimal.
const MaxCurrencyMinorUnits = 4

// Currency assembles the company's money convention.
//
// The code lives on `companies.default_currency`, where it has been since
// before this feature: a second currency column on company_profiles would be
// two rows expressing one idea, which is the rot this repository has avoided
// everywhere else. What T-W2 adds beside it is the precision and the policy.
//
// An unrecognised or empty code yields a zero Currency, which quantises
// nothing — the "behaves exactly as today" path.
func (c *Company) Currency() Currency {
	if c == nil {
		return Currency{}
	}
	code := strings.ToUpper(strings.TrimSpace(c.DefaultCurrency))
	units, known := CurrencyMinorUnits(code)
	if !known {
		return Currency{}
	}
	// An explicit override wins over the table. A tenant who prices in
	// half-cents knows something about their ledger that ISO 4217 does not.
	if c.CurrencyMinorUnits != nil && *c.CurrencyMinorUnits >= 0 && *c.CurrencyMinorUnits <= MaxCurrencyMinorUnits {
		units = *c.CurrencyMinorUnits
	}
	return Currency{Code: code, MinorUnits: units, Rounding: c.CurrencyRounding.OrDefault()}
}

// CurrenciesWithStatedPrecision lists every code this package knows the
// precision of. Exported for the parity test that keeps this table and
// internal/app's accepted-currency list from drifting apart — a currency
// offered in the form and missing here would silently stop being quantised,
// which looks exactly like a currency that was never configured.
func CurrenciesWithStatedPrecision() []string {
	out := make([]string, 0, len(currencyMinorUnits))
	for code := range currencyMinorUnits {
		out = append(out, code)
	}
	return out
}
