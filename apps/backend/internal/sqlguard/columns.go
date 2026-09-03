package sqlguard

import (
	"fmt"
	"sort"
	"strings"
)

// Which columns a statement reads (T-H12, the column half).
//
// **Why this exists as a second pass rather than as part of the table walk.**
// The table half is small because a table name can only appear in two places,
// after FROM and after JOIN. A column can appear anywhere an expression can —
// the select list, a predicate, a join condition, an ordering, inside a
// function call, inside a CASE. There is no position to anchor on, so this
// walks every token and asks the opposite question: is there any reason this
// identifier is *not* a column reference?
//
// That inversion is what makes it sound in the direction that matters. An
// identifier this misclassifies as a column is checked against the allowlist
// and, if absent, refuses a legitimate query — annoying, visible, and fixed by
// the tenant naming the column or widening the list. An identifier it fails to
// notice would be read from a table the tenant restricted, silently. So every
// judgement call below is made toward *over*-collecting, and the keyword set is
// generous rather than minimal.
//
// **The bypass this cannot have.** A column can be read without being named
// only by `*`, and [ValidateReferences] refuses a star against a
// column-restricted table before it gets here. That is the whole soundness
// argument: to read a column you must either name it — in which case this sees
// the name — or star it, in which case you are already refused.
//
// **It costs nothing for the tenants it does not serve.** None of this runs
// unless a referenced table has a column rule on it, which is opt-in
// configuration. A tenant with a table-only allowlist, or none, pays exactly
// what they paid before.

// sqlWords are tokens that can stand where a column would and are not one.
//
// Deliberately over-inclusive: a keyword wrongly listed here can only cause a
// missed check on a column that happens to share its name, and a column named
// `select` or `from` cannot be written unquoted anyway. A keyword *missing*
// from this list causes a false refusal, which is the failure the tenant can
// see and report.
var sqlWords = map[string]bool{
	// Statement and clause structure.
	"select": true, "from": true, "where": true, "group": true, "by": true,
	"order": true, "having": true, "limit": true, "offset": true, "fetch": true,
	"first": true, "next": true, "rows": true, "row": true, "only": true,
	"with": true, "recursive": true, "as": true, "distinct": true, "all": true,
	"union": true, "intersect": true, "except": true, "join": true,
	"inner": true, "left": true, "right": true, "full": true, "cross": true,
	"outer": true, "natural": true, "on": true, "using": true, "lateral": true,
	"window": true, "partition": true, "over": true, "filter": true,
	"within": true, "ordinality": true, "for": true,
	// Operators and predicates.
	"and": true, "or": true, "not": true, "in": true, "is": true, "null": true,
	"like": true, "ilike": true, "between": true, "exists": true, "any": true,
	"some": true, "case": true, "when": true, "then": true, "else": true,
	"end": true, "asc": true, "desc": true, "nulls": true, "last": true,
	"escape": true, "similar": true, "collate": true,
	// Literals.
	"true": true, "false": true, "unknown": true, "default": true,
	// Cast and type names. A cast target sits exactly where a column would.
	"cast": true, "int": true, "integer": true, "bigint": true, "smallint": true,
	"decimal": true, "numeric": true, "real": true, "double": true,
	"precision": true, "float": true, "text": true, "varchar": true,
	"char": true, "character": true, "varying": true, "boolean": true,
	"bool": true, "date": true, "time": true, "timestamp": true,
	"timestamptz": true, "datetime": true, "datetime2": true, "interval": true,
	"json": true, "jsonb": true, "uuid": true, "bytea": true, "money": true,
	"serial": true, "nvarchar": true, "ntext": true, "bit": true,
	"zone": true, "without": true, "at": true,
	// Datetime field names, which appear bare inside EXTRACT and DATE_TRUNC.
	"year": true, "quarter": true, "month": true, "week": true, "day": true,
	"hour": true, "minute": true, "second": true, "millisecond": true,
	"microsecond": true, "epoch": true, "dow": true, "doy": true,
	"isoyear": true, "timezone": true, "century": true, "decade": true,
	// Set-quantifier and misc noise.
	"value": true, "values": true, "returning": true, "into": true,
}

