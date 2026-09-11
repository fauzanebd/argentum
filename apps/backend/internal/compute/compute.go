// Package compute is exact arithmetic over figures a tool already returned
// (T-W1).
//
// **The case it closes.** Revenue came back from one run_sql and cost from
// another, the user asked for the margin, and the ratio in the reply was
// produced by the model dividing two numbers inside a sentence. That figure is
// ungrounded by construction — no tool returned it, so CheckGrounding cannot
// match it and CheckFabrication does not look, because the turn did retrieve
// rows. T-Q14 found the shape of the error it makes: a December figure printed
// as $3,860,405,700.00 where run_sql had returned 3,863,405,700.00, which is
// 0.078% wrong and therefore inside every tolerance this product owns.
//
// **Why decimal and not float.** 0.1 + 0.2 != 0.3 in every IEEE-754 language,
// and the last decimal place is exactly what a reconciliation is looking at.
// This is a constraint on the package, not a preference: there is no float64
// anywhere below.
//
// **Why the grammar is this small.** Every function here is one somebody can
// read off a cell in a spreadsheet. It has no variables it was not handed, no
// assignment, no control flow and no function table it can grow into a
// language — because the moment it has those it is a program, and a program
// needs the sandbox T-W4 costs three days. The measurement that says whether
// that is worth buying is T-W3, and it cannot be taken until this exists.
package compute

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/shopspring/decimal"
)

// The four failures a caller has to tell apart. Every one of them is an error
// rather than a value, because a computation that silently returns zero for a
// division by zero is the fabrication this package exists to prevent, wearing
// the product's own signature.
var (
	// ErrUnbound is a name the expression used and the caller did not supply.
	ErrUnbound = errors.New("unbound name")
	// ErrDivideByZero is what it says. Never NaN, never Inf, never zero.
	ErrDivideByZero = errors.New("division by zero")
	// ErrOverflow is a figure too large to be a business figure. The limit is
	// arbitrary and generous; what matters is that it is a stated error rather
	// than a number nobody can read.
	ErrOverflow = errors.New("result too large")
	// ErrSyntax covers everything the parser could not make sense of, including
	// a function called with the wrong number of arguments.
	ErrSyntax = errors.New("expression is not valid")
)

const (
	// MaxExprBytes bounds the expression. A margin is twenty characters; this
	// is two thousand, and anything longer is a program rather than a sum.
	MaxExprBytes = 2000
	// maxDepth bounds parser recursion, so a pathological expression of nested
	// parentheses is a refusal rather than a stack overflow in the worker.
	maxDepth = 32
	// divisionScale is how many decimal places an inexact division keeps.
	// 1/3 has no exact decimal form and something has to decide where it stops;
	// 28 is the scale IEEE 754-2008's decimal128 carries and is far past any
	// currency. An exact division keeps nothing it does not need — 10/2 is "5",
	// not "5.000…" — because Decimal.String trims what the scale did not use.
	divisionScale = 28
	// maxDigits and maxExponent are the overflow limit, applied to every
	// intermediate rather than only to the result: a product that overflows and
	// is then divided back down would otherwise be an unbounded allocation in
	// the middle of a turn.
	maxDigits   = 120
	maxExponent = 1000
)

// Value is one binding: a single figure, or a column of them.
//
// The ticket's signature was map[string]decimal, and this is wider by exactly
// one case — `sum` over a named column of a prior result, which the same ticket
// asks for. A column is the shape run_sql returns, and requiring the caller to
// pre-reduce it would move the arithmetic back into the place this package
// exists to take it out of.
type Value struct {
	num      decimal.Decimal
	series   []decimal.Decimal
	isSeries bool
}

// Num binds one figure.
func Num(d decimal.Decimal) Value { return Value{num: d} }

// Series binds a column. An empty column is legal and is what `sum` returns
// zero for; `min` and `max` refuse it, because the smallest of nothing is not
// a number and returning one would be an invention.
func Series(ds []decimal.Decimal) Value {
	return Value{series: ds, isSeries: true}
}

// IsSeries reports whether this binding is a column rather than one figure.
func (v Value) IsSeries() bool { return v.isSeries }

// Scalar returns the single figure, and false if this is a column.
func (v Value) Scalar() (decimal.Decimal, bool) {
	if v.isSeries {
		return decimal.Zero, false
	}
	return v.num, true
}

// Len is how many figures the binding holds: 1 for a scalar.
func (v Value) Len() int {
	if v.isSeries {
		return len(v.series)
	}
	return 1
}

