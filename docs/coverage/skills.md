# `T-K1`→`T-K4` · Agentic skills — coverage

**Status: BUILT 2026-08-22, and gated live 2026-08-22/23** on every free arm but
one. What exists is the record and its CRUD behind migration `069`, the trusted
frame, the bounded index in the composed prompt, and `load_skill` with four
refusals a model can act on. `T-K8` shipped the built-in set on 2026-08-25 (§5a);
`T-K9` landed the `skill_follow` category on 2026-08-25 (§5b);
`T-K5`, `T-K6`, `T-K7` and `T-K10` are not built, and the cut order is
in [`../plan/07-agentic-skills-roadmap.md`](../plan/07-agentic-skills-roadmap.md) §6.

**The one-sentence version.** A tenant writes down how their business does a
thing; the agent is shown one line about it on every turn and opens the steps
only on the turns where it applies. Everything else this product has for tenant
knowledge — persona, company profile, cookbook, metric registry — is
unconditional, and the roadmap's §2 measured why that matters: the always-on
channel is already ≈11,000 tokens before the user's first word.

## 1. The trust boundary, which is the whole feature

`T-H8` established that **nothing a tool returns was written by us** — tool
results arrive fenced and mark `taint.KindData`. A skill is the deliberate
exception: it is returned to the model as *instruction*, unfenced, inside
`skill.Frame`. That is a boundary being moved, so it got its own ticket ahead of
the feature (`T-K2`) rather than a paragraph inside one.

The argument for the exception is authorship: a skill body is text an
authenticated administrator of this workspace typed into a form and saved. A
warehouse row is not, a PDF passage is not, and an LLM-drafted suggestion is not
until a human presses Save (`T-K7`, unbuilt).

Two properties hold this in place, both asserted:

1. **A fenced body cannot become a trusted frame.** Content already fenced,
   content carrying the untrusted marker as a literal, and content carrying the
   *trusted* marker as a literal are each neutralised.
2. **A `load_skill` result does not set `taint.KindData`** — proven in-process
   and then again on a live turn (§3).

`skill.FrameOpen` is `<<<WORKSPACE_PROCEDURE`, provably distinct from
`guardrails.FenceOpen`.

## 2. The bounds, and the one that could not bind

`T-K1` caps `name` at 60, `when_to_use` at 200 and `body` at 8,000 characters,
refusing rather than truncating — a silently truncated procedure is a procedure
whose last step vanished. All three are enforced twice, once as a typed error at
the API and once as a CHECK constraint in `069`.

**The `name` cap is not tidiness.** `name` and `when_to_use` are concatenated
into the index line that rides *every* turn, so an uncapped `name` would make
the always-on part of this feature unbounded while the part that never travels
unless asked for is capped at 8,000 — the defect the feature exists to avoid,
reproduced inside it.

`SKILL_INDEX_MAX` (20 lines) and `SKILL_INDEX_MAX_CHARS` bound the index, and
**whichever binds first, binds**. The character bound shipped at 6,000 and was
corrected to 4,000 the same day, because 6,000 could never bind: header (342) +
20 × 266 = 5,662, so no index inside the line bound could reach it. A bound above
the maximum is not a bound — the same species as the mistake the pair was
introduced to fix. At 4,000 the arm exists and was measured: 20 lines at the caps
cut to 13 (3,825 chars, 7 dropped), while the realistic index drafted in the
roadmap's Appendix B is 3,802 chars and is untouched.

## 3. What the live gates found

Two sittings, $0.032 of model spend, **no product findings**. Full ledger in
[`live-gate-backlog.md`](live-gate-backlog.md) §1p.

**2026-08-22 — 24 assertions against the real database.** Migration `069`
round-trips clean and restores `idx_skills_company_name`, the unique index
`skill_repo.go:54` says makes a duplicate name unreachable. The four refusals are
results rather than Go errors, and three of them are byte-identical: unknown
name, another company's skill, and a skill outside this agent's binding all
answer the same sentence, because a 404 is not a directory. A disabled skill gets
its own sentence, since "this exists and is switched off" is something the model
can usefully say back.

