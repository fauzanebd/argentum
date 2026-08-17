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

**Revised 2026-08-11 — §1b, and the count is fourteen across four sittings.**
The agent-quality track's stack-only half was run and produced **three more
defects**, all fixed and re-proven, two of them in the one place this repo has
spent a year trying to make honest: what `run_sql` tells the model when a query
matched nothing. Same ninety minutes, same bucket, same result. It also added a
constraint this file has not carried before — a gate can be blocked by a missing
*credential file* rather than by a cost, and that is worse, because a cost can be
decided and a missing file just looks like a passing test suite.

**Revised 2026-08-16 — §1d is run, and the bucket has now paid six times out of
six.** The three stack-only gates the 2026-08-14 build owed all passed, and the
sitting's one defect came from an endpoint none of them was aimed at: the
dashboard has been offering `read:data` — arbitrary SQL over a tenant's whole
warehouse — as a checkbox with no description beside it since `T-14`. §1d has
the table. **Everything still open in this file needs model spend, a Slack
workspace, a real handset, a Kubernetes cluster, a second browser, or an
operator.** Nothing is blocked on writing code, and nothing is blocked on the
stack.

**Revised 2026-08-17 — §1e, and the bucket has now paid seven times out of
seven.** The native dashboards build owed three gates and none of them needed
money: migration `056` against the real control database, one authored dashboard
from a live turn, and **a browser**. Three defects, all fixed in the sitting.
The third one is why this revision matters more than the count: every gate in
this file until now was driven through HTTP, `psql` or JSON-RPC, so the class of
defect where the data is right and the *rendering* misstates it had no way to be
caught. It took a minute to find one.

**Revised 2026-08-17 again — §1f is added and run the same hour, and the bucket
has paid eight times out of eight.** The four tickets of the loop-after-the-answer
build (`T-Q10`, `T-U13`, `T-D22`, `T-D23`) landed code-complete in one sitting,
and this file's whole record says that is the state in which something is hiding.
It was. **Two defects**, and the second is the largest thing this file has
recorded: a persisted answer stating a figure no tool ever returned, in front of
the true one — found on a turn deliberately run with the new feature *off*, so it
is older than the build that exposed it. §1f has the table;
[`next-steps-and-revision.md`](next-steps-and-revision.md) §6.5 has the
reproduction.

**The cheap bucket paid for itself twice over in one sitting.** Filing these
three in §1f rather than in §2 is why they ran at all — and once the stack was
up, two of §2's own items (the pass's cost and its p95) cost $0.12 to close
rather than the afternoon they had been sitting behind.

Nothing here is blocked on a decision about *how* to build something. Each item
needs one of four things: the stack up, money spent, a browser opened, or a
message sent to a real person's phone.

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

## 1b. ~~Needs the local stack, nothing else~~ — added and run 2026-08-11

The agent-quality track (`T-Q1`→`T-Q9`) landed nine tickets code-complete and
ungated on 2026-08-11. Its stack-only half was run the same day and produced
**three defects, all fixed and re-proven** — the fifth time this bucket has paid.
Full transcripts in [`agent-quality.md`](agent-quality.md).

| Owed by | The gate | Outcome |
| ------- | -------- | ------- |
| `T-Q2` | Migrations `054` and `055` up **and** down against a real Postgres | **Pass, 2026-08-11.** Up/down/up clean from version 53, with the down run against *populated* tables — two feedback rows and a `query_examples` row carrying a real 1536-dim vector. All constraints, the partial index and three cascading FKs verified; `055`'s down correctly leaves the `vector` extension alone, because `table_embeddings` has needed it since `013` ([`agent-quality.md`](agent-quality.md) §1) |
| `T-Q2` | A second vote replacing the first rather than duplicating | **Pass, 2026-08-11.** Same row id, `created_at` preserved, `updated_at` moved; a different actor is a second row; `rating = 0` refused by the CHECK; `Summarize`'s FILTER counts agree. ~~The **400 and 404 refusals are still owed**~~ — **run 2026-08-14: the 404s pass and the 400s found a defect.** A missing or out-of-range `rating` answered **500**, because `Rate` returned a bare error and `feedbackFail` maps anything unrecognised to 500 — the likeliest client bug reported as a server fault. Wrapped in `domain.ErrInvalidInput`, mapped to 400, tests tightened from `err != nil` to the sentinel, re-proven over HTTP ([`agent-quality.md`](agent-quality.md) §10) |
| `T-Q8` | `POST /api/cookbook/harvest` against a tenant with history, then the negative gate | **Failed, then fixed and re-proven.** The candidate query works — **121 real candidates** out of 741 `agent_actions` rows — but the verdict gate could never fire: `message_feedback.message_id` is always an *assistant* message (`Rate` refuses anything else), `agent_actions.message_id` is always the *user* message (717 of 717), so `negative[c.MessageID]` looked a question up in a table of answers. `skipped_negative` was structurally zero and every turn a human marked wrong was learned from anyway — the exact failure the roadmap names as making T-Q8 negative-value. Fixed with `AnswerMessageID` + `VerdictKeys()`; the gate now fires on the real turn that slipped through. **The write half — a harvest that stores an example — still needs embedding credentials** ([`agent-quality.md`](agent-quality.md) §3) |
| `T-Q9` | A zero-row query against a real warehouse, confirming `available_values` returns the column's actual contents | **Failed twice, then fixed and re-proven.** `parseEqualityFilters` found the WHERE clause with `strings.Index(lower, " where ")` — a literal space on both sides — so **multi-line SQL, which is all a model writes, never probed at all**. And `run_sql` tested `Count == 0` for both the note and the probe, missing the shape that produced the original fabrication: an aggregate over no rows returns ONE all-NULL row, so `SELECT SUM(…) WHERE <no match>` got no note, no probe, and a row in the payload. Both fixed; the probe now returns the demo warehouse's real `month_name` and `city` values, quoted, on both query shapes ([`agent-quality.md`](agent-quality.md) §4) |
| `T-Q7` | A thread past 20 messages, confirming hydration carries the recent turns rather than the opening | **Pass, 2026-08-11.** A real 58-message thread: the old `ListByThread(id, 20, 0)` window ends at 14:27 on 08-04, the new `ListRecentByThread` window starts at 14:37 and runs to the thread's last message on 08-07 — **zero overlapping messages**. Roadmap defect 1 seen for the first time on data rather than by reading. The rolling-summary *block* reaching a prompt is still owed and needs a turn ([`agent-quality.md`](agent-quality.md) §5) |

