package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/fauzanebd/argentum/internal/adapters/db"
)

// SchemaProvider hands a tool the schema of one of the tenant's sources.
// *GetSchemaTool satisfies it already, and passing that instance is the point:
// the answer comes off the same two-level cache get_schema serves the agent
// from, so the recovery hint below costs a map lookup rather than another
// introspection of the tenant's database.
type SchemaProvider interface {
	FetchSchema(ctx context.Context, companyID, sourceID string, force bool) (*db.SchemaMetadata, error)
}

// explainSQLError turns a driver's name error into an answer.
//
// Why this exists. A wrong column name used to cost the turn two tool calls:
// one for the query that failed with nothing but the driver's complaint, and
// one for the get_schema call the model then made to find out what it should
// have written. On a twelve-call budget, three bad guesses is half the turn —
// and the observed failure was exactly that. A live report request spent its
// budget on `JLS_SJLH_QRIS` and `JLS_SJLH_TRANSFER`, columns that do not
// exist, and finished without the PDF it was asked for.
//
// The driver already knows the name is wrong. This source already knows what
// the right names are. Answering with both closes the loop inside the one call
// the model already spent.
//
// It stays an error rather than becoming a result: a query that did not run is
// not evidence, agentbudget.Observe has to count it as a failure, and the audit
// log has to record it as one. The text still reaches the model — the provider
// feeds a tool error back into the conversation as `Error executing tool: ...`
// (agent-sdk-go pkg/llm/openai/streaming.go) — which is the whole delivery
// mechanism this depends on.
func explainSQLError(
	ctx context.Context, schema SchemaProvider, companyID, sourceID, sql string, cause error,
) error {
	base := fmt.Errorf("query execution failed: %w", cause)
	if schema == nil {
		return base
	}
	kind, name, ok := parseNameError(cause.Error())
	if !ok {
		// Every other failure — a syntax error, a timeout, a permission — is
		// left exactly as it was. A column list answers none of them, and
		// spending context on one would make the real cause harder to read.
		return base
	}
	// Never force: a cache miss here would introspect the tenant's database on
	// the failure path of a query that already cost them a round trip, and a
	// stale-by-an-hour column list is still the answer to a name they invented.
	meta, err := schema.FetchSchema(ctx, companyID, sourceID, false)
	if err != nil || meta == nil || len(meta.Tables) == 0 {
		return base
	}

	var hint string
	if kind == nameErrorTable {
		hint = tableHint(meta, name)
	} else {
		hint = columnHint(meta, sql, name)
	}
	if hint == "" {
		return base
	}
	return fmt.Errorf("%w\n\n%s", base, hint)
}

type nameErrorKind int

const (
	nameErrorColumn nameErrorKind = iota
	nameErrorTable
)

// nameErrorMarkers are how the dialects this codebase supports say "that
// identifier is not here" in one phrase. SQL Server says "object" where it
// means table.
var nameErrorMarkers = []struct {
	marker string
	kind   nameErrorKind
}{
	{"invalid column name", nameErrorColumn}, // SQL Server 207
	{"unknown column", nameErrorColumn},      // MySQL 1054
	{"no such column", nameErrorColumn},      // SQLite
	{"invalid object name", nameErrorTable},  // SQL Server 208
	{"unknown table", nameErrorTable},        // MySQL 1109
	{"no such table", nameErrorTable},        // SQLite
}

// existencePhrases are the dialects that split the claim in two — Postgres
// (`column "x" does not exist`, `relation "x" does not exist`) and MySQL's
// table form (`Table 'db.x' doesn't exist`). The kind then comes from the noun
// in the same message.
var existencePhrases = []string{"does not exist", "doesn't exist"}

// parseNameError reports whether a driver error is about an identifier that is
// not in the schema, and which identifier it named. The name is whatever the
// driver quoted: every dialect here quotes it, in one of four styles.
//
// Deliberately narrow. A hint attached to a syntax error, a timeout or a type
// conversion is context spent saying nothing — worse than the bare driver
// message, because it reads as if the name were the problem.
func parseNameError(msg string) (kind nameErrorKind, name string, ok bool) {
	lower := strings.ToLower(msg)
	found := false
	for _, m := range nameErrorMarkers {
		if strings.Contains(lower, m.marker) {
			kind, found = m.kind, true
			break
		}
	}
	if !found {
		for _, p := range existencePhrases {
			if !strings.Contains(lower, p) {
				continue
			}
			switch {
			case strings.Contains(lower, "column"):
				kind, found = nameErrorColumn, true
			case strings.Contains(lower, "relation"), strings.Contains(lower, "table"):
				kind, found = nameErrorTable, true
			}
			break
		}
	}
	if !found {
		return 0, "", false
	}
	name = quotedIdentifier(msg)
	if name == "" {
		return 0, "", false
	}
	return kind, name, true
}

// quotedIdentifier returns the first quoted run in a driver message. The
// closing quote is style-matched — `[x]` closes with `]`, the rest with
// themselves — so a message quoting two names still yields the first whole one.
func quotedIdentifier(msg string) string {
	closers := map[byte]byte{'\'': '\'', '"': '"', '`': '`', '[': ']'}
	for i := 0; i < len(msg); i++ {
		closer, isOpen := closers[msg[i]]
		if !isOpen {
			continue
		}
		if end := strings.IndexByte(msg[i+1:], closer); end > 0 {
			return bareName(msg[i+1 : i+1+end])
		}
	}
	return ""
}

