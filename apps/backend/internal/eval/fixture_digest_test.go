package eval

import (
	"database/sql"
	"os"
	"testing"
)

// The digest of the demo warehouse's fact table, as
// `migrations/demo_tenant/003_seed_data_facts.sql` produces it.
//
// **This constant is the guard on `testdata/eval/golden.yaml`'s 21
// warehouse-derived numeric expectations.** Those are values somebody read out
// of a database once and typed into a file, which is only safe while the
// database is reproducible. It was not: the fixture drew from `random()`, every
// fresh volume produced a different warehouse, and by 2026-08-23 the golden set
// expected 21,231,619,600 where the warehouse held 8,684,393,970 — so the agent
// answered correctly and was scored wrong for it, on a set whose whole job is
// to say when the agent is wrong (docs/coverage/live-gate-backlog.md §2b).
//
// If this test fails, the fixture changed. That is allowed — but the golden
// set's numbers describe the old warehouse and every one of them has to be
// re-derived before any eval number means anything. Do that first, then update
// this constant in the same commit.
const factSalesDigest = "b5e068c0956c0eef12ae1902056bc5f6"

// Headline aggregates, kept beside the digest so a failure says *what* moved
// rather than only that something did.
var wantAggregates = map[string]float64{
	"select count(*) from fact_sales":                    1348,
	"select count(distinct product_id) from fact_sales":  30,
	"select count(distinct customer_id) from fact_sales": 50,
	"select sum(sales_amount) from fact_sales":           8684393970,
	"select sum(profit_amount) from fact_sales":          2300098470,
	"select sum(quantity) from fact_sales":               2510,
	"select sum(discount_amount) from fact_sales":        348733830,
}

// TestDemoFixtureIsReproducible needs the demo warehouse. It skips without one,
// because most of this suite runs with no containers at all — but the skip is
// the reason the drift went unnoticed for months, so `make check` on a machine
// with the stack up is where this earns its keep.
func TestDemoFixtureIsReproducible(t *testing.T) {
	dsn := os.Getenv("DEMO_DSN")
	if dsn == "" {
		dsn = "postgres://demo:demo@localhost:5433/demo_analytics?sslmode=disable"
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Skipf("no demo warehouse: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Skipf("no demo warehouse at %s: %v", dsn, err)
	}

	for q, want := range wantAggregates {
		var got sql.NullFloat64
		if err := db.QueryRow(q).Scan(&got); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if got.Float64 != want {
			t.Errorf("%s\n  got  %.2f\n  want %.2f\n"+
				"the demo fixture changed; re-derive golden.yaml's numeric expectations before trusting any eval number", q, got.Float64, want)
		}
	}

	var digest sql.NullString
	const digestQuery = `select md5(string_agg(
		transaction_id||'|'||quantity||'|'||sales_amount||'|'||payment_method||'|'||sales_channel,
		',' order by transaction_id)) from fact_sales`
	if err := db.QueryRow(digestQuery).Scan(&digest); err != nil {
		t.Fatalf("digest: %v", err)
	}
	if digest.String != factSalesDigest {
		t.Errorf("fact_sales digest\n  got  %s\n  want %s\n"+
			"every row-level draw in 003_seed_data_facts.sql must be a pure function of the row's own key; "+
			"if this moved, a fresh volume no longer reproduces the warehouse golden.yaml was written against",
			digest.String, factSalesDigest)
	}
}