**And a bucket this file has not had before: blocked on a missing credential
file, not on cost.** There is no `.env` in the working tree — only a stale
`.env.example` — so `LLM_API_KEY`, `ARGENTUM_JWT_SECRET`, `ARGENTUM_DSN_KEY` and
`DB_PASSWORD` are all absent and neither `cmd/api` nor `cmd/worker` can boot.
Every remaining `T-Q` gate sits behind that one file, including all of §2's
model-spend items for this track. **Model spend for the 2026-08-11 sitting was
$0.00** — not declined, unavailable. Note also that the control Postgres volume
was initialised with a `metabase` role rather than the `argentum` the current
`docker-compose.yml` declares, so a recreated `.env` has to match the volume.

### The `.env` was rebuilt on 2026-08-14 — three of the four are closed

`apps/backend/.env` exists again and `cmd/api` boots against the real control
database: `control DB schema already up to date`, `Listening on :8080`,
`/health` `ok`, `/ready` `{"ready":true}`. Of the four missing variables, three
needed no owner at all:

- **`DB_PASSWORD`, `DB_USER`, `DB_NAME`** — read straight off the running
  `argentum_postgres` container, which has been up throughout. The paragraph
  above was right that the volume disagrees with `docker-compose.yml`, and it
  matters more than it reads: the volume holds **30 companies, 494 threads,
  1,070 messages and 741 `agent_actions`** — every gate transcript this repo
  has. Booting against compose's `argentum`/`argentum123` defaults creates an
  empty database and silently makes every prior gate unreproducible.
- **`ARGENTUM_JWT_SECRET`** — minted fresh. The only cost is that sessions
  issued under the old one are invalid, and there are none.
- **`ARGENTUM_DSN_KEY`** — minted fresh, and this one is **not** free. See
  below.
- **`LLM_API_KEY`** — ~~still owed, and still the only thing standing between
  this repo and §2. Nothing local can supply it.~~ **Supplied later the same
  day.** `T-Q1`'s 55-case run spent $0.441 against `moonshotai/kimi-k2.6`
  ([`eval-q1.md`](eval-q1.md)), which is this row closing itself. §2's remaining
  rows are now blocked on a decision to spend, not on a missing file.

**The DSN key is the part worth writing down.** `db_connections.dsn_encrypted`
holds 20 AES-GCM ciphertexts written under the key that went missing with the
old file. A new key does not damage them — the rows are untouched and the
original still decrypts them if it turns up — but under the new key every stored
tenant connection fails to decrypt, so the warehouses the 2026-08-11 gates ran
against are unreachable *by those rows*. Three ways out, cheapest first: the
original key, if it is in a password manager; re-registering the connections,
which is data entry rather than recovery because all 20 point at local demo
containers; or reading the plaintext copy Metabase keeps.

### Resolved 2026-08-14, and it found a third key nobody knew about

**The original key was in a second working copy of this repository** —
`~/Work/smartsoft/argentum-mono/apps/backend/.env`, dated 31 July — not in a
password manager. It opens the rows; the freshly minted key opens none of them.
Restored into the working `.env` with a comment saying so, and **verified over
all 20 rows**: 18 decrypt.

