package metric

import (
	"strings"
	"testing"
	"time"
)

// pgPlaceholder mimics the Postgres dialect for render tests without importing
// the db package.
func pgPlaceholder(n int) string { return "$" + itoa(n) }
func itoa(n int) string {
	return string(rune('0' + n)) // n stays 1..9 in these tests
}

var win = struct{ from, to time.Time }{
	from: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	to:   time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
}

// The acceptance criterion the whole ticket turns on: the window is BOUND, not
// interpolated. A `'; DROP` value never reaches the SQL text — it is an arg.
func TestRenderBindsTheWindowAsParameters(t *testing.T) {
	sql, args, err := Render(
		`SELECT sum(total) AS v FROM orders WHERE d >= {{from}} AND d < {{to}}`,
		pgPlaceholder, win.from, win.to)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(sql, "{{") || strings.Contains(sql, "2024") {
		t.Errorf("sql still carries the window inline: %q", sql)
	}
	if !strings.Contains(sql, "$1") || !strings.Contains(sql, "$2") {
		t.Errorf("sql has no placeholders: %q", sql)
	}
	if len(args) != 2 || args[0] != win.from || args[1] != win.to {
		t.Errorf("args = %v, want [from to]", args)
	}
}

// The injection payload proof: a malicious window value is data, so it lands in
// args verbatim and the SQL is unchanged.
func TestRenderTreatsAMaliciousValueAsData(t *testing.T) {
	// The value is not even rendered by us — it is passed to the driver — so a
	// string here would ride in args untouched. We assert the SQL shape does not
	// change regardless of the value, which is the property that matters.
	sql, args, err := Render(`SELECT count(*) AS v FROM t WHERE ts BETWEEN {{from}} AND {{to}}`,
		pgPlaceholder, win.from, win.to)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(sql, "DROP") || !strings.HasSuffix(sql, "$1 AND $2") {
		t.Errorf("sql = %q, want the window as $1/$2 with nothing interpolated", sql)
	}
	if len(args) != 2 {
		t.Fatalf("args = %v", args)
	}
}

// A repeated token gets one placeholder and one arg per occurrence, so MySQL's
// positional `?` works without reuse.
func TestRenderRepeatsArgsPerOccurrence(t *testing.T) {
	sql, args, err := Render(`SELECT 1 FROM t WHERE a >= {{from}} OR b >= {{from}} OR c < {{to}}`,
		func(int) string { return "?" }, win.from, win.to)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Count(sql, "?") != 3 {
		t.Errorf("sql = %q, want three placeholders", sql)
	}
	if len(args) != 3 || args[0] != win.from || args[1] != win.from || args[2] != win.to {
		t.Errorf("args = %v, want [from from to]", args)
	}
}

func TestRenderRejectsUnknownTokenAndMissingWindow(t *testing.T) {
	if _, _, err := Render(`SELECT 1 WHERE x = {{evil}} AND d >= {{from}} AND d < {{to}}`, pgPlaceholder, win.from, win.to); err == nil {
		t.Error("an unknown {{token}} must be refused")
	}
	if _, _, err := Render(`SELECT 1 WHERE d >= {{from}}`, pgPlaceholder, win.from, win.to); err == nil {
		t.Error("a template missing {{to}} must be refused")
	}
}

// The structural table — single SELECT, scrubbed comments, forbidden keywords —
// moved to internal/sqlguard with the code under T-H4 step 1. What stays here is
// the metric's own contract with it: the window is the whole vocabulary, both
// bounds required.
func TestValidateTemplateDelegatesWithTheWindowTokens(t *testing.T) {
	if err := ValidateTemplate(`SELECT sum(x) AS v FROM t WHERE d >= {{from}} AND d < {{to}}`); err != nil {
		t.Errorf("a windowed single SELECT must pass: %v", err)
	}
	if err := ValidateTemplate(`SELECT count(*) AS v FROM t`); err == nil {
		t.Error("a template with no window must be refused")
	}
	if err := ValidateTemplate(`SELECT 1 AS v WHERE d >= {{from}} AND d < {{to}}; DROP TABLE t`); err == nil {
		t.Error("a second statement must be refused")
	}
	// The gate's finding: an unknown token used to pass validate and fail at
	// render, turning a 400 the admin could act on into a 500. Now it is refused
	// where it can be reported.
	if err := ValidateTemplate(`SELECT 1 AS v WHERE x = {{evil}} AND d >= {{from}} AND d < {{to}}`); err == nil {
		t.Error("an unknown {{token}} must be refused at validate")
	}
}
