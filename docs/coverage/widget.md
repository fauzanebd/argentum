# T-20 → T-23 — the widget: channel, client, docs and configuration

**Built 2026-08-09/10**, on `T-19`'s auth chain
([`embed-auth.md`](embed-auth.md)). Migrations `052_thread_embed` and
`053_company_widget_config`.

The phase's own decision and trigger:
[`../plan/00-sprint-overview.md`](../plan/00-sprint-overview.md) §9. What fired
it: [`gelael-pilot.md`](gelael-pilot.md).

---

## 1. T-20 — the widget is a channel

`domain.ChannelWidget`, and then every switch on `Channel` handled rather than
left to fall through:

- **`ChatEnqueuer.validate`** — a widget turn without an `embed_user_ref` is a
  wiring bug, not a caller's choice, and is refused. Stricter than the `api`
  arm, which accepts a thread id instead, because the session token always
  carries a ref and every widget read is scoped by it.
- **`ChatEnqueuer.Enqueue`** — the `api` arm's shape with one difference: the
  thread id is never taken on trust. A widget client runs on a page we do not
  control, so a thread id it sends is a value a visitor can edit. Company,
  channel **and** ref are compared, and a mismatch is `ErrNotFound`.
- **`ChatRunner.completeWith`** — a deliberate no-op with a comment saying so,
  exactly like `ChannelAPI`. Delivery is the WebSocket the browser already
  holds; an outbound provider here would send a second copy of an answer that
  is already on screen.
- **`actorOf`** — `actor_kind=embed`, `actor_ref=<embed_user_ref>`. Its own kind
  rather than joining the `ActorKindUser` list: every ref in that list is an
  identity Argentum authenticated, and this is a name the tenant chose and
  vouched for. An API key still outranks it.
- **`UsageByUser`** — `embed_user_ref` is the fifth arm of the COALESCE and the
  fifth `user_key_kind`, so widget spend is attributable per visitor in the
  report a tenant reads to police their own integration.

**Five routes and nothing else** (`/api/embed`, behind `EmbedAuth`): `config`,
`chat`, `threads/current`, `threads/:id/messages`, `threads/:id/stream`. No
connections, no settings, no usage, no metrics, no audit, no documents. The list
is short because every route on it is reachable from a page we do not control,
so the question for each is not "would this be useful?" but "is a visitor of a
tenant's website entitled to it?".

### 1a. `embed_user_ref` is its own column, not `api_user_ref`

Reusing it would have been one migration shorter. It is wrong for the reason
`T-A3` refuses to let a `/v1` key append to a dashboard thread: the two surfaces
are reached with different credentials, and a filter that forgot to compare
`channel` would let one read the other's conversations. The separate column
makes that impossible rather than merely unlikely.

### 1b. `threads/current` does not create a thread

A page that mounts the widget on every route would otherwise write an empty
conversation per page view. The first message is where a conversation begins;
`send` with no `thread_id` opens it.

## 2. T-21 — the client

`apps/widget`: a framework-free loader and a Preact app, from one source. Sizes
on the last build, against the ticket's budgets:

| Output | Budget | Actual |
| ------ | ------ | ------ |
| Loader (`argentum-widget.js`) | 15 KB gz | **1.6 KB** |
| Iframe app (js + css + html) | 80 KB gz | **32.0 KB** |

`pnpm size` enforces both and exits non-zero on a breach, because the way a
15 KB loader becomes a 60 KB one is a dependency added on a Tuesday with nobody
looking at a number.

**The sandbox is `allow-scripts allow-forms`** — deliberately without
`allow-same-origin`, so a compromised widget cannot read anything else stored on
the origin it is served from.

**The session token lives in a closure and nowhere else.** Not `localStorage`,
not a cookie, not the host page. The host holds the *signature*, which is
useless without an allowlisted origin.

**Model output is sanitised before it is rendered**: `marked` → DOMPurify →
insert, with a tag allowlist, no `img`, and `noopener` forced on every link. The
answer is text an LLM produced from a tenant's warehouse, and a product name in
a table can contain markup as easily as a prompt can.

### 2a. `packages/chat-ui` was not extracted

The ticket says *extract, do not port*, and this did neither: the widget has its
own small Preact UI. The reasoning, the cost, and the two events that should
trigger paying it are in
[`../../apps/widget/README.md`](../../apps/widget/README.md) — recorded there
rather than left as a silence, because the drift the ticket warned about is
real and now exists.

