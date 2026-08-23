# `T-K1`→`T-K4` · Agentic skills — coverage

**Status: BUILT 2026-08-22, and gated live 2026-08-22/23** on every free arm but
one. What exists is the record and its CRUD behind migration `069`, the trusted
frame, the bounded index in the composed prompt, and `load_skill` with four
refusals a model can act on. `T-K5`→`T-K10` are not built; the roadmap's cut
order is in [`../plan/07-agentic-skills-roadmap.md`](../plan/07-agentic-skills-roadmap.md) §6.

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

## 6. What is not proven

- **`T-K9` does not exist**, so the question the feature turns on — does the
  model open a skill when it should and leave it shut when it should not — has
  been observed twice on one model and measured zero times. Both observations
  went the right way (§1p).
- **`T-K10` does not exist.** Whether the existence of a trusted-instruction
  channel gives injected text something new to imitate is unmeasured.
- **No frontend.** `T-K6` is unbuilt, so today a skill is created over the API
  and nobody can see the two things a prompt author most needs to see: the one
  line that goes in every turn, and the framed body as `load_skill` returns it.
