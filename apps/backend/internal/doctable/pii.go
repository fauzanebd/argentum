package doctable

import (
	"regexp"
	"strings"
)

// Personal data in an extracted table (T-P12).
//
// The documents a BI tenant uploads are bank statements, payroll summaries and
// customer lists. This product already has a position on personal data — a
// redaction mode per company, and two classes defined in
// `config/guardrails.yaml` — and an ingestion path that ignored all of it would
// be the one place the position does not hold.
//
// **What this does is label, not redact.** The classes are the same two the
// guardrails use, so what a tenant's mode already decides about a warehouse row
// decides the same thing about a document row. What the label adds is the thing
// a reviewer needs before pressing Apply: publishing a column of national
// identity numbers should be a decision somebody made, and it cannot be one if
// nothing on the screen says that is what the column holds.

// PII classes, matching `config/guardrails.yaml`. Kept as strings rather than a
// new enum because they are the guardrails' vocabulary and a second spelling of
// "contact" would be a bug nobody notices until a mode stops working.
const (
	// PIIContact is an email address or a phone number — the fields a customer
	// list is made of, and the ones a tenant asking for one has to be able to
	// see. A `contact_ok` tenant reads these.
	PIIContact = "contact"
	// PIIIdentity is a national id, a tax number or a card number. No BI
	// question needs one, and only a mode of `off` lets them through.
	PIIIdentity = "identity"
)

var (
	emailCell = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[a-zA-Z]{2,}$`)
	// A phone number as this deployment's documents write them: +62, 62 or 0
	// prefixes, with spaces, hyphens or brackets between the groups. **Not
	// dots**: a dot is Indonesia's thousands separator, and allowing it made
	// every rupiah figure in T-P13's corpus match this pattern — three of the
	// eight born-digital fixtures had their revenue column labelled as contact
	// details.
	phoneCell = regexp.MustCompile(`^\+?[\d][\d\s\-()]{7,17}$`)
	// NIK — Indonesia's national identity number — is exactly sixteen digits,
	// and NPWP (tax) is fifteen. Both are written with and without separators.
	identityCell = regexp.MustCompile(`^\d[\d.\-]{13,20}\d$`)
	// A card number is 13–19 digits in groups of four. Checked separately from
	// the identity pattern because a Luhn check is what tells it from an
	// arbitrary long number, and this is where a false positive is cheapest to
	// avoid.
	cardCell = regexp.MustCompile(`^(?:\d[ -]?){13,19}$`)
)

// headerHints are the words a column of personal data is titled with, in both
// languages this deployment reads. English-only would miss most of them: this
// product's documents are mostly Indonesian, which is the T-Q3 lesson applied
// before a gate has to teach it again.
var headerHints = map[string]string{
	"email": PIIContact, "e-mail": PIIContact, "surel": PIIContact,
	"phone": PIIContact, "telepon": PIIContact, "telp": PIIContact,
	"hp": PIIContact, "handphone": PIIContact, "whatsapp": PIIContact, "wa": PIIContact,
	"kontak": PIIContact, "contact": PIIContact,
	"nik": PIIIdentity, "ktp": PIIIdentity, "npwp": PIIIdentity,
	"passport": PIIIdentity, "paspor": PIIIdentity, "ssn": PIIIdentity,
	"identity": PIIIdentity, "identitas": PIIIdentity,
	"card": PIIIdentity, "kartu": PIIIdentity, "rekening": PIIIdentity,
	"account_number": PIIIdentity, "no_rekening": PIIIdentity,
}

// ClassifyPII labels the columns that hold personal data.
//
// Two signals, and the header wins. A header that says `nik` is the document
// telling us what the column is; the cell patterns are a guess about what the
// values look like, and they are there for the export whose header is `no.` or
// blank. Where the two disagree, the stronger class wins — identity over
// contact — because the cost of over-labelling is a reviewer reading one extra
// sentence and the cost of under-labelling is a national id in a dashboard.
func ClassifyPII(t *Table) {
	for c := range t.Columns {
		col := &t.Columns[c]
		class := classFromHeader(col.Header)
		// On a numeric column the phone pattern is switched off, and only that
		// one. A figure written the way this deployment's documents write
		// figures — 3.377.718.500 — is a plausible phone number to any pattern
		// loose enough to catch a real one, and T-P13's corpus found three
		// revenue columns labelled as contact details because of it. The
		// identity and card patterns stay on everywhere: they need fifteen
		// digits or a Luhn checksum, which a rupiah amount does not have, and a
		// column of national identity numbers types as an integer — so
		// switching them off here would miss the class that matters most.
		if cellClass := classFromCells(t.Rows, c, col.Type.Numeric()); rank(cellClass) > rank(class) {
			class = cellClass
		}
		col.PII = class
	}
}

func classFromHeader(header string) string {
	h := strings.ToLower(header)
	// Word-ish matching rather than substring: "wa" is a contact column and
	// "warehouse" is not, and a substring test would label every warehouse
	// report's first column as personal data.
	fields := regexp.MustCompile(`[^a-z0-9]+`).Split(h, -1)
	best := ""
	for _, f := range fields {
		if class, ok := headerHints[f]; ok && rank(class) > rank(best) {
			best = class
		}
	}
	// Two-word forms the split above breaks apart.
	for hint, class := range headerHints {
		if strings.Contains(hint, "_") && strings.Contains(h, strings.ReplaceAll(hint, "_", " ")) {
			if rank(class) > rank(best) {
				best = class
			}
		}
	}
	return best
}

// classFromCells reads the values. A column is only labelled when *most* of its
// non-empty cells match — one email address in a notes column is a note, and
// labelling that column would redact the notes.
func classFromCells(rows []Row, c int, numeric bool) string {
	const share = 0.6
	var total, emails, phones, identities int
	for _, r := range rows {
		if c >= len(r.Cells) {
			continue
		}
		v := strings.TrimSpace(r.Cells[c].Raw)
		if v == "" {
			continue
		}
		total++
		switch {
		case emailCell.MatchString(v):
			emails++
		case identityCell.MatchString(v) && digitsOnly(v) >= 15:
			identities++
		case cardCell.MatchString(v) && luhn(v):
			identities++
		case !numeric && phoneCell.MatchString(v) && digitsOnly(v) >= 8 && digitsOnly(v) <= 15:
			phones++
		}
	}
	if total == 0 {
		return ""
	}
	switch {
	case float64(identities)/float64(total) >= share:
		return PIIIdentity
	case float64(emails)/float64(total) >= share, float64(phones)/float64(total) >= share:
		return PIIContact
	default:
		return ""
	}
}

func rank(class string) int {
	switch class {
	case PIIIdentity:
		return 2
	case PIIContact:
		return 1
	default:
		return 0
	}
}

func digitsOnly(s string) int {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n++
		}
	}
	return n
}

// luhn is what separates a card number from any other long run of digits. It is
// worth the twelve lines: without it every invoice number of the right length
// would be labelled as personal data, and a classifier that cries wolf is one a
// reviewer learns to click past.
func luhn(s string) bool {
	sum, alt := 0, false
	for i := len(s) - 1; i >= 0; i-- {
		r := s[i]
		if r < '0' || r > '9' {
			continue
		}
		d := int(r - '0')
		if alt {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		alt = !alt
	}
	return sum%10 == 0
}
