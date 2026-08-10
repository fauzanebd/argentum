# Sprint 1 — "From Answering to Acting"

**Window:** 8 weeks from kickoff (planning date 2026-07-26)
**Team:** 1 human (repo owner) + AI agents
**Theme:** make Argentum agent-native — it notices, it acts, other agents can call
it, and it drops into any site the customer already runs.

---

## 1. The goal, stated as an outcome

> A company connects Argentum once. From then on, nobody has to remember to ask
> anything. Argentum watches the numbers that matter, says something in the
> team's chat when one of them moves, proposes what to do about it, and — with
> one approval — does it. Other agents can call it for the same answers.

Eight weeks is not enough to fully deliver that. It **is** enough to deliver the
first honest version of each shift, on rails safe enough to put in front of a
customer:

| Shift                     | Sprint 1 deliverable                                                        |
| ------------------------- | --------------------------------------------------------------------------- |
| **Pull → push**           | Watchers: metric-condition triggers that fire an agent turn into any channel |
| **Answer → act**          | Action framework with human-in-the-loop approval + two real actions          |
| **Product → substrate**   | Scoped API keys + an MCP server exposing the agent's tools                   |
| **Product → everywhere**  | Embeddable chat widget: a script tag or npm component on the customer's own internal site |
| **Answer → deliverable**  | Enterprise report system: branded PDF **and** PowerPoint from one spec, on the dashboard's design system |
| **Product → component**   | Public `/v1` API: the tenant's own application asks for a report or an answer over HTTP, with an OpenAPI spec and Node/Python SDKs |

Underneath all six: an **eval harness**, an **audit log**, a **metric
registry**, and **enforced spend limits** — because none of the above is safe to
ship without them.

**That table is the original ambition, and it is no longer the sprint.** Two
owner-set priority inserts have landed since it was written — the report track on
2026-07-27 and the API track on 2026-07-28, both ahead of everything else. Six
shifts do not fit in eight weeks; what fits is **answer → deliverable** and
**product → component**, on the foundation, which is what §6 now commits to.
**Pull → push** (watchers), **answer → act** (actions) and **product →
everywhere** (widget) move to Sprint 2. The rows stay in this table because they
are still the product thesis — they are just not all Sprint 1's deliverables.

**And underneath those: an agent that does not invent numbers.** The `T-00` smoke
test caught it reporting `$1,234,567.89` against a true 3,863,405,700 after
exhausting its 3-iteration budget
([`../coverage/environment-notes.md`](../coverage/environment-notes.md) C-1).
Everything above automates whatever the agent says. That fix (`T-16`) and the
metering fix (`T-02c`) run first, ahead of the report track and ahead of the rest
of the foundation work.

## 1a. Why the widget is in scope

The customer already has an internal website — a React admin panel, an ops
dashboard, an intranet. Today, using Argentum means leaving that site. The widget
removes that step, and it changes the adoption story in three ways:

- **Distribution.** One script tag reaches everyone who already uses the
  customer's internal tools. No per-user onboarding, no new bookmark, no training.
- **Context.** The agent shows up where the work happens, next to the numbers the
  staff were already looking at.
- **Stickiness.** A widget embedded in a company's own internal tooling is
  infrastructure. It does not get churned casually.

It is also the cheapest way to serve a company whose staff will never open a
second dashboard, and there is no reason to make them.

**Audience is locked: the tenant's own staff on the tenant's own site.** Identity
comes from the tenant's backend via an HMAC signature; the widget never
authenticates anonymous visitors. Public-facing embedding is a materially larger
security problem (anonymous sessions, abuse control, aggressive redaction) and is
deferred to Sprint 2 — see [`backlog.md`](backlog.md).

**Scope is locked: chat only.** No dashboard rendering, no watcher feed inside
the widget. Both are additive later against a working embed surface.

## 1b. Why the report system is in scope

**Added 2026-07-27 at the repo owner's request.**

`generate_document` already ships PDF, XLSX and CSV. The PDF is a stock maroto
document: default Helvetica, no cover, no header or footer, no page numbers, no
logo, no charts, no locale-aware numbers, and tables whose columns are the
12-unit grid divided evenly regardless of content.

That artifact is the one thing that leaves the product. Nobody forwards a chat
thread; they forward the file. It is simultaneously the most-shared surface and
the least designed one, and a correct number inside an unbranded document reads
as a prototype.

PowerPoint is in scope alongside PDF because the two are the same work once the
spec is right. A deck is a projection of the same content model, not a second
content model — and a deck is what gets shown in the meeting the PDF was
attached to. Building the spec for one format and retrofitting the other later
costs more than building both against it now.

It also compounds with the rest of the sprint: once watchers (`T-08`) and
`send_message` (`T-12a`) exist, "a branded weekly deck lands in Lark every
Monday morning" is configuration rather than a feature.

**The cost is honest and it is large:** 10 days, which does not fit. See §6 and
the effort roll-up in [`01-tickets.md`](01-tickets.md) — the widget phase moves
to Sprint 2 to pay for it.

**It runs second, not first.** The owner's insert put it immediately after the
re-warm; the `T-00` smoke test then found the agent fabricating figures. A
beautifully branded document containing an invented number is a worse product
than an ugly one containing a real number, so `T-01`/`T-02c`/`T-16` go ahead of
it. That is six days, not a re-prioritisation of the track itself.

## 1c. Why the API track is in scope, and first

**Added 2026-07-28 at the repo owner's request, as the sprint's highest
priority.**

The customer already has an application. Today the only way a report leaves
Argentum is a human opening the dashboard, asking a question, and clicking a
link. That makes Argentum a **destination** — something staff have to remember to
visit. An API makes it a **component**: their nightly job asks for the monthly
deck, their admin panel grows a "Download report" button, their internal tool
asks a question inline and renders the answer in their own interface.

`../coverage/api-surface.md` observation 3 states the blocker in one line: **no
machine authentication exists.** All 61 routes require a human-session JWT.
Nothing can integrate with Argentum at all right now, by anyone, for any price.

This is the same bet as the widget and MCP — reachable from outside the
dashboard — placed on the cheapest of the three consumers:

| Surface | Needs from the customer | Serves |
| ------- | ----------------------- | ------ |
| Widget (`T-19`→`T-23`) | their frontend team, an HMAC signing endpoint, a deploy | their staff, in their UI |
| MCP (`T-14`) | them to be running an agent | other agents |
| **API (`T-A1`→`T-A5`)** | **one backend developer and a key** | **their product** |

And for the request the owner actually described — get a generated PDF or Excel
file out of Argentum — the call originates on a server, not in a browser. The
widget could not serve it at all.

**The flagship is `T-A2`, and it ships two doors.** `POST /v1/reports/render`
takes a report spec and returns a file: no LLM, no thread, sub-second,
deterministic. `POST /v1/reports` takes a prompt, runs a real agent turn, and
returns the document when it is done. Those are different products — different
latency, cost, and failure modes — and collapsing them into one endpoint with a
mode flag would be two endpoints wearing a coat.

**The cost is 10.5 days of new work plus a re-ordering.** `T-13` (API keys) moves
out of week 5 to sit immediately before it, and the foundation the API cannot
safely ship without — `T-02`, `T-03`, `T-04`, `T-05` — has to land first. Those
9 days were always in the plan; the API track moves them earlier, it does not add
them. What it does add is the reason phases 2–6 no longer fit. See §6.

## 2. Why this sequence and not another

The dependency chain from [`../research/03-gap-analysis.md`](../research/03-gap-analysis.md):

```
Phase 0  Environment re-warm, then monorepo consolidation      (T-00, T-00b) ✅
         └─ Re-warm first so a breakage is attributable to drift, not to the move.
            Consolidate before any feature work, because every ticket after this
            touches file paths. Never mid-sprint. Both landed 2026-07-26.
Phase 1  Evals, then the two observed P0s                (T-01, T-02c, T-16) ✅
         └─ The smoke test found the agent fabricating a figure under budget
            exhaustion and recording zero usage for the primary model. Evals come
            first because they are what proves the other two fixed anything, and
            because every downstream week automates whatever the agent says.
            All three landed 2026-07-27. Asked the C-1 question, the agent now
            answers IDR 3,863,405,700 — the true value — and states what it
            retrieved before stating it.
Phase 1a Report system: design tokens ✅ → PDF v2 ✅ → charts ✅ → PPTX ✅ → tenant branding
         └─ Owner-set priority, inserted 2026-07-27. The artifact that leaves the
            building. After T-00b because it creates a new shared package and
            touches two apps; after phase 1 because a branded document with an
            invented number in it is the worse failure.
            T-R1 landed 2026-07-27: one tokens.json generates the dashboard's CSS
            variables and the backend's Go report theme, CI fails on drift, and
            Space Grotesk is embedded in every PDF instead of Helvetica.
            See coverage/design-tokens.md.
            T-R2 landed 2026-07-27: cover, running header, "Page N of M" in the
            document's own language, numbered sections, KPI cards, callouts, and
            tables whose columns are measured against their content. Formatting
            moved out of the model — a cell carries a value and a type, and the
            renderer decides how it reads. v1 specs are unaffected.
            See coverage/report-rendering.md.
            T-R3 landed 2026-07-28: seven chart types drawn in Go at 200 DPI on
            the token palette, one image for both the PDF and the deck. The
            colour-vision gate the ticket asked for found the palette's own
            green ΔE 5.0 from the brand red under deuteranopia and it is now an
            azure; `make palette` is that gate and CI runs it.
            See coverage/report-charts.md.
            T-R4 landed 2026-07-28: the same spec renders as a 16:9 deck, with
            the model's prose in the speaker notes and its lead sentence as the
            bullet. The OOXML is written by hand, so the deck is byte-identical
            between runs by construction. Three packages came out of the PDF
            renderer — measure, layout, labels — so the two renderers cannot
            disagree about how wide a column is or what "Prepared for" is in
            Indonesian. LibreOffice converts every fixture in CI; the
            PowerPoint / Keynote / Google Slides half of the gate is still
            outstanding and is recorded as such.
            See coverage/report-deck.md.
Phase 1b Tests ✅ + CI gate ✅ + RBAC ✅ + audit log ✅ + credit enforcement ✅ + generated types
         └─ You cannot ship autonomy on an unmeasured, unbounded, unaudited system.
            Also fixes the three P0 security/billing findings, which are cheap now
            and expensive after you have users. T-03 waited on T-02c: a budget
            check gating on an always-zero number is worse than none — and it
            then found a second always-zero number, the grant nothing had ever
            written. See coverage/credit-enforcement.md.
            Now also the gate on phase 1c: an API key is a credential a script
            holds, so RBAC, the audit log and the budget check stop being
            hygiene and start being the difference between a product and an
            incident.
            T-02 landed 2026-07-28: 21 of 49 packages have tests, every CRITICAL
            one among them, golangci-lint runs in CI at zero issues, and the
            dashboard is linted for the first time. It found that scheduled
            tasks with a non-UTC timezone cannot work in the deployed images —
            alpine has no zoneinfo and nothing imported time/tzdata — plus two
            unchecked type assertions, a guardrail rule that can never fire, and
            a flaky determinism test in the deck renderer.
            See coverage/test-coverage.md.
            T-04 landed 2026-07-28: 26 routes gated by a policy table the
            router's own route list is diffed against, plus team invites and an
            account lifecycle that ends a removed user's sessions.
            See coverage/rbac.md.
            T-05 landed 2026-07-28: every tool call the agent makes leaves one
            append-only row — actor, channel, redacted arguments, status, rows,
            duration — written by a decorator over the tool registry, wrapped
            outside T-16's budget guard so a refused call records as blocked
            rather than as a false success. A turn a guardrail stops never
            reaches a tool, so those get a row of their own.
            See coverage/agent-audit.md.
Phase 1c API keys ✅ → /v1 foundation ✅ → reports over HTTP ✅ → chat over HTTP ✅ → SDKs
         └─ Owner-set highest priority, inserted 2026-07-28. Argentum stops being
            a destination and becomes a component of the customer's own product.
            T-13 moves here from week 5 because it is the only machine auth that
            exists. T-A2 is the flagship — a tenant's app asks for a PDF or an
            Excel file and gets one — and T-A4 is what makes an integrator
            finish without talking to us.
            T-13 landed 2026-07-28: scoped, hashed, revocable keys, a per-key
            rate bucket, a Settings tab, and a `/v1` namespace that refuses a
            dashboard JWT as flatly as `/api` refuses a key. The hash is a
            SHA-256 rather than Argon2id — the input is 256 random bits, not a
            password, and the KDF's 64 MiB would land on every request of a
            machine API. The live gate found `/v1` inheriting the dashboard's
            permissive CORS headers, which is how an API key ends up usable
            from a browser. See coverage/api-keys.md.
            T-A1 landed 2026-07-28: the shape every `/v1` route inherits —
            a request id on every response and in every audit row, the typed
            envelope, idempotency whose records hold ids rather than payloads,
            rate-limit headers on success as well as on the refusal, cursor
            pagination, a kill switch, a body cap, and the `api` channel both
            T-A2 and T-A3 need. The live gate reordered the middleware: with
            the kill switch above the request id, the 503 an integrator is
            most likely to ask about went out with nothing to quote. Four
            acceptance items have no route to exercise them until T-A2's
            first POST and are recorded as tested-not-live.
            See coverage/api-foundation.md.
            T-A2 landed 2026-07-28: a spec in and a file out, or a prompt in
            and a real agent turn, plus the documents both produce and three
            ways to collect an asynchronous one. See coverage/api-reports.md.
            T-A3 landed 2026-07-28: a question in and an answer out, streamed
            over SSE on the event names the dashboard already receives or
            waited for on a capped synchronous door. The event schema is
            written down for the first time, which closes api-surface.md
            observation 4. The live gate found that `last_message_at` and
            `messages.created_at` are written by two different clocks — 130µs
            apart, in the wrong direction — so attaching to a settled thread
            held the connection open for an answer already in the database.
            See coverage/api-chat.md.
            T-A5 landed 2026-07-30 and closes the phase: per key, what it called
            and how it failed — the last 50 non-2xx responses carrying the
            request id the caller was handed — plus GET /v1/usage for an
            application metering its own users, and per-route latency histograms
            on /metrics. The ticket's own precondition was false: "/metrics is
            secured by T-05" — T-05 was the audit log and secured nothing here,
            T-17 owns that — so the new per-key labels are gated on
            METRICS_TOKEN rather than published on an open endpoint. One row per
            request was the wrong schema; 032 is a bounded hourly rollup plus
            failures-only detail, and the gate's 18 requests produced 5 counter
            rows. See coverage/api-observability.md.
Week 2   Metric registry + query_metric tool
         └─ Watchers need something authoritative to watch. Without this, an alert
            fires off a number the LLM re-derived, and the first false alarm
            destroys trust permanently.
Week 3   Watchers → proactive delivery to any channel
         └─ THE WEDGE. This is the week that changes how a company works.
Week 4   Action framework + approval flow + first two actions
         └─ Needs the audit log (W1) and a trusted trigger source (W3).
Week 5   API keys + MCP server + outbound webhooks
         └─ Independent of W3–W4; deliberately later because it is the least
            risky to cut if weeks 1–4 overrun.
Week 6   Reasoning depth, observability, hardening, launch prep
         └─ Raise the 3-iteration ceiling only once tracing exists to see the cost.
Week 7–8 Embeddable widget: embed auth → widget channel → client → distribution
         └─ Last because it needs T-13's key primitives, T-05's audit log, and
            T-03's budget check. Also the safest place to absorb slippage: nothing
            else depends on it, so it can slide into Sprint 2 without stranding
            half-finished work elsewhere.
```

