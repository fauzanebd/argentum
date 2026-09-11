package compute

import (
	"errors"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func num(t *testing.T, s string) Value {
	t.Helper()
	d, err := decimal.NewFromString(s)
	if err != nil {
		t.Fatalf("bad test fixture %q: %v", s, err)
	}
	return Num(d)
}

func series(t *testing.T, ss ...string) Value {
	t.Helper()
	out := make([]decimal.Decimal, 0, len(ss))
	for _, s := range ss {
		d, err := decimal.NewFromString(s)
		if err != nil {
			t.Fatalf("bad test fixture %q: %v", s, err)
		}
		out = append(out, d)
	}
	return Series(out)
}

func evalOK(t *testing.T, expr string, vars map[string]Value) string {
	t.Helper()
	got, err := Eval(expr, vars)
	if err != nil {
		t.Fatalf("Eval(%q) = error %v, wanted a figure", expr, err)
	}
	return got.String()
}

// The whole reason this package exists in decimal rather than float64. The
// assertion is on the *string* deliberately: 0.30000000000000004 compares equal
// to 0.3 under any tolerance this product owns, which is exactly how a wrong
// figure gets past every instrument (T-Q14).
func TestTenthsAddExactly(t *testing.T) {
	if got := evalOK(t, "0.1 + 0.2", nil); got != "0.3" {
		t.Errorf("0.1 + 0.2 = %q, want exactly %q", got, "0.3")
	}
	if got := evalOK(t, "1.1 * 3", nil); got != "3.3" {
		t.Errorf("1.1 * 3 = %q, want exactly %q", got, "3.3")
	}
	if got := evalOK(t, "2.675 - 2.6", nil); got != "0.075" {
		t.Errorf("2.675 - 2.6 = %q, want exactly %q", got, "0.075")
	}
}

func TestArithmeticAndPrecedence(t *testing.T) {
	vars := map[string]Value{
		"revenue": num(t, "3863405700"),
		"cost":    num(t, "2100000000"),
	}
	for _, tc := range []struct {
		expr string
		want string
	}{
		{"1 + 2 * 3", "7"},
		{"(1 + 2) * 3", "9"},
		{"-4 + 10", "6"},
		{"10 / 4", "2.5"},
		{"revenue - cost", "1763405700"},
		{"(revenue - cost) / revenue", "0.456438136952585642248237093"},
		{"round((revenue - cost) / revenue * 100, 2)", "45.64"},
	} {
		if got := evalOK(t, tc.expr, vars); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

// The second half of grounding: a name the turn did not produce is refused
// rather than treated as zero, and the message says which name.
func TestUnboundNameIsRefusedAndNamed(t *testing.T) {
	_, err := Eval("revenue - cost", map[string]Value{"revenue": num(t, "10")})
	if !errors.Is(err, ErrUnbound) {
		t.Fatalf("Eval with a missing binding = %v, want ErrUnbound", err)
	}
	if !strings.Contains(err.Error(), `"cost"`) {
		t.Errorf("the error does not name the unbound value: %v", err)
	}
}

func TestDivisionByZeroIsAnErrorNotAValue(t *testing.T) {
	_, err := Eval("revenue / orders", map[string]Value{
		"revenue": num(t, "100"),
		"orders":  num(t, "0"),
	})
	if !errors.Is(err, ErrDivideByZero) {
		t.Fatalf("dividing by zero = %v, want ErrDivideByZero", err)
	}
	// The advice has to point at the cause a model can actually act on.
	if !strings.Contains(err.Error(), "matched nothing") {
		t.Errorf("the divide-by-zero message offers no repair: %v", err)
	}
}

func TestOverflowIsRefused(t *testing.T) {
	_, err := Eval("big * big * big", map[string]Value{"big": num(t, strings.Repeat("9", 60))})
	if !errors.Is(err, ErrOverflow) {
		t.Fatalf("a 180-digit product = %v, want ErrOverflow", err)
	}
	// And the limit is not so tight that a real figure trips it: a rupiah
	// trillion squared is 26 digits.
	if got := evalOK(t, "1000000000000 * 1000000000000", nil); got != "1000000000000000000000000" {
		t.Errorf("a trillion squared = %q", got)
	}
}

func TestSumOverAColumn(t *testing.T) {
	vars := map[string]Value{
		"monthly": series(t, "10.05", "20.10", "30.15"),
		"target":  num(t, "50"),
	}
	if got := evalOK(t, "sum(monthly)", vars); got != "60.3" {
		t.Errorf("sum(monthly) = %q, want %q", got, "60.3")
	}
	if got := evalOK(t, "sum(monthly) - target", vars); got != "10.3" {
		t.Errorf("sum(monthly) - target = %q", got)
	}
	if got := evalOK(t, "min(monthly)", vars); got != "10.05" {
		t.Errorf("min(monthly) = %q", got)
	}
	if got := evalOK(t, "max(monthly, 100)", vars); got != "100" {
		t.Errorf("max(monthly, 100) = %q", got)
	}
}

// A column where one figure was needed is the mistake this grammar can
// actually make, so it is refused with the repair in the message rather than
// silently reduced to its first row.
func TestAColumnWhereAFigureWasNeeded(t *testing.T) {
	vars := map[string]Value{"monthly": series(t, "1", "2", "3")}
	for _, expr := range []string{"monthly * 2", "monthly + 1", "abs(monthly)", "monthly"} {
		_, err := Eval(expr, vars)
		if !errors.Is(err, ErrSyntax) {
			t.Fatalf("Eval(%q) = %v, want a refusal", expr, err)
		}
		if !strings.Contains(err.Error(), "sum()") {
			t.Errorf("Eval(%q) refused without naming the repair: %v", expr, err)
		}
	}
}

func TestEmptyColumnAggregates(t *testing.T) {
	vars := map[string]Value{"none": Series(nil)}
	if got := evalOK(t, "sum(none)", vars); got != "0" {
		t.Errorf("sum of an empty column = %q, want 0", got)
	}
	if _, err := Eval("min(none)", vars); !errors.Is(err, ErrSyntax) {
		t.Errorf("min of an empty column = %v, want a refusal", err)
	}
}

func TestRound(t *testing.T) {
	if got := evalOK(t, "round(2.5, 0)", nil); got != "3" {
		t.Errorf("round(2.5, 0) = %q, want 3 — half away from zero", got)
	}
	if got := evalOK(t, "round(-2.5, 0)", nil); got != "-3" {
		t.Errorf("round(-2.5, 0) = %q, want -3", got)
	}
	if got := evalOK(t, "round(1.23456, 3)", nil); got != "1.235" {
		t.Errorf("round(1.23456, 3) = %q", got)
	}
	for _, expr := range []string{"round(1.5)", "round(1.5, 1.5)", "round(1.5, -1)", "round(1.5, 99)"} {
		if _, err := Eval(expr, nil); !errors.Is(err, ErrSyntax) {
			t.Errorf("Eval(%q) = %v, want a refusal", expr, err)
		}
	}
}

func TestComparisonIsOneOrZero(t *testing.T) {
	vars := map[string]Value{"margin": num(t, "-0.2")}
	if got := evalOK(t, "margin < 0", vars); got != "1" {
		t.Errorf("margin < 0 = %q, want 1", got)
	}
	if got := evalOK(t, "margin >= 0", vars); got != "0" {
		t.Errorf("margin >= 0 = %q, want 0", got)
	}
	if got := evalOK(t, "max(margin, 0)", vars); got != "0" {
		t.Errorf("max(margin, 0) = %q", got)
	}
}

func TestSyntaxRefusals(t *testing.T) {
	for _, tc := range []struct{ expr, wantIn string }{
		{"", "empty"},
		{"1 +", "ends where a value was expected"},
		{"(1 + 2", "never closed"},
		{"1 ) 2", "unexpected"},
		{"1 = 2", "use == to compare"},
		{"sqrt(4)", "no function"},
		{"sum()", "was given nothing"},
		{"1 $ 2", "not something this grammar has"},
		{"1.2.3", "two decimal points"},
		{strings.Repeat("(", 40) + "1" + strings.Repeat(")", 40), "nests deeper"},
		{strings.Repeat("1+", MaxExprBytes) + "1", "bytes and the limit is"},
	} {
		_, err := Eval(tc.expr, nil)
		if err == nil {
			t.Errorf("Eval(%.40q) succeeded, want a refusal", tc.expr)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantIn) {
			t.Errorf("Eval(%.40q) = %v, want a message containing %q", tc.expr, err, tc.wantIn)
		}
	}
}

func TestNames(t *testing.T) {
	got, err := Names("round(sum(monthly) / months, 2) + monthly")
	if err != nil {
		t.Fatalf("Names: %v", err)
	}
	want := []string{"monthly", "months"}
	if len(got) != len(want) {
		t.Fatalf("Names = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Names = %v, want %v", got, want)
		}
	}
}

// There is no float64 in the evaluation path, and the test that proves it is
// one a reviewer can re-run: a value no float64 can hold survives a round trip
// through the grammar unchanged.
func TestNoPrecisionIsLostInTransit(t *testing.T) {
	const exact = "123456789012345678901234567.89"
	if got := evalOK(t, "v + 0", map[string]Value{"v": num(t, exact)}); got != exact {
		t.Errorf("a 29-digit figure came back as %q, want %q", got, exact)
	}
}
