package doctable

import (
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/docparse"
)

// The three months of the 2026-08-18 gate, with their real figures, because
// this check exists because of them: the reply that misquoted
// 3.863.405.700 as 3.860.405.700 was 0.078% off and passed every instrument
// this product had (T-Q14). Here the same corruption has to be caught, and the
// corruption is not caught by being large — it is caught by the document
// stating its own total.
func salesGrid(december string, total string) [][]string {
	return [][]string{
		{"Bulan", "Nilai"},
		{"Oktober", "3.377.718.500"},
		{"November", "3.708.552.300"},
		{"Desember", december},
		{"TOTAL", total},
	}
}

func TestATableWhoseTotalMatchesIsVerified(t *testing.T) {
	table := firstTable(t, page(1, salesGrid("3.863.405.700", "10.949.676.500")))
	if table.Verify.Status != VerifyVerified {
		t.Fatalf("status = %s (%s), want verified", table.Verify.Status, table.Verify.Detail)
	}
	if table.Verify.Checked == 0 {
		t.Error("a verified table checked nothing")
	}
	if !table.Verify.Publishable() {
		t.Error("a verified table cannot be published")
	}
}

// Acceptance: one digit changed in one cell quarantines, and the message names
// both figures and the difference.
func TestOneChangedDigitQuarantinesAndSaysWhat(t *testing.T) {
	table := firstTable(t, page(1, salesGrid("3.860.405.700", "10.949.676.500")))

	if table.Verify.Status != VerifyQuarantined {
		t.Fatalf("status = %s, want quarantined — a 3,000,000 error passed", table.Verify.Status)
	}
	if table.Verify.Publishable() {
		t.Fatal("a quarantined table reports itself publishable")
	}
	for _, want := range []string{"10.949.676.500", "10.946.676.500", "3.000.000"} {
		if !strings.Contains(table.Verify.Detail, want) {
			t.Errorf("detail = %q, want it to name %s", table.Verify.Detail, want)
		}
	}
}

// Acceptance: a table with no total is unverified and remains publishable. Most
// tables are this, and a check that treated "nothing to check" as a failure
// would quarantine the whole corpus.
func TestATableWithNoTotalIsUnverifiedAndPublishable(t *testing.T) {
	table := firstTable(t, page(1, [][]string{
		{"Produk", "Harga"},
		{"Kopi", "15.000"},
		{"Teh", "12.000"},
	}))
	if table.Verify.Status != VerifyUnverified {
		t.Fatalf("status = %s, want unverified", table.Verify.Status)
	}
	if !table.Verify.Publishable() {
		t.Error("an unverified table cannot be published; most tables are unverified")
	}
}

// Acceptance: a parts-rounded total does not quarantine. A document that prints
// its parts to the nearest thousand and its total exactly will mismatch
// legitimately, and a check that fired on it would be narrowed within a week —
// which this repo has done to a guardrail before.
func TestAPartsRoundedTotalDoesNotQuarantine(t *testing.T) {
	// Parts printed to the nearest thousand; the total is the unrounded sum,
	// 1,499 away from the sum of what is printed.
	table := firstTable(t, page(1, [][]string{
		{"Bulan", "Nilai"},
		{"Oktober", "3.377.000"},
		{"November", "3.708.000"},
		{"Desember", "3.863.000"},
		{"TOTAL", "10.949.499"},
	}))
	if table.Verify.Status == VerifyQuarantined {
		t.Fatalf("rounded parts quarantined the table: %s", table.Verify.Detail)
	}

	// And the tightening still holds where the parts are exact: the same
	// difference against parts printed to the unit is a real mismatch.
	exact := firstTable(t, page(1, [][]string{
		{"Bulan", "Nilai"},
		{"Oktober", "3.377.123"},
		{"November", "3.708.456"},
		{"Desember", "3.863.789"},
		{"TOTAL", "10.950.868"},
	}))
	if exact.Verify.Status != VerifyQuarantined {
		t.Fatalf("status = %s, want quarantined — the parts are exact and the total is 500 off",
			exact.Verify.Status)
	}
}

