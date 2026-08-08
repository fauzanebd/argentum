package metrics

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// Prometheus text exposition (T-17).
//
// Written against the snapshot rather than by re-plumbing every counter onto
// `prometheus.Collector` types. The ticket names promhttp, and promhttp is the
// right answer when the process's counters *are* prometheus values — but here
// they are a hand-rolled struct with a mutex, read by the JSON endpoint and by
// T-A5's per-key block, and converting them would have been a rewrite of
// `collector.go` rather than the serializer the package comment has been
// promising since it was written. This renders the same snapshot in the same
// format a scraper expects, and the histogram was already shaped for it:
// cumulative buckets keyed by upper bound with a `+Inf` overflow is exactly
// what `_bucket{le="…"}` means.
//
// Two rules the format demands and a hand-written renderer can break, so both
// are asserted in the tests: every series of one metric name must be
// contiguous, and `le` must be present on every bucket line including `+Inf`.

// promNamespace prefixes every series. A scrape that pulls this endpoint into a
// shared Prometheus needs its metrics to be findable by one prefix.
const promNamespace = "argentum"

// WriteProm renders the snapshot in Prometheus text exposition format.
//
// Key labels are already absent from a snapshot served without a credential
// (MetricsSnapshot.WithoutKeyLabels), so there is no second decision here: what
// this writes is whatever the caller was entitled to read.
func (s MetricsSnapshot) WriteProm(w io.Writer) error {
	p := &promWriter{w: w}

	p.gauge("uptime_seconds", "Seconds since this process last reset its counters.", nil, s.UptimeSeconds)

	// --- queries ---
	p.counter("queries_total", "SQL queries the agent has run.", nil, float64(s.Queries.Total))
	p.counter("queries_cached_total", "Queries answered from cache.", nil, float64(s.Queries.Cached))
	p.counter("queries_failed_total", "Queries that returned an error.", nil, float64(s.Queries.Failed))
	p.gauge("query_duration_avg_ms", "Mean query duration since reset.", nil, float64(s.Queries.AvgDurationMs))

	// --- llm ---
	p.counter("llm_requests_total", "Model calls made.", nil, float64(s.LLM.RequestsTotal))
	p.counterVec("llm_tokens_total", "Tokens billed, by direction.", "direction", map[string]float64{
		"in":  float64(s.LLM.TokensInTotal),
		"out": float64(s.LLM.TokensOutTotal),
	})
	p.counter("llm_cost_usd_total", "Model spend in USD since reset.", nil, s.LLM.CostTotal)
	p.counter("llm_stream_turns_total", "Streaming turns completed.", nil, float64(s.LLM.StreamTurnsTotal))
	p.counter("llm_stream_turns_without_usage_total",
		"Streaming turns that reported no usage — an unbilled turn, and the shape of finding C-2.",
		nil, float64(s.LLM.StreamTurnsWithoutUsage))

	// --- cache, jobs, conversations ---
	p.counter("cache_hits_total", "Schema/embedding cache hits.", nil, float64(s.Cache.Hits))
	p.counter("cache_misses_total", "Schema/embedding cache misses.", nil, float64(s.Cache.Misses))
	p.counter("jobs_created_total", "Queued jobs created.", nil, float64(s.Jobs.Created))
	p.counter("jobs_completed_total", "Queued jobs completed.", nil, float64(s.Jobs.Completed))
	p.counter("jobs_failed_total", "Queued jobs that failed.", nil, float64(s.Jobs.Failed))
	p.gauge("conversations_active", "Threads currently mid-turn.", nil, float64(s.Conversations.Active))
	p.counter("context_resets_total", "Threads whose context was reset.", nil, float64(s.Conversations.ContextResets))

	// --- queue depth (T-17), sampled from Redis rather than counted here ---
	//
	// Absent entirely on a process with no poller: a queue gauge is read as
	// "this is the backlog right now", and a process that is not sampling would
	// otherwise export a confident zero.
	queues := sortedKeys(s.Queues)
	if len(queues) > 0 {
		p.header("queue_pending", "gauge", "Tasks waiting to be picked up, by queue.")
		for _, q := range queues {
			p.sample("queue_pending", map[string]string{"queue": q}, float64(s.Queues[q].Pending))
		}
		p.header("queue_active", "gauge", "Tasks a worker is running right now, by queue.")
		for _, q := range queues {
			p.sample("queue_active", map[string]string{"queue": q}, float64(s.Queues[q].Active))
		}
		p.header("queue_scheduled", "gauge", "Tasks queued for a future time, by queue.")
		for _, q := range queues {
			p.sample("queue_scheduled", map[string]string{"queue": q}, float64(s.Queues[q].Scheduled))
		}
		p.header("queue_retry", "gauge", "Tasks that failed and are waiting to be retried, by queue.")
		for _, q := range queues {
			p.sample("queue_retry", map[string]string{"queue": q}, float64(s.Queues[q].Retry))
		}
		p.header("queue_archived", "gauge", "Tasks that exhausted their retries, by queue.")
		for _, q := range queues {
			p.sample("queue_archived", map[string]string{"queue": q}, float64(s.Queues[q].Archived))
		}
	}

	// --- what the product did (T-17) ---
	p.counterVec("watcher_fires_total", "Watcher evaluations, by outcome.", "outcome", asFloats(s.Domain.WatcherFires))
	p.counterVec("action_executions_total", "Actions executed, by kind.", "kind", asFloats(s.Domain.ActionExecutions))
	p.counterVec("action_failures_total", "Actions that failed, by kind.", "kind", asFloats(s.Domain.ActionFailures))

	p.duration("turn_duration_ms", "Agent turn wall clock.", nil, s.Domain.Turns)

	// Tool traffic: three metric names, each with every tool's series together,
	// because the exposition format requires one metric's series to be
	// contiguous and a per-tool loop would interleave them.
	tools := sortedKeys(s.Domain.Tools)
	p.header("tool_calls_total", "counter", "Tool calls, by tool.")
	for _, name := range tools {
		p.sample("tool_calls_total", map[string]string{"tool": name}, float64(s.Domain.Tools[name].Calls))
	}
	p.header("tool_errors_total", "counter", "Tool calls that returned an error, by tool.")
	for _, name := range tools {
		p.sample("tool_errors_total", map[string]string{"tool": name}, float64(s.Domain.Tools[name].Errors))
	}
	p.header("tool_duration_ms_sum", "counter", "Total time in each tool, milliseconds.")
	for _, name := range tools {
		p.sample("tool_duration_ms_sum", map[string]string{"tool": name}, float64(s.Domain.Tools[name].SumMS))
	}
	p.header("tool_duration_ms_max", "gauge", "Slowest single call to each tool, milliseconds.")
	for _, name := range tools {
		p.sample("tool_duration_ms_max", map[string]string{"tool": name}, float64(s.Domain.Tools[name].MaxMS))
	}

	models := sortedKeys(s.Domain.LLMLatency)
	p.header("llm_latency_ms_sum", "counter", "Total model latency, by model, milliseconds.")
	for _, m := range models {
		p.sample("llm_latency_ms_sum", map[string]string{"model": m}, float64(s.Domain.LLMLatency[m].SumMS))
	}
	p.header("llm_latency_ms_count", "counter", "Model calls timed, by model.")
	for _, m := range models {
		p.sample("llm_latency_ms_count", map[string]string{"model": m}, float64(s.Domain.LLMLatency[m].Count))
	}
	p.header("llm_latency_ms_max", "gauge", "Slowest single model call, by model, milliseconds.")
	for _, m := range models {
		p.sample("llm_latency_ms_max", map[string]string{"model": m}, float64(s.Domain.LLMLatency[m].MaxMS))
	}

	// --- /v1 (T-A5) ---
	routes := sortedKeys(s.APIV1.Routes)
	p.header("v1_requests_total", "counter", "Public API requests, by route and status.")
	for _, route := range routes {
		r := s.APIV1.Routes[route]
		for _, status := range sortedKeys(r.ByStatus) {
			p.sample("v1_requests_total",
				map[string]string{"route": route, "status": status}, float64(r.ByStatus[status]))
		}
	}
	p.header("v1_request_errors_total", "counter", "Public API responses outside 2xx, by route.")
	for _, route := range routes {
		p.sample("v1_request_errors_total", map[string]string{"route": route}, float64(s.APIV1.Routes[route].Errors))
	}

	// The histogram, in the shape a quantile can be read out of. `le` is a
	// string label and `+Inf` is a legal value of it — the bucket map already
	// carries both, so this is a transcription rather than a computation.
	// Three passes, not one per route. Emitting a route's buckets, then its sum
	// and count, then the next route's buckets, interleaves three metric names —
	// which the format forbids and which the contiguity test caught.
	p.header("v1_request_duration_ms", "histogram", "Public API request duration, by route.")
	for _, route := range routes {
		r := s.APIV1.Routes[route]
		for _, le := range sortedBuckets(r.Latency.Buckets) {
			p.sample("v1_request_duration_ms_bucket",
				map[string]string{"route": route, "le": le}, float64(r.Latency.Buckets[le]))
		}
	}
	for _, route := range routes {
		p.sample("v1_request_duration_ms_sum",
			map[string]string{"route": route}, float64(s.APIV1.Routes[route].Latency.SumMS))
	}
	for _, route := range routes {
		p.sample("v1_request_duration_ms_count",
			map[string]string{"route": route}, float64(s.APIV1.Routes[route].Latency.Count))
	}

	if len(s.APIV1.Keys) > 0 {
		keys := sortedKeys(s.APIV1.Keys)
		p.header("v1_key_requests_total", "counter", "Public API requests, by key id. Served only to a credentialed scrape.")
		for _, k := range keys {
			p.sample("v1_key_requests_total", map[string]string{"key_id": k}, float64(s.APIV1.Keys[k].Requests))
		}
		p.header("v1_key_errors_total", "counter", "Public API errors, by key id.")
		for _, k := range keys {
			p.sample("v1_key_errors_total", map[string]string{"key_id": k}, float64(s.APIV1.Keys[k].Errors))
		}
	}
	p.counter("v1_keys_untracked_total",
		"Requests by keys beyond the per-key label cap; non-zero means the per-key block is incomplete.",
		nil, float64(s.APIV1.KeysUntracked))

	return p.err
}

