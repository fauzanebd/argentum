# Backlog — Deferred Work

Everything consciously not in Sprint 1, with the reason and the trigger that
should pull it forward. A backlog item without a trigger is a wish; each of these
has one.

---

## Sprint 2 candidates (high confidence)

### The tenant agent roster (`T-S1` → `T-S5`) — **scheduled, tickets written**
The customer creates their own agents — Marketing, Ops, HR, Finance — each with
a persona, a tool allowlist and a data-source allowlist, reachable from the
dashboard, from a bound Discord/Lark/WhatsApp channel, and over `/v1`.
**Status:** owner-set 2026-07-29. Not deferred — filed as five tickets in
[`01-tickets.md`](01-tickets.md), scheduled for Sprint 2 beside `T-19` and
`T-08`. Listed here so this file stays the single place to read what Sprint 2
holds.
**Why not Sprint 1:** Sprint 1's remaining days are committed to the API track;
inserting a 9.5d track would have displaced `T-A5` and overrun. Nothing in the
roster blocks anything in Sprint 1.
**Estimate:** 9.5d (`T-S1` 2.5, `T-S2` 2.5, `T-S3` 1.0, `T-S4` 2.0, `T-S5` 1.5).
`T-S1`/`T-S2` never-cut; `T-S4`/`T-S5` are cuts #2a and #2b.

### Phases 2–6: metric registry, watchers, actions, MCP, hardening (T-06 → T-18)
**Why deferred:** Scheduling, again, and this one is expensive. The API track
(`T-A1`→`T-A5`, 10.5d plus the foundation it forces earlier) was made the
sprint's highest priority on 2026-07-28. Against 27.5 remaining working days, the
report track + foundation + API track is 26.0. Phases 2–6 are 23.5 and do not fit.
**This includes `T-08` watchers — the shift this sprint was originally named
for.** `00-sprint-overview.md` §6 carries the full arithmetic and the argument.
**Trigger:** Sprint 1 closes. Sprint 2 opens with `T-19` and `T-08`, both
never-cut, before anything new is considered.
**Estimate:** 23.5d, unchanged — the tickets need no rewriting.

### The whole widget phase (T-19 → T-23)
**Why deferred:** Not a change of mind about the widget — a scheduling
consequence. The report track (`T-R1`→`T-R5`, 10d) was inserted on 2026-07-27 and
the sprint cannot hold both. The widget phase is the designated slack because
nothing depends on it and it slides whole. `T-19` (embed auth) becomes the first
ticket of Sprint 2 and stays never-cut there.
**Trigger:** Sprint 1 closes.
**Estimate:** 11.5d, unchanged — the tickets need no rewriting.
**Note added 2026-07-28:** the API track (`T-A1`→`T-A5`) covers the
server-to-server half of "reachable from outside the dashboard" and does not
substitute for this. The widget serves humans in the tenant's UI; the API serves
the tenant's backend. `T-19` still builds on `T-13`, which now lands in Sprint 1
regardless, so this phase starts cheaper than it was scoped.

### Scheduled branded report delivery
A scheduled task or a watcher produces the branded PDF or deck and delivers it to
a channel — "the weekly review deck lands in Lark every Monday at 07:00". This is
the report system and the push shift compounding, and it is the cheapest strong
demo the product will have.
**Why deferred:** needs `T-08` (watchers) and `T-12a` (`send_message` with
`attach_document_id`, which already takes one) both in production. After that it
is wiring, not building.
**Trigger:** watchers running in production for any customer.
**Estimate:** 1.5d.

### DOCX / Word output
**Why deferred:** PDF covers "send it", PPTX covers "present it". Word is for a
document the recipient edits, which is a different job and a different renderer.
**Trigger:** a customer whose process requires editing the report before it goes
out — common in finance and legal, so expect it eventually.
**Estimate:** 2.5d, and much less if `T-R4`'s OOXML templating generalises.

### Natively editable charts in PPTX
Charts ship as images in `T-R3`. Native OOXML chart parts stay editable inside
PowerPoint (double-click, change the data).
**Why deferred:** roughly 5× the XML work for a capability only some recipients
want.
**Trigger:** a customer asking to change chart data inside the deck we sent.
**Estimate:** 3d.

### Report template gallery
Named, reusable layouts ("board update", "monthly review", "invoice") the agent
picks from, rather than composing every document from primitives.
**Why deferred:** needs real usage data about which shapes repeat. Guessing them
now produces templates nobody selects.
**Trigger:** the same document structure being composed three times across
tenants — visible in the `generate_document` audit rows after `T-05`.
**Estimate:** 2d.

