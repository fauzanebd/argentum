package app

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ToolDigest is what one tool call did, small enough to carry into the next
// turn (T-Q6).
//
// **The gap it closes.** `domain.MessageRoleTool` has existed since the
// threading migration and was written by nothing — one grep, one hit, the
// declaration. `ChatRunner.completeWith` appends the assistant's prose and
// nothing else, so the only trace a tool call left was an `agent_actions` audit
// row the agent never reads. A follow-up turn therefore began knowing what was
// *said* and nothing about what was *done*: it re-ran `get_schema`, re-derived
// the SQL from scratch, and could derive it differently — the same question,
// twice, with two answers and no way for anyone to see why.
//
// **Why a digest and not the result.** A `run_sql` result is up to a hundred
// rows and the schema results run to tens of thousands of tokens; replaying
// them would spend the follow-up turn's whole context on the previous turn's
// output. What the next turn needs is the *shape* — which source, which query,
// how many rows came back, which columns — because that is what it would
// otherwise pay a `get_schema` round trip to rediscover.
type ToolDigest struct {
	Tool string `json:"tool"`
	// SourceID is which warehouse the call addressed, when it addressed one.
	SourceID string `json:"source_id,omitempty"`
	// Query is the SQL as executed. The single most valuable field here: a
	// follow-up that can read the previous SELECT can extend it instead of
	// rebuilding it, which is the difference between one tool call and three.
	Query string `json:"query,omitempty"`
	// MetricKey is set for query_metric, where there is no SQL to carry.
	MetricKey string `json:"metric_key,omitempty"`
	// Tables is what get_schema was asked about, or what it returned. It is
	// how a second turn knows the schema has already been read.
	Tables []string `json:"tables,omitempty"`
	// Columns is the shape of the result set, which is what makes a follow-up
	// able to say "group that by channel" without looking anything up.
	Columns []string `json:"columns,omitempty"`
	// Rows is the row count the result carried, -1 when it carried none.
	Rows int `json:"rows"`
	// Err is set when the call failed. Carried forward deliberately: a
	// follow-up that repeats a query which already failed is the second most
	// expensive thing this agent does.
	Err string `json:"error,omitempty"`
}

// digestMaxQueryChars bounds the SQL carried into the next turn. Long enough
// for a real analytical query with a couple of joins; short enough that ten of
// them do not become the context.
const digestMaxQueryChars = 600

// maxDigestsPerTurn bounds how many calls one turn contributes. A turn is
// capped at its iteration budget anyway, so this only bites on a document turn
// with the reserve — and a digest list longer than the answer it explains is
// not memory, it is noise.
const maxDigestsPerTurn = 12

// BuildToolDigest reads one tool call's arguments and result into a digest.
// Both are the JSON the stream carried; either being unparseable yields a
// digest with the tool name and nothing else, which is still worth keeping —
// "you already called get_schema" is most of the value.
func BuildToolDigest(name string, args, result map[string]interface{}) ToolDigest {
	d := ToolDigest{Tool: name, Rows: -1}

	d.SourceID = digestString(args, "source_id")
	d.MetricKey = digestString(args, "metric_key")
	if q := firstNonEmpty(digestString(args, "query"), digestString(args, "sql")); q != "" {
		d.Query = truncateQuery(q)
	}
	if t := digestStrings(args, "tables"); len(t) > 0 {
		d.Tables = t
	}

	if result == nil {
		return d
	}
	if e := firstNonEmpty(digestString(result, "error"), digestString(result, "err")); e != "" {
		d.Err = e
	}
	// Row count: the tools do not agree on a name for it, and the audit path
	// has the same problem (tools.resultRows). Both spellings are read rather
	// than one being made canonical, because changing a tool's output shape to
	// suit a digest would be the tail wagging the dog.
	for _, key := range []string{"row_count", "rows_returned", "count"} {
		if n, ok := digestInt(result, key); ok {
			d.Rows = n
			break
		}
	}
	if d.Rows < 0 {
		if rows, ok := result["rows"].([]interface{}); ok {
			d.Rows = len(rows)
		}
	}
	if c := digestStrings(result, "columns"); len(c) > 0 {
		d.Columns = c
	}
	// get_schema answers with the tables it found, which is more useful than
	// the tables it was asked for — the second turn wants to know what exists.
	if t := digestStrings(result, "tables"); len(t) > 0 {
		d.Tables = t
	}
	return d
}

