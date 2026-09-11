# Exact computation — an answer that was computed rather than composed

The plan is
[`../plan/11-voice-and-exact-computation-roadmap.md`](../plan/11-voice-and-exact-computation-roadmap.md)
Track A; the evidence is
[`../research/08-voice-and-exact-computation.md`](../research/08-voice-and-exact-computation.md).
**Two tickets of five are built.**

| Ticket | Status |
| --- | --- |
| `T-W1` `compute`: exact arithmetic, no sandbox | **built 2026-09-12, unit-gated. Migration `081` (a backfill the ticket did not anticipate — §3a). Three gates owed — §7** |
| `T-W2` Money, periods and rounding stated rather than assumed | **built 2026-09-12, unit-gated. Migration `082` (the ticket said none — §5a). Two gates owed — §7** |
| `T-W3` The measurement that decides whether the sandbox is worth building | Not built — §6 is the panel it owes a number to |
| `T-W4` The sandbox: CPython under WASI | Not built, and deliberately not started — it is what `T-W3` decides |
| `T-W5` `run_program`, and the working it leaves behind | Not built |

---

## 1. The defect, which is not a crash

Revenue comes back from one `run_sql` and cost from another. The user asks for
the margin. The model divides two numbers inside a sentence, and the figure in
the reply is one **no tool returned**.

Every instrument this product owns vouches for it:

- `CheckFabrication` is satisfied — the turn *did* retrieve rows
  (`internal/guardrails/fabrication.go`, `TurnEvidence.grounded()`).
- `CheckGrounding` cannot match it, because a derived figure is by construction
  not one of the figures a data tool returned.
- The eval's numeric tolerance is one percent, and the error a model makes doing
  division is smaller than that.

`T-Q14` priced the class exactly. A turn printed December revenue as
`$3,860,405,700.00` where its own `run_sql` had returned `3,863,405,700.00`.
**The misquote is 0.078% — inside every tolerance this product owns — so the
reply reported clean.** One percent of a billion is ten million.

`T-W1` closes the class for arithmetic, in a day, **without executing anything**.

## 2. Two halves, and only doing one would not work

**Exactness.** `internal/compute` is a small arithmetic grammar over
`shopspring/decimal`. There is no `float64` in the evaluation path, because
`0.1 + 0.2 != 0.3` in every IEEE-754 language and the last decimal place is what
a reconciliation is looking at. The test asserts the **string**, not a
comparison inside a tolerance — a tolerance is exactly what let `T-Q14`'s figure
through.

**Provenance.** An input is a *reference* — `r1.total_revenue` — resolved
against this turn's own results, never a number the model typed into an
argument. A `compute` tool that accepted `{"revenue": 3863405700}` would be
exact arithmetic over a transcription, and transcription is the error `T-Q14`
actually found. Binding by id means the digits never pass through the model.

## 3. What was built

| Piece | Where |
| --- | --- |
| The grammar and the evaluator | `internal/compute/compute.go` |
| The turn's memory of what its data tools returned | `internal/tools/turn_values.go` |
| The tool | `internal/tools/compute.go` |
| `result_id` on `run_sql` and `query_metric` payloads | `internal/tools/run_sql.go`, `internal/tools/metric_tools.go` |
| `compute` as evidence; one computed figure counts as one | `internal/agentbudget/budget.go` (`dataTools`, `rowCount`) |
| The memory, installed only for a turn that holds the tool | `internal/app/chat_runner.go` (`allowsCompute`) |
| The working, on the audit row | `internal/tools/audit.go` (`resultWorking`) |
| The catalog line | `internal/bootstrap/system_prompt.go` |
| The tool on every gallery card, and on the agents that predate it | `config/agent_templates.yaml`, `migrations/control/081_agent_tools_compute.up.sql` |

### 3a. The migration the ticket said it did not need

`T-W1`'s header reads **Migration: none**, and that was wrong for a reason worth
writing down: *a capability nobody's `allowed_tools` contains is a capability
nobody has* ([`../AGENTS.md`](../AGENTS.md) §3).

Every card in `config/agent_templates.yaml` lists its tools explicitly, and
`draftFromTemplate` copies that list into the agent verbatim. Without a change
there, **every agent created from a template would be scoped away from
`compute`** — and the templates are the agents that need it most. Their own
starter questions ask for derived figures: *"revenue last month compared with
the month before"*, *"our conversion rate from lead to closed deal"*. Those are
precisely the answers that were being produced by the model dividing two numbers
inside a sentence.