**Why not monetization first?** Billing enforcement (`T-03`) ships in week 1
because unbounded spend is a live risk. Plans, checkout, and invoicing do not,
because pricing a product whose main value hasn't shipped yet means repricing it
two months later.

**Why not more channels first?** Slack and Telegram are additive against an
existing abstraction — a known, low-risk week whenever a customer needs it. They
change no one's workflow. Watchers do.

## 3. Scope

### In scope

- **Monorepo consolidation** — three repos into one, history preserved via
  `git subtree`, `apps/` + `packages/` layout, one path-filtered CI pipeline
- **Generated TS types** from Go structs, with CI failing on drift
- **Shared design tokens** (`packages/design-tokens`) generating both the
  dashboard's CSS variables and the backend's Go report theme, CI-checked for drift
- **PDF renderer v2** — cover page, running header/footer with page numbers,
  vendored Space Grotesk, content-weighted column widths, typed and
  locale-formatted cells, KPI cards, callouts, table paging
- **Chart images** rendered in Go — line, bar, grouped/stacked bar, pie, donut,
  sparkline — one image shared by the PDF and the deck
- **PPTX deck renderer** from the same spec, with the narrative in speaker notes
- **Per-tenant report branding** (logo, colour, footer, legal name,
  confidentiality label) with a live preview in the dashboard
- **Anti-fabrication**: iteration budget replacing the hard cap, an explicit
  incomplete-answer path on exhaustion, and a guardrail rule against stating a
  figure no tool returned
- **Primary-model metering** on streaming turns, with a loud warning when a turn
  records no usage at all
- **Public `/v1` API** on its own namespace and contract: typed error envelope,
  request ids, `Idempotency-Key` replay, cursor pagination, per-key rate limits
- **Reports over HTTP** — a deterministic spec→file door and an agentic
  prompt→file door, PDF / XLSX / CSV / PPTX, with re-presignable download URLs
  and HMAC-signed completion callbacks
- **Chat over HTTP** — SSE streaming plus a capped synchronous door, threads keyed
  by the tenant's own user reference, `api` as a first-class channel
- **OpenAPI 3.1 spec** with a CI parity check in both directions, plus generated
  `@argentum/sdk` (Node) and `argentum` (Python) clients and a quickstart that CI
  actually executes
- Eval harness with a golden question set on the demo tenant
- Test coverage for all CRITICAL packages + CI gate that actually gates
- `AdminOnly` applied; team invite endpoint; `/metrics` secured
- Credit enforcement with graceful degradation
- Agent action audit log (`agent_actions`)
- Metric registry: schema, CRUD API, dashboard tab, `list_metrics` + `query_metric` tools
- Watchers: schema, evaluation loop, breach → agent turn → channel delivery, dashboard UI
- Action framework: registry, per-company permissions, approval cards, idempotency
- Two shipped actions: `send_message` (digest/broadcast) and `http_action` (generic authenticated outbound call)
- API keys: scoped, hashed, revocable, rate-limited per key
- MCP server (`cmd/mcp`) over the same auth + audit rails
- Outbound webhooks with HMAC signing and retry
- Iteration budget replacing the hard 3-iteration cap
- OTel tracing on turns and tool calls; Prometheus-format `/metrics`
- Fix PII-redaction over-reach and the system-prompt-leak false positive
- Embed keys with HMAC identity verification, origin allowlist, and short-lived
  session tokens
- `widget` channel + a deliberately minimal `/api/embed/*` surface
- Widget client as a new workspace member `apps/widget/`: framework-agnostic
  loader + iframe app, with shared chat components **extracted** into
  `packages/chat-ui` rather than copied from the dashboard
- `@argentum/widget` and `@argentum/widget-react` npm packages, versioned CDN
  build, four example apps, signing snippets in four languages
- Widget appearance/content configuration in the dashboard with a live preview

### Explicitly out of scope

| Deferred                          | Why                                                                |
| --------------------------------- | ------------------------------------------------------------------ |
| **The whole widget phase (T-19→T-23)** | Moved to Sprint 2 to pay for the report track. Nothing depends on it; it slides whole. See §6. |
| **Phases 2–6: metric registry, watchers, actions, MCP, hardening** | Moved to Sprint 2 to pay for the API track (2026-07-28). This is the expensive half of the decision and includes the sprint's original wedge. See §6. |
| Go SDK, hosted API docs site, API playground | Node and Python are where the demand is. Markdown in the repo until someone complains. **The docs site was filed in [`backlog.md`](backlog.md) on 2026-08-02** with a trigger and an estimate, because this row deferred three things and only the Go SDK had either — see the entry for why that matters. The playground stays here with neither, deliberately. |
| OAuth / per-end-user API tokens    | `/v1` keys are company-scoped machine credentials. Per-user identity in a browser is `T-19`'s embed key, which is a different threat model. |
| WebSocket transport on `/v1`       | The consumer is a server. SSE works through every HTTP library and proxy; a WS client in a backend is a reconnect state machine the integrator has to write. |
| Per-key spend caps                 | The company budget (`T-03`) is the limit in v1. Per-key caps need a quota model that belongs with pricing. |
| DOCX / Word output                 | PDF covers "send it", PPTX covers "present it". Word is for documents the recipient edits — a different job. |
| Natively editable OOXML charts     | Charts ship as images. Editable chart XML is ~5× the work and only matters when someone wants to change the data inside PowerPoint. |
| Headless-Chromium document rendering | Perfect CSS fidelity, but a ~300 MB browser in the worker image, a sandbox to secure, and ~1s per document. Generated tokens get most of the fidelity for none of that. |
| Report template gallery / WYSIWYG builder | The agent composes the spec. A template designer is a product of its own. |
| Public / anonymous widget mode      | Different security problem entirely: anonymous sessions, abuse control, hard data scoping, aggressive redaction. Sprint 2. |
| Dashboards or alert feed in the widget | Additive against a working embed surface. Chat first.            |
| Widget SSO / silent identity        | HMAC identity from the tenant's backend covers the internal-site case. |
| Plans, checkout, invoicing         | Price after the value ships. `T-03` caps spend in the meantime.     |
| Slack / Telegram / email channels  | Additive, low-risk, no workflow change. See `backlog.md`.            |
| New DB drivers (BigQuery etc.)     | Additive against the driver registry. Pull-driven by demand.         |
| Multi-agent / planner architecture | `T-16` raises the iteration budget; specialist agents **we** write need eval data first. This is the internal planner, not the tenant roster below. |
| **The tenant agent roster (`T-S1`→`T-S5`)** | Owner-set 2026-07-29: the customer creates their own Marketing / Ops / HR / Finance agents. **Scheduled for Sprint 2, not deferred** — 9.5d of written tickets that would have displaced `T-A5` and overrun if inserted here. **`T-S1`→`T-S3` were then built out of that order and gated live on 2026-07-30**, so this row is a schedule the tree does not match: **the whole 9.5d is done as of 2026-07-31** — `T-S5` and `T-S4` closed the track, both gated live the same day. `T-A5` — the ticket this row said the roster "would have displaced" — landed 2026-07-30 anyway, so nothing was displaced in the end; what happened is that Sprint 2 started early. Decide re-plan or note at sprint close — see [`../coverage/agent-roster.md`](../coverage/agent-roster.md) §0. |
| **Tenant MCP servers as a source (`T-M1`→`T-M4`)** | Owner-set 2026-07-29: the customer registers their own MCP server and their agents call its tools. **Scheduled for Sprint 2, not deferred** — 8.0d of written tickets, and it deps `T-S1`/`T-S2`, which are Sprint 2 themselves. **Not `T-14`**, which is the same protocol pointed the other way: `T-14` serves our tools to their agent, this consumes their tools into ours. |
| Forecasting / anomaly ML           | Watchers ship with threshold + delta comparators. Statistical anomaly detection is Sprint 2. |
| SSO / SOC2                         | No enterprise deal is blocked on it yet.                            |
| Native dashboard embedding         | Metabase share URLs are adequate.                                   |
| Frontend test framework            | Backend tests first; the dashboard is thin and visually verifiable.  |

## 4. Milestones and exit criteria

