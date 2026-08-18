package app

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/fauzanebd/argentum/internal/agentbudget"
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
	// Err is set when the call did not do its work — the error a failed call
	// returned, or the reason a refused one was never allowed to run. Carried
	// forward deliberately: a follow-up that repeats a query which already
	// failed is the second most expensive thing this agent does.
	Err string `json:"error,omitempty"`
	// Status is what the executor actually returned, and it exists because Err
	// being empty used to mean two different things — "the call ran" and "we
	// could not tell" (T-Q12). The second one is what let a turn believe an
	// edit had happened: a call refused by agentbudget comes back as a result
	// with no `error` key, so the digest recorded it exactly as it records a
	// success, and the next turn read "update_dashboard already ran".
	//
	// Empty on a successful call and on every row written before this field
	// existed, so a stored digest is byte-identical to what it was; read it
	// through Outcome rather than directly.
	Status string `json:"status,omitempty"`
}

// The three outcomes a call can have. "ok" is never written to the wire — see
// ToolDigest.Status — but it is what Outcome answers, because a caller asking
// "what happened?" should not have to know that one of the three is spelled as
// an absence.
const (
	DigestStatusOK      = "ok"
	DigestStatusFailed  = "failed"
	DigestStatusRefused = "refused"
)

// Outcome is the three-state answer: ok, failed, or refused.
//
// A digest written before Status existed carries only Err, so an error there
// still reads as failed — the one direction that never had the ambiguity.
func (d ToolDigest) Outcome() string {
	switch d.Status {
	case DigestStatusRefused, DigestStatusFailed:
		return d.Status
	}
	if d.Err != "" {
		return DigestStatusFailed
	}
	return DigestStatusOK
}

// Ran reports whether the call reached the tool at all. A refused call did
// not, which is the distinction the whole field exists for.
func (d ToolDigest) Ran() bool { return d.Outcome() != DigestStatusRefused }

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
//
// args and result are the JSON the stream carried; either being unparseable
// yields a digest with the tool name and nothing else, which is still worth
// keeping — "you already called get_schema" is most of the value.
//
// rawResult is what the stream carried *before* it was parsed, and it is the
// only place a Go error survives (T-Q12): agent-sdk-go renders one as the
// plain string "Error executing tool: …" (`pkg/llm/openai/streaming.go`,
// `Error: …` on the Anthropic path), which is not a JSON object, so the parsed
// map is empty and the call would otherwise be indistinguishable from one that
// ran and said nothing. Pass "" when there is no raw form to hand.
func BuildToolDigest(name string, args, result map[string]interface{}, rawResult string) ToolDigest {
	d := ToolDigest{Tool: name, Rows: -1}

	d.SourceID = digestString(args, "source_id")
	d.MetricKey = digestString(args, "metric_key")
	if q := firstNonEmpty(digestString(args, "query"), digestString(args, "sql")); q != "" {
		d.Query = truncateQuery(q)
	}
	if t := digestStrings(args, "tables"); len(t) > 0 {
		d.Tables = t
	}

	if len(result) == 0 {
		d.applyRawFailure(rawResult)
		return d
	}
	// A refused call first, before any of the shapes below: agentbudget's
	// refusal is a well-formed result carrying no `error` key at all, so every
	// other branch here reads it as an ordinary call. That is the whole of
	// T-Q12 — a call that never ran, remembered as one that did, and a turn
	// telling the user an edit was done because of it.
	if agentbudget.IsRefusalPayload(result) {
		d.Status = DigestStatusRefused
		d.Err = firstNonEmpty(agentbudget.RefusalReason(result), "the turn's budget was spent")
		return d
	}
	if e := firstNonEmpty(digestString(result, "error"), digestString(result, "err")); e != "" {
		d.Err = truncateDigestErr(e)
		d.Status = DigestStatusFailed
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
	b.WriteString("re-run the query rather than quoting a number from here as if it were fresh. ")
	// The sentence T-Q12 was written for. A list of calls with no outcome
	// beside them is an invitation to assume they worked, and two consecutive
	// turns told a user their dashboard had been edited on exactly that
	// assumption. A refused call is unfinished work: it is here so the turn
	// does not repeat the discovery that led to it, not so it can be counted.
	b.WriteString("A line marked REFUSED or FAILED did NOT take effect — if that work is still ")
	b.WriteString("wanted, DO IT NOW in this turn by calling the tool again; never report it as done.\n")

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
		case d.Outcome() == DigestStatusRefused:
			fmt.Fprintf(&b, " — REFUSED, it did NOT run: %s", oneLineDigest(d.Err))
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
//
// The outcome is part of the key (T-Q12). A call that was refused and then
// made properly is two facts as well, and collapsing them keeps whichever came
// first — which would have been the refusal, so the successful retry would
// disappear and the turn after it would read the work as never done.
func DedupeDigests(in []ToolDigest) []ToolDigest {
	seen := make(map[string]bool, len(in))
	out := make([]ToolDigest, 0, len(in))
	for _, d := range in {
		key := strings.Join([]string{d.Tool, d.SourceID, d.MetricKey, d.Query,
			strings.Join(d.Tables, "|"), d.Outcome()}, "\x00")
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

// applyRawFailure reads a result the stream carried as a plain string. Only
// the SDK's own error rendering counts; anything else leaves the digest as it
// was, which is what a call with an unreadable result has always looked like.
//
// The prefix table itself moved to agentbudget under T-Q13, where the turn's
// success tracking needs the same answer. Two copies of "what does a failed
// tool call look like" would be two answers, and the one that drifts is
// whichever sits on the less-exercised path.
func (d *ToolDigest) applyRawFailure(raw string) {
	if msg, failed := agentbudget.ToolErrorText(raw); failed {
		d.Err = truncateDigestErr(msg)
		d.Status = DigestStatusFailed
	}
}

// digestMaxErrChars bounds the failure text. A driver error carrying a whole
// query plan is a paragraph, and a paragraph per failed call would make the
// memory block longer than the answer it explains.
const digestMaxErrChars = 300

func truncateDigestErr(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= digestMaxErrChars {
		return s
	}
	return s[:digestMaxErrChars] + " …"
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
