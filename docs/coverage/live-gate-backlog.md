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

Nothing here is blocked on a decision about *how* to build something. Each item
needs one of three things: the stack up, money spent, or a message sent to a real
person's phone.

---

## 1. Needs the local stack, nothing else

Cheapest group. `make infra` plus the API and worker, no LLM spend beyond what a
turn costs, no outside party.

| Owed by | The gate | What it would prove |
| ------- | -------- | ------------------- |
| `T-15` | Local receiver, trigger a watcher breach, verify the signature against the workspace secret | The fan-out reaches a real HTTP server and the HMAC verifies. Worth adding: a receiver that answers `500` twenty times, so auto-disable is watched rather than reasoned about ([`outbound-webhooks.md`](outbound-webhooks.md) §7) |
| `T-M4` | Propose → approve → the courier showing the effect once, plus the reject case and both audit rows | That a write tool proposes rather than executes, over the wire, against the same Go MCP server `T-M2` was gated on ([`mcp-source.md`](mcp-source.md) §T-M4) |
| `T-14` | Claude Code connecting with a key, listing tools, retrieving a metric; the audit row and usage event that follow | The handshake and the transport. Everything below the protocol is already proven — this is the layer nothing has exercised ([`mcp-server.md`](mcp-server.md) §6) |
| `T-07b` | One dashboard turn returning `[EMAIL REDACTED]` | That the output rules fire on a real streaming turn, not only at the seam a unit test calls ([`guardrail-overreach.md`](guardrail-overreach.md) §4) |
| `T-09`, `T-11` | The non-admin renderings of both UIs, photographed | Both refusals are proven at the API; neither disabled control has been seen. The smallest item here ([`watchers-ui.md`](watchers-ui.md), [`action-framework.md`](action-framework.md)) |

## 2. Needs the stack **and** real LLM spend

| Owed by | The gate | Cost |
| ------- | -------- | ---- |
| `T-07b` | `make eval` on both sides of switching the output guardrails on | Two full runs of the 40-case golden set. The risk it measures is narrow — the rules only rewrite a final reply, and an answer containing an email address or an Indonesian phone number is the only shape that can score differently — but the ticket asks for it and activation is a behaviour change on every turn |
| `T-A2b` | Ten live agentic report calls, confirming the injection guardrail no longer refuses them | The fix (the directive moved into the system prompt) is proven by construction and by one gate run; ten is what the ticket asked for |
| `T-R4` | Three unautomatable applications of the deck renderer | Opening the generated `.pptx` in PowerPoint, Keynote and Google Slides. No test can do this |
| `T-18` | The final eval run → `docs/coverage/eval-sprint1.md`, compared against baseline | One full run of the golden set. **Order matters:** run `T-07b`'s before/after pair first, or the guardrail question gets answered against a baseline this run has already moved ([`launch-hygiene.md`](launch-hygiene.md) §6) |
| `T-17` | `curl` the exposition; one trace waterfall for a tool-calling turn | The exposition needs only the stack; the waterfall needs a collector — a local Jaeger or an OTel collector in the compose file, which is itself not written ([`observability.md`](observability.md) §6) |

## 3. Needs somebody's real phone

| Owed by | The gate | Why it is deferred |
| ------- | -------- | ------------------ |
| `T-12a` | The message arrives | `.env` holds live Twilio credentials and the worker delivers, so closing this sends a real WhatsApp message to a real handset. **Deferred by the repo owner**, not by an implementer. Both halves of the ticket's gate are owed, because the un-allowlisted-target refusal is only reachable by approving a proposal ([`delivery-log.md`](delivery-log.md) Phase 2c) |

## 4. Needs an operator's decision, not a gate

| Owed by | What | Why it is not guessed at |
| ------- | ---- | ------------------------ |
| `T-14` | A Helm deployment for `cmd/mcp` | `Dockerfile.mcp` exists and matches the discord image's shape, but the chart has no `deployment-mcp.yaml`. The ingress is where a hostname and a TLS certificate get decided, and both are an operator's call ([`mcp-server.md`](mcp-server.md) §6) |

---

## How to run group 1 in one sitting

They share a stack, and three of them share a tenant. In dependency order:

1. Bring up the stack; confirm `schema_migrations` reaches `046` on the API's
   boot (`045` is `T-07b`'s, `046` is `T-15`'s).
2. **`T-07b`** first, because it changes what every later transcript shows: ask a
   question whose answer contains a customer email under `strict`, then flip the
   company to `contact_ok` in Settings → General and ask again.
3. **`T-15`** next: a local receiver on a port the deployment can reach, a
   subscription to `watcher.breached`, and a watcher whose threshold is already
   breached. Then point a second subscription at a receiver that always answers
   `500` and let it disable itself.
4. **`T-M4`** and **`T-14`** together, since both want the courier MCP server
   from `T-M2`'s run: register it, approve a write tool, watch a proposal go
   through the card; then point Claude Code at `cmd/mcp` with a key and ask it
   for a metric.
5. **`T-09`/`T-11`** last: two screenshots as a member rather than an admin.

Group 2's eval pair is the only item that costs money, and it is the only one
that should wait for an explicit yes.
