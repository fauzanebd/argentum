// Package sqlguard holds the one structural check this product runs over SQL it
// did not write itself: a statement is a single read, it references only the
// tokens its caller declared, and it carries no mutating keyword.
//
// It exists as its own package because three callers need the same answer and
// the day there are two implementations is the day they drift — and the one that
// drifts is whichever sits on the least-exercised path. Today the callers are
// the metric registry (a template an admin saves and a watcher runs unattended),
// the dashboard spec (validated at save and again at resolve, because a stored
// spec is not trusted just because it passed once), and run_sql (T-H4 step 3).
//
// This is a lexer, not a parser: it scrubs comments and string literals and then
// reads structure off what is left. T-H4 step 2 replaces the body with a real
// parse — pg_query_go for Postgres, vitess for MySQL, this lexer kept as the SQL
// Server arm because no credible Go parser exists there. The signature is the
// part meant to survive that, so callers do not move twice.
//
// It was promoted here from metric.ValidateTemplate, which shipped under T-06
// and had nine live-gated refusals behind it (docs/coverage/metric-registry.md
// §4). The behaviour is unchanged except for the token rules, which the caller
// now supplies — see ValidateStatement.
package sqlguard

import (
	"fmt"
	"regexp"
	"strings"
)

// Token is the name inside a `{{name}}` placeholder. A metric declares from and
// to; a dashboard declares whatever its filters do; run_sql declares none.
type Token string

// TokenRef is one `{{name}}` occurrence and the byte range it spans, so a
// renderer can rewrite the statement without re-deriving the grammar. There is
// one grammar for tokens in this product and it lives here — a validator and a
// renderer that disagree about what a token looks like is a token that passes
// validation and is never bound.
type TokenRef struct {
	Name  Token
	Start int // index of the leading `{`
	End   int // index just past the trailing `}`
}

