# T-18 · Launch hygiene — coverage

**Status: MOSTLY DONE 2026-08-03.** Five of seven items closed; one was closed
earlier the same day; the last needs LLM spend and is written down rather than
run.

## 1. The landing page stopped promising things (P-1)

The ticket asked to remove the Telegram claim. What the page actually claimed
was wider, and all of it went:

| Claimed | Reality |
| ------- | ------- |
| Telegram, Slack, Email channels | none of the three exist |
| BigQuery, Snowflake, MongoDB, Redshift | none exist; the driver registry makes them additive and nobody has asked |
| "Web widget" | `T-19`→`T-23`, not built — the dashboard is what exists |

The integrations grid now lists what ships: dashboard, WhatsApp, Discord,
Lark, and the API/MCP surface. Databases are Postgres, MySQL and SQL Server.
The comment above the arrays says the rule — **anything added here needs a
driver or a channel behind it** — because a landing page that names an
integration is a promise a salesperson repeats, and Telegram was the one a
customer would try first since it is the cheapest to set up.

The hero and the feature grid also carried the old story. The hero now leads
with the thing that is actually new — *it tells you when a number moves before
you go looking* — and the "Schedules + automations" card became **"It tells you
first"**, which is the watchers messaging the ticket asked for and the headline
capability as of `T-08`/`T-09`.

## 2. Down migrations for 001–014 (Q-7)

Written, not documented-as-irreversible. All 46 migrations now have a `.down.sql`,
and each says what reversing it costs, which is the part worth having:

- `003` drops the metering ledger and the credit balances — **not reconstructible
  from anywhere else.**
- `007` drops the document index but not the objects, so reversing it orphans
  every file in the bucket.
- `011` drops the table-picker embeddings; re-applying means re-embedding, which
  is model spend rather than a schema change.
- `012` drops every tenant's own LLM credentials — encrypted secrets a tenant
  typed that we cannot reproduce, and a billing change as well as a schema one.
- `013` is the interesting one. Its up migration *removed* an ivfflat index
  because, built on an empty table, its centroids are degenerate and every query
  returns roughly one hit. The down restores it — restoring the bug — and says
  so in the file, because a down that silently does nothing is worse than one
  that faithfully returns to a state somebody chose.
- `001` and `011` deliberately leave `pgcrypto` and `vector` installed: an
  extension is database-level, another schema may be using it, and dropping one
  somebody else depends on is a worse outcome than leaving it behind.

## 3. `apps/backend/docs/` has an index

[`../../apps/backend/docs/README.md`](../../apps/backend/docs/README.md). The
ticket asked for the WebSocket event schema, the tool contracts and API docs for
metrics/watchers/actions/api-keys to be written there. What that would have
produced is a second copy of five documents that already exist — and the failure
mode of a second copy is this repo's most frequently repeated lesson (design
tokens, hand-written types, the OpenAPI spec).

So the index points at the canonical record for each, and states the rule: one
document per fact, and that file says which one. The two things genuinely not
visible from a tool's own declaration — that every call is audited and metered,
and that every call is budget-guarded — are written down there rather than
linked.

`api.md`'s staleness is now stated in the index as well as at the top of the
file itself.

## 4. Both READMEs

`apps/backend/README.md`'s diagram predated Discord, Lark, SQL Server, watchers
and the tenant MCP tools; it now carries them, plus the two processes that hang
off the same stack (`cmd/discord`, `cmd/mcp`) and the sentence that makes the
shared-stack point: everything above `cmd/` is built by `internal/bootstrap`,
which is why the eval harness scores the same agent the worker runs. Go 1.25 →
1.26, the SQL Server driver, and a dashboard path that pointed at the
pre-monorepo repository were all fixed with it.

The root `README.md` already existed (`T-00b`) and needed only `cmd/mcp` in the
layout.

## 5. `feature-coverage.md` refreshed

Done earlier the same day, and it needed more than a sprint-end pass: three rows
in *Agent capability* had said `❌` since the file was written and had been
shipped and gated the day before. Recorded in the delivery log's Phase 2d.

## 6. Not done: the final eval run

`docs/coverage/eval-sprint1.md`, compared against baseline. It needs a live
stack and a full run of the golden set — real model spend — and is the last
item in [`live-gate-backlog.md`](live-gate-backlog.md) §2 that has not been run.

It should be run **after** `T-07b`'s before/after pair, not instead of it: that
pair measures one change against itself, and this one measures the sprint. Doing
the sprint run first and the guardrail pair second would leave the guardrail
question answered against a moved baseline.
