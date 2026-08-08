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

## 2. Needs the stack **and** real LLM spend

| Owed by | The gate | Cost |
| ------- | -------- | ---- |
| `T-07b` | `make eval` on both sides of switching the output guardrails on | Two full runs of the 40-case golden set. The risk it measures is narrow — the rules only rewrite a final reply, and an answer containing an email address or an Indonesian phone number is the only shape that can score differently — but the ticket asks for it and activation is a behaviour change on every turn |
| `T-A2b` | Ten live agentic report calls, confirming the injection guardrail no longer refuses them | The fix (the directive moved into the system prompt) is proven by construction and by one gate run; ten is what the ticket asked for |
| `T-R4` | Three unautomatable applications of the deck renderer | Opening the generated `.pptx` in PowerPoint, Keynote and Google Slides. No test can do this |
| `T-18` | The final eval run → `docs/coverage/eval-sprint1.md`, compared against baseline | One full run of the golden set. **Order matters:** run `T-07b`'s before/after pair first, or the guardrail question gets answered against a baseline this run has already moved ([`launch-hygiene.md`](launch-hygiene.md) §6) |
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