**The other two do not, and they are the finding.** `Gate TV3`'s
`Demo analytics` (2026-08-09) and `EmbedGate`'s unlabelled source (2026-08-10)
open under *neither* key — not the 31-July original, not the 2026-08-14
replacement. Both blobs are well-formed and 94 bytes; both fail the GCM tag. So
a **third `ARGENTUM_DSN_KEY` existed between 9 and 10 August and is gone too**,
which means this is not one lost file, it is a pattern nobody was measuring.

The data loss is nil — both rows belong to throwaway gate tenants and point at
local demo containers, and neither was mirrored to Metabase — but the mechanism
is the same one that would take a customer's connection with it.

**Two things follow, and neither is a gate:**

- **Nothing in the product notices.** A row that cannot be decrypted is
  discovered by an agent turn failing at query time, in front of whoever asked
  the question. A startup or admin-facing count — *"N of M stored connections
  do not decrypt under the current key"* — is small, and it is the difference
  between an operator knowing and a tenant finding out. It belongs with `T-H14`
  (key management), which is written and unbuilt.
- **The eval tenant's two rows were re-sealed rather than deleted.** They had
  been written under the 08-14 key hours earlier; `ensureSources` only creates
  a source when its *label* is missing, so a re-seed would have left them
  unreadable and looked like a passing dry run. Decrypted with the retiring
  key, re-encrypted under the restored one, and checked back out of the
  database. The full 56-case run that followed executed SQL on every case,
  which is the end-to-end proof the restore worked.

**That third option working is itself a finding.** `argentum_metabase` runs with
no `MB_ENCRYPTION_SECRET_KEY`, so `metabase_database.details` is unencrypted and
every DSN `UpsertWarehouse` ever mirrored is readable from the `metabase_app`
database. It is filed against `T-H5`/`T-D15` in
[`../plan/03-security-hardening-roadmap.md`](../plan/03-security-hardening-roadmap.md),
and it means the Metabase decommission has to destroy that datastore rather than
just stop pointing at it.

**One more thing that cost this session time and will cost the next one the
same.** `docker` on `PATH` is the nix build at 24.0.5, whose API version 1.43 is
below the daemon's minimum of 1.44, so `docker ps` answers *"client version 1.43
is too old"*. §1a above already records this exact error being misread as
"Docker is not running" and costing a day. The daemon was up and healthy both
times. The working client is Docker Desktop's own, at
`/Applications/Docker.app/Contents/Resources/bin/docker` (29.1.3) — put it ahead
of the nix profile on `PATH`.

## 1c. The hardening track was never in this file — added and half-run 2026-08-14

**This file said "every acceptance item this repo currently owes … in one
place" and carried no `T-H` row at all.** The security-hardening track landed
four tickets code-complete and unit-gated on 2026-08-11 with its own "What is
owed" list, and that list was never folded in here — so the one bucket this
project's record says gets run did not know about it. Folded in now.

| Owed by | The gate | Outcome |
| ------- | -------- | ------- |
| `T-H1` | A forged form POST against a running API | **Pass, 2026-08-14.** Three forged shapes 401, the GET handshake 403, nothing enqueued — and all three took the **Meta** branch including the two carrying `X-Twilio-Signature`, which is the bypass itself proven dead over HTTP rather than in a handler test ([`security-hardening.md`](security-hardening.md) §9) |
| `T-H2` | A Lark event with the signature header omitted, expecting 401 | **Owed, and now for a known reason.** The route is `/webhook/lark/events/:app_id`, mounts only under `LARK_ENABLED=true`, and refuses an unknown app id with `404` *before* any signature work. The 401 needs a seeded `company_lark_credentials` row with an encrypt key — a seeding step through the product's own configuration path, not a cost |
| `T-H3` | Boot with each setting empty in production mode; a raw-DSN registration with no TLS parameters | **Pass, 2026-08-14, and it found two CORS defects.** All four required variables refuse with exit 1 on the real `cmd/api` path; the WhatsApp and app-secret rows warn and boot as the 2026-08-11 decision says; all three plaintext-DSN shapes 400 over HTTP. The findings: the production `CORS_ORIGINS` warning **could not fire for an unset or empty value** — `getEnv` substitutes the development default, so production silently allowed only `http://localhost:5173` with nothing in the log — and `middleware/cors.go` claimed in its own comment that `Validate()` refuses to boot in that state, which it has not done since `6248963` ([`security-hardening.md`](security-hardening.md) §10) |
| `T-H15` | A resolver that changes its answer between the two lookups, through a real worker | **Pass on the property, 2026-08-14; not through `cmd/worker`.** A rebinding resolver (public at check time, loopback at dial time) drove the real `webhookout.Deliverer` over real sockets: the dial went to the checked address, a loopback listener counted zero connections, and the same rebound answer dialled without the pin reached it — the control that makes the result mean something. What is still a read rather than a measurement is the worker's own wiring, one `NewDeliverer` call at `cmd/worker/main.go:107`; the flip needs DNS this machine cannot supply, because the public rebinder's answers are filtered upstream ([`security-hardening.md`](security-hardening.md) §11) |