// bareName strips the qualification and quoting a driver may carry into its
// message: `mydb.dbo.tbSales` and `[tbSales]` are both tbSales here, because
// what the caller matches against is a schema table name.
func bareName(s string) string {
	s = strings.Trim(s, "[]`\"' ")
	if i := strings.LastIndexByte(s, '.'); i >= 0 && i < len(s)-1 {
		s = s[i+1:]
	}
	return strings.Trim(s, "[]`\"' ")
}

// hintCaps bound what a hint may add to the turn's context. Generous against
// the alternative — two more tool calls, one of them a full get_schema — and
// still far short of a wide table's full description.
const (
	maxHintColumns = 200
	maxHintChars   = 6000
	maxSuggestions = 5
)

// columnHint answers a bad column name with the columns of the tables the query
// actually referenced. Scoped to those tables rather than the whole source:
// the model already chose where to look, and re-sending the schema it filtered
// down to is how this would become more expensive than the call it replaces.
func columnHint(meta *db.SchemaMetadata, sql, missing string) string {
	referenced := tablesInSQL(sql)
	var matched []db.TableInfo
	for _, t := range meta.Tables {
		if referenced[strings.ToLower(bareName(t.Name))] {
			matched = append(matched, t)
		}
	}
	if len(matched) == 0 {
		// The query named no table this source knows — usually an alias-only
		// reference or a CTE. Suggesting against the whole source is still
		// better than the bare driver error.
		return suggestionLine(missing, allColumnNames(meta.Tables))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%q is not a column of the table(s) this query reads. Do not guess another name — "+
		"these are the real columns:\n", missing)
	for _, t := range matched {
		names := make([]string, 0, len(t.Columns))
		for _, c := range t.Columns {
			names = append(names, c.Name)
		}
		fmt.Fprintf(&b, "  %s: %s\n", t.Name, joinCapped(names, maxHintColumns))
	}
	if line := suggestionLine(missing, allColumnNames(matched)); line != "" {
		b.WriteString(line)
	}
	b.WriteString("Rewrite the query against these columns and run it again. " +
		"If none of them holds what you need, say so rather than substituting one that looks close.")
	return truncateHint(b.String())
}

// tableHint answers a bad table name with the source's table names. Names
// only: a table that does not exist gives the model no reason to be handed
// every column of every table that does.
func tableHint(meta *db.SchemaMetadata, missing string) string {
	names := make([]string, 0, len(meta.Tables))
	for _, t := range meta.Tables {
		names = append(names, t.Name)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%q is not a table in this source.\n", missing)
	if line := suggestionLine(missing, names); line != "" {
		b.WriteString(line)
	}
	fmt.Fprintf(&b, "  all tables: %s\n", joinCapped(names, maxHintColumns))
	return truncateHint(b.String())
}

func allColumnNames(tables []db.TableInfo) []string {
	var out []string
	for _, t := range tables {
		for _, c := range t.Columns {
			out = append(out, c.Name)
		}
	}
	return out
}

// suggestionLine ranks candidates against the name the model got wrong. The
// observed guesses were near misses on a shared prefix (`JLS_SJLH_QRIS` beside
// real `JLS_SJLH_*` columns), so token overlap is what is scored rather than
// edit distance — it is the mistake this is answering.
func suggestionLine(missing string, candidates []string) string {
	type scored struct {
		name  string
		score int
	}
	want := nameTokens(missing)
	var ranked []scored
	for _, c := range candidates {
		if s := overlap(want, nameTokens(c)); s > 0 {
			ranked = append(ranked, scored{c, s})
		}
	}
	if len(ranked) == 0 {
		return ""
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	if len(ranked) > maxSuggestions {
		ranked = ranked[:maxSuggestions]
	}
	names := make([]string, 0, len(ranked))
	for _, r := range ranked {
		names = append(names, r.name)
	}
	return fmt.Sprintf("  closest to %q: %s\n", missing, strings.Join(names, ", "))
}

// nameTokens splits an identifier into lowercase alphanumeric runs, so
// JLS_SJLH_QRIS and jls-sjlh-qris compare equal token for token.
func nameTokens(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !isASCIIAlnum(r) })
}

func isASCIIAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

func overlap(want, got []string) int {
	if len(want) == 0 || len(got) == 0 {
		return 0
	}
	in := make(map[string]bool, len(got))
	for _, g := range got {
		in[g] = true
	}
	n := 0
	for _, w := range want {
		if in[w] {
			n++
		}
	}
	return n
}

// sqlTableKeywords introduce a table reference in the read-only SQL this tool
// admits. UPDATE and INTO are absent because a write never reaches here.
var sqlTableKeywords = map[string]bool{"from": true, "join": true}

// tablesInSQL returns the lowercased bare names the query reads from. A rough
// scan on purpose: it decides which columns to quote back, and the cost of
// missing a name is a less specific hint, not a wrong one.
func tablesInSQL(sql string) map[string]bool {
	out := map[string]bool{}
	fields := strings.FieldsFunc(sql, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ',' || r == '(' || r == ')' || r == ';'
	})
	for i, f := range fields {
		if !sqlTableKeywords[strings.ToLower(f)] || i+1 >= len(fields) {
			continue
		}
		if name := bareName(fields[i+1]); name != "" {
			out[strings.ToLower(name)] = true
		}
	}
	return out
}

func joinCapped(names []string, max int) string {
	if len(names) <= max {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s, … (+%d more, call get_schema for the rest)",
		strings.Join(names[:max], ", "), len(names)-max)
}

func truncateHint(s string) string {
	if len(s) <= maxHintChars {
		return s
	}
	return s[:maxHintChars] + "\n… (truncated; call get_schema for the full list)"
}