// Eval evaluates expr against vars and returns one exact figure.
//
// vars may be nil, which makes every name unbound — the correct behaviour for
// an expression that should have been all literals and was not.
func Eval(expr string, vars map[string]Value) (decimal.Decimal, error) {
	if len(expr) > MaxExprBytes {
		return decimal.Zero, fmt.Errorf("%w: the expression is %d bytes and the limit is %d", ErrSyntax, len(expr), MaxExprBytes)
	}
	if strings.TrimSpace(expr) == "" {
		return decimal.Zero, fmt.Errorf("%w: the expression is empty", ErrSyntax)
	}
	toks, err := lex(expr)
	if err != nil {
		return decimal.Zero, err
	}
	p := &parser{toks: toks, vars: vars}
	v, err := p.parseExpr(0)
	if err != nil {
		return decimal.Zero, err
	}
	if p.pos < len(p.toks) {
		return decimal.Zero, fmt.Errorf("%w: unexpected %q at position %d", ErrSyntax, p.toks[p.pos].text, p.toks[p.pos].pos)
	}
	if v.isSeries {
		return decimal.Zero, fmt.Errorf("%w: the expression returns a column of %d values rather than one figure — wrap it in sum(), min() or max()", ErrSyntax, len(v.series))
	}
	return v.num, nil
}

// Names returns every name expr refers to, so a caller can bind exactly what is
// asked for and report the rest before evaluating anything. Order is the order
// of first appearance, which is the order a person reads them in.
func Names(expr string) ([]string, error) {
	toks, err := lex(expr)
	if err != nil {
		return nil, err
	}
	var out []string
	seen := map[string]bool{}
	for i, t := range toks {
		if t.kind != tokIdent || seen[t.text] {
			continue
		}
		// A name immediately followed by "(" is a function, not a binding.
		if i+1 < len(toks) && toks[i+1].kind == tokLParen {
			continue
		}
		seen[t.text] = true
		out = append(out, t.text)
	}
	return out, nil
}

// ---------------------------------------------------------------- the lexer

type tokKind int

const (
	tokNum tokKind = iota
	tokIdent
	tokOp
	tokLParen
	tokRParen
	tokComma
)

type token struct {
	kind tokKind
	text string
	pos  int
}

