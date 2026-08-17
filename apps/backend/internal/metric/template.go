// Package metric holds the pure logic behind the metric registry (T-06/T-07):
// how a metric's SQL template is validated and rendered with a bound window, and
// how a comparison window is derived. It touches no database — the service in
// internal/app wires it to a tenant connection — so the injection-safety
// property and the window arithmetic are testable without one.
package metric

import (
	"fmt"
	"strings"
	"time"

	"github.com/fauzanebd/argentum/internal/sqlguard"
)

// windowTokens is the whole token vocabulary a metric template may use: the two
// window bounds, both declared and both required. A metric that could reference
// a third token could reference one that resolves to nothing, and `WHERE d >= `
// is valid SQL that reads the whole table.
var windowTokens = []sqlguard.Token{"from", "to"}

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

	// The token grammar is sqlguard's, so what ValidateTemplate refused and what
	// this binds can never be two different sets.
	for _, ref := range sqlguard.FindTokens(template) {
		b.WriteString(template[last:ref.Start])
		var val any
		switch ref.Name {
		case "from":
			val, sawFrom = from, true
		case "to":
			val, sawTo = to, true
		default:
			return "", nil, fmt.Errorf("unknown template parameter {{%s}} — only {{from}} and {{to}} are allowed", ref.Name)
		}
		n++
		b.WriteString(placeholder(n))
		args = append(args, val)
		last = ref.End
	}
	b.WriteString(template[last:])

	if !sawFrom || !sawTo {
		return "", nil, fmt.Errorf("template must reference both {{from}} and {{to}}")
	}
	return b.String(), args, nil
}

// ValidateTemplate accepts only a single SELECT that binds a window (T-06).
//
// It is deliberately strict: a metric is a definition an admin writes once and
// the agent runs unattended forever, so "anything but a single SELECT" is a
// rejected save, not a thing discovered when a watcher fires it at 3am.
//
// The check itself moved to internal/sqlguard under T-H4 step 1, so the metric
// registry, a dashboard panel and run_sql all get the same answer — and all
// three upgrade together when the lexer becomes a parser. What is metric-
// specific is only the token set, which is why it is an argument now.
func ValidateTemplate(template string) error {
	return sqlguard.ValidateStatement(template, windowTokens, windowTokens...)
}
