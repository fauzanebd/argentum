package domain

import (
	"fmt"
	"strings"
)

// The per-source table and column allowlist (T-H12).
//
// **What it is for.** "Can you restrict the agent to these twelve tables?" is a
// line item on every enterprise security questionnaire, and until this type
// existed the answer was "ask your DBA". Scoping was source-level only
// (`agentscope.Scope.AllowsSource`): a tenant could say *which database* an
// agent may reach and nothing about what inside it.
//
// **What it is not.** It is not the guarantee. A restricted login and masked
// views are the guarantee, they stay the recommendation, and nothing here is a
// reason to hand Argentum a wider account than it needs — the same sentence
// T-H4's `guardStatement` carries, for the same reason. What this buys is
// defence in depth over SQL a *model* wrote, plus the thing the questionnaire
// is actually asking about: the agent is never *told* the other tables exist.
//
// **Empty means unrestricted, and that is the only value an existing row can
// have.** Migration 068 adds these columns to every `db_connections` row; a
// tenant who has not set an allowlist must keep the behaviour they had
// yesterday, so absence cannot mean deny-all. The cost of that choice is stated
// where it matters: [Allowlist.Restricted] is what callers branch on, so "no
// restriction configured" and "restricted to nothing" are never the same
// question.

// Allowlist is what an agent may read inside one source.
type Allowlist struct {
	// Tables is the set of table names the agent may reference. Empty means
	// every table.
	Tables []string `json:"tables,omitempty"`
	// Columns restricts individual tables further: a table present here exposes
	// only the columns listed. A table absent from this map exposes all of its
	// columns, which is why an empty map is unrestricted rather than empty.
	//
	// Keys must also pass Tables when Tables is non-empty; a column rule on a
	// table the allowlist excludes is dead configuration, and
	// [Allowlist.Validate] refuses it rather than storing something that reads
	// as a permission and grants nothing.
	Columns map[string][]string `json:"columns,omitempty"`
}

// Restricted reports whether this allowlist restricts anything at all. The
// branch every caller needs, and the reason it is a method: `len(a.Tables) == 0`
// scattered across four packages is four chances to get the empty case
// backwards, and getting it backwards in the safe direction breaks every
// existing tenant while getting it backwards in the other direction silently
// enforces nothing.
func (a Allowlist) Restricted() bool {
	return len(a.Tables) > 0 || len(a.Columns) > 0
}

// AllowsTable reports whether the agent may reference this table.
//
// Case-insensitive, because SQL identifiers are: an admin who types
// `Fact_Sales` into the settings form and a model that writes `fact_sales` are
// naming the same table, and a check that disagreed would refuse correct SQL
// for a reason nobody could see. Schema qualification is stripped for the same
// reason — `public.fact_sales` and `fact_sales` are one table, and an allowlist
// that could be evaded by prefixing the default schema would not be one.
func (a Allowlist) AllowsTable(name string) bool {
	if len(a.Tables) == 0 {
		return true
	}
	name = normalizeIdentifier(name)
	for _, t := range a.Tables {
		if normalizeIdentifier(t) == name {
			return true
		}
	}
	return false
}

// AllowsColumn reports whether the agent may read this column of this table.
//
// A table with no entry in Columns exposes everything it has. A table with an
// entry exposes exactly what is listed — so removing a name from that list is
// what hides it, and an empty list hides the whole table's columns, which
// [Allowlist.Validate] refuses as a configuration that means "allowed, but
// unreadable".
func (a Allowlist) AllowsColumn(table, column string) bool {
	if !a.AllowsTable(table) {
		return false
	}
	cols, restricted := a.columnsFor(table)
	if !restricted {
		return true
	}
	column = normalizeIdentifier(column)
	for _, c := range cols {
		if normalizeIdentifier(c) == column {
			return true
		}
	}
	return false
}

// ColumnsRestricted reports whether this table exposes fewer than all of its
// columns.
//
// It exists for the one check that cannot be phrased as "is this column
// allowed": `SELECT *` names no column and expands, at the database, to every
// one of them. A star against a column-restricted table has to be refused
// rather than inspected, and the caller needs to know the table is restricted
// without having a column to ask about.
func (a Allowlist) ColumnsRestricted(table string) bool {
	_, restricted := a.columnsFor(table)
	return restricted
}

func (a Allowlist) columnsFor(table string) ([]string, bool) {
	if len(a.Columns) == 0 {
		return nil, false
	}
	table = normalizeIdentifier(table)
	for name, cols := range a.Columns {
		if normalizeIdentifier(name) == table {
			return cols, true
		}
	}
	return nil, false
}

// Validate rejects an allowlist that cannot mean what it says.
//
// Every rule here refuses configuration that reads as a permission and grants
// nothing. Stored silently, each one produces the same support ticket: the
// admin restricted the agent, the agent still cannot answer, and the settings
// page shows exactly what they intended.
func (a Allowlist) Validate() error {
	seen := make(map[string]bool, len(a.Tables))
	for _, t := range a.Tables {
		n := normalizeIdentifier(t)
		if n == "" {
			return wrapInvalid("an allowlisted table name is empty")
		}
		if seen[n] {
			return wrapInvalid("table %q is listed twice", t)
		}
		seen[n] = true
	}
	for table, cols := range a.Columns {
		n := normalizeIdentifier(table)
		if n == "" {
			return wrapInvalid("a column rule has an empty table name")
		}
		// A column rule on an excluded table is dead configuration.
		if len(a.Tables) > 0 && !seen[n] {
			return wrapInvalid("table %q has a column rule but is not in the table allowlist", table)
		}
		if len(cols) == 0 {
			return wrapInvalid("table %q allows no columns; remove it from the table allowlist instead of allowing it with nothing readable", table)
		}
		colSeen := make(map[string]bool, len(cols))
		for _, c := range cols {
			cn := normalizeIdentifier(c)
			if cn == "" {
				return wrapInvalid("table %q has an empty column name", table)
			}
			if colSeen[cn] {
				return wrapInvalid("table %q lists column %q twice", table, c)
			}
			colSeen[cn] = true
		}
	}
	return nil
}

// wrapInvalid keeps every refusal above comparable with errors.Is, so a handler
// can turn the whole family into one 400 without matching on wording.
func wrapInvalid(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalidInput}, args...)...)
}

// normalizeIdentifier lowercases a SQL identifier and strips schema
// qualification and quoting, so `"Public"."Fact_Sales"` and `fact_sales` are
// one name.
//
// Only the last segment is kept. That is the right reading for an allowlist
// entry an admin typed, and it is also the conservative one for a reference
// extracted from model SQL: `other_schema.fact_sales` normalises to
// `fact_sales` and is therefore *checked* rather than waved through as an
// unrecognised name.
func normalizeIdentifier(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "`\"[]")
	if i := strings.LastIndex(s, "."); i >= 0 {
		s = s[i+1:]
	}
	s = strings.Trim(s, "`\"[]")
	return strings.ToLower(strings.TrimSpace(s))
}
