# T-V4 · The player: an animated deck at a link — coverage

**Status: DELIVERED 2026-08-09.** Migration `050`. Gated live the same day
against the compose stack, with one defect found and fixed; two acceptance
items need a browser and are recorded as owed rather than counted as met.

`T-V3` made a report a file you send. This makes it a link you open: the same
plan, played in a browser, with the narrative beside the frame instead of
buried in the speaker notes `T-R4` had to put it in.

---

## 1. The player was the cheap half

`packages/motion` already draws every scene, and `@remotion/player` runs the
identical compositions client-side from the identical plan. So the component
work is a page, and **the ticket is really about the link**: who can create
one, how long it lives, how it is taken back, and what is recorded when
somebody opens it.

That is where all four of this document's arguments are.

## 2. The plan is stored beside the document, for three formats

`docgen` writes `{id}.plan.json` next to the object it just uploaded, after the
row and never before: a plan is an enhancement to a document that already
exists, and a report that failed to store because its plan did not would be a
worse product than one that cannot be shared.

**It is written for `pdf` and `pptx` as well as `mp4`.** The ticket says "store
the plan beside the video", and the narrower reading would have made the
feature reachable only from documents that had already paid for a four-minute
render. The player never reads the mp4 — it replays the compositions — so a
PDF is playable the moment it exists, and a shared link opens instantly whether
or not a video was ever made. A document that *was* rendered plays the
identical scenes, because it is the same projection the render service was
sent.

Three refusals, all silent and all logged:

- **A format with nothing to animate** — CSV and XLSX are data.
- **A document that is a record** — the same `spec.Analytical` predicate the
  mp4 door refuses an invoice with, so a shared invoice is impossible for
  exactly the reason a video of one is.
- **A plan that will not build** — over the scene cap, too long, a chart that
  cannot be drawn. All deterministic, all the caller's spec, none of them a
  reason to fail a PDF that rendered perfectly well.

## 3. The link, and why it is not a presigned URL

A presigned URL cannot be revoked before it expires, cannot be counted, and
cannot be scoped to a page. "Who has seen the Q3 numbers, and can I stop them"
is the question a tenant asks the moment they have shared one, and a presigned
URL answers none of it.

So `report_shares` is a table, and the token is a bearer credential we mint:

| Property | What it is | Why |
| -------- | ---------- | --- |
| Entropy | 32 random bytes, base64url | The same budget `T-13` gives an API key's secret |
| At rest | SHA-256 of the token | A dump of the table is not a set of working links. `T-13`'s argument unchanged: 256 uniformly random bits have no dictionary behind them, so a KDF slows nothing down and costs 64 MiB on every view of a public URL |
| Shape | No marker, no public prefix | An API key *wants* to be greppable so a scanner can revoke it. A share token travels in a URL a tenant pastes into an email, where recognisable is the opposite of what you want |
| Comparison | None, anywhere | The lookup is `WHERE token_hash = $1` on an indexed column. Nothing in Go ever compares a presented secret against a stored one, so there is no constant-time question to get wrong |
| Lifetime | 30 days default, 90 maximum | Expiry bounds the link nobody remembers; **revocation** is the button pressed at 11pm. A link with only one of those is either immortal or untakeable |

Over the ceiling is a **refusal, not a clamp**. An admin who typed 365 has a
reason, and a link that quietly dies 275 days before they expect is worse than
being told no.

## 4. One route that authenticates nobody, and where it lives

`GET /share/:token` is under neither `/api` nor `/v1`. Both of those mean
"authenticated" — one by a session, the other by a key — and every middleware,
policy table and route test on them assumes a tenant. A keyless route inside
either would be an exemption in somebody else's chain, which is the shape a
mistake hides in.

It has its own group with two links: an address-keyed rate limit and the
handler. It is named in `unpolicedPaths` so "this route authenticates nobody"
stays a decision somebody wrote down, and `TestUnpolicedPathsAreReal` diffs
that list against the router.

Four properties the handler holds:

- **Every failure answers identically.** Unknown, expired, revoked, and
  document-deleted are one `ErrShareGone` and one 404 with one body. A
  distinguishable "expired" tells somebody trying tokens that they guessed one
  correctly, which turns a wall into an oracle.
- **`Cache-Control: private, no-store`**, set before anything can return. A CDN
  or a corporate proxy holding a copy would serve a link that has been taken
  back, from a machine we cannot reach — so revocation would mean nothing.
- **`X-Robots-Tag: noindex, nofollow, noarchive`.** A link in an email is a
  link a crawler eventually follows.
- **The plan is served as bytes, not as a URL to one.** A presigned URL for the
  plan would outlive the revocation and be shareable onward without ever
  passing this handler again.

## 5. Every view is counted and audited

`view_count` and `last_viewed_at` on the row are the summary the dashboard
shows; the audit log holds the detail — one `agent_actions` row per view
carrying the share id, the document id, the visitor's address and their user
agent, both truncated because both are attacker-controlled text heading for
storage.

