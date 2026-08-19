package tools

import (
	"strings"
	"testing"
)

// T-H4 step 3. The three callers sqlguard's package comment names are the
// metric registry, the dashboard spec and run_sql — and until this test was
// written the third one was not true: `run_sql.Execute` handed `params.SQL`
// to the driver untouched, and the only thing between a model-authored
// INSERT and the tenant's data was the read-only transaction. On SQL Server
// there is not even that (`adapters/db/sqlserver/conn.go:36` begins a plain
// transaction, because the driver rejects TxOptions.ReadOnly), so the barrier
// there is the customer's grants alone.
//
// These cases are the ones a read-only tx does NOT catch on every driver, plus
// the shapes that have to keep working — a validator that refuses ordinary
// analytical SQL costs more than it saves.
func TestGuardStatementRefusesWhatIsNotASingleRead(t *testing.T) {
	refused := []struct {
		name, sql, want string
	}{
		{"insert", "INSERT INTO orders (id) VALUES (1)", "INSERT"},
		{"update behind a select", "SELECT 1; UPDATE orders SET total = 0", "single statement"},
		{"select into", "SELECT * INTO staging FROM orders", "INTO"},
		{"copy", "COPY orders TO '/tmp/x.csv'", "SELECT"},
		{"exec", "EXEC sp_who", "SELECT"},
		{"drop behind a comment", "SELECT 1 -- \nDROP TABLE orders", "DROP"},
		{"unbound token", "SELECT * FROM orders WHERE d > {{from}}", "{{from}}"},
		{"empty", "   ", "empty"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			err := guardStatement(tc.sql)
			if err == nil {
				t.Fatalf("guardStatement(%q) = nil, want a refusal", tc.sql)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal = %q, want it to name %q", err.Error(), tc.want)
			}
			// The refusal has to be actionable, the same way explainSQLError is:
			// a model told only "no" spends another call finding out what "yes"
			// looks like.
			if !strings.Contains(err.Error(), "single SELECT") {
				t.Errorf("refusal = %q, want it to name what would have worked", err.Error())
			}
		})
	}
}

// The other half, and the more important one. Every string here is analytical
// SQL a model legitimately writes, and several carry a forbidden keyword as a
// substring of an identifier or inside a literal — which is exactly where a
// pattern-matching guard turns into an outage nobody can debug.
func TestGuardStatementAllowsOrdinaryAnalyticalSQL(t *testing.T) {
	allowed := []struct{ name, sql string }{
		{"plain select", "SELECT count(*) FROM orders"},
		{"trailing semicolon", "SELECT count(*) FROM orders;"},
		{"cte", "WITH m AS (SELECT 1 AS n) SELECT n FROM m"},
		{"keyword inside an identifier", "SELECT create_date, update_count, call_id FROM merge_log"},
		{"keyword inside a literal", "SELECT count(*) FROM orders WHERE status = 'deleted'"},
		{"keyword inside a comment", "-- we do not delete rows here\nSELECT 1"},
		{"lowercase and leading whitespace", "\n  select 1\n"},
		{"indonesian columns", "SELECT pelanggan, sum(nilai) AS total FROM penjualan GROUP BY pelanggan"},
	}
	for _, tc := range allowed {
		t.Run(tc.name, func(t *testing.T) {
			if err := guardStatement(tc.sql); err != nil {
				t.Errorf("guardStatement(%q) = %v, want nil", tc.sql, err)
			}
		})
	}
}
