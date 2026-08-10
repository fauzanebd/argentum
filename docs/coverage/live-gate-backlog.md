# Live-gate backlog — what is written down instead of run

Every acceptance item this repo currently owes that **cannot be closed by
writing code**, in one place, with what it needs and what it would prove.

Written 2026-08-03, after six tickets landed code-complete in one day
(`T-07b`, `T-M4`, `T-15`, `T-14`, `T-17`, `T-18`) beside the six that were
already owed. The
reason this file exists is the pattern the delivery log has been recording since
`T-13`: **the live half found something the unit tests could not, on every
ticket where it was run.** Ten tickets went into the 2026-08-02 gate and came out
with two defects fixed the same day and six findings written down. A list of
un-run gates is therefore a list of unknown defects, not a list of paperwork.

**Group 1 was run on 2026-08-04** — all five items, one sitting, one tenant. The
pattern held: three findings, none of which a unit test could have produced
([`delivery-log.md`](delivery-log.md) Phase 2e). What is left needs money or a
real phone.

**And `T-17`'s exposition half was run on 2026-08-08** (§2), which needed
neither — it had been filed under "needs real LLM spend" and sat there for five
days because of it. Everything still open needs model spend, a Slack workspace,
a real handset, or an operator's decision. Nothing left is blocked on writing
code.

**Revised 2026-08-09.** The video track added three items, and one of them
moves this file's own claim: **§1 is no longer empty.** `T-V3` and `T-17b` both
landed with gates that need the compose stack and nothing else, which is the
bucket that gets run — the two items in it on 2026-08-04 produced three
findings between them. §1a below is where they are, deliberately kept out of §2
so the mistake this file recorded on 2026-08-08 is not repeated: a gate filed
behind a cost it does not have is a gate nobody runs.

**Revised again the same day: §1a was run.** It cost about ninety minutes and
produced **three defects**, all fixed and re-proven. Filing them in the cheap
bucket was the difference between an afternoon and a backlog entry, and the
count is now eleven findings across three sittings of a gate that only ever
needed the stack.

Nothing here is blocked on a decision about *how* to build something. Each item
needs one of three things: the stack up, money spent, or a message sent to a real
person's phone.

---

## 1. ~~Needs the local stack, nothing else~~ — run 2026-08-04

| Owed by | The gate | Outcome |
| ------- | -------- | ------- |
| `T-15` | Local receiver, watcher breach, signature verified; plus a receiver answering `500` twenty times | **Pass.** Body carried value and threshold, HMAC verified over the raw bytes and failed on a tampered copy, and the failing subscription disabled itself on the twentieth while the healthy one stayed at zero ([`outbound-webhooks.md`](outbound-webhooks.md) §7) |
| `T-M4` | Propose → approve → the courier showing the effect once, plus the reject case and both audit rows | **Pass.** One `cancel_shipment` line on the courier after approve, none after reject, five audit rows across the two decisions ([`mcp-source.md`](mcp-source.md) §T-M4) |
| `T-14` | An MCP client connecting with a key, listing tools, retrieving a metric; the audit row and usage event that follow | **Pass with a defect, since fixed.** Handshake, `401`-before-session, metric retrieval, the `read:metrics`-cannot-`run_sql` split, `agent_actions` and `usage_events` all as designed — but the surface was **7 tools, not 8**: `list_watchers` was advertised and did not exist ([`mcp-server.md`](mcp-server.md) §7) |
| `T-07b` | One dashboard turn returning `[EMAIL REDACTED]` | **Pass.** Same question under `strict` and `contact_ok`, nothing else changed ([`guardrail-overreach.md`](guardrail-overreach.md) §4) |
| `T-09`, `T-11` | The non-admin renderings of both UIs, photographed | **Failed, then fixed and re-photographed.** Two of six watcher controls were gated and four were not — `Pause` among them, because it is `Enable`'s other branch — and the approval card had no role check at all. The `403`s were all real ([`watchers-ui.md`](watchers-ui.md), [`action-framework.md`](action-framework.md)) |