**And one item that is not a stack gate at all:** `T-H1`'s marginal finding was
always deployment-shaped — whether the reverse proxy in front of `cmd/api`
preserves the `Host` header the Twilio signature is computed over. Today's run
had no proxy in front of it, so that question is exactly as open as it was. It
belongs beside `T-14`'s Helm hostname in §4, because both need an operator.

## 1d. ~~Owed by the 2026-08-14 build — stack only~~ — run 2026-08-16

Seven items landed that afternoon: `T-H7`, `T-H10`, `T-H13`, the metric zero
path, the DSN-key count, the `T-Q6` re-spec and the deletion of
`internal/cache`. All are unit-gated; three owed a live half, and none of the
three needed money. **All three were run on 2026-08-16 — three passes and one
defect, found in an endpoint none of them was aimed at.** That is the **sixth
sitting** of the bucket that only ever needs the stack, and the sixth to find
something no unit test had. Full transcript: [`delivery-log.md`](delivery-log.md)
Phase 2r; the mechanics are in
[`security-hardening.md`](security-hardening.md) §15.

| Owed by | The gate | Outcome |
| ------- | -------- | ------- |
| `T-H7` | One turn with a literal in the query, read back out of the API log: the Info line carries `'?'`, and the raw statement appears only under `LOG_LEVEL=debug` | **Pass, 2026-08-16.** At `LOG_LEVEL=info`: `t1.email = '?'`, `t1.customer_id = ?`, both comments dropped, aliases and table names intact, **no `sql_raw` key**, and the email, the NIK and the phone number all absent as substrings from the whole Info slice rather than from that line alone. At `debug`: the same Info line plus the statement byte-for-byte. The probe's own line reads `probed_columns: dim_customers.email` while the tool hands twenty real addresses to the caller ([`security-hardening.md`](security-hardening.md) §15) |
| `T-H10` | A zero-row query filtered on an email column under each of `strict`, `contact_ok` and `off` — refused, allowed, allowed — and an ordinary label column still probing under `strict` | **Pass on all four, 2026-08-16, and the refusal is proven at the network.** With `log_statement = all` on the demo warehouse, the `strict` run shows only the user's own query in the database's log — no probe crossed the socket; `contact_ok` and `off` both show `SELECT DISTINCT email …` and return twenty real addresses; and a `city` filter under `strict` still returns the seven real cities, so T-Q9's case survived the fix. The tenant's mode was changed through `PUT /api/settings`, which is the product's own path ([`security-hardening.md`](security-hardening.md) §15) |
| The DSN-key count | Boot `cmd/api` against the control database and read the line: `18 of 20` is the number this machine should produce, because two rows have been undecryptable since 10 August | **Pass, 2026-08-16 — `total 20, undecryptable 2, companies 2`,** and the two ids resolve to `Demo analytics` (Gate TV3, 08-09) and `EmbedGate`'s unlabelled source (08-10): the same pair §1b found by hand. `GET /api/connections/key-health` answered the per-tenant question (`total 1, undecryptable 0` for the gate tenant), and `cmd/mcp` swept on its own boot too. **The expected number moves to 2 of 21 from now on** — the gate tenant registered a working connection and was left in the database, as every prior gate tenant has been |
| `T-H13` | The job runs and blocks on GitHub | **Run 2026-08-16 — it blocked, and it found a real one.** On the push carrying the eval re-score, `Security scanning` failed: **`GO-2026-6222`**, excessive memory allocation decoding VP8L in `golang.org/x/image@v0.43.0`, with a **reachable trace** — `internal/branding/service.go:197` → `NormalizeLogo` → `image.Decode` → `vp8l.Decode`. A tenant's uploaded logo is the input. Fixed by bumping to `v0.45.0`; govulncheck reads `No vulnerabilities found` and CI is green. **The interesting part is that the tree did not change — the advisory database did.** `c74a890` ran all three scanners by hand on 08-14 and recorded them green, which was true that day. That gap is the entire argument for the job existing. What is still owed is narrow: `dependency-review` is gated on `if: github.event_name == 'pull_request'`, so that one step has yet to run at all |

**The defect this sitting found is not in either ticket.**
`GET /api/api-keys/scopes` served `read:data` and `write:visualizations` with an
empty `description` — the two scopes `T-14` added to the vocabulary and not to
the map that describes them — so the dashboard offered the **widest read
capability a key can carry** (arbitrary SQL over every table a connection can
see) as a checkbox with no sentence beside it. The endpoint was being called to
mint the key the other gates needed. Fixed, with a test that fails on the old
map and names both scopes ([`api-keys.md`](api-keys.md) §6).