## 3. T-22 — the docs

[`apps/backend/docs/embed/README.md`](../../apps/backend/docs/embed/README.md):
key, signing endpoint, script tag, the full option reference, the security model
in five sentences, the CSP block, and a troubleshooting table.

Two of its rows were paid for by the Gelael pilot before the widget existed
([`gelael-pilot.md`](gelael-pilot.md) §3): proxy buffering, and `final` versus
the deltas. A third — the origin allowlist as a rule with an example — is the
one the pilot had to implement by hand.

`apps/widget/examples/vanilla/` is a working page plus a thirty-line signing
server.

**Not done from this ticket:** the npm packages (`@argentum/widget`,
`@argentum/widget-react`), the versioned CDN path, changesets, and the react /
vue / nextjs examples. `dist/` is static and deployable anywhere today, which is
what the Gelael integration needs; publishing is what the *next* tenant needs.
It is the cheapest remaining piece of the phase and it is called out in §5.

## 4. T-23 — configuration

`companies.widget_config` jsonb (053), the same shape as `report_branding`
(022): greeting, up to five suggested prompts, locale, accent, radius, mode,
launcher and position.

- **Defaults are applied on read, not stored on write.** A tenant who configured
  nothing and a tenant who chose our defaults get the same widget, and changing
  a default reaches everyone who never overrode it.
- **The edit form reads the stored record**, not the defaulted one — showing an
  admin our defaults as though they had chosen them makes every unset field look
  set.
- **The accent is validated as a hex colour and refused otherwise.** It becomes
  a CSS value in somebody else's page, so accepting arbitrary text is a style
  injection rather than a wrong colour.
- **The preview is drawn from the form, not from what is saved.** An admin who
  has to press Save to see a colour presses Save with the wrong one.

Config reaches a deployed widget without a redeploy, because `GET
/api/embed/config` is a live read.

### 4a. A test caught `omitempty` eating the contract

`suggested_prompts` was `json:"...,omitempty"`, so a tenant with no prompts sent
the widget no key at all rather than an empty list — and a client reading
`.length` gets a TypeError instead of zero. The tag is gone and the comment
explaining why is on the field. It is the second defect in this phase found by a
test rather than by a gate, after `T-19`'s `sub` claim.

## 5. The gate, run 2026-08-10

**A widget turn was served end to end**, which is what this section previously
said had never happened. Docker had been up the whole time — the earlier check
failed on a client/daemon API version mismatch (`1.43` against a daemon
requiring `1.44+`), which reported as "not running". Worth recording: the
project lost a day of gate time to a misread error message, not to a missing
dependency.

**Migrations.** Schema was at 50. `051`, `052`, `053` applied up, rolled **all
three down** — table dropped, both columns gone, version back to 50 — and
re-applied. The partial index and the `'{}'::jsonb` default came back on the
second pass.

**The turn.** A fresh workspace, the demo warehouse connected, the worker
running:

```
POST /api/embed/session   → 200, a 15-minute token
POST /api/embed/chat      → 202  {thread_id, task_id, is_new_thread:true}
GET  /api/embed/threads/current   → the thread, titled "Database Table Row Counts"
GET  /api/embed/threads/{id}/messages → the question and the answer
```

The answer was drawn from real data — four tables, 1,612 rows, counted by the
agent running `get_schema` then `run_sql`. Cost: **6,476 µUSD** (~$0.0065) on
the deployment's own configured model.

**Attribution, checked in the database rather than inferred:**

| Claim | Row |
| ----- | --- |
| The thread is keyed by the widget's own column | `channel=widget`, `embed_user_ref=emp_812`, `api_user_ref` NULL |
| `T-05` attributes the turn to the visitor | `agent_actions`: `embed \| emp_812 \| widget \| get_schema` and `… \| run_sql` |
| The usage rollup has a fifth kind | `usage_events` joined by channel shows `widget` beside `api`, `dashboard`, `discord`, `whatsapp` |

**Per-visitor isolation, with two real sessions.** Visitor B, holding a valid
session from the same key, against visitor A's thread id:

```
GET  /api/embed/threads/{A}/messages  → 404 {"error":"no such conversation"}
POST /api/embed/chat  {thread_id: A}  → 404 {"error":"no such conversation"}
GET  /api/embed/threads/current       → {"thread": null}
```

and visitor A still reads their own — 200. That is §1's whole argument, proven
rather than asserted: the read, the *write*, and the resolve are all scoped, and
a wrong id and somebody else's id are one answer.

**T-23 end to end.** `PUT /api/embed-config` with an Indonesian greeting and two
prompts, then `GET /api/embed/config` on a live session returned them —
**config reaching a running widget with no redeploy**, which is the ticket's
acceptance item. A non-hex accent (`red; background:url(//evil)`) was refused
400, since that string becomes a CSS value in somebody else's page.

## 5a. The browser gate, run 2026-08-10 — four defects

**The panel had never been opened.** Opening it found four defects in one
sitting, three of which no test in this repository could have reached and none
of which the `curl` matrix in §5 detected. The pattern the delivery log has
recorded since `T-13` held, and it held hardest on the surface that had the
most tests.

**1. `OPTIONS /api/embed/*` was a 404, so no browser could reach the API at
all.** Gin runs group middleware only for routes that **exist**; the preflight
was never registered, fell through to the 404 handler that no group wraps, and
came back with no `Access-Control-Allow-Origin`. Every browser request to the
embed surface was blocked before it was sent. The mint matrix passed anyway
**because `curl` does not preflight** — twelve green cases over a surface no
page could use. Fixed with a preflight route on its own group, and pinned by
`cmd/api/embed_cors_test.go` over all six paths.

**2. The iframe app was built as an ES module, which a sandboxed frame cannot
load.** The loader sandboxes `allow-scripts allow-forms` — deliberately without
`allow-same-origin` — which gives the frame an opaque origin. A
`<script type="module">` is then fetched under CORS with `Origin: null`, and no
static host answers that. The panel opened blank, with the error inside a frame
the host page cannot read. Now built as a classic script; the sandbox stays
tight.

**3. Asset URLs were root-absolute.** Vite's default `base: "/"` emitted
`/app.js`, which resolves only when the app is served from a domain root — not
from `/widget/v1/`, which is the CDN path `T-22` specifies. `base: "./"`.

**4. The session was minted from inside the iframe, where the origin allowlist
can never match.** This is the design error, and the other three were hiding
it. `T-19` checks the `Origin` of the minting request against the tenant's
allowlist — but the iframe's origin is the CDN's, or `null` under the sandbox,
and never the tenant's site. **The mint moved into the loader**, which runs in
the host page and therefore presents the one origin a tenant can allowlist. The
frame now receives a token instead of a credential: the client key and the HMAC
never enter it, and it cannot mint another when its own expires.

A fifth, smaller one came out of exercising the responsive branch: switching
from the desktop panel to the mobile sheet left the desktop `max-height` set,
so a "full-screen" sheet was 120px short after a rotation. Cleared.

**What was proven once those landed**, in Chrome against the live stack: the
launcher in the tenant's own accent; the panel opening with the visitor's
earlier conversation restored; a question typed into the composer; the answer
**streaming over the WebSocket** with a `run_sql` tool chip above it and
markdown rendered inside it — *"Samsung Galaxy S24 … 1,770 units sold"*; and
the composer re-enabling when the turn ended.

**What is still owed**, and it is now only what a `curl` cannot reach:

| Owed | Needs |
| ---- | ----- |
| Safari and Firefox | a human at those browsers — Chrome is done (§5a) |
| The mobile sheet at a genuinely narrow viewport | a device or devtools emulation; Chrome would not size its window below 662 CSS px here, so the branch was exercised by patching `innerWidth` — which tests the loader's logic and not the browser's layout |
| The `wss:` CSP row proved by removing it | a host page that sends CSP headers |
| The Gelael dashboard's own launcher, against a Gelael workspace | that workspace, and its key |
| npm + CDN publishing, the React wrapper, three more examples | a registry and a CDN decision |

**The gate found no defect, and that is worth being precise about rather than
pleased with.** Both defects this phase produced were found by tests — `T-19`'s
`sub` claim and `T-23`'s `omitempty` — and the live matrix re-ran twelve cases
a table-driven test already covered. The gate earned its place on the
migrations, the audit rows and the two-visitor isolation, none of which any test
could reach.
