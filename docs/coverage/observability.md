# T-17 · Observability — coverage

**Status: CODE COMPLETE 2026-08-03**, in two halves a day apart. The disclosure
fix landed in the morning; the exposition format, the domain counters and the
OTel spans landed in the same session. The gate — a `curl` of the exposition and
one trace waterfall — needs the stack and is outstanding.

**GATED LIVE 2026-08-08, and the ticket is complete.** Everything this file
listed as *not done* is done: queue depth is exported (§6), the sub-tool spans
are wired (§7), the exposition was scraped and its auth proven (§8), and the
trace waterfall was read (§9) — which found and fixed a span-parenting defect
that had been splitting every turn into two traces. The pattern the delivery log
has recorded since `T-13` held again: **the live half found something the unit
tests could not.**

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

## 6. Queue depth, added 2026-08-08

The one number in the exposition this process cannot count. Every other metric
here is something Argentum did; a backlog is a fact about Redis that stays true
while this process does nothing at all. `internal/queue.DepthPoller` asks
`asynq.Inspector` every 15 seconds and writes the answer into the collector;
`cmd/api` runs it, because the API is what serves `/metrics` and a gauge in a
process nothing scrapes is not an exported metric.

| Metric | Meaning |
| ------ | ------- |
| `argentum_queue_pending{queue}` | waiting to be picked up |
| `argentum_queue_active{queue}` | running in a worker right now |
| `argentum_queue_scheduled{queue}` | queued for a future time |
| `argentum_queue_retry{queue}` | failed, waiting to be retried |
| `argentum_queue_archived{queue}` | out of retries |

Two decisions worth keeping:

**Queues are discovered, not configured.** `WORKER_QUEUES` lives on the worker,
so an API that only knew the queues it was told about would stop reporting the
day somebody added one.

**The sample is replaced wholesale, and nothing is exported until something
samples.** A queue that disappears from Redis disappears from the exposition,
because a stale gauge reading 400 pending cannot be told from a real backlog;
and a process with no poller exports no queue series rather than a confident
zero it cannot vouch for.

## 7. Spans below the tool call, added 2026-08-08

`tracing.Step` had no callers. Three now, each one line at a seam that already
existed: `memory.hydrate` (Postgres → agent memory), `table_picker` (the
embedding round trip, which happens before the model is called at all), and
`guardrails.output` (the T-07b rules, started after the nil check so a
deployment without them records nothing).

`guardrails.output` carries `argentum.outcome` — `blocked` or `redacted` —
through the new `tracing.Outcome` helper, because a guardrail that redacted a
reply did its job and a waterfall that cannot tell that from a clean pass is
missing the reason the span was worth exporting. It is not an error, and
recording it as one would make every redaction look like a fault.

## 8. The gate — exposition half run 2026-08-08

`METRICS_TOKEN=local-gate-token`, API on the host against the compose stack:

| Request | Result |
| ------- | ------ |
| `GET /metrics`, no credential | `401` |
| `GET /metrics`, `Authorization: Bearer wrong` | `401` |
| `GET /metrics`, correct token | `200`, `text/plain; version=0.0.4; charset=utf-8`, 105 lines |

The queue gauges appeared in that scrape reading the `default` queue asynq
already had in Redis — discovered, not configured, which is the property above
being exercised rather than asserted. That they *move* is
`TestDepthPollerReportsWhatRedisHolds`: enqueue one task on a queue of the
test's own and `pending` reads 1, drain it and the same sample reads 0. It is
skipped unless `ARGENTUM_TEST_REDIS` names a live Redis — CI has none, and a
test that passes without its dependency is worse than no test.

```
ARGENTUM_TEST_REDIS=localhost:6380 go test ./internal/queue/ -run Depth
```

**A collector now exists to trace into.** `docker-compose.yml` gained a
`jaeger` service — all-in-one, OTLP native, so there is no separate
otel-collector to configure — behind the `tracing` profile, so an ordinary
`docker compose up` still starts nothing. Verified up on 2026-08-08: UI on
`:16686`, OTLP HTTP on `:4318` answering `200`. `.env.example` carries the
three-line runbook, including the part that is easy to get wrong: **both** api
and worker must export, because `agent.turn` starts on the worker and a trace
with only the API's spans is half a trace.

## 9. The waterfall, read 2026-08-08 — and the defect it found

`docker compose --profile tracing up -d jaeger`, then one golden-set case
(`december-2024-sales`) run through `cmd/eval` with the OTLP variables set. The
harness runs the same turn path as the worker, one question at a time, against a
tenant that is already seeded — the cheapest way to produce a waterfall on
demand. It needed one change to do that: **`cmd/eval` never called
`tracing.Init`**, so every span in it was non-recording and the collector saw
nothing. It does now, with the flush called on the `os.Exit(1)` path as well as
the deferred one — a failing run is precisely the run whose trace somebody wants
to look at.

**The first read came back as two traces, not one:**

```
trace b4ec5071…  1 span:  agent.memory.hydrate
trace 4f49f453…  4 spans: agent.turn → table_picker, tool(query_metric), guardrails.output
```

`hydrateMemory` ran *between* the agent being built and `tracing.Turn` being
called, so its span had no parent and Jaeger filed it as a trace of its own. The
turn span now starts before LLM resolution rather than after agent construction:
the user waits for resolution, construction and hydration exactly as they wait
for the model, so covering them is also the more honest reading of "the turn as
the user experiences it" that the old comment claimed. Second read:

```
agent.turn                 +   0.0ms   7750.3ms  channel=dashboard company_id=de3caef9… thread_id=499725c7…
  agent.memory.hydrate     +   2.5ms      0.9ms
  agent.table_picker       +   5.1ms      0.0ms
  agent.tool               +6084.2ms     18.1ms  tool=query_metric
  agent.guardrails.output  +7745.4ms      1.1ms
```

**And there is the LLM/SQL split the ticket asked for**: 7,750 ms of turn, 18 ms
of it inside the tool. The metric query is 0.2% of what the user waited for and
the rest is model latency — the answer to "why is it slow" for this product,
stated in one line for the first time. `table_picker` at 0.0 ms is the feature
being off for this tenant, returning before the embedding call.

## 10. Not done

- **A worker-side trace.** The waterfall above came from `cmd/eval`. On the real
  deployment the turn is enqueued by `cmd/api` and run by `cmd/worker`, and
  nothing has yet shown the trace context surviving that hop — the propagators
  are installed, but `ChatRunPayload` carries no traceparent field, so it almost
  certainly does not. A trace that stops at the queue is two traces again, one
  layer up from the bug §9 just fixed. Worth a ticket; not worth claiming.
- **Queue depth is API-only.** The poller runs where `/metrics` is served, which
  is right for today's deployment and would need revisiting if the worker ever
  exposed an endpoint of its own.
