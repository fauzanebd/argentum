# The Gelael pilot — the first integration outside this repository

**Built 2026-08-09.** Record of the integration that fired §8e's widget trigger:
what it is, what it exercised, what it found, and what it still owes before any
of it counts as proven.

The code lives in **`gelael-member`**, a separate repository — Smartsoft's
membership and loyalty platform for Gelael Supermarket. **Nothing in this
repository changed.** That is the finding, not an omission: `T-A1`→`T-A5` were
built so a server-side consumer needs nothing new from us, and the first one did
not.

Decision and its reasoning: [`../plan/00-sprint-overview.md`](../plan/00-sprint-overview.md) §9.

---

## 1. What was built

A **Tanya Data** page in the Gelael admin dashboard (Next.js 14 app router,
next-auth, TailAdmin). An admin types a question in Indonesian and reads a
streamed answer drawn from Gelael's own MySQL data, without leaving the tool
they already have open.

Nine files, one of them an edit:

| Path (in `gelael-member/apps/dashboard/`) | What |
| --- | --- |
| `utils/argentumServer.ts` | Server-only: base URL, API key, caller identity from the next-auth session, the upstream fetch, one error envelope |
| `app/api/argentum/chat/route.ts` | `POST` → `POST /v1/chat`, SSE passed through untouched |
| `app/api/argentum/agents/route.ts` | `GET` → `GET /v1/agents` |
| `app/api/argentum/threads/[id]/messages/route.ts` | `GET` → the transcript, behind an ownership check (§3.1) |
| `api/argentum.ts` | Browser client and SSE frame parser |
| `components/Argentum/ArgentumChat.tsx` | The chat surface — streaming, tool chips, agent picker, per-turn cost |
| `app/dashboard/argentum/page.tsx` | The route |
| `types/argentum.ts` | Agent, turn, message, event and error types, hand-written from the spec |
| `components/Sidebar/index.tsx` | One menu item |

**Verified:** `tsc --noEmit` clean, `next build` succeeds with all three API
routes and the page in its output. **Not verified:** anything that needs a live
deployment — see §5. No turn has been sent.

## 2. The contract it exercised

Everything below came from the published spec with no additions, no exceptions
and no undocumented headers:

- **`POST /v1/chat` with `Accept: text/event-stream`** — the whole surface of
  the integration. `message`, `user_ref`, optional `thread_id`, optional
  `agent_id`.
- **`Idempotency-Key` on every send.** The host generates one UUID per logical
  turn.
- **The SSE vocabulary** — `started`, `delta`, `thinking`, `tool_call`,
  `tool_result`, `error`, `final`. The client ignores names it does not know,
  which is what the contract asks of it.
- **Thread continuity by `thread_id`**, carried from `started` and sent back on
  the next turn.
- **`user_ref` as the tenant's own identity for a person** —
  `gelael-dashboard:<email>`, namespaced so a second Gelael surface calling the
  same workspace stays distinguishable in `usage/by-user`.
- **`GET /v1/agents`** for the picker. Keyless by design, which made it the
  cheapest possible smoke test of the whole arrangement.
- **The typed error envelope**, rendered to the user as its `message`.

**Nothing was missing.** The one thing the integrator had to read Go for is in
§3.3, and it is a documentation gap rather than a contract gap.

## 3. What it found

### 3.1 A tenant who skips the ownership check has a silent data leak

`/v1` authorises the **workspace**. The key the Gelael dashboard holds can read
every thread in it, so an admin passing a colleague's `thread_id` to the
transcript route would have been served it — no error, no audit signal, no way
for the reader to know it was not theirs.

The pilot fetches `GET /v1/threads/{id}`, compares `user_ref` with the caller's,
and answers a mismatch with **404 rather than 403** — a wrong id and someone
else's id are deliberately one answer.

`T-20` already specifies exactly this check for `/api/embed`, which is the good
news: the design anticipated it. What the pilot adds is evidence that a tenant
integrating over `/v1` has to write it themselves and will not be told when they
have not. **`T-22`'s docs must carry it as a rule with an example, not as a
note** — a per-user surface built on a workspace key is the default shape of
every integration like this one.

### 3.2 A streamed answer dies behind a default reverse proxy

Nginx buffers proxied responses, so an SSE stream arrives as one lump once the
turn is already finished. Locally there is no proxy and it streams perfectly;
the failure appears only in a cluster, where it reads as "the widget is slow"
rather than as a configuration problem.

`X-Accel-Buffering: no` on the response fixes it. This belongs in `T-22`'s
troubleshooting table beside the CSP row — same class of problem, same
first-hour cost, and the CSP row is already there because every widget product
learns it.

### 3.3 `final` carries the answer; the deltas are a preview of it

`v1_chat.go` reads the persisted assistant message on `final` rather than
echoing the accumulated content, so `final.message.content` is the answer of
record and a client that only concatenates `delta` frames can end up subtly
different from what the thread will show on reload.

The quickstart's Node example prints deltas and then ignores `final`'s message,
which is fine for a terminal and wrong for a transcript. **This one is ours to
fix, in [`../api/quickstart.md`](../api/quickstart.md)** — the widget will hit
it too, and one sentence in §5 of that document prevents it.

## 4. What the pilot deliberately is not

- **It is not the widget, and it is not evidence about the widget's security
  model.** No browser-held credential, no origin allowlist, no HMAC identity, no
  short-lived session token — the four things `T-19` is. The pilot's key is
  workspace-wide and server-side, which is the arrangement `T-19` exists to make
  unnecessary for tenants who cannot hold one safely.
- **It is not reusable.** Every line of it is Gelael's Next.js app. The next
  tenant starts from zero, which is the argument for the phase.
- **It is throwaway, by about a day.** Stated in the sprint overview §9a and in
  the Gelael plan file, so nobody later reads the pilot as the plan.
- **It is staff-only.** Anonymous visitors asking questions of a warehouse is
  the *Public / anonymous widget mode* backlog entry, with its own threat model
  and its own trigger.

## 5. What is owed

Gate-shaped, because everything in §1 is code that compiles and nothing in it
has answered a question:

| # | Owed | Needs |
| - | ---- | ----- |
| 1 | An Argentum workspace for Gelael, MySQL connected as a read-only source | A deployment and a DSN |
| 2 | An API key scoped `write:chat` + `read:threads`, set on the dashboard Deployment | The workspace, and an `infra` commit |
| 3 | **One real question answered end to end by a human**, against production data | 1 and 2 |
| 4 | The ownership check exercised against a real colleague's thread id — expect 404 | 3, plus a second admin account |
| 5 | Streaming confirmed *through the cluster's proxy*, not just locally | 3, deployed |
| 6 | A decision on who may spend credits — today every admin who sees the sidebar can | A human decision, not code |

Items 3–5 are the ones that would change what this file claims. Until they run,
§2's "nothing was missing" is a statement about the contract as **read**, not as
**exercised**, and this file says so in its own §1 for the same reason
[`live-gate-backlog.md`](live-gate-backlog.md) exists.

## 6. What the widget phase should take from it

1. `T-20`'s per-`embed_user_ref` thread ownership check is not a nicety. §3.1 is
   what happens without it.
2. `T-22` gains two troubleshooting rows: proxy buffering (§3.2) and the
   `final`-versus-deltas rule (§3.3).
3. `T-22`'s gate has a real host now. Integrate Gelael a second time using only
   the published docs and compare it against what §1 actually took — a
   documented path that is slower than the bespoke one is a failed gate, and no
   throwaway Vite app can tell you that.
4. `T-23`'s live preview has a real tenant to preview against, with real data,
   real branding and staff who will say whether the answer was useful.
