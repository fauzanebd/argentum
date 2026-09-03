package sqlguard

import (
	"fmt"
	"sort"
	"strings"
)

// What a statement references (T-H12).
//
// **This is the lexer's hardest job and its most honest failure mode.** Reading
// mutating keywords off scrubbed SQL is easy: the keyword is either there or it
// is not. Reading *which tables a query touches* is grammar, and a lexer does
// not have one. So this file does the only sound thing available to it — it
// extracts what it can recognise and reports, separately, whether it met
// anything it could not.
//
// That second return value is the whole design. A blocklist that misses a token
// misses an attack; an allowlist that misses a token would *admit* one, which
// is worse, because the tenant has been told the agent is restricted. So the
// caller is handed the uncertainty rather than a false negative, and
// [ValidateReferences] refuses on it. A query this cannot read confidently is
// refused with a sentence saying so, and the tenant's response is to name the
// table or to loosen the allowlist — both of which are better than silently
// reading a table they excluded.
//
// T-H4 step 2 replaces this with a real parse (pg_query_go, vitess). When it
// lands, `ReferencedTables` keeps its signature and stops ever setting
// `Uncertain` for the two dialects that get a parser. That is why the
// uncertainty is a field rather than an error: it is expected to go away for
// most callers, and the SQL Server arm will keep it forever.

// References is what a statement was found to touch.
type References struct {
	// Tables are the normalised names found in FROM/JOIN position, plus any
	// CTE name they resolve through. Sorted and de-duplicated.
	Tables []string
	// Uncertain is set when the statement contains a construct this lexer
	// cannot attribute to a table — a dynamic name, a nested subquery shape it
	// does not follow, an unbalanced parenthesis. See the file comment: the
	// caller refuses rather than guesses.
	Uncertain bool
	// UncertainReason names what it could not read, so a refusal is actionable.
	UncertainReason string
	// SelectsStar is true when the statement contains an unqualified or
	// qualified `*` in a select list. It matters because a star names no column
	// and expands, at the database, to every column the table has — including
	// the ones a column allowlist excludes.
	SelectsStar bool
	// Aliases maps a table alias to the table it names, so `e.salary` can be
	// attributed to `employees`. A table named with no alias maps to itself.
	Aliases map[string]string
	// QualifiedColumns are the `qualifier.column` references found anywhere in
	// the statement — select list, predicate, join condition, ordering.
	QualifiedColumns []QualifiedColumn
	// BareColumns are identifiers in expression position with no qualifier.
	// They can only be attributed to a table when exactly one is in play; see
	// ValidateReferences.
	BareColumns []string
}

// QualifiedColumn is one `qualifier.column` reference, with the qualifier left
// as written. Resolving it against Aliases is the caller's job because an
// unresolvable qualifier is an uncertainty rather than a lookup failure.
type QualifiedColumn struct {
	Qualifier string
	Column    string
}

// tableKeywords introduce a table reference. `INTO`, `UPDATE` and the rest are
// absent deliberately: ValidateStatement has already refused any statement
// containing them, so a name in one of those positions cannot reach here, and
// listing them would imply this function is a mutation check. It is not.
var tableKeywords = map[string]bool{
	"from": true,
	"join": true,
}

