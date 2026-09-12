---
name: continue-building
description: Read the board — every roadmap's status block, the backlog and the owed-gate list — pick the next ticket by this repo's own ordering rules, build it, gate it and document it. Use when the user says "continue building", "what's next", "keep going", or asks you to pick up the next piece of work.
---

# Continue building

One ticket per invocation. Pick it, build it, gate it, document it, report. **Do
not commit** — `docs/AGENTS.md` §2 makes that the repo owner's call, and this
skill does not override it.

## 1. Read the board — and not the whole ticket file

```bash
ls -lt docs/plan/ docs/coverage/ | head -20    # what moved most recently
git log --oneline -15
```

Then read **only the status blocks**: the `> **Status…**` quote at the top of
each `docs/plan/NN-*-roadmap.md`, plus `docs/plan/backlog.md` and
`docs/coverage/live-gate-backlog.md`.

**`docs/plan/01-tickets.md` is ~380 KB. Never read it whole.** `grep -n` it for
a specific ticket id. The lettered tracks (`T-W`, `T-Z`, `T-K`…) live in their
roadmap files, not in it.

The newest roadmap's status block usually names every open item on the board in
one sentence. Start there.

## 2. Pick, and say why in one line

In priority order:

1. **A ticket whose deps are met and whose decision point is answered.**
2. **Never build past an unanswered measurement.** This repo gates work on
   reads that cost nothing and size days of effort — `T-F5` on `T-F4`'s
   coverage query, `T-W4`/`T-W5` on `T-W3`'s residue count. Building the
   measured-away ticket first is the one mistake that wastes the most time, and
   "the measurement said cut it" is a *success*.
3. **Respect the roadmap's own cut order** when one is stated.
4. **Prefer the track already in flight** over opening a new one.

If the best pick needs something this machine lacks (a database, a stack, model
spend, a cluster), say so, note it against `live-gate-backlog.md`, and pick the
best ticket that does not.

State the pick and the reason before writing code. Do not wait for approval —
"continue building" is the approval.

## 3. Read the contract

`docs/AGENTS.md` (hard rules, definition of done, reporting shape), then
`docs/agents/conventions.md` for the local idiom and
`docs/agents/verification.md` for this change type's gate. Match the
surrounding code's comment density — this codebase explains *why*, at length,
and a terse patch reads as foreign.

## 4. Build — the traps this repo actually has

**Environment**
- Go is not on `PATH`: `export PATH=$PATH:/usr/local/go/bin`.
- No database, no Docker, no model key here. Anything needing them is a gate to
  file, not a step to skip silently.

**A new agent tool is never just a tool** (`docs/AGENTS.md` §3)
- `TestEveryRegisteredToolHasAPromptLine` makes a catalog line in
  `internal/bootstrap/system_prompt.go` **mandatory** — so registering a tool
  *is* a prompt change, and a prompt change owes a paired `make eval`. A ticket
  that schedules the prompt sentence for "later" is wrong about that.
- Every card in `config/agent_templates.yaml` lists its tools explicitly, and
  `draftFromTemplate` copies that list verbatim. Without an entry there **and** a
  backfill migration for existing rows, no template-created agent can ever use
  it. *A capability nobody's `allowed_tools` contains is a capability nobody
  has.* Precedents: `043`, `081`.

**Migrations**
- Claim the next number: `ls apps/backend/migrations/control/ | tail -3`.
- Never edit an applied one. Forward-compatible only — `cmd/api` self-applies on
  boot during rolling deploys.
- A backfill's `down` is usually `SELECT 1;` — see `043` and `081` for why
  losing tenant intent is worse than doing nothing.
- **Tickets in this repo are often wrong about "Migration: none."** `T-W1` and
  `T-W2` both needed one. Check where the data actually lives before trusting
  the header — and if a column for the same idea already exists somewhere else,
  extend it rather than adding a second (`companies.default_currency`, not a new
  one on `company_profiles`).

**Generated artefacts** — never hand-edit; regenerate and commit the diff:
`make types` (Go structs → `packages/api-types`), `make openapi` (spec → SDKs,
Postman), `make tokens`.

## 5. Gate — and believe only what the gate says

```bash
export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin
gofmt -l apps/backend/internal apps/backend/cmd   # must print nothing
cd /path/to/repo && set -o pipefail && make check 2>&1 | tail -20; echo "MAKE EXIT: ${PIPESTATUS[0]}"
```

Three failure modes worth naming, all hit for real:

- **`make check | tail` reports `exit 0` even when `make` failed.** The status
  belongs to `tail`. Capture `PIPESTATUS[0]` or the run proves nothing.
- **`golangci-lint` fails on `gofmt` alone.** Run `gofmt -l` first; it is
  fifteen minutes cheaper than finding out at the end.
- **Do not edit any source file while the gate is running.** The result then
  covers a tree that no longer exists and has to be thrown away. Markdown is
  safe; Go, TS, YAML and SQL are not.

`make check` takes ~15 minutes, most of it the `agreement`/`pdf`/`pptx`/
`videoplan` race tests. Run it in the background and do documentation while you
wait.

## 6. Document — five surfaces, every time

| File | What goes in it |
| --- | --- |
| `docs/coverage/<track>.md` | The track's record. Create it if the track has none. |
| `docs/coverage/delivery-log.md` | A new `## Phase …` at the end, before *Feature velocity*. |
| `docs/coverage/feature-coverage.md` | The capability row: ✅ / 🟡 / ❌ with its evidence. |
| `docs/coverage/live-gate-backlog.md` | Every acceptance item you could not run, with what it needs and what it would prove. |
| `docs/plan/NN-*-roadmap.md` | The status block, and what the ticket got wrong. |

Plus `docs/README.md`'s directory map for a new coverage file, and
`apps/backend/openapi/v1.yaml` for anything under `/v1`.

**Write down where the ticket was wrong.** Both `T-W1` and `T-W2` had a false
"Migration: none"; `T-W2`'s quarters example could not hold for any single
instant. Those notes are worth more than the code to whoever reads this next.

**Record a prediction for every gate you file** — "no movement", "coverage is
high" — so it can be checked rather than remembered.

## 7. Report

Use `docs/AGENTS.md` §4's shape: `DONE / FILES / GATE / NOTES / RISK`. Paste the
gate's real output. If a gate could not run, say so and say why — never
substitute inspection.

Then offer the next pick and stop. Do not start a second ticket unasked.