func lex(expr string) ([]token, error) {
	var out []token
	rs := []rune(expr)
	for i := 0; i < len(rs); {
		c := rs[i]
		switch {
		case unicode.IsSpace(c):
			i++
		case c >= '0' && c <= '9', c == '.':
			start := i
			dot := false
			for i < len(rs) && (rs[i] >= '0' && rs[i] <= '9' || rs[i] == '.') {
				if rs[i] == '.' {
					if dot {
						return nil, fmt.Errorf("%w: %q has two decimal points, at position %d", ErrSyntax, string(rs[start:i+1]), start)
					}
					dot = true
				}
				i++
			}
			out = append(out, token{kind: tokNum, text: string(rs[start:i]), pos: start})
		case unicode.IsLetter(c) || c == '_':
			start := i
			for i < len(rs) && (unicode.IsLetter(rs[i]) || unicode.IsDigit(rs[i]) || rs[i] == '_') {
				i++
			}
			out = append(out, token{kind: tokIdent, text: string(rs[start:i]), pos: start})
		case c == '(':
			out = append(out, token{kind: tokLParen, text: "(", pos: i})
			i++
		case c == ')':
			out = append(out, token{kind: tokRParen, text: ")", pos: i})
			i++
		case c == ',':
			out = append(out, token{kind: tokComma, text: ",", pos: i})
			i++
		case strings.ContainsRune("+-*/", c):
			out = append(out, token{kind: tokOp, text: string(c), pos: i})
			i++
		case c == '<', c == '>', c == '=', c == '!':
			op := string(c)
			if i+1 < len(rs) && rs[i+1] == '=' {
				op += "="
			}
			switch op {
			case "<", "<=", ">", ">=", "==", "!=":
				out = append(out, token{kind: tokOp, text: op, pos: i})
			case "=":
				// The single = is the one typo worth naming: it is assignment
				// in most languages and equality in a spreadsheet, and this is
				// neither.
				return nil, fmt.Errorf("%w: %q at position %d is not an operator here — use == to compare", ErrSyntax, "=", i)
			default:
				return nil, fmt.Errorf("%w: %q at position %d is not an operator", ErrSyntax, op, i)
			}
			i += len(op)
		default:
			return nil, fmt.Errorf("%w: %q at position %d is not something this grammar has", ErrSyntax, string(c), i)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: the expression is empty", ErrSyntax)
	}
	return out, nil
}

// --------------------------------------------------------------- the parser

type parser struct {
	toks []token
	pos  int
	vars map[string]Value
}

func (p *parser) peek() (token, bool) {
	if p.pos >= len(p.toks) {
		return token{}, false
	}
	return p.toks[p.pos], true
}

func (p *parser) eatOp(ops ...string) (string, bool) {
	t, ok := p.peek()
	if !ok || t.kind != tokOp {
		return "", false
	}
	for _, op := range ops {
		if t.text == op {
			p.pos++
			return op, true
		}
	}
	return "", false
}

// parseExpr is the comparison level: a <= b, a == b. A comparison is 1 or 0,
// which is what makes it composable — max(margin > 0, 0) is a floor — and it
// does not chain, because a < b < c means two different things to two different
// readers and neither of them is worth guessing.
func (p *parser) parseExpr(depth int) (Value, error) {
	if depth > maxDepth {
		return Value{}, fmt.Errorf("%w: the expression nests deeper than %d levels", ErrSyntax, maxDepth)
	}
	left, err := p.parseSum(depth + 1)
	if err != nil {
		return Value{}, err
	}
	op, ok := p.eatOp("<", "<=", ">", ">=", "==", "!=")
	if !ok {
		return left, nil
	}
	right, err := p.parseSum(depth + 1)
	if err != nil {
		return Value{}, err
	}
	l, err := scalarOf(left, op)
	if err != nil {
		return Value{}, err
	}
	r, err := scalarOf(right, op)
	if err != nil {
		return Value{}, err
	}
	var truth bool
	switch op {
	case "<":
		truth = l.LessThan(r)
	case "<=":
		truth = l.LessThanOrEqual(r)
	case ">":
		truth = l.GreaterThan(r)
	case ">=":
		truth = l.GreaterThanOrEqual(r)
	case "==":
		truth = l.Equal(r)
	case "!=":
		truth = !l.Equal(r)
	}
	if truth {
		return Num(decimal.NewFromInt(1)), nil
	}
	return Num(decimal.Zero), nil
}

func (p *parser) parseSum(depth int) (Value, error) {
	if depth > maxDepth {
		return Value{}, fmt.Errorf("%w: the expression nests deeper than %d levels", ErrSyntax, maxDepth)
	}
	acc, err := p.parseProduct(depth + 1)
	if err != nil {
		return Value{}, err
	}
	for {
		op, ok := p.eatOp("+", "-")
		if !ok {
			return acc, nil
		}
		rhs, err := p.parseProduct(depth + 1)
		if err != nil {
			return Value{}, err
		}
		l, err := scalarOf(acc, op)
		if err != nil {
			return Value{}, err
		}
		r, err := scalarOf(rhs, op)
		if err != nil {
			return Value{}, err
		}
		var got decimal.Decimal
		if op == "+" {
			got = l.Add(r)
		} else {
			got = l.Sub(r)
		}
		if err := guard(got); err != nil {
			return Value{}, err
		}
		acc = Num(got)
	}
}

func (p *parser) parseProduct(depth int) (Value, error) {
	if depth > maxDepth {
		return Value{}, fmt.Errorf("%w: the expression nests deeper than %d levels", ErrSyntax, maxDepth)
	}
	acc, err := p.parseUnary(depth + 1)
	if err != nil {
		return Value{}, err
	}
	for {
		op, ok := p.eatOp("*", "/")
		if !ok {
			return acc, nil
		}
		rhs, err := p.parseUnary(depth + 1)
		if err != nil {
			return Value{}, err
		}
		l, err := scalarOf(acc, op)
		if err != nil {
			return Value{}, err
		}
		r, err := scalarOf(rhs, op)
		if err != nil {
			return Value{}, err
		}
		var got decimal.Decimal
		if op == "*" {
			got = l.Mul(r)
		} else {
			if r.IsZero() {
				return Value{}, fmt.Errorf("%w: the divisor evaluated to 0. Check the figure you divided by — a zero denominator usually means the query it came from matched nothing", ErrDivideByZero)
			}
			got = l.DivRound(r, divisionScale)
		}
		if err := guard(got); err != nil {
			return Value{}, err
		}
		acc = Num(got)
	}
}

func (p *parser) parseUnary(depth int) (Value, error) {
	if depth > maxDepth {
		return Value{}, fmt.Errorf("%w: the expression nests deeper than %d levels", ErrSyntax, maxDepth)
	}
	if op, ok := p.eatOp("+", "-"); ok {
		v, err := p.parseUnary(depth + 1)
		if err != nil {
			return Value{}, err
		}
		d, err := scalarOf(v, op)
		if err != nil {
			return Value{}, err
		}
		if op == "-" {
			return Num(d.Neg()), nil
		}
		return Num(d), nil
	}
	return p.parsePrimary(depth + 1)
}

func (p *parser) parsePrimary(depth int) (Value, error) {
	t, ok := p.peek()
	if !ok {
		return Value{}, fmt.Errorf("%w: the expression ends where a value was expected", ErrSyntax)
	}
	switch t.kind {
	case tokNum:
		p.pos++
		d, err := decimal.NewFromString(t.text)
		if err != nil {
			return Value{}, fmt.Errorf("%w: %q at position %d is not a number", ErrSyntax, t.text, t.pos)
		}
		if err := guard(d); err != nil {
			return Value{}, err
		}
		return Num(d), nil
	case tokLParen:
		p.pos++
		v, err := p.parseExpr(depth + 1)
		if err != nil {
			return Value{}, err
		}
		if nt, ok := p.peek(); !ok || nt.kind != tokRParen {
			return Value{}, fmt.Errorf("%w: a '(' opened at position %d was never closed", ErrSyntax, t.pos)
		}
		p.pos++
		return v, nil
	case tokIdent:
		if nt, ok := p.next(); ok && nt.kind == tokLParen {
			return p.parseCall(depth)
		}
		p.pos++
		v, ok := p.vars[t.text]
		if !ok {
			return Value{}, fmt.Errorf("%w: %q was not bound. Every name in an expression must be bound to a value a tool returned in this turn — bind it, or remove it", ErrUnbound, t.text)
		}
		return v, nil
	default:
		return Value{}, fmt.Errorf("%w: unexpected %q at position %d", ErrSyntax, t.text, t.pos)
	}
}

func (p *parser) next() (token, bool) {
	if p.pos+1 >= len(p.toks) {
		return token{}, false
	}
	return p.toks[p.pos+1], true
}

func (p *parser) parseCall(depth int) (Value, error) {
	name := p.toks[p.pos]
	fn, known := funcs[name.text]
	if !known {
		return Value{}, fmt.Errorf("%w: there is no function %q. This grammar has %s and nothing else", ErrSyntax, name.text, funcNames())
	}
	p.pos += 2 // the name and the '('
	var args []Value
	if t, ok := p.peek(); ok && t.kind == tokRParen {
		p.pos++
		return fn(args)
	}
	for {
		v, err := p.parseExpr(depth + 1)
		if err != nil {
			return Value{}, err
		}
		args = append(args, v)
		t, ok := p.peek()
		if !ok {
			return Value{}, fmt.Errorf("%w: %s( opened at position %d was never closed", ErrSyntax, name.text, name.pos)
		}
		switch t.kind {
		case tokComma:
			p.pos++
		case tokRParen:
			p.pos++
			return fn(args)
		default:
			return Value{}, fmt.Errorf("%w: unexpected %q at position %d inside %s()", ErrSyntax, t.text, t.pos, name.text)
		}
	}
}

// ------------------------------------------------------------- the functions

var funcs = map[string]func([]Value) (Value, error){
	"abs":   fnAbs,
	"round": fnRound,
	"sum":   fnSum,
	"min":   fnMin,
	"max":   fnMax,
}

func funcNames() string { return "abs, max, min, round and sum" }

func fnAbs(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%w: abs() takes one value and was given %d", ErrSyntax, len(args))
	}
	d, err := scalarOf(args[0], "abs")
	if err != nil {
		return Value{}, err
	}
	return Num(d.Abs()), nil
}

