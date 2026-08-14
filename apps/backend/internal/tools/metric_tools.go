package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/metric"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// MetricStore is what the metric tools need of the registry (T-07): the enabled
// definitions, and a single query that renders one and runs it. app.MetricService
// satisfies it; declared here so internal/tools does not import internal/app,
// which would be a cycle. Nil is legal — the tools then report that metrics are
// not configured, which is what the API's name-only registry build gets.
type MetricStore interface {
	ListEnabled(ctx context.Context, companyID string) ([]*domain.MetricDefinition, error)
	Query(ctx context.Context, companyID, key string, from, to time.Time, compare metric.Comparison) (*metric.Result, error)
}

// ListMetricsTool tells the agent which named numbers exist, so it can prefer a
// defined metric over re-deriving SQL (T-07). It returns the fields the model
// needs to choose: the key it will pass to query_metric, and the description it
// reads to decide relevance.
type ListMetricsTool struct{ store MetricStore }

func NewListMetricsTool(store MetricStore) *ListMetricsTool { return &ListMetricsTool{store: store} }

func (t *ListMetricsTool) Name() string { return "list_metrics" }

func (t *ListMetricsTool) Description() string {
	return "List the metrics defined for this organization — authoritative, pre-validated numbers. " +
		"Each has a key, label, description, unit and grain. PREFER these over run_sql: if a metric " +
		"answers the question, call query_metric with its key. Only fall back to run_sql for questions " +
		"no metric covers."
}

func (t *ListMetricsTool) Parameters() map[string]interfaces.ParameterSpec {
	return map[string]interfaces.ParameterSpec{}
}