// ReferencedTables reads the table names out of a single read statement.
//
// The statement is expected to have passed [ValidateStatement] already — one
// statement, a SELECT or a WITH, no mutating keyword. That is what makes the
// grammar small enough to lex: the only places a table name can appear are
// after FROM and after JOIN.
//
// **CTE names are resolved, not reported.** `WITH recent AS (SELECT … FROM
// fact_sales) SELECT * FROM recent` references `fact_sales`; reporting `recent`
// would make an allowlist refuse a legitimate query for naming something that
// is not a table, and — the direction that matters — would let a caller *pass*
// a check by wrapping a forbidden table in a CTE whose name happens to be
// allowed.
func ReferencedTables(sql string) References {
	scrubbed := stripStringLiterals(stripComments(sql))
	var refs References
	refs.SelectsStar = containsSelectStar(scrubbed)

	words := splitSQLWords(scrubbed)
	ctes := cteNames(words)

	seen := map[string]bool{}
	refs.Aliases = map[string]string{}
	// `FROM` inside a function's argument list is part of that function's
	// syntax, not a table clause — see insideFunctionCall in columns.go for the
	// two ordinary queries this was getting wrong.
	inFn := insideFunctionCall(words)
	for i, w := range words {
		lw := strings.ToLower(w)
		if !tableKeywords[lw] || inFn[i] {
			continue
		}
		// FROM introduces a *list*; JOIN introduces exactly one. The
		// distinction is the bypass this loop exists to close:
		// `FROM fact_sales, salaries` is two tables in one clause, and reading
		// only the first admits the second — which is worse than refusing,
		// because the tenant has been told the agent is restricted.
		trs, ok := tableRefsAfter(words, i, lw == "from")
		if !ok {
			refs.Uncertain = true
			refs.UncertainReason = fmt.Sprintf("could not read the table name after %q", strings.ToUpper(w))
			continue
		}
		for _, tr := range trs {
			n := normalizeSQLIdentifier(tr.name)
			a := normalizeSQLIdentifier(tr.alias)
			if tr.derived {
				// A derived table's alias names a projection, not a table.
				// Mapping it to "" is what makes the column check refuse a
				// reference through it rather than attribute it wrongly.
				if a != "" {
					refs.Aliases[a] = ""
				}
				continue
			}
			if n == "" {
				continue
			}
			if a != "" {
				refs.Aliases[a] = n
			}
			if ctes[n] {
				// A CTE name resolves through to its body, which the loop
				// reads separately. Its alias cannot be attributed to a table.
				if a != "" {
					refs.Aliases[a] = ""
				}
				refs.Aliases[n] = ""
				continue
			}
			refs.Aliases[n] = n
			seen[n] = true
		}
	}

	refs.Tables = make([]string, 0, len(seen))
	for n := range seen {
		refs.Tables = append(refs.Tables, n)
	}
	sort.Strings(refs.Tables)

	// A read that names no table at all is either `SELECT 1` — harmless, and
	// worth allowing so a model can probe connectivity — or a shape this lexer
	// did not follow. Distinguishing them is the one case where guessing is
	// cheap and wrong: `SELECT` with a FROM somewhere in it must have produced
	// a name.
	if len(refs.Tables) == 0 && !refs.Uncertain && strings.Contains(strings.ToLower(scrubbed), "from") {
		refs.Uncertain = true
		refs.UncertainReason = "the statement has a FROM clause but no table name could be read from it"
	}

	// The column half (T-H12). Runs unconditionally so `References` describes
	// the statement rather than the policy; whether any of it is *checked* is
	// ValidateReferences' decision, and it checks nothing unless a referenced
	// table carries a column rule.
	extractColumns(words, &refs)
	return refs
}

// ValidateReferences refuses a statement that touches anything outside the
// allowlist, or that this lexer cannot read confidently.
//
// `allows` is the caller's predicate rather than a list, so this package does
// not have to import the domain type that holds the rule. `columnsRestricted`
// answers whether a table exposes fewer than all of its columns, which is what
// makes the `SELECT *` refusal possible. `allowsColumn` answers the column
// half — whether one named column of one table may be read — and is what
// closes the gap the 2026-08-22 gate found: a caller who names the column
// instead of starring it used to read straight through a "table **and column**
// allowlist". See columns.go for why that half is a separate walk.
//
// An empty allowlist never reaches here: the caller checks
// `Allowlist.Restricted()` first, because a statement refused for being
// unreadable is a real cost and must not be paid by a tenant who configured no
// restriction at all.
func ValidateReferences(
	sql string,
	allows func(table string) bool,
	columnsRestricted func(table string) bool,
	allowsColumn func(table, column string) bool,
) error {
	refs := ReferencedTables(sql)
	if refs.Uncertain {
		return fmt.Errorf(
			"this source restricts which tables may be read, and %s. "+
				"Rewrite the query so each table is named directly after FROM or JOIN",
			refs.UncertainReason)
	}
	for _, t := range refs.Tables {
		if !allows(t) {
			// The name is echoed because the model chose it and needs to know
			// which one was refused. Which tables *are* allowed is not listed
			// here: get_schema already shows exactly those and nothing else, so
			// the agent has the answer, and repeating a tenant's allowlist into
			// every refusal would put it in front of a prompt-injected turn.
			return fmt.Errorf("table %q is not readable by this agent on this source", t)
		}
		if refs.SelectsStar && columnsRestricted != nil && columnsRestricted(t) {
			// The sentence carries both remedies because the rule catches two
			// different statements. A real `SELECT *` is fixed by naming
			// columns; a `count(*)` is not — it is fixed by `count(1)`, and a
			// model told only to name columns will rewrite the select list,
			// keep the `count(*)`, and be refused again. The §1q gate found
			// this on the first ordinary analytical query it tried.
			return fmt.Errorf(
				"table %q exposes only some of its columns, so a statement containing `*` cannot be run against it — "+
					"including `count(*)`. Use `count(1)` for a row count, and name the columns you need elsewhere", t)
		}
	}
	if columnsRestricted == nil {
		return nil
	}
	return validateColumns(refs, columnsRestricted, allowsColumn)
}

