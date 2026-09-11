package tools

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/shopspring/decimal"

	"github.com/fauzanebd/argentum/internal/adapters/db"
)

// turnValues is what this turn's data tools returned, remembered so `compute`
// can bind its inputs to figures **this product produced** rather than to
// figures the model retyped into an argument (T-W1).
//
// That is the half of grounding a value-taking tool would otherwise lose. A
// compute tool that accepted `{"revenue": 3863405700}` would be exact
// arithmetic over a transcription, and transcription is precisely the error
// T-Q14 found: run_sql returned 3,863,405,700 and the reply said
// 3,860,405,700, which is 0.078% wrong and inside every tolerance this product
// owns. Binding by id means the digits never pass through the model at all.
//
// A pointer holder on the context, like turnSource above it, and for the same
// reason: the memory is written by one tool call and read by the next, and
// every tool in a turn shares the one context the runner built.
type turnValues struct {
	mu    sync.Mutex
	seq   int
	byID  map[string]*recordedResult
	order []string
}

// recordedResult is one result, addressable as `<id>.<field>`.
type recordedResult struct {
	tool string
	// fields maps a column name to the numeric cells of that column, in row
	// order. A NULL or non-numeric cell is absent rather than zero — the same
	// rule SQL's own SUM follows, and the only one that does not invent a
	// figure.
	fields map[string][]decimal.Decimal
	// names is fields' keys in the order the result declared them, so a listing
	// reads like the result rather than like a map.
	names []string
	rows  int
}

type turnValuesKey struct{}

// WithTurnValues installs an empty result memory on ctx.
//
// **Installed only for a turn that holds `compute`.** A context without one is
// not an error and not a special case: nothing is remembered, and — the point
// of the arrangement — run_sql and query_metric attach no `result_id` to their
// payloads, so a turn that cannot compute produces bytes identical to the ones
// it produced before this package existed. The same discipline attachFreshness
// follows for a source with no expression.
func WithTurnValues(ctx context.Context) context.Context {
	return context.WithValue(ctx, turnValuesKey{}, &turnValues{byID: map[string]*recordedResult{}})
}

func turnValuesFrom(ctx context.Context) *turnValues {
	t, _ := ctx.Value(turnValuesKey{}).(*turnValues)
	return t
}

// reserveResultID takes the next id for a result that is about to be built,
// without yet knowing what is in it. Returns "" when this turn has no memory
// installed, which every caller treats as "attach nothing".
//
// Two phases rather than one because run_sql trims rows off the tail of a
// payload until it fits the context budget, and what this turn can bind must be
// what the model was actually shown. The id has to exist *before* the trim (it
// is in the bytes the trim is measuring) and the contents can only be recorded
// *after* it.
func reserveResultID(ctx context.Context, tool string) string {
	t := turnValuesFrom(ctx)
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seq++
	id := "r" + strconv.Itoa(t.seq)
	t.byID[id] = &recordedResult{tool: tool, fields: map[string][]decimal.Decimal{}}
	t.order = append(t.order, id)
	return id
}

func (t *turnValues) fill(id string, names []string, fields map[string][]decimal.Decimal, rows int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	res, ok := t.byID[id]
	if !ok {
		return
	}
	res.fields, res.names, res.rows = fields, names, rows
}

// recordSQLResult files the numeric columns of a query result under an id
// reserved earlier. Called after the byte cap has trimmed the result, so the
// figures this turn can bind are exactly the figures the model was shown.
func recordSQLResult(ctx context.Context, id string, result *db.QueryResult) {
	t := turnValuesFrom(ctx)
	if t == nil || id == "" || result == nil {
		return
	}
	fields := map[string][]decimal.Decimal{}
	var names []string
	for _, col := range result.Columns {
		var vals []decimal.Decimal
		for _, row := range result.Rows {
			if d, ok := asDecimal(row[col]); ok {
				vals = append(vals, d)
			}
		}
		if len(vals) == 0 {
			// A text column is not bindable, and saying so by omission is
			// better than binding an empty list somebody can sum to zero.
			continue
		}
		fields[col] = vals
		names = append(names, col)
	}
	t.fill(id, names, fields, result.Count)
}

// recordPayloadFigures files the numeric fields of a flat tool payload —
// query_metric's `value`, its `delta` and its `delta_pct`. Only the keys asked
// for, because a payload also carries row counts and window boundaries, and a
// figure bound off `row_count` is a business figure with no business meaning.
func recordPayloadFigures(ctx context.Context, id string, payload map[string]interface{}, keys ...string) {
	t := turnValuesFrom(ctx)
	if t == nil || id == "" {
		return
	}
	fields := map[string][]decimal.Decimal{}
	var names []string
	for _, k := range keys {
		d, ok := asDecimal(payload[k])
		if !ok {
			continue
		}
		fields[k] = []decimal.Decimal{d}
		names = append(names, k)
	}
	t.fill(id, names, fields, 1)
}