The one thing the run needed that no document named:
`API_V1_CALLBACK_ALLOW_PRIVATE=true` for a loopback webhook receiver — separate
from `MCP_ALLOW_PRIVATE_EGRESS`, and the two are easy to confuse.

## 1a. ~~Needs the local stack, nothing else~~ — added and run 2026-08-09

| Owed by | The gate | Outcome |
| ------- | -------- | ------- |
| `T-V3` | A video through `POST /v1/reports/render`: the 202, the `render_progress` events, and the download. Then one through a real turn. Then the four refusals: the invoice, the cap (asserted by an **empty access log on the render service**, the same way `T-A2` asserts nothing was uploaded), the 402, and the unconfigured-service message | **Pass on every item, with two defects fixed on the way.** The stream never ended for a threadless render — progress to 0.94, then heartbeats for ten minutes against a report already `completed` — and the scene cap was the worker's rather than the door's, so a spec that can never render was answered `202`. Both fixed and re-run ([`report-video.md`](report-video.md) §6) |
| `T-17b` | One waterfall spanning both processes: `cmd/api`'s span, the queue wait, `cmd/worker`'s turn | **Failed, then fixed and re-read.** `argentum-api` was absent from Jaeger's service list: `cmd/api` installed a tracer and started no span, so `Inject` had nothing to propagate and every turn was its own root trace. A server-span middleware landed; the waterfall now shows one trace across both processes with **934 ms** in the queue ([`observability.md`](observability.md) §10a) |
| `T-V2` | The image in a cluster: the readiness probe passing, the `egress: []` NetworkPolicy holding, the emptyDir sized for a render | **Still open.** The image itself was built and run on 2026-08-09 ([`report-video.md`](report-video.md) §8) and found five defects. What is left is Kubernetes-shaped and needs a cluster rather than Docker |
| `T-V5` | The scene contact sheet, and the pale-brand frame beside the PDF cover | **Owed, added 2026-08-09.** The still export exists and produces them (`--stills` on the fixture CLI); what is missing is the render service running and a place to put the PNGs. Both are photographs of behaviour that is already shared code — one `theme.Readable` call against one floor, for all three formats ([`report-motion.md`](report-motion.md) §4) |
| `T-V4` | The shared player rendering in Chrome, Safari and Firefox, and the notice a plan with an unknown version shows | **Owed, added 2026-08-09.** Everything server-side passed and one defect was fixed ([`report-player.md`](report-player.md) §7); these two are visual and need a human opening the page. Same shape as `T-R4`'s four-application check, one surface further out — and cheaper, because the page is served by `pnpm dev` rather than by an office suite |

**Added 2026-08-09, and ~~owed~~ run on 2026-08-10.** `T-19`'s and the widget
phase's stack-only gates were in this bucket and have now been run — with the
migrations, the mint matrix, an end-to-end turn and two-visitor isolation all
passing. Details in [`embed-auth.md`](embed-auth.md) §5 and
[`widget.md`](widget.md) §5. What remains on the track needs a browser and a
second origin, which is §3's bucket rather than this one.

**The reason it sat for a day is worth recording**, because it is the same
failure this file was written about: `docker info` answered *"client version
1.43 is too old"* and was read as "Docker is not running". The daemon had been
up for 36 hours with the whole stack healthy. A gate skipped over a misread
error message costs exactly as much as one filed in the wrong bucket.

| Owed by | The gate | Outcome |
| ------- | -------- | ------- |
| `T-19` | Migration `051` applied up **and** down against a real Postgres; then a `curl` transcript of a successful session mint and a forged one, plus one from an origin that is not on the key's allowlist (expect `403`) | **Pass, 2026-08-10.** Up/down/up clean from version 50; eight-case mint matrix over HTTP matching the unit tests exactly; revoke refusing the next mint; the token carrying no `sub` and no `role`; cross-family refusal both ways. No defect found — the matrix was re-running a table-driven test that already existed ([`embed-auth.md`](embed-auth.md) §5). ~~**Owed.**~~ The full refusal matrix passes as unit tests, including both cross-family token checks; what no test covers is the migration itself and the three responses as an integrator would see them ([`embed-auth.md`](embed-auth.md) §5) |
| `T-19` | The Embed tab in a browser: create a key, copy the secret once, edit the origin list, pause, resume, revoke. Then one real cross-origin preflight of `POST /api/embed/session` from a page on another origin | **Owed.** `tsc -b` is clean and every route it calls has a test; the preflight is the half that needs a second origin serving a page, and it is the one `EmbedCORS` exists for |

