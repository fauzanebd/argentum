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
Phase 1b Tests ✅ + CI gate ✅ + RBAC ✅ + audit log ✅ + generated types + credit enforcement
         └─ You cannot ship autonomy on an unmeasured, unbounded, unaudited system.
            Also fixes the three P0 security/billing findings, which are cheap now
            and expensive after you have users. T-03 waits on T-02c: a budget
            check gating on an always-zero number is worse than none.
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
Phase 1c API keys → /v1 foundation → reports over HTTP → chat over HTTP → SDKs
         └─ Owner-set highest priority, inserted 2026-07-28. Argentum stops being
            a destination and becomes a component of the customer's own product.
            T-13 moves here from week 5 because it is the only machine auth that
            exists. T-A2 is the flagship — a tenant's app asks for a PDF or an
            Excel file and gets one — and T-A4 is what makes an integrator
            finish without talking to us.
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
| Go SDK, hosted API docs site, API playground | Node and Python are where the demand is. Markdown in the repo until someone complains. |
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
| Multi-agent / planner architecture | `T-16` raises the iteration budget; specialist agents need eval data first. |
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
| 1b | **Safe to change**       | CI fails on a failing test. All CRITICAL packages have tests. A Go struct rename without `make types` is a red build. Non-admin cannot rotate a DSN. A tenant at zero credits gets a clear refusal instead of a bill. **Half met 2026-07-28 by `T-02`:** all CRITICAL packages have tests (21 of 49 packages, up from 16), CI runs `go vet`, `golangci-lint` and `go test -race` and the tree is clean under all three, and a deliberate break was shown to fail the suite locally — the CI-run proof needs a push and is recorded as outstanding. **`T-04` closed the RBAC half 2026-07-28** — non-admin cannot rotate a DSN, proven against a live API. **`T-05` closed the audit half 2026-07-28** — every tool call, and every guardrail-stopped turn, leaves an append-only row scoped to its tenant. `T-02b` (type drift) and `T-03` (credits) are the rest. |
| 1c | **Anyone can call it**   | A throwaway Node script holding an API key writes a branded PDF to disk in under 10 minutes, using only the published quickstart and no help from us. The same key streams a chat answer over SSE, and is rejected by every `/api` dashboard route. A retried request with the same `Idempotency-Key` bills once. Adding a `/v1` route without an OpenAPI entry is a red build. |
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
| **A leaked API key spends a tenant's credits**             | Medium     | **High** | A key sits in someone else's CI config, and a `for` loop over `POST /v1/reports` is an unbounded LLM bill. Four layers, none optional: `T-03`'s budget check inside the `/v1` chain returning a typed 402, per-key rate limits on a separate bucket, Argon2id hashing with the plaintext shown exactly once (`T-13`), and per-key usage visible to the tenant (`T-A5`) so they notice before we do. |
| **`/v1` freezes a shape we regret**                       | **High**   | Medium | A public contract is a promise, and the first customer writing against it makes it permanent. Mitigation is scope, not process: ship the narrowest surface that does the job, keep `/api` unversioned so first-party dashboard churn never touches `/v1`, and state the additive-only policy in `T-A1` before the first key is issued. |
| **An untrusted spec takes down the renderer**             | Medium     | High   | A spec arriving over HTTP is untrusted in a way the agent's own spec never was. maroto will attempt to lay out 500 000 rows. `T-A2` caps rows, columns and string lengths and rejects **before** rendering; a sync render over 20s becomes a 202. |
| **The OpenAPI spec drifts from the routes**               | **High**   | Medium | Exactly the failure the design tokens already had — two copies of one truth, disagreeing quietly. Same fix, and it is proven in this repo: `T-A4`'s CI check walks the gin route tree and fails in **both** directions. A stale spec is worse than no spec, because integrators trust it. |
| **A sync `/v1/chat` call dies behind a proxy**            | Medium     | Low    | Turns run five to seven tool calls after `T-16`. The sync door is capped by `API_V1_SYNC_TIMEOUT_SECONDS` and returns a resumable 504 carrying the `thread_id` rather than a dead connection; SSE with 15s heartbeats is the documented default. Do not raise the cap when someone complains — point them at the stream. |
| **The push shift ships in neither sprint**                | Medium     | **High** | Watchers were Sprint 1's wedge and are now Sprint 2's, behind `T-19`. Two priority inserts have moved them once already; a third would strand them. The mitigation is a decision, not a mechanism: Sprint 2 opens with `T-19` and `T-08` and nothing is inserted ahead of them without explicitly writing down what slips. |
| **Embed auth flaw exposes tenant data**                   | Low        | **Critical** | `T-19` ships before any widget UI exists and is gated on a full forgery matrix: tampered signature, wrong origin, expired token, far-future expiry, revoked key. Constant-time comparison enforced by diff review. Mandatory origin allowlist, wildcard rejected. |
| Integrators copy an insecure signing shortcut              | Medium     | High   | `T-22` ships complete server-side snippets in four languages. The failure mode is a partial example, so examples are treated as security surface, not documentation. |
| Widget bundle bloats and the customer's frontend team removes it | Medium | Medium | Hard budgets in `T-21`: loader ≤15 KB gzipped, iframe app ≤80 KB. Preact + `marked`, not React + `react-markdown`. Sizes are a gate item, not an aspiration. |
| Widget phase slips past week 8                            | Medium     | Low    | Nothing depends on it. It slides to Sprint 2 whole rather than shipping half-integrated. |

## 6. Cut order

If the sprint slips, cut in this order. Do not improvise a different order —
each cut is chosen to preserve the dependency chain.

1a. `T-A5` integrator-facing observability — ship the API with logs and let the
   per-key error view follow. Costs an integrator a slower first debugging
   session, costs nothing structurally. Lettered rather than numbered so the
   positions below keep the numbers the tickets already cite.
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
| Finish the report track (~~`T-R3`~~ ✅, ~~`T-R4`~~ ✅, `T-R5`) | 1.5 | 1.5 |
| Foundation (~~`T-02`~~ ✅, `T-02b`, `T-03`, `T-04`, `T-05`) | 5.0 | 6.5 |
| **The API track (`T-13`, `T-A1`→`T-A5`)** | 12.5 | **19.0** |
| Remaining budget | | **26.0** |

**Updated 2026-07-28 after `T-02`.** Three of the foundation's eight days are
spent, so the slack across the three phases is now 7.0 days rather than 4.0.
That is the most comfortable this plan has been since the first priority
insert, and it is worth naming why it should not be spent: the API track's
2.5-day estimate for `T-A1` covers an error envelope, an idempotency contract
and a pagination style that all become permanent the first time a customer
writes against them.

**Sprint 1 is now: finish the report system, build the foundation, ship the API.**
It fits — with 4.0 days of slack across three phases after `T-R4` landed on
2026-07-28, which is more comfortable than the 1.5 this table showed a day
earlier but is still a plan that has already absorbed two priority inserts. What pays for it is phases 2–6 — the metric registry,
**watchers**, actions, MCP and hardening, 23.5 days — moving to Sprint 2
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
