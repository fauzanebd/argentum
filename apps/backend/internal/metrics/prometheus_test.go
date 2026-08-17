package metrics

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The exposition format is hand-written (see prometheus.go for why), so the
// rules a library would enforce are enforced here instead: one metric's series
// must be contiguous, every bucket line carries `le`, and the parse must not
// depend on map iteration order.

func renderSnapshot(t *testing.T, c *Collector) string {
	t.Helper()
	var buf bytes.Buffer
	if err := c.GetSnapshot().WriteProm(&buf); err != nil {
		t.Fatalf("WriteProm: %v", err)
	}
	return buf.String()
}

func TestExpositionCarriesTheDomainCounters(t *testing.T) {
	c := NewCollector()
	c.RecordWatcherFire("breached")
	c.RecordWatcherFire("suppressed")
	c.RecordWatcherFire("breached")
	c.RecordActionExecution("http_action", true)
	c.RecordActionExecution("http_action", false)
	c.RecordToolCall("run_sql", 120*time.Millisecond, false)
	c.RecordTurn(3 * time.Second)
	c.RecordLLMLatency("claude-opus-5", 900*time.Millisecond)

	out := renderSnapshot(t, c)

	for _, want := range []string{
		`argentum_watcher_fires_total{outcome="breached"} 2`,
		`argentum_watcher_fires_total{outcome="suppressed"} 1`,
		`argentum_action_executions_total{kind="http_action"} 2`,
		`argentum_action_failures_total{kind="http_action"} 1`,
		`argentum_tool_calls_total{tool="run_sql"} 1`,
		`argentum_tool_duration_ms_sum{tool="run_sql"} 120`,
		`argentum_turn_duration_ms_count 1`,
		`argentum_turn_duration_ms_sum 3000`,
		`argentum_llm_latency_ms_sum{model="claude-opus-5"} 900`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("exposition is missing:\n  %s\n\ngot:\n%s", want, out)
		}
	}
}

// The wrong-but-nonempty instrument (T-Q11). Two numbers, because one reply
// carrying five invented figures and five replies carrying one each are the
// same figure count and different problems.
func TestExpositionCarriesTheGroundingCounters(t *testing.T) {
	c := NewCollector()
	// A clean reply must not touch either counter, or the rate is meaningless.
	c.RecordUngroundedFigures(0)
	c.RecordUngroundedFigures(2)
	c.RecordUngroundedFigures(1)

	out := renderSnapshot(t, c)
	for _, want := range []string{
		"argentum_ungrounded_replies_total 2",
		"argentum_ungrounded_figures_total 3",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("exposition is missing:\n  %s\n\ngot:\n%s", want, out)
		}
	}
	if snap := c.GetSnapshot(); snap.Grounding.UngroundedReplies != 2 {
		t.Errorf("snapshot counted %d replies, want 2", snap.Grounding.UngroundedReplies)
	}
}

// Every series of one metric name must appear together — a scraper reading a
// second block under a name it has already closed is entitled to reject it.
func TestEachMetricNamesSeriesAreContiguous(t *testing.T) {
	c := NewCollector()
	c.RecordAPIRequest("GET", "/v1/me", "key-a", 200, 5*time.Millisecond)
	c.RecordAPIRequest("POST", "/v1/chat", "key-b", 500, 50*time.Millisecond)
	c.RecordToolCall("run_sql", time.Second, false)
	c.RecordToolCall("get_schema", time.Second, true)

	out := renderSnapshot(t, c)

	seen := map[string]bool{}
	current := ""
	for _, line := range strings.Split(out, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name := line
		if i := strings.IndexAny(line, "{ "); i >= 0 {
			name = line[:i]
		}
		if name == current {
			continue
		}
		if seen[name] {
			t.Fatalf("series for %s resume after another metric intervened:\n%s", name, out)
		}
		seen[name] = true
		current = name
	}
}

// A histogram without `le` is not a histogram, and `+Inf` is the bucket a
// quantile reader needs most.
func TestHistogramBucketsCarryLe(t *testing.T) {
	c := NewCollector()
	c.RecordAPIRequest("GET", "/v1/me", "key-a", 200, 7*time.Millisecond)

	out := renderSnapshot(t, c)

	var buckets int
	var sawInf bool
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "argentum_v1_request_duration_ms_bucket") {
			continue
		}
		buckets++
		if !strings.Contains(line, `le="`) {
			t.Errorf("bucket line has no le label: %s", line)
		}
		if strings.Contains(line, `le="+Inf"`) {
			sawInf = true
		}
	}
	if buckets == 0 {
		t.Fatalf("no bucket lines at all:\n%s", out)
	}
	if !sawInf {
		t.Error("no +Inf bucket; a cumulative histogram without it cannot be read")
	}
	if !strings.Contains(out, "argentum_v1_request_duration_ms_count") {
		t.Error("histogram has no _count")
	}
}