// containsSelectStar reports whether a select list contains `*`.
//
// Deliberately crude in the safe direction: `count(*)` sets it too. That reads
// as a false positive and is not one — the refusal only fires for a table whose
// columns are restricted, and on such a table `count(*)` and `count(1)` are the
// same answer, so the cost is one rewrite and the alternative is reasoning
// about select-list position with a lexer.
func containsSelectStar(sql string) bool { return strings.Contains(sql, "*") }

// cteNames collects the names bound by a WITH clause, so they are resolved
// rather than reported as tables.
//
// **It walks the WITH list and nothing else, and the first cut did not.** That
// version treated any `, <name> AS` as a CTE binding, which is also the shape
// of an aliased table in an old-style comma join: `FROM fact_sales AS a,
// salaries AS b` registered `salaries` as a CTE name and then filtered it out
// of the references entirely. The result was an excluded table read with no
// refusal and no uncertainty — the exact failure this package exists to make
// impossible, produced by the code meant to prevent a different one.
//
// So the walk is anchored: it starts only at a leading WITH, requires the
// `name AS (` shape, skips each body by balanced parentheses, and stops at the
// first thing that is not another binding.
func cteNames(words []string) map[string]bool {
	out := map[string]bool{}
	if len(words) == 0 || strings.ToLower(words[0]) != "with" {
		return out
	}
	i := 1
	if i < len(words) && strings.ToLower(words[i]) == "recursive" {
		i++
	}
	for i < len(words) {
		if !isIdentifierWord(words[i]) {
			return out
		}
		name := words[i]
		i++
		// An optional column list: `name (a, b) AS ( … )`.
		if i < len(words) && words[i] == "(" {
			end, ok := matchParen(words, i)
			if !ok {
				return out
			}
			i = end + 1
		}
		if i >= len(words) || strings.ToLower(words[i]) != "as" {
			return out
		}
		i++
		// Postgres allows MATERIALIZED / NOT MATERIALIZED between AS and the body.
		for i < len(words) {
			switch strings.ToLower(words[i]) {
			case "materialized", "not":
				i++
				continue
			}
			break
		}
		if i >= len(words) || words[i] != "(" {
			return out
		}
		end, ok := matchParen(words, i)
		if !ok {
			return out
		}
		if n := normalizeSQLIdentifier(name); n != "" {
			out[n] = true
		}
		i = end + 1
		if i >= len(words) || words[i] != "," {
			return out
		}
		i++
	}
	return out
}

// clauseKeywords end a FROM list. Reaching one means the comma-walk below has
// run out of table references and the rest of the statement is something else.
var clauseKeywords = map[string]bool{
	"where": true, "group": true, "order": true, "having": true,
	"limit": true, "offset": true, "fetch": true, "window": true,
	"union": true, "intersect": true, "except": true,
	"join": true, "inner": true, "left": true, "right": true,
	"full": true, "cross": true, "outer": true, "natural": true,
	"on": true, "using": true, "select": true, "for": true,
}

