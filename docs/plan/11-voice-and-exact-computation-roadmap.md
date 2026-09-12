# An answer that was computed, and one you can hear — `T-W1` → `T-W9`

Written 2026-09-11 against `main` @ `7b1a55b`, from
[`../research/08-voice-and-exact-computation.md`](../research/08-voice-and-exact-computation.md),
which is the evidence for every claim this document does not re-derive. Nine
tickets, **~14.5 days — ~12.0 backend, ~2.5 frontend** — across three tracks.

**Revised 2026-09-11, the same day: `T-W6` is superseded and this document is
now eight tickets and ~13.0 days.** The owner widened the access half of the
request beyond voice, and it became
[`12-access-grants-roadmap.md`](12-access-grants-roadmap.md). Track C now
depends on `T-Z1`.

**Why `W`.** `grep -rhoE "T-[A-Z]" docs/` finds `A B C D F G H K M N P Q R S U
V X Y`, and `grep -rhoE "\b[A-Z]-[0-9]+\b"` finds bare findings under `B C E O
P Q S T`. The two obvious mnemonics are both taken: `T-V` is the video track,
and `T-C1` would sit one character from the finding `C-1`. `W` collides with
neither list.

> **Status, 2026-09-12: `T-W1`, `T-W2` and `T-W3` are built, `make check`
> green, unit-gated — Track A's 2.5-day floor.** Record:
> [`../coverage/exact-computation.md`](../coverage/exact-computation.md).
>
> **Nothing on this track should be built next, and that is `T-W3`'s finding,
> not a pause.** `T-W4` was to be decided by `T-W3`'s residue count, and that
> count exists for no past turn: nothing stored whether a reply's figures
> matched its tools, so the reply half of the measurement starts the day `T-W3`
> deploys (§6a of the record). The decision needs weeks of production turns
> with `compute` and the grounding record both live. Starting `T-W4` before then
> is building past an unanswered measurement.
>
> **What is owed is deployment and reads, not code:** one paired `make eval`
> for `T-W1`'s catalog line and `T-W2`'s guideline together, `081`/`082`'s
> round-trips, the live turns, and `T-W3`'s SQL against a real Postgres
> ([`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §7b).
> The board's other open items are `T-F5` (held on a measurement), `T-H4` step 2,
> `T-H14`'s envelope half, `T-K8`→`T-K10`, `T-G8`→`T-G9`, Track C (`T-W7`→`T-W9`,
> which depends on `T-Z1`) and roadmap 12 (`T-Z1`→`T-Z9`, nothing built).

**The two requests are not one feature, and the order between them is not a
preference.** Track A closes a defect this repository has already recorded in
its own delivery log — `T-Q14`'s finding that *a figure 0.078% wrong passed
every instrument this product has*, which is exactly the size of error a model
makes doing division. Track C is a capability nobody has asked for by name.
Both are worth building; if only one is, it is Track A. The research §6's last
paragraph makes the argument at length and it is not repeated here.

---

## 1. Decisions (locked — do not re-litigate inside the tickets)

### About being exact

**1. SQL first, an expression second, a program third.** A ratio a `SELECT` can
compute is computed by `run_sql`, because that path is already grounded,
already bound, already guarded by `sqlguard` and already fast. A program is the
tool for the step *after* the query — across two sources, over a loop, or
between two result sets that arrived separately (research §3d). A design that
routes arithmetic away from SQL would be slower, less safe and would bypass the
one validator this product spent `T-H4` on.

**2. Money is decimal, never float.** `0.1 + 0.2 != 0.3` in every IEEE-754
language, and the last decimal place is precisely what a reconciliation is
looking for. This is a hard constraint on the runtime, not a style note.

**3. Therefore no pandas and no numpy.** They are float engines. Handing the
model a dataframe library ships the bug the track exists to fix, and the absence
is a feature to be stated in the tool description rather than a limitation to be
apologised for.

**4. A computed figure is evidence.** `compute` and `run_program` join
`agentbudget.dataTools` (`budget.go:162`). A number a program returned is a
number a tool produced, which is the whole point — today a derived figure is
grounded by nothing, and `CheckFabrication` does not see it as unsupported
because it is composed of supported parts.

**5. The program is stored, and it is the working.** An accounting figure
nobody can reproduce is not usable by an accountant. `agent_actions` already
carries tool parameters, so the chain becomes *this query → these rows → this
program → this number*, and the middle step can be re-run by hand.

**6. Determinism is a property, not an aspiration.** No clock, no randomness, no
network, no ambient input. The same program over the same values returns the
same digits, which makes a re-run a check rather than a second opinion.

**7. The program is assumed adversarial every time.** A turn that ran `run_sql`
has read `taint.KindData` before it wrote a line of code (research §3g), so
there is no such thing as a program composed from trusted context. The fence is
**isolation, not approval**: a pure function over values has nowhere to send
anything and nothing to steal, and putting `T-H9`'s approval gate in front of
arithmetic would be an off switch rather than a control — the argument
`taint.go:13` already makes about applying document rules to warehouse rows.

**8. Isolation must not be something a deployment can forget.** gVisor and
Firecracker are cluster capabilities; a security property that depends on a
`RuntimeClass` somebody remembers to set ships open the first time somebody
does not. WASI grants no sockets and no filesystem unless the host asks for
them, so *"the program cannot phone home"* is a property of the runtime
(research §3f). [`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md)
§4 has an item that has sat for a month waiting on an operator's decision; this
design is arranged so it never becomes one.

**9. A program that fails returns its error to the model, never a number.** No
partial result, no "best effort" value, no silent fallback to the model doing it
in prose. `T-P5`'s rule: *do not "fix" a mismatch by trusting the stated total.*

### About the gate

**10. Voice is a capability, not a rank.** `domain.Role` stays two values. A
tenant will want three of forty people to have voice and those three will not be
the three admins — an admin is whoever configured the database. A rank cannot
express that and a grant can, and the grant leaves `Role.Valid`, the JWT claim,
invitations and ~200 existing policy rows untouched (research §1).

**11. The capability table keeps `TestEveryAuthedRouteIsClassified`'s
property.** The role policy stays the complete, both-directions-diffable list of
every authenticated route. A capability is a *second* map over a subset, and the
test gains one arm: every capability entry must name a route that exists. An
unlisted route is still denied; a capability is only ever an additional
requirement, never a way to reach something the role table did not already
allow.

### About voice

**12. Voice wraps the existing turn and is never a second agent path.** The
transcript enters `ChatRunner` as an ordinary user message, so
`CheckFabrication`, `CheckStaleness`, `CheckEmptyReply`, `CheckToolCallLeak`,
`agentbudget`, `taint`, credit enforcement and the `agent_actions` row all apply
because it is literally the same turn. A realtime duplex session needs a second
implementation of every one of them (research §2c), and this repository's entire
safety argument is that there is one path.

**13. The transcript is confirmed before it becomes a turn.** *"tiga ratus
juta"* and *"tiga puluh juta"* differ by one syllable and a factor of ten, and a
misheard multiplier produces a question the agent then answers perfectly and
uselessly — grounded, cited, non-fabricated and wrong, which is the class
`T-F1` closed for stale data arriving by a new door.

**14. Spoken output may state no figure the written answer does not.** A spoken
summary is a second statement of the same numbers, generated separately, and
nothing in this repository currently checks one restatement against another.
Rounding down to something sayable is expected; a new number is a defect, and it
is checked deterministically rather than asked for politely.

**15. Speech is never a reason a turn fails.** `ChatRunner.companyContext`'s
rule. A dead TTS provider yields a text answer; a dead STT provider yields a
disabled button and a typed question. One `speech.Transcriber` and one
`speech.Synthesizer` interface with a `nop` behind each, logged once at startup
— the `email.Sender` shape from `T-F6`, carrying `T-P8`'s finding that a
disabled dependency returning no error reads as a working one.

**16. Audio is evidence with a short life; the transcript is the record.** The
message is the transcript. The clip is retained for a bounded window so a user
can check what was heard, and it is wired into `T-H6`'s erasure from day one
rather than discovered by it later.

---

## 2. What already exists, and is the reason this is 14.5 days

| Mechanism | Where | Why this track needs no new version of it |
| --- | --- | --- |
| Access as a diffable table | `cmd/api/policy.go`, `TestEveryAuthedRouteIsClassified` (`policy_test.go:114`) | A capability is a second column, not a second authorisation system |
| An optional outbound dependency with a `nop` and a startup log | `internal/email` (`T-F6`) | `internal/speech` is the same shape, for the same reason |
| Object storage with streaming reads | `StorageService.StreamKey` (`adapters/storage/minio.go:151`) | An audio clip needs no new store and no new presigning |
| A cheap second model with its own budget line | `LIGHT_LLM_API_KEY` (`config/config.go:625`) | Transcription and the speakable reduction already have a home |
| Per-turn evidence and output guards | `guardrails.TurnEvidence`, `CheckFabrication`, applied at `chat_runner.go:1153` | A computed figure rides `TurnEvidence` exactly as `DataRows` does |
| The data-tool list | `agentbudget.dataTools` (`budget.go:162`) | `compute` is one more entry, no signature change |
| Untrusted-input tracking with kinds | `internal/taint` (`T-P10`, `T-H8`) | A program written after reading rows is already recorded as such |
| Exact re-derivation at tight tolerance | `internal/doctable/verify.go` (`T-P5`) | The precedent for *exact, not within one percent*, including the rounding rule |
| Caps and a cost line per unit of work | `T-P11`'s per-document quotas | CPU-seconds is the same shape as pages |
| Asking as a tool rather than an instruction | `internal/tools/ask_clarification.go` (`T-Q4`) | When a calculation is ambiguous, the way to ask precisely already exists |
| Fiscal year in the composed prompt | `CompanyProfile.fiscalLine()` (`domain/company_profile.go:140`) | Period boundaries have a home and a form |
| A Python sidecar image, non-root, nothing installed it does not need | `apps/docparse/Dockerfile` | The precedent — and `T-W4` is the argument for **not** repeating it |
| A tool payload decorator pattern | `attachProbe` / `attachFreshness`, `internal/tools/run_sql.go` | The working attaches the same way |

---

## 3. The tickets

### Track A — An answer that was computed (7.5d) · do first

#### `T-W1` `compute`: exact arithmetic, and no sandbox at all — **BUILT 2026-09-12**
**Repo:** BE · **Size:** 1.0d · **Deps:** none · **Migration:** none

> **Built, `make check` green, unit-gated.** Record and the two owed gates:
> [`../coverage/exact-computation.md`](../coverage/exact-computation.md).
>
> **Two things the ticket did not anticipate, both settled in the build.**
> The signature widened from `map[string]decimal` to a `Value` that is either a
> figure or a column, because `sum` over a named column — which the same ticket
> asks for — has no other shape. And the ticket's last acceptance line ("a
> payload for a turn that never calls `compute` is byte-identical") turned out
> to constrain the *design* rather than to be a test of it: `result_id` cannot
> be attached unconditionally, so the turn's result memory is installed only
> for a turn that holds the tool, exactly as `attachFreshness` attaches nothing
> for a source with no expression.
>
> **And two the ticket got wrong.** It says *Migration: none*, and a backfill
> turned out to be mandatory: every gallery card lists its tools explicitly, so
> without `081` every template-created agent would be scoped away from the tool
> — the *"a capability nobody's allowed_tools contains is a capability nobody
> has"* rule, and the same hole `043` closed for `generate_document`.
>
> Second: it scheduled the prompt sentence into
> `T-W2` — but `TestEveryRegisteredToolHasAPromptLine` makes a catalog line
> mandatory for any registered tool, so registering `compute` *is* a prompt
> change. The line was written; the paired eval it owes is filed rather than
> skipped, and should be run once, covering `T-W2`'s guideline as well.

##### Why
The margin case, which is the common one. Revenue and cost came back from two
`run_sql` calls; the ratio the user asked for was produced by the model dividing
two numbers inside a sentence. It is ungrounded by construction (research §3a)
and wrong by less than the one-percent tolerance when it is wrong at all
(research §3b). **This closes that case in a day and without executing anything**
— and it is the measurement `T-W3` reads to decide whether the sandbox is worth
three more.

##### Do
- `internal/compute`: `Eval(expr string, vars map[string]decimal) (decimal, error)`
  over a small arithmetic grammar — `+ - * / ( )`, comparison, `min`, `max`,
  `abs`, `round(x, n)`, `sum` over a named column of a prior result. No
  identifiers it was not handed, no function table it can grow into a language.
- A `compute` tool taking an expression, a map of named values, and a `unit`.
  The names are bound from **prior tool results in this turn**, by id, so the
  inputs are values this product returned rather than values the model retyped —
  which is the second half of grounding and the half that catches transcription.
- Decimal throughout (decision 2), with the expression, the bound values and the
  result on the payload so the working is on the audit row (decision 5).
- `dataTools["compute"] = true` (decision 4).
- A division by zero, an unbound name and an overflow are errors with a message
  the model can act on, never a value (decision 9).

##### Acceptance
- [ ] `0.1 + 0.2` returns exactly `0.3`, and the test asserts the string
- [ ] A name not produced by a tool in this turn is refused, and the error says so
- [ ] A figure `compute` returned grounds a reply; the same figure typed by the
      model with no `compute` call does not
- [ ] Division by zero returns an error and no number reaches the reply
- [ ] The expression and every bound value appear on the `agent_actions` row
- [ ] A payload for a turn that never calls `compute` is byte-identical to
      today's — marshalled twice and compared, the `T-F2` arm

##### Gate
`go test ./internal/compute/... ./internal/tools/... ./internal/guardrails/... -race`, then `make check`.

##### Out of scope
Loops, cross-source joins and reconciliation. Those are `T-W4`/`T-W5` and the
whole point of `T-W3` is to find out how much of it is left.

---

#### `T-W2` Money, periods and rounding are stated rather than assumed — **BUILT 2026-09-12**
**Repo:** BE + FE · **Size:** 1.0d · **Deps:** `T-W1` · **Migration:** none

> **Built, `make check` green, unit-gated.** Record:
> [`../coverage/exact-computation.md`](../coverage/exact-computation.md) §5.
>
> **"Migration: none" was wrong for the second ticket running** — and this time
> because of where the data already lives. The currency *code* has been on
> `companies.default_currency` since before any of this, read by the report
> renderer, the document generator and the prompt. Putting a second one on
> `company_profiles` would be two rows expressing one idea, so the precision
> and the rounding policy went beside the code: migration `082`.
>
> **And the quarters acceptance line cannot hold as written.** "April fiscal
> year returns Jan–Mar, January one returns Oct–Dec" needs two different
> `now`s — the windows that satisfy each are disjoint. The more useful half:
> April, July, October and January are themselves calendar-quarter boundaries,
> so a fiscal year starting in any of them produces the *same blocks* as a
> calendar year and the example cannot distinguish a correct implementation
> from one that ignores the fiscal month at all. The discriminating test uses a
> **May** fiscal year.

##### Why
Exact arithmetic over the wrong convention is exactly wrong. IDR has **zero**
minor units, so a rupiah figure carrying two decimals is a dollar assumption
that has already gone wrong somewhere upstream. A fiscal year that starts in
April makes *"this quarter"* a different three months, and
`CompanyProfile.FiscalYearStartMonth` already knows — it is rendered into the
prompt and read by nothing that computes.

##### Do
- `domain.Currency` on the company profile: code, minor units, and the rounding
  convention (half-up or half-even) stated rather than defaulted silently.
- `compute` carries the currency's scale, and a money-typed result is quantised
  to it. A ratio is not money and is not quantised — the type is on the call.
- A `fiscal_period(name)` helper resolving *"last quarter"*, *"YTD"*, *"last
  month"* against `FiscalYearStartMonth`, returning a closed-open date range,
  and exposed to `compute` and to the prompt as a fact rather than as a hint.
- The settings form gains the currency and the rounding convention beside the
  fiscal year already there.
- **One sentence** in `bootstrap.SystemPrompt` about using `compute` for a
  derived figure. This is a prompt change, so `make eval` before and after is
  mandatory ([`../agents/verification.md`](../agents/verification.md)).

##### Acceptance
- [ ] An IDR money result carries no decimal places; a USD one carries two
- [ ] A ratio is not quantised to the currency's scale
- [ ] `fiscal_period("last quarter")` against an April fiscal year returns
      Jan–Mar, and against a January one returns Oct–Dec — the table test
- [ ] A company with no currency set behaves exactly as today: nothing quantised,
      no block added to the prompt
- [ ] Half-up and half-even both round `2.5` the way their names say
- [ ] `make eval` at or above [`../coverage/eval-baseline.md`](../coverage/eval-baseline.md), both numbers pasted

---

#### `T-W3` The measurement that decides whether the sandbox is worth building — **BUILT 2026-09-12**
**Repo:** BE + FE · **Size:** 0.5d · **Deps:** `T-W1` · **Migration:** none

> **Built, unit-gated, the number not read.** Record:
> [`../coverage/exact-computation.md`](../coverage/exact-computation.md) §6.
> `Migration: none` held — the first header on this track that did.
>
> **What the ticket got wrong: it is not retroactive, and it cannot decide
> anything yet.** "Retroactive to all 437 turns" is true of the tool half only.
> Nothing stored a turn's grounding verdict — `CheckGrounding` wrote a log line
> and a counter — and tool outputs are not stored at all, so "stated a figure no
> tool returned" had no answer for any past turn. The build now stores the
> verdict on the reply's `metadata`, and the measurement starts the day it
> deploys. **The decision point below therefore waits on weeks of production
> turns with `compute` and the record both live**, not on one read.
>
> **And one it could not have known: the instrument was blind to margins.**
> `CheckGrounding` skips every figure under 1,000, so 18.42% was never even
> extracted. A residue count over it would have read small by construction and
> cut `T-W4` on a property of the regex. Percentages are now checked in their
> own report fields, and `T-Q11`'s counter is unchanged.
>
> Acceptance lines 2 and 3 are unit-proven (`TallyDerivedFigures`, and the
> grounding record on the same margin sentence with and without `compute`).
> Line 1's SQL half and line 4 are owed:
> [`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §7b.

##### Why
`T-F5` is currently held on a read that costs nothing and sizes three days of
work, and the discipline is worth repeating rather than admiring. `T-W4` and
`T-W5` are **five days** aimed at the three classes `compute` cannot express
(research §3d). Nobody knows how often this deployment meets them.

##### Do
- Over `agent_actions` and `messages`, grouped by `message_id` the way
  `CoverageService` already does (`T-F4`): how many turns state a figure that no
  data tool returned, how many call `compute` once it exists, and how many call
  it and then state a *further* derived figure anyway — which is the number that
  says a program is needed.
- A count of turns whose data tools spanned **two different sources**, which is
  the cross-source class and needs no new instrumentation at all.
- A panel row on `/quality`, beside `T-F4`'s coverage panel, because it answers
  the same shape of question.

##### Acceptance
- [ ] The query is scoped `WHERE company_id = $1` and a second tenant cannot appear
- [ ] A turn calling `compute` and stating only `compute`'s result is counted as covered
- [ ] A turn calling `compute` and then stating an *additional* derived figure is
      counted separately — this is the bucket that justifies `T-W4`
- [ ] Run against real data, and the number pasted into the track's coverage
      record, `coverage/exact-computation.md`, which this ticket creates

##### Decision point
**If the residue is small, `T-W4` and `T-W5` are cut and the track ends here at
2.5 days.** That outcome is a success, not a shortfall, and this ticket exists
to make it available.

---

#### `T-W4` The sandbox: CPython under WASI, with no way out
**Repo:** BE · **Size:** 3.0d · **Deps:** `T-W3` · **Migration:** none

##### Why
Loops, cross-source arithmetic and reconciliation (research §3d). And the
request as the owner put it: *the agent creates a program by itself*.

##### Do
- `internal/sandbox`: `Run(ctx, program string, input []byte, Limits) (Result, error)`,
  executing CPython compiled to `wasm32-wasi` under
  [`wazero`](https://wazero.io/) — pure Go, no cgo, no new deployment object
  (decision 8).
- **A fresh instance per call.** No pooling in v1: pooling shares an interpreter
  between tenants, and the saving is measured in `T-W3`'s own numbers before it
  is bought.
- The host grants **no** sockets, **no** filesystem, **no** environment and
  **no** clock. Input arrives on stdin as JSON; the result leaves on stdout as
  JSON; stderr is the error channel.
- `Limits`: wall clock, fuel or instruction budget, memory, and output bytes —
  the `T-P11` shape, each with a stated default and each surfaced as a distinct
  error rather than a generic failure.
- The stdlib subset is fixed and documented, with `decimal` in it and the
  dataframe libraries deliberately absent (decision 3).

##### Acceptance
- [ ] `Decimal('0.1') + Decimal('0.2')` returns exactly `0.3`
- [ ] A program opening a socket fails, and the failure names the reason
- [ ] A program reading `/etc/passwd` fails
- [ ] A program reading an environment variable sees nothing
- [ ] An infinite loop is killed by the fuel limit, not by the wall clock, and
      the error says which — the two limits are distinguishable or neither is useful
- [ ] A program allocating without bound is killed by the memory limit
- [ ] Output over the byte cap is truncated with a visible marker, and the
      marker is inside the cap (`T-B1`'s rule)
- [ ] The same program over the same input, run twice, returns identical bytes
- [ ] Two concurrent runs cannot observe each other — asserted by one writing a
      module global and the other reading it

##### Notes for the implementer
- **Three things are unmeasured and any of them can change this ticket**
  ([`../research/08-voice-and-exact-computation.md`](../research/08-voice-and-exact-computation.md) §6):
  whether `decimal` works in a WASI build at all, cold-start latency, and the
  reported ≈150 MB bundle. **Spend the first half-day proving `decimal` before
  writing anything else.** If it does not work, the fallback is an ephemeral
  no-network container per execution and this ticket is re-sized, not patched.
- Do not add a package because a program failed without it. The stdlib subset is
  a security boundary, and every addition is a decision with a reason written
  down.

---

#### `T-W5` `run_program`, and the working it leaves behind
**Repo:** BE + FE · **Size:** 2.0d · **Deps:** `T-W4` · **Migration:** none

##### Do
- A `run_program` tool: the source, a declaration of which prior tool results
  are bound as input, and a description of what it computes. Results are bound
  by id, never retyped by the model (`T-W1`'s rule).
- `dataTools["run_program"] = true` (decision 4).
- The program, its inputs' ids and its output land on the `agent_actions` row
  (decision 5), and the turn's `taint` record already says what was read before
  it was written (decision 7).
- The dashboard shows the program under the answer, collapsed, beside the
  existing tool-call card — *this is how that number was produced*.
- A budget line: programs per turn, and CPU-seconds counted into the turn's cost
  the way `T-P3`'s OCR pages are.

##### Acceptance
- [ ] A figure `run_program` returned grounds a reply
- [ ] A program naming a tool result from a *different turn* is refused
- [ ] A program that raises returns the traceback to the model and no number
      reaches the reply
- [ ] The stored program re-run by hand over the stored inputs reproduces the
      stored output — the property decision 5 exists for, asserted rather than claimed
- [ ] A turn that calls no program produces a byte-identical payload to today's
- [ ] The per-turn program cap refuses the N+1th call with a message the model
      can act on

---

### Track B — The gate that does not exist yet (1.5d) · **moved to `T-Z1`**

#### ~~`T-W6` A capability a user can be granted~~ — **superseded 2026-09-11 by `T-Z1`**
**Repo:** BE + FE · **Size:** 1.5d · **Deps:** none · ~~**Migration:** `081`~~

> **Do not build this ticket.** The owner widened the request the same day —
> *an admin gives specific access to agents, dashboards and features, not only
> to voice* — and [`12-access-grants-roadmap.md`](12-access-grants-roadmap.md)
> `T-Z1` builds this table once, for every capability rather than for one.
> Track C depends on `T-Z1` instead, at the same size and with the same
> acceptance items; the `voice` capability is one member of `T-Z1`'s
> vocabulary. Everything below is kept because `T-Z1` inherited its
> reasoning, and a superseded ticket that is deleted takes its argument
> with it.

##### Why
The request asks for *"an access like director or manager"*, and this product
has `admin` and `member` (`domain/user.go:12`). Adding a rank asks what a
manager may do to every one of ~200 policy rows; adding a grant asks nothing of
any of them (research §1, decision 10).

##### Do
- `081_user_capabilities`: `(company_id, user_id, capability, granted_by,
  granted_at)`, unique on the first three. Additive, no backfill, nobody has
  anything.
- `domain.Capability` as a closed set — `voice` is the only member on day one,
  and the type is the reason a typo in a policy entry is a compile error.
- `middleware.RequireCapability(capabilityPolicy)`, composed **after**
  `RequireRole`. A capability only ever adds a requirement; it can never admit a
  caller the role table refused (decision 11).
- `TestEveryAuthedRouteIsClassified` gains an arm: every capability entry names a
  route that exists, and every capability-gated route is also in the role table.
- Grant and revoke are admin routes; the read is the user's own.
- Settings → Team gains a toggle per member, and a member without the capability
  sees **the control, disabled, with a sentence saying who to ask** — the
  2026-08-04 decision recorded in [`../coverage/watchers-ui.md`](../coverage/watchers-ui.md).

##### Acceptance
- [ ] A member without the capability gets 403 on a capability-gated route
- [ ] An **admin** without the capability also gets 403 — a rank is not a grant,
      and this is the assertion that proves decision 10 was actually implemented
- [ ] A revoke takes effect on the next request, with no re-login
- [ ] A capability naming a route that does not exist fails the classification test
- [ ] A capability-gated route missing from the role table fails the same test
- [ ] Granting the same capability twice is idempotent, not an error
- [ ] Every route that exists today behaves identically — the whole role table
      re-asserted unchanged

##### Out of scope
Capabilities on API keys and embed sessions. `T-A2`'s scopes are the analogous
mechanism there and they are a separate matrix; a voice route is a dashboard
route and nothing else reaches it.

---

### Track C — A question you speak, and an answer you hear (5.5d)

#### `T-W7` Speech in: a question you say out loud
**Repo:** BE · **Size:** 2.0d · **Deps:** `T-Z1` (was `T-W6`) · **Migration:** `082`

##### Do
- `internal/speech`: `Transcriber` (`Transcribe(ctx, audio io.Reader, mime, langHint) (Transcript, error)`),
  one provider implementation, and a `nopTranscriber` that logs once at startup
  that voice is off (decision 15).
- Config: `SPEECH_ENABLED`, `SPEECH_PROVIDER`, `SPEECH_API_KEY`,
  `SPEECH_STT_MODEL`, `SPEECH_MAX_CLIP_SECONDS`, `SPEECH_RETENTION_DAYS` — into
  `.env.example`, which `T-H6`'s gate found missing variables the process
  refuses to boot without.
- `POST /api/threads/:id/voice` (member **+ `voice`**): multipart audio in, a
  transcript out. **It does not start a turn** (decision 13) — it returns text
  the user then sends, or edits, or discards.
- `082_voice_clips`: the clip's object key, its duration, its transcript, the
  message it became if it became one, and `expires_at`. Wired into `T-H6`'s
  erasure in this ticket, not a later one (decision 16).
- Duration, size and MIME are capped at the route, before a byte reaches a
  provider. An unbounded upload to a metered API is an unbounded bill.
- Cost per clip into `usage_events`, priced per second, so voice shows up in the
  same ledger as everything else rather than as an invoice nobody can attribute.

##### Acceptance
- [ ] A deployment with `SPEECH_ENABLED=false` boots, logs once, and the route 404s
- [ ] A user without the `voice` capability gets 403 before any audio is read
- [ ] A clip over the cap is refused at the route and never reaches the provider
- [ ] A transcript is returned and **no message is created** — asserted by
      counting the thread's messages before and after
- [ ] A provider failure returns an error the UI can show and leaves no orphan clip
- [ ] No audio and no transcript appears in any log line at `Info`
- [ ] A clip past `expires_at` is deleted by the sweep, and its message survives
- [ ] `T-H6` erasure removes a user's clips and their objects
- [ ] Language hint defaults to the tenant's, not to English

---

#### `T-W8` Speech out: an answer you can listen to
**Repo:** BE · **Size:** 2.0d · **Deps:** `T-W7` · **Migration:** none

##### Why
The request, exactly: *"I want our agent to be able to answer in voice too."*

##### Do
- `speech.Synthesizer` (`Speak(ctx, text, voice) (audio, error)`) behind the
  same optionality rules, with its own `nop`.
- **A speakable reduction** of the reply: no markdown, no table, no SQL, no
  citations-as-brackets, figures rounded to something a person can hold. Produced
  by the light model (`LIGHT_LLM_API_KEY`, `config.go:625`), because it is a
  formatting job and not a reasoning one.
- `guardrails.CheckSpokenFigures(written, spoken) error` — **deterministic**:
  every number in the spoken text must round-trip to a number in the written
  one, within that number's own stated precision. A reduction that invents a
  figure is refused and the text answer stands alone (decision 14).
- `GET /api/messages/:id/audio` (member **+ `voice`**), synthesised on demand and
  cached by message id. Nobody pays to synthesise an answer nobody plays.

##### Acceptance
- [ ] A table in the written answer produces no pipes, no dashes and no column
      headers in the spoken text
- [ ] `1,234,567` may be spoken as *"about 1.2 million"*; `2 million` is refused
      and the check names both figures — the `T-P5` message shape
- [ ] A spoken text stating a figure absent from the written answer is refused
- [ ] A synthesiser failure yields the text answer and a disabled play button,
      never a failed turn (decision 15)
- [ ] The same message synthesised twice hits the cache and bills once
- [ ] A user without the capability gets 403

##### Out of scope
Barge-in, streamed audio and a continuous session. That is the realtime fork and
it is a different product decision — research §2c has the table, and §5 of this
document is where it is filed.

---

#### `T-W9` The microphone, the transcript you check, and the player
**Repo:** FE · **Size:** 1.5d · **Deps:** `T-W7`, `T-W8` · **Migration:** none

##### Do
- Push-to-talk in the composer: hold to record, a live level meter so a user can
  see it is listening, release to send. `MediaRecorder`, no new dependency.
- The transcript lands **in the composer as editable text**, not in the thread
  (decision 13). Sending is the existing button.
- A play control on an assistant message, which fetches on first press.
- A user without the capability sees the microphone **disabled with a sentence**,
  not hidden — the 2026-08-04 decision: hiding a control makes a member think
  the feature is missing, disabled tells them who to ask.
- Permission denied, no microphone, and an unsupported browser are three
  distinct messages, because they have three distinct fixes.

##### Acceptance
- [ ] Recording stops and uploads on release, and on tab blur — a recording that
      keeps running after the user has left is a bill and a privacy incident
- [ ] The transcript is editable before sending, and editing it sends the edit
- [ ] Cancelling after recording uploads nothing
- [ ] The play control is absent, not broken, when synthesis is off
- [ ] Denied permission renders the reason and the fix
- [ ] `pnpm --filter dashboard lint` and `build` clean, plus a harness
      screenshot of the composer in all three states (granted, denied, ungranted)

---

## 4. Cut order

| # | Cut | Saves | What is lost |
| - | --- | ----- | ------------ |
| 1 | `T-W8` + `T-W9`'s player | 2.5d | You can speak to it and read the answer. Dictation, which is most of the value of voice on a dashboard |
| 2 | Track C entirely (`T-W7`→`T-W9`) | 5.5d | No voice. The capability grant does **not** go with it — `T-Z1` owns it now and other capabilities want it |
| 3 | `T-W4` + `T-W5` | 5.0d | No programs. `T-W1` still closes the margin case, and **`T-W3` is what says whether this cut costs anything** |
| — | **Floor** | **2.5d** | `T-W1` + `T-W2` + `T-W3`: derived figures become exact and grounded, money and periods stop being assumed, and the residue is measured |

**`T-W1` is never cut.** It is the only ticket here that closes a defect this
repository has already recorded against itself.

**`T-W3` is never cut either, and it is half a day.** Cutting the measurement
and keeping the sandbox is how five days get spent on a class of question this
deployment may not ask.

---

## 5. What is deliberately not here

- **Realtime duplex voice** — barge-in, sub-second latency, a session that feels
  like a phone call. It bypasses the text turn, so `CheckFabrication`,
  `agentbudget`, `taint`, the audit row and credit enforcement each need a
  second implementation against a token stream (research §2c). Filed, not built,
  and it needs a product decision rather than a ticket.
- **Voice notes from WhatsApp, Slack or Lark.** Inbound audio from a channel is
  a new untrusted-input surface with its own threat model — `T-H8`'s argument
  and `T-F6`'s decision 12 about inbound email, for the same reason.
- **Voice in the embeddable widget.** The widget serves unauthenticated visitors
  on a tenant's own site; a microphone there is a consent question, not a
  capability question.
- **pandas, numpy, or any package added because a program failed without it**
  (decision 3). The stdlib subset is a security boundary.
- **A learning loop over stored programs** — *"this cluster of questions is
  always answered by this program, promote it to a metric"*. It is `T-F5`'s
  shape one layer up, and it is gated on `T-F5`'s own measurement first.

## 6. What needs no ticket

- **Voice on the mobile dashboard.** `MediaRecorder` is the same API; the
  composer is the same component.
- **`compute` over the MCP surface.** An MCP client calling `run_sql` gets the
  same payload the in-process tool does, so a tool registered in the shared
  registry reaches it for free — with the caveat `T-14`'s `list_watchers` earned:
  **do not register `run_program` over MCP until something asks for it**, because
  a tool in the registry is a capability in every turn's prompt.
- **A per-tenant speech provider.** `llmtenant` already resolves per-tenant model
  credentials; speech would follow it if a tenant ever asks, and no tenant has.

## 7. The case against doing this now

Committed work has been at 0.0 days since
[`00-sprint-overview.md`](00-sprint-overview.md) §9e. What is actually owed is
not code: four un-run gates against work already written — `079`/`080`'s
round-trips, `T-F4`'s coverage read, `T-F2`'s paired eval, and an email surface
that is **zero percent live-gated on a new protocol**
([`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §1t,
§3d, §7a). This repository's own record is that the live half has found
something in sixteen sittings out of sixteen, so that list is a list of unknown
defects rather than paperwork.

Starting 14.5 days of new surface on top of it is a decision, and it should be
made rather than drifted into. **The version of that decision this document
would defend is: run the three cheap gates first — they are one afternoon — then
build `T-W1`→`T-W3` (2.5 days), and let `T-W3`'s number decide the rest.**
