package tools

import (
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

func TestClassifyColumnName(t *testing.T) {
	cases := []struct {
		column string
		want   piiClass
	}{
		{"email", piiContact},
		{"email_address", piiContact},
		{"customer_email", piiContact},
		{"no_hp", piiContact},
		{"phone_number", piiContact},
		{"nik", piiIdentity},
		{"customer_nik", piiIdentity},
		{"no_ktp", piiIdentity},
		{"npwp", piiIdentity},
		{"card_number", piiIdentity},
		{"tax_id", piiIdentity},
		// The probe's ordinary subjects, which must stay probeable — this is
		// the case T-Q9 exists for and a name list that swallowed it would cost
		// more than it saves.
		{"month_name", piiNone},
		{"city", piiNone},
		{"channel", piiNone},
		{"product_category", piiNone},
		{"status", piiNone},
		// Near-misses: the class is announced by the whole word, not by a
		// substring of another one.
		{"emails_sent_count", piiNone},
		{"discard_reason", piiNone},
	}
	for _, tc := range cases {
		if got := classifyColumnName(tc.column); got != tc.want {
			t.Errorf("classifyColumnName(%q) = %q, want %q", tc.column, got, tc.want)
		}
	}
}

func TestClassifyValue(t *testing.T) {
	cases := []struct {
		value string
		want  piiClass
	}{
		// distinctValues quotes what it returns, so the quoted form is what
		// this function actually sees.
		{`"budi@example.co.id"`, piiContact},
		{`"+62 812-3456-7890"`, piiContact},
		{`"08123456789"`, piiContact},
		{`"3201234567890123"`, piiIdentity}, // NIK, 16 digits
		{`"4111111111111111"`, piiIdentity}, // card, 16 digits
		{`"123-45-6789"`, piiIdentity},      // SSN
		// The ordinary contents of a probed column.
		{`"December "`, piiNone},
		{`"Bandung"`, piiNone},
		{`"web"`, piiNone},
		{`""`, piiNone},
		// A decimal is a number, not an identifier: an amount column must not
		// read as PII, or the probe loses the case it was written for.
		{`"1500000.50"`, piiNone},
		{`"2024"`, piiNone},
		{`"12345"`, piiNone},
	}
	for _, tc := range cases {
		if got := classifyValue(tc.value); got != tc.want {
			t.Errorf("classifyValue(%q) = %q, want %q", tc.value, got, tc.want)
		}
	}
}

// One PII value in a column of ordinary ones condemns the whole column: a
// filtered sample would disclose the same class of thing while looking careful.
func TestClassifyValuesTakesTheMostProtectiveVerdict(t *testing.T) {
	cases := []struct {
		name   string
		values []string
		want   piiClass
	}{
		{"all ordinary", []string{`"Bandung"`, `"Jakarta"`}, piiNone},
		{"one email among labels", []string{`"web"`, `"budi@example.co.id"`, `"toko"`}, piiContact},
		{"identity beats contact", []string{`"budi@example.co.id"`, `"3201234567890123"`}, piiIdentity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyValues(tc.values); got != tc.want {
				t.Errorf("classifyValues(%v) = %q, want %q", tc.values, got, tc.want)
			}
		})
	}
}

// The tenant's policy decides, and the default decides against disclosure.
func TestProbeAllowsFollowsTheTenantsMode(t *testing.T) {
	type want struct{ none, contact, identity bool }
	cases := []struct {
		mode domain.PIIRedactionMode
		want want
	}{
		{domain.PIIRedactionStrict, want{true, false, false}},
		{domain.PIIRedactionContactOK, want{true, true, false}},
		{domain.PIIRedactionOff, want{true, true, true}},
		// Unset (a row written before migration 045) and unknown both read as
		// strict, which is what normalizePIIMode is for.
		{"", want{true, false, false}},
		{"nonsense", want{true, false, false}},
	}
	for _, tc := range cases {
		mode := normalizePIIMode(tc.mode)
		got := want{
			none:     probeAllows(piiNone, mode),
			contact:  probeAllows(piiContact, mode),
			identity: probeAllows(piiIdentity, mode),
		}
		if got != tc.want {
			t.Errorf("mode %q: (none, contact, identity) = %v, want %v", tc.mode, got, tc.want)
		}
	}
}