**And the driver is worth recording, because the next gate on a tool will want
it.** `run_sql` is not reachable from `cmd/api` — a turn runs in `cmd/worker`
behind a model — so these were driven through **`cmd/mcp`**, which adapts the
same registry instance rather than reimplementing it: a `read:data` key and
three JSON-RPC posts run the exact code path a turn runs, with the arguments
chosen by the gate. What it cannot show is a *model-generated* statement
carrying the literals, which is the half no stack can supply anyway.

## 1e. ~~Owed by the native dashboards build~~ — run 2026-08-17, and a browser finally opened

The seventh sitting, and the seventh to find something. `T-D3`→`T-D7`, `T-D10`
and `T-D11` landed in one day with three gates owed: the migration against a real
Postgres, a dashboard authored by a real turn, and — for the first time in this
file — **a screen looked at**.

| Owed by | The gate | Outcome |
| ------- | -------- | ------- |
| `T-D5` | Migration `056` up, down against a populated table, and up again, against the real control database | **Pass, 2026-08-17.** 12 columns, 3 indexes, all four FK rules as designed, and `ON DELETE RESTRICT` proven rather than read — Postgres refused to delete a connection a stored dashboard reads. DSN key-health read `total 21, undecryptable 2`, the expected number §1d predicted |
| `T-D11` | One real turn on a live model authoring a dashboard end to end | **Two defects, both fixed and re-proven in the sitting.** `create_dashboard` refused a call with no `source_id` on a one-source company — the only data tool not going through `ResolveSource`; and a dashboard whose default window matched nothing was saved clean and described in confident prose beside `$12.73B` from the turn's own `run_sql`. `dryRun` now warns on a zero-row panel with the window in the message ([`native-dashboards.md`](native-dashboards.md) §1) |
| `T-D11` frontend | Open a panel in a browser | **One defect, and it is the cheapest one in this file.** Monthly revenue in the billions clipped three different axis ticks to the same `100,000` in a 48px gutter — an axis contradicting its own bars. Compact axis formatter and 56px; the tooltip keeps full precision ([`native-dashboards.md`](native-dashboards.md) §1a) |

**The lesson this sitting adds to the six before it: a screenshot is a gate, and
this file had no bucket for one.** Every prior entry was driven through HTTP,
`psql` or a JSON-RPC post, so a whole class of defect — the kind where the data
is right and the rendering lies about it — had no way to be found. It cost about
a minute.

**Still owed on this build, and it needs no money either:** the `/dashboards`
list page, the chat embed inside a real transcript, and the dark ramp on a real
dark card. `T-D8` and `T-D9` are unbuilt rather than ungated.

## 1f. ~~Owed by the loop-after-the-answer build~~ — run 2026-08-17, and the bucket has paid eight times out of eight

Added and run 2026-08-17. `T-Q10`, `T-U13`, `T-D22` and `T-D23` landed
code-complete in one sitting, and by this file's own record that is the state in
which seven builds out of seven were hiding something. **It was eight out of
eight**: all three stack-only gates passed, and running them cost about $0.12
because two of §2's items came along for free once the stack was up.

**Two defects, and the second is the one that matters.** The first is this
build's: `T-Q10`'s 5s pass timeout is three times too small for this
deployment's light model, so the feature was switched on, billed nothing and did
nothing — the C-2 shape. The second is **older than this build and worse**: a
persisted answer containing a figure no tool ever returned, in front of the true
one, on a turn that ran with the new feature switched *off*. Full write-up:
[`next-steps-and-revision.md`](next-steps-and-revision.md) §6.

**And it opened with this file's own lesson, again.** `docker ps` answered
*"client version 1.43 is too old"*, which reads as "Docker is not running". The
daemon was up; the client first on `PATH` was too old for it. Docker Desktop's
own client at `/Applications/Docker.app/Contents/Resources/bin/docker` works.
Second time. Written where somebody will be standing.

| Owed by | The gate | Outcome |
| ------- | -------- | ------- |
| `T-D22` | Migration `057` up, down and up against the real control database, across all three agent shapes | **Pass, on 45 real agent rows rather than seeded ones.** The 39 unrestricted rows are byte-identical before and after — `md5` of `(id, allowed_tools)` is `f96223b0…` through a full `56 → 58 → 56 → 58` round trip — so no every-tool agent was narrowed. Exactly the 4 holding `create_dashboard` gained the tool; re-running reports `UPDATE 0` with no duplicate array entries |
| `T-U13` | Migration `058` up, down against a populated table, and up; then the pick endpoint's 404 / 400 / 200 and the summary's role split | **Pass on every item.** Two picks on one message both persisted (the no-unique-key design, proven rather than read). A client posting an invented `label` and `recommended` got a row carrying the message's own values — the property the table rests on, tested adversarially. Member picks 200, member summary 403, admin summary 200 |
| `T-Q10` | Boot with `NEXT_STEPS_ENABLED=false`, run one turn, and diff the persisted message and the `final` event against a turn on the default | **Pass.** `final` metadata goes from `[latency_ms, next_steps]` to `[latency_ms]`, the persisted `metadata` from three steps to `NULL`, no `next_steps` usage event is written and the worker logs nothing about suggestions. It also proved the pass works when given room: 3 steps in 12,962 ms, on the `final` event and on the row |