// RenderPriorWork turns a thread's digests into the context block a turn
// reads, or "" when there is nothing worth saying.
//
// Composed like withSourcesContext and the table-picker hint rather than
// replayed into the SDK's memory as `role: tool` messages. That is not a
// stylistic choice: a provider's tool-result message is only valid immediately
// after the assistant message whose tool_call_id it answers, and a synthesised
// one from a previous turn has no such id. Anthropic and OpenAI both reject
// the sequence. A context block has no protocol to violate.
func RenderPriorWork(digests []ToolDigest) string {
	if len(digests) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[System context: Work already done earlier in THIS conversation. ")
	b.WriteString("Reuse it — do not re-read a schema you have read or re-derive a query you have run. ")
	b.WriteString("To extend a previous answer, build on the query below rather than starting again. ")
	b.WriteString("These results are from earlier turns: if the user asks for the same figure again, ")
	b.WriteString("re-run the query rather than quoting a number from here as if it were fresh.\n")

	for _, d := range digests {
		b.WriteString(" - ")
		b.WriteString(d.Tool)
		if d.SourceID != "" {
			fmt.Fprintf(&b, " on %s", d.SourceID)
		}
		if d.MetricKey != "" {
			fmt.Fprintf(&b, " (%s)", d.MetricKey)
		}
		switch {
		case d.Err != "":
			fmt.Fprintf(&b, " — FAILED: %s", oneLineDigest(d.Err))
		case d.Rows >= 0:
			fmt.Fprintf(&b, " — %d row(s)", d.Rows)
		}
		if len(d.Columns) > 0 {
			fmt.Fprintf(&b, ", columns: %s", strings.Join(d.Columns, ", "))
		}
		if len(d.Tables) > 0 {
			fmt.Fprintf(&b, ", tables: %s", strings.Join(d.Tables, ", "))
		}
		if d.Query != "" {
			fmt.Fprintf(&b, "\n   SQL: %s", oneLineDigest(d.Query))
		}
		b.WriteString("\n")
	}
	b.WriteString("]\n\n")
	return b.String()
}

// DedupeDigests collapses repeats, keeping the first of each and preserving
// order.
//
// A turn calls get_schema on the same source twice more often than it should,
// and carrying both into the next turn would spend the context saying one
// thing twice. Keyed on everything that makes a call distinguishable — two
// different SELECTs against one source are two facts, not one.
func DedupeDigests(in []ToolDigest) []ToolDigest {
	seen := make(map[string]bool, len(in))
	out := make([]ToolDigest, 0, len(in))
	for _, d := range in {
		key := strings.Join([]string{d.Tool, d.SourceID, d.MetricKey, d.Query,
			strings.Join(d.Tables, "|")}, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, d)
	}
	if len(out) > maxDigestsPerTurn {
		out = out[:maxDigestsPerTurn]
	}
	return out
}

// EncodeDigests serialises a turn's digests for the `role: tool` message row.
func EncodeDigests(in []ToolDigest) string {
	raw, err := json.Marshal(in)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// DecodeDigests reads them back. A row that does not parse yields nothing
// rather than an error: the turn it belongs to is over, and a malformed digest
// must not be able to stop the next turn from running.
func DecodeDigests(raw string) []ToolDigest {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []ToolDigest
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func digestString(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return strings.TrimSpace(s)
}

func digestInt(m map[string]interface{}, key string) (int, bool) {
	if m == nil {
		return 0, false
	}
	switch n := m[key].(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}

// digestStrings reads a list of names, accepting both a JSON array of strings
// and an array of objects carrying a "name" or "table" field — which is what
// get_schema returns for its tables.
func digestStrings(m map[string]interface{}, key string) []string {
	if m == nil {
		return nil
	}
	raw, ok := m[key].([]interface{})
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		var name string
		switch v := item.(type) {
		case string:
			name = v
		case map[string]interface{}:
			name = firstNonEmpty(digestString(v, "name"), digestString(v, "table"),
				digestString(v, "table_name"), digestString(v, "column"))
		}
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	// Sorted so a re-run produces a byte-identical digest. Two turns whose
	// digests differ only by map iteration order would look like a change.
	sort.Strings(out)
	// A wide table has hundreds of columns and a warehouse has hundreds of
	// tables. The point is "you have seen these", not an inventory.
	const maxNames = 40
	if len(out) > maxNames {
		out = append(out[:maxNames:maxNames], fmt.Sprintf("…and %d more", len(out)-maxNames))
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncateQuery(q string) string {
	q = strings.TrimSpace(q)
	if len(q) <= digestMaxQueryChars {
		return q
	}
	return q[:digestMaxQueryChars] + " …"
}

func oneLineDigest(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}