The actor is `ActorKindShare` with the share id in `ActorRef`. That is the
honest description of who did this: whoever opened it has no session, no
account and no tenant, and the only thing we know is which link was used —
which is exactly what makes "revoke the one being read from an address I do not
recognise" answerable.

**No `share` channel was invented.** A view is not a conversation on one, and
widening `Channel` would say a thread can arrive from a share, which is false
and would land in the generated TypeScript union as a case every `switch` has
to handle.

Both writes are best-effort. A visitor looking at a link somebody sent them is
not the right person to show a bookkeeping error to.

## 6. What the dashboard gained on the way

**There was no documents list at all.** `/v1/documents` has served integrators
since `T-A2`, but that surface takes an API key and refuses a session as flatly
as `/api` refuses a key — so the staff who generated a report could only reach
it through the markdown link in the chat thread that produced it. Scroll past
it and it was gone.

`GET /api/documents` is member-readable (a document is a report the staff asked
for; hiding the record of it from them buys nothing) and the share routes
beside it are admin, on the API keys' line rather than the documents' one: a
share is a bearer credential that reaches a tenant's figures from outside every
session they control.

A member sees the Share control **disabled with a sentence**, not hidden —
the decision recorded on 2026-08-04 for the watcher and approval UIs.

## 7. The gate, run 2026-08-09

Migration `050` applied on boot; a PDF rendered through `POST
/v1/reports/render` wrote `8aabe5ae….plan.json` beside `8aabe5ae….pdf` in
MinIO.

| Acceptance item | Result |
| --------------- | ------ |
| A logged-out visitor opens the link | **Pass** at the API and SPA layers: `GET /share/:token` with no session returns the plan — 7 scenes (`cover`, `section`, `statement`, `kpi`, `table`, `chart`, `closing`), 901 frames, branded `Gate TV3` — and `/s/:token` serves the app. **The three-browser render check is owed**, see §8 |
| Revoking kills it within one request | **Pass.** `204`, then the next load is `404` with `no-store` on it |
| An expired link is a 404 with the same body as a wrong one | **Pass.** `expires_at` moved into the past: byte-identical body to a token that never existed |
| A member cannot create or revoke; an admin can | **Pass.** `403` on all three share routes as a member, `200`/`201`/`204` as an admin, and `GET /api/documents` still `200` for the member |
| One company's admin cannot share another's document | **Pass.** `404` on create, `404` on revoke, empty list |
| Each view appends an audit row with ip, user agent and share id; the count matches | **Pass.** Two opens → `view_count: 2`, `last_viewed_at` set, two rows with `actor_kind: share`, the share id in `actor_ref`, and both fields in the args |
| The token appears exactly once and is not readable afterwards from any endpoint or log | **Failed, then fixed and re-run** — see below |
| A plan with an unknown version renders its known scenes and shows a notice | **Owed.** The branch is in the component; proving it needs a browser |
| The share route is absent from `/api` and `/v1`'s policy tables and exercised as deliberately keyless | **Pass.** In `unpolicedPaths`, and `cmd/api`'s three route-diff tests are green |

### The defect: the token was in the log, three times over

Every other route in this system carries its credential in a header, so
`RequestLogging` has always written `c.Request.URL.Path` safely. `GET
/share/:token` is the first route where **the path is the credential**, and the
gate found the token written to `api.log` in full on every page view:

```
{"level":"info","method":"GET","msg":"request","path":"/share/MkTHcTZjVz04Yd67bCR5ZURVO-ylYnnGL1qpELH_1Bo","status":200}
```

Read access to a log file was the ability to replay a link somebody else had
been sent. `loggablePath` now substitutes the route template for that one
route, so the line still says what was hit and how long it took. The redaction
is deliberately not general: for every other route the concrete path is what an
operator greps for, and a test pins both halves — the token absent, and
`/api/documents/doc-42` still concrete. Re-run: **0 occurrences**, and the log
reads `"path":"/share/:token"`.

## 8. What is owed

- **The three-browser check.** Chrome, Safari and Firefox each rendering the
  player. The browser automation this repo has was not connected during the
  gate, and the check is the same shape as `T-R4`'s four-application PPTX
  check — a human opening it. It belongs in
  [`live-gate-backlog.md`](live-gate-backlog.md) §1a.
- **The unknown-version notice.** Same reason: the component branches on
  `plan.version !== SUPPORTED_VERSION` and the proof is visual.

## 9. Out of scope, and still out

Comments or reactions on the shared page, password-gated shares, and embedding
the player in a customer's own site — that last one is the widget phase's
threat model, not this one's, and `backlog.md` holds it with a trigger.

## 10. One thing found next door

Signup writes the company row before the user row and does not roll back: a
failed signup leaves an orphan `companies` row whose slug then blocks anybody
retrying with the same company name (`duplicate key … companies_slug_key`). Hit
while creating a second tenant for the cross-company check. It is not this
ticket's and it is not filed as one — it is recorded here because it cost ten
minutes and will cost the next person the same.