## 2. Needs the stack **and** real LLM spend

| Owed by | The gate | Cost |
| ------- | -------- | ---- |
| ~~`T-07b`~~ | ~~`make eval` on both sides of switching the output guardrails on~~ — **run 2026-08-13, and it could not measure its own question** | `off` 35/39, `strict` 35/40. The narrow risk this row named turns out to be an **empty** one: the golden set holds no email, phone or NIK, so **no case in it can score differently under a redaction rule**. Activation is free on ordinary BI traffic; the contact-list answer `contact_ok` exists for is still unmeasured. Adding PII-shaped cases is the follow-up ([`eval-sprint1.md`](eval-sprint1.md) §2). ⚠ Run on the **40-case set at `4caf1fa`**, before `T-Q1`→`T-Q9` |
| ~~`T-A2b`~~ | ~~Ten live agentic report calls~~ — **run 2026-08-13** | **The guardrail question is closed: 0 refusals in 10 calls**, against 4 in 5 before the fix, with no `guardrail` row in `agent_actions` at all. The acceptance line is not met — 5 documents in 10 — and the misses found two defects **both still present on `origin/main` at the time**: the terminal status write ran on the turn's dead context (fixed here), and a report can be handed a later report's document in a shared thread (open). [`api-reports.md`](api-reports.md) §7a |
| `T-R4` | Three unautomatable applications of the deck renderer | Opening the generated `.pptx` in PowerPoint, Keynote and Google Slides. No test can do this |
| `T-18` | The final eval run → [`eval-sprint1.md`](eval-sprint1.md), compared against baseline | **Run 2026-08-13 in the prescribed order — 87.5% (35/40) against a 100% baseline — but on a tree 45 commits stale**, so the number describes the agent *before* the quality track and `T-Q1`'s fifteen new cases. It is not the sprint's closing figure and the row stays open; what it does carry forward is two defects re-verified against `origin/main` (§the file). **The real closing run is `T-Q1`'s, on the 55-case set** — one run, not two |
| The prompt-contradiction fix (2026-08-09) | `report-directive-is-not-an-injection` passing on both models | The guardrail slice is ~$0.42 on haiku (8 cases, measured 2026-08-08); the full set is ~$2.10. The fix removes the chart guidelines from a turn whose deliverable is a file, which is a mechanism with an argument behind it and **no number** — the deterministic half is tested, and whether the case now passes is exactly what a golden set exists to answer ([`delivery-log.md`](delivery-log.md) Phase 2g) |
| ~~`T-Q1`~~ | ~~`make eval` on the extended **55-case** set~~ | **Run 2026-08-14 — 83.6%, 46/55, $0.441, inside the predicted band.** The set discriminates, which was the point; four of the five categories `T-Q1` added are where the failures concentrate. It cost $0.44 rather than the $2.10 the estimate carried. Full triage in [`eval-q1.md`](eval-q1.md), and only three of the nine failures are the agent getting an answer wrong — three more are `ask_clarification` being answered in prose instead of called, one is a case that contradicts the metric registry, one was this run's Metabase credentials, and one is an empty reply whose cause is open. **On a different model from every prior number** (kimi-k2.6, owner's choice the same day), so it is a new baseline and not a delta |
| ~~`T-Q1`~~ | ~~A re-score of the five 2026-08-14 fixes~~ | **Run 2026-08-14 — 87.5%, 49/56, $0.631**, same model on the 56-case set. `zero_row_trap`, `chart_dashboard` and `multi_source` went to 100%; `guardrail` did not move, and the reason is the finding — the off-topic fix was a prompt edit to a classifier running on `gpt-5-nano`, and it still admits the recipe. Three empty replies with three distinct causes, and a real Indonesian number-parsing defect in the grounding check, fixed with a table test ([`eval-q1.md`](eval-q1.md) *The re-run*) |
| ~~`T-Q5`~~ | ~~`make eval-matrix MODELS=…` across 2–3 models~~ | **Run 2026-08-14 — kimi-k2.6 87.5% / $0.631 against deepseek-v3.2 83.9% / $0.173**, as two single-model runs against one commit rather than one matrix call: the same evidence at half the spend. It answered more than "which model?" — `ask_clarification` is called by deepseek and never by kimi, so the tool is a model property and the *asking policy* is ours and wrong on both; and the recipe case passes on deepseek only because that model declines on its own manners, which means **deepseek has been masking a classifier that admits off-topic questions** in every guardrail number this project has published ([`eval-q1.md`](eval-q1.md) *T-Q5*) |
| ~~Rule 1 re-score~~ | ~~The full set on both models after the classifier, asking-policy and fixture changes~~ | **Run 2026-08-14 — kimi 98.2% (55/56) / $0.629, deepseek 89.3% (50/56) / $0.141.** `guardrail` is 8/8 on **both** for the first time, which is the deterministic cooking block and the refusal-language fix carrying a category that used to depend on deepseek's manners. **Everything left is `zero_row_trap`** (kimi 2/3, deepseek 0/3), and the case both models fail is a product finding: a `COALESCE(sum(…),0)` metric template answers an out-of-coverage window with a genuine 0, so `query_metric` gives the model the soft note instead of the "this is NOT a zero" one — the `T-Q9` mechanism, alive on the path `T-Q9` did not close. At 98.2% the set is above the 95% line this project treats as a signal to harden rather than bank ([`eval-q1.md`](eval-q1.md) *The Rule 1 re-score*) |
| ~~`T-Q3`~~ | ~~A before/after on the chart-restraint prompt change~~ | **Run 2026-08-16 — and the answer is no.** kimi scored **54/56 with the guideline and 54/56 without it**, from two different pairs of failures; deepseek's before-arm built no unrequested chart at all. The one the off-arm did produce — a card and a dashboard for `id-kanal-terbesar` — sits inside a ±2 noise band the same sitting measured, so it is an event and not a result. T-Q3 stays a prompt change with no number, which is now known rather than assumed. It did expose a real instrument gap: all three `must_not_call` assertions were in English and the violation landed in Indonesian, so the five `indonesian` cases now carry the assertion too ([`eval-q1.md`](eval-q1.md) §5) |
| ~~The metric zero path (2026-08-14)~~ | ~~The full set on both models, reading `zero-row-future-quarter` first~~ | **Run 2026-08-16 — it works.** kimi's `zero_row_trap` goes **2/3 → 3/3** and the reply names the data's true coverage (*"1 July 2024 to 31 December 2024"*) instead of answering Rp 0. On deepseek the case still fails, but on a different behaviour — it names the coverage correctly and then volunteers the covered period's total. No zero was over-hedged: `simple_aggregate` 7/7 on both. The aggregate moved down on both models for reasons that are **not this change** — see the row below ([`eval-q1.md`](eval-q1.md) §1) |
| The `query_metric` retry loop | A repeat-guard in the agent loop | **Found 2026-08-16, half-fixed, and the rest is written down.** A `query_metric` call carrying one window bound was refused with a Go error, and deepseek answers an error by sending the identical call — six times, then the iteration budget ends the turn. **Five of its ten failures were that one loop.** The refusal is now a result the model can act on (tested, proven failing first), and re-running proves that **does not rescue the turn** (0/3), nor does a prompt sentence (0/3, twice, reverted under rule 1). What is left needs a guard where a tool returning the same refusal to byte-identical arguments twice ends the loop — ten other tool paths share the shape ([`eval-q1.md`](eval-q1.md) §2) |
| `T-Q6` | ~~A two-turn conversation at `PRIOR_WORK_TURNS=3` and again at `=0`~~ → **re-specified 2026-08-14**: `follow-up-breakdown-no-reschema` at `=3` and at `=0` on one model (two cases of spend), or a follow-up on a thread where turn 1 has fallen out of `CONTEXT_MAX_TURNS` ([`../plan/02-agent-quality-roadmap.md`](../plan/02-agent-quality-roadmap.md) §T-Q6) | **Run 2026-08-14 — the mechanism passes and the acceptance line does not measure it.** `role='tool'` rows exist now (the column had none), and injection fires at `=3` and never at `=0` while the rows are written at both. But six turns across the two arms produced **identical tool sequences**: inside `CONTEXT_MAX_TURNS` the assistant's own prior message already quotes the SQL, so the digest tells the model what it can read. T-Q6 is load-bearing only once the tool turn falls out of the window, which a two-turn conversation cannot reach ([`agent-quality.md`](agent-quality.md) §11) |
| ~~`T-Q7`~~ | ~~The rolling-summary block appearing in a turn on a thread past 20 messages~~ | **Pass, 2026-08-14** on the 58-message thread: `summary_chars=202 message_count=60 history_window=20`, and the answer reconstructs the opening alert the window no longer covers. The line proving it **did not exist** — four silent exits, no success log, and nothing else records the composed user message, so injected and skipped logged identically. One of those exits (`messageCount` failing) disables T-Q7 on every thread and looks like a short conversation ([`agent-quality.md`](agent-quality.md) §12) |
| `T-Q8` | A harvest that writes an example, and a turn that retrieves one | Embedding calls only, one per example. Every gate above `client.Embed` is proven ([`agent-quality.md`](agent-quality.md) §3) |
| `T-D22` | Build the 08-17 two-panel dashboard, then in the same thread ask for the window change, a panel swapped to a line chart, and a third panel added — three turns, `update_dashboard` each time, the same id throughout. Then a fresh thread asking to change "the revenue dashboard" with no id | **Still owed.** Four turns. The one that matters most is the last: the no-id path returns a **result** listing recent dashboards rather than a Go error, and this repo has already measured what an error does to deepseek — the identical call, seven times, until the budget ends the turn |
| ~~`T-Q10`~~ | ~~Turns producing `next_steps`, and the two numbers that decide the feature's future~~ | **Run 2026-08-17 — and the numbers settle the design against the ticket. 607 µUSD per pass (≈3% of the turn) and 12,962 ms.** Cheap and slow, where `T-Q10` assumed the opposite; its own rule says revisit above 1s. The 5s timeout it specified could never be met here (12.5–16.6s measured), so the feature was on and inert — now `NEXT_STEPS_TIMEOUT_SECS`, and exhausting it logs at `Warn` ([`next-steps-and-revision.md`](next-steps-and-revision.md) §6.4) |
| `T-Q10` | One turn on an agent scoped to `get_schema` + `run_sql`, showing the chart suggestion narrowed away | **The one arm that did not run.** It is the only live cover for `needsMissingTool`, which is otherwise proven by unit test alone — and it is the inverse of the failure `feature-coverage.md` records, an agent recommending work it cannot do |
| `T-Q10` | The `next_steps` eval category — parse, cap, allowlist, no-figures, and the clarification negative — with the pass on and off | One set per arm. Rule 1 applies: the pass changes what reaches the user on every turn. The set carries a ±2-case noise band, so a one-case delta is not a result |
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

