package tools

import (
	"regexp"
	"strings"

	"github.com/fauzanebd/argentum/internal/domain"
)

// What the empty-result probe is allowed to disclose (T-H10).
//
// **The hole.** `distinctValues` answers a zero-row query by handing the model
// the filtered column's real contents. That is the right answer for
// `month_name = 'December'` against a column padded to `'December '` — the case
// the probe was written for — and the wrong one for
// `email = 'budi@examle.co.id'` against a column of real customer emails: the
// user typo'd a domain and received twenty addresses their own query did not
// return, none of which any guardrail sees, because output redaction runs on
// the reply and this is a tool result.
//
// **Two filters, because either alone is wrong.** A column *named* `email`
// gives itself away; a column named `keterangan` holding addresses does not.
// So the name is checked, and then the values that came back are checked, and a
// column that fails either check is dropped whole rather than value by value —
// one email among twenty rows means the column holds emails, and returning the
// other nineteen would disclose the same class of thing while looking careful.
//
// **The classes are the guardrails' own** (`config/guardrails.yaml:14-15`):
// `contact` is emails and phone numbers, `identity` is NIK/KTP, SSN and card
// numbers. They are re-stated here rather than imported because these are
// column names and value shapes in a warehouse, not redaction rules over a
// reply, and the two lists drift for good reasons.
type piiClass string

const (
	piiNone     piiClass = ""
	piiContact  piiClass = "contact"
	piiIdentity piiClass = "identity"
)

// contactColumn and identityColumn match a column *name* that announces what it
// holds. Word-boundary-ish anchors on `_` and the ends of the name, so
// `email_address` and `cust_nik` match while `emails_sent_count` and
// `nikko_brand` do not.
var (
	contactColumn = regexp.MustCompile(
		`(?i)(^|_)(e?mail|email_address|phone|phone_number|telp|telepon|no_telp|hp|no_hp|mobile|whatsapp|wa_number|msisdn|contact|kontak)(_|$)`)
	identityColumn = regexp.MustCompile(
		`(?i)(^|_)(nik|ktp|no_ktp|npwp|nisn|ssn|sin|passport|paspor|no_paspor|cc|card|card_number|credit_card|cvv|iban|rekening|no_rek|account_number|tax_id)(_|$)`)
)

// The value shapes, for the column whose name says nothing. Deliberately
// broader than the name lists: a false positive costs the probe one column and
// the model keeps the plain zero-row note, which is exactly what it had before
// this probe existed.
var (
	emailValue = regexp.MustCompile(`(?i)^[^@\s]+@[^@\s]+\.[a-z]{2,}$`)
	// A phone number as a tenant stores one: an optional +, then 8–15 digits,
	// once the spaces, dashes and parentheses people type into a form are gone.
	phoneValue = regexp.MustCompile(`^\+?\d{8,15}$`)
	// 13–19 digits is a card number, a NIK (16) or an NPWP (15).
	longDigitsValue = regexp.MustCompile(`^\d{13,19}$`)
	ssnValue        = regexp.MustCompile(`^\d{3}-\d{2}-\d{4}$`)
	// Not the decimal point: a value carrying one is a number rather than a
	// phone, and an amount column that reads as `contact` would cost the probe
	// the ordinary case it exists for.
	phoneSeparators = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "")
)

// classifyColumnName reports what a column's name says it holds.
func classifyColumnName(column string) piiClass {
	name := strings.ToLower(strings.TrimSpace(column))
	switch {
	case identityColumn.MatchString(name):
		return piiIdentity
	case contactColumn.MatchString(name):
		return piiContact
	default:
		return piiNone
	}
}

// classifyValue reports what one returned value looks like. Values arrive from
// distinctValues already quoted with %q, so the quotes are trimmed first — the
// quoting is what makes a trailing space visible and it is not part of the
// datum.
func classifyValue(v string) piiClass {
	s := strings.TrimSpace(strings.Trim(v, `"`))
	if s == "" {
		return piiNone
	}
	// A value with a decimal point in it is a number, not an identifier and not
	// a phone; classified before the digit runs so `1500000.50` stays ordinary.
	bare := ""
	if !strings.Contains(s, ".") {
		bare = phoneSeparators.Replace(s)
	}
	switch {
	case ssnValue.MatchString(s), bare != "" && longDigitsValue.MatchString(bare):
		return piiIdentity
	case emailValue.MatchString(s), bare != "" && phoneValue.MatchString(bare):
		return piiContact
	default:
		return piiNone
	}
}

// classifyValues is the column's verdict from its contents: the most protective
// class any single value earns. One identity value in a column of twenty makes
// the column identity, for the reason in this file's opening comment.
func classifyValues(values []string) piiClass {
	worst := piiNone
	for _, v := range values {
		switch classifyValue(v) {
		case piiIdentity:
			return piiIdentity
		case piiContact:
			worst = piiContact
		}
	}
	return worst
}

// probeAllows reports whether a column of class c may be disclosed to the model
// under the tenant's redaction mode.
//
// The mapping is the tenant's own policy, read the only way it can honestly be
// read here: `contact_ok` is a tenant saying "we want customer contact details
// in answers", so a contact-class probe is what they asked for; `off` is a
// tenant who has switched redaction off entirely; `strict` — and an unset or
// unknown value, and a company we could not read — discloses neither class.
//
// Note what this does NOT do: it never widens what the query itself returned.
// The probe only ever runs on a result with zero rows in it, so everything it
// discloses is data the user's own query did not fetch. That asymmetry is why
// the default here is the protective one even though the *output* rules default
// to the same mode for a different reason.
func probeAllows(c piiClass, mode domain.PIIRedactionMode) bool {
	switch c {
	case piiNone:
		return true
	case piiContact:
		return mode == domain.PIIRedactionContactOK || mode == domain.PIIRedactionOff
	case piiIdentity:
		return mode == domain.PIIRedactionOff
	default:
		return false
	}
}

// normalizePIIMode reads an unset, unknown or unreadable policy as strict. A
// company row written before migration 045 carries "", and a lookup that failed
// carries nothing at all; both are answered the same way, because "we could not
// find out" is not a reason to disclose.
func normalizePIIMode(m domain.PIIRedactionMode) domain.PIIRedactionMode {
	if m.Valid() {
		return m
	}
	return domain.PIIRedactionStrict
}