// nonFunctionBefore are the tokens that can precede a `(` which opens a
// subquery or a parenthesised expression rather than a function's argument
// list. Anything else before a `(` is a function name.
//
// Phrased as the exception list rather than as a list of function names for the
// obvious reason: every deployment's warehouse has functions this package has
// never heard of, and a missing name would put us back to reading `FROM` inside
// a function call as a table clause.
var nonFunctionBefore = map[string]bool{
	"from": true, "join": true, "in": true, "exists": true, "on": true,
	"union": true, "intersect": true, "except": true, "and": true, "or": true,
	"not": true, "where": true, "select": true, "by": true, "as": true,
	"when": true, "then": true, "else": true, "values": true, "all": true,
	"any": true, "some": true, "between": true, "using": true, "lateral": true,
	"returning": true, "having": true, "distinct": true, "over": true,
	// `WITH t AS MATERIALIZED (SELECT …)` puts a keyword between AS and the
	// body, so the body's parenthesis is preceded by a name that is not a
	// function. Caught by TestCTEWalkHandlesTheRealForms the moment this list
	// went in, which is the test earning its keep.
	"materialized": true, "recursive": true,
	"limit": true, "offset": true, "case": true, "is": true, "like": true,
	"ilike": true, "filter": true, "partition": true,
}

// insideFunctionCall marks every token that sits inside a function's argument
// list.
//
// **This exists because SQL spells several functions with clause keywords in
// them.** `extract(year FROM created_at)`, `substring(name FROM 1 FOR 3)` and
// `trim(BOTH ' ' FROM name)` all put a `FROM` inside parentheses, and the table
// walk reads every `FROM` as introducing a table. Before this, the first of
// those reported `created_at` as a *table* and the second reported `1` — so on
// a table-restricted source a perfectly ordinary date query was refused with
// `table "created_at" is not readable by this agent`, which is both wrong and
// unactionable. Found by the column half's own false-positive tests, 2026-09-03;
// it predates them and belongs to the table half.
func insideFunctionCall(words []string) []bool {
	out := make([]bool, len(words))
	var isFn []bool
	depth := 0
	for i, w := range words {
		switch w {
		case "(":
			fn := i > 0 && isIdentifierWord(words[i-1]) && !nonFunctionBefore[strings.ToLower(words[i-1])]
			isFn = append(isFn, fn)
			if fn {
				depth++
			}
		case ")":
			if n := len(isFn); n > 0 {
				if isFn[n-1] {
					depth--
				}
				isFn = isFn[:n-1]
			}
		}
		out[i] = depth > 0
	}
	return out
}

// resultAliases collects the names bound by `AS` in a select list. They are
// referenced later by GROUP BY and ORDER BY, where they look exactly like bare
// column names and are not: `sum(amount) AS total … GROUP BY total` reads no
// column called `total`.
func resultAliases(words []string) map[string]bool {
	out := map[string]bool{}
	for i := 1; i < len(words); i++ {
		if strings.ToLower(words[i-1]) != "as" {
			continue
		}
		// `x AS (` is a CTE binding, not a result alias.
		if i+1 < len(words) && words[i+1] == "(" {
			continue
		}
		if n := normalizeSQLIdentifier(words[i]); n != "" {
			out[n] = true
		}
	}
	return out
}

// isQuoteNoise reports whether a token is what stripStringLiterals left behind
// (`”`) or bare quoting with no name inside it.
func isQuoteNoise(w string) bool {
	return strings.Trim(w, "`\"[]'") == ""
}

// extractColumns walks the token stream and collects every reference that could
// be a column.
//
// It is given the statement's tables and aliases so that a name in table
// position is not also read as a column: `FROM employees e` must not contribute
// `employees` or `e` to the column set.
func extractColumns(words []string, refs *References) {
	tableNames := map[string]bool{}
	for _, t := range refs.Tables {
		tableNames[t] = true
	}
	for a := range refs.Aliases {
		tableNames[a] = true
	}

	qualified := map[QualifiedColumn]bool{}
	bare := map[string]bool{}
	aliases := resultAliases(words)

	for i, w := range words {
		if !isIdentifierWord(w) || isQuoteNoise(w) {
			continue
		}
		lw := strings.ToLower(w)
		if sqlWords[lw] {
			continue
		}
		// A name immediately followed by `(` is a function, not a column.
		if i+1 < len(words) && words[i+1] == "(" {
			continue
		}
		// An alias definition: `… AS total`. The name binds a result column
		// rather than reading a stored one.
		if i > 0 && strings.ToLower(words[i-1]) == "as" {
			continue
		}
		// Anything the FROM walk already accounted for.
		if tableNames[normalizeSQLIdentifier(w)] {
			continue
		}
		tok := stripCast(w)
		if tok == "" || isNumericWord(tok) {
			continue
		}
		if q, c, ok := splitQualified(tok); ok {
			qualified[QualifiedColumn{Qualifier: q, Column: c}] = true
			continue
		}
		n := normalizeSQLIdentifier(tok)
		if n == "" || tableNames[n] || aliases[n] {
			continue
		}
		bare[n] = true
	}

	for q := range qualified {
		refs.QualifiedColumns = append(refs.QualifiedColumns, q)
	}
	sort.Slice(refs.QualifiedColumns, func(i, j int) bool {
		a, b := refs.QualifiedColumns[i], refs.QualifiedColumns[j]
		if a.Qualifier != b.Qualifier {
			return a.Qualifier < b.Qualifier
		}
		return a.Column < b.Column
	})
	for c := range bare {
		refs.BareColumns = append(refs.BareColumns, c)
	}
	sort.Strings(refs.BareColumns)
}