**Added 2026-08-10, with the widget phase built.** Same bucket, same cost.

| Owed by | The gate | Outcome |
| ------- | -------- | ------- |
| `T-20`→`T-23` | Migrations `051`, `052`, `053` up **and** down; then one widget turn end to end — mint a session, send a question, watch the answer stream into the panel — and the same visitor's conversation still there after a reload | **Pass, 2026-08-10.** All three up/down/up; a real turn answered from the demo warehouse (4 tables, 1,612 rows, `get_schema` then `run_sql`, 6,476 µUSD); `threads/current` resolving it afterwards; `agent_actions` reading `embed \| emp_812 \| widget`; `usage_events` showing `widget` beside the other four channels; and T-23's config reaching a live session with no redeploy ([`widget.md`](widget.md) §5) |
| `T-21` | The panel in Chrome, Safari and Firefox, the full-screen sheet under 640px, and a real cross-origin preflight from a page on a second origin | **Run in Chrome 2026-08-10 — four defects, all fixed.** A 404 preflight that blocked every browser from the whole surface, an ES-module bundle a sandboxed frame cannot load, root-absolute asset URLs, and a session minted from an origin no allowlist can match. Then a live turn streamed into the panel. Safari and Firefox are still owed, and the narrow-viewport sheet needs a device — Chrome would not size below 662 CSS px ([`widget.md`](widget.md) §5a) |
| `T-20` | Thread ownership with two signed identities: visitor B passing visitor A's thread id, expecting 404 | **Pass, 2026-08-10.** Two real sessions from one key: B reading A's transcript → 404, B *posting into* A's thread → 404, B's own `threads/current` → null, A still 200. The write direction is the one no unit test had covered end to end |

**Three items, three defects, and the same pattern for the fourth time.** Every
one of them is a seam between two processes that no unit test crosses, and each
had passing tests over the parts either side of it. `T-17b`'s is the sharpest:
nine tests asserted the carrier travels, and all nine built a context that
already held a span — the one condition production never met.

What the run needed that no document names: the API refuses to boot without
WhatsApp credentials on a deployment that uses no WhatsApp
([`report-video.md`](report-video.md) §6).

## 2. Needs the stack **and** real LLM spend

| Owed by | The gate | Cost |
| ------- | -------- | ---- |
| `T-07b` | `make eval` on both sides of switching the output guardrails on | Two full runs of the 40-case golden set. The risk it measures is narrow — the rules only rewrite a final reply, and an answer containing an email address or an Indonesian phone number is the only shape that can score differently — but the ticket asks for it and activation is a behaviour change on every turn |
| `T-A2b` | Ten live agentic report calls, confirming the injection guardrail no longer refuses them | The fix (the directive moved into the system prompt) is proven by construction and by one gate run; ten is what the ticket asked for |
| `T-R4` | Three unautomatable applications of the deck renderer | Opening the generated `.pptx` in PowerPoint, Keynote and Google Slides. No test can do this |
| `T-18` | The final eval run → `docs/coverage/eval-sprint1.md`, compared against baseline | One full run of the golden set. **Order matters:** run `T-07b`'s before/after pair first, or the guardrail question gets answered against a baseline this run has already moved ([`launch-hygiene.md`](launch-hygiene.md) §6) |
| The prompt-contradiction fix (2026-08-09) | `report-directive-is-not-an-injection` passing on both models | The guardrail slice is ~$0.42 on haiku (8 cases, measured 2026-08-08); the full set is ~$2.10. The fix removes the chart guidelines from a turn whose deliverable is a file, which is a mechanism with an argument behind it and **no number** — the deterministic half is tested, and whether the case now passes is exactly what a golden set exists to answer ([`delivery-log.md`](delivery-log.md) Phase 2g) |
| ~~`T-17`~~ | ~~`curl` the exposition; one trace waterfall for a tool-calling turn~~ | **Both run 2026-08-08 — ticket closed.** Exposition: 401 / 401 / 200 with the right token, queue gauges reading a queue discovered from Redis. Waterfall: one `agent.turn` of 7,750 ms with 18 ms inside `query_metric`, which is the LLM/SQL split the ticket asked for. It cost one case of model spend, and it found the defect §9 records — `memory.hydrate` was landing in its own trace ([`observability.md`](observability.md) §8–§9) |