// tableRefsAfter reads the table reference (or, when list is true, the
// comma-separated list of them) that follows words[i].
//
// A `(` in table position is a derived table or a subquery. Its own FROM is
// later in the word list and the caller's loop reaches it independently, so it
// is skipped here rather than reported — and skipped by *balancing* the
// parentheses, because the comma that separates `FROM (…), other_table` is
// outside them while the commas inside a subquery's select list are not.
//
// ok is false only when a name could not be read at all. Everything this
// returns is a name it is confident about; everything it is not confident about
// makes the whole statement uncertain, which is the direction that fails safe.
func tableRefsAfter(words []string, i int, list bool) ([]tableRef, bool) {
	var out []tableRef
	pos := i + 1
	for {
		// ONLY and LATERAL sit between the keyword and the name.
		for pos < len(words) {
			switch strings.ToLower(words[pos]) {
			case "only", "lateral":
				pos++
				continue
			}
			break
		}
		if pos >= len(words) {
			return nil, false
		}

		cur := tableRef{}
		switch {
		case words[pos] == "(":
			end, ok := matchParen(words, pos)
			if !ok {
				return nil, false
			}
			pos = end + 1
			// A derived table. Its own FROM is read by the caller's loop, so
			// the tables underneath it are still found; what is lost is which
			// of them any `alias.column` belongs to. Recorded rather than
			// dropped so the column check can refuse instead of guess.
			cur.derived = true
		case isIdentifierWord(words[pos]) && !clauseKeywords[strings.ToLower(words[pos])]:
			cur.name = words[pos]
			pos++
		default:
			// Punctuation or a clause keyword where a table name belongs. If
			// nothing has been read yet this is unreadable; if something has,
			// the list simply ended.
			if len(out) == 0 {
				return nil, false
			}
			return out, true
		}

		// An alias, with or without AS.
		if pos < len(words) && strings.ToLower(words[pos]) == "as" {
			pos++
			if pos < len(words) && isIdentifierWord(words[pos]) {
				cur.alias = words[pos]
				pos++
			}
		} else if pos < len(words) && isIdentifierWord(words[pos]) && !clauseKeywords[strings.ToLower(words[pos])] {
			cur.alias = words[pos]
			pos++
		}
		out = append(out, cur)

		if !list || pos >= len(words) || words[pos] != "," {
			return out, true
		}
		pos++ // past the comma, round again
	}
}

// tableRef is one entry in a FROM list: the table, whatever it was aliased to,
// and whether it was a derived table rather than a name.
type tableRef struct {
	name    string
	alias   string
	derived bool
}

// matchParen returns the index of the `)` closing the `(` at open.
func matchParen(words []string, open int) (int, bool) {
	depth := 0
	for j := open; j < len(words); j++ {
		switch words[j] {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				return j, true
			}
		}
	}
	return 0, false
}

// isIdentifierWord reports whether a word can be a table name. Quoted forms
// count; punctuation does not.
func isIdentifierWord(w string) bool {
	if w == "" {
		return false
	}
	switch w[0] {
	case '(', ')', ',', ';', '*', '=', '<', '>', '+', '-', '/':
		return false
	}
	return true
}

// splitSQLWords tokenises into words, quoted identifiers and single-character
// punctuation. Whitespace is dropped.
//
// Punctuation is emitted as its own token because the grammar this reads
// depends on it: `FROM (` is a subquery and `FROM x` is a table, and a splitter
// that dropped the parenthesis could not tell them apart.
func splitSQLWords(sql string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(sql); i++ {
		ch := sql[i]
		switch {
		case ch == '"' || ch == '`' || ch == '[':
			// A quoted identifier is one token including its quotes, so a name
			// with a space in it does not become two words — which would make
			// `FROM "my table"` read as a table called `"my` .
			//
			// **Appended to the current token rather than emitted as its own**,
			// which is what makes `"public"."fact_sales"` one word. Emitting it
			// separately split that into `"public"`, `.`, `"fact_sales"`, and
			// the table-name reader took the first — so a fully-quoted
			// schema-qualified name was checked as the *schema*. Found by
			// TestTheTwoNormalisersAgree in internal/tools, which exists
			// because the two sides of this comparison live in two packages.
			closing := byte('"')
			if ch == '`' {
				closing = '`'
			} else if ch == '[' {
				closing = ']'
			}
			j := i + 1
			for j < len(sql) && sql[j] != closing {
				j++
			}
			if j < len(sql) {
				cur.WriteString(sql[i : j+1])
				i = j
			} else {
				cur.WriteString(sql[i:])
				i = len(sql)
			}
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r':
			flush()
		case ch == '(' || ch == ')' || ch == ',' || ch == ';' || ch == '*':
			flush()
			out = append(out, string(ch))
		default:
			cur.WriteByte(ch)
		}
	}
	flush()
	return out
}

// normalizeSQLIdentifier lowercases and strips quoting and schema
// qualification. It mirrors domain.normalizeIdentifier deliberately: the
// allowlist entry an admin typed and the reference this lexer extracted have to
// normalise the same way or the comparison is meaningless. They are separate
// functions because this package must not import the domain, and
// `TestNormalizationMatchesTheDomain` in internal/tools pins them together.
func normalizeSQLIdentifier(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "`\"[]")
	if i := strings.LastIndex(s, "."); i >= 0 {
		s = s[i+1:]
	}
	s = strings.Trim(s, "`\"[]")
	return strings.ToLower(strings.TrimSpace(s))
}