### Headless-Chromium document rendering
The escape hatch if maroto's grid genuinely cannot express a required layout.
Renders HTML with the dashboard's real CSS for perfect fidelity.
**Why deferred / mostly rejected:** ~300 MB browser layer in the worker image, a
sandbox to secure, ~1s per document.
**Trigger:** a specific layout the grid cannot express — not "this is taking
longer than expected".
**Estimate:** 3d, plus permanent operational cost.
### Public / anonymous widget mode
**Why deferred:** Sprint 1's widget (T-19→T-23) serves the tenant's **own staff**,
with identity asserted by the tenant's backend via HMAC. Serving anonymous
visitors on a public marketing site is a different product with a different threat
model: anonymous session issuance, per-visitor spend caps, bot and scraping
defence, and much harder data scoping — a public visitor must not be able to
enumerate the company's warehouse through natural language.
**Trigger:** a customer wanting Argentum on a customer-facing page. Push back
first; usually what they actually want is a support bot, which is a different
product.
**Estimate:** 6d, and it needs its own threat model written before any code.

### Dashboards inside the widget
**Why deferred:** Chat only in v1. Rendering saved Metabase dashboards inside a
constrained iframe panel is a layout and auth problem (Metabase's own session vs.
the embed session) worth solving separately.
**Trigger:** widget users repeatedly asking for a chart and then leaving to open
the dashboard — visible in `usage/by-user` as widget turns followed by dashboard
logins.
**Estimate:** 3d.

### Watcher / alert feed inside the widget
**Why deferred:** Watchers deliver to chat channels in Sprint 1. An in-widget feed
means the staff see breaches without leaving their internal tool — a genuinely good
combination, but it needs T-08 in production first so the feed has real events.
**Trigger:** watchers running in production for a customer that also has the widget
installed.
**Estimate:** 2.5d.

### Widget: proactive nudges
The launcher badges when a watcher breaches, so the agent gets attention inside the
customer's own tool without an email or a chat message. This is the widget and the
push shift compounding — likely the strongest version of "disrupt how a company
works" available to this product.
**Trigger:** in-widget alert feed shipped.
**Estimate:** 2d.

### Chat-native approval
**Why deferred:** T-11 ships dashboard-only approval, which proves the state
machine. Chat-native approval (reply "YES" in WhatsApp, click a Discord button)
is a per-channel UX problem on top of a working core.
**Trigger:** first customer who lives entirely in WhatsApp and never opens the dashboard.
**Estimate:** 3d (WhatsApp quick-reply templates, Discord buttons, Lark cards).

### Statistical anomaly detection for watchers
**Why deferred:** Sprint 1 watchers use threshold and percent-change comparators,
which cover most real alerts and are explainable to a non-technical owner.
**Trigger:** users setting thresholds and then complaining about noise — that is
the signal that they want "unusual", not "above X".
**Estimate:** 4d (rolling z-score / seasonal decomposition per metric grain, plus
a sensitivity control that a business owner can actually understand).

### Metric registry v2: dimensions and drill-down
**Why deferred:** v1 is deliberately one number per metric per window. Dimensions
turn the registry into a semantic-layer DSL, which is a multi-week design problem.
**Trigger:** three or more metrics that differ only by a `WHERE` clause — that is
the point where a `dimensions` column pays for itself.
**Estimate:** 5d.

### Slack channel
**Why deferred:** Additive against the existing channel abstraction (`Channel`
enum + provider + thread resolver + allowlist + migration). Known shape, ~2 days,
no workflow change.
**Trigger:** first non-Indonesian mid-market prospect, or any inbound asking for it.
**Estimate:** 2d.

### Telegram channel
**Why deferred:** Advertised on the landing page but not built. T-18 fixes the
copy instead of the code, because Telegram has weaker business adoption in the
target market than WhatsApp or Lark.
**Trigger:** inbound demand, or a market where Telegram business use dominates.
**Estimate:** 2d.

### Frontend test framework
**Why deferred:** Backend tests protect correctness; the dashboard is thin and
visually verifiable, and `tsc -b` already catches the common class of breakage.
**Trigger:** the first FE regression that reaches a user.
**Estimate:** 2d (Vitest + Testing Library, plus tests for `use-thread-stream.ts`
— the one genuinely stateful piece of frontend logic).

---

## Monetization track (pull forward when pricing is decided)