// A percentage column that claims to be a breakdown has to add to 100.
func TestAPercentageBreakdownMustSumToOneHundred(t *testing.T) {
	ok := firstTable(t, page(1, [][]string{
		{"Kanal", "Porsi"},
		{"Toko", "50,0%"},
		{"Online", "30,0%"},
		{"Grosir", "20,0%"},
	}))
	if ok.Verify.Status == VerifyQuarantined {
		t.Fatalf("a breakdown summing to 100 quarantined: %s", ok.Verify.Detail)
	}

	broken := firstTable(t, page(1, [][]string{
		{"Kanal", "Porsi"},
		{"Toko", "50,0%"},
		{"Online", "30,0%"},
		{"Grosir", "22,5%"},
	}))
	if broken.Verify.Status != VerifyQuarantined {
		t.Fatalf("status = %s, want quarantined — the shares sum to 102.5", broken.Verify.Status)
	}

	// A column of growth rates is not a breakdown and is not held to 100.
	rates := firstTable(t, page(1, [][]string{
		{"Bulan", "Pertumbuhan"},
		{"Oktober", "5,0%"},
		{"November", "7,5%"},
		{"Desember", "9,0%"},
	}))
	if rates.Verify.Status == VerifyQuarantined {
		t.Fatalf("a column of rates was read as a breakdown: %s", rates.Verify.Detail)
	}
}

// An unlabelled total row — the label was a merged cell the parser could not
// carry — is held out of the data, and it is *not* counted as verification.
func TestAnUnlabelledTotalIsHeldOutButProvesNothing(t *testing.T) {
	table := firstTable(t, page(1, [][]string{
		{"Bulan", "Nilai"},
		{"Oktober", "100"},
		{"November", "200"},
		{"Desember", "300"},
		{"", "600"},
	}))

	if len(table.Rows) != 3 {
		t.Fatalf("got %d data rows, want 3 — the unlabelled total is data and every sum doubles",
			len(table.Rows))
	}
	if len(table.Totals) != 1 || table.Totals[0].Total != "arithmetic" {
		t.Fatalf("totals = %+v, want one row recognised arithmetically", table.Totals)
	}
	if table.Verify.Status == VerifyVerified {
		t.Error("a row recognised because it added up was then used to prove the table adds up")
	}
}

// The dates fixture's shape, kept as its own case: three rows where the last is
// the sum of the two before it is a growing series, not a total.
func TestAGrowingSeriesIsNotATotalRow(t *testing.T) {
	table := firstTable(t, page(1, [][]string{
		{"Bulan", "Nilai"},
		{"Oktober", "100"},
		{"November", "200"},
		{"Desember", "300"},
	}))
	if len(table.Rows) != 3 {
		t.Fatalf("got %d data rows, want 3 — December was demoted because 100+200=300", len(table.Rows))
	}
}

// A quarantined table cannot be published through any path. The publish service
// asks this method, and this test is the reason it is a method rather than a
// string comparison at the call site.
func TestQuarantineIsAStateNotAWarning(t *testing.T) {
	for status, want := range map[VerifyStatus]bool{
		VerifyVerified:    true,
		VerifyUnverified:  true,
		VerifyQuarantined: false,
	} {
		if got := (Verification{Status: status}).Publishable(); got != want {
			t.Errorf("%s: publishable = %v, want %v", status, got, want)
		}
	}
}

// The check runs over the joined table, not over each page's part of it. A
// three-page table whose total is on page three is the common shape, and
// verifying page by page would compare that total against a third of the rows.
func TestTheCheckRunsAfterTheJoin(t *testing.T) {
	header := []string{"Bulan", "Nilai"}
	tables := Build([]docparse.Page{
		page(1, [][]string{header, {"Januari", "100"}, {"Februari", "200"}}),
		page(2, [][]string{header, {"Maret", "300"}, {"April", "400"}}),
		page(3, [][]string{header, {"Mei", "500"}, {"TOTAL", "1.500"}}),
	}, Options{})

	if len(tables) != 1 {
		t.Fatalf("got %d tables, want 1", len(tables))
	}
	if tables[0].Verify.Status != VerifyVerified {
		t.Fatalf("status = %s (%s), want verified against all five rows",
			tables[0].Verify.Status, tables[0].Verify.Detail)
	}
}
