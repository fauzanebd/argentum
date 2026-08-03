# T-17 · Observability — coverage

**Status: CODE COMPLETE 2026-08-03**, in two halves a day apart. The disclosure
fix landed in the morning; the exposition format, the domain counters and the
OTel spans landed in the same session. The gate — a `curl` of the exposition and
one trace waterfall — needs the stack and is outstanding.

## 1. What landed first: the endpoint stopped being public

Recorded in [`api-observability.md`](api-observability.md). `METRICS_TOKEN` set
means the token or `401`; unset means loopback only and `404` for everyone else,
with loopback read off the socket peer rather than `X-Forwarded-For`. That
closed the open finding in [`../plan/00-sprint-overview.md`](../plan/00-sprint-overview.md) §5.

## 2. Prometheus exposition

`GET /metrics` now answers in the text exposition format. The JSON snapshot is
still served on `?format=json` or an explicit `Accept: application/json` — a
browser's `*/*` is not a request for JSON, it is a request for the default.

**Not promhttp, and the reason is worth writing down.** The ticket names it, and
promhttp is the right answer when a process's counters *are* prometheus values.
Here they are a hand-rolled struct behind a mutex, read by the JSON endpoint and
by `T-A5`'s per-key block; converting them would have been a rewrite of
`collector.go` rather than the serializer the package comment has been promising
since it was written. So `WriteProm` renders the same snapshot in the format a
scraper expects. The histogram needed no remodelling at all — cumulative buckets
keyed by upper bound with a `+Inf` overflow is exactly what `_bucket{le="…"}`
means, which is what `T-A5` chose that shape for.

**What a library would have enforced is enforced by tests instead**, and one of
them found a real bug before it shipped: the first version emitted a route's
buckets, then its `_sum` and `_count`, then the next route's buckets —
interleaving three metric names, which the format forbids. Three passes now.
The other rules under test: `le` on every bucket line including `+Inf`, buckets
ascending numerically with `+Inf` last, label values escaped for backslash,
quote and newline (and quoted by hand, because escaping and then `%q` escapes
twice), and byte-identical output across repeated scrapes so a diff of two
scrapes reads as what moved.

## 3. The counters the ticket asked for

| Metric | Recorded at |
| ------ | ----------- |
| `argentum_turn_duration_ms_{sum,count,max}` | `ChatRunner`, once per turn |
| `argentum_tool_calls_total{tool}`, `_errors_total`, `_duration_ms_{sum,max}` | the audit decorator |
| `argentum_llm_latency_ms_{sum,count,max}{model}` | `MeteredLLM` |
| `argentum_watcher_fires_total{outcome}` | `WatcherService`, four outcomes |
| `argentum_action_executions_total{kind}`, `_failures_total` | `ActionService.execute` |

Every label is bounded by code — a tool name from the registry, an action kind
from the action registry, a model from deployment config, a watcher outcome from
a fixed set. Nothing is labelled by anything a tenant types, for the same reason
`maxTrackedAPIKeys` exists: an unbounded label set on a scraped endpoint is how
an exporter becomes the process's largest allocation.

The watcher outcomes are four rather than a boolean — `breached`, `suppressed`,
`credits_exhausted`, `quiet` — because a watcher firing constantly and a watcher
suppressing constantly are different problems. The events sheet had to be fixed
on 2026-08-03 to answer the same question.

**A dead counter, found while wiring this.** `Collector.RecordLLMRequest` has
existed since the endpoint did and had **no call site**. Every LLM call in this
product goes through `MeteredLLM`, and that wrapper only ever wrote a
`usage_event` — so `llm_requests_total`, `llm_tokens_total` and
`llm_cost_usd_total` were three counters that could not move. The tenant-facing
numbers were always right; the operator-facing ones were always zero. They are
fed now, from the same wrapper, in a function kept separate from `record()`
because the two answer to different owners: `record()` bills a tenant and needs a
company on the context, while this is the deployment's own view and counts a call
made with no tenant exactly as it counts one made in a turn.

## 4. OTel tracing

`internal/tracing`. One span per turn (`agent.turn`, carrying company, thread
and channel), one per tool call (`agent.tool`), and `Step` for named phases.

**Unset `OTEL_EXPORTER_OTLP_ENDPOINT` installs no provider**, so `Start` returns
a non-recording span: a struct copy and a context value, no allocation per
attribute and no exporter goroutine. That is what makes it acceptable to
instrument the turn path unconditionally — an `if` at every call site is how
instrumentation ends up covering only the paths somebody remembered.

The variable names are OTel's own, not Argentum's, so a deployment already
exporting from another service needs no setting specific to us.
`OTEL_EXPORTER_OTLP_PROTOCOL` picks `grpc` (default) or `http/protobuf`. A
collector that cannot be reached logs an error and leaves the process running:
a process whose job is answering questions must not fail to start because a
trace sink is down.

**The tool span is started at the same seam as the audit row and the counter.**
Three records of one call, and none of them can end up covering a different set
of calls than the others.

**No message text on any span.** A span exported to a third-party collector is a
copy of whatever is on it, and the question a trace answers is about latency.

## 5. ServiceMonitor

`templates/servicemonitor.yaml`, off by default and gated on
`metrics.serviceMonitor.enabled`. Off because rendering the resource into a
cluster without the Prometheus Operator's CRD fails the whole release, which is
a bad trade for a metric.

The values block carries the warning that matters: a scrape arrives from the
Prometheus pod, which is not loopback, so a deployment enabling this must set
`METRICS_TOKEN` and name the secret holding it — otherwise every scrape reads
`401`, which is the endpoint working as designed and looking broken.

## 6. Not done

- **The gate.** `curl` the exposition and paste it; one trace waterfall for a
  tool-calling turn showing the LLM/SQL split. Both need the stack, and the
  waterfall needs a collector — a local Jaeger or an OTel collector in the compose
  file, which is itself not written.
- **Queue depth is not exported.** The ticket names it. It needs an
  `asynq.Inspector` against the same Redis, polled on a ticker — a small amount
  of work in a place none of the rest of this touches, and it is the one counter
  here that is about the infrastructure rather than the product. Left out rather
  than half-wired.
- **No spans below the tool call.** The ticket names guardrails, memory
  hydration and the embedding lookup. `tracing.Step` exists for exactly that and
  is unused; each site is one line, and the reason to add them is a real
  waterfall showing where the time went, which is the gate above.