// promWriter accumulates the first write error and stops caring afterwards,
// which is what every io.Writer wrapper in a serializer does: a scrape whose
// connection died does not benefit from the remaining four hundred lines.
type promWriter struct {
	w   io.Writer
	err error
}

func (p *promWriter) printf(format string, args ...any) {
	if p.err != nil {
		return
	}
	_, p.err = fmt.Fprintf(p.w, format, args...)
}

func (p *promWriter) header(name, kind, help string) {
	p.printf("# HELP %s_%s %s\n# TYPE %s_%s %s\n", promNamespace, name, help, promNamespace, name, kind)
}

func (p *promWriter) sample(name string, labels map[string]string, value float64) {
	p.printf("%s_%s%s %s\n", promNamespace, name, renderLabels(labels), formatValue(value))
}

func (p *promWriter) counter(name, help string, labels map[string]string, value float64) {
	p.header(name, "counter", help)
	p.sample(name, labels, value)
}

func (p *promWriter) gauge(name, help string, labels map[string]string, value float64) {
	p.header(name, "gauge", help)
	p.sample(name, labels, value)
}

// counterVec writes one metric with a series per label value, sorted so a diff
// of two scrapes is readable.
func (p *promWriter) counterVec(name, help, label string, values map[string]float64) {
	p.header(name, "counter", help)
	for _, k := range sortedKeys(values) {
		p.sample(name, map[string]string{label: k}, values[k])
	}
}

