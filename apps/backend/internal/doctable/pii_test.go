package doctable

import "testing"

// Acceptance (T-P12): a column of emails or identity numbers is classified at
// publish, and shown in review. This is the classification half; the showing is
// the review surface's, and it reads exactly this field.
func TestAColumnOfContactDetailsIsLabelled(t *testing.T) {
	table := firstTable(t, page(1, [][]string{
		{"Nama", "Email", "Kota"},
		{"Andi", "andi@example.co.id", "Jakarta"},
		{"Budi", "budi@example.co.id", "Bandung"},
		{"Citra", "citra@example.co.id", "Surabaya"},
	}))
	if got := table.Columns[1].PII; got != PIIContact {
		t.Errorf("email column classified %q, want %q", got, PIIContact)
	}
	if got := table.Columns[0].PII; got != "" {
		t.Errorf("a column of names was classified %q; a name is not what these classes are for", got)
	}
	if got := table.Columns[2].PII; got != "" {
		t.Errorf("a column of cities was classified %q", got)
	}
}

func TestAColumnOfNationalIdentityNumbersIsLabelledIdentity(t *testing.T) {
	table := firstTable(t, page(1, [][]string{
		{"Nama", "NIK"},
		{"Andi", "3171012345670001"},
		{"Budi", "3171012345670002"},
		{"Citra", "3171012345670003"},
	}))
	if got := table.Columns[1].PII; got != PIIIdentity {
		t.Errorf("NIK column classified %q, want %q", got, PIIIdentity)
	}
}

// The header is the document telling us what a column is, and it wins even when
// the values are unreadable — a truncated or masked id is still an id column.
func TestTheHeaderClassifiesWhenTheValuesDoNot(t *testing.T) {
	table := firstTable(t, page(1, [][]string{
		{"Nama", "No. KTP"},
		{"Andi", "3171****0001"},
		{"Budi", "3171****0002"},
		{"Citra", "3171****0003"},
	}))
	if got := table.Columns[1].PII; got != PIIIdentity {
		t.Errorf("masked KTP column classified %q, want %q", got, PIIIdentity)
	}
}

// The false positive that would make the label worthless: a column of invoice
// numbers, order ids or product codes is not personal data, and a classifier
// that says it is teaches a reviewer to click past the warning.
func TestOrdinaryBusinessNumbersAreNotPersonalData(t *testing.T) {
	table := firstTable(t, page(1, [][]string{
		{"Faktur", "Jumlah"},
		{"INV-2024-0001", "1.500.000"},
		{"INV-2024-0002", "2.500.000"},
		{"INV-2024-0003", "3.500.000"},
	}))
	for i, col := range table.Columns {
		if col.PII != "" {
			t.Errorf("column %d (%q) classified %q", i, col.Header, col.PII)
		}
	}

	// A warehouse column, which the substring test this deliberately avoids
	// would have labelled as a WhatsApp number.
	warehouse := firstTable(t, page(1, [][]string{
		{"Warehouse", "Stok"},
		{"Cakung", "120"},
		{"Cikarang", "340"},
		{"Bekasi", "560"},
	}))
	if warehouse.Columns[0].PII != "" {
		t.Errorf("\"Warehouse\" was classified %q — \"wa\" matched as a substring",
			warehouse.Columns[0].PII)
	}
}

// One address in a notes column is a note. Labelling the column would redact
// every note in it under a strict tenant.
func TestOneAddressInANotesColumnDoesNotLabelIt(t *testing.T) {
	table := firstTable(t, page(1, [][]string{
		{"Produk", "Catatan"},
		{"Kopi", "kirim ulang"},
		{"Teh", "hubungi andi@example.co.id"},
		{"Gula", "stok habis"},
	}))
	if got := table.Columns[1].PII; got != "" {
		t.Errorf("a notes column with one address was classified %q", got)
	}
}

// The strongest class wins where the two signals disagree: over-labelling costs
// a reviewer one sentence, and under-labelling puts an identity number on a
// dashboard.
func TestTheStrongerClassWins(t *testing.T) {
	table := firstTable(t, page(1, [][]string{
		{"Nama", "Kontak"},
		{"Andi", "3171012345670001"},
		{"Budi", "3171012345670002"},
		{"Citra", "3171012345670003"},
	}))
	if got := table.Columns[1].PII; got != PIIIdentity {
		t.Errorf("a column headed \"Kontak\" holding identity numbers was classified %q, want %q",
			got, PIIIdentity)
	}
}

func TestLuhnSeparatesACardFromALongNumber(t *testing.T) {
	if !luhn("4539578763621486") {
		t.Error("a valid card number failed the Luhn check")
	}
	if luhn("4539578763621487") {
		t.Error("an invalid number passed the Luhn check")
	}
}