// Buckets ascend numerically with +Inf last. Sorted as strings, "10000" would
// precede "5" and +Inf would land in the middle — legal, and unreadable.
func TestBucketsAscendWithInfLast(t *testing.T) {
	c := NewCollector()
	c.RecordAPIRequest("GET", "/v1/me", "key-a", 200, 3*time.Second)

	var bounds []string
	for _, line := range strings.Split(renderSnapshot(t, c), "\n") {
		if !strings.HasPrefix(line, "argentum_v1_request_duration_ms_bucket") {
			continue
		}
		start := strings.Index(line, `le="`) + 4
		end := strings.Index(line[start:], `"`) + start
		bounds = append(bounds, line[start:end])
	}
	if len(bounds) < 2 {
		t.Fatalf("expected several buckets, got %v", bounds)
	}
	if bounds[len(bounds)-1] != "+Inf" {
		t.Errorf("last bucket is %q, want +Inf", bounds[len(bounds)-1])
	}
	prev := -1.0
	for _, b := range bounds[:len(bounds)-1] {
		v, err := strconv.ParseFloat(b, 64)
		if err != nil {
			t.Fatalf("bucket bound %q does not parse: %v", b, err)
		}
		if v <= prev {
			t.Errorf("bucket bounds are not ascending: %v", bounds)
		}
		prev = v
	}
}

// The per-key block is the one part of this endpoint that names a tenant's own
// identifier. A snapshot stripped of it must render no key series at all.
func TestStrippedSnapshotRendersNoKeyLabels(t *testing.T) {
	c := NewCollector()
	c.RecordAPIRequest("GET", "/v1/me", "key-belonging-to-a-tenant", 200, time.Millisecond)

	var buf bytes.Buffer
	if err := c.GetSnapshot().WithoutKeyLabels().WriteProm(&buf); err != nil {
		t.Fatalf("WriteProm: %v", err)
	}
	if strings.Contains(buf.String(), "key-belonging-to-a-tenant") {
		t.Errorf("a stripped snapshot rendered a key id:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "argentum_v1_requests_total") {
		t.Error("route numbers were stripped too; they name no tenant and are always served")
	}
}

// Route labels carry `/` and `:`, both legal unescaped; a backslash or a
// newline is not. The escaping has its own test because getting it wrong yields
// a scrape that fails to parse rather than a wrong number.
func TestLabelValuesAreEscaped(t *testing.T) {
	if got := renderLabels(map[string]string{"route": `GET /v1/things/:id`}); got != `{route="GET /v1/things/:id"}` {
		t.Errorf("labels = %s", got)
	}
	if got := renderLabels(map[string]string{"x": "a\\b"}); got != `{x="a\\b"}` {
		t.Errorf("backslash not escaped: %s", got)
	}
	if got := renderLabels(map[string]string{"x": "a\nb"}); got != `{x="a\nb"}` {
		t.Errorf("newline not escaped: %s", got)
	}
}

func withoutUptime(out string) string {
	var kept []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "argentum_uptime_seconds") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func TestQuotesInsideALabelAreEscaped(t *testing.T) {
	if got := renderLabels(map[string]string{"x": `a"b`}); got != `{x="a\"b"}` {
		t.Errorf("quote not escaped: %s", got)
	}
}

func TestIntegersRenderWithoutADecimalPoint(t *testing.T) {
	if got := formatValue(42); got != "42" {
		t.Errorf("formatValue(42) = %q", got)
	}
	if got := formatValue(0.5); got != "0.5" {
		t.Errorf("formatValue(0.5) = %q", got)
	}
}

// Every scrape must be byte-identical for an unchanged snapshot: a diff of two
// scrapes is how an operator reads what moved, and map iteration order would
// make every diff total.
func TestExpositionIsStableAcrossScrapes(t *testing.T) {
	c := NewCollector()
	for _, tool := range []string{"run_sql", "get_schema", "query_metric", "list_sources"} {
		c.RecordToolCall(tool, time.Millisecond, false)
	}
	c.RecordAPIRequest("GET", "/v1/me", "key-a", 200, time.Millisecond)
	c.RecordAPIRequest("POST", "/v1/chat", "key-b", 200, time.Millisecond)

	// Uptime is excluded: it is a clock reading and is *supposed* to move. Every
	// other line must be identical, which is what makes a diff of two scrapes
	// readable.
	first := withoutUptime(renderSnapshot(t, c))
	for range 5 {
		if got := withoutUptime(renderSnapshot(t, c)); got != first {
			t.Fatal("two scrapes of one snapshot differ; the rendering depends on map order")
		}
	}
}

// Queue depth is the one gauge set sampled from Redis rather than counted
// here, so it has a state the others do not: nothing has sampled yet.
func TestQueueDepthGaugesAppearOnlyOnceSampled(t *testing.T) {
	c := NewCollector()

	if out := renderSnapshot(t, c); strings.Contains(out, "argentum_queue_") {
		t.Errorf("a process that has not sampled must export no queue series, got:\n%s", out)
	}

	c.SetQueueDepths(map[string]QueueDepth{
		"default": {Pending: 3, Active: 1, Scheduled: 7, Retry: 2, Archived: 4},
		"low":     {Pending: 0},
	})
	out := renderSnapshot(t, c)
	for _, want := range []string{
		`argentum_queue_pending{queue="default"} 3`,
		`argentum_queue_pending{queue="low"} 0`,
		`argentum_queue_active{queue="default"} 1`,
		`argentum_queue_scheduled{queue="default"} 7`,
		`argentum_queue_retry{queue="default"} 2`,
		`argentum_queue_archived{queue="default"} 4`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("exposition is missing:\n  %s\n\ngot:\n%s", want, out)
		}
	}

	// A queue that vanishes from the sample must vanish from the exposition:
	// a stale gauge reading 3 pending cannot be told from a real backlog.
	c.SetQueueDepths(map[string]QueueDepth{"low": {Pending: 0}})
	if out := renderSnapshot(t, c); strings.Contains(out, `queue="default"`) {
		t.Errorf("a queue dropped from the sample must not linger, got:\n%s", out)
	}
}
