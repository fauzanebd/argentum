package sqlguard

import (
	"strings"
	"testing"
)

// window is the token set the metric registry declares: both bounds, both
// required. It is the case these tests moved here with (T-06), so it stays the
// default fixture.
var window = []Token{"from", "to"}

func TestValidateStatementAcceptsASingleSelect(t *testing.T) {
	ok := []string{
		`SELECT sum(x) AS v FROM t WHERE d >= {{from}} AND d < {{to}}`,
		`  select count(*) as v from t where ts between {{from}} and {{to}} ;`,
		`WITH w AS (SELECT * FROM t WHERE d >= {{from}} AND d < {{to}}) SELECT count(*) AS v FROM w`,
		// A status literally named 'deleted' must not trip the keyword scan.
		`SELECT count(*) AS v FROM orders WHERE status = 'deleted' AND d >= {{from}} AND d < {{to}}`,
		// REPLACE() is a read-only function, not the REPLACE statement.
		`SELECT sum(REPLACE(amount, ',', '')::numeric) AS v FROM t WHERE d >= {{from}} AND d < {{to}}`,
	}
	for _, s := range ok {
		if err := ValidateStatement(s, window, window...); err != nil {
			t.Errorf("ValidateStatement rejected a valid statement: %q\n  %v", s, err)
		}
	}
}

func TestValidateStatementRejectsNonSelectAndMultiStatement(t *testing.T) {
	bad := map[string]string{
		"delete":         `DELETE FROM t WHERE d >= {{from}} AND d < {{to}}`,
		"update":         `UPDATE t SET x = 1 WHERE d >= {{from}} AND d < {{to}}`,
		"drop appended":  `SELECT 1 AS v WHERE d >= {{from}} AND d < {{to}}; DROP TABLE t`,
		"two selects":    `SELECT 1 AS v WHERE d >= {{from}}; SELECT 1 WHERE d < {{to}}`,
		"cte then write": `WITH w AS (SELECT 1) INSERT INTO t SELECT * FROM w WHERE d >= {{from}} AND d < {{to}}`,
		"no window":      `SELECT count(*) AS v FROM t`,
		"select into":    `SELECT * INTO t2 FROM t WHERE d >= {{from}} AND d < {{to}}`,
	}
	for name, s := range bad {
		if err := ValidateStatement(s, window, window...); err == nil {
			t.Errorf("%s: ValidateStatement accepted an invalid statement: %q", name, s)
		}
	}
}

// A semicolon or a keyword hidden in a comment must not fool the single-
// statement or keyword checks.
func TestValidateStatementScrubsComments(t *testing.T) {
	if err := ValidateStatement("SELECT 1 AS v -- ; DROP TABLE t\nFROM t WHERE d >= {{from}} AND d < {{to}}", window, window...); err != nil {
		t.Errorf("a comment holding ; and DROP should be ignored: %v", err)
	}
	if err := ValidateStatement("SELECT 1 AS v /* delete */ FROM t WHERE d >= {{from}} AND d < {{to}}", window, window...); err != nil {
		t.Errorf("a block comment holding a keyword should be ignored: %v", err)
	}
}

// The gap the metric registry's live gate found: presence was checked and
// absence was not, so an unknown token passed save and blew up at render — a 500
// where the admin should have got a 400 naming the token.
func TestValidateStatementRefusesAnUndeclaredToken(t *testing.T) {
	err := ValidateStatement(`SELECT 1 AS v WHERE x = {{evil}} AND d >= {{from}} AND d < {{to}}`, window, window...)
	if err == nil {
		t.Fatal("an undeclared {{token}} must be refused at validate, not at render")
	}
	if !strings.Contains(err.Error(), "evil") {
		t.Errorf("the error must name the offending token, got %q", err)
	}
	if !strings.Contains(err.Error(), "{{from}}") {
		t.Errorf("the error must list the tokens that would have worked, got %q", err)
	}
}

// Declared and required are separate rules, which is the whole reason this
// function took a parameter instead of keeping the metric's hardcoded pair: a
// dashboard filter is declared once and bound by some panels and not others.
func TestValidateStatementSeparatesDeclaredFromRequired(t *testing.T) {
	declared := []Token{"from", "to", "channel"}
	// Declared, unused, nothing required: fine. A panel need not use every filter.
	if err := ValidateStatement(`SELECT count(*) AS v FROM t`, declared); err != nil {
		t.Errorf("a panel that binds no filter must be allowed: %v", err)
	}
	// Uses a declared token, and the required one is missing.
	if err := ValidateStatement(`SELECT count(*) AS v FROM t WHERE ch = {{channel}}`, declared, "from"); err == nil {
		t.Error("a missing required token must be refused")
	}
	// Uses only what it declared, and every required token is present.
	if err := ValidateStatement(`SELECT count(*) AS v FROM t WHERE ch = {{channel}} AND d >= {{from}}`, declared, "from"); err != nil {
		t.Errorf("a statement referencing declared tokens must pass: %v", err)
	}
}

// run_sql's case: model-written SQL declares nothing, so a {{token}} in it has
// nothing to bind and would otherwise reach the driver with the braces in it.
func TestValidateStatementWithNoDeclaredTokens(t *testing.T) {
	if err := ValidateStatement(`SELECT count(*) AS v FROM orders WHERE status = 'paid'`, nil); err != nil {
		t.Errorf("plain SQL with no tokens must pass: %v", err)
	}
	err := ValidateStatement(`SELECT count(*) AS v FROM orders WHERE d >= {{from}}`, nil)
	if err == nil {
		t.Fatal("a token must be refused where none is declared")
	}
	if !strings.Contains(err.Error(), "no parameters") {
		t.Errorf("the error should say the statement declares none, got %q", err)
	}
}

// A token inside a string literal or a comment still renders, so it still
// validates — the scrub is for structure, never for tokens.
func TestFindTokensReadsTheRawStatement(t *testing.T) {
	refs := FindTokens(`SELECT '{{from}}' AS label -- {{to}}` + "\n" + `FROM t WHERE d >= {{ from }}`)
	if len(refs) != 3 {
		t.Fatalf("FindTokens found %d tokens, want 3 (literal, comment, spaced)", len(refs))
	}
	if refs[0].Name != "from" || refs[1].Name != "to" || refs[2].Name != "from" {
		t.Errorf("names = %v %v %v", refs[0].Name, refs[1].Name, refs[2].Name)
	}
	// The ranges must span the braces, because a renderer rewrites by them.
	src := `SELECT {{from}}`
	r := FindTokens(src)[0]
	if src[r.Start:r.End] != "{{from}}" {
		t.Errorf("range spans %q, want the whole token", src[r.Start:r.End])
	}
}