func (t *ListMetricsTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

func (t *ListMetricsTool) Execute(ctx context.Context, _ string) (string, error) {
	if t.store == nil {
		return "", fmt.Errorf("metrics are not configured on this deployment")
	}
	companyID := tenantctx.CompanyID(ctx)
	if companyID == "" {
		return "", fmt.Errorf("no tenant in context")
	}
	metrics, err := t.store.ListEnabled(ctx, companyID)
	if err != nil {
		return "", fmt.Errorf("list metrics: %w", err)
	}
	type row struct {
		Key         string `json:"key"`
		Label       string `json:"label"`
		Description string `json:"description,omitempty"`
		Unit        string `json:"unit"`
		Grain       string `json:"grain"`
	}
	rows := make([]row, 0, len(metrics))
	for _, m := range metrics {
		rows = append(rows, row{
			Key: m.Key, Label: m.Label, Description: m.Description,
			Unit: string(m.Unit), Grain: string(m.Grain),
		})
	}
	out, _ := json.Marshal(map[string]any{"metrics": rows})
	return string(out), nil
}

// QueryMetricTool runs one defined metric over a window and returns its number
// — the same value validate-on-save proved and the dashboard's Test button
// showed, because all three go through one evaluate() path (T-07). This is what
// makes "the same question, the same answer" true.
type QueryMetricTool struct {
	store    MetricStore
	recorder UsageRecorder
}

func NewQueryMetricTool(store MetricStore, recorder UsageRecorder) *QueryMetricTool {
	if recorder == nil {
		recorder = nopRecorder{}
	}
	return &QueryMetricTool{store: store, recorder: recorder}
}

func (t *QueryMetricTool) Name() string { return "query_metric" }

func (t *QueryMetricTool) Description() string {
	return "Return the value of a defined metric over a date window. Pass metric_key (from list_metrics), " +
		"and optionally from and to as YYYY-MM-DD dates. OMIT from and to for an all-time question " +
		"('in total', 'all time', 'across all transactions', 'sepanjang waktu') — the metric is then " +
		"evaluated over every period the data holds. Do not ask the user which window they meant when " +
		"they did not name one. Optionally pass compare_to ('previous_period' or " +
		"'same_period_last_year') to also get the comparison value and the delta. Use this instead of " +
		"run_sql whenever a metric covers the question — the number is authoritative and consistent."
}

func (t *QueryMetricTool) Parameters() map[string]interfaces.ParameterSpec {
	return map[string]interfaces.ParameterSpec{
		"metric_key": {Type: "string", Description: "The metric's key, as returned by list_metrics (e.g. 'revenue').", Required: true},
		"from":       {Type: "string", Description: "Optional. Window start, inclusive, as YYYY-MM-DD (or RFC3339). Omit together with 'to' to cover all available data.", Required: false},
		"to":         {Type: "string", Description: "Optional. Window end as YYYY-MM-DD (or RFC3339); whether it is inclusive depends on the metric's definition. Omit together with 'from' to cover all available data.", Required: false},
		"compare_to": {
			Type:        "string",
			Description: "Optional. 'previous_period' compares against the immediately preceding window of equal length; 'same_period_last_year' compares against the same window one year earlier.",
			Required:    false,
			Enum:        []interface{}{"previous_period", "same_period_last_year"},
		},
	}
}

func (t *QueryMetricTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

func (t *QueryMetricTool) Execute(ctx context.Context, args string) (string, error) {
	if t.store == nil {
		return "", fmt.Errorf("metrics are not configured on this deployment")
	}
	var p struct {
		MetricKey string `json:"metric_key"`
		From      string `json:"from"`
		To        string `json:"to"`
		CompareTo string `json:"compare_to"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("could not parse arguments: %w", err)
	}
	companyID := tenantctx.CompanyID(ctx)
	if companyID == "" {
		return "", fmt.Errorf("no tenant in context")
	}
	if strings.TrimSpace(p.MetricKey) == "" {
		return "", fmt.Errorf("metric_key is required")
	}
	var from, to time.Time
	allTime := strings.TrimSpace(p.From) == "" && strings.TrimSpace(p.To) == ""
	if allTime {
		// Both omitted means "every period the data holds" (2026-08-14). They
		// were both required until then, and a question that names no window —
		// "what is our total revenue", "berapa total penjualan sepanjang waktu"
		// — left the model three bad options: invent a range, abandon the
		// authoritative metric for run_sql, or ask. Two eval runs took the
		// third, on four cases across two models, against a guideline that
		// already told them not to. A guideline loses to a missing affordance.
		//
		// One bound without the other stays an error: "from 2024-07-01" with no
		// end is a window the caller half-specified, and guessing which half
		// they meant is how a metric answers a question nobody asked.
		from, to = allTimeWindow()
	} else {
		var err error
		from, err = parseWindowBound(p.From)
		if err != nil {
			return "", fmt.Errorf("from: %w", err)
		}
		to, err = parseWindowBound(p.To)
		if err != nil {
			return "", fmt.Errorf("to: %w", err)
		}
	}
	compare := metric.Comparison(strings.TrimSpace(p.CompareTo))

	res, err := t.store.Query(ctx, companyID, strings.TrimSpace(p.MetricKey), from, to, compare)
	if err != nil {
		// An unknown key is the common, recoverable case: name what is available
		// rather than a bare "not found", so the model picks a real key or falls
		// back to run_sql knowingly.
		if errors.Is(err, domain.ErrNotFound) {
			return t.unknownKey(ctx, companyID, p.MetricKey), nil
		}
		return "", err
	}

	// A metric read is a SQL query against the tenant's warehouse; meter it as
	// one, on the same path run_sql uses (T-07). The comparison is a second read,
	// so a compared call meters twice.
	t.recorder.RecordSQL(ctx, companyID, tenantctx.ThreadID(ctx))
	if res.Comparison != nil {
		t.recorder.RecordSQL(ctx, companyID, tenantctx.ThreadID(ctx))
	}

	// row_count is what the rest of the system reads to decide whether a tool
	// produced evidence: agentbudget.Tracker.Observe grounds T-16's fabrication
	// check on it, and T-05's audit decorator records it as rows_returned. Both
	// parse the key off the tool's own result, and a metric result that omitted
	// it counted as no evidence at all — so a reply carrying a figure this tool
	// had just returned was replaced with "my query returned no data", which is
	// the opposite of what happened. An evaluation that produced a number is
	// exactly one row by construction — the registry refuses to save a template
	// that returns any other count — and a compared call that produced two
	// numbers says two, on the same reasoning that meters it twice above.
	//
	// A window the metric matched nothing in is worth no rows, and saying so
	// here is what makes the rest of the system treat it as the empty result it
	// is: agentbudget counts a row_count of 0 as an empty result rather than as
	// evidence, so T-16's fabrication check replaces a reply that states a
	// figure anyway. Counting a NULL as a row is how "Rp 0" reached a
	// customer-facing sentence (T-Q9, docs/coverage/eval-q1.md).
	rows := 0
	if !res.Primary.Empty {
		rows++
	}
	if res.Comparison != nil && !res.Comparison.Empty {
		rows++
	}
	payload := map[string]any{
		"metric_key": res.Metric.Key,
		"label":      res.Metric.Label,
		"unit":       string(res.Metric.Unit),
		"row_count":  rows,
		"value":      res.Primary.Value,
		"window":     map[string]string{"from": res.Primary.From.Format(time.RFC3339), "to": res.Primary.To.Format(time.RFC3339)},
	}
	if allTime {
		// Say the window was not the caller's, or the model quotes 1900 at a
		// user who asked about their total sales. What it should say is "all
		// time", which is what was actually computed.
		payload["window_scope"] = "all_available_data"
		payload["window_note"] = "No window was requested, so this covers ALL available data. " +
			"Describe it as the all-time total; do not quote these bounds as if the user chose them."
	}
	// The note carries the distinction in words, for the same reason run_sql's
	// zero-row note does: the empty set alone was not enough of a signal there
	// either, and the model asked the same question a different way answered
	// honestly. `value` is null rather than 0 — a zero in that field is the
	// thing being denied.
	if res.Primary.Empty {
		payload["value"] = nil
		payload["note"] = "This metric ran over the window and matched ZERO rows, so it has NO value — " +
			"this is NOT a zero. Tell the user there is no data for this period, name the window you " +
			"asked for, and offer to check which periods the data covers. Do NOT say the metric was 0, " +
			"and do NOT state any total for this window."
	} else if res.Primary.Value == 0 {
		// A real zero, and the one case where the tool cannot settle the
		// question on its own: a template written as COALESCE(SUM(x), 0)
		// answers an empty window with a genuine 0 and this path has no way to
		// tell it from a period that really summed to nothing. The model is
		// told to check rather than assert, which is cheap and only fires on a
		// zero.
		payload["note"] = "The value is exactly 0. If this metric sums or counts, a 0 can also mean the " +
			"window matched no rows — say which you mean only if you know, and otherwise say the metric " +
			"returned 0 for this window and offer to confirm the data's coverage."
	}
	if res.Metric.Currency != "" {
		payload["currency"] = res.Metric.Currency
	}
	if res.Comparison != nil {
		cmp := map[string]any{
			"basis":  string(compare),
			"value":  res.Comparison.Value,
			"window": map[string]string{"from": res.Comparison.From.Format(time.RFC3339), "to": res.Comparison.To.Format(time.RFC3339)},
		}
		// Same rule one window over. A comparison period we hold no data for is
		// the commoner half of the pair — "the same period last year" reaches
		// past the start of most warehouses — and reading it as zero produces
		// the growth figure that is hardest to unsay.
		if res.Comparison.Empty {
			cmp["value"] = nil
			cmp["note"] = "The comparison window matched ZERO rows: there is no value to compare " +
				"against, and no growth or decline can be stated. Say the comparison period has no data."
		}
		payload["comparison"] = cmp
		if res.Delta != nil {
			payload["delta"] = *res.Delta
		}
		if res.DeltaPct != nil {
			payload["delta_pct"] = *res.DeltaPct
		}
	}
	out, _ := json.Marshal(payload)
	return string(out), nil
}

func (t *QueryMetricTool) unknownKey(ctx context.Context, companyID, key string) string {
	var keys []string
	if metrics, err := t.store.ListEnabled(ctx, companyID); err == nil {
		for _, m := range metrics {
			keys = append(keys, m.Key)
		}
		sort.Strings(keys)
	}
	out, _ := json.Marshal(map[string]any{
		"error":          fmt.Sprintf("no metric with key %q", key),
		"available_keys": keys,
		"note":           "Pass one of available_keys, or use run_sql if no metric covers the question.",
	})
	return string(out)
}

// allTimeWindow is the window a call that named none is evaluated over: wide
// enough to hold every row a warehouse plausibly carries, and no wider.
//
// The bounds are not arbitrary. The floor is 1900-01-01 because SQL Server's
// `datetime` begins in 1753 and MySQL's `DATE` in 1000, so anything earlier
// buys nothing and risks a dialect rejecting the literal. The ceiling is one
// year out rather than a round 2999 because MySQL's `TIMESTAMP` ends in 2038:
// a template comparing a TIMESTAMP column against a year-3000 literal is a
// error or a silent truncation depending on the server's mode, and a metric
// that fails on the tenant with the oldest MySQL is worse than one that misses
// a forecast row dated more than a year ahead.
func allTimeWindow() (time.Time, time.Time) {
	return time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Now().UTC().AddDate(1, 0, 0).Truncate(24 * time.Hour)
}

// parseWindowBound accepts a plain date (the common case the model is told to
// send) or a full RFC3339 timestamp. A plain date is midnight UTC, which is the
// boundary a day/week/month metric expects.
func parseWindowBound(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("a date is required (YYYY-MM-DD)")
	}
	if d, err := time.Parse("2006-01-02", s); err == nil {
		return d, nil
	}
	if ts, err := time.Parse(time.RFC3339, s); err == nil {
		return ts, nil
	}
	return time.Time{}, fmt.Errorf("%q is not a date; use YYYY-MM-DD", s)
}