// fnRound rounds half away from zero: 2.5 is 3 and -2.5 is -3. That is the
// convention a person expects and the one a spreadsheet uses. Half-even —
// which a currency's accounting policy may require instead — is T-W2's, and it
// arrives as a property of the company's currency rather than as a second
// function, because the choice belongs to the tenant and not to whichever
// expression the model happened to write.
func fnRound(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%w: round() takes a value and a number of decimal places, and was given %d argument(s)", ErrSyntax, len(args))
	}
	d, err := scalarOf(args[0], "round")
	if err != nil {
		return Value{}, err
	}
	places, err := scalarOf(args[1], "round")
	if err != nil {
		return Value{}, err
	}
	if !places.IsInteger() || places.Cmp(decimal.NewFromInt(0)) < 0 || places.Cmp(decimal.NewFromInt(divisionScale)) > 0 {
		return Value{}, fmt.Errorf("%w: round()'s second argument is the number of decimal places and must be a whole number between 0 and %d, not %s", ErrSyntax, divisionScale, places.String())
	}
	return Num(d.Round(int32(places.IntPart()))), nil
}

func fnSum(args []Value) (Value, error) {
	ds, err := flatten("sum", args)
	if err != nil {
		return Value{}, err
	}
	// The sum of nothing is zero, which is the one aggregate with a defensible
	// answer over an empty column: adding no figures adds nothing. min and max
	// refuse the same input, below, for the mirrored reason.
	acc := decimal.Zero
	for _, d := range ds {
		acc = acc.Add(d)
		if err := guard(acc); err != nil {
			return Value{}, err
		}
	}
	return Num(acc), nil
}

