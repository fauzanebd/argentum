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

Nothing here is blocked on a decision about *how* to build something. Each item
needs one of three things: the stack up, money spent, or a message sent to a real
person's phone.

---

## 1. ~~Needs the local stack, nothing else~~ — run 2026-08-04

| Owed by | The gate | Outcome |
| ------- | -------- | ------- |
| `T-15` | Local receiver, watcher breach, signature verified; plus a receiver answering `500` twenty times | **Pass.** Body carried value and threshold, HMAC verified over the raw bytes and failed on a tampered copy, and the failing subscription disabled itself on the twentieth while the healthy one stayed at zero ([`outbound-webhooks.md`](outbound-webhooks.md) §7) |
| `T-M4` | Propose → approve → the courier showing the effect once, plus the reject case and both audit rows | **Pass.** One `cancel_shipment` line on the courier after approve, none after reject, five audit rows across the two decisions ([`mcp-source.md`](mcp-source.md) §T-M4) |
| `T-14` | An MCP client connecting with a key, listing tools, retrieving a metric; the audit row and usage event that follow | **Pass with a defect.** Handshake, `401`-before-session, metric retrieval, the `read:metrics`-cannot-`run_sql` split, `agent_actions` and `usage_events` all as designed — but the surface is **7 tools, not 8**: `list_watchers` is advertised and does not exist ([`mcp-server.md`](mcp-server.md) §7) |
| `T-07b` | One dashboard turn returning `[EMAIL REDACTED]` | **Pass.** Same question under `strict` and `contact_ok`, nothing else changed ([`guardrail-overreach.md`](guardrail-overreach.md) §4) |
| `T-09`, `T-11` | The non-admin renderings of both UIs, photographed | **Fail — the rendering does not exist.** A member sees every admin control enabled on both surfaces, and the admin's card is pixel-identical. The `403`s are all real ([`watchers-ui.md`](watchers-ui.md), [`action-framework.md`](action-framework.md)) |

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
| `T-17` | `curl` the exposition; one trace waterfall for a tool-calling turn | The exposition needs only the stack; the waterfall needs a collector — a local Jaeger or an OTel collector in the compose file, which is itself not written ([`observability.md`](observability.md) §6) |

`T-17`'s exposition half needs no spend and was not run on 2026-08-04 — it is a
`curl` against a running API with `METRICS_TOKEN` set, and the only reason it sat
out is that the group-1 list did not carry it.

## 3. Needs somebody's real phone

| Owed by | The gate | Why it is deferred |
| ------- | -------- | ------------------ |
| `T-12a` | The message arrives | `.env` holds live Twilio credentials and the worker delivers, so closing this sends a real WhatsApp message to a real handset. **Deferred by the repo owner**, not by an implementer. Both halves of the ticket's gate are owed, because the un-allowlisted-target refusal is only reachable by approving a proposal ([`delivery-log.md`](delivery-log.md) Phase 2c) |

## 4. Needs an operator's decision, not a gate

| Owed by | What | Why it is not guessed at |
| ------- | ---- | ------------------------ |
| `T-14` | A Helm deployment for `cmd/mcp` | `Dockerfile.mcp` exists and matches the discord image's shape, but the chart has no `deployment-mcp.yaml`. The ingress is where a hostname and a TLS certificate get decided, and both are an operator's call ([`mcp-server.md`](mcp-server.md) §8) |
| `T-14` | `list_watchers`: write the tool, or delete the promise | Writing it puts the tool in the *agent's* registry too, which changes every turn's prompt; deleting it is two doc rows and a map entry. A product call, not an implementation one ([`mcp-server.md`](mcp-server.md) §7) |
| `T-09`/`T-11` | Whether a member sees a disabled control or no control | The fix is one change across both surfaces — the pending payload and the watcher row would carry `can_decide` / `can_edit` — but which of the two renderings is wanted is a design decision |

---

## What running group 1 cost, for the next estimate

About two hours end to end, of which the auto-disable was 24 minutes of waiting
(twenty terminal failures, each already five attempts with backoff) and roughly
thirty briefing turns of LLM spend, because the watcher driving it fired every
minute with a zero cooldown. A cheaper shape for the next run: keep the
minute cron for the first breach, then raise the cooldown before pointing a
second subscription at a failing receiver.
