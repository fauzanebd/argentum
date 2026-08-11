package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/adapters/db"
)

// probeEmptyResult answers "why did nothing match?" inside the call that
// matched nothing (T-Q9).
//
// **The failure it closes.** A query that succeeds and returns zero rows is
// the second fabrication mechanism this project has recorded (E-5), and the
// case that found it is exact: `dim_date.month_name` was seeded with
// `TO_CHAR(d, 'Month')`, which pads to nine characters, so the stored value was
// `'December '` and the obvious query — `month_name = 'December'` — returned
// nothing from a table holding 310 December transactions. Three months of
// demos, and nobody hit it. Handed that empty result, the agent reported
// **IDR 1,488,000**, a figure with no origin anywhere in the database.
//
// T-16 answered the fabrication half: the result now says in words that there
// is no figure in it, and the guardrail blocks a reply that states one anyway.
// What neither does is tell the agent — or the user — *what the values
// actually are*. So the honest answer the agent now gives is "no data matched,
// shall I check the available values?", which costs the user a round trip to
// say yes and the turn another tool call to do the thing it could have done
// here.
//
// **Why here and not as a prompt rule.** This is `explainSQLError`'s argument
// one branch over. That function exists because a wrong column name cost the
// turn two tool calls — one to fail, one to look up the right name — and it
// closed the loop inside the call the model already spent. A wrong *value* is
// the same shape: the driver knows nothing is wrong, the query ran, and the
// answer is one cheap SELECT DISTINCT away.
//
// It is best-effort by construction. A filter it cannot parse, a column it
// cannot place in a table, a probe that errors — each returns nothing extra,
// and the caller keeps the zero-row note it already had.
func probeEmptyResult(
	ctx context.Context, conn db.Conn, schema SchemaProvider,
	companyID, sourceID, dbType, sql string,
) []map[string]interface{} {
	if conn == nil || schema == nil {
		return nil
	}
	filters := parseEqualityFilters(sql)
	if len(filters) == 0 {
		return nil
	}
	meta, err := schema.FetchSchema(ctx, companyID, sourceID, false)
	if err != nil || meta == nil || len(meta.Tables) == 0 {
		return nil
	}

	out := make([]map[string]interface{}, 0, maxProbes)
	for _, f := range filters {
		if len(out) >= maxProbes {
			break
		}
		table, ok := tableHolding(meta, f.column)
		if !ok {
			continue
		}
		values, err := distinctValues(ctx, conn, dbType, table, f.column)
		if err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"company_id": companyID, "source_id": sourceID,
				"table": table, "column": f.column,
			}).Debug("empty-result probe failed; the reply keeps the plain zero-row note")
			continue
		}
		if len(values) == 0 {
			continue
		}
		out = append(out, map[string]interface{}{
			"column":           f.column,
			"table":            table,
			"you_filtered_for": f.value,
			"actual_values":    values,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// maxProbes bounds how many columns one empty result investigates. Two,
// because each is a round trip to the tenant's database on a path the user is
// already waiting on, and a query with more than two literal filters that
// matched nothing usually has one wrong one rather than three.
const maxProbes = 2

// maxProbeValues bounds the value list. Twenty is enough to show a padded
// label, a case mismatch or a spelling; a column with hundreds of distinct
// values is not one whose full list belongs in a prompt.
const maxProbeValues = 20

type equalityFilter struct {
	column string
	value  string
}

// literalPredicate matches `col = 'value'` and `alias.col = 'value'`, plus the
// same with LIKE and the first literal of an IN list.
//
// A regex rather than a parser, deliberately, and the same trade
// `parseNameError` makes: what a real parser would buy is the ability to
// handle predicates this one skips, and skipping them costs nothing — the
// caller keeps the plain zero-row note. What a parser would cost is a
// dependency and a class of disagreement with the database about what the
// query means.
var literalPredicate = regexp.MustCompile(
	`(?i)([a-z_][a-z0-9_]*)\s*(?:=|\bLIKE\b|\bIN\b\s*\()\s*'([^']*)'`)

// aliasQualified strips a table alias from a column reference: the probe needs
// the bare column name to find which table holds it.
var aliasQualified = regexp.MustCompile(`(?i)^[a-z_][a-z0-9_]*\.`)

// whereKeyword finds the start of the WHERE clause.
//
// A regex on word boundaries rather than `strings.Index(lower, " where ")`,
// which is what this was until 2026-08-11 and which required a literal SPACE
// on both sides. Models write multi-line SQL — the WHERE goes on its own line,
// preceded by a newline — so the index never matched, `parseEqualityFilters`
// returned nothing, and the whole T-Q9 probe was unreachable on every real
// query. Every unit test in this package used a single-line query, so the tests
// and the code agreed and both disagreed with production. Found by running the
// probe against the demo warehouse, not by reading it.
var whereKeyword = regexp.MustCompile(`(?is)\bwhere\b`)

func parseEqualityFilters(sql string) []equalityFilter {
	// Only the WHERE clause. A literal in a SELECT list or a JOIN condition is
	// not what the user filtered on, and probing it would answer a question
	// nobody asked.
	loc := whereKeyword.FindStringIndex(sql)
	if loc == nil {
		return nil
	}
	where := sql[loc[1]:]

	seen := map[string]bool{}
	var out []equalityFilter
	for _, m := range literalPredicate.FindAllStringSubmatch(where, -1) {
		col := aliasQualified.ReplaceAllString(strings.TrimSpace(m[1]), "")
		val := m[2]
		if col == "" || seen[strings.ToLower(col)] {
			continue
		}
		seen[strings.ToLower(col)] = true
		out = append(out, equalityFilter{column: col, value: val})
	}
	return out
}

// tableHolding finds which table carries a column. Ambiguity resolves to the
// first match, which is the honest answer: the probe's job is to show the
// values that exist, and a column name living in two tables usually means the
// same values in both.
func tableHolding(meta *db.SchemaMetadata, column string) (string, bool) {
	want := strings.ToLower(column)
	for _, t := range meta.Tables {
		for _, c := range t.Columns {
			if strings.ToLower(c.Name) == want {
				return t.Name, true
			}
		}
	}
	return "", false
}

// identifier accepts only a bare SQL name. Every value interpolated into the
// probe below is checked against it.
//
// The probe cannot use parameters: a placeholder can stand for a value, never
// for a table or a column, so the two identifiers have to be interpolated. They
// come from our own schema metadata rather than from the model — `tableHolding`
// returns a table this source reported and the column matched one it reported
// too — but "it came from a trusted place" is the argument every injection
// starts with, so the shape is checked at the point of use as well.
var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// distinctValues asks what the column actually holds.
//
// ORDER BY on the value, so the answer is stable between calls and a padded or
// case-shifted variant sorts next to the one the model tried.
func distinctValues(ctx context.Context, conn db.Conn, dbType, table, column string) ([]string, error) {
	if !identifier.MatchString(table) || !identifier.MatchString(column) {
		return nil, fmt.Errorf("refusing to probe a non-identifier: %q.%q", table, column)
	}

	var q string
	switch strings.ToLower(dbType) {
	case "sqlserver":
		q = fmt.Sprintf(
			"SELECT DISTINCT TOP %d %s FROM %s WHERE %s IS NOT NULL ORDER BY %s",
			maxProbeValues, column, table, column, column)
	default: // postgres, mysql
		q = fmt.Sprintf(
			"SELECT DISTINCT %s FROM %s WHERE %s IS NOT NULL ORDER BY %s LIMIT %d",
			column, table, column, column, maxProbeValues)
	}

	res, err := conn.ExecuteReadOnly(ctx, q, maxProbeValues)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(res.Rows))
	for _, row := range res.Rows {
		for _, v := range row {
			if v == nil {
				continue
			}
			// Quoted, always. The whole point of this probe is a value whose
			// stored form differs from what was typed by whitespace or case,
			// and `December ` is indistinguishable from `December` unquoted —
			// which is exactly how the bug survived three months of demos.
			out = append(out, fmt.Sprintf("%q", fmt.Sprint(v)))
			break
		}
	}
	return out, nil
}

// probeNote is what the payload says once a probe has found something. It
// replaces the plain zero-row note rather than joining it: two notes on one
// result is two instructions to reconcile, and this one contains the other's
// warning.
const probeNote = "The query succeeded but matched ZERO rows. There is no figure in this result. " +
	"The `available_values` field shows what the filtered columns ACTUALLY contain — compare it with " +
	"what you filtered for. Values are quoted so you can see leading or trailing spaces and case. " +
	"If one of them is what the user meant, re-run the query with it. If none is, tell the user no " +
	"data matched and show them the real values. Do NOT state a total, count or amount — there isn't one."

// attachProbe folds a probe's findings into a zero-row payload.
func attachProbe(payload map[string]interface{}, probes []map[string]interface{}) {
	if len(probes) == 0 {
		return
	}
	payload["available_values"] = probes
	payload["note"] = probeNote
}

// probeJSON is a test and logging convenience: the probe result as it appears
// in the payload.
func probeJSON(probes []map[string]interface{}) string {
	raw, err := json.Marshal(probes)
	if err != nil {
		return "[]"
	}
	return string(raw)
}