func fnMin(args []Value) (Value, error) { return extreme("min", args, -1) }
func fnMax(args []Value) (Value, error) { return extreme("max", args, 1) }

func extreme(name string, args []Value, want int) (Value, error) {
	ds, err := flatten(name, args)
	if err != nil {
		return Value{}, err
	}
	if len(ds) == 0 {
		return Value{}, fmt.Errorf("%w: %s() of nothing is not a number — the column it was given is empty", ErrSyntax, name)
	}
	best := ds[0]
	for _, d := range ds[1:] {
		if d.Cmp(best) == want {
			best = d
		}
	}
	return Num(best), nil
}

// flatten turns the arguments of an aggregate into one list of figures, so
// sum(col), sum(a, b) and sum(col, a) all mean what they look like.
func flatten(name string, args []Value) ([]decimal.Decimal, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("%w: %s() was given nothing to work on", ErrSyntax, name)
	}
	var out []decimal.Decimal
	for _, a := range args {
		if a.isSeries {
			out = append(out, a.series...)
			continue
		}
		out = append(out, a.num)
	}
	return out, nil
}

// scalarOf is the one place a column is refused where a single figure was
// needed. The message names the operator, because "revenue * 2" against a
// twelve-row column is a mistake with an obvious repair and the model can make
// it without being told which step went wrong.
func scalarOf(v Value, op string) (decimal.Decimal, error) {
	if !v.isSeries {
		return v.num, nil
	}
	return decimal.Zero, fmt.Errorf("%w: %q was given a column of %d values where one figure was needed — reduce it first with sum(), min() or max()", ErrSyntax, op, len(v.series))
}

// guard refuses a figure past the size limit. Applied to every intermediate:
// an unbounded product divided back down would otherwise allocate the whole
// product first, inside a turn that is holding a tenant connection.
func guard(d decimal.Decimal) error {
	if d.IsZero() {
		return nil
	}
	if d.NumDigits() > maxDigits {
		return fmt.Errorf("%w: an intermediate value has %d digits and the limit is %d", ErrOverflow, d.NumDigits(), maxDigits)
	}
	if e := d.Exponent(); e > maxExponent || e < -maxExponent {
		return fmt.Errorf("%w: an intermediate value has an exponent of %d and the limit is ±%d", ErrOverflow, e, maxExponent)
	}
	return nil
}

// Rounding is what Quantize does at exactly half a unit.
type Rounding int

const (
	// HalfAwayFromZero: 2.5 → 3, -2.5 → -3. What a person expects.
	HalfAwayFromZero Rounding = iota
	// HalfToEven: 2.5 → 2, 3.5 → 4. Banker's rounding, which several
	// accounting standards require because it does not drift upward over a
	// long ledger.
	HalfToEven
)

// Quantize fixes a figure to a currency's precision (T-W2).
//
// It lives here rather than in the domain because it is arithmetic, and this
// is the package that owns arithmetic: the domain states *which* convention a
// tenant uses and this applies it. Which also keeps the decimal dependency out
// of the domain.
//
// Applied to money and to nothing else. A ratio quantised to a currency's
// scale is a ratio destroyed — 0.4564 at IDR's zero places is 0 — and the
// caller decides by what the figure *is*, not by how it looks.
func Quantize(d decimal.Decimal, places int, mode Rounding) decimal.Decimal {
	if places < 0 || places > 18 {
		return d
	}
	if mode == HalfToEven {
		return d.RoundBank(int32(places))
	}
	return d.Round(int32(places))
}