### Plans, quotas, and checkout
**Why deferred:** T-03 caps spend, which is the urgent half. Pricing a product
whose headline capability (watchers, actions) ships in weeks 3–4 means pricing it
twice.
**Trigger:** the first customer who asks "how much?" with intent to pay.
**Estimate:** 6d (plan model, quota enforcement per plan, Xendit or Stripe
checkout, invoicing, dunning).
**Note:** Indonesian market → Xendit or Midtrans is likely a better default than
Stripe. Verify before building.

### Per-message cost attribution
**Why deferred:** Finding B-2. Thread-level attribution already works and is what
customers actually look at.
**Trigger:** a customer disputing a bill, or a support case needing per-turn cost.
**Estimate:** 1d — pass `messageID` through `MeteredLLM` (it is dropped at
`metering_llm.go:225`) and have `ChatRunner.completeWith` receive real token counts
instead of the hardcoded `0, 0`.

### Usage-based overage billing
**Trigger:** plans exist and a customer exceeds one.
**Estimate:** 3d.

---

## Platform depth

### Multi-agent architecture (planner + specialists)
**Not to be confused with the tenant agent roster (`T-S1`→`T-S5`), which is
scheduled for Sprint 2.** This entry is the *internal* one: a planner that
decomposes a question across specialists we write. That one is invisible to the
customer and gated on eval data; the roster is a product surface the customer
operates. A planner would eventually sit *inside* one roster agent. Neither
blocks the other.
**Why deferred:** T-16 raises the iteration budget, which is the cheap 80%.
Specialist agents (SQL analyst / report writer / ops executor) need eval data to
prove they beat one well-prompted agent — otherwise it is added cost and latency
for a feeling.
**Trigger:** eval cases that consistently fail because one agent is doing two
incompatible jobs in a single prompt.
**Estimate:** 8d.
**Note added 2026-07-29:** this entry was the only place the plan mentioned
multiple agents, so a customer-facing roster was read into it for a while. It
does not cover that, and its trigger would never have fired for it — eval
regressions are not customer demand. The roster is now its own track in
[`01-tickets.md`](01-tickets.md).

### Per-agent user grants
Restrict which users may open which agent, so the HR agent is reachable only by
HR. `T-S1`'s v1 makes company membership the whole boundary: the Finance agent
cannot query the HR source, but any member can talk to the Finance agent.
**Why deferred:** decided 2026-07-29 to keep the roster's first version to
persona + tools + sources. Grants add an authz surface to every agent route and
every enqueue path, plus a negative test on each — roughly doubling `T-S1`.
**Trigger:** the first tenant who puts genuinely sensitive data behind an agent
— payroll, personnel, unreleased financials. Expect it early, because "HR agent"
is one of the four use cases that motivated the track. **Until it ships, the
dashboard must say plainly that an agent is not an access boundary.**
**Estimate:** 2d. `agents` and `agent_sources` are shaped so an `agent_grants`
table adds no column to either.

### Per-agent model, temperature, and budget
A cheap model for the marketing agent, the expensive one for finance.
**Why deferred:** the seams already exist — `BudgetResolver` is
`func(ctx, companyID)` and `llmCache.For` takes a tier — but a tenant who puts
an agent on a weak model has changed what `T-16` guarantees about fabricated
figures, so it needs its own eval run per configuration, not a settings field.
**Trigger:** a tenant whose bill is dominated by one high-volume, low-stakes
agent.
**Estimate:** 1.5d plus an eval matrix.

### Agent templates
Prebuilt Marketing / Ops / HR / Finance personas a tenant can start from instead
of writing a prompt in an empty textarea.
**Why deferred:** we do not yet know what a good persona for these looks like in
production. Shipping four guesses as "templates" makes them the default and
freezes the guess.
**Trigger:** three tenants having written roughly the same persona by hand.
**Estimate:** 1d.

### Persisted run traces with replay
**Why deferred:** Finding O-3. T-17's OTel spans cover live debugging; replay is a
deeper capability.
**Trigger:** the first "why did the agent say that?" question you cannot answer
from logs and traces.
**Estimate:** 3d.

### Additional warehouse drivers (BigQuery, Snowflake, ClickHouse, Redshift)
**Why deferred:** The driver registry makes each one additive. Building drivers
nobody has asked for is inventory.
**Trigger:** a specific prospect's specific warehouse.
**Estimate:** 3d each, less after the first.

### Non-SQL sources (Google Sheets, REST APIs)
**Why deferred:** Breaks the `Conn` abstraction's read-only-transaction contract
and needs a different safety model.
**Trigger:** three prospects whose real data lives in Sheets — plausible in the
SMB segment, so watch for it.
**Estimate:** 5d.

