package tools

import (
	"encoding/json"
	"testing"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/domain"
)

// The shape the 2026-08-19 live gate found: a published document table with a
// column of real email addresses, returned whole to a `strict` tenant because
// run_sql only ever consulted the redaction mode on the zero-row probe path.
func rowsWithEmails() []map[string]interface{} {
	return []map[string]interface{}{
		{"pelanggan": "PT Maju", "email": "andi@maju.co.id", "nilai": 1250000},
		{"pelanggan": "CV Sentosa", "email": "budi@sentosa.co.id", "nilai": 980500},
		{"pelanggan": "UD Berkah", "email": "citra@berkah.co.id", "nilai": 760250},
	}
}

func TestStrictWithholdsAContactColumnFromAResultWithRows(t *testing.T) {
	rows := rowsWithEmails()
	redacted := RedactResultColumns([]string{"pelanggan", "email", "nilai"}, rows, domain.PIIRedactionStrict)

	if len(redacted) != 1 || redacted[0] != "email" {
		t.Fatalf("expected the email column withheld, got %v", redacted)
	}
	for i, row := range rows {
		if row["email"] != "[CONTACT REDACTED]" {
			t.Errorf("row %d still carries the address: %v", i, row["email"])
		}
	}
	// The columns that are not personal data are untouched — a redactor that
	// takes the figures too is one nobody can use.
	if rows[0]["pelanggan"] != "PT Maju" || rows[0]["nilai"] != 1250000 {
		t.Errorf("an ordinary column was redacted: %v", rows[0])
	}
}

func TestContactOKReturnsContactAndStillWithholdsIdentity(t *testing.T) {
	rows := rowsWithEmails()
	if redacted := RedactResultColumns([]string{"pelanggan", "email", "nilai"}, rows, domain.PIIRedactionContactOK); len(redacted) != 0 {
		t.Fatalf("contact_ok is a tenant asking for contact details, got %v withheld", redacted)
	}
	if rows[0]["email"] != "andi@maju.co.id" {
		t.Errorf("contact_ok withheld a contact column: %v", rows[0]["email"])
	}

	idRows := []map[string]interface{}{{"nama": "Andi", "nik": "3171234567890123"}}
	redacted := RedactResultColumns([]string{"nama", "nik"}, idRows, domain.PIIRedactionContactOK)
	if len(redacted) != 1 || redacted[0] != "nik" {
		t.Fatalf("contact_ok must still withhold identity, got %v", redacted)
	}
	if idRows[0]["nik"] != "[IDENTITY REDACTED]" {
		t.Errorf("an identity number survived contact_ok: %v", idRows[0]["nik"])
	}
}

func TestOffReturnsEverything(t *testing.T) {
	rows := rowsWithEmails()
	if redacted := RedactResultColumns([]string{"pelanggan", "email", "nilai"}, rows, domain.PIIRedactionOff); len(redacted) != 0 {
		t.Fatalf("off is off, got %v withheld", redacted)
	}
}

// The column whose name says nothing. This is the half a name list alone cannot
// do, and the reason the probe checks values as well.
func TestAColumnNamedNothingIsCaughtByItsValues(t *testing.T) {
	rows := []map[string]interface{}{
		{"keterangan": "andi@maju.co.id"},
		{"keterangan": "sudah lunas"},
	}
	redacted := RedactResultColumns([]string{"keterangan"}, rows, domain.PIIRedactionStrict)
	if len(redacted) != 1 {
		t.Fatalf("a column of addresses named `keterangan` was returned whole")
	}
	// Whole column, not the one offending cell: one address among the rows means
	// the column holds addresses.
	if rows[1]["keterangan"] != "[CONTACT REDACTED]" {
		t.Errorf("the column was redacted cell by cell: %v", rows[1]["keterangan"])
	}
}

// A limit, pinned rather than papered over. `classifyValue` anchors on the whole
// cell, because T-H10 wrote it for `distinctValues` — where a cell *is* the
// value. An address inside a sentence therefore passes, and a free-text notes
// column is the one shape this redactor does not cover.
//
// Left as it is deliberately: the classifier is shared with the empty-result
// probe, so loosening the anchors changes what the probe discloses on every
// warehouse turn, which is a measurement rather than a same-sitting patch. This
// test exists so the next person finds the gap named instead of assuming
// coverage the code does not have.
func TestAnAddressInsideASentenceIsNotCaught(t *testing.T) {
	rows := []map[string]interface{}{{"keterangan": "hubungi andi@maju.co.id untuk konfirmasi"}}
	if redacted := RedactResultColumns([]string{"keterangan"}, rows, domain.PIIRedactionStrict); len(redacted) != 0 {
		t.Fatalf("the whole-cell limit has changed; update this test and re-measure the probe: %v", redacted)
	}
}

// An unset or unknown policy reads as strict, the same way the probe reads it.
func TestAnUnknownModeIsStrict(t *testing.T) {
	rows := rowsWithEmails()
	if redacted := RedactResultColumns([]string{"email"}, rows, domain.PIIRedactionMode("")); len(redacted) != 1 {
		t.Fatalf("an unset policy disclosed a contact column")
	}
}

// A redacted payload has to say so. A model handed a column of markers with no
// explanation has been known to report the data as missing, which is a false
// statement about the tenant's own document.
func TestThePayloadNamesWhatItWithheld(t *testing.T) {
	rows := rowsWithEmails()
	redacted := RedactResultColumns([]string{"pelanggan", "email", "nilai"}, rows, domain.PIIRedactionStrict)
	out := marshalSQLResult("src-1", "postgres", &db.QueryResult{
		Columns: []string{"pelanggan", "email", "nilai"}, Rows: rows, Count: len(rows),
	}, 0, nil, redacted)

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	cols, ok := payload["redacted_columns"].([]interface{})
	if !ok || len(cols) != 1 || cols[0] != "email" {
		t.Fatalf("the payload does not name the withheld column: %v", payload["redacted_columns"])
	}
	note, _ := payload["redaction_note"].(string)
	if note == "" {
		t.Fatal("the payload carries no note, so the model cannot tell withheld from empty")
	}
	if string(out) == "" || containsAny(string(out), "andi@maju.co.id", "budi@sentosa.co.id", "citra@berkah.co.id") {
		t.Error("an address reached the serialised payload")
	}
}

// A result with no redaction is byte-identical to what it was before this
// change: nothing is added to an ordinary payload.
func TestAnOrdinaryPayloadIsUnchanged(t *testing.T) {
	res := &db.QueryResult{
		Columns: []string{"bulan", "nilai"},
		Rows:    []map[string]interface{}{{"bulan": "Oktober", "nilai": 3377718500}},
		Count:   1,
	}
	out := marshalSQLResult("src-1", "postgres", res, 0, nil, nil)
	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := payload["redacted_columns"]; present {
		t.Error("an ordinary result grew a redaction key")
	}
	if _, present := payload["redaction_note"]; present {
		t.Error("an ordinary result grew a redaction note")
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && len(s) >= len(sub) && stringContains(s, sub) {
			return true
		}
	}
	return false
}

func stringContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
