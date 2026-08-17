package dashboard

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func pg(n int) string { return "$" + strconv.Itoa(n) }
func qm(int) string   { return "?" }

var jan = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
var feb = time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)

// The property the whole ticket turns on: a filter value is bound, never
// interpolated. After T-D13 this value comes off a query string a stranger can
// edit.
func TestRenderBindsValuesAsParameters(t *testing.T) {
	sql, args, err := Render(
		`SELECT sum(total) AS v FROM orders WHERE d >= {{p_from}} AND d < {{p_to}} AND channel = {{channel}}`,
		pg, map[string]any{"p_from": jan, "p_to": feb, "channel": "web'; DROP TABLE orders --"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(sql, "{{") || strings.Contains(sql, "DROP") {
		t.Errorf("sql carries a value inline: %q", sql)
	}
	if !strings.HasSuffix(sql, "$1 AND d < $2 AND channel = $3") {
		t.Errorf("sql = %q", sql)
	}
	if len(args) != 3 || args[0] != jan || args[2] != "web'; DROP TABLE orders --" {
		t.Errorf("args = %v", args)
	}
}

// One placeholder per occurrence, so MySQL's positional `?` needs no reuse.
func TestRenderRepeatsArgsPerOccurrence(t *testing.T) {
	sql, args, err := Render(`SELECT 1 FROM t WHERE a >= {{p_from}} OR b >= {{p_from}} OR c < {{p_to}}`,
		qm, map[string]any{"p_from": jan, "p_to": feb})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Count(sql, "?") != 3 {
		t.Errorf("sql = %q, want three placeholders", sql)
	}
	if len(args) != 3 || args[0] != jan || args[1] != jan || args[2] != feb {
		t.Errorf("args = %v", args)
	}
}

// `WHERE tenant = ` is valid SQL that reads the whole table, so an unbound token
// is an error rather than an empty string.
func TestRenderRefusesAnUnboundToken(t *testing.T) {
	_, _, err := Render(`SELECT 1 FROM t WHERE region = {{region}}`, pg, map[string]any{"channel": "web"})
	if err == nil {
		t.Fatal("a token with no value must be refused")
	}
	if !strings.Contains(err.Error(), "region") || !strings.Contains(err.Error(), "{{channel}}") {
		t.Errorf("the error must name the token and what is bound, got %q", err)
	}
}

func TestRenderLeavesTokenlessSQLAlone(t *testing.T) {
	sql, args, err := Render(`SELECT count(*) AS v FROM orders`, pg, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if sql != `SELECT count(*) AS v FROM orders` || len(args) != 0 {
		t.Errorf("sql = %q args = %v", sql, args)
	}
}