### Native embeddable dashboards
**Why deferred:** Metabase share URLs work today.
**Trigger:** a customer wanting Argentum charts inside their own product.
**Estimate:** 5d.
**Note:** overlaps with "Dashboards inside the widget" above. Do that one first —
it is narrower and the embed auth already exists after T-19.

### ~~Client SDKs for the API (JS / Python / Go)~~ — **pulled into Sprint 1 as `T-A4`, 2026-07-28**
The trigger fired early: the repo owner made a tenant-callable API the sprint's
highest priority. Node and Python ship in `T-A4`, generated from the OpenAPI spec
this entry already concluded had to come first. **Go stays deferred** — the demand
is Node and Python, and a third generated client with no consumer is inventory.
**Trigger for the Go client:** a customer integrating from Go.
**Estimate:** 1d once `T-A4`'s spec exists.

---

## Enterprise readiness

### SSO (SAML / OIDC)
**Trigger:** an enterprise deal blocked on it. Not before.
**Estimate:** 4d.

### Row-level tenant data policy
Let a company restrict which of *their* users can see which rows (e.g. a regional
manager sees only their region). Distinct from Argentum's own tenant isolation,
which is already solid.
**Trigger:** first customer with more than ~10 end users on one connection.
**Estimate:** 6d — needs a policy model injected into every generated query, which
in turn is far easier once the metric registry (T-06) is the primary query path.
That ordering is not accidental.

### SOC2 / ISO groundwork
**Trigger:** enterprise procurement asking for it.
**Estimate:** ongoing, not a ticket. T-05's audit log and T-04's RBAC are the
prerequisites and both ship in Sprint 1.

### Self-hosted / on-prem deployment
**Why deferred:** The Helm chart is close, but per-tenant LLM credentials and
BYO-LLM already cover the usual "our data can't leave" objection.
**Trigger:** a regulated customer requiring it contractually.
**Estimate:** 5d (air-gapped mode, license check, offline docs).

---

## Hygiene and debt

| Item | Finding | Estimate | Trigger |
| ---- | ------- | -------- | ------- |
| Down migrations for 001–014 | Q-7 | 1d | Folded into T-18 if time allows; otherwise the first failed production migration |
| WebSocket auth without a query-param token | S-4 | 1d | A security review, or moving to a subprotocol-based scheme |
| Prune stale feature branches (7 merged branches still on origin) | — | 0.5h | Folded into `T-00b`; otherwise any time |
| Build orchestrator (Turborepo / Nx) with remote caching | — | 1d | `packages/` exceeds ~4 members, or CI wall-clock becomes annoying. `pnpm -r` + the Makefile is enough below that. |
| Generated TS types for the `/api/embed` contract | — | 0.5h | `T-02b` covers the dashboard API; extend it when `T-19`/`T-20` add embed types |
| Frontend tests for `packages/chat-ui` | — | 1d | Shared by dashboard **and** widget after `T-21`, so a regression there breaks two consumers — the strongest case for the first Vitest setup |
| Self-host Space Grotesk in the dashboard instead of the Google Fonts CDN | — | 0.5h | `T-R1` vendors the TTFs for the backend anyway, so the files are already in the repo — the dashboard is then one `@font-face` block away from dropping a third-party request |
| Error tracking (Sentry) both repos | O-4 | 0.5d | First user-reported bug you cannot reproduce |
| Onboarding checklist incl. "enable table embeddings" | P-4 | 1.5d | Signup-to-first-answer conversion looking bad |
| `DefaultPricing` still labelled "approximates GPT-4o" | B-4 | 0.5h | Any time — misleading comment on live billing code |
| Repo owner mismatch (`fauzanebd` module path, `haritsrizkall` GHCR/CI) | — | — | Note only; harmless but confusing to a new contributor |

---

## Explicitly rejected (not deferred — decided against)

| Idea | Why not |
| ---- | ------- |
| Relaxing `run_sql` to allow writes | Read-only tenant SQL is a core safety property and a selling point. Write access goes through the T-10 action framework with approval and audit, never through the SQL path. |
| Removing guardrail topic enforcement to widen the product | The narrow scope is what makes the agent trustworthy and cheap. A general assistant competes with ChatGPT; a trusted business-data agent does not. |
| Building an in-house charting engine | Metabase already does this well and the integration works. |
| One shared LLM key for all tenants | Per-tenant credentials already exist and are the better model — the tenant's spend, the tenant's rate limits, the tenant's data-processing agreement. |