## 3a. Needs a browser

Added 2026-08-17, and it is the bucket §1e argued into existence — every gate in
this file before that sitting was driven through HTTP, `psql` or JSON-RPC, so
the class of defect where the data is right and the rendering lies about it had
no way to be found. It took a minute to find one.

| Owed by | The gate | Why a test cannot do it |
| ------- | -------- | ----------------------- |
| `T-D11` | The `/dashboards` list page, the chat embed inside a real transcript, and the dark chart ramp on a real dark card | Carried over from §1e. The dark ramp in particular: eight hexes were re-picked against a dark background and nothing but an eye confirms that |
| `T-U13` | The chip row in both modes; the recommended chip visually distinct and its reason reachable without a mouse; a click filling the composer and starting **no** turn; the `suggestion_picks` row afterwards; and an older message in the same thread with no chips under it | The last one is the acceptance item most likely to be quietly wrong, because it is an absence. The "starts no turn" half is the rule this feature shares with the starter questions, and the reason is money |
| `T-D23` | The header action, the prefilled composer, and the panel grid before and after one edit turn | The invalidation fires on a `tool_call` event naming `update_dashboard`; whether the grid actually redraws without a reload is a thing somebody watches happen |

## 3b. Found by a gate, owned by nobody — a fabricated figure in a persisted answer