// tokenRe matches a `{{name}}` placeholder, tolerating inner whitespace.
var tokenRe = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}`)

// FindTokens returns every token the statement references, in order, including
// repeats.
//
// It reads the raw statement rather than the scrubbed one on purpose: a renderer
// substitutes `{{from}}` wherever it appears, including inside a string literal
// or a comment, so a validator that ignored those would bless a statement whose
// render does something else.
func FindTokens(sql string) []TokenRef {
	ms := tokenRe.FindAllStringSubmatchIndex(sql, -1)
	refs := make([]TokenRef, 0, len(ms))
	for _, m := range ms {
		refs = append(refs, TokenRef{Name: Token(sql[m[2]:m[3]]), Start: m[0], End: m[1]})
	}
	return refs
}

// forbiddenStmt matches a mutating or multi-statement keyword used as a word.
// The read-only transaction the query runs in is the real guarantee — a mutation
// errors at the database regardless — so this is defence in depth and, more
// usefully, a specific error at save time rather than a runtime failure a tenant
// only hits when a watcher fires at 3am. `replace` is absent on purpose:
// REPLACE() is a common read-only string function, and the read-only tx catches
// the REPLACE statement anyway.
var forbiddenStmt = regexp.MustCompile(`(?i)\b(insert|update|delete|drop|alter|create|truncate|grant|revoke|merge|exec|execute|call|into|attach|copy|vacuum)\b`)

// ValidateStatement accepts only a single SELECT (or WITH … SELECT) that
// references nothing but the tokens `declared`, and every token in `required`.
//
// The two token rules are separate because a filter is usually declared without
// being required: a dashboard's date range may appear in eleven panels and not
// in the twelfth. A metric passes the same set as both, which is what
// ValidateTemplate hardcoded before the promotion.
//
// Passing a nil `declared` means the statement may reference no token at all,
// which is the run_sql case: model-written SQL carrying a `{{token}}` has
// nothing to bind it and would otherwise reach the driver with the braces in it.
//
// An undeclared token is refused HERE rather than at render. The live gate on
// the metric registry found that gap the expensive way: presence was checked and
// absence was not, so an unknown token passed save and failed at render, turning
// a 400 an admin could fix into a 500 that reads like an outage
// (docs/coverage/metric-registry.md §4).
func ValidateStatement(sql string, declared []Token, required ...Token) error {
	allowed := make(map[Token]bool, len(declared))
	for _, t := range declared {
		allowed[t] = true
	}
	seen := make(map[Token]bool, len(declared))
	for _, ref := range FindTokens(sql) {
		if !allowed[ref.Name] {
			return fmt.Errorf("unknown parameter {{%s}}%s", ref.Name, wouldHaveWorked(declared))
		}
		seen[ref.Name] = true
	}
	for _, t := range required {
		if !seen[t] {
			return fmt.Errorf("the statement must reference {{%s}}", t)
		}
	}

	// Scrub comments and string literals before the structural checks, so a
	// keyword or a semicolon inside a comment or a data value ('deleted', 'a; b')
	// does not read as SQL structure.
	scrubbed := stripStringLiterals(stripComments(sql))
	trimmed := strings.TrimSpace(scrubbed)
	if trimmed == "" {
		return fmt.Errorf("the SQL statement is empty")
	}

	// One statement. A trailing semicolon is allowed and stripped; any other is a
	// second statement and is refused.
	body := strings.TrimRight(trimmed, "; \t\r\n")
	if strings.Contains(body, ";") {
		return fmt.Errorf("the statement must be a single statement — remove the extra %q", ";")
	}

	// A SELECT, or a WITH … SELECT (CTE). Nothing else starts a read.
	lower := strings.ToLower(body)
	if !strings.HasPrefix(lower, "select") && !strings.HasPrefix(lower, "with") {
		return fmt.Errorf("the statement must be a SELECT (or a WITH … SELECT); it starts with %s", leadingWord(body))
	}

	if kw := forbiddenStmt.FindString(body); kw != "" {
		return fmt.Errorf("the statement contains %q, which a read-only query may not use", strings.ToUpper(kw))
	}
	return nil
}

// leadingWord names the keyword a refused statement actually starts with, so
// the refusal is a thing the caller can act on rather than a restatement of the
// rule. "it starts with INSERT" tells a model which line to change; "it starts
// with something else", which this said until T-H4 step 3 went looking, tells
// it only that it was wrong.
//
// It falls back to the old phrasing when the statement opens with punctuation —
// `(SELECT …) UNION (SELECT …)` is the real example, and it is refused here for
// a reason that is about the prefix check and not about a keyword.
func leadingWord(body string) string {
	end := 0
	for end < len(body) {
		ch := body[end]
		isWord := ch == '_' ||
			(ch >= 'a' && ch <= 'z') ||
			(ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9')
		if !isWord {
			break
		}
		end++
	}
	if end == 0 {
		return "something else"
	}
	return strings.ToUpper(body[:end])
}

// wouldHaveWorked names the tokens the caller declared, the same repair-
// instruction shape internal/tools/sql_error_hint.go uses when a query fails:
// the error that names the alternatives is the one that does not need a second
// round-trip to act on.
func wouldHaveWorked(declared []Token) string {
	if len(declared) == 0 {
		return " — this statement declares no parameters"
	}
	names := make([]string, len(declared))
	for i, t := range declared {
		names[i] = "{{" + string(t) + "}}"
	}
	return " — the declared parameters are " + strings.Join(names, ", ")
}

// stripStringLiterals blanks the contents of single-quoted string literals,
// handling the doubled-quote escape ('it”s'). It leaves the quotes so the SQL
// shape is unchanged; only the data between them is removed.
func stripStringLiterals(sql string) string {
	var b strings.Builder
	inStr := false
	for i := 0; i < len(sql); i++ {
		ch := sql[i]
		if ch == '\'' {
			if inStr && i+1 < len(sql) && sql[i+1] == '\'' {
				// Escaped quote inside a literal: skip both, emit nothing.
				i++
				continue
			}
			b.WriteByte(ch)
			inStr = !inStr
			continue
		}
		if !inStr {
			b.WriteByte(ch)
		}
	}
	return b.String()
}

// stripComments removes -- line comments and /* */ block comments so the keyword
// and semicolon checks read the SQL and not a comment that mentions "delete" or
// hides a ";".
func stripComments(sql string) string {
	var b strings.Builder
	for {
		start := strings.Index(sql, "/*")
		if start < 0 {
			break
		}
		end := strings.Index(sql[start:], "*/")
		if end < 0 {
			sql = sql[:start] // unterminated: drop the rest
			break
		}
		sql = sql[:start] + " " + sql[start+end+2:]
	}
	for line := range strings.SplitSeq(sql, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}