A template reaches no *existing* agent by design, so `081` fixes the rows that
already exist — the same shape as `043`, which backfilled `generate_document`
after a Sales agent told a user to press Ctrl+P.

**`081` is unconditional where `043` had to be selective, and the reason is the
security argument:** `compute` opens no connection, reads no source and calls
nothing. Its only inputs are results the same turn already retrieved through
tools the agent was already allowed. There is no agent for whom "may also
compute" widens what it can *see* — only what it can say correctly about it.
The `down` is `SELECT 1;` for `043`'s reason: nothing records which rows were
touched, and stripping the tool from every scoped agent would also strip it from
the ones an admin ticked by hand.

**The grammar, in full:** `+ - * / ( )`, the six comparisons, `abs`, `round(x,
places)`, `sum`, `min`, `max`. No loops, no variables, no assignment, and no
function table it can grow into a language — the moment it has those it is a
program, and a program needs the sandbox `T-W4` costs three days. Division keeps
28 decimal places, which is decimal128's scale and far past any currency; an
exact division keeps nothing it does not need, so `10/2` is `"5"`.

**Division by zero, an unbound name and an overflow are errors, never values.**
A computation that silently returns zero for a division by zero is the
fabrication this package exists to prevent, wearing the product's own signature.
Every refusal carries the menu of what *is* bindable, because the model's next
move after a rejected reference is a second guess, and a guess against a listed
menu is a repair.

## 4. The two design decisions worth re-reading before changing anything

**The memory is installed per turn, and only when the turn holds `compute`.**
`run_sql` and `query_metric` attach a `result_id` when it is present and attach
nothing when it is not. An agent scoped away from `compute` therefore sees
byte-for-byte the payloads it saw before this existed — the same bargain
`attachFreshness` strikes for a source with no expression, and the arm that
proves it is `TestAPayloadIsUnchangedForATurnThatCannotCompute`.

**What the model is shown and what it can bind are the same figures.** `run_sql`
trims rows off the tail until the payload fits the context budget. The id is
therefore *reserved* before the payload is built (so it is inside the bytes the
cap is measuring) and *filled in* after the trim (so a `sum` covers the rows that
survived). `TestBindingFollowsTheTrimmedPayload` asserts it, and fails the test
rather than passing vacuously if the cap did not bite.

A NULL or non-numeric cell is absent from a bound column rather than zero —
the rule SQL's own `SUM` follows, and the only one that does not invent a figure.

## 5. `T-W2` — the convention a figure was computed under

Exact arithmetic over the wrong convention is exactly wrong, and `T-W1` shipped
the exactness without the convention.

**Money.** A `unit: "money"` result is now quantised to the tenant's currency
precision using their stated rounding. IDR carries **no** decimal places, USD
carries two, and a ratio carries whatever it has — `0.4564` quantised to
rupiah's zero places is `0`, which is why the decision is made on what the
figure *is* (`unit` is on the call) rather than on how the number looks. A
company with no currency, or one this product does not recognise, quantises
nothing: the "behaves exactly as today" path, asserted three ways.

**Rounding is a policy, not a fact.** Half-up and half-even are both offered
because several accounting standards require half-even precisely so a long
ledger does not drift upward, and which standard a tenant is held to is not
something this product can know. Unstated resolves to half-up — which is what
every answer this product has ever given already did, so the default changes
nothing — and the resolved convention is *rendered into the prompt as a
sentence*, so it is visible rather than implied.

**Periods.** `domain.FiscalPeriod` resolves "last quarter", "ytd", "last month"
and three more against the tenant's fiscal year, returning a **closed-open**
range. `BETWEEN '2026-01-01' AND '2026-03-31'` silently drops everything
timestamped on the 31st after midnight, which is the commonest off-by-one in
analytical SQL and the hardest to notice: the answer is only slightly wrong, and
only for the last day. The resolved ranges go into the turn as dates — a fact,
not the hint `fiscalLine` has been since `T-B1` — and a period's **day count**
is bindable by `compute` as `period.last_quarter.days`, which is what makes a
run rate computable rather than narrated.

### 5a. Two things the ticket got wrong, and one it could not have known

**"`domain.Currency` on the company profile" — it went on `companies`
instead.** The currency *code* has lived at `companies.default_currency` since
before the agent could compute anything, and it is read by the report renderer,
the document generator and the prompt. A second currency column on
`company_profiles` would be two rows expressing one idea — the rot this
repository has avoided everywhere else — so the precision and the policy went
beside the code rather than opposite it. Which made **`082` a migration the
ticket said it did not need**, for the second ticket running.

