# Sprint 1 Tickets

Each ticket is an independently executable unit. Format is defined in
[`../agents/task-template.md`](../agents/task-template.md).

**App shorthand:** `BE` = `apps/backend/`, `FE` = `apps/dashboard/`,
`LP` = `apps/landing/`, `WID` = `apps/widget/`, `PKG` = `packages/`.
Single monorepo as of `T-00b` — a ticket spanning BE and FE is **one commit**.

**Migration numbers are pre-assigned.** The last applied migration is `020`.
Claim your number, do not renumber, and always write both `.up.sql` and
`.down.sql`.

| Ticket | Migration |
| ------ | --------- |
| T-05   | `021_agent_actions` |
| T-06   | `022_metric_definitions` |
| T-08   | `023_watchers` |
| T-10   | `024_actions` |
| T-13   | `025_api_keys` |
| T-15   | `026_outbound_webhooks` |
| T-04   | `027_user_invites` |
| T-19   | `028_embed_keys` |
| T-20   | `029_thread_embed` |
| T-R5   | `030_report_branding` |
| T-A1   | `031_api_channel` |
| T-A2   | `032_documents_api` |

---

## Execution order (revised 2026-07-28)

Week headings below are **thematic groupings, not the running order**. Three
things changed after they were written: the `T-00` smoke test found the agent
fabricating numbers (`../coverage/environment-notes.md` C-1) and recording no
usage for the primary model (C-2); the repo owner inserted the report track on
2026-07-27; and the owner made the tenant-facing API the sprint's highest
priority on 2026-07-28. This table is the authoritative order.

| Phase | Tickets | Days | Why here |
| ----- | ------- | ---- | -------- |
| 0 — done | `T-00`, `T-00b` | 2.0 | Re-warm, then monorepo. Both landed 2026-07-26. |
| 1 — done | ~~`T-01`~~, ~~`T-02c`~~, ~~`T-16`~~ | 6.0 | A branded PDF containing an invented figure is worse than an ugly one containing a real figure. Evals first because they are what proves the other two fixed anything. **All three landed 2026-07-27.** `T-01` baseline 96.8% → **97.0% (32/33) after `T-16`**, [`../coverage/eval-baseline.md`](../coverage/eval-baseline.md). `T-02c` — primary-model turns are billed, `T-03` unblocked. `T-16` — the `C-1` question now returns the true figure, and a turn that runs out of budget says so. |
| 1a — worth forwarding | ~~`T-R1`~~, ~~`T-R2`~~, ~~`T-R3`~~, `T-R4`→`T-R5` | 10.0 | Owner-set priority. The document is the artefact that leaves the building. **`T-R1` and `T-R2` landed 2026-07-27** — one `tokens.json` generates the dashboard's CSS variables and the backend's Go report theme ([`../coverage/design-tokens.md`](../coverage/design-tokens.md)), and the PDF renderer was rewritten against it: cover, running header, `Page N of M`, numbered sections, KPI cards, typed and locale-formatted cells, content-weighted columns ([`../coverage/report-rendering.md`](../coverage/report-rendering.md)). **`T-R3` landed 2026-07-28** — seven chart types on the token palette, which the colour-vision gate forced a change to ([`../coverage/report-charts.md`](../coverage/report-charts.md)). |
| 1b — safe to change | ~~`T-02`~~, `T-02b`, `T-03`, `T-04`, `T-05` | 8.0 | The rest of the foundation: CI gate, generated types, credit enforcement, RBAC, audit log. Not optional ahead of 1c — a public API is the first surface where an unaudited, unbounded, un-role-gated system is reachable by a script. **`T-02` landed 2026-07-28**: every CRITICAL package covered, `golangci-lint` at 0 issues, and the dashboard linted for the first time. It also found that non-UTC scheduled tasks cannot work in the deployed images ([`../coverage/test-coverage.md`](../coverage/test-coverage.md)). |
| 1c — callable | `T-13`, `T-A1`→`T-A5` | 12.5 | **Owner-set highest priority, 2026-07-28.** The tenant's own app asks Argentum for a report or an answer over HTTP. `T-13` moves here from week 5 — it is the prerequisite, not a week-5 nicety. |
| 2→6 | `T-06`→`T-12b`, `T-14`, `T-15`, `T-17`, `T-18` | 23.5 | Metric registry → watchers → actions → MCP → hardening. **Does not fit what is left of the sprint** — see the roll-up. |
| 7–8 | `T-19`→`T-23` | 11.5 | **Moved to Sprint 2 whole** — see `00-sprint-overview.md` §6. |

Two dependency notes for phase 1:

- `T-02c` carries `Deps: T-02`. That is ordering convenience, not a real
  dependency — its regression test is a fake LLM emitting a stream with and
  without usage, which needs none of `T-02`'s coverage sweep. It runs in phase 1.
- `T-16` is filed under week 6 below and stays there physically. It **runs in
  phase 1**, after `T-01`, because nothing that acts on its own (`T-08` watchers,
  `T-10` actions) should ship on top of an agent that invents figures.

Three ordering notes for 1a → 1b → 1c, decided 2026-07-28:

- **The report track finishes first** (owner's call). `T-R3` ✅ → `T-R4` →
  `T-R5`, 4.0 days left of 5.5. `T-A2` sells "ask our API for a PDF or an Excel file"; that pitch is
  materially better when the PDF has a chart and the deck exists, and `T-R4`
  is what puts `pptx` in `/v1/reports`'s format enum at all.
- **`T-R5` drags phase 1b forward whether or not the API exists.** It deps `T-04`,
  which deps `T-02`. So 4.5 days of "phase 1b" work is already embedded inside
  "finish the report track". The running order is therefore
  ~~`T-R3`~~ → `T-R4` → `T-02` → `T-04` → `T-R5` → `T-05` → `T-03` → `T-13` → `T-A1`…,
  not three clean blocks.
- **`T-13` is no longer a week-5 ticket.** Scoped API keys are the only machine
  authentication this product has, and every `/v1` route is behind them. It runs
  immediately before `T-A1`.

---

# Week 0 — Re-warm and consolidate

**Status: complete (2026-07-26).** Records in
[`../coverage/environment-notes.md`](../coverage/environment-notes.md) and
[`../coverage/migration-notes.md`](../coverage/migration-notes.md).

## T-00 · Environment re-warm
**Repo:** BE, FE · **Size:** 0.5d · **Deps:** none · **Priority:** P0

Nine weeks since the last commit. Prove the system still runs before changing it.

**Do:**
1. `docker-compose --profile dev up -d postgres postgres_demo redis metabase`
2. `go build ./...` — expect `go.mod` at 1.26.1 to pull a toolchain.
3. `go run ./cmd/api`, `go run ./cmd/worker` — migrations should self-apply.
4. `cd ../argentum-dashboard && pnpm install && pnpm dev` (still three separate
   repos at this point — `T-00b` consolidates them)
5. Sign up, add the demo DSN
   (`postgres://demo:demo@localhost:5433/demo_analytics?sslmode=disable`), ask
   one question end-to-end, confirm streaming works.
6. Record every breakage in `docs/coverage/environment-notes.md` (create it).

**Gate:** one screenshot or transcript of a successful streamed chat answer
against the demo tenant. If anything needed fixing, list the fix.

---

## T-00b · Consolidate into a monorepo
**Repo:** all · **Size:** 1.5d · **Deps:** T-00 · **Priority:** P0 · **Never cut**

Three repos, but every feature already ships as two commits in two of them
(`feb7a47`+`d11edef`, `17f81f5`+`135ca35`, `8cf653b`+`432d6f0`). That means no
atomic commit, no atomic revert, no CI that checks both halves agree, and — with
`T-19`→`T-23` adding a fourth repo — a widget forced to duplicate the dashboard's
chat components.

**Run this after `T-00` and before `T-01`.** Re-warm first so that if something
breaks you know whether it was nine weeks of drift or your own migration. Never
mid-sprint: a restructure in week 4 invalidates every in-flight agent's file paths.

### Target layout

```
argentum/                        ← single repo
├── apps/
│   ├── backend/                 from `argentum`
│   ├── dashboard/               from `argentum-dashboard`
│   ├── landing/                 from `argentum-landing`
│   └── widget/                  created later, in T-21
├── packages/
│   ├── api-types/               scaffolded here, filled by T-02b
│   └── chat-ui/                 created later, in T-21
├── docs/                        this documentation — now tracked
├── .github/workflows/           one pipeline, path-filtered
├── pnpm-workspace.yaml
├── Makefile
└── .gitignore                   union of the three, de-duplicated
```

### Do

**1. Create the repo with history preserved.**

```bash
cd /Users/rizkal/Work/smartsoft
git init argentum-mono && cd argentum-mono
git commit --allow-empty -m "chore: init monorepo"

git subtree add --prefix=apps/backend   ../argentum/argentum            main
git subtree add --prefix=apps/dashboard ../argentum/argentum-dashboard  main
git subtree add --prefix=apps/landing   ../argentum/argentum-landing    main
```

Then move `docs/` in, commit, and only once everything below passes, swap the
directory into place and archive the three originals read-only.

**2. Leave `go.mod` alone.** `apps/backend/go.mod` keeps
`module github.com/fauzanebd/argentum` despite living in a subdirectory. A Go
module path is a namespace, not a filesystem path, and nothing external imports
this module — so **zero import rewrites across ~120 files**. Do not "tidy" this.
Add a comment in `go.mod` explaining why, or the next person will fix it and cause
a 120-file diff.

**3. pnpm workspace.**
```yaml
# pnpm-workspace.yaml
packages:
  - 'apps/dashboard'
  - 'apps/landing'
  - 'apps/widget'
  - 'packages/*'
```
Keep each app's `package.json` and its own lockfile resolution; do **not** attempt
to unify React 18 (dashboard/landing) with Preact (widget). They are separate
workspace members for a reason.

**4. Top-level `Makefile`** — the single entry point agents and CI both use:
```make
dev-infra:  cd apps/backend && docker-compose --profile dev up -d postgres postgres_demo redis metabase
api:        cd apps/backend && go run ./cmd/api
worker:     cd apps/backend && go run ./cmd/worker
test:       cd apps/backend && go test -race ./...
vet:        cd apps/backend && go vet ./...
eval:       cd apps/backend && go run ./cmd/eval -set testdata/eval/golden.yaml
types:      # filled in by T-02b
web:        pnpm --filter dashboard dev
build:      cd apps/backend && go build ./... && pnpm -r build
```

**5. One CI workflow, path-filtered.** Replace the three pipelines with jobs
gated on `dorny/paths-filter`:

| Job | Fires on | Runs |
| --- | -------- | ---- |
| `backend` | `apps/backend/**` | vet, `test -race`, build api + worker + **discord** |
| `web` | `apps/{dashboard,landing,widget}/**`, `packages/**` | `pnpm -r build`, `pnpm -r lint` |
| `types` | `apps/backend/**` | `make types` then `git diff --exit-code packages/api-types` |
| `docker` | tags `v*.*.*` | build + push GHCR images, context `apps/backend` |

**Delete the current `paths:` filter on the whole workflow** — today a non-Go
change skips CI entirely (finding Q-3). Path filtering belongs per-job, not on the
trigger.

**6. Fix the Docker build context.** `Dockerfile.api`, `Dockerfile.worker`, and
`Dockerfile.discord` move to `apps/backend/`; the build context in CI becomes
`apps/backend`. Verify each image still builds — this is the step most likely to
silently break.

**7. Reconfigure Cloudflare Pages — the only step that can break production.**
For each of the two Pages projects, set:
- Root directory: `apps/dashboard` / `apps/landing`
- Build command: `pnpm install --frozen-lockfile && pnpm build`
- Output directory: `dist`

`apps/dashboard/functions/` (the SPA-fallback Pages middleware) must still be
picked up relative to the new root. **Deploy a preview branch and confirm before
pointing production at the monorepo.** You already spent four commits fighting
Pages (`a715171`→`9e9899f`); budget for that again.

**8. Settle the owner mismatch while you are here.** `go.mod` says `fauzanebd`,
GHCR and CI say `haritsrizkall`. Pick one for the new remote and note the decision
in `apps/backend/README.md`. (Keep the module path as-is regardless — see step 2.)

**9. Update `.gitignore`** as the de-duplicated union of the three, with paths
re-rooted: `apps/*/node_modules`, `apps/*/dist`, `apps/backend/.env`.

**10. Delete the stray** `apps/dashboard/scratch-chat-page-plan.md` (finding P-5)
— it is a one-line artifact and this is the natural moment.

### Notes for the implementer

- `git subtree add` produces a merge commit per app. **Know exactly what survives
  — verified, not assumed:**

  | Command | Works? |
  | ------- | ------ |
  | `git blame apps/backend/internal/app/chat_runner.go` | ✅ attributes to real pre-migration commits (`d782129`, `dcd0355`, `94fe370`, …) |
  | Original SHAs still resolve — `git show 3891579` | ✅ subtree does not rewrite commits |
  | `git log -- apps/backend/<path>` | ❌ shows only post-migration commits |
  | `git log --full-history -- <path-without-the-apps/backend-prefix>` | ✅ full pre-migration history |

  Old commits recorded old paths, so path-filtered `log` does not cross the merge.
  Blame does, because rename detection handles it. `--follow` does not help.

  **This is the right trade.** `git filter-repo --to-subdirectory-filter` would fix
  path-filtered `log`, but it rewrites every commit — so `3891579`, `d782129` and
  the ~20 other SHAs cited throughout `docs/research/` and `docs/coverage/` would
  cease to exist, and the archived originals would no longer correspond. Blame plus
  stable SHAs is worth more than `log`-by-path, which has a one-flag workaround.
- Do **not** delete the three original repos. Archive them read-only on the remote.
  They are the fallback if a deploy reconfiguration goes wrong.
- Do not attempt Turborepo or Nx yet. Two frontends and one Go module do not need a
  build orchestrator; `pnpm -r` plus the Makefile is enough. Revisit if
  `packages/` exceeds four members.

### Acceptance
- [x] One repo, all three histories in the graph — 75 commits
- [x] `git blame apps/backend/internal/app/chat_runner.go` reaches `d782129`; dashboard blame reaches `0687da5`
- [x] Original SHAs still resolve, so every citation in `docs/` stays valid
- [x] `cd apps/backend && go build ./... && go vet ./... && go test ./...` — identical to the `T-00` baseline (build OK, vet clean, same 3 passing packages)
- [x] **Zero changes to any Go import path** — tree diff vs. the original shows no `.go` differences at all
- [x] `pnpm -r build` builds dashboard and landing
- [x] `pnpm -r lint` passes (and now actually runs — see Q-11)
- [ ] All three Docker images build from the new context — **UNVERIFIED, Docker was not running**
- [ ] Cloudflare Pages preview deploys succeed for both frontends **before** production is repointed
- [ ] CI: a docs-only change runs no app jobs; a backend-only change runs `backend` but not `web`
- [x] `cmd/discord` builds in CI (it never did before)
- [x] `docs/` is tracked — both workspace `docs/` and the recovered `apps/backend/docs/`

### Gate

Paste: (a) blame output proving history survived for one file per app, (b) the full
backend build/vet/test output, (c) a tree diff vs. the originals showing no `.go`
changes, (d) Cloudflare preview URLs for both frontends, (e) a CI run showing
correct per-job path filtering.

### Status — 2026-07-26

Local migration **complete** at `/Users/rizkal/Work/smartsoft/argentum-mono`,
commits `eef3cb5` (migration) and the lint fix on top. All local gates green. The
three original repos are untouched.

Outstanding, and each needs a human:
1. **Docker image builds** — Docker Desktop was not running; the three Dockerfiles
   are unverified against the `apps/backend` context.
2. **Cloudflare Pages** — two projects need their root directory and build command
   repointed, verified on a preview branch first.
3. **Remote** — `git remote add` needs an owner decision (`fauzanebd` vs
   `haritsrizkall`) and a repo name.
4. **Directory swap** — move `argentum-mono` into place and archive the originals.

See `docs/coverage/migration-notes.md` for the exact steps.

### Out of scope
- `packages/api-types` contents — scaffold the directory only; `T-02b` fills it
- `packages/chat-ui` — created in `T-21` when the widget needs it
- Turborepo / Nx / remote caching
- Renaming the Go module path

---

# Week 1a — Reports that look enterprise

**Priority insert, added 2026-07-27 at the repo owner's request.** Runs ahead of
the remaining foundation work (`T-02`, `T-02b`, `T-03`, `T-04`, `T-05`), and
after `T-00b` for the same reason as everything else: the monorepo moves every
path, and this track creates a new shared package plus files in two apps.

**Amended after the `T-00` smoke test.** The insert originally ran immediately
after the re-warm. Phase 1 (`T-01`, `T-02c`, `T-16`) now goes first: the smoke
test caught the agent inventing a sales figure under budget exhaustion, and a
branded, board-ready document carrying an invented number is a worse artefact
than an unbranded one carrying a real one. Six days ahead of this track, not a
demotion of it.

## Why this is here

`generate_document` already produces PDF, XLSX and CSV
(`internal/tools/document/`), and the PDF is a stock maroto document: default
Helvetica, no cover, no header, no footer, no page numbers, no logo, no charts,
no locale-aware number formatting, tables with no rules or alignment. Column
widths come from `splitGrid` dividing 12 evenly.

That artifact is the one thing a customer forwards to someone who never logs in.
It is the product's most-shared surface and currently the least designed one. A
correct number in an unbranded document reads as a prototype.

The track also compounds with the rest of the sprint: once watchers (`T-08`) and
`send_message` (`T-12a`) exist, "a branded weekly deck lands in Lark every Monday"
is a configuration, not a feature.

## Decisions (locked — do not re-litigate inside the tickets)

1. **Renderer stays maroto (v2.4.0) in Go. Headless Chromium is rejected.**
   Rendering HTML with the dashboard's real CSS would give perfect fidelity and
   costs a ~300 MB Chromium layer in the worker image, a browser sandbox to
   secure, and roughly a second per document. Tokens shared through codegen get
   most of the fidelity for none of that. **Revisit trigger:** a layout the grid
   genuinely cannot express — not "this took longer than expected".
2. **One spec, many formats.** PDF and PPTX render from the same `Spec`. A deck
   is not a second content model; it is a second projection. Anything that
   requires per-format authoring is a design mistake in the spec.
3. **Design tokens are generated, never hand-copied.** One `tokens.json`
   produces the dashboard's CSS variables and the backend's Go theme. CI fails on
   drift, exactly like `T-02b` does for API types.
4. **PPTX is hand-rolled OOXML from committed templates.** `unioffice` is
   commercially licensed and its open fork is unmaintained. A `.pptx` is a zip of
   XML; with a fixed layout set, `text/template` + `archive/zip` is smaller,
   deterministic, and has no license exposure.
5. **Charts are rendered as images, in Go.** Same image goes into the PDF and the
   deck. Natively editable OOXML charts are deferred — see `backlog.md`.

---

## ~~T-R1~~ · Report design tokens + theme package — **DONE 2026-07-27**
**Repo:** PKG, FE, BE · **Size:** 1.5d · **Deps:** T-00b · **Priority:** P0 · **Never cut**

The plumbing that makes "same design system" true by construction instead of by
discipline.

**Landed.** Record, with every gate pasted:
[`../coverage/design-tokens.md`](../coverage/design-tokens.md).

**Do:**
- `packages/design-tokens/tokens.json` — canonical source. Seed it from the
  values already in `apps/dashboard/src/index.css`, converted to hex:

  | Token | Value | Role |
  | ----- | ----- | ---- |
  | `color.background` | `#F5F5F0` | page cream |
  | `color.surface` | `#FFFFFF` | cards, table bands |
  | `color.primary` | `#F25C5C` | accent, rules, chart series 1 |
  | `color.foreground` | `#0A0A0A` | body text |
  | `color.muted` | `#6B6B6B` | captions, footers, axis labels |
  | `color.border` | `#E2E2DC` | hairlines, table rules |
  | `radius.base` | `12px` (0.75rem) | cards, callouts |
  | `font.display` | Space Grotesk | titles, headings |
  | `font.body` | Space Grotesk | body |

  Plus a type scale (display 24 / h1 16 / h2 13 / body 10 / caption 8 pt in
  print units), a spacing scale in mm, and the categorical chart palette from
  `T-R3`.
- Two generators, run by `make tokens`:
  - `→ apps/dashboard/src/tokens.generated.css` — the shadcn HSL custom
    properties. `index.css` imports it; the hand-written `:root` block for the
    tokens listed above is deleted, not left as a duplicate.
  - `→ apps/backend/internal/report/theme/tokens_gen.go` — typed Go constants
    (`theme.ColorPrimary`, `theme.FontDisplay`, `theme.TypeScale.H1`, …).
- Generated output is **committed**. CI job `tokens`: run `make tokens`, then
  `git diff --exit-code packages/design-tokens apps/dashboard/src/tokens.generated.css apps/backend/internal/report/theme`.
  Follow the pattern `T-02b` establishes for API types — same job shape, same
  failure mode.
- **Vendor the fonts.** The dashboard pulls Space Grotesk from the Google Fonts
  CDN; a Go renderer cannot. Commit the Space Grotesk TTFs (Regular, Medium,
  Bold) under `internal/report/theme/fonts/` with their **OFL license file**,
  embed with `go:embed`, and register them with maroto's font repository at
  renderer construction. Fail loudly at startup if a face is missing — a silent
  fallback to Helvetica is exactly the regression this track exists to remove.
- `internal/report/theme` also exposes the derived print constants: A4, 18 mm
  margins, table row heights, hairline width.

**Notes for the implementer:**
- Dark mode is out of scope. Documents are printed and forwarded; they are
  light-only, and the dark palette has no meaning on paper.
- Do not let the generator emit Tailwind classes. It emits variables and Go
  constants; the consumers decide how to use them.

**Acceptance:**
- [x] `make tokens` regenerates both outputs; a hand edit to either is reverted by it — and `go test` fails on the hand edit before that, because `TestGeneratedTokensMatchSource` reads `tokens.json`
- [x] CI fails when `tokens.json` changes without regeneration — proved with `color.primary` → `#0000FF`, exit 1, then clean after `make tokens`
- [ ] ~~Dashboard renders identically before and after the CSS migration (visual diff on two screens)~~ **Substituted, see below.** Every one of the 27 light variables was compared numerically: 19 are bit-identical, 8 move by ≤ 4/255 because the old hand-written HSL was a rounded approximation of the brand hex the comment beside it named. Screenshots could not be captured — headless Chrome on this machine is intercepted by something outside this repo (`curl` reaches the dev server; Chrome gets an "Authentication Required — Hamilton portal" page, with a clean profile too).
- [x] Backend registers all three font faces (six registrations: two families × normal/bold, plus italic aliases because Space Grotesk has none and gofpdf errors on an unregistered style); a deliberately removed TTF fails **at compile time**, which is louder than the startup check asked for. `theme.VerifyFonts()` in `bootstrap.New` covers what `go:embed` cannot see — a file that exists but is not a font.
- [x] License file committed alongside the fonts

**Gate:** paste the CI failure from a deliberate token change, then the pass after
`make tokens`. Plus before/after dashboard screenshots showing no visual change.

**Gate met**, except the screenshots — all of it in
[`../coverage/design-tokens.md`](../coverage/design-tokens.md). Worth carrying
forward:

- **The whole light palette moved into `tokens.json`, not just the nine listed
  tokens.** A generated `:root` beside a hand-written one keeps the duplication
  this ticket exists to remove. shadcn's *variable names* stay in the CSS
  generator, where a consumer's naming belongs.
- **`.dark` had to leave `@layer base`.** Unlayered beats layered regardless of
  specificity, so a layered `.dark` under an unlayered generated `:root` would
  have silently disabled dark mode. Both are unlayered now.
- **A themed PDF is 34.5 KB where the stock one was 1.6 KB** — six embedded
  faces. That is the cost of a document that renders the same on a machine that
  has never heard of Space Grotesk, and it is why `T-R2`'s renderer should not
  embed more families than it uses.
- **`T-R3` inherits an unverified palette.** Eight colours on a CIE L\* ladder
  (min pairwise gap 6.5 L\*, method recorded in `tokens.json`), but the
  colour-vision simulation and greyscale contact sheet are still that ticket's
  gate item. **It was right to flag: `T-R3` ran the simulation and the palette
  failed it.** Series 8's green sat ΔE 5.0 from the brand red under
  deuteranopia; it is now `#5CA8E0` and `make palette` is a CI gate. The ladder's
  tightest greyscale pair is 6.8 L\* after the change.

---

## T-R2 · PDF renderer v2 — enterprise document layout
**Repo:** BE · **Size:** 3d · **Deps:** T-R1 · **Priority:** P0 · **Never cut**

Rewrite `internal/tools/document/render_pdf.go` into `internal/report/pdf/`
against the theme package. This is the ticket that changes what the customer
sees.

**Spec v2** — additive, in `internal/report/spec/`. `spec_version: 2` opts in;
absent or `1` renders through a shim so existing eval cases and any prompt in the
wild keep working:

| Section | Payload | Renders as |
| ------- | ------- | ---------- |
| `cover` | `{title, subtitle, period, prepared_for, prepared_by, confidentiality}` | Full cover page |
| `heading` | `{text, level: 1\|2}` | Numbered section heading with a primary-colour rule |
| `paragraph` | `{text}` | Justified body copy |
| `kpi_row` | `{items:[{label, value, delta_pct, direction, fmt}]}` | 2–4 KPI cards, delta arrow coloured by `higher_is_better` |
| `table` | `{columns:[{label, fmt, align, width_weight}], rows, total_row}` | Ruled table, zebra bands, repeating header |
| `callout` | `{tone: info\|warn\|good, title, text}` | Tinted rounded box |
| `key_value` | `{items:[{k,v}]}` | Two-column label/value block |
| `chart` | see `T-R3` | Chart image, captioned |
| `footnote` | `{text}` | Small muted text, source/methodology line |
| `page_break` | — | Hard break |

**Do:**
- **Cover page:** tenant logo (from `T-R5`; Argentum mark until then), document
  title, period covered, generated-at, prepared-by, confidentiality label.
- **Running header** from page 2: small logo left, document title right,
  hairline rule under. **Footer:** `Page N of M`, generated-at, confidentiality
  label. maroto v2.4.0 has `RegisterHeader`/`RegisterFooter` and page numbering
  — verify both against the pinned version before designing around them; if the
  page **total** is not available, render twice and inject it on the second pass
  rather than shipping "Page 3" with no denominator.
- **Typed cells.** A cell becomes `{v, fmt}` where `fmt` ∈
  `text|number|currency|percent|date`, with a column-level default. The
  **renderer** formats and aligns — numerics right-aligned, decimals consistent
  down a column, currency symbol from company settings. Plain strings still
  accepted and rendered as `text`. This is most of what separates a professional
  table from a dumped one, and it removes a job the LLM was doing inconsistently.
- **Locale-aware formatting** in `internal/report/format`, `id` and `en`:
  `Rp 1.234.567` / `$1,234,567`, magnitude words (Juta / Miliar / Triliun) above
  a configurable threshold, `27 Juli 2026` / `27 July 2026`. Share this package
  with the eval harness's numeric comparator (`T-01`) — one parser, one
  formatter, no second opinion about what "1,2 Juta" means.
- **Column widths from content, not `splitGrid`.** Weight by header length and a
  sample of cell widths, clamp to min/max, then normalise to the 12-column grid.
  An 8-column table with one long text column currently renders unreadably.
- **Table paging:** header row repeats on every page; never leave a header row
  alone at the bottom of a page; never break a `kpi_row` or `callout` across pages.
- **PDF metadata:** title, author (company legal name), subject, creator
  `Argentum`, creation date. Plus `generated_at` accepted as an explicit spec
  field so golden tests are byte-stable.
- Delete the old `render_pdf.go` once `render_test.go`'s cases pass against the
  new path. Do not leave two renderers.

**Acceptance:**
- [x] Cover, running header, footer with `Page N of M` all render — and in the document's locale: an `id` report says `Halaman 2 dari 17`
- [x] A 200-row table pages correctly with a repeating header and no orphaned header — 17 pages, header on 16 (page 1 is the cover), asserted structurally
- [x] Numeric columns right-aligned with consistent decimals; `id` locale renders rupiah correctly
- [x] v1 specs still render (shim proven by the existing tests, unmodified) — `internal/tools/document/render_test.go` is byte-for-byte unchanged and passes against the new renderer
- [x] Two runs with the same spec and a fixed `generated_at` produce identical bytes — needed `gofpdf.SetDefaultCatalogSort(true)`, see below
- [x] `pdfcpu validate` passes on every fixture — in-process via `pdfcpu/pkg/api`, relaxed mode, which is what the CLI defaults to
- [x] No Helvetica anywhere in the output — fonts are the embedded faces

**Gate:** render four fixtures — monthly sales report, invoice, KPI summary,
200-row export — and attach a PNG of page 1 and one interior page of each. Paste
`pdfcpu validate` output and the byte-identical rerun hash.

**Gate met 2026-07-27.** Output, page counts and hashes in
[`../coverage/report-rendering.md`](../coverage/report-rendering.md). Worth
carrying forward:

- **The v1 shim is two `UnmarshalJSON` methods, not a translation layer.**
  `spec.Column` and `spec.Cell` each accept the v1 and v2 shapes, so a v1
  payload *is* a v2 document whose cells are all text. `spec_version` only
  decides what the renderer offers — a spec that has been producing a plain
  document for three months keeps producing one.
- **The cover's clean page is an ordering constraint, not a flag.** maroto's
  `RegisterHeader` adds header rows to whichever page is current, so there is no
  "from page 2". The cover is drawn, the page is flushed with an empty
  `AddPages`, then the footer and header are registered — footer first, because
  `RegisterHeader`'s fit check reads the footer height.
- **Layout needs text metrics before maroto exists.** `measure.go` keeps a
  second gofpdf document that draws nothing, built the way maroto builds its
  own, and transcribes maroto's unexported line-breaking function. An
  approximation here is a row that clips its own text.
- **Byte-stability needed two globals, and CI found the second one.** gofpdf
  writes its font catalogue in Go map order, so the same spec rendered twice
  produced identical pages with the font objects renumbered —
  `gofpdf.SetDefaultCatalogSort(true)`. It also writes `/ModDate` from the wall
  clock, which maroto's config cannot set, so two renders matched inside a
  second and differed across one. Six local runs passed; the first CI run failed
  on two of four fixtures. The test now asserts both timestamps equal
  `generated_at` rather than comparing two renders and hoping they straddle a
  second.
- **The 200-row fixture found four layout bugs the other three could not** —
  unbreakable tokens overflowing their column, a stride sampler that measured
  the wrong rows, a grid distribution biased against wide columns, and rounding
  that clipped cells by under a millimetre. All four are recorded with their
  causes in the coverage note. It also found that the fixture's own hand-rolled
  LCG produced correlated data, which had nearly masked the second one.
- **`internal/report/format` was wrong in both directions and neither would have
  shown up in a document.** Compact form honoured rupiah's zero decimal places
  and rendered 3,863,405,700 as `Rp 4 Miliar`; `Parse` could not read the
  `-Rp 1.234` the formatter had just written, because the minus sits in front of
  the symbol. The second one is precisely the failure the shared package exists
  to prevent — `T-01`'s comparator and this renderer disagreeing about what a
  number is — and it was there from the first commit of the formatting
  direction. A round-trip test over every locale × currency × compact
  combination now pins it.
- **Eight columns of long text do not fit on A4 and the renderer does not
  pretend.** Numbers, dates and unbreakable keys are served first and stay
  intact; prose columns truncate visibly. `T-R4` inherits the same problem with
  less room, so the character-budget approach in that ticket should start from
  `fitText` rather than from scratch.

---

## ~~T-R3~~ · Chart images for documents and decks — **DONE 2026-07-28**
**Repo:** BE · **Size:** 1.5d · **Deps:** T-R2 · **Priority:** P1 · **Cut: types only, never the ticket**

**Shipped.** Record: [`../coverage/report-charts.md`](../coverage/report-charts.md).
Gate artifacts: [`../coverage/assets/chart-contact-sheet.png`](../coverage/assets/chart-contact-sheet.png)
and [`../coverage/assets/chart-contact-sheet-greyscale.png`](../coverage/assets/chart-contact-sheet-greyscale.png).

Three things came out of it that the ticket did not anticipate:

- **The palette failed its own gate.** Writing the verifier the ticket asked for
  found series 8's green sitting ΔE 5.0 from the brand red under deuteranopia,
  and no green at any lightness clears both floors against this palette. It is
  now `#5CA8E0` azure. `make palette` is the gate, and CI runs it.
- **Semantic colour left the series ramp.** `ChartPalette[7] // green` was the
  colour of a good KPI delta and would have turned blue with the palette. Three
  print-scoped tokens — `positive`, `warning`, `info` — now carry meaning, and
  the categorical ramp carries only separability.
- **The category cap does not apply to line charts,** which is a deliberate
  departure from the ticket. Bucketing the smallest points of a time series puts
  an invented point on a real timeline; the reasoning is in the record.

Also: bar charts are forced to a zero baseline, and chart titles and captions are
document text rather than pixels — which is what lets `T-R4` set the same words
at slide scale over the identical image.

A report without a chart is a table with a cover page.

**Do:**
- `internal/report/chart` — pure-Go PNG rendering, no CGO, no headless browser.
  Evaluate `github.com/go-analyze/charts` first (maintained successor to
  `vicanso/go-charts`, echarts-style API, themeable, PNG + SVG); fall back to
  `github.com/wcharczuk/go-chart/v2` if it cannot express the set below. State
  which you picked and why in the report.
- Types: `line` (time series), `bar`, `grouped_bar`, `stacked_bar`, `pie`,
  `donut`, `sparkline` (for KPI cards).
- **Categorical palette lives in `tokens.json`** (`T-R1`), anchored on the brand
  red `#F25C5C` and extended with hues that stay distinguishable under
  deuteranopia and in greyscale — enterprise reports get printed in black and
  white more often than anyone admits. Verify both, state the method.
- Render at 3× and downscale, so a chart is sharp at print resolution rather
  than a blurry screen-DPI bitmap.
- Axis labels, tick values, and legends format through
  `internal/report/format` from `T-R2` — the axis and the table beneath it must
  agree on what a rupiah looks like.
- Explicit **no-data** and **single-point** states. A silently empty chart area
  is worse than a "no data for this period" caption.
- Cap series (default 8) and categories (default 40); above that, render top-N
  plus an "other" bucket and say so in the caption.
- `chart` section payload: `{chart_type, title, caption, categories, series:[{name, values}], y_fmt, stacked, height_mm}`.

**Acceptance:**
- [x] All seven types render from a fixture spec
- [x] Same chart embedded in a PDF and a PPTX is the same image, generated once — `chart.Render` is the single producer and is deterministic; the PDF consumes it now and `T-R4` consumes the same bytes
- [x] Palette verified colourblind-safe and greyscale-distinguishable — method stated, gated in CI, and it failed first (see the record)
- [x] Empty and single-point series render their explicit state, not a blank box
- [x] Deterministic output: same input → same PNG bytes

**Gate:** a contact sheet PNG showing all seven types with the brand palette, plus
the greyscale conversion of the same sheet. **Both committed under
`docs/coverage/assets/`, regenerated by `TestContactSheet`.**

---

## T-R4 · PPTX deck renderer
**Repo:** BE · **Size:** 2.5d · **Deps:** T-R2, T-R3 · **Priority:** P0 · **Never cut**
**New format:** `pptx`

Same spec, projected onto slides. A deck is what gets presented in the meeting
the PDF was attached to.

**Do:**
- `internal/report/pptx` — build the OOXML package directly: `archive/zip` +
  `text/template` over committed part templates (`[Content_Types].xml`,
  `_rels/`, `ppt/presentation.xml`, slide masters, layouts, `ppt/media/`).
  16:9, 12192000 × 6858000 EMU.
- Layout set, mapped from spec sections:

  | Spec | Slide |
  | ---- | ----- |
  | `cover` | Title slide — logo, title, period, prepared-for |
  | `heading` level 1 | Section divider |
  | `kpi_row` | KPI slide, 2–4 stat tiles |
  | `chart` | Chart slide, title + caption |
  | `table` | Table slide; over ~12 rows, continue onto `(cont.)` slides |
  | `paragraph` / `callout` | Bullet or callout slide, chunked to fit |
  | end of deck | Closing slide with the confidentiality label |
- **Narrative goes in speaker notes.** The agent's prose explanation belongs in
  `notesSlide`, not crammed onto the slide. This is the single change that makes
  a generated deck feel authored rather than dumped.
- Text fitting is estimated, not measured — no layout engine here. Budget
  characters per layout from the type scale, and overflow to a continuation
  slide. Silent clipping is a bug; a `(cont.)` slide is not.
- Fonts by name (`Space Grotesk`) with a declared fallback chain, since the
  recipient's machine may not have it. Do **not** attempt OOXML font embedding —
  it only works in PowerPoint on Windows and doubles the file size.
- `domain.DocumentFormat`: add `DocumentFormatPPTX`, extension `pptx`,
  content type
  `application/vnd.openxmlformats-officedocument.presentationml.presentation`.
  Update `Valid()`, `Extension()`, `ContentType()`, the tool's `format` enum, the
  `Spec.Validate()` switch, and the tool description's format-picking guidance
  ("pptx for anything that will be presented — reviews, board updates, weekly
  readouts").
- CI smoke test: `libreoffice --headless --convert-to pdf` on every fixture deck.
  A deck that LibreOffice refuses is a deck PowerPoint may also refuse.

**Acceptance:**
- [x] ~~A deck opens cleanly in **PowerPoint, Keynote, Google Slides, and LibreOffice**~~ — **partly.** LibreOffice 7.4.7.2 converts all five fixtures, in CI and locally. The other three cannot be driven from a headless environment and have **not** been opened. See `coverage/report-deck.md` § Not yet verified.
- [x] Same spec renders as both PDF and PPTX with no format-specific authoring — the deck's tests read `../pdf/testdata/*.json` and change only `format`
- [x] Speaker notes carry the narrative — the paragraph lands whole in `notesSlide`, its lead sentence on the slide; asserted in both directions
- [x] Long tables continue across slides; nothing is silently clipped — 200 rows across 50 table slides, all 200 present, total row on the last only; every placed string asserted against its box
- [x] Charts appear at slide resolution without visible artefacts — rasterised at the 290.7mm slide measure, which needed `chart.maxWidthMM` raised from its A4-shaped 200
- [x] Zip is deterministic (fixed entry order, fixed timestamps)

**Gate:** one deck rendered from the monthly-sales fixture, with screenshots from
all four applications and the LibreOffice conversion output.
**Met for LibreOffice** (output in `coverage/report-deck.md`); the three
remaining applications are outstanding and are the only open item on this
ticket.

---

## T-R5 · Tenant report branding + dashboard configuration
**Repo:** BE, FE · **Size:** 1.5d · **Deps:** T-R2, T-04 · **Priority:** P1 · **Cut #6**
**Migration:** `030_report_branding`

Argentum's palette is the default. A customer sending a report to *their* board
wants their own mark on it.

**Do:**
- Migration `030_report_branding`: `companies.report_branding jsonb` —
  `{logo_key, primary_color, footer_text, legal_name, locale, confidentiality_label, show_argentum_credit}`.
  Empty means Argentum defaults; the renderer must never require a branding row
  to exist.
- Logo upload to the existing MinIO/S3 service under
  `branding/{company_id}/logo.{ext}` — reuse `storage.UploadKey`. PNG or JPEG,
  ≤512 KB, ≤2000 px on the long edge, re-encoded server-side (this strips EXIF
  and neutralises a malformed-image payload in one step). Not SVG: an SVG in a
  document renderer is a script-injection surface for no benefit here.
- **Validate `primary_color` for contrast** — reject anything below 3:1 against
  white, with a message naming the measured ratio. A customer picking pale
  yellow produces an unreadable report and blames the product.
- `GET|PUT /api/reports/branding`, admin-only via `T-04`'s `AdminOnly()`.
- `POST /api/reports/preview` → renders a fixed sample report with the submitted
  branding and returns the PDF. Dashboard shows it in an `<iframe>`; no PDF.js
  dependency, no second rendering path.
- FE: Settings → **Reports** tab. Logo upload with preview, colour picker with
  the live contrast readout, footer text, legal name, default locale,
  confidentiality label, and the live preview pane.
- Renderer resolves branding once per document and falls back per field, not
  per object — a tenant with a logo but no custom colour gets their logo and
  Argentum's red.

**Acceptance:**
- [ ] Branding change appears in the next generated PDF and PPTX with no redeploy
- [ ] A low-contrast colour is rejected with the measured ratio in the message
- [ ] Oversized or non-image upload rejected; uploaded images are re-encoded
- [ ] A company with no branding row renders the Argentum default, never an error
- [ ] Non-admin gets 403 on both routes
- [ ] `pnpm build` clean

**Gate:** screenshots of the Reports tab, the live preview, and the same report
generated from chat afterwards carrying the branding. Plus the rejection message
for a low-contrast colour.

---

# Week 1c — Callable: the tenant-facing API

**Priority insert, added 2026-07-28 at the repo owner's request, and the highest
priority item in the sprint.** Runs after the report track (`T-R3`→`T-R5`) and
after the foundation it cannot ship without (`T-02`, `T-04`, `T-05`, `T-03`,
`T-13`).

## Why this is here

The customer already has an application. Today the only way a report leaves
Argentum is a human opening the dashboard, asking in chat, and clicking a link.
That makes Argentum a destination. An API makes it a component: their nightly job
asks for the monthly deck, their admin panel grows a "Download report" button,
their internal tool asks a question inline and renders the answer in their own UI.

This is the same strategic bet as the widget and MCP — make Argentum reachable
from outside its own dashboard — aimed at a different consumer. The widget puts
our UI inside their page and needs their frontend team. **The API puts our output
inside their product and needs only a backend developer with a key.** For the
thing the owner actually asked for — "get a generated report (PDF or Excel) from
Argentum" — the request originates on a server, not in a browser, so this is the
surface that fits it.

`api-surface.md` observation 3 states the blocker plainly: **no machine
authentication exists.** All 61 routes require a human-session JWT. Nothing can
integrate with Argentum today at all.

## Decisions (locked — do not re-litigate inside the tickets)

1. **`/v1` is a separate namespace from `/api`, with a separate contract.**
   `/api/*` is the dashboard's private surface: JWT, unversioned, free to change
   whenever the dashboard changes. `/v1/*` is a public promise: API key,
   additive-only, breaking changes require `/v2`. A dashboard JWT must not
   authenticate a `/v1` route and an API key must not authenticate an `/api`
   route — not as a policy, as middleware.
2. **Reports get two doors, not one endpoint with a mode flag.**
   `POST /v1/reports/render` takes a spec and returns a file: no LLM, no thread,
   sub-second, cost of a render. `POST /v1/reports` takes a prompt and runs a real
   agent turn: seconds to minutes, bills tokens, can fail in ways a renderer
   cannot. Different latency, different cost, different failure modes, different
   error handling in the caller. One endpoint doing both would need a flag that
   changes everything about the response, which is two endpoints wearing a coat.
3. **Streaming is SSE, not WebSocket.** The consumer is a server. Every HTTP
   library, proxy and load balancer handles SSE; a WebSocket client in a backend
   is an extra dependency and a reconnect state machine the integrator has to
   write. The dashboard keeps its WebSocket — this adds a second reader of the
   same Redis pubsub, not a second event pipeline.
4. **No second implementation of anything.** `/v1` handlers call
   `internal/tools/document` and `internal/app`. Same hard rule `T-14` carries for
   MCP: any divergence between the API surface and the agent surface is a bug, and
   a PR that copies renderer or tool logic is rejected.
5. **The OpenAPI spec is the contract, and CI enforces it both ways.** A route
   without a spec entry fails the build; a spec entry without a route fails the
   build. Exactly the guard shape `T-R1` used for token drift — which has already
   caught a real drift in this repo, so it is a proven mechanism here rather than
   a hopeful one.

---

## T-A1 · `/v1` foundation: envelope, auth, idempotency, limits, `api` channel
**Repo:** BE · **Size:** 2.5d · **Deps:** T-13, T-05, T-03 · **Priority:** P0 · **Never cut**
**Migration:** `031_api_channel`

### Why

Everything else in this track is a route. This is the shape every route shares,
and it is the part that cannot be retrofitted: an error format, an idempotency
contract and a pagination style become permanent the first time a customer writes
code against them. Get it wrong and the fix is `/v2`.

It also owns the `api` channel, which is **not** where the first draft of this
plan put it. `T-A3` (chat) obviously needs a channel, but so does `T-A2`'s
agentic report door — it runs a real turn through `ChatEnqueuer`, and a turn
needs a `domain.Channel`. Leaving the channel in `T-A3` while scheduling `T-A2`
first is a dependency inversion that would have surfaced as a compile error on
day one of the flagship ticket. Both consumers need it, so it lands here.

### Do

- `cmd/api/router.go`: `v1 := r.Group("/v1")` with `middleware.APIKeyAuth()`
  (from `T-13`), `middleware.RequestID()`, and a per-key rate limiter. **Never**
  `middleware.Auth`.
- Error envelope in `internal/transport/http/apierr`:
  ```json
  {"error":{"type":"invalid_request","code":"spec_too_large",
            "message":"…","param":"rows","request_id":"req_…"}}
  ```
  `type` ∈ `invalid_request | authentication | permission | not_found |
  rate_limit | budget_exhausted | server`, mapped to status codes in one table.
  Every `/v1` handler returns through it. A raw `gin.H{"error": err.Error()}`
  inside `/v1` is a review finding — it leaks internals and it is unparseable by
  a client.
- `X-Request-Id` on every response, generated when the caller sends none, carried
  into the log fields and into the `T-05` audit row. A support conversation starts
  with a request id, so a request id has to resolve to a row.
- **Idempotency.** `Idempotency-Key` required on `POST /v1/reports`,
  `POST /v1/reports/render` and `POST /v1/chat`, accepted everywhere else. Redis
  `idem:{company_id}:{key}` for 24h. **The same key with a different request body
  returns 409** — the case that catches a broken retry loop before it bills twice.
  Three sub-cases the naive "store the response body" design gets wrong, all of
  which occur in this API:
  - **Never store the payload.** `POST /v1/reports/render` with
    `Accept: application/pdf` returns megabytes; caching that per key for 24h
    turns Redis into a document store. The record holds the **document id and
    status**; a replay re-reads from object storage and re-presigns, then returns
    the identical logical response.
  - **A streamed response cannot be replayed from a record.** A replayed
    `POST /v1/chat` with `Accept: text/event-stream` attaches to the existing
    turn's pubsub channel and replays persisted events; if the turn has already
    finished it returns the `final` event alone.
  - **A replay while the original is still in flight** returns
    `409 request_in_flight` with the `thread_id`/`report_id`, not a duplicate
    turn. This is the common case — it is what a client timeout plus a retry
    looks like.
- Cursor pagination everywhere a list is returned:
  `{"data":[…],"has_more":true,"next_cursor":"…"}`, cursor an opaque base64 of
  `(created_at, id)`. Never offset: rows arrive while a caller pages.
- `RateLimit-Limit` / `RateLimit-Remaining` / `RateLimit-Reset` on every response;
  429 carries `Retry-After`. Separate Redis bucket from the user limiter.
- `GET /v1/me` — company id and name, key name, scopes, rate limit, credit
  balance, API version. The first call an integrator makes and the one paste that
  makes a support question answerable.
- Config: `API_V1_ENABLED` (kill switch), `API_V1_RATE_PER_MIN` (default 120),
  `API_V1_SYNC_TIMEOUT_SECONDS` (default 120), `API_V1_MAX_BODY_BYTES`
  (default 1 MiB).
- **CORS: `/v1` emits no permissive CORS headers.** An API key in a browser is a
  leaked API key. The browser path is `T-19`'s embed key, and conflating them is
  how a secret ends up in a bundle.
- **`domain.ChannelAPI Channel = "api"`** and migration `031_api_channel`, both
  needed by `T-A2` and `T-A3`:
  - `conversation_threads.api_user_ref text`, unique index on
    `(company_id, api_user_ref, id)`.
  - `api_user_ref` added to the `by-user` rollup at
    `internal/adapters/postgres/usage_repo.go:331` as a **fifth** `user_key_kind`
    — the query coalesces `user_id / phone_number / discord_user_id /
    lark_open_id` today and the `CASE` at line 346 must gain the matching arm.
  - `ThreadRepository.LatestForAPIUser(ctx, companyID, apiUserRef)` alongside the
    four `LatestFor…` methods already on the interface
    (`internal/domain/thread.go:41`), plus `APIUserRef` on
    `domain.ConversationThread`.
  - Follow [`../agents/playbooks/add-channel.md`](../agents/playbooks/add-channel.md)
    and **grep every switch on `Channel`**: `ChatRunner.completeWith` (no outbound
    provider — delivery is the HTTP response, a deliberate no-op **with a comment
    saying so**), the usage-by-channel SQL, the dashboard's channel labels.

### Notes for the implementer

- Reuse `middleware.NewRateLimiter`'s shape (`internal/transport/http/middleware/ratelimit.go`);
  do not build a second limiter. Separate bucket, same mechanism.
- `T-03`'s budget check belongs in the `/v1` chain and must surface as a typed
  402 `budget_exhausted`, not a 500. A programmatic caller retries a 500.
- Scopes come from `T-13`. This ticket adds `write:reports` and `read:documents`
  to that list — deny by default, as `T-13` already specifies.

### Acceptance

- [ ] A dashboard JWT on any `/v1` route returns 401; an API key on any `/api` route returns 401
- [ ] A replayed `Idempotency-Key` returns the same logical response with `Idempotent-Replay: true`, and exactly one document/turn exists afterwards
- [ ] A replay of a still-running request returns `409 request_in_flight`, not a second turn
- [ ] No idempotency record in Redis exceeds 4 KiB, including after a 10 MB PDF render
- [ ] The same key with a changed body returns 409
- [ ] A turn started through `/v1` shows `channel=api` in `/api/usage/by-channel`, and `ChatRunner.completeWith` does not attempt an outbound send
- [ ] Every `/v1` error response matches the envelope — no bare `{"error":"…"}` anywhere under `/v1`
- [ ] A 429 carries `Retry-After` and all three `RateLimit-*` headers
- [ ] `API_V1_ENABLED=false` → 503 on every `/v1` route, including `/v1/me`
- [ ] The `request_id` in a response body appears in the audit row for that call

### Gate

`curl` transcript covering all seven cases above, plus a grep over the `/v1`
handlers showing zero direct `gin.H{"error"` sites.

### Out of scope

- OAuth or per-end-user tokens — API keys are company-scoped machine credentials
- `/v2` planning, deprecation tooling, sunset headers
- Public API docs site (`T-A4`)

---

## T-A2 · Reports over the API
**Repo:** BE · **Size:** 2.5d · **Deps:** T-A1, T-R2 (`pptx` needs T-R4) · **Priority:** P0 · **Never cut**
**Migration:** `032_documents_api`

### Why

The thing the owner asked for on 2026-07-28: a tenant's app asks Argentum for a
PDF or an Excel file and gets one. It is also the cheapest integration this
product will ever sell — no chat UI to build, no streaming to handle, one POST
and a file — which makes it the right flagship for the track.

### Do

**Two doors, per locked decision 2:**

| Route | Input | LLM? | Latency | Bills |
| ----- | ----- | ---- | ------- | ----- |
| `POST /v1/reports/render` | a report spec | no | sub-second | `document_generated` |
| `POST /v1/reports` | a prompt | yes | seconds–minutes | tokens + `document_generated` |

- **`POST /v1/reports/render`** — body is the same `spec.Document` the
  `generate_document` tool accepts (`spec_version: 2`, `format: pdf|xlsx|csv|pptx`).
  `Accept: application/json` returns the document object with a presigned
  `download_url`; `Accept: application/pdf` (or the format's content type) returns
  the bytes inline. Deterministic, no thread, no agent.
- **`POST /v1/reports`** — `{prompt, format, user_ref?, callback_url?, locale?,
  currency?, meta?}` → 202 with a report object at `status=queued`. Runs a real
  turn through `ChatEnqueuer` on `domain.ChannelAPI` (from `T-A1`) with a
  directive to finish by calling `generate_document`. Three ways to collect the
  result, because integrators differ: poll `GET /v1/reports/:id`, stream
  `GET /v1/reports/:id/events` (SSE), or receive the signed `callback_url`.
  **The SSE bridge for this endpoint ships here, not in `T-A3`** — `T-A3` may
  land after this ticket, and a flagship that cannot stream progress on a
  two-minute operation is a flagship people poll in a `while` loop.
- `GET /v1/documents` (cursor-paginated, filterable by `format` and date),
  `GET /v1/documents/:id`, `GET /v1/documents/:id/content`.
  **`:id` re-presigns on every call.** A presigned URL expires; an integrator who
  stored one must be able to get a fresh link without paying to regenerate the
  document. `/content` streams the object rather than 302-ing, so a server-side
  client that does not follow redirects still works.
- Migration `032_documents_api`:
  - `ALTER TABLE documents ALTER COLUMN thread_id DROP NOT NULL` — the
    **`/render` door** has no thread. The FK and its `ON DELETE CASCADE` stay,
    because the agent path and the **agentic door** both still have one.
    `source` and `thread_id` are independent: `source=api` with a non-null
    `thread_id` is the normal shape for `POST /v1/reports`.
  - `ADD COLUMN source TEXT NOT NULL DEFAULT 'agent'` (`agent | api`)
  - `ADD COLUMN api_key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL`
  - `CREATE INDEX idx_documents_company_created ON documents(company_id, created_at DESC)`
    — what the list route pages on.
  - **The down migration must delete rows with a null `thread_id` first**, or
    `SET NOT NULL` fails. Say so in a comment in the file.
- Storage key: `generate_document.go:179` embeds `thread_id`, which the `/render`
  door does not have. Use `documents/{company_id}/api/{document_id}.{ext}` when
  there is no thread and leave the existing threaded key untouched otherwise —
  one branch, not a new scheme for everything.
- Signed callback delivery in `internal/webhookout`: HMAC-SHA256 over the raw
  body, `Argentum-Signature: t=…,v1=…`, asynq-backed with exponential retry and a
  delivery log. **`T-15` subscribes watcher events to this package rather than
  building a second sender.**
- Limits: `API_V1_MAX_SPEC_ROWS` (default 50 000), `API_V1_MAX_SPEC_COLS`
  (default 40), `API_V1_SYNC_RENDER_TIMEOUT` (default 20s — over it, return 202
  and finish async).

### Notes for the implementer

- **Do not write a second renderer.** `RenderPDF` / `RenderXLSX` / `RenderCSV` are
  already pure `(*Spec) ([]byte, error)`. The tool's only extra work is upload +
  metadata + metering. Factor that into `internal/app/document_service.go` and
  have both `GenerateDocumentTool` and the `/v1` handler call it.
- `generate_document.go:164` hard-requires `tenantctx.ThreadID`. That check moves
  into the service's **agent** path, not the shared one.
- A spec arriving over HTTP is untrusted in a way the agent's own spec never was.
  Cap rows, columns and string lengths and reject **before** rendering — maroto
  will cheerfully attempt to lay out 500 000 rows and take the worker with it.
- `T-R2` made cells carry a value and a type so the renderer decides formatting.
  Keep that property in the API contract: the caller sends `3863405700` +
  `currency`, not `"Rp 3.863.405.700"`. It is the same reason the model stopped
  formatting, and it applies harder to a third party.

### Acceptance

- [ ] `POST /v1/reports/render` with the `monthly_sales.json` fixture returns a PDF byte-identical to what `go test ./internal/report/pdf` renders
- [ ] The same fixture at `format: xlsx` opens in Excel; at `pptx` opens in PowerPoint (after `T-R4`)
- [ ] `POST /v1/reports` with a prompt against the demo tenant produces a document whose figures match a direct `run_sql`
- [ ] A `/render` document row has `source=api`, a **null** `thread_id`, and the generating `api_key_id`
- [ ] An agentic-door document row has `source=api`, a **non-null** `thread_id` on an `api`-channel thread, and the same `api_key_id`
- [ ] `GET /v1/documents/:id` an hour after creation still returns a working `download_url`
- [ ] A spec over the row cap is rejected with `invalid_request` **before** any rendering starts
- [ ] A callback body verifies against the secret; a tampered body does not
- [ ] A key without `write:reports` gets 403 on both doors
- [ ] `migrate down` succeeds against a database holding both an agent document and an API document

### Gate

`curl` transcript: render → download → re-fetch after the first presign expires.
Then the agentic door end to end with the signed callback received and verified.
Then `migrate up` / `migrate down` output against a database with both row kinds.

### Out of scope

- Report templates or a saved-spec library (`backlog.md`)
- Scheduled report delivery — that is watchers + `send_message`, already in `backlog.md`
- Document retention/expiry policy (note the column, do not build the sweeper)

---

## T-A3 · Chat over the API: SSE and sync
**Repo:** BE · **Size:** 2d · **Deps:** T-A1 · **Priority:** P0
**Migration:** none — the `api` channel and `api_user_ref` land in `T-A1`'s
`031_api_channel`, because `T-A2` needs them too.

### Why

A report is an artefact; a question is a conversation. A tenant embedding
Argentum in their own admin panel wants both, and the streaming contract is the
part they cannot build around if we get it wrong.

This ticket also writes down the WebSocket event schema for the first time —
`api-surface.md` observation 4 calls it the dashboard's most important contract
and records it as undocumented.

### Do

- `POST /v1/chat` — `{message, thread_id?, user_ref?}`.
  - `Accept: text/event-stream` → SSE. Subscribe to `eventbus.ChannelFor(threadID)`
    exactly as `internal/transport/ws/handler.go:98` does, and emit the event names
    the dashboard already receives: `started`, `delta`, `thinking`, `tool_call`,
    `tool_result`, `error`, `final`.
  - `Accept: application/json` → block until `final`, capped by
    `API_V1_SYNC_TIMEOUT_SECONDS`. On timeout return **504 with
    `{thread_id, run_id}`** so the caller resumes over SSE instead of re-asking
    and paying for the turn twice.
- SSE hardening, all three of which are load-bearing:
  - `:heartbeat` comment every 15s — idle proxies close silent streams.
  - `Last-Event-ID` honoured, resuming from the persisted message log.
  - **A client disconnect must not cancel the turn.** The worker finishes, the
    answer persists, the next call collects it. Cancelling on disconnect means a
    flaky network costs the tenant money for nothing.
- Threads keyed by the tenant's own user identifier:
  `ThreadService.ResolveForAPIUser(ctx, companyID, userRef, msg)` over `T-A1`'s
  `LatestForAPIUser`, reusing the **existing idle-gap + classifier fork logic**
  that Discord and Lark share. There are already four `LatestFor…` resolvers on
  `domain.ThreadRepository`; this is the fifth caller of one shared heuristic,
  not a fifth heuristic.
- `GET /v1/threads`, `GET /v1/threads/:id`, `GET /v1/threads/:id/messages`,
  `DELETE /v1/threads/:id` — all cursor-paginated, all company-scoped.
- Scope split: `write:chat` to send, `read:threads` to read. A read-only key must
  not be able to spend the tenant's credits.

### Notes for the implementer

- Turns now run five to seven tool calls after `T-16`. The sync door is a
  convenience for short questions, not the default — document it that way, and
  keep the timeout conservative rather than raising it when someone complains.
- The SSE writer must flush per event (`c.Writer.Flush()`), or gin buffers the
  whole stream and the feature silently does nothing.

### Acceptance

- [ ] An SSE turn streams deltas and ends with `final` carrying the message and usage
- [ ] The sync door returns the same answer as the SSE door for the same question
- [ ] Killing the client mid-stream still persists the answer; a later `GET …/messages` shows it
- [ ] The same `user_ref` inside the idle gap continues one thread; two different refs get two threads
- [ ] Neither `user_ref` can read the other's thread by id
- [ ] `/api/usage/by-channel` shows `api`; `/api/usage/by-user` shows the refs
- [ ] A sync call over the timeout returns 504 with a resumable `thread_id`, and the turn still completes
- [ ] A `read:threads`-only key gets 403 on `POST /v1/chat`

### Gate

`curl -N` transcript of a streamed turn with heartbeats visible, the same
question through the sync door, the disconnect case, and the resulting
`by-channel` usage row. Paste all four.

### Out of scope

- WebSocket transport on `/v1` (locked decision 3)
- Tool-call approval over the API — that is `T-10`/`T-11`
- Per-end-user rate limits inside a tenant (the key's bucket is the limit in v1)

---

## T-A4 · OpenAPI 3.1, SDKs, and a 10-minute quickstart
**Repo:** BE, PKG · **Size:** 2.5d · **Deps:** T-A2, T-A3 · **Priority:** P0 · **Never cut**

### Why

"Easily integrate" is this ticket. The rest of the track makes the API exist;
this is what lets someone finish an integration without talking to us. Pulled
forward from [`backlog.md`](backlog.md) ("Client SDKs for the API"), which had
already concluded the spec must come first and be generated from, not hand-written
alongside.

### Do

- `apps/backend/openapi/v1.yaml` — OpenAPI 3.1 covering every `/v1` route, the
  error envelope, the auth scheme, and the SSE event schema (documented as a
  `text/event-stream` response with a `oneOf` over the seven event types). Served
  at `GET /v1/openapi.json`, **public and keyless** — an integrator reads it
  before they have a key.
- **CI parity check, both directions.** A test walks the gin route tree and diffs
  `/v1` paths + methods against the spec. A route with no spec entry fails; a spec
  entry with no route fails. Same guard shape as `T-R1`'s token-drift job.
- SDKs — generated types, hand-written ergonomics:
  - `packages/argentum-node` → `@argentum/sdk`: `client.reports.render(spec)`
    returning a `Buffer`, `client.reports.create({prompt})` returning a poller,
    `for await (const ev of client.chat.stream({…}))`.
  - `packages/argentum-python` → `argentum`: the same three shapes, sync and
    async clients.
  - Both: retry with backoff on 429/5xx honouring `Retry-After`, automatic
    `Idempotency-Key` generation on POSTs, and typed errors mirroring the
    envelope's `type` field.
- `T-02b` already generates TS types from Go structs. **The Node SDK consumes
  those** — a second generated copy of the same types is exactly the drift `T-R1`
  was written to stop.
- `docs/api/quickstart.md` — empty directory to a PDF on disk in under ten
  minutes: create a key, `GET /v1/me`, `POST /v1/reports/render` with a
  copy-pasteable spec, then the agentic door and the chat stream. curl first,
  then Node, then Python.
- Regenerate `apps/backend/docs/postman/` from the spec, replacing the
  hand-maintained collection.
- **Every sample runs in CI against the demo tenant** — an example that has never
  been executed is a support ticket with a delay fuse, and `T-22` already records
  that examples are security surface, not documentation. **Split by cost, though:**
  the deterministic samples (`/v1/me`, `/v1/reports/render`, the document routes)
  run on every push because they cost a render; the agentic samples
  (`POST /v1/reports`, `POST /v1/chat`) run **nightly**, because putting a real
  LLM turn in the per-push path bills the demo tenant for every commit in the
  monorepo. A nightly failure still catches a broken example within a day.

### Acceptance

- [ ] Adding a `/v1` route without a spec entry fails CI; deleting a route that is still specced fails CI
- [ ] `npm i @argentum/sdk` in an empty project → a PDF on disk in under 10 minutes using only the quickstart
- [ ] Same for the Python package
- [ ] The SDK retries a 429 automatically and raises a typed error for a 403
- [ ] `GET /v1/openapi.json` validates against the OpenAPI 3.1 meta-schema
- [ ] Every deterministic code sample is executed on every push; every agentic one nightly
- [ ] Breaking a sample turns the corresponding job red (demonstrate, do not assert)

### Gate

The CI run showing the parity check red on a deliberately unspecced route, then
green. Plus a terminal transcript of the full ten-minute path from empty
directory to PDF, timed.

### Out of scope

- A Go SDK — add it when someone asks; the demand is Node and Python
- A hosted docs site (Markdown in the repo until it hurts)
- An interactive API playground

---

## T-A5 · Integrator-facing observability
**Repo:** BE, FE · **Size:** 1d · **Deps:** T-A1, T-13 · **Priority:** P1 · **Cut #1a**

### Why

An integrator debugging a 403 at 11pm should not need us to read logs. This is
the difference between an API someone adopts and an API someone abandons after
the first unexplained failure.

### Do

- Dashboard Settings → API Keys, per key: request count, error rate, last used,
  and the last 50 non-2xx responses with request id, route, status, and the
  error `code`.
- `GET /v1/usage` — the tenant's own spend and remaining credits, so their
  application can meter its own users.
- Per-route latency and status histograms on `/metrics`, labelled by route and
  key id. (`/metrics` is secured by `T-05`; do not add this before that lands.)

### Acceptance

- [ ] A forced 403 and a forced 429 both appear in the tab within a minute
- [ ] The request id shown matches the `X-Request-Id` the caller received
- [ ] The tab is admin-only (`T-04`'s `AdminOnly()`)

### Gate

Screenshots of the tab after deliberately triggering a 403, a 429 and a 500,
alongside the three `curl` responses whose request ids they match.

### Out of scope

- Alerting on API error rates
- A per-key spend cap (the company budget from `T-03` is the limit in v1)

---

# Week 1 — Safe to change

## T-01 · Eval harness
**Repo:** BE · **Size:** 3d · **Deps:** T-00 · **Priority:** P0 · **Never cut**

The system has no way to know whether a prompt or model change helped. Build one.

**Do:**
- `cmd/eval/main.go` — CLI: `go run ./cmd/eval -set testdata/eval/golden.yaml [-model X] [-out report.json]`.
- `internal/eval/` — runner, scorer, report types.
- Golden set at `testdata/eval/golden.yaml`, **≥30 cases** against the demo
  tenant's star schema (`fact_sales`, `dim_customers`, `dim_products`, `dim_date`).
  Case shape:
  ```yaml
  - id: rev-monthly-total
    question: "What was total revenue last month?"
    lang: en
    expect:
      kind: numeric          # numeric | contains | sql_shape | refusal | tool_called
      value: 1234567.89      # for numeric
      tolerance: 0.01
      must_call: [run_sql]
      must_not_call: [create_dashboard]
  ```
- Cover these categories, minimum counts: simple aggregate ×6, time-window ×5,
  grouping/top-N ×4, multi-source disambiguation ×3, chart/dashboard request ×3,
  Indonesian-language ×5 (including rupiah magnitude formatting), guardrail
  refusal ×4 (off-topic, injection, SQL mutation).
- Runner must go through the **real** `ChatRunner` path — same agent factory, same
  guardrails, same tools — against a seeded demo tenant. Not a mocked LLM.
- Score: per-case pass/fail plus aggregate `pass_rate`, `mean_tokens_in`,
  `mean_tokens_out`, `mean_latency_ms`, `mean_cost_usd`.
- `make eval` target. Write the first run to `docs/coverage/eval-baseline.md`.

**Notes for the implementer:**
- Numeric comparison must tolerate formatting: strip currency symbols, magnitude
  suffixes (Juta/Miliar/Triliun/K/M/B), and thousands separators before parsing.
  **Put that parser in `internal/report/format`, not `internal/eval`.** `T-R2`
  extends the same package with the formatting direction — one package, built
  from the eval side first because phase 1 now runs before phase 1a.
- Language check: assert the reply's language matches `lang`. A cheap
  heuristic (Indonesian stopword ratio) beats an LLM judge for this and costs nothing.
- Guardrail cases assert the refusal *message*, not just non-answering.

**Acceptance:**
- [ ] `make eval` runs offline against local infra, no cloud dependency except the LLM
- [ ] ≥30 cases across all listed categories
- [ ] Report includes pass rate, token, latency, and cost aggregates
- [ ] Baseline committed to `docs/coverage/eval-baseline.md`

**Gate:** paste the full report summary. State the baseline pass rate as a number.

---

## ~~T-02~~ · Test coverage for CRITICAL packages + real CI gate — **DONE 2026-07-28**
**Repo:** BE, FE · **Size:** 3d · **Deps:** none (parallel with T-01) · **Priority:** P0 · **Never cut**

**Shipped.** Record, with the gate output:
[`../coverage/test-coverage.md`](../coverage/test-coverage.md).
**21 of 49 packages have tests** (was 16 of 49), every CRITICAL package is
covered, `go test -race` is green and `golangci-lint` reports 0 issues against
a config the tree failed in 50 places on the first run.

Four things came out of it that the ticket did not anticipate:

- **Non-UTC scheduled tasks cannot work in the deployed images.**
  `time.LoadLocation` reads `/usr/share/zoneinfo`, which `alpine:latest` does
  not ship and nothing installed; no file imported `time/tzdata`. So
  `normalizeTimezone("Asia/Jakarta")` succeeds on every developer machine and
  fails in production. One blank import in `internal/app` fixes it, and the
  test that guards it sets `ZONEINFO` to a path that does not exist so it
  cannot pass by accident.
- **Two unchecked type assertions in the chat handler**, found by turning on
  errcheck's `check-type-assertions`: `uid.(string)` on a value only
  `middleware.Auth` sets, so a route wired without it panics instead of
  returning 401.
- **`redact_nik` can never fire.** Sixteen consecutive digits are also a
  separator-less credit-card number, and `redact_credit_cards` is declared
  first. The golden suite records this as a shadowing rather than covering the
  rule with a case that would have asserted the wrong one. Two smaller
  redaction edges and one topic-gate false positive ("margins are collapsing")
  are pinned the same way. All belong to `T-07b`.
- **The PPTX determinism test was flaky and had never been seen to be.**
  `v1_legacy.json` carries no `generated_at`, so it rendered with the wall
  clock; the test rendered twice and compared, which only fails when the pair
  straddles a second. Under `-race` it does. The PDF's equivalent test had
  already been taught this lesson in `T-R2` and skips unstamped fixtures; the
  deck's now pins the clock instead, so `v1_legacy` is covered rather than
  excluded.

**Tests to write** (see `../coverage/test-coverage.md` for the risk ranking):

| Package | Must cover |
| ------- | ---------- |
| `internal/crypto` | Encrypt/decrypt round-trip; wrong key fails; malformed ciphertext errors rather than panics; key-length validation |
| `internal/tenantctx` | Every getter returns "" for an unset key; values do not leak across derived contexts |
| `internal/guardrails` | **Golden suite: every rule in `config/guardrails.yaml` gets ≥1 must-block and ≥1 must-pass case.** Include the specific false positives the YAML comments describe: "create a dashboard", "update me on sales", CSS `margins`, "integer target", "linked list", benign follow-ups ("ok", "why?"), Indonesian particles. LLM patterns get a stub LLM returning TRUE/FALSE. |
| `internal/app` (pricing) | `RecordLLM` cost math incl. cache multipliers (1.25× create, 0.10× read); unknown model falls back to `DefaultPricing`; zero tokens → zero cost |
| `internal/app` (threading) | `continueOrFork` decision table: under idle threshold → continue; over → classifier RELATED continues / NEW forks; classifier error → safe default. Fake classifier + fake repos |
| `internal/app` (cron) | `validateCron`, `normalizeTimezone`, `nextFire` — including DST boundaries and invalid IANA names |
| `internal/config` | All 7 `Effective*()` fallback chains; `WorkerQueueMap()` CSV parsing incl. malformed input; `DatabaseURL()` escaping with special characters in the password; `redisDialAddr()` for URI and bare-host forms |
| `internal/auth` | Argon2id hash/verify; JWT sign/verify; expired token rejected; refresh token rejected on an access-token route |
| `internal/tools` | `run_sql` byte-cap trimming loop (wide rows shrink and set `truncated`); `ResolveSource` with 0 / 1 / many sources and an explicit `source_id`; empty-company-ID rejection |

**CI changes** in `.github/workflows/ci.yaml`:
- ~~`GO_VERSION` → `'1.26'`~~ ✅ `T-00b`
- ~~add `go vet ./...`~~ ✅ `T-00b`
- ~~add `go test -race -count=1 ./...`~~ ✅ `T-00b`
- ~~add `go build -o discord ./cmd/discord`~~ ✅ `T-00b`
- ~~add `golangci-lint run` with a committed `.golangci.yml`~~ ✅ — five
  linters as specified, plus a gofmt check that comes free with the v2
  `formatters` block. Config at `apps/backend/.golangci.yml`, run through
  `golangci/golangci-lint-action@v8` pinned to v2.12.
- ~~add a frontend job~~ ✅ `T-00b` (`web`), which now actually lints: the
  dashboard's `lint` script is `tsc -b --noEmit && eslint .` with eslint 9 and
  a flat config installed by this ticket.
- ~~**remove the `paths:` filter**~~ ✅ `T-00b`

Also: `make lint` (Go + web) and `make check` (vet + lint + test + build) at the
repo root, so the gate is one command locally.

**Acceptance:**
- [x] Every CRITICAL package from the coverage doc has tests — `internal/crypto`,
      `internal/tenantctx`, `internal/guardrails` and the three `internal/app`
      services named above, plus the HIGH/MEDIUM ones the ticket listed
      (`internal/auth`, `internal/config`, `internal/tools`) and the middleware
      that enforces token type
- [x] Guardrail golden suite covers every rule, both directions — enforced by
      `TestEveryRuleHasGoldenCases`, which fails when a rule is added without
      cases or covered in only one direction. `redact_nik` is the one exception
      and it is explicit: the rule cannot fire, and a named test asserts the
      shadowing rather than faking coverage.
- [ ] CI fails when a test fails (prove it: push a deliberately broken test,
      observe red, revert) — **proved locally, not yet in CI.** The break was
      made in `internal/crypto/dsn_test.go` (invert the round-trip assertion),
      `go test -race -count=1 ./internal/crypto/` failed on five subtests, and
      the revert returned exit 0. Pushing a branch is the repo owner's call, so
      the CI-run half of this item is outstanding.
- [x] `cmd/discord` builds in CI — since `T-00b`; unchanged here

**Gate:** `go test -race ./... 2>&1 | tail -40` — paste it. Plus the CI run URL
showing red on the deliberate break and green after revert.

**Gate met except the CI run URL**, which needs a push. Output in
[`../coverage/test-coverage.md`](../coverage/test-coverage.md).

---

## T-02b · Generate TS types from Go structs
**Repo:** BE, PKG, FE · **Size:** 1d · **Deps:** T-00b, T-02 · **Priority:** P1

Today `apps/dashboard/src/features/*/types.ts` hand-mirrors Go JSON tags and
nothing checks that they agree. A renamed field or a changed type is a runtime
surprise, found by a user. The monorepo makes this mechanically fixable, so fix it.

**Do:**
- Pick a generator. `tygo` is the least-effort fit: reads Go source, respects
  `json` tags, no annotations required. Evaluate `go-jsonschema` +
  `json-schema-to-typescript` only if `tygo` cannot express the domain types.
- Config covering `internal/domain` and `pkg/models` — the two packages whose
  types cross the wire. Output to `packages/api-types/src/`.
- `make types` regenerates; the output is **committed**, so a reviewer sees
  contract changes in the diff.
- CI job `types`: run `make types`, then `git diff --exit-code packages/api-types`.
  A Go struct change without a regenerated type is a red build.
- Migrate the dashboard's hand-written types to import from
  `@argentum/api-types`, one feature at a time. Delete each `types.ts` only once
  its feature compiles against the generated types.
- Where the generated shape and the hand-written one disagree, **the Go struct is
  the truth** — but check each disagreement before deleting: some are real bugs
  worth a line in the report.

**Notes for the implementer:**
- WebSocket event types (`ChatEvent`, `ToolCallEvent`) matter most — that is the
  contract with the least documentation and the most drift risk.
- Go `map[string]interface{}` generates as `Record<string, unknown>`, which is
  correct but weak. Do not hand-strengthen it in the generated file; if a metadata
  shape deserves a type, give it one in Go.
- `time.Time` → `string`. Make sure the dashboard's date handling still expects a
  string, not a `Date`.

**Acceptance:**
- [ ] `make types` produces types for every domain type crossing the API
- [ ] Dashboard compiles against `@argentum/api-types` with its hand-written
      duplicates deleted
- [ ] CI fails when a Go struct changes without regeneration — prove it and revert
- [ ] Any real Go↔TS mismatch found during migration is listed in the report

**Gate:** paste the CI failure from a deliberate Go field rename, then the pass
after `make types`. List every mismatch the migration surfaced.

---

## T-02c · Fix primary-model metering on streaming turns
**Repo:** BE · **Size:** 1d · **Deps:** T-02 · **Priority:** P0 · **Never cut**

**Finding Q-12, observed live in the `T-00` smoke test.** A full multi-step agent
turn recorded **zero** usage events for the primary model. The only `llm_call` row
was `gpt-5-mini` — the light model behind guardrails. Under the current default
provider, the dominant cost of every chat turn is invisible.

**Must land before `T-03`**, whose budget check would otherwise gate on a number
that is always near zero — enforcement that silently never triggers is worse than
no enforcement, because it looks like it works.

**Do:**
- Confirm the mechanism first. `MeteredLLM.wrapStream`
  (`internal/app/metering_llm.go:136`) only calls `record()` when the provider put
  usage in stream event metadata. Determine what `agent-sdk-go` sends for the
  OpenAI interface — the strong hypothesis is a missing
  `stream_options: {"include_usage": true}`, but verify before changing anything.
- Fix at the source if possible (request usage in the stream). If `agent-sdk-go`
  cannot be made to emit it, fall back to counting tokens locally with a tokenizer
  and record with a `estimated: true` metadata flag — an approximate cost beats a
  silent zero.
- **Add a loud failure mode.** A completed streaming turn that produced no usage
  event must log at `Warn` with company, model, and interface. Silence is what let
  this survive; make it noisy.
- Add a metric: usage-events-per-turn, so a future regression is visible on a
  dashboard rather than discovered by a smoke test.
- Regression test: fake LLM emitting a stream *with* usage and one *without*;
  assert the with-usage case records, and the without-usage case records an
  estimate and logs a warning.

**Acceptance:**
- [x] A streaming turn on an OpenAI-interface provider records a non-zero `llm_call`
- [~] A streaming turn on Anthropic still records, including cache tokens (no regression on `74f5419`) — **unit-tested, not live-tested: no Anthropic-native credentials on this machine**
- [x] Zero-usage streams warn loudly rather than passing silently
- [x] `cost_by_model_usd` shows the primary model after one chat turn

**Gate:** repeat the `T-00` smoke test — signup, connection, one analytical
question — then paste `/api/usage/summary`. The primary model must appear with
non-zero tokens. Compare against the pre-fix output recorded in
`../coverage/environment-notes.md` C-2.

### Status — landed 2026-07-27

**The hypothesis in "Do" was wrong, and the wrongness is the finding.**
`include_usage` was already being requested — `withForcedUsage` has set
`EnableReasoning` since `74f5419`, which is the flag agent-sdk-go's OpenAI
client checks. The provider sent usage on every turn. agent-sdk-go forwards it
into a `StreamEvent` **only** in `GenerateStream`
(`pkg/llm/openai/streaming.go:212`), the no-tools path; the tool-calling path
every agent turn uses sets `IncludeUsage: true` per iteration (line 361) and
then never reads `chunk.Usage`.

Fix: `internal/llmusage` taps usage out of the SSE body via an
`http.RoundTripper` installed on the OpenAI-interface client, keyed to a
collector in the request context. Exact provider numbers, every tool-calling
iteration included — so the tokenizer-estimate fallback this ticket allowed was
not needed and no `estimated: true` flag exists. Anthropic keeps metering from
stream metadata, which takes priority whenever present.

Gate output (post-fix `/api/usage/summary`, versus C-2's pre-fix JSON) is in
[`../coverage/environment-notes.md`](../coverage/environment-notes.md) C-2 under
"Resolved". Before: one `llm_call`, `gpt-5-mini` only. After:
`deepseek/deepseek-v3.2` at 5232 in / 579 out, 3840 of them cache reads.

Two things a reader should know:

- **The `T-01` baseline's cost and token aggregates are now known to be
  light-model-only** and understate a turn by roughly 10x. Pass rate is
  unaffected. Noted in [`../coverage/eval-baseline.md`](../coverage/eval-baseline.md).
- **The new metric is process-local.** `llm.stream_turns_without_usage` is
  served on the API's `/metrics`, but agent turns run in the worker, which has
  no HTTP surface. Until `T-17` gives it one, the warning log is the operational
  signal and the counter is a unit-testable invariant.

---

## T-03 · Enforce credits with graceful degradation
**Repo:** BE, FE · **Size:** 1d · **Deps:** T-02, **T-02c** · **Priority:** P0

**Finding B-1:** `UsageService.append` decrements the balance and ignores the
result. Nothing checks it. A tenant on platform LLM keys can spend without limit.

**Do:**
- `UsageService.CheckBudget(ctx, companyID) (BudgetState, error)` returning
  `BudgetOK` / `BudgetWarning` (<20% remaining) / `BudgetExhausted` (≤0).
- Check in `ChatEnqueuer` **before** enqueueing, not in the worker — fail fast
  and don't pay for a task that gets refused.
- `BudgetExhausted` → `HTTP 402` with a clear message; on WhatsApp/Discord/Lark,
  a plain-language reply, not a stack trace.
- **Never block a tenant using their own LLM key.** If
  `company_llm_credentials` has a primary row, skip the check — they pay their
  provider directly.
- `BudgetWarning` → include a `budget_warning` field in the chat response; FE
  shows a dismissible banner.
- Redis-cache the balance for 60s so the check doesn't add a query per turn.
- Config: `CREDITS_ENFORCEMENT_ENABLED` (default `true`),
  `CREDITS_WARNING_THRESHOLD_PCT` (default `20`).

**Acceptance:**
- [ ] Tenant at zero balance gets 402 with an actionable message, no LLM call made
- [ ] Tenant with own LLM credentials is never blocked
- [ ] Warning banner appears below the threshold
- [ ] Kill switch restores today's behaviour

**Gate:** integration test — seed a company with 0 credits, POST `/api/chat`,
assert 402 **and** assert zero new `usage_events` rows. Repeat with a BYO-LLM
company and assert 200.

---

## T-04 · Apply RBAC + team invites
**Repo:** BE, FE · **Size:** 1.5d · **Deps:** T-02 · **Priority:** P0 · **Never cut**
**Migration:** `027_user_invites`

**Findings S-1, S-2.** `AdminOnly()` exists and is applied to nothing. Nine
credential/config-mutating routes are open to any member.

**Do:**
1. Apply `middleware.AdminOnly()` to: `PUT /api/connections/:id/dsn`,
   `DELETE /api/connections/:id`, `PUT /api/settings`, `POST /api/phones`,
   `DELETE /api/phones/:phone`, `PUT|DELETE /api/discord`,
   `POST|DELETE /api/discord/users`, `PUT|DELETE /api/lark`,
   `POST|DELETE /api/lark/users`, `DELETE /api/scheduled-tasks/:id`,
   and every LLM-credential route.
2. Team management (new `UserHandler` routes, admin-only):
   - `GET /api/users` — list company users
   - `POST /api/users/invite` — `{email, role}` → create a pending user + a
     single-use invite token (hashed at rest, 7-day TTL)
   - `POST /api/auth/accept-invite` — `{token, password}` → activate (public route)
   - `PATCH /api/users/:id` — change role
   - `DELETE /api/users/:id` — deactivate; **never** allow removing the last admin
3. FE: Settings → Team tab. Invite form, member list with role badges, revoke.
4. Migration `027_user_invites` — `user_invites` table (`company_id`, `email`,
   `role`, `token_hash`, `expires_at`, `accepted_at`, `invited_by`), plus a
   nullable `users.activated_at` so a pending user cannot log in before accepting.

**Acceptance:**
- [ ] Member JWT gets 403 on every route listed in step 1
- [ ] Admin JWT succeeds on all of them
- [ ] Invite → accept → login works end to end
- [ ] Last admin cannot be removed or demoted

**Gate:** table-driven test over every gated route × {admin, member} asserting
{200-ish, 403}. Paste the test output.

---

## T-05 · Agent action audit log
**Repo:** BE · **Size:** 1.5d · **Deps:** T-02 · **Priority:** P0
**Migration:** `021_agent_actions`

**Finding S-5.** `usage_events` records cost, not behaviour. Before the agent can
act, there must be an immutable record of what it did.

**Do:**
- Table `agent_actions`: `id`, `company_id`, `thread_id`, `message_id`,
  `actor_kind` (`user`|`schedule`|`watcher`|`api_key`), `actor_ref`, `channel`,
  `tool_name`, `source_id`, `args_redacted` (jsonb), `args_hash`,
  `result_status` (`ok`|`error`|`blocked`|`truncated`), `error_text`,
  `rows_returned`, `duration_ms`, `created_at`. Index on
  `(company_id, created_at desc)` and `(thread_id)`.
- `domain.AgentAction` + `AgentActionRepository`; `adapters/postgres/agent_action_repo.go`.
- Record from a **wrapper around the tool interface**, not inside each tool —
  `tools.WithAudit(tool, repo)` decorating every tool in `cmd/worker/main.go`.
  One integration point, no per-tool duplication.
- `args_redacted` must strip anything DSN-shaped. **Full SQL text is retained** —
  it is the point of the log — but never a credential.
- `GET /api/audit/actions?from&to&thread_id&tool&limit&offset`, admin-only.
- Append-only: repository exposes no update or delete.

**Acceptance:**
- [ ] Every tool call produces exactly one row, success or failure
- [ ] A blocked guardrail turn records `result_status=blocked`
- [ ] No row contains a decrypted DSN or API key
- [ ] Audit endpoint is admin-gated and company-scoped

**Gate:** run one demo chat that calls `get_schema` + `run_sql` +
`create_visualization`; paste the resulting three rows (redacted args visible).
Then `grep` the table dump for the demo DSN password and show zero matches.

---

# Week 2 — Authoritative numbers

## T-06 · Metric registry
**Repo:** BE, FE · **Size:** 3d · **Deps:** T-02 · **Priority:** P0 · **Never cut**
**Migration:** `022_metric_definitions`

**The accuracy foundation.** Today every question re-derives its SQL, so the same
question can produce two different numbers. Watchers cannot exist on top of that.

**v1 shape — deliberately narrow. Do not add dimensions, joins, or a DSL:**

```sql
CREATE TABLE metric_definitions (
  id            uuid PRIMARY KEY,
  company_id    uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  source_id     uuid NOT NULL REFERENCES db_connections(id) ON DELETE CASCADE,
  key           text NOT NULL,            -- 'revenue', 'active_customers'
  label         text NOT NULL,
  description   text NOT NULL,            -- what the agent reads to decide relevance
  sql_template  text NOT NULL,            -- must contain {{from}} and {{to}}
  value_column  text NOT NULL,
  grain         text NOT NULL,            -- day|week|month|quarter|year
  unit          text NOT NULL,            -- currency|count|percent|ratio
  currency      text,
  higher_is_better boolean NOT NULL DEFAULT true,
  enabled       boolean NOT NULL DEFAULT true,
  created_by    uuid, created_at timestamptz, updated_at timestamptz,
  UNIQUE (company_id, key)
);
```

- `{{from}}` / `{{to}}` are bound as **parameters**, never string-interpolated.
  Reject templates containing anything but a single SELECT (reuse the guardrail
  mutation patterns).
- Validation on save: render with a trailing-7-day window, execute via
  `ExecuteReadOnly`, assert exactly one row and that `value_column` is numeric.
  **A metric that does not validate cannot be saved.**
- CRUD API `/api/metrics` — read for members, write admin-only.
- FE: Settings → Metrics tab. Create/edit form with a "Test" button showing the
  rendered SQL and the returned value.

**Acceptance:**
- [ ] Saving an invalid metric fails with a specific reason
- [ ] Non-SELECT templates rejected
- [ ] Window params are bound, not interpolated (prove with a `'; DROP` style value)
- [ ] Same metric queried twice returns an identical number

**Gate:** define three demo-tenant metrics (revenue, order count, AOV). Paste each
one's validated value. Attempt one injection payload in the window param and show
the failure.

---

## T-07 · `list_metrics` + `query_metric` tools
**Repo:** BE · **Size:** 1.5d · **Deps:** T-06 · **Priority:** P0 · **Never cut**

**Do:**
- `internal/tools/list_metrics.go` — returns key, label, description, unit, grain
  per enabled metric.
- `internal/tools/query_metric.go` — params: `metric_key`, `from`, `to`,
  optional `compare_to` (`previous_period` | `same_period_last_year`). Returns
  value, comparison value, delta, delta percentage, and the window used.
- Register both in `cmd/worker/main.go`, wrapped by the T-05 audit decorator.
- Inject the metric catalog into the turn context in `ChatRunner`, alongside the
  source catalog — same pattern as `withSourcesContext`.
- System prompt: add a rule ranked above the `run_sql` guidance — *if a defined
  metric answers the question, use `query_metric`; only fall back to `run_sql` for
  questions no metric covers, and say so.*
- Meter `query_metric` as a `sql_query` event.

**Acceptance:**
- [ ] "What was revenue last month?" calls `query_metric`, not `run_sql`
- [ ] A question with no matching metric still works via `run_sql`
- [ ] `compare_to` returns a correct delta
- [ ] Unknown `metric_key` returns a helpful error listing available keys

**Gate:** eval run with metric-specific cases added. Paste the before/after pass
rate **and** the token delta — this should reduce mean input tokens measurably.

---

## T-07b · Fix guardrail over-reach
**Repo:** BE · **Size:** ~~0.5d~~ **1d** · **Deps:** T-02 · **Priority:** P1

**Findings Q-4, Q-6.** Redaction rules break legitimate BI output; the
system-prompt-leak rule false-positives on "what can you do?".

**Plus a finding from `T-16` that reframes the whole ticket: the output rules
have never run.** agent-sdk-go calls `Guardrails.ProcessOutput` in exactly one
place — `pkg/agent/agent.go:1315`, on the blocking path. The streaming path
applies `ProcessInput` only, and every chat turn streams. So `redact_nik`,
`redact_emails`, `redact_phone_numbers` and `block_system_prompt_leak` are
configured, tested by eye, and dead. Narrowing a rule that never fires is
worthless until the rule fires, so this ticket now starts by making output
rules run on the streaming path — the same seam `ChatRunner.rejectFabrication`
uses (T-16) — and only then narrows them. Budget +0.5d.

Two consequences worth carrying into the work: Q-4 and Q-6 were reported from
*reading* the rules, so their real-world blast radius is unmeasured; and
switching the rules on is a behaviour change on every turn, which needs a
`make eval` run on both sides of it.

**Do:**
- Apply output-scope guardrails on the streaming path, then verify with a case
  that a redaction actually fires end to end.
- Narrow `redact_nik`: require NIK context nearby (`nik`, `ktp`, `no. identitas`)
  rather than matching any 16-digit run — it currently blanks order IDs and
  account numbers.
- Make `redact_emails` and `redact_phone_numbers` **configurable per company**
  (`companies.pii_redaction_mode`: `strict` | `contact_ok` | `off`, default
  `strict`). A tenant asking for a customer contact list must be able to get one.
- Narrow `block_system_prompt_leak` to leak-shaped phrasing only (e.g. "my
  instructions are", "my system prompt is") instead of the bare phrase "you are
  an ai".
- Every change needs a golden case in the T-02 guardrail suite, both directions.

**Acceptance:**
- [ ] An output-scope rule demonstrably fires on a streaming turn
- [ ] "list top 10 customers with their emails" returns emails under `contact_ok`
- [ ] Under `strict`, it still redacts
- [ ] A 16-digit order ID survives; a labelled NIK is still redacted
- [ ] "What can you do?" answers normally
- [ ] `make eval` before and after switching output rules on: no regression

**Gate:** guardrail golden suite green, with the four new cases visible in output.

---

# Week 3 — It tells you first

## T-08 · Watchers domain + evaluation loop
**Repo:** BE · **Size:** 3d · **Deps:** T-06, T-07 · **Priority:** P0 · **Never cut**
**Migration:** `023_watchers`

**The wedge.** This is the ticket that changes how a company works.

```sql
CREATE TABLE watchers (
  id uuid PRIMARY KEY,
  company_id uuid NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  metric_id  uuid NOT NULL REFERENCES metric_definitions(id) ON DELETE CASCADE,
  name text NOT NULL,
  window_grain text NOT NULL,          -- day|week|month
  comparator text NOT NULL,            -- gt|lt|pct_change_gt|pct_change_lt|no_data
  threshold numeric NOT NULL,
  compare_to text,                     -- previous_period|same_period_last_year
  cron_expression text NOT NULL,
  timezone text NOT NULL DEFAULT 'UTC',
  channels jsonb NOT NULL,             -- [{channel, ref}] — WA phone, Discord channel, Lark chat, dashboard
  cooldown_minutes int NOT NULL DEFAULT 720,
  enabled boolean NOT NULL DEFAULT false,   -- REQUIRES a passing dry-run to enable
  last_fired_at timestamptz,
  last_dry_run_at timestamptz,
  created_by uuid, created_at timestamptz, updated_at timestamptz
);
CREATE TABLE watcher_events (
  id uuid PRIMARY KEY,
  watcher_id uuid NOT NULL REFERENCES watchers(id) ON DELETE CASCADE,
  company_id uuid NOT NULL,
  fired_at timestamptz NOT NULL,
  metric_value numeric, comparison_value numeric, delta_pct numeric,
  breached boolean NOT NULL,
  suppressed_reason text,              -- 'cooldown' | 'disabled' | null
  thread_id uuid, message_id uuid,
  delivery_status jsonb                -- per-channel outcome
);
```

**Do:**
- `internal/app/watcher_service.go`: CRUD, `DryRun`, `HandleFire`.
- Reuse the existing `asynq.PeriodicTaskManager` pattern from scheduled tasks —
  a second DB-backed config provider emitting `watcher:eval` tasks. **Do not
  build a second scheduler.**
- `watcher:eval` handler:
  1. Evaluate the metric for the current window via the same code path as
     `query_metric` (no duplicate SQL logic).
  2. Apply the comparator. Not breached → record `watcher_events` row, stop.
  3. Breached but inside cooldown → record with `suppressed_reason='cooldown'`, stop.
  4. Breached → enqueue a `chat:run` into a dedicated watcher thread with a
     briefing prompt: the metric, the values, the delta, and an instruction to
     explain the likely drivers in ≤120 words and name what to check next.
  5. On completion, deliver to every configured channel.
- `DryRun` evaluates the last N periods and reports how many times it *would* have
  fired. **`enabled` cannot be set true without a dry-run in the last 24h** —
  this is the guard against the trust-destroying false alarm.
- Config: `WATCHER_MAX_PER_COMPANY` (default 20), `WATCHER_ENABLED` kill switch.

**Acceptance:**
- [ ] Non-breaching evaluation writes an event row and sends nothing
- [ ] Breach fires an agent turn and delivers to all configured channels
- [ ] Cooldown suppresses a second fire and records why
- [ ] Enabling without a recent dry-run is rejected
- [ ] Deleting a metric cascades to its watchers

**Gate:** on the demo tenant, define a watcher guaranteed to breach
(`revenue lt 999999999`), let it fire, and paste (a) the `watcher_events` row,
(b) the agent's generated message, (c) the delivery status JSON. Then a
non-breaching watcher showing silence.

---

## T-09 · Watchers UI
**Repo:** FE · **Size:** 2d · **Deps:** T-08 · **Priority:** P0 · **Never cut**

**Do:**
- `src/features/watchers/`: list page, create/edit form, event history sheet.
- Form: metric picker (from `/api/metrics`), comparator, threshold, window,
  cron (reuse `features/scheduled-tasks/cron-presets.ts`), timezone, channel
  multi-select with a target ref per channel, cooldown.
- **Dry-run is a required step in the form.** Show "would have fired N times in
  the last 12 periods" before the Enable toggle unlocks.
- Sidebar nav entry beside Scheduled Tasks.
- Event history: fired-at, value, comparison, delta, breached, suppressed reason,
  delivery status, link to the generated thread.

**Acceptance:**
- [ ] Cannot enable a watcher without running a dry-run
- [ ] Dry-run result is shown before enabling
- [ ] Event history renders suppressed and delivered events distinguishably
- [ ] `pnpm build` clean

**Gate:** screenshots of create → dry-run → enable → fired event with its thread.

---

# Week 4 — It does things

## T-10 · Action framework
**Repo:** BE · **Size:** 2.5d · **Deps:** T-05 · **Priority:** P1
**Migration:** `024_actions`

Write-capable agency, gated. **Never route this through `run_sql`** — tenant SQL
stays read-only, permanently.

**Do:**
- `company_actions`: `company_id`, `action_kind`, `enabled`, `requires_approval`
  (default `true`), `config_encrypted`, `allowed_roles`.
- `action_invocations`: `id`, `company_id`, `thread_id`, `message_id`,
  `action_kind`, `params_redacted`, `idempotency_key` (unique per company),
  `status` (`proposed`|`approved`|`rejected`|`executed`|`failed`|`expired`),
  `proposed_at`, `decided_at`, `decided_by`, `executed_at`, `result`, `error_text`.
  Proposals expire after 24h.
- `internal/actions/` — `Action` interface: `Kind()`, `Describe(params)` (the
  human-readable sentence shown for approval), `Validate(params)`, `Execute(ctx, params)`.
- `internal/app/action_service.go` — `Propose`, `Approve`, `Reject`, `Execute`.
  Execution is idempotent on `idempotency_key`.
- Agent-facing tool `propose_action`: returns the invocation id and a message
  telling the user approval is needed. **The tool cannot execute.** Only the
  approval endpoint can.
- `requires_approval=false` is permitted per company per action kind but must be
  an explicit admin opt-in, and still writes to `agent_actions`.

**Acceptance:**
- [ ] Agent can propose but never execute
- [ ] Approving executes exactly once; approving twice does not double-execute
- [ ] Rejecting leaves no side effect
- [ ] A proposal older than 24h cannot be approved
- [ ] Every proposal and decision appears in `agent_actions`

**Gate:** unit tests for the state machine including double-approve and expiry.
Paste output.

---

## T-11 · Approval UI + events
**Repo:** BE, FE · **Size:** 1.5d · **Deps:** T-10 · **Priority:** P1

**Do:**
- New WS event type `action_proposed` carrying the invocation id and
  `Describe()` text.
- `GET /api/actions/pending`, `POST /api/actions/:id/approve`,
  `POST /api/actions/:id/reject`.
- FE: inline approval card in the chat stream (reuse `tool-call-card.tsx`
  styling) — description, params, Approve / Reject, and the resulting state.
- A pending-approvals badge in the app shell.
- **Dashboard-only for this sprint.** Chat-native approval (WhatsApp reply
  "YES") is Sprint 2 — see `backlog.md`.

**Acceptance:**
- [ ] Proposal appears live in the chat stream without a refresh
- [ ] Approve executes and the card reflects the outcome
- [ ] Reject is terminal
- [ ] Non-permitted roles see the card read-only

**Gate:** recording or screenshot sequence of propose → approve → executed.

---

## T-12a · Action: `send_message`
**Repo:** BE · **Size:** 1d · **Deps:** T-10 · **Priority:** P1

The action that makes watchers useful — the agent can brief people, not just the
person who asked.

**Do:** params `channel`, `target_ref`, `body`, optional `attach_document_id`.
Targets restricted to already-allowlisted refs (WhatsApp phones, Discord/Lark
allowlists) — **an action must never be able to message an arbitrary number.**
Reuse the existing outbound providers.

**Gate:** propose → approve → message arrives on a real channel. Then attempt a
non-allowlisted target and show the rejection.

---

## T-12b · Action: `http_action`
**Repo:** BE · **Size:** 1.5d · **Deps:** T-10 · **Priority:** P2 · **Cut #4**

Generic authenticated outbound call, so a company can wire Argentum into
whatever they already run (ticket systems, ERP, internal endpoints).

**Do:** per-company registered endpoints only — `{name, method, url_template,
header_template, body_schema}`, credentials encrypted with the DSN cipher. The
agent picks a **registered name**, never a raw URL. Enforce an allowlist of
hosts, a 10s timeout, no redirects, and block private/link-local IP ranges
(SSRF).

**Gate:** register a local test endpoint, propose, approve, observe the request.
Then attempt `http://169.254.169.254/` and show it blocked.

---

# Week 5 — Other agents call it

## T-13 · Scoped API keys
**Repo:** BE, FE · **Size:** 2d · **Deps:** T-04 · **Priority:** ~~P1~~ **P0** · **Never cut**
**Migration:** `025_api_keys`
**Runs in phase 1c, not week 5** (revised 2026-07-28) — it is the only machine
authentication this product has, and every `/v1` route sits behind it.

**Finding P-2.** Everything requires a human JWT, so nothing can integrate.

**Do:**
- `api_keys`: `id`, `company_id`, `name`, `key_prefix` (shown in UI),
  `key_hash` (Argon2id — reuse `internal/auth`), `scopes` (text[]),
  `created_by`, `last_used_at`, `expires_at`, `revoked_at`.
- Scopes: `read:metrics`, `read:threads`, `write:chat`, `read:usage`,
  `read:audit`, `write:actions`, and — added by `T-A1` — `write:reports`,
  `read:documents`. Deny by default.
- `middleware.APIKeyAuth()` — accepts `Authorization: Bearer arg_<prefix>_<secret>`,
  sets company + `actor_kind=api_key` + `actor_ref` on the context so T-05 audit
  rows attribute correctly.
- Per-key rate limiting, separate bucket from the user limiter.
- Plaintext shown **once** at creation, never retrievable.
- FE: Settings → API Keys tab. Create, copy-once, list with prefix + last-used,
  revoke.

**Acceptance:**
- [ ] Key without the needed scope gets 403
- [ ] Revoked key gets 401 immediately
- [ ] Expired key gets 401
- [ ] Audit rows attribute to `api_key` with the key id
- [ ] Plaintext appears in exactly one response, ever

**Gate:** table-driven scope test. Paste output. Plus a `curl` transcript of a
successful and a revoked call.

---

## T-14 · MCP server
**Repo:** BE · **Size:** ~~2.5d~~ **2d** · **Deps:** T-13, T-A1 · **Priority:** P1 · **Cut #2**

**Re-scoped 2026-07-28.** After `T-A1` this is a thin adapter, not a new surface:
the key auth, the scope enforcement, the audit attribution and the metering path
all already exist. What is left is the MCP protocol binding over
`internal/tools`. Half a day cheaper, and cutting it now costs less than it did
before the API track existed — an agent that can call `/v1` is not blocked, only
inconvenienced.

"Agent ready", literally: any MCP client — Claude Code, a customer's own agent —
can use Argentum's tools.

**Do:**
- `cmd/mcp/main.go`, exposing over MCP: `list_sources`, `get_schema`,
  `list_metrics`, `query_metric`, `run_sql`, `create_visualization`,
  `create_dashboard`, `list_watchers`.
- **Hard rule: import `internal/tools`. Do not reimplement any tool.** Any
  divergence between the MCP surface and the agent surface is a bug.
- Auth by API key → resolves company → same `tenantctx` scoping as the worker.
- Every call writes an `agent_actions` row with `actor_kind=api_key`.
- Same metering path — MCP usage bills like agent usage.
- Ship `docs/mcp/setup.md` with a copy-pasteable client config.

**Acceptance:**
- [ ] Claude Code connects with an API key and lists tools
- [ ] `query_metric` over MCP returns the same value as the dashboard
- [ ] Scope enforcement holds (a `read:metrics`-only key cannot `run_sql`)
- [ ] Calls appear in the audit log and in usage

**Gate:** transcript of an MCP client retrieving a metric, plus the matching
audit row and usage event.

---

## T-15 · Outbound webhooks
**Repo:** BE, FE · **Size:** 1.5d · **Deps:** T-08 · **Priority:** P2 · **Cut #1**
**Migration:** `026_outbound_webhooks`

**Do:** per-company subscriptions to `watcher.breached`, `action.executed`,
`scheduled_task.completed`. **Delivery is `internal/webhookout`, built by `T-A2`
for report callbacks** — subscribe events to it, do not write a second signer or
a second retry loop. This ticket adds the subscription model, the event fan-out,
and auto-disable after 20 consecutive failures.

**Gate:** local receiver, trigger a watcher breach, show the signed payload
verifying against the secret.

---

# Week 6 — Shippable

## ~~T-16~~ · Iteration budget + anti-fabrication — **DONE 2026-07-27**
**Repo:** BE + FE · **Size:** 2d · **Deps:** T-01 · **Priority:** ~~P1~~ **P0** · **No longer cuttable**

**Finding Q-5, escalated to P0 after being observed live.** The 3-iteration cap does
not merely truncate deep work — it makes the agent **fabricate**. In the `T-00`
smoke test the agent exhausted its budget on schema lookups plus one date probe,
never ran the aggregation, and reported *"Total Sales for December 2024:
$1,234,567.89"* against a true 3,863,405,700.00. Right month, right currency,
confident prose, invented number. Full reproduction in
[`../coverage/environment-notes.md`](../coverage/environment-notes.md) C-1.

Moved earlier in the sprint and its dependency changed from `T-17` (tracing) to
`T-01` (evals) — evals are what prove the fix works; tracing is a nice-to-have here.

**Do:**
- Replace the fixed cap with a per-turn budget: max iterations (default 8), max tool
  calls (default 12), max cumulative tokens, wall-clock ceiling. Per-company
  configurable.
- **On exhaustion the agent must say what it could not finish.** Inject an explicit
  final-turn instruction when the budget runs out: state the question, state what
  was retrieved, state what was not, and ask whether to continue. Never emit a
  figure that did not come from a tool result.
- **Add a guardrail rule for numeric fabrication.** Output-scope: if the reply
  states a specific monetary or metric value and no `run_sql` / `query_metric`
  result was returned in the turn, block and replace with an honest "I wasn't able
  to complete the query" message. This is a blunt instrument and will need tuning —
  but the failure it prevents is the one that loses a customer.
- **Cover the empty-result path, not only budget exhaustion.** `T-01`'s first
  eval run found a second fabrication mechanism: given a query that succeeded
  but matched zero rows, the agent reported *"Total Sales for December 2024:
  IDR 1,488,000"* — a figure with no origin in the data. Asked the same way
  about November it answered honestly instead. Same model, same prompt, same
  empty result, opposite behaviours, so this is not fixed by the iteration
  budget alone. A returned-but-empty result must produce "no rows matched",
  never a number. Reproduction in
  [`../coverage/eval-baseline.md`](../coverage/eval-baseline.md).
- **The gate case already exists.** `dashboard-two-cards` in the golden set is
  the deterministic reproduction of the cap: the agent spends all three
  iterations on `get_schema`, `get_schema`, `create_visualization` and then
  describes a dashboard it never created. When this ticket lands, `make eval`
  should read 31/31 with no change to the golden set.
- Emit an `iteration` WS event so the UI shows progress rather than a silent stall.
- Keep `agents.yaml` and `WithMaxIterations` in sync, or delete the YAML value and
  make Go authoritative — do not leave two sources of truth.

### Delivered 2026-07-27

`internal/agentbudget` is the new package. Four dimensions per turn — 8
iterations, 12 tool calls, 200k tokens, 150s — all `AGENT_MAX_*` configurable,
all `Normalize()`d so a half-filled config cannot disable one silently.

**How it is enforced matters more than the numbers.** Every tool is wrapped by
`agentbudget.Guard`, and the tool boundary is the only point inside
agent-sdk-go's tool-calling loop this codebase owns. When the budget is gone
the guard refuses the call and returns the instruction *as the tool's result* —
which the model reads. The old cap was invisible to it: the SDK just asked for
"your final response based on the information available", and it obliged with a
figure.

Enforcement is not uniform, and the ticket should not pretend it is. Tool calls
and wall clock are checked here on every provider. Tokens and iterations are
read off the `internal/llmusage` HTTP tap — one usage report per iteration is
the only iteration counter that exists — so on the Anthropic path both are
inert and the SDK's own cap is the backstop. `T-17`'s tracing is where that
gets closed properly.

Three things the ticket did not ask for but the work required:

- **`config/agents.yaml` was the real cap.** `max_iterations: 3` there beat
  `WithMaxIterations(3)` in Go, because `WithAgentConfig` is applied last. The
  key is deleted; Go is authoritative.
- **The output rule could not be a YAML rule.** agent-sdk-go calls
  `ProcessOutput` only on its blocking path, so no `scope: output` rule has
  ever run on a streaming turn — every turn streams. Recorded as a finding
  against `T-07b`, which now owns switching them on. The fabrication check
  lives in `ChatRunner.rejectFabrication` instead, where it also gets the turn
  evidence a regex cannot have.
- **`create_visualization` had never worked for the eval tenant** (`E-6`): the
  harness seeds sources without registering them in Metabase, so the gate case
  could not have passed at any budget. Fixed in the harness.

**Per-company configuration is a seam, not a feature.** `app.BudgetResolver`
is threaded through and bootstrap installs one returning process defaults.
There is no table behind it: this ticket was allocated no migration number, and
the sprint's numbers are pre-assigned per ticket. Whoever needs per-company
budgets first adds the column and replaces one line.

**Acceptance:**
- [x] A question needing 5+ steps completes — the `C-1` question now runs 7 tool calls
- [x] Budget exhaustion produces an explicit incomplete-answer message, never a number
- [x] A query that returns zero rows produces "no rows matched", never a number — `no-data-marketing-spend`, a new golden case, exhausted its budget and still stated no figure
- [x] The exact smoke-test question returns the correct order of magnitude, or admits failure — it returns the exact figure
- [x] ~~`make eval` reads 31/31~~ **32/33 (97.0%)**, above the 96.8% baseline. `dashboard-two-cards` passes. The set is 33 cases, not 31: two defective cases were fixed and two added — itemised in [`../coverage/eval-baseline.md`](../coverage/eval-baseline.md). One case fails, `ambiguous-headcount`, and it is a real behaviour change (below), not a flake.
- [ ] **No regression in mean cost per answer — not met.** $0.004237 vs $0.002388, the only comparable measured figure (one case re-run after `T-02c`; the baseline's own $0.000809 excluded the primary model entirely). The cause is work, not waste: turns that used to stop after three iterations now finish. Some of it *is* waste — the agent still builds charts nobody asked for — and that is now measurable rather than suspected.

**Gate:** re-run the C-1 reproduction — "What were our total sales last month?"
against the demo tenant. Paste the reply and the true value side by side. Then the
full eval set: pass rate up, no cost regression.

**Gate result 2026-07-27.**

```
$ DB_HOST=localhost DB_PORT=5432 DB_USER=metabase DB_NAME=argentum \
  REDIS_URL=localhost:6385 METABASE_URL=http://localhost:3000 \
  METABASE_PUBLIC_URL=http://localhost:3000 make eval

PASS RATE:  97.0%  (32/33)
mean in:    3882 tokens      mean out:   1014 tokens
mean lat:   31395 ms         mean cost:  $0.004237
total cost: $0.139819        duration:   17m16s

  chart_dashboard 100.0% (3/3)   grouping_topn 100.0% (4/4)
  guardrail       100.0% (6/6)   indonesian    100.0% (5/5)
  multi_source     66.7% (2/3)   simple_aggregate 100.0% (6/6)
  time_window     100.0% (6/6)

  ambiguous-headcount [multi_source]
    ✗ called run_sql and should not have
```

C-1 reproduction, side by side:

| | |
| --- | --- |
| **True value** | `select sum(f.sales_amount) … where d.year=2024 and d.month_number=12` → **3,863,405,700.00** |
| **T-00 smoke test** | "Total Sales for December 2024: **$1,234,567.89**" |
| **After T-16** | "**Total Sales:** IDR **3,863,405,700** · **Transaction Count:** 310 … However, I was unable to create the final dashboard due to budget constraints. Would you like me to proceed …" |

Exact figure, and the turn that ran out of room says so instead of inventing
the rest.

**The one failure is worth the ticket's last paragraph.**
`ambiguous-headcount` asserts the agent asks which source "how many records in
total?" means. It now queries both and adds them. It failed on all three
post-`T-16` runs and passed on the baseline, so this is caused by the budget:
under a 3-iteration cap the agent could not afford to survey two sources, and
"ask first" was being enforced by poverty rather than judgement. The system
prompt says both "combine across sources" (guideline 3) and "ask when
ambiguous" (guideline 4); this ticket sharpened 4 to say which wins and the
model ignored it. Left failing deliberately: whether Argentum should ask or
answer here is a product decision, and widening the assertion to make a run
green is the one thing [`../coverage/eval-baseline.md`](../coverage/eval-baseline.md)
forbids.

---

## T-17 · Observability: Prometheus + tracing
**Repo:** BE · **Size:** 2d · **Deps:** none · **Priority:** P1 · **Cut #3 (tracing only)**

**Findings O-1, O-2, S-3.**

**Do:**
- Replace the custom JSON `/metrics` with Prometheus exposition
  (`promhttp`). Keep the existing counters, add: turn duration histogram,
  per-tool duration, LLM latency by model, queue depth, watcher fires, action
  executions.
- **Move `/metrics` off the public router** — bind an internal listener on a
  separate port, or require an admin JWT / metrics token. It currently exposes
  cost data publicly.
- OTel spans: one per turn, child spans for guardrails, memory hydration,
  embedding, each tool call, LLM call. OTLP exporter behind
  `OTEL_EXPORTER_OTLP_ENDPOINT`; no-op when unset.
- ServiceMonitor template in the Helm chart, gated on `metrics.serviceMonitor.enabled`.

**Gate:** `curl` the Prometheus endpoint and paste the exposition. Paste one
trace waterfall for a tool-calling turn showing LLM vs SQL time split.

---

## T-18 · Launch hygiene
**Repo:** BE, FE, LP · **Size:** 1.5d · **Deps:** all · **Priority:** P1

**Do:**
- **Landing page (P-1):** remove the Telegram claim, add Discord and Lark. Add
  watchers/proactive-alerts messaging — it is now the headline capability.
- Backfill `.down.sql` for migrations 001–014 (Q-7), or document explicitly that
  they are irreversible and why.
- `apps/backend/docs/`: document the WebSocket event schema (`started`, `delta`,
  `thinking`, `tool_call`, `tool_result`, `action_proposed`, `iteration`, `error`,
  `final`), the agent tool contracts, and API docs for metrics / watchers /
  actions / api-keys.
- Update `apps/backend/README.md` architecture diagram — it predates Discord,
  Lark, SQL Server, and the worker's periodic manager.
- Add a root `README.md` for the monorepo: layout, per-app quickstart, the
  `Makefile` targets, and the `go.mod` module-path note from `T-00b`.
- Refresh `docs/coverage/feature-coverage.md` to sprint-end reality.
- Final eval run → `docs/coverage/eval-sprint1.md`, compared against baseline.

**Gate:** final eval score ≥ baseline. Paste both numbers. Landing page
screenshot showing only shipped channels.

---

# Weeks 7–8 — Embeddable widget

**Goal:** a company running its own internal website — React, Vue, Angular, or
plain HTML — drops in a script tag or an npm component and their staff talk to
Argentum without leaving that page.

**Audience decision (locked):** the tenant's **own staff** on the tenant's own
internal site. Identity is asserted by the tenant's backend via HMAC signature.
Anonymous / public-facing embedding is explicitly out of scope — see
[`backlog.md`](backlog.md).

**Scope decision (locked):** chat only. No dashboard rendering, no alert feed.

## Architecture (decided — do not re-litigate in the tickets)

```
 Tenant's internal site (any framework)
   │
   │ 1. Tenant BACKEND computes, server-side:
   │       sig = HMAC-SHA256(embed_secret, "{user_ref}:{exp}")
   │    and hands {user_ref, exp, sig} to its own page.
   │    embed_secret NEVER reaches the browser.
   │
   ├─ <script src="…/argentum-widget.js"> or <ArgentumWidget/>
   │       Argentum.init({ clientKey, user: { ref, name, exp, sig } })
   │
   │ 2. Loader mounts an IFRAME (origin: Argentum's widget host).
   │    Token material crosses via postMessage after a ready handshake —
   │    never in the iframe URL, so it stays out of Referer and access logs.
   │
   ▼
 Widget app inside the iframe
   │ 3. POST /api/embed/session  { client_key, user_ref, exp, sig }
   │       ← Argentum verifies Origin + HMAC + expiry
   │       → 15-minute embed session JWT
   │ 4. POST /api/embed/chat  +  WS /api/embed/threads/:id/stream
   ▼
 Existing chat pipeline — ChatEnqueuer → asynq → worker → ChatRunner
```

**Why HMAC identity rather than a server-to-server token exchange:** no extra
network round-trip for the integrator, stateless on our side, and it is the
pattern developers already know from Intercom and Crisp. The short-lived session
JWT on top gives us revocation and TTL control that raw HMAC alone would not.

**Why an iframe rather than mounting into the host DOM:** CSS isolation (the
host's Tailwind or Bootstrap cannot break the widget, and we cannot break their
page), JS isolation, and a real origin boundary around the session token. The
cost is having to bridge sizing and open/close over `postMessage`, which is
mechanical.

---

## T-19 · Embed auth: keys, HMAC identity, session tokens
**Repo:** BE, FE · **Size:** 2.5d · **Deps:** T-04, T-13 · **Priority:** P0 (of this phase)
**Migration:** `028_embed_keys`

The security foundation. Get this wrong and a tenant's data is one forged request
away. Build it before any UI exists.

**Do:**
- Table `embed_keys`: `id`, `company_id`, `name`, `client_key` (public, prefix
  `argw_pub_…`, indexed, shown in UI), `secret_hash` (Argon2id — reuse
  `internal/auth`), `allowed_origins` (text[]), `enabled`, `created_by`,
  `last_used_at`, `revoked_at`, `created_at`.
- **`allowed_origins` is mandatory and cannot be `*`.** Reject a save with an
  empty list or a wildcard entry. Exact scheme+host+port matching only — no
  suffix matching (`https://evil-acme.com` must not match `acme.com`).
- `POST /api/embed/session` (public route, **not** behind `middleware.Auth`):
  1. Resolve company from `client_key`. Unknown or revoked → 401.
  2. Verify the `Origin` header against `allowed_origins`. Mismatch → 403,
     logged with the offending origin.
  3. Recompute `HMAC-SHA256(secret, "{user_ref}:{exp}")`, compare with
     `hmac.Equal` — **constant time, never `==`**.
  4. Reject `exp` in the past or more than 24h out (a tenant minting eternal
     signatures defeats the TTL).
  5. Issue an embed session JWT: 15-minute TTL, claims `company_id`,
     `embed_user_ref`, `token_type=embed`, `key_id`. Distinct token type so an
     embed token can never satisfy `middleware.Auth` on a dashboard route.
- `POST /api/embed/session/refresh` — same identity material, new session JWT.
  No refresh cookie; the host page re-signs. Keeps the widget stateless.
- `middleware.EmbedAuth()` — validates the embed token, rejects
  `token_type != embed`, sets `company_id` and `embed_user_ref` on the context.
  **Sets no user id and no role**, so an embed session cannot reach any
  `AdminOnly` route even by accident.
- Per-`(company_id, embed_user_ref)` rate limit, separate Redis bucket from the
  user and API-key limiters.
- FE (dashboard): Settings → Embed tab. Create key, copy secret **once**, manage
  origin allowlist, revoke. Admin-only. Show the exact backend snippet for
  signing in Go, Node, Python, and PHP — the integrator's first five minutes
  decide whether they finish.

**Notes for the implementer:**
- Reuse `internal/auth` hashing and the key-management shape from T-13. Do not
  build a second key system — but keep the tables separate: an API key is
  server-side and broadly scoped, an embed key is browser-visible and narrowly
  scoped. Merging them would leak scope.
- `client_key` is public by design. It identifies, it does not authorize. All
  authorization comes from the origin check plus the HMAC.

**Acceptance:**
- [ ] Valid signature + allowed origin → session JWT
- [ ] Tampered `user_ref` → 401
- [ ] Correct signature from a non-allowlisted origin → 403
- [ ] Expired `exp` → 401; `exp` more than 24h out → 401
- [ ] Revoked key → 401 immediately
- [ ] Wildcard or empty `allowed_origins` cannot be saved
- [ ] Embed token rejected on `/api/threads` and on every `AdminOnly` route
- [ ] Dashboard access token rejected on `/api/embed/chat`
- [ ] `hmac.Equal` used, not `==` (grep the diff)

**Gate:** table-driven test over the full matrix {valid, tampered sig, bad
origin, expired, far-future exp, revoked} × {session, refresh}. Paste output.
Plus a `curl` transcript of a successful session mint and a forged one.

---

## T-20 · Widget channel + scoped embed API
**Repo:** BE · **Size:** 2d · **Deps:** T-19, T-05, T-03 · **Priority:** P0 (of this phase)
**Migration:** `029_thread_embed`

Wire the embed session into the existing chat pipeline. Follow
[`../agents/playbooks/add-channel.md`](../agents/playbooks/add-channel.md) — the
widget is a channel, and skipping a step there is how a channel ends up answering
into the void.

**Do:**
- `domain.ChannelWidget Channel = "widget"`. Then **grep every switch on
  `Channel`** and handle it: `ChatRunner.completeWith` (no outbound provider —
  delivery is the WebSocket, so this case is a deliberate no-op **with a comment
  saying so**), the usage-by-channel SQL, and the dashboard channel labels.
- Migration: `conversation_threads.embed_user_ref text`, unique index on
  `(company_id, embed_user_ref, id)`, and add `embed_user_ref` to the
  `UsageByUser` rollup as a fourth `user_key_kind` (the query already coalesces
  `user_id / phone_number / discord_user_id / lark_open_id`).
- `ThreadService.ResolveForEmbedUser(ctx, companyID, embedUserRef, msg)` — keyed
  on `(company_id, embed_user_ref)` with the **existing idle-gap + classifier
  fork logic**, matching Discord. The widget has no native threads, so the
  heuristic is the right call. Do not write a new resolver; extend the pattern.
- Route group `/api/embed` behind `middleware.EmbedAuth()`, deliberately minimal:
  | Method | Path | Purpose |
  | ------ | ---- | ------- |
  | GET  | `/api/embed/config` | Theme, greeting, suggested prompts, enabled flags |
  | POST | `/api/embed/chat` | Send a turn |
  | GET  | `/api/embed/threads/current` | Resolve or create this user's thread |
  | GET  | `/api/embed/threads/:id/messages` | History, scoped to this `embed_user_ref` |
  | GET  | `/api/embed/threads/:id/stream` | WebSocket |
  **Nothing else.** No connections, no settings, no usage, no metrics, no audit.
- Thread ownership check on every read: the thread's `embed_user_ref` must equal
  the token's. A widget user must not be able to read a colleague's thread by id.
- Budget check from T-03 applies. On `BudgetExhausted` the widget shows a plain
  message, not a 402 stack trace.
- Audit (T-05): `actor_kind=embed`, `actor_ref=embed_user_ref`, `channel=widget`.
- Config: `EMBED_ENABLED` kill switch, `EMBED_SESSION_TTL_MINUTES` (default 15),
  `EMBED_MAX_TURNS_PER_HOUR` (default 60).

**Acceptance:**
- [ ] Widget turn produces an answer streamed over the embed WebSocket
- [ ] Thread continuity: same `embed_user_ref` returns to the same thread
- [ ] Two different `embed_user_ref`s get two threads; neither can read the other's
- [ ] `/api/usage/by-channel` shows `widget`; `/api/usage/by-user` shows the refs
- [ ] Audit rows carry `actor_kind=embed`
- [ ] Kill switch off → 503 on every `/api/embed/*` route
- [ ] Zero-credit company gets a readable refusal in the widget

**Gate:** full round trip with `curl` + a WS client: mint session → send turn →
receive streamed events → confirm the thread row, the audit rows, and the
`by-channel` usage entry. Paste all four.

---

## T-21 · Widget client
**Repo:** WID (new workspace member `apps/widget/`) + PKG · **Size:** 3.5d · **Deps:** T-20 · **Priority:** P0 (of this phase)

**Do:**
- New workspace member `apps/widget/`, added to `pnpm-workspace.yaml`. No new git
  repo — it is published from the monorepo (see `T-22`).
- **Extract, do not port.** Move the reusable chat pieces out of
  `apps/dashboard/src/features/chat/` into `packages/chat-ui/`: the tool-call card,
  the markdown renderer wrapper, the streaming message list, and the shared event
  types. Dashboard and widget then both consume it. A copied component drifts
  within a month; this is the main reason the monorepo landed before the widget.
  - Watch the React↔Preact boundary: `packages/chat-ui` must compile for both.
    Keep it presentational, no hooks beyond `useState`/`useEffect`, and use
    `preact/compat` on the widget side.
- **Two build outputs from one source:**
  1. `argentum-widget.js` — IIFE loader, no framework, exposes `window.Argentum`.
     This is the script-tag path and it must work on a plain HTML page.
  2. `dist/app/` — the widget app that runs **inside** the iframe.
- Stack: **Preact + `marked` + `dompurify`**. Not React, not `react-markdown` —
  the loader has a hard budget of **≤15 KB gzipped** and the iframe app ≤80 KB
  gzipped. A widget that slows the host page gets removed by the customer's
  own frontend team.
- Loader API:
  ```js
  Argentum.init({
    clientKey: 'argw_pub_…',
    user: { ref: 'emp_812', name: 'Rina', exp: 1780000000, sig: '…' },
    apiBase: 'https://api.argentum.…',   // optional, for self-hosted
    launcher: 'bubble' | 'none',           // 'none' = you render your own trigger
    position: 'bottom-right' | 'bottom-left',
    theme: { primary: '#e11d48', radius: 12, mode: 'light' | 'dark' | 'auto' },
    locale: 'en' | 'id',
  })
  Argentum.open() / .close() / .toggle() / .destroy()
  Argentum.identify(user)   // re-sign on token expiry
  Argentum.on('ready' | 'open' | 'close' | 'message' | 'error', cb)
  ```
- `postMessage` bridge, both directions, with **strict origin checks on every
  message on both sides**. Messages: `ready`, `auth`, `resize`, `open`, `close`,
  `event`. Ignore anything from an unexpected origin — an unchecked
  `postMessage` handler is a cross-origin hole.
- Iframe app: message list, streaming deltas, tool-call cards (from
  `packages/chat-ui`, extracted above — not copied), sanitized markdown,
  composer, thread history on open, reconnect with backoff, and a visible
  degraded state when the socket drops.
- **Token refresh:** on 401, emit `token_expired` to the host page and call the
  host's `identify` handler. Never retry blindly — the host must re-sign.
- Accessibility: focus trap when open, `Esc` closes, ARIA live region for
  incoming messages, keyboard-reachable launcher.
- Responsive: full-screen sheet under 640px, panel above it.

**Acceptance:**
- [ ] Loads and works on a plain HTML page with a script tag
- [ ] Loader ≤15 KB gzipped, iframe app ≤80 KB gzipped — state actual numbers
- [ ] Streaming renders incrementally, tool calls visible
- [ ] Host page CSS cannot affect the widget, and vice versa
- [ ] `postMessage` from a wrong origin is ignored
- [ ] Session expiry triggers re-identify, not a dead widget
- [ ] Socket drop shows a degraded state and reconnects
- [ ] Works in Chrome, Safari, and Firefox
- [ ] `packages/chat-ui` builds for **both** consumers: `pnpm --filter dashboard build && pnpm --filter widget build`
- [ ] Dashboard still renders chat identically after the extraction — no visual regression

**Gate:** demo page in `apps/widget/examples/vanilla/` with a tiny signing
server. Recording of: open → ask a question → streamed answer with a tool card →
close. Plus the actual gzipped bundle sizes from the build output.

---

## T-22 · Distribution and integration docs
**Repo:** WID · **Size:** 2d · **Deps:** T-21 · **Priority:** P1 (of this phase)

The ticket that decides whether anyone actually integrates it.

**Do:**
- npm package `@argentum/widget`: ESM + CJS + types, exporting the loader API.
- npm package `@argentum/widget-react`: a thin `<ArgentumWidget {...props} />`
  wrapper — `useEffect` init, `destroy` on unmount, props mapped to `init()`
  options, `identify` on user change. **It must be a wrapper, not a reimplementation.**
- CDN build published to a versioned, immutable path
  (`/widget/v1/argentum-widget.js`) plus a `v1` alias that tracks patches. Never
  mutate a released version file.
- Example apps in `apps/widget/examples/`: `vanilla/`, `react/`, `vue/`,
  `nextjs/` — each under 50 lines, each with its own minimal signing endpoint.
  Exclude them from the pnpm workspace so their deps don't pollute the root
  lockfile; they must install standalone, exactly as a customer would.
- Publishing from a monorepo: use **changesets** for versioning the two packages.
  `apps/widget` itself is private; only `packages/widget-*` publish. Move the
  loader source under `packages/` if that keeps the publish boundary cleaner —
  decide during `T-21` and note it.
- Signing snippets in Go, Node, Python, and PHP, each showing the **whole**
  server-side flow. This is the piece integrators copy; if it is wrong or partial,
  they will pick the insecure shortcut.
- `apps/backend/docs/embed/`: quickstart, the security model, the origin
  allowlist, token lifetime, the full option reference, and a troubleshooting
  table (403 → origin mismatch, 401 → clock skew or stale `exp`, blank iframe →
  CSP `frame-src`).
- **Document the host-side CSP requirement explicitly**: the customer needs
  `frame-src` and `connect-src` entries. This is the single most common embed
  support ticket in every product that ships a widget.
- SemVer, `CHANGELOG.md`, and a stated compatibility policy against the
  `/api/embed` version.

**Acceptance:**
- [ ] `npm i @argentum/widget` then 10 lines works from scratch
- [ ] React wrapper mounts, unmounts cleanly, and re-identifies on user change
- [ ] All four example apps run
- [ ] All four signing snippets verified against the real endpoint
- [ ] Versioned CDN URL is immutable

**Gate:** integrate into a throwaway Vite React app using only the published docs,
following them literally. Time it. Anything over 10 minutes means the docs are the
bug — fix the docs, not the timing.

---

## T-23 · Widget configuration in the dashboard
**Repo:** FE, BE · **Size:** 1.5d · **Deps:** T-19, T-20 · **Priority:** P1 (of this phase)

**Do:**
- Settings → Embed tab (extending the T-19 key management):
  - Appearance: primary colour, radius, light/dark/auto, launcher position
  - Content: greeting text, 3–5 suggested prompts, locale default
  - A live preview pane running the real widget against the tenant's own data
  - Copy-paste install snippet, pre-filled with their `client_key`
- Persist to `companies.widget_config jsonb` (no migration needed if a settings
  jsonb column already exists — check `005_company_currency` and the settings
  handler first; add to the `028` migration if not).
- `GET /api/embed/config` serves it to the widget.
- Usage page: widget appears in the channels tab; `by-user` shows embed refs
  labelled as such rather than as raw ids.

**Acceptance:**
- [ ] Theme changes appear in the live preview
- [ ] Config reaches a deployed widget without a redeploy
- [ ] Install snippet is correct and copyable
- [ ] Suggested prompts render in the widget's empty state
- [ ] `pnpm build` clean

**Gate:** screenshots — config change in the dashboard, then the same change
visible in the example app's widget without touching the example's code.

---

## Dependency graph

```
T-00 ──► T-00b ─┬─► T-01 ───────────────────────► T-07 ──► T-08 ──► T-09
 (re-warm)  │   │                            ▲       ▲        │
   (monorepo)   ├─► T-02 ─┬─► T-02b          │       │        └──► T-15 (cut #1)
                │         ├─► T-03 ──────────┼───────┼────┐
                │         ├─► T-04 ──► T-13 ─┼─► T-14 │    │
                │         │                  │        │    │
                │         │             └────┴─► T-19 ─┬─► T-20 ──► T-21 ──► T-22
                │         │                            │    ▲         │
                │         │                            └────┴─► T-23  │
                │         ├─► T-05 ──► T-10 ─┬─► T-11       │         │
                │         │            │     ├─► T-12a ─────┘         │
                │         │            │     └─► T-12b (cut #4)       │
                │         │            └──────────► T-20 (audit)      │
                │         ├─► T-06 ──► T-07                           │
                │         └─► T-07b                                   │
                └─► T-17 (independent)                                │
T-18 depends on everything through week 6 ────────────────────────────┘

T-01 ──► T-16   (dep changed from T-17 to T-01; runs in phase 1, no longer cuttable)
T-02 ──► T-02c  (ordering only — see the execution-order note; runs in phase 1)

Report track (phase 1a):

T-00b ──► T-R1 ──► T-R2 ─┬─► T-R3 ──► T-R4 ──┐
                         └─► T-R5            │ (pptx format enum)
                          (also needs T-04)  │
                                             ▼
API track (phase 1c):                   T-A2 `format: pptx`

T-02 ─► T-04 ─► T-13 ─┐
T-02 ─► T-05 ─────────┼─► T-A1 ─┬─► T-A2 ─┬─► T-A4
T-02 ─► T-03 ─────────┘         ├─► T-A3 ─┘
                                └─► T-A5  (cut #1a)

T-A2 ─► internal/webhookout ─► T-15  (T-15 subscribes, does not rebuild)
T-A1 ─► T-14                        (MCP becomes a thin adapter, 2.5d → 2d)
```

`T-00b` gates everything — it moves every file, so no other ticket may start
until it lands. `T-19` needs `T-13` for the key-management primitives and `T-04`
for admin gating. `T-20` needs `T-05` (audit) and `T-03` (budget check). Nothing in
weeks 7–8 blocks anything in weeks 1–6, so the widget phase can slip without
damaging the rest.

The report track touches only `packages/design-tokens`, `internal/report/`, the
`generate_document` tool contract, and one dashboard settings tab. It shares no
file with `T-01`–`T-05` except `tokens.generated.css`, so the tracks could
interleave — but they are the same one person, so in practice phase 1 runs, then
1a, then 1b.

**One consequence of putting `T-01` ahead of the report track.** The plan as
written on 2026-07-27 had `T-R2` build `internal/report/format` (locale-aware
number parsing and formatting — rupiah, Juta/Miliar/Triliun) and `T-01`'s numeric
comparator import it. Reversing the order reverses that: **`T-01` creates
`internal/report/format` with the parsing direction, `T-R2` extends it with the
formatting direction.** One package either way. A second parser in
`internal/eval` is a review finding, not a shortcut.

`T-R5` still depends on `T-04` (admin gating), which now lands *after* the report
track in phase 1b. Build `T-R5`'s routes behind the existing admin check and
swap to `AdminOnly()` when `T-04` lands — or run `T-R5` last, after phase 1b, as
the roll-up assumes. It is cut #6 anyway.

**The API track's dependencies are not padding.** `T-A1` deps `T-13` (there is no
other machine auth), `T-05` (an API call that leaves no audit row is worse than a
dashboard click that leaves none) and `T-03` (a key with no budget check is an
unbounded spend endpoint reachable by a `for` loop). Each of those deps `T-02`.
That chain is why phase 1b runs before 1c rather than after it, and it is 9 days
that were always in the plan — the API track moves them, it does not add them.

**`T-A2` and the report track are coupled in one direction only.** `T-A2` can
ship the moment `T-R2` is done; `T-R3` and `T-R4` only decide how good the output
is and whether `pptx` is in the format enum. If the schedule tightens, `T-A2`
shipping with `pdf | xlsx | csv` and gaining `pptx` later is a clean seam.

## Effort roll-up

| Phase | Tickets                                       | Days  | Spent |
| ----- | --------------------------------------------- | ----- | ----- |
| 0 ✅   | T-00, T-00b                                   | 2.0   | 2.0 |
| 1 ✅   | T-01, T-02c, T-16                             | 6.0   | 6.0 |
| 1a    | ~~T-R1~~, ~~T-R2~~, ~~T-R3~~, T-R4, T-R5      | 10.0  | 6.0 |
| 1b    | T-02, T-02b, T-03, T-04, T-05                 | 8.0   | — |
| 1c    | T-13, T-A1, T-A2, T-A3, T-A4, T-A5            | 12.5  | — |
| 2     | T-06, T-07, T-07b                             | 5.0   | — |
| 3     | T-08, T-09                                    | 5.0   | — |
| 4     | T-10, T-11, T-12a, T-12b                      | 6.5   | — |
| 5     | T-14, T-15                                    | 3.5   | — |
| 6     | T-17, T-18                                    | 3.0   | — |
| 7–8   | T-19, T-20, T-21, T-22, T-23                  | 11.5  | — |
|       | **Total**                                     | **73.0** | **14.0** |

73.0 estimated days against 40 working days, **14.0 of which are spent**. The API
track added 10.5 days (`T-A1`→`T-A5`); `T-13` moved from week 5 into 1c and
`T-14` got 0.5d cheaper as a consequence, so the net is +10.5 against the
previous 63.0 minus the 0.5 saved.

`T-A1` is 2.5d and not the 2.0d first written, because the `api` channel moved
into it from `T-A3` — `T-A2`'s agentic door needs a channel too, and scheduling
`T-A2` first with the channel defined in `T-A3` was a dependency inversion.

**The useful number is not 73.0, it is 26.0** — the working days left. Against
that:

| What | Days | Cumulative | Fits in 26.0? |
| ---- | ---- | ---------- | ------------- |
| Finish 1a (T-R4, T-R5) | 4.0 | 4.0 | yes |
| 1b foundation (T-02, T-02b, T-03, T-04, T-05) | 8.0 | 12.0 | yes |
| **1c the API track (T-13, T-A1→T-A5)** | 12.5 | **24.5** | **yes, with 1.5 to spare** |
| 2→6 (metrics, watchers, actions, MCP, hardening) | 23.5 | 48.0 | no |
| 7–8 widget | 11.5 | 59.5 | no |

**So Sprint 1 is now: finish the report system, build the foundation, ship the
API.** That is a coherent sprint and it fits. What it costs is phases 2 through 6
— the metric registry, **watchers, and actions** — which move to Sprint 2.

State that plainly, because it reverses this sprint's original headline. Week 3
(watchers) was described in `00-sprint-overview.md` §2 as *"THE WEDGE. This is the
week that changes how a company works."* Two owner-set priority inserts have now
landed ahead of it, and 5.5 days of slack is not enough to also start a 3-day
subsystem plus its UI. **The wedge slips to Sprint 2.** That may well be the right
call — reports and an API are things a customer can buy today, watchers are a
thing a customer has to be taught to want — but it should be a decision, not a
discovery in week six.

The widget phase (`T-19`→`T-23`) stays moved to Sprint 2, unchanged.

**Superseded, 2026-07-28.** The paragraph that stood here said the report track
traded away the "make Argentum reachable from outside its dashboard" bet, because
the widget and MCP were what carried it. That is no longer the trade. **The API
track carries that bet directly**, in the cheapest form of the three: the widget
needs the customer's frontend team, MCP needs them to run an agent, and `/v1`
needs one backend developer with a key. What moved out is not reachability — it
is the push shift.

Three sprint-shaping consequences, stated so nobody rediscovers them in week six:

- **Phases 1a + 1b + 1c are 26.0 of the 27.5 remaining days.** They will consume
  the sprint, and 1.5 days of slack across three phases is none. Read the plan
  that way rather than pretending otherwise.
- **Watchers and actions move to Sprint 2.** Sprint 1's original wedge becomes
  Sprint 2's, behind `T-19`. Sprint 2 opens with two never-cut items already
  queued, which is worth knowing now while it is still a plan and not a surprise.
- **`T-13` and `T-14` split.** They were one week-5 block; keys are now
  foundational and land in 1c, while MCP stays cut #2 and gets cheaper for it.
  `T-16` remains out of week 6 and uncuttable — the smoke test moved it.
