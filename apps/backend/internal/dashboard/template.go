// Package dashboard binds a stored dashboard spec to a request: it coerces the
// filter values a viewer sent (or the defaults the spec carries) into bound
// query parameters, renders each panel's SQL with them, and runs the panels.
//
// The spec types themselves are in ./spec, which is pure — types, validation,
// projection and the window presets. This package is where a spec meets a clock
// and a tenant connection.
package dashboard

import (
	"fmt"
	"sort"
	"strings"

	"github.com/fauzanebd/argentum/internal/sqlguard"
)

// Render replaces each {{name}} in a panel's SQL with one dialect placeholder
// and appends its value to args, left to right (T-D3).
//
// The values are **never interpolated into the SQL text**. That is the whole
// injection defence and it is the same one metric.Render makes: a filter value a
// viewer chose — or typed into a query string, after T-D13 puts this behind a
// share link — is data the driver escapes, not SQL it parses. One placeholder
// per occurrence rather than reusing a marker, so MySQL's positional `?` needs
// no reuse and every dialect renders the same way.
//
// A token with no value in `values` is an error naming it, not an empty string.
// This matters more than the binding does: `WHERE tenant = ` is valid SQL that
// returns the whole table, and a filter that silently vanished is a panel that
// looks like it answered.
//
// There is no separate `declared` argument, because there is no second list to
// disagree with: which tokens a panel may reference is settled at save time by
// sqlguard.ValidateStatement against the spec's filters, and `values` is built
// from those same filters — every one of them, defaults included. A token
// missing here is therefore a bug in the caller, and it is reported rather than
// bound.
func Render(template string, placeholder func(n int) string, values map[string]any) (string, []any, error) {
	var b strings.Builder
	var args []any
	last, n := 0, 0

	for _, ref := range sqlguard.FindTokens(template) {
		val, ok := values[string(ref.Name)]
		if !ok {
			return "", nil, fmt.Errorf("no value for parameter {{%s}}%s", ref.Name, boundList(values))
		}
		b.WriteString(template[last:ref.Start])
		n++
		b.WriteString(placeholder(n))
		args = append(args, val)
		last = ref.End
	}
	b.WriteString(template[last:])
	return b.String(), args, nil
}

// boundList names the parameters that do have values, the repair-instruction
// shape internal/tools/sql_error_hint.go uses: an error that lists the
// alternatives can be acted on without a second round-trip.
func boundList(values map[string]any) string {
	if len(values) == 0 {
		return " — this dashboard binds no parameters"
	}
	names := make([]string, 0, len(values))
	for k := range values {
		names = append(names, "{{"+k+"}}")
	}
	sort.Strings(names) // map order would make the same error read differently twice
	return " — the bound parameters are " + strings.Join(names, ", ")
}