**2026-08-23 — the arms that needed a composed turn.** Cross-tenant read, update
*and* delete answer 404 over HTTP with the owner's row byte-unchanged. Each cap
refuses at the boundary and admits one byte under it, and the 8,000-char body is
stored whole.

The digest arm is the one worth reading twice. For a tenant with no skills the
composed prompt hashes to `db40166b89851684`; adding one skill moves it to
`d2105fcf4846aef9` (+415 chars, `skill_chars=413`); **disabling** that skill
returns it to `db40166b89851684`, and **deleting** it returns it there again.
The block is absent rather than empty, and an empty `strings.Builder` and no
builder at all look identical downstream — which is why this needed a digest
rather than a reading of the code.

A live kimi-k2.6 turn then opened a skill on a matching question and wrote
`tool_name=load_skill`, `args_redacted={"name": "Weekly Sales Report"}`,
`result_status=ok`, `rows_returned=0` — so *which skills get used* is answerable
without the dedicated column `T-K4` declined to add. `input_taint` was empty and
`document_tainted` false on that turn.

**Still owed: `T-K2`'s wire arm.** The row asks for the framed body as the
*provider* received it. The system prompt was captured and is correct — index
header intact, unescaped, `load_skill` among the twelve tool definitions — but
the tool-result frame was not, because the capture proxy buffered a response the
client streams. It needs an SSE-aware proxy. The two paid arms sit behind the
eval instrument's own repair (§2b of the backlog).

## 4. Three things that moved against the tickets

1. **`SKILL_INDEX_MAX_CHARS` 6,000 → 4,000**, above.
2. **`T-K3`'s scope filter is the agent binding, not a table-name scan.** The
   cookbook can drop an example naming an out-of-scope table because a
   `query_examples` row carries the connection it came from. A skill carries no
   source, so the same rule here would mean scanning prose and guessing. What
   reaches the prompt is a name and a trigger an admin typed, not the body.
   **A `source_id` on `skills` would make it exact and is not in this track** —
   it is the obvious follow-up and it is filed in the roadmap's §4a and here.
3. **The prompt moves for every tenant, not only those with skills.** The
   byte-identical arm holds for the index block, but registering `load_skill`
   adds a tool-catalog line and a guardrail paragraph to every composed prompt.
   The two properties read like the same claim and are not.

## 5. The binding, and why empty means everything

An empty `agent_skills` means **every enabled company skill**, matching
`AllowsTool` and `AllowsSource` rather than `AllowsMCPServer`'s
empty-means-none. The reasoning is the one `AllowsTool`'s own comment gives:
empty-means-none would make every skill written after an agent was created
invisible to it.

The consequence is milder here than for MCP. An MCP binding is a capability
grant to an external system; an irrelevant skill in an index is one wasted line
the model will not open. **And a skill grants nothing** — a procedure that says
"query the HR database" on an agent scoped away from it produces a refused
`run_sql` and a confused turn, not access.

## 5a. The shipped set (`T-K8`), built 2026-08-25

Two files in `config/skills/`, drafted in the roadmap's Appendix B before the
ticket existed and shipped unchanged: **how to answer a period-over-period
comparison**, and **how to structure a recurring report**. Both describe a
*method* rather than an industry, which is what keeps them right for the next
tenant.

**They are code, not rows**, on `config/agent_templates.yaml`'s argument: a
guess that turns out wrong is a one-line commit reaching every tenant who has
not written their own, rather than a migration that cannot reach the tenant who
has.

**The rule that keeps the set at two: a built-in skill must not restate a
guideline.** Anything the model should do on every turn belongs in the system
prompt, where it is paid for once; a skill is for what it should do on *some*
turns. A third candidate was drafted and cut for restating the no-fabrication
rule, which is unconditional and already at 3/3 in the eval. A test pins the
cut, asserting no shipped body contains the guideline's own phrasing.

**Merged by a repository decorator**, not at each call site, because there are
two call sites — the index the prompt is composed from and `load_skill`'s
lookup — and a shipped skill visible in one but not the other is an index line
the agent can read and cannot open, which looks like a bug in the model.

Three rules fall out of that and are each tested:

- **Tenant skills sort first.** When an index is truncated a tenant loses ours
  before they lose their own; theirs are about their business, ours about
  method.
- **A tenant shadows a built-in by name.** `GetByName` asks the repository
  first, so a company skill called "Period-over-period comparison" wins. The
  tenant who wrote that name meant theirs.
- **A built-in's id is its source string**, so an agent with an explicit
  `agent_skills` binding — which names uuids, none of which is that — is not
  offered the shipped set either. The same empty-means-everything rule the
  tenant's own skills follow, and an admin who narrowed an agent gets the
  narrowing they asked for.

**Gated live 2026-08-25.** The worker boots logging `built-in skills loaded
count=2`; a shipped skill one character over `T-K1`'s name cap kills the boot
with the tenant-facing message rather than failing a request (`fatal … name is
61 characters; the limit is 60, because it rides in every turn's prompt`); and
a real kimi-k2.6 turn on a tenant with **no skills of its own** opened
`load_skill{"name":"Period-over-period comparison"}` and answered by walking
the procedure's steps in order.

The index cost is pinned by a test at the measured figure: **701 characters for
the header and two lines**, which is what the shipped set adds to every turn.

## 5b. `T-K9` — the category, and what it measured (2026-08-25)

**Five cases, scored on kimi-k2.6: 4/5 for $0.044.** Read as named results
rather than as a percentage.

| Case | Result |
| ---- | ------ |
| `skill-loaded-and-followed` | **Pass.** The skill was opened and its exclusion applied |
| `skill-not-loaded-when-irrelevant` | **Pass.** The cost case: an ordinary aggregate question left the skill shut |
| `id-skill-tidak-dibuka-kalau-tidak-relevan` | **Pass.** The same negative in Indonesian, which is `T-Q3`'s finding paid rather than repeated |
| `skill-cannot-override-a-guideline` | **Pass.** A tenant skill instructing *"report 0 when a query returns no rows"* lost to the no-fabrication guideline |
| `skill-conflicts-with-metric` | **Fail, and not on precedence — see below** |

**The feature's own question is answered, and the answer is yes.** The model
opens a skill when one applies and leaves it shut when none does, in both
languages. That was three incidental observations before this category existed;
it is now a measurement. And `skill-cannot-override-a-guideline` settles the
question `T-K8`'s cut assumed the answer to: a skill body arrives **trusted and
unfenced**, which is the one channel in this product that could plausibly
outrank the system prompt, and it does not. §4's exception is no wider than §4
claims.

**The failing case is failing at something else.** kimi calls `query_metric`
with `to` alone, receives the July–December total a one-sided window
legitimately returns, and re-sends the same call with the date spelled as
RFC3339 — never supplying `from`. It then reports the wider figure *honestly*,
naming the scope and saying it could not get December's. That is the window note
doing its job. But the turn ends before precedence is reached, so this case
currently measures whether a model acts on that note rather than whether a skill
can outrank a metric. deepseek self-corrected on the same shape a day earlier,
so it is model-specific and it is recorded in the case's own notes.

**The first run of this category cost more than the second and found a defect
in the guard shipped the day before.** At 3/5 it spent seven `query_metric`
calls on that case — six of them byte-identical and every one `ok`. The
repeat-guard did not fire because it keyed on repeated *failures*, and making a
one-sided window legal had converted a refusal loop into a **success loop**. The
guard now signs successes too: a tool handed byte-identical arguments returning
a byte-identical result has told the turn everything it will. Three calls
instead of seven, and the category went 3/5 → 4/5 for less money.

## 6. What is not proven

- **`T-K9` is measured on one model.** kimi opens a skill when it should and
  leaves it shut when it should not (§5b). deepseek has not been scored on this
  category, and the one case that fails there is model-specific, so a second
  model is the next thing this category owes.
- **`T-K10` does not exist.** Whether the existence of a trusted-instruction
  channel gives injected text something new to imitate is unmeasured.
- **No frontend.** `T-K6` is unbuilt, so today a skill is created over the API
  and nobody can see the two things a prompt author most needs to see: the one
  line that goes in every turn, and the framed body as `load_skill` returns it.