| Phase | Milestone             | Exit criteria (all must be demonstrable)                                                                             |
| -- | ------------------------ | ------------------------------------------------------------------------------------------------------------------- |
| 0 ✅ | **One tree**            | Single repo, all three histories blameable through the subtree boundary. Zero Go import-path changes in the migration diff. Both Cloudflare Pages previews deploy from the new roots. CI path-filters correctly per job, and `cmd/discord` builds in it for the first time. |
| 1 ✅ | **It admits what it doesn't know** | `make eval` prints a score over ≥30 golden questions. The exact C-1 question — "What were our total sales last month?" — returns the right order of magnitude or an explicit "I could not complete this", and never an invented figure. `/api/usage/summary` shows the primary model with non-zero tokens after one chat turn. **Met 2026-07-27.** The C-1 question returns the exact figure; a turn that runs out now says so in the reply ("the budget was exhausted before I could get the final sum") instead of inventing one. |
| 1a | **Worth forwarding**     | The same monthly-sales spec renders as (a) a branded PDF with a cover, a running header, `Page N of M`, a chart, right-aligned rupiah, and a repeating table header across 200 rows, and (b) a PPTX deck that opens cleanly in PowerPoint, Keynote, Google Slides, and LibreOffice with the narrative in speaker notes. Both derive their colours, type scale, and fonts from the same generated tokens as the dashboard, and CI fails if the two drift. A tenant logo and colour set in Settings → Reports appear in the next generated file with no redeploy. **PDF half met in full 2026-07-28:** cover, running header, `Page N of M`, right-aligned rupiah, a header repeating across 17 pages of a 200-row table, byte-identical between runs, and — since `T-R3` — a chart, on the same generated tokens as the dashboard and on a palette now gated against deuteranopia and greyscale. **Deck half met 2026-07-28 except the four-application check:** the identical fixture — same file, only `format` changed — renders as 11 slides with the narrative in speaker notes, byte-identical between runs, and LibreOffice 7.4.7.2 converts all five fixtures in CI. PowerPoint, Keynote and Google Slides cannot be driven from a headless runner; that check is outstanding and named in `coverage/report-deck.md`. Tenant branding is `T-R5`. |
| 1b | **Safe to change**       | CI fails on a failing test. All CRITICAL packages have tests. A Go struct rename without `make types` is a red build. Non-admin cannot rotate a DSN. A tenant at zero credits gets a clear refusal instead of a bill. **Half met 2026-07-28 by `T-02`:** all CRITICAL packages have tests (21 of 49 packages, up from 16), CI runs `go vet`, `golangci-lint` and `go test -race` and the tree is clean under all three, and a deliberate break was shown to fail the suite locally — the CI-run proof needs a push and is recorded as outstanding. **Half closed 2026-07-30:** run `30522320695` on `main` executed all three jobs green in 10m17s, so the pipeline demonstrably runs on a push. The other half — *"CI fails on a failing test"*, which is the criterion's actual wording — still has no red run behind it, because nobody has pushed a deliberate break. It stays outstanding, and it is cheap: one throwaway branch with a broken assertion. **`T-04` closed the RBAC half 2026-07-28** — non-admin cannot rotate a DSN, proven against a live API. **`T-05` closed the audit half 2026-07-28** — every tool call, and every guardrail-stopped turn, leaves an append-only row scoped to its tenant. **`T-03` closed the credits half 2026-07-28** — a tenant at zero gets a 402 and a plain sentence on every channel, with zero `usage_events` written, and a tenant on their own key is never blocked; it also had to ship the starting grant, because until now nothing had ever credited a company and "refuse at zero" would have refused everyone ([`../coverage/credit-enforcement.md`](../coverage/credit-enforcement.md)). **`T-02b` closed the last of it 2026-07-29** — the dashboard's four hand-written `types.ts` files are gone, `packages/api-types` is generated from the Go structs, and a renamed JSON tag was shown to fail `make types-check`, fail CI's regenerate-and-diff, and stop the dashboard compiling. The migration found seven live mismatches, including a `Thread.channel` union that had said `"whatsapp" | "dashboard"` since two channels before Discord ([`../coverage/generated-types.md`](../coverage/generated-types.md)). **Phase 1b is met.** |
| 1c | **Anyone can call it**   | A throwaway Node script holding an API key writes a branded PDF to disk in under 10 minutes, using only the published quickstart and no help from us. The same key streams a chat answer over SSE, and is rejected by every `/api` dashboard route. A retried request with the same `Idempotency-Key` bills once. Adding a `/v1` route without an OpenAPI entry is a red build. **The credential half is met 2026-07-28 by `T-13`**: a key authenticates `/v1`, is rejected by all 66 policed `/api` routes, and a dashboard JWT is rejected by `/v1` — proven against the real router in both directions. **`T-A1` met the contract half 2026-07-28**: the envelope, request ids, idempotency, rate-limit headers, cursor pagination and the kill switch are in, and `GET /v1/me` reports the key, its scopes, its rate limit and the tenant's credit position. **`T-A2` shipped the routes 2026-07-28**: a spec posted to `POST /v1/reports/render` comes back as a branded PDF — inline bytes or a presigned URL — and a prompt posted to `POST /v1/reports` runs a real agent turn whose progress streams over SSE and whose result arrives as a signed callback. "Bills once" is now proven **over the wire**: a replayed `Idempotency-Key` returns the same document with a re-presigned URL, a retry mid-render gets `409 request_in_flight`, and a changed body under the same key gets 409. **`T-A3` closed the chat half 2026-07-28**: the same key streams an answer over SSE — deltas, tool calls, a 15s heartbeat, and a `final` carrying the message and what the turn cost — and the synchronous door returns the same answer to the same question. A turn that outruns the wait answers 504 with the ids to resume from, and **keeps its idempotency key**, so the retry it invites replays instead of billing twice; proven over the wire, one question and one answer in the thread afterwards. **`T-A4` met the last of it 2026-07-29**: the contract is published as OpenAPI 3.1, served keyless at `GET /v1/openapi.json`, and a throwaway project reached a branded PDF on disk in **one second** from an empty directory using only the quickstart — four in Python — against a ten-minute budget. "Adding a `/v1` route without an OpenAPI entry is a red build" is now literally true, in both directions, and so are three checks the criterion did not ask for. **The criterion is met.** What the gate also found: `T-A2`'s agentic door is refused by our own injection guardrail four times in five, silently — `T-A2b`, **fixed the same day**: the report directive now travels in the system prompt for that turn, so the guardrail judges only what the caller sent, and the classifier keeps its teeth. The ten-run confirmation needs a live deployment and is outstanding. **`T-A5` closed the track 2026-07-30**, beyond the criterion rather than inside it: an integrator whose key gets a 403 at 11pm now reads the 403 themselves, in their own dashboard, with the request id their script was handed — proven live for a forced 403, five forced 429s and a forced 500, each id matching its `curl`, admin-only against a member session. `GET /v1/usage` gives their application the spend and the balance over a window it chooses. The ticket's parenthetical *"`/metrics` is secured by `T-05`"* was false — `T-05` was the audit log — so per-key labels there are gated on `METRICS_TOKEN` and route-level numbers, which name no tenant, are served as before ([`../coverage/api-observability.md`](../coverage/api-observability.md)). |
| 2  | **Authoritative numbers**| A metric is defined once in the UI; asking the same question twice in two threads returns the same number via `query_metric`. Eval score has not regressed. |
| 3  | **It tells you first**   | A watcher on a demo-tenant metric breaches and a WhatsApp/Discord message arrives, unprompted, containing the number and the agent's explanation. |
| 4  | **It does things**       | The agent proposes an action, an approval card appears, approving executes it, and `agent_actions` shows who approved what and when. Rejecting does nothing. |
| 5  | **Other agents call it** | Claude Code, configured with an Argentum API key, retrieves a metric through MCP. The call appears in the audit log attributed to that key. |
| 6  | **Shippable**            | A 5-step agentic task completes without hitting an iteration cap. A slow turn is decomposable in a trace. Landing-page claims match shipped reality. Eval score ≥ week-1 baseline. |
| 7  | **Embed rails hold**     | A forged signature, a wrong origin, an expired token, and a revoked key are each rejected with the right status. An embed token cannot reach a single dashboard or admin route. A widget turn streams an answer and shows up in `usage/by-channel` as `widget`. |
| 8  | **Anyone can install it** | A throwaway React app integrates the widget in under 10 minutes using only the published docs. Loader ≤15 KB gzipped. Theme change in the dashboard appears in that app without touching its code. |

## 5. Risk register