**And the acceptance line about quarters cannot hold as written:**

> `fiscal_period("last quarter")` against an April fiscal year returns Jan–Mar,
> and against a January one returns Oct–Dec — the table test

Those need two different `now`s. A January fiscal year yields Oct–Dec only when
asked in Jan–Mar; an April one yields Jan–Mar only when asked in Apr–Jun. The
two windows are disjoint, so no single instant satisfies both.

**The more useful half of that finding is why the example was reachable at
all.** April, July, October and January are themselves calendar-quarter
boundaries, so a fiscal year starting in any of them produces **exactly the same
three-month blocks** as a calendar year — only the numbering differs. The
ticket's own example therefore cannot tell a correct implementation from one
that ignores `FiscalYearStartMonth` entirely. The test that discriminates uses a
**May** fiscal year, and `TestAFiscalYearThatIsNotOnAQuarterBoundary` asserts
exactly that: a May year and a calendar year must *disagree* about what this
quarter is, and the test fails if they agree.

**`IDR` is 0 minor units here and ISO 4217 says 2.** The sen was withdrawn
decades ago; Indonesian prices, invoices and ledgers are whole rupiah, and this
product has rendered them that way since `T-R2`. Writing 2 because a standard
says so would put `Rp 1.234.567,00` on every figure — a dollar's shape wearing a
rupiah's name. The note is in `currency.go` beside the table, and the same
practice-over-standard call is made for no other currency.

### 5b. What `T-W2` added, by file

| Piece | Where |
| --- | --- |
| The money convention, and the ISO precision table | `internal/domain/currency.go` |
| The fiscal calendar, closed-open | `internal/domain/fiscal_period.go` |
| Quantisation, half-up and half-even | `internal/compute/compute.go` (`Quantize`) |
| Money quantised, periods bindable | `internal/tools/compute.go` |
| The columns | `migrations/control/082_company_currency.{up,down}.sql` |
| The setter, and the three-state `currency_minor_units` body | `internal/app/company_service.go`, `internal/transport/http/handlers/company.go` |
| The convention on every enqueued turn | `internal/queue/tasks.go`, the three enqueue sites |
| The currency sentence, and the resolved periods | `internal/app/chat_runner.go` |
| The guideline: do not do arithmetic yourself | `internal/bootstrap/system_prompt.go` |
| Rounding, and what a currency's precision means | `apps/dashboard/src/features/settings/general-tab.tsx` |

**The parity test worth knowing about.** The accepted-currency list lives in
`internal/app` and the precision table in `internal/domain`, and
`TestEverySupportedCurrencyHasAStatedPrecision` asserts both directions.
Without it, adding a currency to one list would let a tenant select a currency
that silently stops being quantised — which looks exactly like a currency that
was never configured.

## 6. The measurement this file exists to hold

`T-W3`'s panel goes here: how many turns state a figure no data tool returned,
how many call `compute` once it exists, and how many call it and then state a
*further* derived figure anyway. **That last number is what justifies `T-W4`**,
and until it is read, the sandbox is five days aimed at a residue nobody has
sized. The discipline is `T-F5`'s, deliberately repeated.

Nothing is in this section yet. It needs `T-W3` and a database.

## 7. What is owed

| Gate | What it would prove | Blocker |
| --- | --- | --- |
| `make eval`, paired | `T-W1` adds a line to the prompt catalog, and this repo's rule is that a prompt change owes a paired score ([`../agents/verification.md`](../agents/verification.md)). **The prediction, recorded so it can be checked rather than remembered: no movement.** No golden case asks for a derived figure, so the line is a description of a tool nothing in the set will reach for. A score that *moves* would mean one catalog line changed behaviour on turns it does not apply to, which is the more interesting outcome | A local stack and ~$0.03 of model spend. `cmd/eval` refuses a non-local `DB_HOST` |
| One live turn | That the model reaches for it at all, and that the reference syntax survives contact with a real model. Two figures from two queries, one margin — then the same question with `compute` scoped away, which must still answer and must not silently divide | The stack, a source, and a model key |

`081`'s round-trip is owed beside them, and is the cheapest of the three: one
`UPDATE` against an array column, no new table, no data loss on the way up.

All three are in [`live-gate-backlog.md`](live-gate-backlog.md) §7b. Neither is
blocked on writing code.

**`T-W2` is where the prompt gains its *guideline*** — one sentence telling the
agent to prefer `compute` for a derived figure, as opposed to the catalog line
that merely says the tool exists. The two prompt edits should be scored in one
paired run rather than two, which is the only reason this one is filed rather
than run.