// stripCast removes a `::type` suffix, which the splitter leaves attached
// because `:` is not punctuation to it. `e.salary::numeric` has to become
// `e.salary` or the reference is unreadable.
func stripCast(w string) string {
	if i := strings.Index(w, "::"); i >= 0 {
		return w[:i]
	}
	return w
}

// splitQualified splits `alias.column` into its two halves.
//
// Only the last two segments matter: `public.employees.salary` qualifies
// `salary` with `employees`, which is what the alias map is keyed on.
func splitQualified(w string) (qualifier, column string, ok bool) {
	i := strings.LastIndex(w, ".")
	if i <= 0 || i == len(w)-1 {
		return "", "", false
	}
	left, right := w[:i], w[i+1:]
	if j := strings.LastIndex(left, "."); j >= 0 {
		left = left[j+1:]
	}
	q := normalizeSQLIdentifier(left)
	c := normalizeSQLIdentifier(right)
	if q == "" || c == "" {
		return "", "", false
	}
	return q, c, true
}

// isNumericWord reports whether a token is a number rather than a name.
func isNumericWord(w string) bool {
	w = strings.Trim(w, "`\"[]")
	if w == "" {
		return false
	}
	dots := 0
	for _, r := range w {
		switch {
		case r >= '0' && r <= '9':
		case r == '.':
			dots++
		default:
			return false
		}
	}
	return dots <= 1
}

// validateColumns is the column half of [ValidateReferences].
//
// Called only when at least one referenced table carries a column rule, and
// after the star check, so everything reaching it names its columns.
func validateColumns(refs References, restricted func(string) bool, allows func(table, column string) bool) error {
	if allows == nil {
		// A caller that restricted columns and supplied no predicate to check
		// them with is a wiring bug, and the safe reading of a missing rule is
		// that nothing satisfies it.
		return fmt.Errorf("this source restricts which columns may be read, and the check is not configured; refusing rather than guessing")
	}

	restrictedTables := make([]string, 0, len(refs.Tables))
	for _, t := range refs.Tables {
		if restricted(t) {
			restrictedTables = append(restrictedTables, t)
		}
	}
	if len(restrictedTables) == 0 {
		return nil
	}

	// A derived table's alias names a projection this lexer cannot see into, so
	// a column read through it cannot be attributed. Refused only when a
	// restricted table is in play, which is the whole cost of the feature.
	for alias, table := range refs.Aliases {
		if table != "" {
			continue
		}
		for _, q := range refs.QualifiedColumns {
			if q.Qualifier == alias {
				return fmt.Errorf(
					"this source restricts which columns may be read on %q, and %q reads through a subquery or CTE this check cannot see into. "+
						"Query the table directly, naming the columns you need",
					restrictedTables[0], alias+"."+q.Column)
			}
		}
	}

	for _, q := range refs.QualifiedColumns {
		table, known := refs.Aliases[q.Qualifier]
		if !known {
			return fmt.Errorf(
				"this source restricts which columns may be read, and %q is qualified by %q, which is not a table or alias named in this statement. "+
					"Qualify each column with the table or alias it comes from",
				q.Column, q.Qualifier)
		}
		if table == "" || !restricted(table) {
			continue
		}
		if !allows(table, q.Column) {
			// The column is echoed and the allowed set is not, for the same
			// reason the table refusal above does not recite the allowlist:
			// get_schema already shows exactly the readable columns, so the
			// agent has the answer, and repeating it here would put a tenant's
			// configuration in front of a prompt-injected turn.
			return fmt.Errorf("column %q of table %q is not readable by this agent on this source", q.Column, table)
		}
	}

	if len(refs.BareColumns) == 0 {
		return nil
	}
	// An unqualified column belongs to whichever table has it, which is a
	// question about the schema rather than about the statement. With exactly
	// one table in the statement there is nothing to decide; with more than one
	// there is, and this refuses rather than picks.
	if len(refs.Tables) != 1 {
		return fmt.Errorf(
			"this source restricts which columns may be read on %q, and this statement names columns (%s) without saying which table they come from. "+
				"Qualify every column with its table or alias",
			restrictedTables[0], strings.Join(refs.BareColumns, ", "))
	}
	table := refs.Tables[0]
	for _, c := range refs.BareColumns {
		if !allows(table, c) {
			return fmt.Errorf("column %q of table %q is not readable by this agent on this source", c, table)
		}
	}
	return nil
}