// duration writes a sum/count/max triple under one name. Not a histogram: no
// bucket boundaries have been chosen for these, and inventing them here would
// bake a guess into a wire format.
func (p *promWriter) duration(name, help string, labels map[string]string, d DurationMetrics) {
	p.header(name+"_sum", "counter", help+" Total, milliseconds.")
	p.sample(name+"_sum", labels, float64(d.SumMS))
	p.header(name+"_count", "counter", help+" Observations.")
	p.sample(name+"_count", labels, float64(d.Count))
	p.header(name+"_max", "gauge", help+" Slowest observation, milliseconds.")
	p.sample(name+"_max", labels, float64(d.MaxMS))
}

// renderLabels writes the label set, sorted, with the values escaped the way
// the exposition format requires. A route label carries a `/` and a `:` — both
// legal — but an escape bug here produces a scrape that fails to parse rather
// than a wrong number, which is why the escaping is its own function with its
// own test.
func renderLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	pairs := make([]string, 0, len(labels))
	for _, k := range sortedKeys(labels) {
		// The quoting is written out rather than left to %q: Go's verb escapes a
		// superset of what the exposition format asks for, so escaping first and
		// quoting with %q escapes twice — `a\b` renders as `a\\b`, which is a
		// different label value.
		pairs = append(pairs, k+`="`+escapeLabelValue(labels[k])+`"`)
	}
	return "{" + strings.Join(pairs, ",") + "}"
}

// escapeLabelValue escapes exactly the three characters the exposition format
// reserves inside a label value: backslash, double quote, and newline. In that
// order — escaping the backslash second would escape the ones this function
// just introduced.
func escapeLabelValue(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	v = strings.ReplaceAll(v, "\n", `\n`)
	return v
}

// formatValue renders a float the way the format wants: integers without a
// decimal point, everything else at full precision.
func formatValue(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func asFloats(in map[string]int64) map[string]float64 {
	out := make(map[string]float64, len(in))
	for k, v := range in {
		out[k] = float64(v)
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortedBuckets orders bucket bounds numerically with `+Inf` last. Sorting them
// as strings would put "10000" before "5" and `+Inf` in the middle, which is a
// legal but unreadable exposition — and a cumulative histogram read out of
// order is the kind of thing somebody debugs for an hour.
func sortedBuckets(buckets map[string]int64) []string {
	out := make([]string, 0, len(buckets))
	for k := range buckets {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i] == "+Inf" {
			return false
		}
		if out[j] == "+Inf" {
			return true
		}
		a, errA := strconv.ParseFloat(out[i], 64)
		b, errB := strconv.ParseFloat(out[j], 64)
		if errA != nil || errB != nil {
			return out[i] < out[j]
		}
		return a < b
	})
	return out
}