| Risk                                                    | Likelihood | Impact | Mitigation                                                                 |
| ------------------------------------------------------- | ---------- | ------ | -------------------------------------------------------------------------- |
| Week 1 foundation work eats week 2                       | **High**   | Medium | `T-01`/`T-02` are hard-capped at 6 working days combined. Ship 30 golden questions, not 100. |
| Metric registry design churns                             | Medium     | High   | Start with the narrowest useful shape: name, description, source, SQL template, grain. No dimensions/joins in v1. |
| False-positive watcher alert destroys customer trust      | Medium     | **High** | Every watcher requires a dry-run over trailing data before it can be enabled. Ship a per-watcher cooldown. |
| Approval flow UX is harder than the backend               | Medium     | Medium | Dashboard-only approval in `T-11`; chat-native approval cards deferred to Sprint 2. |
| MCP server duplicates the tool layer                      | Low        | Medium | `cmd/mcp` must import `internal/tools`, never reimplement. Reject any PR that copies tool logic. |
| Nine weeks of environment drift blocks week 1             | **High**   | Low    | `T-00` is a half-day environment re-warm before anything else.               |
| **Monorepo migration breaks a production deploy**         | Medium     | **High** | Cloudflare Pages is the exposed surface — both frontends deploy from it, and it has bitten this project before (`a715171`→`9e9899f`). `T-00b` requires a passing preview deploy per frontend **before** production is repointed. The three original repos are archived read-only, not deleted, so rollback is repointing a remote. |
| Monorepo migration silently changes Go imports            | Low        | Medium | `T-00b` keeps the module path unchanged, so the acceptance criterion is literally "zero `.go` content diffs in the migration commit" — verifiable with `git diff --stat`. |
| Phases 1 + 1a + 1b are 24 days of work                    | **Certain**| Low    | Stated openly in the roll-up rather than hidden. They will run ~5 weeks; everything downstream compounds off them, so it is the wrong place to rush. |
| Agent-executed work drifts from intent                    | Medium     | Medium | Every ticket carries an explicit verification gate. No ticket is done on inspection alone. |
| **Report track becomes an open-ended design exercise**    | **High**   | High   | "Enterprise-grade" has no exit condition, so `T-R2`/`T-R4` are gated on a **fixed fixture set** — monthly sales report, invoice, KPI summary, 200-row export — and on the four-application PPTX compatibility check. When the fixtures render correctly the ticket is done. Polish beyond that is a Sprint 2 item, not a reason to keep the ticket open. **Held for `T-R2` (2026-07-27):** it shipped against the fixture set and stopped. The eight-column export is left cramped rather than redesigned, recorded as a known limit. `T-R4` still carries the risk. |
| PPTX renders differently in Keynote / Google Slides       | **High**   | Medium | Hand-rolled OOXML gets read by four different implementations. **Partly retired 2026-07-28, and partly still open.** `T-R4` shipped against the failure mode rather than against the applications: no inherited placeholders, one blank layout, no table styles, every fill and rule explicit, fonts named with a substitution class — so there is nothing left for the three renderers to interpret differently. The CI job converts every fixture through headless LibreOffice, the strictest of the four, and all five pass. What is *not* done is opening a deck in the other three, because they cannot be driven headlessly; that acceptance item is outstanding and is stated in `coverage/report-deck.md` rather than quietly counted as met. |
| ~~Chart palette illegible in print or to colourblind readers~~ **Closed 2026-07-28** | **Observed** | Medium | It was not a risk, it was a defect already in the tree. `T-R3`'s verifier found series 8's green at ΔE 5.0 from the brand red under simulated deuteranopia — two series a red-green-deficient reader cannot separate at the width of a chart line — and a sweep found no green at any lightness that clears both floors against this palette. The eighth series is now an azure. The method (Brettel/Viénot LMS simulation + CIE76 for colour vision, CIE L\* for greyscale) is stated in `tokens.json` and enforced by `make palette` in the tokens CI job, so the residual risk — a future palette edit — now fails a build instead of shipping. |
| ~~Text overflows a slide and is silently clipped~~ **Closed 2026-07-28** | **Observed** | Medium | `T-R4` measures rather than budgeting characters: every block goes through `internal/report/measure` against 94% of its real box before it is placed, overflows to a `(cont.)` slide, and declares `normAutofit` on top of that. The first LibreOffice conversion proved the risk was real and not hypothetical — a one-line subtitle estimate came back on two lines with the brand rule drawn through it — so the cover, divider and closing slides were rebuilt on a fixed vertical grid whose bands do not move when an estimate is a line out. A test asserts every placed string against its box. |
| ~~Tokens drift back apart by hand-editing~~ **Closed 2026-07-27** | Medium | Medium | `T-R1` landed with two independent guards: the `tokens` CI job regenerates and runs `git diff --exit-code`, and `go test` compares `tokens_gen.go` against `tokens.json` directly, so a hand edit fails even when the tokens job does not trigger. The hand-written `:root` block was deleted, not left beside the generated one. The migration also showed the risk was already real: the old HSL values had drifted from the hex their own comments named. |
| **A branded report carries a fabricated number**          | **High if unfixed** | **Critical** | The exact reason the report track runs *after* phase 1. A stock-Helvetica PDF with a wrong figure is embarrassing; a branded, logo'd, board-ready one with a wrong figure is a lie with letterhead. `T-16` lands first, and `T-R2`'s fixtures render from real query results, never from LLM-narrated figures. **`T-R2` narrowed this further:** v2 cells carry raw values and the renderer formats them, so the model no longer retypes a figure on its way into a document — one fewer place for a digit to change. |
| ~~**The agent fabricates numbers under budget exhaustion**~~ **Closed 2026-07-27** | **Observed** | **Critical** | `T-16` landed: a four-dimension turn budget, an exhaustion message the model actually receives (as a tool result, because it never saw the old cap), an output check that replaces a reply stating a figure no tool returned, and a zero-row `run_sql` note for the second mechanism `E-5` found. `T-01`'s golden set is what keeps it fixed. Residual risk is now the reverse one, below. |
| A reply is blocked for a figure it legitimately holds | Medium | Medium | The new output check is blunt by design and could suppress a correct answer — the failure mode of every guardrail this project has had to narrow. It is scoped as tightly as the evidence allows (it fires only when no data tool returned a row) and every block is logged at Warn **with the full blocked reply**, because tuning it is impossible without the text. If false positives appear, narrow it against a golden case, not by eye. |
| **Spend is invisible on the default provider**            | **Observed** | **High** | Primary-model streaming turns record no usage at all (`Q-12`). New ticket `T-02c` fixes it and blocks `T-03`, whose budget check would otherwise gate on a permanent near-zero. |
| A local `.env` points at production                       | **Observed** | **High** | The working `.env` had `DB_HOST` on a remote host while looking local; the smoke test nearly wrote test data to it. `.env.example` is now tracked (`Q-10`); add a startup warning when a non-production `ENV` targets a non-local `DB_HOST`. |
| ~~**Code works locally and fails only in the container**~~ **Closed 2026-07-28** | **Observed** | **High** | `T-02`'s cron tests found that `time.LoadLocation` reads `/usr/share/zoneinfo`, which `alpine:latest` does not ship and nothing installed — so every scheduled task with a non-UTC timezone was rejected in production and worked on every developer machine. This is the general class, not the instance: the deployed images are near-empty and anything the standard library reads off the host filesystem is a candidate. The instance is fixed by a `time/tzdata` blank import with a test that removes the host lookup; the class is mitigated by the same rule the fix follows — depend on what is compiled in, not on what the base image happens to carry. |
| ~~**`/metrics` is on the public router**~~ **Closed 2026-08-03** | **Observed** | Medium | New 2026-07-30, raised by `T-A5` and *not* fixed by it. The endpoint has never had a credential — it is in `cmd/api/policy.go`'s `unpolicedPaths` — and `T-A5`'s ticket asserted the opposite (*"`/metrics` is secured by `T-05`"*), which is how a false precondition nearly shipped tenant key ids onto an open endpoint. What landed instead: route-level numbers, which name no tenant, were served as before, and the new per-key labels required `METRICS_TOKEN`. **The residual risk this row named — model spend, queue depth, token totals — is now closed** by `T-17`'s first bullet in its second form (*"or require an admin JWT / metrics token"*): with `METRICS_TOKEN` set the endpoint answers to the token or `401`, and with it unset it answers on loopback only and `404`s everyone else. Loopback is the socket peer, never `X-Forwarded-For`. The internal listener and Prometheus exposition remain `T-17`'s, and are now a format problem rather than a disclosure one ([`../coverage/api-observability.md`](../coverage/api-observability.md)). |
| **A leaked API key spends a tenant's credits**             | Medium     | **High** | A key sits in someone else's CI config, and a `for` loop over `POST /v1/reports` is an unbounded LLM bill. Four layers, none optional: `T-03`'s budget check inside the `/v1` chain returning a typed 402, per-key rate limits on a separate bucket, hashing with the plaintext shown exactly once (`T-13`), and per-key usage visible to the tenant (`T-A5`) so they notice before we do. **Three of the four landed 2026-07-28 with `T-13`; the hash is SHA-256, not the Argon2id this row used to name.** Argon2id defends a password — a low-entropy input with a dictionary behind it. The secret half of a key is 256 uniformly random bits, so the KDF buys nothing against guessing while costing ~64 MiB and ~50 ms on *every authenticated request* of a machine-to-machine API, and handing anyone holding a valid prefix a 64 MiB allocation per wrong guess. The property this row wanted — a dump of `api_keys` is not replayable — holds either way. Argued in full in `coverage/api-keys.md` §1. |
| **`/v1` freezes a shape we regret**                       | **High**   | Medium | A public contract is a promise, and the first customer writing against it makes it permanent. Mitigation is scope, not process: ship the narrowest surface that does the job, keep `/api` unversioned so first-party dashboard churn never touches `/v1`, and state the additive-only policy in `T-A1` before the first key is issued. **`T-13` opened the namespace on 2026-07-28 with one route**, `GET /v1/me`, chosen because it is the one route `T-A1` cannot change the meaning of. **`T-A1` fixed the contract the same day** — envelope, idempotency, pagination, request ids — and wrote the additive-only policy into `internal/transport/http/apiv1`'s package doc, where the next person to add a field reads it rather than into a plan document they may not open. The residual risk moved to `T-A4`, and **`T-A4` closed it on 2026-07-29** — the spec is bound to the router by four checks, so the promise and the routes cannot part company without a red build. |
| **A `/v1` route ships without a scope on it**             | Medium     | High   | New 2026-07-28. `T-04` made "which routes are privileged?" enumerable by putting roles in a table a test diffs against the router. Scopes cannot work that way — they are per-key, and `RequireScope` is a middleware beside each route. So the equivalent guarantee is a review rule (*every `/v1` route names its scope*) plus the fact that an unscoped route reaches every key the tenant ever minted. `T-13` ships the gate with 47 test requests against it and **no production call site**; `T-A2` is the first, and is where a missing `RequireScope` first costs something. **`T-A1` added `write:reports` and `read:documents` to the vocabulary ahead of their routes** — deliberately, because scopes are fixed at a key's creation and a scope that arrives with its route forces every existing key to be re-minted. **`T-A2` is the first real call site, 2026-07-28**, and it brought the guarantee the row said could not exist: `TestEveryV1RouteNamesAScope` authenticates as a key holding *no scopes* and requires a 403 from every `/v1` route but `/v1/me`. That is a behavioural equivalent of `T-04`'s table diff — it cannot be read out of a built router, so it is sent through one. The residual risk is now a route added without a test run, not a route added without a scope. |
| **An untrusted spec takes down the renderer**             | ~~Medium~~ **Closed** | High   | A spec arriving over HTTP is untrusted in a way the agent's own spec never was. maroto will attempt to lay out 500 000 rows. **`T-A2` closed this 2026-07-28**: rows (across the whole document, not per table), columns, string length, sections and chart points are all capped and rejected **before** a renderer is reached — proven by a test that asserts nothing was uploaded, which is the only way to show the check ran first. A sync render over `API_V1_SYNC_RENDER_TIMEOUT` becomes a 202. The renderers never check for cancellation, so the timeout abandons the goroutine rather than stopping it; the work is paid for twice, only by specs pathological enough to overrun 20 seconds. |
| **The OpenAPI spec drifts from the routes**               | ~~High~~ **Closed** | Medium | Exactly the failure the design tokens already had — two copies of one truth, disagreeing quietly. Same fix, and it is proven in this repo. **Closed 2026-07-29 by `T-A4`, with four checks rather than one**: route parity walks the gin tree in both directions; scope parity asserts `x-argentum-scope` behaviourally (the named scope must be both sufficient and necessary); schema parity reflects over the Go response structs so a renamed field fails; and a drift gate covers every artifact generated from the document. The residual risk is the prose, which no test can check. |
| ~~**A sync `/v1/chat` call dies behind a proxy**~~ **Closed 2026-07-28** | Medium | Low | `T-A3` shipped it as designed and the gate's own turns showed why the row existed: 58s and 130s for two ordinary questions. The sync door is capped by `API_V1_SYNC_TIMEOUT_SECONDS` and answers 504 with `{thread_id, run_id}` and a sentence naming the stream to resume on; SSE with a 15s heartbeat comment is the documented default. Two things the row did not anticipate. The 504 has to **keep** its idempotency key — every other 5xx correctly forgets one, but here the turn is still running and still being billed, so the retry a 504 invites would start a second. And an SSE client hanging up cancels the request context the middleware completes its record with, which stranded the key `in_flight` for 24 hours; bookkeeping for work that has already run is now detached from the request. Do not raise the cap when someone complains — point them at the stream. |
| **A comparison between two writers' clocks decides something** | **Observed** | Medium | New 2026-07-28, from `T-A3`'s live gate. `conversation_threads.last_message_at` is written by the API process and `messages.created_at` by Postgres; they land ~130µs apart, in the wrong direction, and the code that asked "has this turn answered?" by comparing them held every settled thread's SSE stream open until the client gave up — for an answer already in the database. The instance is fixed by comparing two rows written by one clock (`LatestByThread`). The class is not: this codebase writes timestamps from both processes into the same tables, and any future predicate across them is the same bug. The rule is in `api-chat.md` §3 and in the code: never compare timestamps from two writers when you can compare two rows from one. |
| **A tenant MCP server URL is an SSRF into our infrastructure** | **High if unguarded** | **Critical** | New 2026-07-29 with the `T-M1`→`T-M4` track. The tenant types a URL and we fetch it from inside the worker. `http://169.254.169.254/`, `http://localhost:6379`, a public hostname whose DNS answers with an RFC1918 address, or a 302 from a legitimate host to any of those — each is a request to our own network with our own network position. Mitigation is `T-M1`'s gate, not a hardening pass: https-only outside dev, resolve-and-reject private ranges, pin the resolved address for the request, re-run the check on every redirect, one function with its own test table. It is written as a gate item because "we validated the URL" is the sentence every SSRF postmortem contains. |
| **A tenant MCP server prompt-injects our agent**          | **High**   | High   | New 2026-07-29. A tool description and a tool result are both text the tenant's server writes and our agent reads, in a context that can already run SQL against their warehouse. Two distinct vectors, and the second is worse: a description is reviewed by an admin once (`T-M1`'s approval screen) and pinned by digest, so a server that rewrites one shows as drifted; a *result* is fresh on every call and nobody reviews it. It goes through the same guardrail path as any tool output. This is not fully solvable — it is the same trust the tenant already extends by connecting a database — but the digest pin, the admin review, and read-only-by-default are what keep it from being silent. |
| **`T-14` and `T-M1` get confused for each other**         | **High**   | Low    | Two tracks, same protocol, opposite directions, adjacent names. Someone will cut one thinking they cut the other, or build the client and tick the server's acceptance boxes. Both tickets open by stating the distinction; the one-line test is **who holds the credential** — `T-14` authenticates their agent with our API key, `T-M1` authenticates us with their token. |
| **The push shift ships in neither sprint**                | Medium     | **High** | Watchers were Sprint 1's wedge and are now Sprint 2's, behind `T-19`. Two priority inserts have moved them once already; a third would strand them. The mitigation is a decision, not a mechanism: Sprint 2 opens with `T-19` and `T-08` and nothing is inserted ahead of them without explicitly writing down what slips. |
| **Embed auth flaw exposes tenant data**                   | Low        | **Critical** | `T-19` ships before any widget UI exists and is gated on a full forgery matrix: tampered signature, wrong origin, expired token, far-future expiry, revoked key. Constant-time comparison enforced by diff review. Mandatory origin allowlist, wildcard rejected. |
| Integrators copy an insecure signing shortcut              | Medium     | High   | `T-22` ships complete server-side snippets in four languages. The failure mode is a partial example, so examples are treated as security surface, not documentation. |
| Widget bundle bloats and the customer's frontend team removes it | Medium | Medium | Hard budgets in `T-21`: loader ≤15 KB gzipped, iframe app ≤80 KB. Preact + `marked`, not React + `react-markdown`. Sizes are a gate item, not an aspiration. |
| Widget phase slips past week 8                            | Medium     | Low    | Nothing depends on it. It slides to Sprint 2 whole rather than shipping half-integrated. |

