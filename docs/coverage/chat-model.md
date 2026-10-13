# Workspace chat model — RIZ-97 record

> **Status, 2026-10-13: built and unit-gated; the spend gates are owed.** An admin
> picks the workspace's chat model in Settings → General → AI Models: Kimi K2.6
> (the default) or DeepSeek V4.1 Flash. Merge is gated on the paired eval and the
> endpoint probe below (§4, §5). Both are approved for QA and are not run here.

Ticket: [`RIZ-97`](https://linear.app/rizkal-uhuy/issue/RIZ-97/admins-can-choose-which-llm-model-their-workspace-uses).

## 1. What it is

The **chat model** is the primary tier. It writes every answer, for every agent
on every channel (dashboard, widget, WhatsApp, Slack, Discord, Lark, `/v1`).
The light, classifier, embedding and speech models stay deployment env, because
the guardrails are tuned to them.

There is no new storage. Migration `012` already had a per-company `primary` row
in `company_llm_credentials`, and `llmtenant.Resolver` already merged it over env
on every turn, field by field. Nothing could write it. The picker writes **only
`model`** on that row, so:

- the workspace keeps our key, our base URL and our interface, and runs on
  Argentum's OpenRouter account;
- credits keep charging our balance, because `hasOwnPrimaryKey` counts a row as
  bring-your-own only when it carries a key (`credits.go`);
- `Upsert` moves `updated_at`, which is the resolver's version, so each
  process's `ClientCache` rebuilds that tenant's client on its next turn. No
  restart is needed and no cache is called.

**Choosing the default deletes the row** (R4), so the workspace follows any later
change to `LLM_MODEL`. Saving the model the workspace already runs writes nothing.

A row that carries **its own provider** (any of `api_key_encrypted`, `base_url`
or `interface`) is support's. The card shows it read-only with an *Own provider*
badge, and `PUT` answers `409 own_provider` before anything is written.

## 2. The allow-list

`app.ChatModels` in `internal/app/chat_models.go`:

| Model id | Label | Per 1M in / out | Hint |
| --- | --- | --- | --- |
| `moonshotai/kimi-k2.6` | Kimi K2.6 | $0.95 / $4.00 | The model Argentum is tuned and tested on. |
| `deepseek/deepseek-v4.1-flash` | DeepSeek V4.1 Flash | $0.15 / $0.60 | About a sixth of the cost per answer. Check a few of your usual questions after switching. |

The option equal to `LLM_MODEL` is marked default. If `LLM_MODEL` is not on the
list (a local stack on a Claude key), it is offered first, labelled by its id, so
a workspace can always get back to what the deployment runs.

**Adding a model** takes three steps: a row in `modelPricing` (or
`TestEveryChatModelIsPriced` fails), an entry in `ChatModels`, and a paired `make
eval -models <kimi>,<new>` recorded in §4. Also add the id to `models:` in
`testdata/eval/golden.yaml` and `security.yaml`.

**Pricing source.** OpenRouter's `/models` list price, read on 2026-10-13, with no
markup (decision 2). Credits are the same micro-USD the meter computes, so a
workspace on DeepSeek spends credit at about a sixth of Kimi's rate with no
separate credit price table. Kimi's row ($0.95 / $4.00) is above today's list
price ($0.65 / $3.41). Refreshing it changes every tenant's bill, so it is its own
ticket.

## 3. The surface

- `GET /api/config/models` (member). `primary` is now the **caller's workspace's
  resolved** model, not `LLM_MODEL`. The chat page header reads it. A new optional
  `chat_model` block carries `model`, `default_model`, `locked` and `options`
  (label, hint, default, per-1K rates, `pricing_known`). If the row cannot be
  read, the route answers 500 rather than falling back to env, because the
  fallback would be the very misreport this ticket fixed. The card is then absent,
  as it always was on an error.
- `PUT /api/config/chat-model` (admin). Body `{"model": "…"}`. Answers 200 with
  the new setting, `400 model_not_offered`, `403` for a member (the role table),
  `409 own_provider`, or `503` on a router built without the service.
- No `/v1` route, no `openapi/v1.yaml` change, no migration. `make types`
  generated `ChatModelOption` and `ChatModelSetting` (domain) and `ModelsResponse`
  and `ModelInfo` (`handlers/wire.go`). The dashboard's `use-models.ts` no longer
  hand-writes the response type.
- Every change logs one `chat_model_changed` line with `company_id`, `user_id`,
  `from` and `to` (R10). There is no audit-table entry (a non-goal).

The card (`chat-model-card.tsx`) has its own Save, separate from Company
preferences', so one failing does not block the other. Members get it disabled
through the `AdminGate` around the General tab. A dashboard deployed ahead of its
API sees no `chat_model` block and renders the old read-only rows.

## 4. Eval: Kimi K2.6 against DeepSeek V4.1 Flash — owed

**The bar (AC11):** one paired run of
`make eval -models moonshotai/kimi-k2.6,deepseek/deepseek-v4.1-flash` on the
golden and security sets (`METABASE_URL=http://localhost:3000`). DeepSeek ships
if it is within 3 golden cases of Kimi **on the same run** and fails no security
case that Kimi passes. If it misses, the owner decides before merge.

| Set | Kimi K2.6 | DeepSeek V4.1 Flash | Δ | Meets the bar |
| --- | --- | --- | --- | --- |
| golden | — | — | — | owed |
| security | — | — | — | owed |

**Prediction:** DeepSeek lands 1–3 golden cases under Kimi. deepseek-v3.2 lost
six in `eval-q1.md`, and the Flash tier is smaller. The Kimi-specific
workarounds (the tool-call-leak guard, the `decart` block) were measured on Kimi
only, so a security regression would most likely show as a leaked tool call.

**Before running it:** check the demo warehouse on `localhost:5433`. On the
build machine, `internal/eval`'s `TestDemoFixtureIsReproducible` failed against
it on 2026-10-13: it held 2 products where the fixture has 30, and sums from the
old random-seeded volume. Every golden number is scored against that fixture, so
a run on a stale volume measures nothing. Reseed it first.

## 5. Endpoint probe — owed

Run the probe in [`provider-routing.md`](provider-routing.md) §4 against
`deepseek/deepseek-v4.1-flash`. It must show that at least one **ZDR** endpoint
serves the model (production sets `LLM_ZDR=true`), and that each endpoint returns
structured `tool_calls` rather than `functions.<name>:<n>{…}` in content. Add any
endpoint that leaks to `BrokenToolCallProviders` and record the results in
`provider-routing.md` §4a. If no ZDR endpoint exists, stop and tell the owner: a
DeepSeek turn would then fail on every workspace that picked it.

## 6. Where the plan was wrong, or silent

- **"The suffix fallback cannot match `v4.1-flash`."** It can.
  `lookupModelPricing` tries the last `/` before the last `.`, so
  `deepseek/deepseek-v4.1-flash` falls back to `deepseek-v4.1-flash`. The
  fallback that fails is a dotted name behind a `.` prefix (`vertex.gemini-1.5-pro`,
  pinned in its test). Both keys were added anyway, as every OpenRouter model in
  the table has, so the id each DeepSeek turn bills against is an exact match.
- **`llm_pricing_test.go`** does not exist. The lookup and per-model-rate cases
  went into the existing tables in `usage_pricing_test.go`.
- **Wire types.** The plan put the response structs in `internal/domain`. The two
  chat model shapes are there. The whole `/config/models` response is in
  `handlers/wire.go`, which is where `/api` bodies that are not entities live, so
  the dashboard could drop its hand-written mirror.
- **`pricing_known` on each option.** This field is additive and not in the
  plan's sample. A local stack's `LLM_MODEL` can be unpriced, and showing "$0.00"
  for it would read as free.
- **The open list shows the first sentence of each hint**, as the approved
  mockup does ("About a sixth of the cost per answer."). The full hint sits under
  the select once an option is chosen.
- **`security.yaml`** gained the model as well as `golden.yaml`. The ship bar
  covers both sets.

## 7. Rollback

Revert the PR, then return every model-only workspace to `LLM_MODEL`:

```sql
DELETE FROM company_llm_credentials
 WHERE tier = 'primary'
   AND api_key_encrypted IS NULL AND base_url IS NULL AND interface IS NULL;
```

Deploy workers and the API before telling admins about the picker. An old pod
already honours a model-only row but has no DeepSeek price, so in the deploy
window it would bill DeepSeek at the default rate. The signal is a usage event
with model `deepseek/deepseek-v4.1-flash` at $0.005 per 1K input.

## 8. Gate

See [`delivery-log.md`](delivery-log.md) Phase 3cd.