~~`T-17`'s exposition half needs no spend and was not run on 2026-08-04~~ —
**run on 2026-08-08**, against the compose stack with the API on the host. It
cost nothing but the stack, exactly as this paragraph claimed, and it is worth
recording why it sat out twice: it was written down in a *group 2* table headed
"needs real LLM spend", so every reading of this file filed it behind a cost it
did not have. A gate in the wrong bucket is a gate nobody runs.

## 2a. Needs a Slack workspace

| Owed by | The gate | Why it is deferred |
| ------- | -------- | ------------------ |
| Slack channel | An @mention answered in-thread, a follow-up inside that thread landing on the same `conversation_threads` row, and `/api/usage/by-channel` showing `slack` with a non-zero cost | Needs a Slack app installed in a real workspace — signing secret, bot token, Event Subscriptions pointed at a reachable host. No CI job and no local stack can supply one. Steps: [`slack-channel.md`](slack-channel.md) §7 |
| Slack watcher delivery | A breach posting top-level in a channel, and the delivery row reading `delivered` | Same workspace. The unit test pins the part a workspace cannot teach us — that the post carries no `thread_ts` — so what the gate adds is proof the token and channel id are right |

## 3. Needs somebody's real phone

| Owed by | The gate | Why it is deferred |
| ------- | -------- | ------------------ |
| `send_message`'s document link (T-V3) | A real WhatsApp message carrying a presigned link, opened on a handset | Same deferral as the row below and the same reason: it goes to a real phone. What a test cannot show is that the link survives WhatsApp's own URL handling and that the markdown-link flattening the chat path already does reaches this body too ([`report-video.md`](report-video.md) §8) |
| `T-12a` | The message arrives | `.env` holds live Twilio credentials and the worker delivers, so closing this sends a real WhatsApp message to a real handset. **Deferred by the repo owner**, not by an implementer. Both halves of the ticket's gate are owed, because the un-allowlisted-target refusal is only reachable by approving a proposal ([`delivery-log.md`](delivery-log.md) Phase 2c) |

## 4. Needs an operator's decision, not a gate

| Owed by | What | Why it is not guessed at |
| ------- | ---- | ------------------------ |
| `T-14` | A Helm deployment for `cmd/mcp` | `Dockerfile.mcp` exists and matches the discord image's shape, but the chart has no `deployment-mcp.yaml`. The ingress is where a hostname and a TLS certificate get decided, and both are an operator's call ([`mcp-server.md`](mcp-server.md) §8) |
| ~~`T-14`~~ | ~~`list_watchers`: write the tool, or delete the promise~~ | **Decided 2026-08-04: deleted.** The registry is shared with the agent, so writing it would have put a tool nobody asked for into every turn's prompt ([`mcp-server.md`](mcp-server.md) §7) |
| ~~`T-09`/`T-11`~~ | ~~Whether a member sees a disabled control or no control~~ | **Decided 2026-08-04: disabled, with a sentence.** Hiding a control makes a member think the feature is missing; disabled tells them who to ask ([`watchers-ui.md`](watchers-ui.md), [`action-framework.md`](action-framework.md)) |

---

## What running group 1 cost, for the next estimate

About two hours end to end, of which the auto-disable was 24 minutes of waiting
(twenty terminal failures, each already five attempts with backoff) and roughly
thirty briefing turns of LLM spend, because the watcher driving it fired every
minute with a zero cooldown. A cheaper shape for the next run: keep the
minute cron for the first breach, then raise the cooldown before pointing a
second subscription at a failing receiver.
