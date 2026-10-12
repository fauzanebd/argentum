# Onboarding — from sign-up to the first useful answer

The audit is [`RIZ-76`](https://linear.app/rizkal-uhuy/issue/RIZ-76/a-new-customer-gets-from-sign-up-to-their-first-useful-answer-without),
read from code at `f7f2267` on 2026-10-12. **The browser walk was not run**
(the local stack was down when the audit was written) — everything in §1 is
from the source, and the walk is owed ([`live-gate-backlog.md`](live-gate-backlog.md) §7p).

The audit proposed three slices. **Slice 1 is built**; slices 2 and 3 are
queued here so they start from this file rather than from a second audit.

| Slice | What | Status |
| --- | --- | --- |
| 1 | The stuck points after sign-up: the silent Save, the dead empty chat, no in-flight state, no starter questions; and signup's 500 leaking its error | **built 2026-10-12, unit-gated — §2** |
| 2 | Backlog `P-4`: an onboarding checklist, plus funnel events for sign-up, first connection and first turn | queued, ~1.5d — §4 |
| 3 | Public sign-up: a real CTA, email verification, password reset, sample data | **waits on the owner** — §5 |

---

## 1. The gaps, ranked by what they cost time-to-first-answer

| # | Gap | Where | Status |
| --- | --- | --- | --- |
| 1 | **No public way in.** The landing page's only CTAs are WhatsApp links; `/signup` is revealed after seven theme toggles on the login page ("Public registration is closed"). Every trial needs a person on our side | `apps/landing/src/lib/contact.ts`, `features/auth/login-page.tsx:22-35` | Slice 3 — owner decision |
| 2a | **Sign-up is unverified.** Company, default agent and admin are created and tokens issued at once; no email verification, no password reset route | `internal/app/auth_service.go:65-115` | Slice 3 |
| 2b | **Signup's 500 returned `err.Error()`** — a wrapped driver error, to an unauthenticated visitor | `handlers/auth.go` | **Fixed here (R8)** |
| 3a | **The onboarding Save failed silently.** `onSubmit` had no try/catch: a failed `POST /api/connections` or `PUT /api/settings` showed nothing and the button looked dead | `features/onboarding/onboarding-page.tsx` | **Fixed here (R1, R2)** |
| 3b | **No way out of onboarding.** Save stays disabled until Test passes, so a visitor without credentials to hand could only close the tab | same | **Fixed here (R7)** — *Skip for now* |
| 3c | **No in-flight state.** Nothing said the describer and business inference were still running after Save, so weak first answers were unexplained | `internal/app/company_service.go:180-187` | **Fixed here (R4)** |
| 3d | **A tenant with no data landed on a chat that could not answer.** Login goes to `/chat`; nothing re-offered onboarding | `features/auth/login-page.tsx:53` | **Fixed here (R3)** — a card, not a redirect |
| 3e | First-timer connection help (allow-listing our IPs, creating a read-only user); the "Production analytics" label default; USD as the default currency | `onboarding-page.tsx` | Not scheduled — PRD non-goal |
| 4 | **The first question had no scaffold.** The default agent is created blank, so `starterQuestionsFor` returned nothing | `features/chat/use-agents.ts` | **Fixed here (R5, R6)** |
| 5 | Embeddings are not part of setup; a large schema stays "sparse index" until an admin finds the button | `connections-tab.tsx` | Slice 2 (`P-4`'s checklist) |
| 6 | Channels are scattered: no "connect where your team works" step, and the WhatsApp tab never says which number to message | `settings/phones-tab.tsx`, `settings/tabs.ts` | Not scheduled — PRD non-goal |
| 7 | Team invites are a copied link | `feature-coverage.md`'s team row | Not scheduled. **The audit may be stale here:** `T-F6` built invite email ([`email.md`](email.md)), never sent live — re-check before scoping |
| 8 | No in-product orientation: no checklist or tour, ~16 Settings tabs at once | — | Slice 2 |
| 9 | The developer path's key comes from Settings after sign-up, so it inherits gap 1 | `docs/api/quickstart.md` | Slice 3 |

## 2. Slice 1 — what was built

No migration, no flag, no `/v1` change; `packages/api-types` regenerated for
one field.

| Piece | Where |
| --- | --- |
| Save catches both requests. A failed `POST /api/connections` shows *Could not save the connection: {reason}. Nothing was saved. Press Save to try again.* and re-enables Save; a failed `PUT /api/settings` still goes to `/chat`, with the toast *Connected, but the default currency was not saved — Set it in Settings → General.* A ref records that the connection exists, so no path posts it twice | `apps/dashboard/src/features/onboarding/onboarding-page.tsx` |
| *Saving…* while pending, Save and *Skip for now* both disabled; a second ref closes the double-click gap before the re-render | same |
| *Skip for now* → `/chat` | same |
| The connect-your-data card: an admin's has **Connect your data** → `/onboarding`, a member's says to ask an admin and has no button. No starter questions under it. The composer stays usable | `apps/dashboard/src/features/chat/onboarding-notice.tsx` |
| The reading / ready / timeout notice, in the budget banner's slot and shape. Polls `GET /api/connections` and invalidates `["agents"]` every 5s while a description is empty, for at most 5 minutes after mount | `onboarding-notice.tsx`, `use-onboarding-status.ts` |
| `AgentsResponse.data_starter_questions`: up to three questions from fixed templates, one inferred table each, default source first; the generic three while no profile exists; `[]` when the profiles cannot be read | `apps/backend/internal/app/starter_questions.go`, `handlers/agents.go`, `handlers/wire.go` |
| The untemplated company default offers them once a source is connected; templated agents keep their card's; every other blank agent still gets none | `apps/dashboard/src/features/chat/use-agents.ts`, `chat-page.tsx` |
| Signup's 500 is `{"error":"could not create the account, please try again"}`; the real error goes to the log, with nothing from the request | `handlers/auth.go` |

**Readiness is the describer's description.** An empty `description` on a
connection is the only "not yet" this product records — `description_source`
is `""` until the describer writes one, a manual description is never empty,
and a document-built source is created described. No new endpoint, no new
column.

**The questions are built by code, not by a model.** SourceProfile entities
are already filtered against the introspected schema (`keepKnownEntities`), so
every question names a real table; the builder only drops names a person
should not see in a button — empty, over 60 runes, or holding a control
character or a line break — and de-duplicates across sources.

## 3. Where the plan was wrong, or silent

- **It would have shown a member the tables of a source restricted from them.**
  The PRD builds the list from `ListByCompany` alone. The drafts are
  company-wide, and since `T-Z6` a member's source list is narrowed by grant;
  a pill naming `salaries` would have told a member restricted from the HR
  source what it holds. The handler now narrows a member's profiles with the
  same `Visible(…, connection, …)` read `visibleSources` uses, admins whole,
  and a failed check sends `[]` rather than the unfiltered list.
  `TestRosterCarriesTheDefaultAgentsStarterQuestions` has both arms.
- **The two background jobs are not one.** The notice flips on the
  describer's description; the table-named questions come from the business
  inference queued beside it. Polling stops when the descriptions are filled,
  so **if inference lands after the describer, the questions stay generic until
  the next visit.** Q8 will say which order they land in on a real model.
  **Prediction: the describer first by seconds, and the profile already
  there by the poll that sees the description — one light call each, the
  inference's slightly larger.**
- **A member restricted from every source sees *No data connected yet***, since
  the card reads the same grant-narrowed list. True for what they can use,
  not for the workspace. Accepted.
- **"Nothing was saved" has one exception.** `AddConnection` creates the row,
  then sets it default; if that second statement fails the API answers an
  error with a row in place. One statement on a transaction-less path, never
  seen; noted rather than fixed.
- **The mockups set the table name in the pill apart** (darker, medium
  weight). The wire is `[]string` (R5) and the pills are the existing
  component (R6), so the name renders as plain text in the sentence.
- **A missing repository logs at `Debug`, not `Warn`.** The PRD asks for a
  warning when the repository is nil *or* errors; nil is a stripped-down
  wiring (tests), which conventions log at `Debug`. An error is a `Warn`.

## 4. Slice 2 — the checklist and the funnel (queued)

Backlog `P-4`, 1.5d. Connect a source, enable embeddings, ask a first
question, add a phone, invite a teammate — and three funnel events (sign-up,
first connection, first turn) so the backlog's own trigger, *"conversion
looking bad"*, can be measured at all.

- **`provisionGrant` is not a first-turn hook.** The grant is provisioned on
  any budget check (`credits.go:145-160`), and inference and BYO-key tenants
  bypass or pre-empt it. The first-turn event needs its own signal.
- **Persisting progress needs a migration**, unless it lives in localStorage.
  Decide which before building.

## 5. Slice 3 — public sign-up (waits on the owner)

3–5d, and blocked on one question: **open public sign-up?** Every sign-up
spends LLM credits through the starting grant. The audit recommends yes, behind
email verification and a small grant, with a real *Start free* CTA in place of
the seven-toggle Easter egg — and, if not, a request-access form instead of
WhatsApp. Also in this slice: password reset and email verification (outbound
email exists since `T-F6`; whether it has ever delivered is
[`live-gate-backlog.md`](live-gate-backlog.md) §1t), and the no-database first path —
sample data, or upload a file to `/knowledge` and ask, not yet verified to
produce an answerable source without review.

## 6. Gates

**Unit.** `TestDataStarterQuestions` (13 cases: order across two sources,
duplicates, 60/61 runes, newline, NUL, tab, U+2028, blank, nil, a `%s` in a
name), `TestRosterCarriesTheDefaultAgentsStarterQuestions` (7: member, member
narrowed, admin whole, no profile, repo error, access error, nil repo — always
`200`, never null), `TestSignupFailureBodies` (500 generic, 409 and 400
byte-for-byte unchanged). Dashboard: `onboarding-page.test.tsx` (5),
`onboarding-notice.test.tsx` (9, fake timers: the 5s poll, the flip to Ready,
the 5-minute timeout, polling stopping in both), `use-agents.test.tsx` (5).

**Owed** — [`live-gate-backlog.md`](live-gate-backlog.md) §7p: the browser walk
from sign-up to a first answer on the local stack (Q3–Q7, Q9, Q10, Q12, Q13,
Q15), and the two that spend (Q8, Q11).
