package tools

import (
	"strings"
	"testing"
)

// The property that matters: no value a user filtered on survives into the
// line that gets logged at Info, and everything an operator reads a query log
// for does.
func TestNormalizeSQLForLogRemovesValuesAndKeepsShape(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want string
	}{
		{
			// The T-H7 case verbatim: an identity number in a WHERE clause.
			"string literal",
			"SELECT name FROM customers WHERE nik = '3201234567890123'",
			"SELECT name FROM customers WHERE nik = '?'",
		},
		{
			"doubled-quote escape stays inside the literal",
			"SELECT * FROM c WHERE name = 'O''Brien' AND city = 'Bandung'",
			"SELECT * FROM c WHERE name = '?' AND city = '?'",
		},
		{
			// A phone number, an account id and a NIK are all numeric, so a
			// normaliser that only handled quoted strings would leak the ones
			// that matter most.
			"bare numbers",
			"SELECT * FROM t WHERE phone = 628123456789 AND amount > 1500000.50",
			"SELECT * FROM t WHERE phone = ? AND amount > ?",
		},
		{
			"digits inside identifiers survive",
			"SELECT t2.col1, x_1 FROM fact_sales_2024 t2 LIMIT 100",
			"SELECT t2.col1, x_1 FROM fact_sales_2024 t2 LIMIT ?",
		},
		{
			"scientific notation",
			"SELECT * FROM t WHERE ratio < 1.5e-3",
			"SELECT * FROM t WHERE ratio < ?",
		},
		{
			"quoted identifiers are names, not data",
			`SELECT "order id", ` + "`total`" + ` FROM "orders"`,
			`SELECT "order id", ` + "`total`" + ` FROM "orders"`,
		},
		{
			// A model that echoes the question into a comment would otherwise
			// write it to the log in full.
			"line comment is dropped whole",
			"-- how much did 3201234567890123 spend\nSELECT 1",
			"\nSELECT ?",
		},
		{
			"block comment is dropped whole",
			"SELECT /* nik 3201234567890123 */ 1 FROM t",
			"SELECT   ? FROM t",
		},
		{
			"unterminated block comment takes the rest with it",
			"SELECT 1 FROM t /* nik 3201234567890123",
			"SELECT ? FROM t  ",
		},
		{
			// Multi-line is what a model actually writes; the single-line
			// assumption is what made the T-Q9 probe unreachable for months.
			"multi-line query",
			"SELECT city, sum(sales_amount)\nFROM fact_sales\nWHERE month_name = 'December '\n  AND year = 2024\nGROUP BY city",
			"SELECT city, sum(sales_amount)\nFROM fact_sales\nWHERE month_name = '?'\n  AND year = ?\nGROUP BY city",
		},
		{
			"unterminated literal does not run past the end",
			"SELECT * FROM t WHERE name = 'Budi",
			"SELECT * FROM t WHERE name = '?'",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeSQLForLog(tc.sql); got != tc.want {
				t.Errorf("normalizeSQLForLog(%q)\n got %q\nwant %q", tc.sql, got, tc.want)
			}
		})
	}
}

// A leak is a substring, so assert the absence of the value itself rather than
// the presence of a shape. This is the test that fails if someone adds a branch
// that copies a literal through.
func TestNormalizeSQLForLogLeaksNoLiteral(t *testing.T) {
	const nik = "3201234567890123"
	const email = "budi@example.co.id"

	queries := []string{
		"SELECT * FROM c WHERE nik = '" + nik + "'",
		"SELECT * FROM c WHERE email IN ('" + email + "', 'x@y.z')",
		"SELECT * FROM c WHERE nik = " + nik,
		"/* " + nik + " */ SELECT 1",
		"SELECT 1 -- " + email,
	}
	for _, q := range queries {
		got := normalizeSQLForLog(q)
		if strings.Contains(got, nik) || strings.Contains(got, email) {
			t.Errorf("normalizeSQLForLog(%q) = %q — a literal survived", q, got)
		}
	}
}