// asDecimal converts one cell to an exact figure, and reports false for
// anything that is not a number — a NULL, a label, a date.
//
// A float64 is converted through its shortest decimal representation rather
// than through its binary expansion: a driver that rendered 0.1 as a float64
// meant 0.1, and decimal.NewFromFloat is the conversion that says so.
func asDecimal(v interface{}) (decimal.Decimal, bool) {
	switch n := v.(type) {
	case nil:
		return decimal.Zero, false
	case float64:
		return decimal.NewFromFloat(n), true
	case float32:
		return decimal.NewFromFloat32(n), true
	case int:
		return decimal.NewFromInt(int64(n)), true
	case int32:
		return decimal.NewFromInt(int64(n)), true
	case int64:
		return decimal.NewFromInt(n), true
	case string:
		// The shape every driver in this product uses for DECIMAL, which is
		// every money column in the demo warehouse.
		d, err := decimal.NewFromString(strings.TrimSpace(n))
		if err != nil {
			return decimal.Zero, false
		}
		return d, true
	case []byte:
		d, err := decimal.NewFromString(strings.TrimSpace(string(n)))
		if err != nil {
			return decimal.Zero, false
		}
		return d, true
	default:
		return decimal.Zero, false
	}
}

// boundValue is one resolved binding: the figures, and how to say in the
// payload where they came from.
type boundValue struct {
	// Column is true when the reference resolved to a whole column.
	Column bool
	Values []decimal.Decimal
}

// resolveRef resolves `<id>.<field>` or `<id>.<field>[<row>]` against this
// turn's memory.
//
// Every failure names what *is* available, because the model's next move after
// a rejected reference is to guess a second one, and a guess against a listed
// menu is a repair where a guess against a bare refusal is another round trip.
func resolveRef(ctx context.Context, ref string) (boundValue, error) {
	t := turnValuesFrom(ctx)
	if t == nil {
		// No memory at all, which is not the same as an empty one. It means
		// this caller is not a chat turn — the MCP server hands out one tool
		// per JSON-RPC call, so there is no "earlier call in this turn" for a
		// reference to point at — or the agent is scoped away from `compute`.
		// Either way the honest answer is that the tool cannot work here, not
		// that the reference was wrong.
		return boundValue{}, fmt.Errorf("this caller has no earlier tool results to bind to. `compute` works inside a conversation turn, where it can refer to what a previous call in the SAME turn returned")
	}
	ref = strings.TrimSpace(ref)
	id, field, ok := strings.Cut(ref, ".")
	if !ok || id == "" || field == "" {
		return boundValue{}, fmt.Errorf("%q is not a reference: write it as <result_id>.<column>, for example r1.revenue. %s", ref, t.describe())
	}

	var row = -1
	if open := strings.Index(field, "["); open >= 0 {
		if !strings.HasSuffix(field, "]") {
			return boundValue{}, fmt.Errorf("%q is missing its closing ']'", ref)
		}
		idx := field[open+1 : len(field)-1]
		n, err := strconv.Atoi(idx)
		if err != nil || n < 0 {
			return boundValue{}, fmt.Errorf("%q is not a row number in %q — rows are counted from 0", idx, ref)
		}
		row = n
		field = field[:open]
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	res, ok := t.byID[id]
	if !ok {
		return boundValue{}, fmt.Errorf("there is no result %q in this turn. %s", id, t.describeLocked())
	}
	vals, ok := res.fields[field]
	if !ok {
		return boundValue{}, fmt.Errorf("result %s has no numeric column %q. It has: %s", id, field, strings.Join(res.names, ", "))
	}
	if row >= 0 {
		if row >= len(vals) {
			return boundValue{}, fmt.Errorf("result %s column %q has %d value(s), so row %d does not exist — rows are counted from 0", id, field, len(vals), row)
		}
		return boundValue{Values: []decimal.Decimal{vals[row]}}, nil
	}
	if len(vals) == 1 {
		// A one-row result is the common case and binding it as a column would
		// make every margin expression say sum() for no reason.
		return boundValue{Values: []decimal.Decimal{vals[0]}}, nil
	}
	return boundValue{Column: true, Values: vals}, nil
}

// describe lists what can be bound, for an error message.
func (t *turnValues) describe() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.describeLocked()
}

func (t *turnValues) describeLocked() string {
	if len(t.order) == 0 {
		return "No tool in this turn has returned a figure yet — run the query first."
	}
	parts := make([]string, 0, len(t.order))
	for _, id := range t.order {
		res := t.byID[id]
		names := append([]string(nil), res.names...)
		sort.Strings(names)
		cols := strings.Join(names, ", ")
		if cols == "" {
			cols = "no numeric columns"
		}
		parts = append(parts, fmt.Sprintf("%s (%s, %d row(s)): %s", id, res.tool, res.rows, cols))
	}
	return "This turn has: " + strings.Join(parts, "; ") + "."
}

// attachResultID puts the binding handle on a tool payload, and nothing at all
// when the turn cannot compute.
func attachResultID(payload map[string]interface{}, id string) {
	if id == "" {
		return
	}
	payload["result_id"] = id
}
