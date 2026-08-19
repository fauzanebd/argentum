package tools

import (
	"encoding/json"
	"fmt"
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

// classifyResultValues is the query-time verdict on a column of driver values,
// and it differs from classifyValues in one rule: on a column the driver
// returned as numbers, the phone pattern is switched off.
//
// `doctable.ClassifyPII` reached this rule first, at publish time, and the
// argument is the same one commit later: `T-P4` types a rupiah column by
// stripping the separators that make `3.377.718.500` legible, so what arrives
// here is `3377718500` — ten bare digits, indistinguishable from a phone number
// to any pattern loose enough to catch a real one. The empty-result probe never
// hit this because it reads `distinctValues`, which are strings by the time it
// sees them; the redaction added beside it inherited the blindness.
//
// **Only the phone pattern.** An email address is not a Go number, so a
// `contact` verdict on a numeric column can only have come from the phone
// pattern — which is what makes demoting it here exactly as narrow as
// doctable's `!numeric` guard. Identity and card patterns stay on everywhere,
// because a column of national identity numbers types as an integer and that is
// the class where a mistake matters most.
func classifyResultValues(values []interface{}) piiClass {
	numeric := len(values) > 0
	strs := make([]string, 0, len(values))
	for _, v := range values {
		strs = append(strs, fmt.Sprintf("%v", v))
		if !isNumericValue(v) {
			numeric = false
		}
	}
	worst := piiNone
	for _, s := range strs {
		switch classifyValue(s) {
		case piiIdentity:
			return piiIdentity
		case piiContact:
			// An email is an email whatever the column looks like.
			if emailValue.MatchString(strings.TrimSpace(strings.Trim(s, `"`))) {
				worst = piiContact
				continue
			}
			// Everything else here reached `contact` through the phone
			// pattern, which is the one rule narrowed: off entirely on a
			// numeric column, and on a text column only for values shaped
			// like a phone number rather than like a figure.
			if !numeric && phoneShaped(s) {
				worst = piiContact
			}
		}
	}
	return worst
}

// phoneShaped reports whether a digit run is written the way a phone number is
// written rather than the way a figure is.
//
// Needed because the type rule above cannot see an aggregate: `SUM(nilai)` over
// a bigint returns a Postgres `numeric`, lib/pq delivers it as bytes and the
// connection layer turns those into a **string** on purpose — coercing a numeric
// wider than float64 is `native-dashboards.md` defect 3. So every total an
// analyst asks for arrives here as bare digits, and T-P13's answer score caught
// one being withheld from the tenant that owns it.
//
// What separates the two in practice is punctuation a person types and a
// stripped figure never carries: a `+`, a leading zero (`08…`, `021…` — every
// Indonesian number has one), or the spaces, dashes and parentheses of a form
// field. The residual is a bare international number with none of those, and it
// is covered by the column-name rule, which is how such a column is named in
// every case this repository has seen. Pinned by
// TestABareInternationalNumberInAnUnnamedColumnIsNotCaught.
func phoneShaped(v string) bool {
	s := strings.TrimSpace(strings.Trim(v, `"`))
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "+") || strings.HasPrefix(s, "0") {
		return true
	}
	return strings.ContainsAny(s, " -()")
}

// isNumericValue reports whether the driver handed back a number rather than
// text. A string of digits is deliberately not numeric: a tenant who stores
// phone numbers in a text column stores them with their leading zero, and that
// column must still be withheld.
func isNumericValue(v interface{}) bool {
	switch v.(type) {
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return true
	case json.Number:
		return true
	default:
		return false
	}
}

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

// --- Result-row redaction (T-P12) -------------------------------------------

// redactedMarkerFor is what a withheld value is replaced with. It names the
// class rather than blanking the cell, and that choice is the whole point: an
// emptied column is the zero-row hazard again — the model cannot tell "no value
// here" from "not allowed to see it", and this repository has twice watched a
// model answer the first by inventing something. A marker the model can read is
// a fact it can repeat to the user.
func redactedMarkerFor(c piiClass) string {
	switch c {
	case piiIdentity:
		return "[IDENTITY REDACTED]"
	case piiContact:
		return "[CONTACT REDACTED]"
	default:
		return "[REDACTED]"
	}
}

// RedactResultColumns withholds the columns a tenant's redaction mode does not
// allow, and reports which ones it took (T-P12).
//
// **Why this exists.** T-P12 asked for the tenant's `PIIRedactionMode` to be
// respected "in what `run_sql` returns from a document source, using the same
// code path T-H10 established". T-H10's path is the *zero-row probe*, which
// only ever runs on a result with no rows in it — so until the 2026-08-19 gate
// nothing inspected a result that had rows, and a `strict` tenant's published
// customer list came back with three real email addresses on it.
//
// **Whole column, not cell by cell.** The same argument the probe makes at the
// top of this file: one email among twenty rows means the column holds emails,
// and returning the other nineteen discloses the same class of thing while
// looking careful.
//
// **Why the classification is re-derived here rather than read off
// `document_tables.columns`.** The stored classification is what a reviewer saw
// and can override, and an override is a statement about what to *show in
// review* — not permission to hand identity numbers to a model. Re-deriving
// costs one pass over a capped result and cannot be turned off by an edit made
// somewhere else.
func RedactResultColumns(columns []string, rows []map[string]interface{}, mode domain.PIIRedactionMode) []string {
	if len(columns) == 0 || len(rows) == 0 {
		return nil
	}
	mode = normalizePIIMode(mode)

	var redacted []string
	for _, col := range columns {
		class := classifyColumnName(col)
		if class == piiNone {
			values := make([]interface{}, 0, len(rows))
			for _, row := range rows {
				if v, ok := row[col]; ok && v != nil {
					values = append(values, v)
				}
			}
			class = classifyResultValues(values)
		}
		if class == piiNone || probeAllows(class, mode) {
			continue
		}
		marker := redactedMarkerFor(class)
		for _, row := range rows {
			if _, ok := row[col]; ok {
				row[col] = marker
			}
		}
		redacted = append(redacted, col)
	}
	return redacted
}
