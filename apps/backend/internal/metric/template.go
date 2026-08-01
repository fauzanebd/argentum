// Package metric holds the pure logic behind the metric registry (T-06/T-07):
// how a metric's SQL template is validated and rendered with a bound window, and
// how a comparison window is derived. It touches no database — the service in
// internal/app wires it to a tenant connection — so the injection-safety
// property and the window arithmetic are testable without one.
package metric

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// tokenRe matches a `{{name}}` placeholder. Only {{from}} and {{to}} are legal;
// Render rejects anything else so a template cannot smuggle in a token that
// resolves to nothing (and therefore to empty text) at run time.
var tokenRe = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}`)

// Render turns a stored template into executable SQL plus the ordered arguments
// its placeholders bind to (T-06).
//
// The window bounds are **never interpolated into the SQL text** — each
// {{from}}/{{to}} occurrence becomes one dialect placeholder and appends its
// value to args, left to right. That is the whole injection defence: a
// `'; DROP TABLE …` passed as a window value is a timestamp the driver escapes,
// not SQL it parses. One placeholder per occurrence (rather than reusing a
// marker) so every dialect works the same, including MySQL's positional `?`.
func Render(template string, placeholder func(n int) string, from, to time.Time) (string, []any, error) {
	var b strings.Builder
	var args []any
	last, n := 0, 0
	sawFrom, sawTo := false, false

	for _, m := range tokenRe.FindAllStringSubmatchIndex(template, -1) {
		b.WriteString(template[last:m[0]])
		name := template[m[2]:m[3]]
		var val any
		switch name {
		case "from":
			val, sawFrom = from, true
		case "to":
			val, sawTo = to, true
		default:
			return "", nil, fmt.Errorf("unknown template parameter {{%s}} — only {{from}} and {{to}} are allowed", name)
		}
		n++
		b.WriteString(placeholder(n))
		args = append(args, val)
		last = m[1]
	}
	b.WriteString(template[last:])

	if !sawFrom || !sawTo {
		return "", nil, fmt.Errorf("template must reference both {{from}} and {{to}}")
	}
	return b.String(), args, nil
}

// forbiddenStmt matches a mutating or multi-statement keyword used as a word.
// The read-only transaction the query runs in is the real guarantee — a
// mutation errors at the database regardless, and validate-on-save actually
// executes the template — so this is defence in depth and, more usefully, a
// specific error at save time rather than a runtime failure a tenant only hits
// when a watcher fires. `replace` is absent on purpose: REPLACE() is a common
// read-only string function, and the read-only tx catches the REPLACE statement
// anyway.
var forbiddenStmt = regexp.MustCompile(`(?i)\b(insert|update|delete|drop|alter|create|truncate|grant|revoke|merge|exec|execute|call|into|attach|copy|vacuum)\b`)

// ValidateTemplate accepts only a single SELECT that binds a window (T-06).
//
// It is deliberately strict: a metric is a definition an admin writes once and
// the agent runs unattended forever, so "anything but a single SELECT" is a
// rejected save, not a thing discovered when a watcher fires it at 3am.
func ValidateTemplate(template string) error {
	if !hasToken(template, "from") || !hasToken(template, "to") {
		return fmt.Errorf("the template must reference the window with both {{from}} and {{to}}")
	}

	// Scrub comments and string literals before the structural checks, so a
	// keyword or a semicolon inside a comment or a data value ('deleted',
	// 'a; b') does not read as SQL structure.
	scrubbed := stripStringLiterals(stripComments(template))
	trimmed := strings.TrimSpace(scrubbed)
	if trimmed == "" {
		return fmt.Errorf("the SQL template is empty")
	}

	// One statement. A trailing semicolon is allowed and stripped; any other is
	// a second statement and is refused.
	body := strings.TrimRight(trimmed, "; \t\r\n")
	if strings.Contains(body, ";") {
		return fmt.Errorf("the template must be a single statement — remove the extra %q", ";")
	}

	// A SELECT, or a WITH … SELECT (CTE). Nothing else starts a read.
	lower := strings.ToLower(body)
	if !strings.HasPrefix(lower, "select") && !strings.HasPrefix(lower, "with") {
		return fmt.Errorf("the template must be a SELECT (or a WITH … SELECT); it starts with something else")
	}

	if loc := forbiddenStmt.FindString(body); loc != "" {
		return fmt.Errorf("the template contains %q, which a read-only metric may not use", strings.ToUpper(loc))
	}
	return nil
}

// hasToken reports whether the template references {{name}}, tolerating inner
// whitespace like {{ from }}.
func hasToken(template, name string) bool {
	for _, m := range tokenRe.FindAllStringSubmatch(template, -1) {
		if m[1] == name {
			return true
		}
	}
	return false
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

// stripComments removes -- line comments and /* */ block comments so the
// keyword and semicolon checks read the SQL and not a comment that mentions
// "delete" or hides a ";".
func stripComments(sql string) string {
	// Block comments first, then line comments.
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
	for _, line := range strings.Split(sql, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}