## 6. Cut order

If the sprint slips, cut in this order. Do not improvise a different order —
each cut is chosen to preserve the dependency chain.

1a. ~~`T-A5` integrator-facing observability~~ — **removed from the cut list;
   landed 2026-07-30.** It was the cheapest cut in the sprint and it was never
   taken, which means the cut list now starts at position 1 with nothing above
   it already spent. Lettering retained so the positions below keep the numbers
   the tickets already cite.
1. `T-15` outbound webhooks (its delivery core, `internal/webhookout`, ships
   inside `T-A2` regardless — this cut is the subscription model, not the sender)
2. `T-14` MCP server. **Cheaper to cut than it was:** after `T-A1` an outside
   agent can already reach Argentum over `/v1`, so cutting MCP costs
   convenience rather than reachability. Keep `T-13` — it is now foundational,
   not a week-5 item.
3. `T-17` OTel tracing (keep the Prometheus endpoint fix)
4. `T-12b` `http_action` (keep `send_message` — it is what makes watchers useful)
5. ~~`T-16` iteration budget~~ — **removed from the cut list.** The `T-00` smoke test
   showed the 3-iteration cap makes the agent fabricate figures rather than admit it
   ran out of steps (`Q-5`; reproduction in
   [`../coverage/environment-notes.md`](../coverage/environment-notes.md) C-1).
   Shipping watchers or actions on top of an agent that invents numbers would only
   automate the fabrication.
6. `T-R5` tenant report branding UI — ship reports on Argentum's own defaults and
   let per-tenant branding follow. The renderer must already treat branding as
   optional, so this cut costs nothing structurally.
7. ~~`T-R3` chart types beyond line and bar.~~ **Removed from the cut list —
   landed whole 2026-07-28**, all seven types. The principle it carried stands
   for anything that replaces it: a report with no chart is a table with a cover
   page, which is not what was asked for.
8. `T-23` widget config UI (ship the widget with sane hardcoded defaults; the
   dashboard config tab can follow)
9. `T-22` npm packages and examples — but only down to the vanilla example and
   the Go + Node signing snippets. **Never ship the widget with no integration
   docs**; an undocumented embed surface invites the insecure shortcut.

**Never cut:** `T-00b` (monorepo), `T-01` (evals), `T-02c` (metering), `T-16`
(anti-fabrication), `T-R1`/`T-R2` (tokens + PDF v2), `T-R4` (PPTX), `T-02` (CI
gate), `T-04` (RBAC), `T-13` (API keys), `T-A1`/`T-A2`/`T-A4` (the API, its
flagship, and the thing that makes it usable), `T-06`/`T-07` (metric registry),
`T-08`/`T-09` (watchers), `T-19` (embed auth). Those are the sprint.

**Revised 2026-07-28: the problem is no longer 63 days, it is 26.0.** The
arithmetic that mattered on 2026-07-27 — cut down from 63 and slide the widget —
has been overtaken. What matters now is the working days remaining and what fills
them:

| What | Days | Cumulative |
| ---- | ---- | ---------- |
| Finish the report track (~~`T-R3`~~ ✅, ~~`T-R4`~~ ✅, ~~`T-R5`~~ ✅) | 1.5 | 1.5 |
| Foundation (~~`T-02`~~ ✅, ~~`T-04`~~ ✅, ~~`T-05`~~ ✅, ~~`T-03`~~ ✅, ~~`T-02b`~~ ✅) | 4.0 | 5.5 |
| **The API track (~~`T-13`~~ ✅, ~~`T-A1`~~ ✅, ~~`T-A2`~~ ✅, ~~`T-A3`~~ ✅, ~~`T-A4`~~ ✅, ~~`T-A2b`~~ ✅, ~~`T-A5`~~ ✅)** | 12.5 | **19.0** |
| Remaining budget | | **26.0** |

**Updated 2026-07-29 after `T-A4`.** The API track's *deliverable* half is done
too: the contract is published, both SDKs exist, and the quickstart's every code
block is a file CI runs against a live server. Phase 1c's exit criterion is met
in full for the first time.

The 2.5 days bought one thing the ticket did not ask for and one it did not
expect. The extra: four drift checks instead of one — a path-and-method diff
proves the routes are listed, and says nothing about whether `Document.filename`
is still called `filename`, so the response structs are diffed by reflection
too. The unexpected: five live runs of `POST /v1/reports` produced one document.
Four were refused by our own `semantic_prompt_injection` guardrail, because
`T-A2`'s report directive travels inside the user message and reads exactly like
an instruction override. The route answers 202 and completes with no document
and no error, which is the worst shape a failure can take on a flagship path.
That is `T-A2b`, 0.5d, never-cut — and it is why the nightly examples job is
expected red until it lands.

**`T-A2b` landed 2026-07-29**, the same day it was raised. The directive travels
as a per-turn system-prompt addendum, so the guardrail inspects only what the
caller sent; the classifier was not weakened, because admitting our own
instruction blocks would admit the real injections shaped like them. The
unit and eval halves are done and the live half — ten consecutive report calls,
and the nightly job green — needs a running deployment and is the one thing
still open on it ([`../coverage/api-reports.md`](../coverage/api-reports.md) §7).

**Updated 2026-07-28 after `T-A3`.** The product half of the API track is
done: a tenant's application can now ask Argentum for a document and for an
answer, and collect either. What remains — `T-A4` (OpenAPI, SDKs, a quickstart
CI actually executes) and `T-A5` (integrator-facing observability) — is what
makes it usable by somebody we never speak to, which is the whole point of
shipping it. `T-A4` is never-cut for that reason; `T-A5` is cut position 1a.
**Both have since landed** — `T-A4` on 2026-07-29 and `T-A5` on 2026-07-30 —
so the API track is complete and the cut list's cheapest position was never
taken.

**Updated 2026-07-28 after `T-A1`.** The 2.5 days that ticket was priced at are
spent, and the thing they bought is the half of the API track that cannot be
revised later: the error format, the idempotency contract, the pagination
style and the request-id chain are now fixed for every route `T-A2`→`T-A5`
adds. What is left in the track is routes, and routes are additive.

**Updated 2026-07-28 after `T-02`.** Three of the foundation's eight days are
spent, so the slack across the three phases is now 7.0 days rather than 4.0.
That is the most comfortable this plan has been since the first priority
insert, and it is worth naming why it should not be spent: the API track's
2.5-day estimate for `T-A1` covers an error envelope, an idempotency contract
and a pagination style that all become permanent the first time a customer
writes against them.