Added 2026-08-17 by §1f's sitting. **It is not a gate that is owed; it is a
defect that needs a ticket**, and it is filed here because this is the file
somebody reads before deciding what to run next.

Asked *"How many transactions were there in November 2024?"*, the answer of
record reads:

> There were **1,667 transactions** in November 2024.  There were **1,667
> transactions** in November 2024. There were **300 transactions** in November
> 2024.

`300` is correct and is what the agent's own `run_sql` returned. `1,667` is in
no table — `fact_sales` holds 1,348 rows altogether. The December turn has the
same shape in front of its true 310.

**The mechanism, from the event stream rather than from a guess:** the turn
carried `iteration: 2`, and the concatenation of its 44 `delta` events *is* the
stored content. The model wrote a sentence with an invented figure before
calling the tool, wrote it again, then wrote the true one once the result came
back — and `runStream` accumulates every iteration's prose into one reply.

**Why the guardrail passed it.** `CheckFabrication` grounds a reply on
`TurnEvidence.DataRows > 0`. A data tool did return a row, so the check is
satisfied while a figure no tool produced sits in the same paragraph. It asks
*"is there evidence?"*; this needs *"is every figure evidenced?"*. That is the
wrong-but-nonempty class `../plan/02-agent-quality-roadmap.md` assigns to
`T-Q9`, one door further out than that ticket looked.

**Not this build's**, and the control is in the transcript: the turn ran with
`NEXT_STEPS_ENABLED=false`. Neither fix is an edit — dropping pre-tool prose
also drops legitimate narration, and making the fabrication check evidence every
figure is a change to a guardrail that has blocked correct answers before. Both
are somebody's decision. Reproduction:
[`next-steps-and-revision.md`](next-steps-and-revision.md) §6.5.

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