**Updated 2026-07-30: all three are done.** `T-A5` closed the API track, which
was the last committed phase, so Sprint 1's scope is delivered with the two
acceptance items named in phases 1a and 1c still owed (`T-R4`'s three
unautomatable applications, `T-A2b`'s ten live report calls). The sprint's
remaining budget is now uncommitted, and the only thing standing in it is
Sprint 2's roster track, which started early.

**Sprint 1 is now: finish the report system, build the foundation, ship the API.**
It fits — with 4.0 days of slack across three phases after `T-R4` landed on
2026-07-28, which is more comfortable than the 1.5 this table showed a day
earlier but is still a plan that has already absorbed two priority inserts. What pays for it is phases 2–6 — the metric registry,
**watchers**, actions, MCP and hardening, 23.0 days — moving to Sprint 2
alongside the widget phase.

**Say the expensive part out loud.** §2 calls week 3 *"THE WEDGE. This is the week
that changes how a company works."* It is not in this sprint any more. The
defensible reading is that reports and an API are things a customer can buy
today while watchers are a thing a customer has to be taught to want, and that
selling the first two funds the third. The indefensible one is discovering in
week six that the wedge quietly fell off the plan. It is written here so it
cannot.

Sprint 2 therefore opens with two never-cut items already queued — `T-19` (embed
auth) and `T-08` (watchers) — before anything new is considered.

**Updated 2026-07-29: three, not two.** The tenant agent roster (`T-S1`→`T-S5`,
9.5d) was made a Sprint 2 commitment by the owner on 2026-07-29 and its tickets
are written. Sprint 2 therefore opens with ~40.5 days already spoken for. Say it
now rather than let Sprint 2 repeat Sprint 1's arithmetic surprise: **something
in phases 2–6 will have to move again**, and that decision is better made against
a written roster track than against a discovery in week four.

**Updated again the same day: four.** Tenant MCP servers as a source
(`T-M1`→`T-M4`, 8.0d) was made a Sprint 2 commitment by the owner on 2026-07-29
and its tickets are written. The customer registers their own MCP server and
their agents call its tools; it is `T-14` pointed the other way, and it deps the
roster, so it lands behind `T-S2` regardless of priority.

**Sprint 2 now holds 52.0 days of committed work** — 23.0 (phases 2–6) + 11.5
(widget) + 9.5 (roster) + 8.0 (MCP-as-source) — before anything new is
considered, and before anyone has said how long Sprint 2 is. **Was `52.5` and
`23.5` until 2026-07-30 (§8a); 46.0 of the 52.0 is still ahead, since
`T-S1`→`T-S3` are delivered.** The paragraph above
says "something in phases 2–6 will have to move again"; with a fourth track that
is no longer a prediction, it is arithmetic. **Whoever opens Sprint 2 writes its
cut order first, before its first ticket** — Sprint 1's cut order was written up
front and is the only reason two priority inserts did not strand anything.
**Written 2026-07-30 in §8, three tickets late** — `T-S1`→`T-S3` had already
landed. They ran in dependency order and stranded nothing, so the cost was the
guarantee rather than the work.

**Updated 2026-07-30: 6.0 of those days are already delivered.** `T-S1`, `T-S2`
and `T-S3` are done and gated live, so the roster track has 3.5d left (`T-S4`
2.0d, `T-S5` 1.5d). That reduces whichever of the two figures below turns out to
be right by the same 6.0 — it does not settle which one it is.

**Updated 2026-07-31: the remaining 3.5 are delivered too.** `T-S5` and `T-S4`
both landed and both gates ran live, so the roster track is 9.5d complete and
none of it is left — see §8a.

**And the ~40.5 above does not reconcile.** The same set of tickets totals 44.5
by `01-tickets.md`'s own phase figures (23.5 + 11.5 + 9.5). One of the two is
wrong, neither has been checked, and the gap is 4.0 days — a working week's
worth of planning error sitting in the number Sprint 2 will be sized against.
~~Settle it at sprint close.~~ **Settled 2026-07-30 in §8a, and neither figure
survived: the answer is `44.0`.** `~40.5` is a 4.0-day arithmetic slip, and the
`23.5` both figures leaned on is itself 0.5 stale — `T-14` was re-estimated from
`2.5d` to `2d` and the phase table never followed. So the `52.5` above is
**`52.0`**, and less the three delivered roster tickets, **46.0 days remain
committed**.

`T-00b` is uncuttable for a scheduling reason rather than a product one: it moves
every file in the workspace, so it is only cheap **before** the sprint. Deferred to
week 4 it would invalidate every in-flight ticket's paths; deferred past the sprint
it never happens, and the widget ships duplicating the dashboard's chat components.

**Cut the widget phase whole, never partially.** If weeks 7–8 cannot finish,
`T-19`→`T-23` move to Sprint 2 together. A shipped embed endpoint with no client,
or a client with incomplete docs, is worse than nothing: the first is dead attack
surface, the second gets integrated insecurely.

## 7. Working agreement with AI agents

- Every ticket in [`01-tickets.md`](01-tickets.md) is written as an independently
  executable unit with its own verification gate.
- An agent claims one ticket, respects the stated dependencies, and reports in the
  format in [`../AGENTS.md`](../AGENTS.md) §4.
- Migrations are serialized — one claimant at a time on the next number.
- No ticket is complete without pasted command output from its gate.
- **No agent starts before `T-00b` lands.** It rewrites every path in the
  workspace; work started against the old layout is work thrown away.
- After `T-00b`, a ticket spanning backend and frontend is **one commit**. Two
  commits for one feature is now a review finding, not a necessity.

## 8. Sprint 2 cut order

**Written 2026-07-30, and late.** §6 says *"whoever opens Sprint 2 writes its cut
order first, before its first ticket."* Sprint 2's first three tickets
(`T-S1`→`T-S3`) landed on 2026-07-29–30 without one, so this is written against
three tickets already spent rather than against a clean sprint. Nothing was
stranded — the roster track ran in dependency order — but the guarantee §6 was
buying did not exist while it ran.

### 8a. The 40.5 / 44.5 gap, settled

§6 flagged two irreconcilable totals — `~40.5` and `44.5` — and said "settle it
at sprint close". Settled here instead, because Sprint 2 is about to be sized
against it. **Neither figure is right.** Summed from the tickets themselves rather
than from any phase table:

| Track | Days | Sum |
| ----- | ---- | --- |
| Phases 2–6 (`T-06`→`T-12b`, `T-14`, `T-15`, `T-17`, `T-18`) | **23.0** | 3 + 1.5 + 3 + 2 + 2.5 + 1.5 + 1 + 1.5 + 2 + 1.5 + 2 + 1.5 |
| Widget (`T-19`→`T-23`) | 11.5 | 2.5 + 2 + 3.5 + 2 + 1.5 |
| Roster (`T-S1`→`T-S5`) | 9.5 | 2.5 + 2.5 + 1 + 2 + 1.5 |
| MCP-as-source (`T-M1`→`T-M4`) | 8.0 | 2.5 + 3 + 1 + 1.5 |
| Business context (`T-B1`→`T-B4`) — **added 2026-07-31** | 8.5 | 2 + 2.5 + 2 + 2 |
| Video and animated decks (`T-V1`→`T-V5`) — **added 2026-08-09** | 11.5 | 2.5 + 3 + 2.5 + 2 + 1.5 |
| **Committed total** | **72.0** | |

Two separate errors, not one. **`~40.5` is a 4.0-day arithmetic slip** against its
own inputs — the three tracks it summed came to `44.5` by the figures available
when it was written. And **`23.5` for phases 2–6 is stale by 0.5**: `T-14` was
re-estimated from `2.5d` to `2d` in its own header and
[`01-tickets.md`](01-tickets.md)'s execution-order table never picked it up. The
correct three-track figure is therefore **44.0**, and **`52.5` becomes `52.0`**.

The lesson is the one the migration table keeps teaching in the same document: a
number copied into a summary stops tracking the thing it summarises. **Sum from
the ticket headers.**

**Less `T-S1`→`T-S3` (6.0d, delivered): 46.0 days remain committed** before
anything new is considered.

**Something new was considered the next day.** The business-context track was
owner-set on 2026-07-31 and added 8.5d, so the table reads `60.5` where this
section was written against `52.0`, and **54.5 days remain committed**. The
row is dated rather than folded in silently, because the paragraph above it is
about numbers that stopped tracking what they summarised. §8c is what it cost.
(The track was filed at `8.0` earlier the same day and re-summed to `8.5` when
`T-B4` grew from persona drafting to the specified Generate-with-AI flow — two
fields, create *and* edit, and an undo. Same lesson, four hours apart.)

**A sixth row was added on 2026-08-09** — video and animated decks
(`T-V1`→`T-V5`, 11.5d), owner-set, the fourth priority insert in the plan's
history and dated in the table for the same reason the business-context row is.
§8d is what it cost. The paragraphs below this line
were written against `60.5` and are left as they were written; the arithmetic
they lead to is carried forward in §8d, which states the remaining figure against
what is actually left rather than against a total that has been re-summed five
times.

**`T-S5` landed the same day (1.5d): 53.0 days remain committed.** It ran ahead
of `T-S4` because §8c's running order puts it there, and it takes row 10 of
§8b's list off the table with it — which also removes the "cut 10 only after 9"
knot, since `T-M3`'s dependency on it is now satisfied rather than pending.
Its gate ran the same day, with `T-S4`'s.

**`T-S4` landed the same day too (2.0d): 51.0 days remain committed**, and the
roster track is finished — 9.5d of it, all five tickets, against a row in §3
that still calls the track "scheduled for Sprint 2, not deferred". Row 8 of
§8b's list comes off with it. Both gates then ran on one stack the same
evening — `033` applied, every acceptance item exercised live — so **the roster
track is 9.5d delivered and gated**, with nothing owed.

**`T-B1` landed the same evening (2.0d): 49.0 days remain committed**, opening
the business-context track and applying migration `034`. It is a never-cut row,
so nothing comes off §8b's list with it; what comes off is the track's first
dependency — `T-B2`, `T-B3` and `T-B4` all dep it and none of them was startable
before tonight.

**What its gate found belongs to five earlier tickets at once.** The composed
system prompt — SQL rules, `T-16`'s anti-fabrication language, the formatting
contract, `T-S2`'s persona, `T-A2b`'s report directive — was being replaced by
`config/agents.yaml`'s role text before every request left the process, because
the SDK's `WithAgentConfig` assigns the prompt and was applied last. Fixed and
regression-tested in the same change. No estimate moves: those tickets shipped
what they claimed, in code that was being discarded at the last option.

**What the roster gate found belongs to nobody in this list.** The
`semantic_prompt_injection` guardrail refused two of seven ordinary questions,
which is the third appearance of a class fixed twice before (`3891579`,
`T-A2b`). It is `T-07b`'s, it is not filed as a track, and it now has a measured
rate rather than an anecdote — see
[`../coverage/agent-roster.md`](../coverage/agent-roster.md) §T-S4 §6. The same
section records the second finding — `docker-compose.yml` shipped no object
storage — and that it was fixed the same evening: `make infra` now starts a
MinIO, and `docs/api/examples/run.sh deterministic` passes whole for the first
time on a developer machine.

### 8b. The order

Cut from the top. One list, not four — the per-track markers (`#2a`, `#2b`,
`#3a`, `#3b`) were assigned inside their own tickets and **collided with the
positions Sprint 1's list already used**, which is the concrete cost of not
having written this in time. The markers are mapped here; where a ticket's own
header disagrees with this table, **this table wins**.

**Renumbered 2026-07-31** when the business-context track was inserted. Ten rows
became twelve and eight of them moved — `T-15` was 2 and is 3, `T-S5` was 8 and
is 10. Anything citing a bare number from the previous version of this table is
citing the wrong ticket; cite the ticket id.

| # | Ticket | Days | Old marker | What cutting it costs |
| - | ------ | ---- | ---------- | --------------------- |
| 1 | ~~`T-M4` write-capable MCP tools~~ — **delivered 2026-08-03** | 1.5 | `#3b` | Was: read-only tenant tools are the value; writes are the second product. Moot — it landed as one action registered in `T-10`'s framework rather than a write path of its own, so the dependency it was cut to avoid bought the exactly-once guarantee instead of costing one. The live gate is outstanding. |
| 2 | `T-B2` business inference from the source | 2.5 | — | The tenant types their industry and description into `T-B1`'s form instead of reviewing a draft. Four fields, once — this cuts the onboarding, not the capability, and `T-B4` still generates from whatever they typed. **Held position 7 until 2026-07-31**, when the owner specified the Generate-with-AI flow and `T-B4` became the primary create path; the two swapped. |
| 3 | ~~`T-15` outbound webhook subscriptions~~ — **delivered 2026-08-03** | 1.5 | Sprint 1 `#1` | Was: the delivery core shipped inside `T-A2`, and this is the subscription model rather than the sender. That is exactly what it turned out to cost — a table, a fan-out at three call sites, and a failure counter. The gate is outstanding. |
| 4 | ~~`T-14` MCP server (us as server)~~ — **delivered 2026-08-03** | 2.0 | Sprint 1 `#2` | Was: after `T-A1` an outside agent reaches Argentum over `/v1`, so this costs convenience rather than reachability. The re-scope held — it came in as an adapter over `internal/tools` plus two scopes, with no tool reimplemented and no audit code written. The gate and a Helm deployment are outstanding. |
| 5 | `T-17` OTel tracing | ~~2.0~~ **1.5** | Sprint 1 `#3` | Keep the `/metrics` half — and **the disclosure half of it was taken on 2026-08-03**: the endpoint requires `METRICS_TOKEN` when set and answers on loopback only when unset, which is the ticket's own second option and closes §5's row. What is left of that bullet is the internal listener, and of the ticket the Prometheus exposition format and the ServiceMonitor. Cutting the tracing still does not cut those. |
| 6 | `T-12b` `http_action` | 1.5 | Sprint 1 `#4` | Keep `send_message` — it is what makes watchers useful. |
| 7 | `T-B4` "Generate with AI" | 2.0 | — | The owner's specified create flow: type a description, press the button, get it improved plus a persona. Cutting it leaves `T-B3`'s six templates and the blank form — a good agent is still creatable, but the tenant who picks no template is back to writing a prompt, which is the behaviour this track exists to remove. **It deps `T-B1`/`T-B3` only**, so cutting `T-B2` at position 2 does not strand it. |
| 8 | ~~`T-S4` channel bindings~~ — **delivered 2026-07-31** | 2.0 | `#2a` | Was: the roster stays dashboard-only, and Discord/Lark/WhatsApp keep answering on the company default. Moot — a bound channel now answers as its own agent, and an unbound one still answers as the default, so the cut's own fallback is the shipped behaviour. |
| 9 | `T-M3` MCP legibility — **partially, never whole** | 1.0 | `#3a` | Cut the per-server usage breakout and the thread-view labelling. **Keep the agent↔server binding control.** `T-M1` creates servers and `T-M2` calls them, but the binding UI is in `T-M3`; cut whole, the track ships reachable only by writing `agent_mcp_servers` rows by hand. Same shape as Sprint 1's `T-22` cut. |
| 10 | ~~`T-S5` `agent_id` on `/v1`~~ — **delivered 2026-07-31** | 1.5 | `#2b` | Was: cutting it strands `T-M3`, which deps it, so cut 10 only after 9 is already gone. Moot — the dependency is satisfied, and `T-M3` can now be cut or kept on its own terms. |
| 11 | `T-23` widget config UI | 1.5 | Sprint 1 `#8` | Ship the widget on hardcoded defaults. |
| 12 | `T-22` npm packages and examples | 2.0 | Sprint 1 `#9` | Only down to the vanilla example and the Go + Node signing snippets. **Never ship the widget with no integration docs.** |
| 13 | `T-V4` the player and share links | 2.0 | — | The video ships as a file only: an mp4 in a chat message and a download, with no link that plays. Costs the "let me show you" half of the track and nothing structural — `T-V1`'s plan is stored either way, so the player is additive against a working renderer whenever it is wanted. |
| 14 | `T-V5` motion system and the agreement gate | 1.5 | — | **Partially, never whole.** Cut the scene polish and the still export; **keep the three-format agreement test**, which is the only automated proof that a figure reads the same in the video as in the PDF. Cutting that is cutting the check, not the polish — and locked decision 2 is what it checks. |

**Rows 13 and 14 are appended, not inserted, and their numbers are identifiers
rather than a position.** Renumbering is what §8b's own header warns against, so
the video track's place in the cut order is stated in §8d instead: **the whole
track is cut before anything already in this table.** Read the numbers as names.

**State of play, 2026-08-03.** Positions **1 through 5 are all delivered** —
`T-M4`, `T-B2`, `T-15`, `T-14`, `T-17` — as are 8 and 10, which were already
struck. Of the twelve, what remains unbuilt is `T-12b`'s cut (moot: it shipped),
`T-B4` (shipped), `T-M3` (shipped), and **positions 11 and 12, which are the
widget phase**. So the cut list has stopped being a list of things to drop and
become a list of things that landed.

**The widget phase (`T-19`→`T-23`, 11.5d) is the one track with no code.** It is
cut whole or not at all — §6's rule, carried forward — so it is not something to
start between other tickets; it wants a decision to open the scope, not an
afternoon. Nothing else in the plan is blocked on it, and
[`../coverage/feature-coverage.md`](../coverage/feature-coverage.md) records the
consequence honestly: *Embeddability ░░░░░░░░░░ — Argentum lives only in its own
dashboard and in chat apps.*

Everything else outstanding is a gate rather than a build, and all of it is in
[`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md).

**Updated 2026-08-08.** Two things happened that this section should not make a
reader re-derive. **`T-17` is finished and gated** — queue depth, the sub-tool
spans, the exposition scrape and the trace waterfall, which found a
span-parenting defect on the way ([`../coverage/observability.md`](../coverage/observability.md)).
And **Slack shipped as a channel** outside the twelve entirely — it was a
backlog entry, not a cut-list row, and watcher delivery to it followed the same
day ([`../coverage/slack-channel.md`](../coverage/slack-channel.md)). The widget
phase is still the one track with no code, and the sentence above still stands:
it wants a decision to open the scope, not an afternoon.

**One new ticket came out of that gate: `T-17b`, 0.5d, P2** — the trace does not
survive the queue. `ChatRunPayload` carries no `traceparent`, so `cmd/api`'s
spans and `cmd/worker`'s are two unrelated traces of one turn, and the wait
between them — the only part of a slow turn `T-17`'s waterfall cannot see — is
unmeasurable. It is a build rather than a gate, so it belongs here rather than
in [`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md). It is
**not** in the cut order below: that table is Sprint 2's twelve, written on
2026-07-31, and renumbering it for a half-day P2 would break every citation
already pointing into it. Take it whenever the next observability question is
asked, or alongside the widget work — nothing depends on it and it blocks
nothing.

Cutting all twelve recovers at most **21.0 of the 54.5** — less than that in
practice, because positions 5 and 9 are scoped cuts rather than whole tickets
(`T-17` keeps the `/metrics` fix, `T-M3` keeps the binding control), and because
positions 11–12 belong to the widget phase, which §6 says is cut whole or not at
all. Read the list as a sequence to stop partway down, not as a budget.

**Updated 2026-08-09 with the video track.** The list is now fourteen rows and
the paragraph above is unchanged on purpose: of the original twelve, ten are
delivered, so "cutting all twelve" describes a decision nobody can still make.
What is actually cuttable today is **23.5 days in three whole tracks and two
scoped rows** — the widget phase (11.5, whole), the video track (11.5, whole
before it starts), and `T-17b` (0.5). Rows 13 and 14 only become live decisions
once the video track has opened; before that the only decision is whether it
opens at all.

**Never cut:** `T-06`/`T-07` (metric registry), `T-08`/`T-09` (watchers — twice
displaced already, see §5), `T-10`/`T-11`/`T-12a` (the action framework and the
one action that makes watchers useful), `T-19` (embed auth), `T-M1`/`T-M2` (the
egress gate and the turn-time integration; a half-built MCP client is dead
attack surface in the same way a half-built embed surface is), `T-B1`/`T-B3`
(the business block every other `T-B` ticket deps, and the template gallery the
track was set for; cutting `T-B3` leaves the blank textarea that motivated it),
and — **added 2026-08-09** — `T-V1`/`T-V2`/`T-V3` **as a set**: the video track
is cut whole or its first three ship together, because a format the tool
description advertises and the renderer cannot produce is the `list_watchers`
failure found on 2026-08-04, one door further out, where the model promises a
customer a file.

**Two rules carried forward from §6.** The widget phase is cut whole or not at
all — `T-19`→`T-23` move together. And nothing is inserted ahead of `T-19` and
`T-08` without writing down what slips; Sprint 1 absorbed two priority inserts
and this is the mechanism that let it.

**Every un-run acceptance item is now collected in one place** —
[`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md), added
2026-08-03 — grouped by what each needs: the stack, real spend, or a real phone.
**The five that needed only the stack were run on 2026-08-04**, in one sitting,
in that file's order: `T-07b`'s redaction, `T-15`'s signed fan-out and
auto-disable, `T-M4`'s propose→approve→effect, and `T-14`'s MCP handshake all
passed; `T-09`/`T-11`'s non-admin rendering failed, because a member is shown
every admin control enabled on both surfaces. Two defects came out of it — that
one, and `list_watchers` being advertised on the MCP surface without existing —
plus a third `semantic_prompt_injection` false positive. What remains in that
file needs LLM spend, a real handset, or an operator's decision.

**What is not in this list, and should be decided before Sprint 2 opens:** the
two acceptance items Sprint 1 still owes (`T-R4`'s three unautomatable
applications, `T-A2b`'s ten live report calls), the `/metrics` finding above, and
~~the unfiled `T-S3` gate finding — the dashboard's host/port connection form pins
`sslmode=require` and does not test the connection on create, so a source added
through the UI fails one turn later after an agent has spent its budget
discovering it~~ — **fixed 2026-08-03**: the form has an encryption control,
`ssl_mode` travels on all three DSN-building endpoints rather than only create,
and create opens the database before storing it, with a `Save anyway` override
for one that is legitimately down ([`../coverage/agent-roster.md`](../coverage/agent-roster.md) §2).
**Two more joined them on 2026-07-31** from `T-S4`'s gate: the
injection guardrail refusing two of seven ordinary questions (`T-07b`'s, and now
a measured rate rather than an anecdote), and `docker-compose.yml` shipping no
object storage — **that second one was fixed the same evening**, so the
deterministic example suite now runs locally and a report can be gated on a
developer machine for the first time. None of the remaining five is a track; all
are smaller than any row above and none has an owner.

### 8c. The third insert, and what it slips

**Owner-set 2026-07-31: the business-context track (`T-B1`→`T-B4`, 8.5d) runs
after the roster track and before `T-M1`.** This is the note §8b's second carried
rule demands. Sprint 1 absorbed two priority inserts — the report track on
2026-07-27 and the API track on 2026-07-28 — and the cost of each was only
visible afterwards. This is the third, written on the day it was made.

**The running order becomes:** `T-S5` → `T-S4` → `T-B1` → `T-B2`/`T-B3` →
`T-B4` → `T-M1` → `T-M2` → `T-M3` → `T-M4`.

**`T-S5` and `T-S4` both delivered and gated live 2026-07-31**, so the roster
track is done and the order in flight is `T-B1` onwards. Unlike
`T-S1`→`T-S3`, which sat a day at "code complete, gate outstanding", these two
closed their gates the same evening.

**`T-B1` delivered and gated live 2026-07-31** (migration `034`), same evening
again. The business-context track has **6.5d left** — `T-B2`/`T-B3` in either
order, then `T-B4`. The gate found that the composed system prompt had not been
reaching the model on any deployment loading `config/agents.yaml`, which is a
defect against `T-16`, `T-S2` and `T-A2b` rather than against this ticket; it is
fixed and regression-tested, and the write-up is in
[`../coverage/business-context.md`](../coverage/business-context.md) §T-B1 §3.
Nothing about it changes the order or the estimates — the prompt-level
protections those tickets shipped were simply not in force until now, and no
estimate here assumed otherwise.

**`T-B2`, `T-B3` and `T-B4` all delivered and gated live 2026-08-01, so the
business-context track is done** — 8.5d of tickets closed in the two days after
it was inserted, and the whole record is in
[`../coverage/business-context.md`](../coverage/business-context.md). `T-B4`
needed no migration. **The next thing in flight is `T-M1`**, and the running
order below is unchanged: the insert cost the queue what §8c said it would and
nothing more.

**`T-M1` delivered and gated live 2026-08-01** (migration `037`), the same day
the business-context track closed. The MCP track has **5.5d left** — `T-M2`,
then `T-M3` and `T-M4` in either order. The gate produced three findings, all
fixed in the ticket: a public hostname resolving to a private address was stored
rather than refused, the development egress flag opened the cloud metadata
endpoint, and an embedded pointer generated a useless TypeScript type. The
write-up is in [`../coverage/mcp-source.md`](../coverage/mcp-source.md) §4.
**One owner-set scope change:** plaintext `http` to a public address is now its
own opt-in rather than only a development flag.

**What slips, in days:**

| What | Slips by | Note |
| ---- | -------- | ---- |
| MCP-as-source (`T-M1`→`T-M4`) | **8.5** | Nothing in it is blocked by the new track — this is queue position, not a dependency. `T-M3` still deps `T-S5`, which runs first either way. |
| `T-08`/`T-09` watchers | **8.5** | **Third displacement.** §5 already records two; this is the one that makes it three, and watchers have now been the next thing since Sprint 1's week 3. |
| `T-19`→`T-23` widget | **8.5** | Cut whole or not at all, so it moves whole. |
| Roster remainder (`T-S5`, `T-S4`) | 0 | The insert is behind them. |

**What does not slip:** nothing already in flight, because nothing is. The
working tree at the time of writing holds one uncommitted dashboard change (a
build-identity footer) that belongs to no ticket.

**The honest read.** 54.5 days of committed work sits ahead of a sprint that has
delivered 6.0, and this insert adds 8.5 of it while pushing watchers — a
never-cut item — for the third time. Two ways out, and they are the owner's call
rather than the implementer's: cut from §8b's list down to position 7, which
recovers 13.0 and costs the MCP write tools, business inference, webhooks, the
MCP server, tracing, `http_action` and Generate-with-AI; or accept that phases
2–6 and the widget are Sprint 3. **Neither is decided here.** What is decided is
that the number is 54.5 and not 46.0, and that watchers slipped again.

**Settled by delivery rather than by decision.** Between 2026-08-01 and
2026-08-08 everything in that paragraph except the widget landed — watchers
included (`T-08`/`T-09`, gated live 2026-08-02), and Slack arrived from the
backlog on top. The two ways out were never taken because the work outran the
arithmetic. That is a good outcome and a bad precedent: it is the second time a
"something must slip" note was overtaken instead of decided, and the next insert
should not assume a third.

### 8d. The fourth insert: video and animated decks

**Owner-set 2026-08-09: the video track (`T-V1`→`T-V5`, 11.5d) runs ahead of the
widget phase.** This is the note §8b's second carried rule demands, and it is the
fourth such insert — the report track (2026-07-27), the API track (2026-07-28),
business context (2026-07-31), and this. Each of the first three was cheaper than
it looked because the thing it extended already existed. So is this one:
`spec.Document`, the chart images, the branding, the fixtures and the delivery
doors are all built, and `T-R4` already established that a new format is a
projection of the same content model rather than a second one.

**What the track is.** The same report spec renders as a silent 1080p video and
as an animated deck at a shareable link. A Go package projects the spec into a
finished plan; a new Node service (`apps/render`) draws it with Remotion; `mp4`
becomes a document format on every door that already serves PDF and PPTX. The
argument, the alternatives considered, and eleven locked decisions are in
[`01-tickets.md`](01-tickets.md) under *Reports that move*.

**The one thing worth reading twice** is that it puts a headless browser in this
product for the first time, which [`backlog.md`](backlog.md) explicitly rejected
for documents. It is not that decision reopened: the rejection's clauses are
about the *worker image* and about *documents*, and both hold — the browser ships
in its own image behind its own deployment, no document renderer changes, and
`cmd/worker` gains an HTTP client rather than 300 MB. Locked decisions 3 and 4
are the fence, and decision 4 is the strong one: the plan is self-contained, so
the render service makes no outbound call at all and can be deployed with egress
denied.

**What slips, in days:**

| What | Slips by | Note |
| ---- | -------- | ---- |
| `T-19`→`T-23` widget | **11.5** | Cut whole or not at all, so it moves whole — §6's rule, carried forward for the third time. It has now been the next thing since Sprint 1's week 7, and it is still the one track with no code. |
| `T-17b` the trace stops at the queue | 0 | 0.5d, blocked by nothing and blocking nothing. Take it whenever. |

**What does not slip:** everything else, because everything else is delivered.
Positions 1–10 of §8b are done, watchers shipped on 2026-08-02, `T-17` finished
on 2026-08-08, and Slack arrived outside the list entirely. This is the first
insert in the plan's history that displaces exactly one track.

**Remaining committed work is therefore 23.5 days:** the widget phase (11.5),
this track (11.5), and `T-17b` (0.5). Summed from the ticket headers, per §8a's
lesson.

**The honest read, again.** The widget has now been displaced four times — by the
report track, by the API track, by business context, and by this — and each note
was written truthfully at the time. Four true notes still add up to a track that
never starts. Two things follow, and both are the owner's call:

1. **If the widget is still wanted, the next insert has to displace something
   else** — or the widget goes first and the video track waits. Neither ordering
   is wrong; drifting into the first by default is.
2. **If it is not wanted, say so and cut it**, rather than carrying 11.5 days at
   the top of every remaining-work figure. `feature-coverage.md`'s
   *Embeddability ░░░░░░░░░░* is honest today; carrying a phase nobody intends to
   start would make it dishonest.

**What is decided here** is the ordering and the cost: the video track runs
first, the widget slips 11.5 days, and remaining committed work is 23.5.

### 8e. The widget phase stops being committed work

**Decided 2026-08-09, together with the insert above, because §8d's question
should not be asked a fifth time.**

The widget phase moves to [`backlog.md`](backlog.md) with a trigger. It is **not
cancelled** — `T-19`→`T-23` stay written, nothing depends on them, and the phase
slides back whole the day the trigger fires. What changes is that it stops being
counted as committed.

**Why, in one line:** four consecutive plans have said the widget is next and
none of them ran it. That is not four scheduling accidents, it is a revealed
priority, and §8b's own rule for the roster track applies in reverse — *a
committed feature buried behind a condition nobody is measuring is a feature that
never ships*. A track that is always next and never started makes every
remaining-work figure in this document wrong by 11.5 days, which is the exact
failure §8a spent a section correcting.

**The trigger:** a customer asking to put Argentum's chat inside their own
internal site. Not a hypothetical one — a named tenant with a frontend team. The
API (`T-A1`→`T-A5`) and MCP (`T-14`) already cover *reachable from outside the
dashboard* for every consumer that is a server or an agent; the widget covers
humans in the tenant's own UI, and that is a demand nobody has expressed yet in
the seven weeks it has been carried.

**What it costs to be wrong:** the phase starts 11.5 days later than it would
have. `T-19` builds on `T-13`, which shipped; `T-21` builds on `packages/chat-ui`,
which does not exist yet either way. Nothing rots.

**Remaining committed work is therefore 12.0 days** — the video track (11.5) and
`T-17b` (0.5). For the first time since 2026-07-27 that is a number a sprint can
be sized against rather than a number that needs a paragraph of explanation.

### 8f. What the same day delivered against it

**Written 2026-08-09, hours after §8d and §8e.** Of the 12.0 committed days,
**7.0 are delivered**: `T-V1` (2.5) and `T-V2` (3.0) landed earlier the same
day, `T-V3` (2.5) and `T-17b` (0.5) that evening — which sums to 8.5 against a
12.0 that counted `T-V1`/`T-V2` as outstanding when §8d was written. Summed
from the ticket headers, per §8a's lesson: **`T-V4` (2.0) and `T-V5` (1.5)
remain, so 3.5 days of committed work are left.**

That is the smallest this figure has ever been, and the reason to say so
plainly is §8e's: a remaining-work number that quietly counts delivered work is
the failure that section spent a paragraph correcting.

**Both remaining tickets are cuttable** — rows 13 and 14 of §8b — and neither
is a never-cut item. `T-V1`/`T-V2`/`T-V3` were the never-cut set, and they
shipped together as that row required.

**What is *not* in the 3.5 is the gate debt this day added**: the video track's
live gate, `T-17b`'s joined waterfall, and a scored eval run for the
prompt-contradiction fix. All three are in
[`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md), and the
first two are in §1a — the bucket that needs only the stack, which is the one
that actually gets run.

**This is the owner's call to reverse**, and reversing it is one line in
[`backlog.md`](backlog.md) plus this section. If the widget is wanted on a date,
say the date and the video track moves behind it — that ordering is equally
defensible and it is the *drifting* that this section exists to stop.

### 8g. Committed work reaches zero

**Written 2026-08-09, later the same day.** `T-V4` (2.0d) and `T-V5` (1.5d)
both landed, so the 3.5 days §8f named are spent and **committed work is now
0.0 days** — the first time since 2026-07-27 that this document has had nothing
left in it.

The video track is 11.5d complete, all five tickets. `T-V4` shipped the share
link and, on the way, the documents list the dashboard had never had — the
staff who generated a report could only reach it through the chat message that
produced it. `T-V5` shipped the check the whole track was pointed at: the PDF,
the deck and the video demonstrably carry the same figures.

**What was done first, and what it cost, is the part worth carrying forward.**
The day opened by running [`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md)
§1a — ninety minutes — and it produced three defects, none of which a unit test
could have found: the report SSE stream never ended for a threadless render,
the video's own caps were enforced in the worker rather than at the door, and
`cmd/api` had never started a span, so `T-17b`'s joined trace could not exist.
Building `T-V4` on an ungated `T-V3` would have compounded the first two. Then
`T-V4`'s own gate found a share token written to the request log in full on
every page view, and `T-V5`'s gate found itself crying wolf twice before it
passed. **Five defects, from four gates, in one day.** The pattern the delivery
log has recorded since `T-13` held again, and the cheapest thing in this
project remains running the gate you already wrote.

**What is left is not build work.** Every outstanding item is a gate:
`T-V2`'s cluster checks (a Kubernetes cluster), `T-V4`'s three browsers and
`T-V5`'s contact sheet (a human looking at a screen), `T-A2b`'s ten live report
calls and `T-07b`'s before/after eval pair (model spend), `T-R4`'s three office
applications, `T-12a`'s real handset, Slack's workspace, and `T-14`'s Helm
hostname. They are collected in
[`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) and
grouped by what each needs, because that grouping is what decides whether one
gets run — the file records its own lesson about a gate filed behind a cost it
did not have.

**So the next decision is the owner's, and it is a scoping one rather than a
scheduling one.** With nothing committed, §8e's widget trigger, the phases-2–6
backlog and anything new all start from the same line. This document should not
choose for them; what it can say is that the two cheapest things on the table
are a cluster and a browser, and that both close items this sprint has been
carrying since the video track opened.

## 9. The widget trigger fires — Gelael Member

**Decided 2026-08-09, hours after §8g.** §8e moved the widget phase to the
backlog behind one condition: *a named tenant with a frontend team asking to put
Argentum's chat inside their own internal site.* That condition is now met, and
this section is the record of it firing rather than a fifth plan saying the
widget is next.

**The tenant is Gelael Supermarket** — the membership and loyalty platform
Smartsoft already builds and operates (`gelael-member`, a separate repo). Its
admin dashboard is Next.js 14 with next-auth, its analytical data is the MySQL
database that dashboard already reports on, and its frontend team is this
repository's owner. It is a first tenant with an unusual property: **the same
people own both sides**, so the integration's requirements arrive as commits
rather than as a support thread.

### 9a. What was built first, and why it is not the widget

**A bespoke `/v1` integration, in the Gelael repo, in a day.** The Gelael
dashboard gained three route handlers that proxy `POST /v1/chat`,
`GET /v1/agents` and `GET /v1/threads/{id}/messages` with a workspace API key
held server-side, and a **Tanya Data** page that streams the answer. Nothing in
*this* repository changed — that is the point of the finding below.

**Why this order, against the widget going first:** the widget is 11.5 days and
its shape has been guessed at since it was written. A day of somebody actually
answering questions from inside a real internal tool buys the requirements those
11.5 days get spent against, and `T-A1`→`T-A5` were built precisely so a
server-side consumer needs nothing new from us. The cost of the ordering is a
throwaway UI, about a day of it, and that is stated here so nobody later reads
the pilot as the plan.

**What the pilot proves, and what it cannot.** It proves the `/v1` contract is
sufficient for a third-party surface: streaming, thread continuity, agent
selection, typed errors and per-user attribution all worked against the
published spec with no additions. It cannot prove anything about the widget's
security model, because it does not use it — there is no browser-held
credential, no origin allowlist and no HMAC identity anywhere in it. The pilot's
key is workspace-wide and server-side, which is exactly the arrangement `T-19`
exists to make unnecessary.

### 9b. Three requirements the pilot hands the widget phase

Each is something the pilot had to solve in the host application, and each is
something the widget must solve once so the next tenant does not.

1. **Thread ownership is not the tenant's job.** `/v1` authorises the workspace,
   so an admin passing a colleague's `thread_id` would have been served it. The
   Gelael proxy fetches the thread, compares `user_ref`, and answers a mismatch
   with 404. **`T-20` already specifies this check** — this is the first
   evidence that a tenant who skips it has a data leak and no error, so the
   embed docs (`T-22`) must state it as a rule and not as a note.
2. **A streaming answer dies behind a default proxy.** Nginx buffers, so the
   host needs `X-Accel-Buffering: no` or the stream arrives as one lump after
   the answer is finished. This is invisible in local development and obvious in
   a cluster. It belongs in `T-22`'s troubleshooting table beside the CSP row.
3. **`final` versus the deltas.** The persisted message on `final` is the answer
   of record and the deltas are a preview of it; a client that only concatenates
   deltas is subtly wrong. Undocumented in the quickstart, discovered by reading
   `v1_chat.go`. Fix the quickstart — that one is ours, not the widget's.

### 9c. What this does to the plan

**The widget phase (`T-19`→`T-23`, 11.5d) returns to committed work**, whole and
unchanged, and [`backlog.md`](backlog.md)'s entry for it is closed rather than
edited. Committed work goes from 0.0 days to 11.5.

**Nothing is displaced**, which is the first time an insert in this document has
been able to say so — §8g emptied the plan the same day. The ordering question
§8d and §8e kept re-asking does not arise.

**`T-19` starts on the day it was scoped for.** It builds on `T-13`, which
shipped; `T-21` builds on `packages/chat-ui`, which still does not exist and is
still the extraction it always was. The seven weeks of deferral cost this phase
nothing except the seven weeks.

**Gelael is `T-22`'s gate, not a hypothetical throwaway Vite app.** That
ticket's gate — *integrate into an app using only the published docs, and time
it* — now has a real host with a real frontend, real CSP, real auth and real
staff. A pilot that has already integrated once is the strongest possible check
on whether the documented path is the easy one, because it can be compared
against what the bespoke integration actually took.

**What the pilot owes before any of this is called proven**: an Argentum
workspace for Gelael with the MySQL source connected, a scoped key, and one real
question answered end to end by a human. All three are in
[`../coverage/gelael-pilot.md`](../coverage/gelael-pilot.md) §5, which is a
gate-shaped list because that is what this project has learned to write.

### 9d. T-19 lands the same day

`T-19` (2.5d) is built: `051_embed_keys`, the session mint, `EmbedAuth`, the two
rate buckets and the dashboard's Embed tab with signing snippets in four
languages. **Remaining committed work is 9.0 days** — `T-20` (2.0), `T-21`
(3.5), `T-22` (2.0), `T-23` (1.5).

Two departures from the ticket, both in
[`../coverage/embed-auth.md`](../coverage/embed-auth.md) rather than in a commit
message: the signing secret is encrypted rather than hashed, because the
ticket's own HMAC requirement makes `secret_hash` unsatisfiable; and the surface
grew a CORS policy and a mint rate bucket the ticket never named, because a
browser cannot read a refusal it is not allowed to see and an unauthenticated
route needs a bound.

**Its live gate is owed and it is a cheap one** — `051` against a real Postgres
and three `curl` transcripts, in
[`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §1a. §8g
recorded that the cheapest thing in this project is running the gate you already
wrote, and `T-20` builds directly on this ticket's auth chain.

### 9e. The phase closes, and what is left is a browser

**Written 2026-08-10.** `T-20` (2.0), `T-21` (3.5), `T-22` (2.0) and `T-23`
(1.5) are built. **Committed work returns to 0.0 days** — the second time this
document has been able to say that, and the first time it has said it about a
track that spent seven weeks being deferred.

Two deliberate departures, both recorded in
[`../coverage/widget.md`](../coverage/widget.md) rather than left implicit:

1. **`packages/chat-ui` was not extracted** (`T-21`). The widget has its own
   Preact UI, so the drift the ticket warned about is now real: two places
   render a chat message. The trade — a refactor of a working staff surface,
   with the regression landing on people who use it daily, to share ~200 lines
   of presentational markup against an 80 KB budget — and the two events that
   should trigger paying the cost are in `apps/widget/README.md`.
2. **`T-22` shipped its docs and its example, not its packaging.** No npm
   package, no versioned CDN path, no changesets, three of four example apps
   missing. `dist/` is static and deployable, which is what the *pilot* needs;
   publishing is what the *next tenant* needs, and it is the cheapest remaining
   item in the phase.

**What the phase cost against its estimate:** 11.5 days scoped, two days spent,
with `T-22` partial. The gap is mostly packaging and the extraction — the two
things above — and stating it that way is more useful than a velocity claim.

**What is left is not build work, and it is the same sentence §8g ended on.**
Every outstanding item on this track needs something a test cannot supply: a
Postgres for three migrations, an LLM key for one turn, three browsers for the
panel, a second origin for a preflight. They are in
[`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §1a, in
the bucket this project's own record says is the one that actually gets run —
and the two defects this phase found were both found by tests, which is exactly
the pattern that precedes a gate finding the rest.
