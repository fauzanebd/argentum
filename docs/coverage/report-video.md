# Video and animated decks — `T-V1` → `T-V5` Record

The track that makes the same report spec render as a silent 1080p video and as
an animated deck at a link. Tickets in
[`../plan/01-tickets.md`](../plan/01-tickets.md); the insert and what it slipped
are [`../plan/00-sprint-overview.md`](../plan/00-sprint-overview.md) §8d.

**State: `T-V1`, `T-V2` and `T-V3` code complete, 2026-08-09. `T-V4`/`T-V5`
not started.** A plan renders to a real MP4 from a container, and `mp4` is a
format the API, the agent and the queue all understand. The live gate — a
video through `/v1` and through a real turn, on a stack with MinIO — has not
run.

---

## §T-V1 — `videoplan`: the projection, the plan contract, and the timing model

Shipped 2026-08-09. A report spec goes in and a **Plan** comes out: a list of
scenes in which every string is final, every line is already broken, every
duration is already counted in frames, and every image is already a data URI.
Nothing renders yet — that is `T-V2` — and nothing needs to for this half to be
checkable, which is why it was built first.

```
internal/report/canvas/     NEW — the 16:9 surface, shared by the deck and the video
   canvas.go                  dimensions, margins, type scale, wrapping, fitting
   table.go                   the table solver, moved out of pptx
internal/report/flow/       NEW — the spec → beats walk, shared by both
   flow.go                    Sink, Walk, LeadSentences, SplitSentences
internal/report/videoplan/  NEW — spec → Plan
   plan.go                    the contract: Plan, Scene, Metrics, Brand, Table, Chart
   build.go                   Options, Limits, the builder, the pre-check
   scenes.go                  the flow.Sink: one beat → one or more scenes
   timing.go                  the timing model
   testdata/*.plan.json       five golden plans
```

### 1. What was extracted, and why it was not optional

Three things moved out of `internal/report/pptx` before a line of video code was
written. None of them is refactoring for its own sake — each is a decision two
renderers would otherwise make separately:

| Moved | To | What breaks without it |
| ----- | -- | ---------------------- |
| Slide geometry, type scale, wrapping, fitting | `canvas` | The video wraps a line where the deck does not, so the same paragraph is four lines in one format and five in the other |
| The table solver — column typing, formatting, widths, row heights | `canvas` | A column that is 38% of the measure in the deck is 41% in the video, and a table breaks after row 12 in one and row 14 in the other |
| The section walk — what becomes a beat, what it is called, where prose goes | `flow` | A table continues onto a second slide but not onto a second scene, and the same report has quietly become two reports |

That is `T-R4`'s argument applied one renderer further out. It extracted
`measure`, `layout` and `labels` so the PDF and the deck could not disagree
about how wide a column is or what "Prepared for" is in Indonesian; a third
renderer in another language, in another process, cannot reach any of that
unless the decisions travel with the data.

**The deck's bytes did not change.** Proven rather than asserted: the five
fixtures were rendered before and after each extraction and compared by SHA-256.

```
ad717c52037ea950ecfbf5e208b3502fddbe5ddf18ed792dcbd8f35a4a1363ca  monthly_sales.json   82319 bytes
8c37dfb39c76ba05c8d1ecc98e2010ca168cd4ef7b4e108b9dd9f183efe43908  invoice.json         19603 bytes
3519384fd3453095459f50f221207ec9c8dce8c10e1cdb96e668c0d1fd130fc0  kpi_summary.json     33708 bytes
55812d7e0838948e8451f6894b26e36e14cac240fed9785c361864521001b4b5  export_200.json     135430 bytes
0b268636a2d78996e3a4fb7b82016b7402a81f75c6b7a36a2988d37b2ca68050  v1_legacy.json       16503 bytes
```

### 2. The finding: `338.667` is not 338.667

The first version of `canvas` wrote the surface width as a three-decimal
literal. OOXML's widescreen slide is 12192000 EMU and the deck derives its
millimetres from that, so the real number is `12192000 ÷ 36000` =
338.66666…mm. The literal is **0.00033mm wider**.

**All five deck fixtures changed** — between 29 and 375 bytes each — and **every
test still passed**, including the determinism test, the overflow test and the
structural gate. The difference reaches every measured column width, every
wrap decision and every EMU rounding in the package; none of them is asserted
against a fixed value, because until now there was only one renderer and
whatever it produced was correct by definition.

It was caught only because the extraction was checked by rendering before and
after and diffing the bytes. Two consequences:

- `canvas.WidthMM` is now written as the division, with the incident in its doc
  comment. This is the same lesson §8a of the sprint overview draws about a
  number copied into a summary: **a number copied into a second place stops
  tracking the first.**
- `TestSlideAndCanvasAgree` and `TestTwoPixelsPerPoint` in
  `internal/report/pptx/geometry_test.go` now assert the relationship rather
  than describing it.

### 3. The video frame is the PowerPoint slide

Not by analogy — arithmetically. The slide is 338.667 × 190.5 mm; 1920 × 1080 px
maps onto it at 5.669 px/mm, which is **exactly 2 px per point**. A 29pt slide
title is a 58px frame title. A line that fits on a slide fits in a frame,
because the same font metrics measured both.

That is what makes the plan's approach possible: **the plan carries lines, not
paragraphs.** The browser is asked to draw strings at fixed positions and never
to lay them out, so the one thing a second layout engine could disagree about is
not asked of it.

### 4. What the video does differently from the deck, deliberately

| | Deck | Video |
| - | ---- | ----- |
| A run of paragraphs | Packed onto one slide as bullets | **One scene each** — a frame is on screen for as long as its own text takes to read, so packing four paragraphs onto one either rushes three or holds the fourth for half a minute |
| A callout | Packed beside the bullets | **Its own scene** — it is the finding the reader must not miss |
| The prose | Speaker notes | `Scene.Notes`, for `T-V4`'s player |
| Everything else | — | The same projection: cover, dividers, KPI cards, table paging, closing |

### 5. The timing model

Written down in `timing.go` rather than tuned in a component, because a scene
that is on screen for the wrong length is not fixable by making its animation
slower.

- Reading pace **2.7 words/second** — slower than silent reading, faster than
  speech. The viewer is reading text they did not write about figures they are
  seeing for the first time, and **they cannot scroll back**; that last
  constraint is why this is not tuned to a reading-speed study.
- **1.2s** lead-in, **3.5s** floor, **15s** ceiling.
- Cover 4.0s, divider 2.0s, closing 3.0s — fixed, because they carry almost no
  text and a computed duration would flash past.
- Chart: +1.5s for the reveal before the caption is assumed to be read.
- Table: +0.45s per row; KPI: +0.9s per card. Scanned, not read — but twelve
  rows is not the same work as three.
- A figure counts as **one word** regardless of its digits. "Rp 3.863.405.700"
  is one thing the eye takes in, and `strings.Fields` would call it two and buy
  the scene an extra second for a currency symbol.

The monthly-sales fixture comes out as **13 scenes, 82.8 seconds**:

```
 0 cover       120f   4.0s  Laporan Penjualan Bulanan
 1 section      60f   2.0s  Ringkasan Eksekutif
 2 kpi         233f   7.8s  Ringkasan Eksekutif
 3 statement   425f  14.2s  Ringkasan Eksekutif
 4 quote       303f  10.1s  Ringkasan Eksekutif
 5 section      60f   2.0s  Rincian Bulanan
 6 chart       159f   5.3s  Pendapatan per bulan
 7 table       164f   5.5s  Rincian Bulanan
 8 table       150f   5.0s  Rincian Bulanan
 9 section      60f   2.0s  Catatan Metodologi
10 statement   447f  14.9s  Perlakuan pesanan yang dibatalkan
11 quote       214f   7.1s  Perlakuan pesanan yang dibatalkan
12 closing      90f   3.0s  Laporan Penjualan Bulanan
```

### 6. Deviation from the ticket: where the caps live

The ticket says *"extend `spec.Limits` with the first two so `/v1` rejects an
oversized request with `T-A2`'s existing typed error"*. **They are in
`videoplan.Limits` instead**, and the ticket is wrong rather than the code.

`spec.Limits` is enforced by `spec.CheckLimits`, which reads a spec and counts
rows, columns and sections. Scenes and frames are not properties of a spec —
deriving them means running the timing model, which lives here, and `spec`
cannot import `videoplan` because `videoplan` imports `spec`. Two fields on
`spec.Limits` that `spec` could not enforce would be configuration wearing the
wrong struct.

**What `T-V3` has to do instead**: read the caps from config into
`videoplan.Limits`, and translate the refusal into `T-A2`'s typed 400. The
refusal messages are written for that — they name the cap and the observed
value, in minutes and seconds rather than in frames.

### 7. Two claims that are proofs rather than assertions

**The chart in the video is the chart in the deck.** Not "both call
`chart.Render`" — `TestChartIsTheDeckImage` renders the fixture as a PPTX,
unzips `ppt/media/image1.png`, and compares it byte for byte with the base64 in
the plan. Locked decision 6 says animation is a mask over those pixels and never
a redraw; this is what stops that becoming a comment nobody checks.

**Nothing is rasterised before the limits are checked.** Every chart in
`TestOverlongSpecIsRefusedBeforeAnythingIsBuilt`'s document is deliberately
unrenderable, so if the projection had reached one the error would be the
chart's. Getting the limit's error back is the only way to show from outside the
package that no work was done — the same trick `T-A2` uses when it asserts that
nothing was uploaded. `TestAnUnrenderableChartIsAnError` is its control:
without the limit in the way, that same chart does fail the build.

### 8. Gate

Run locally 2026-08-09.

```
$ make vet          → clean
$ make lint-go      → 0 issues
$ go test ./...     → all packages pass
$ make types-check  → 6 generated files are current
```

The drift gate, proven red then green — `Plan.FPS`'s JSON tag renamed and
`make types` deliberately not run:

```
$ make types-check
api-types: 1 file(s) differ from the Go structs: videoplan.ts
Run 'make types' and commit the result.
make: *** [types-check] Error 1

$ # tag restored
$ make types-check
api-types: 6 generated files are current
```

**One pre-existing failure is not mine and is not fixed here.** `make check`'s
web half fails on `packages/argentum-python/src/argentum/types.py` being stale
against `apps/backend/openapi/v1.yaml`. It reproduces with every change in this
track stashed, it is fixed by
`pnpm --filter @argentum/openapi-tools build` and committing the result, and it
belongs to whoever last touched the OpenAPI document.

### 9. Where the types go

`packages/api-types/src/videoplan.ts`, generated by tygo from `plan.go`, exported
as `@argentum/api-types/videoplan` rather than from the barrel. Same reason the
webhook envelopes are kept out of it and a sharper one: `Brand`, `Table` and
`Metrics` are names a dashboard namespace should not be handed.

Two TypeScript consumers will compile against it — `apps/render` (`T-V2`) and
the dashboard's player (`T-V4`) — which is the whole argument for generating it:
a hand-written interface matching a Go struct is the exact defect `T-02b`
deleted four files to end.

### 10. What is not done

- Anything that renders. `T-V2`.
- The `mp4` format, the queue path, metering, the tool description. `T-V3`.
- `MaxPlanBytes` is defined and **not enforced** — nothing marshals the plan
  inside this package, so the check belongs at the boundary that does, which is
  `T-V3`'s client.
- The `RevealGrow` constant is defined and never chosen. `T-V5` decides whether
  a growing mask suits a bar chart better than a wipe; the wipe is the default
  until it does.

---

## §T-V2 — `apps/render`: the Remotion service and `packages/motion`

Shipped 2026-08-09, the same day as `T-V1`. A plan goes in over HTTP and an MP4
comes out. **It rendered:** the KPI fixture is 13 scenes and 1 016 frames, and it
encoded in 136 seconds on a developer machine, after 13 stills that took about a
second each.

```
packages/motion/            NEW — the compositions, drawn from the plan and nothing else
   plan.ts                    the contract re-exported, plus validate/partition/timeline
   anim.ts                    one curve, two durations — the whole motion vocabulary
   chrome.tsx                 Frame, TitleBand, Footer, Body, Lines
   scenes/index.tsx           one component per scene kind, and the switch
   Report.tsx                 the scenes as Sequences, and the font gate
   composition.tsx            the Composition, sized by calculateMetadata
   Root.tsx                   registerRoot, and nothing else
   public/fonts/              Space Grotesk, the same three TTFs the PDF embeds
apps/render/                NEW — the service
   render.ts                  bundle once, renderMedia, the encoding settings
   jobs.ts                    the job store, the wall clock, the TTL sweep
   server.ts                  five routes, no framework
   fixture.ts                 the CLI: a plan in, an MP4 and a still per scene out
   plan.test.ts               the checks that run before a browser is started
   Dockerfile                 the one image in this repository with a browser in it
```

### 1. The renderer holds no palette, no type scale and no layout

`T-V1` said the plan carries the strings. Building the components made it clear
that was half a decision, so the plan now also carries **the colours, the type
sizes in pixels, the margins, the radius and the spacing** — everything
`internal/report/theme` knows, resolved once per plan.

The payoff is a rule a grep can enforce: **a colour literal anywhere in
`packages/motion` is a defect.** The alternative — importing
`@argentum/design-tokens` into the compositions and checking that each hex
matches a token — is a rule only a human can apply, and `T-R1` already had to
delete a hand-written `:root` block whose HSL values had drifted from the hex
their own comments named.

Four fields were added to `Plan.Brand` for it (`surface`, `surface_subtle`,
`positive`, `destructive`), plus a `tones` map for callouts and four `Metrics`
fields for radius and spacing. Additive, so the plan version stays 1.

### 2. What is actually enforced by "the frame is the slide"

`Lines` draws one element per line with `white-space: pre` and no width. The
browser is never asked where to break, so it cannot break somewhere the Go
measurement did not. That is the whole return on `T-V1`'s pre-wrapping, and it
is why the table scene's rupiah column lands character for character where the
deck's does.

### 3. What the render found

**The `(cont.)` marker was drawn through the brand rule.** Exactly the class
`T-R4`'s gate found on the deck's cover — *"a one-line subtitle estimate came
back on two lines with the brand rule drawn through it"* — arrived at a
different way: the marker was an inline `span` after a stack of block-level
lines, so it wrapped onto its own line and landed on the rule. It is now
absolutely positioned in the band's opposite corner, where the band's fixed
height means it cannot collide with anything. Caught by looking at the 200-row
export's second table scene; no test would have.

**A golden plan is not a renderable plan.** `T-V1`'s goldens replace chart
images with a `sha256:` digest so the golden stays reviewable, and feeding one
to the renderer produces `net::ERR_UNKNOWN_URL_SCHEME` on that scheme — which is
what happened on the first attempt at the Indonesian fixture. `TestWritePlans`
(`ARGENTUM_PLAN_OUT=…`) now writes undigested plans, the same escape hatch the
deck has in `TestWriteDecks`, and both the test and the CLI say so.

**`.js` import specifiers break the bundler.** Remotion's webpack does not map
`./plan.js` onto `plan.ts`, so the NodeNext-style suffixes had to go. They are
extensionless throughout both packages now; `tsx` and the bundler agree on that
and nothing else needed changing.

### 4. Decisions inside the service

- **One job at a time, one replica.** Remotion already uses every core it is
  given, so two concurrent renders on one pod are slower than two sequential
  ones and twice as likely to be killed by a memory limit. The single replica is
  a stated limit with a trigger — a second tenant waiting — because fixing it
  means moving results to object storage, which costs the service its
  no-egress property.
- **Two timeouts.** Remotion's per-frame timeout catches a frame that hangs; a
  ten-minute wall clock catches everything else. Without the second, a pod is
  healthy, useless and holding a tenant's report forever.
- **The readiness probe renders.** `/healthz` reports the boot bundle, so a pod
  whose browser cannot start never receives a report — it fails the probe
  instead.
- **The log carries job ids, durations and frame counts, never a plan.** A plan
  is a customer's business figures, and this service has no tenant, no user and
  no thread to attach them to. It must not acquire one by way of a log line.
- **A caller's bad plan is a 400 even when it surfaces late.** `PlanError` is a
  distinct type for exactly that: the difference between an integrator fixing
  their spec and an integrator opening a ticket.

### 5. Deployment

`Dockerfile` is Node 22 on bookworm-slim with ffmpeg and Chromium's shared
libraries, the browser downloaded at **build** time — doing it on first request
puts a 94 MB download inside a tenant's first report, and doing it never is a pod
that needs egress it is not supposed to have. Non-root, read-only root
filesystem, `/tmp` and `/dev/shm` as sized emptyDirs.

The Helm chart adds `deployment-render.yaml`: one replica, `Recreate`, a
`ClusterIP` Service, **no ingress**, and a `NetworkPolicy` with `egress: []`.
`render.enabled` is **false by default** — it is the only image with a browser in
it and a deployment that never asks for an mp4 should not pay for one.

`docker-compose.yml` gains the service, so a video can be rendered on a developer
machine. That gap is what `T-S4`'s gate found when `make infra` shipped no object
storage and the deterministic example suite could not run locally.

### 6. Gate

Run locally 2026-08-09.

```
$ pnpm --filter @argentum/motion lint     → clean
$ pnpm --filter @argentum/render lint     → clean, 7 tests pass
$ pnpm render:fixture kpi_summary.plan.json out
  kpi_summary: 13 scenes, 1016 frames, 33.9s
  13 stills, then: video out/kpi_summary.mp4 (136.1s to render)
$ pnpm render:fixture monthly_sales.plan.json out --stills
  monthly_sales: 13 scenes, 2485 frames, 82.8s — 13 stills
```

The Indonesian fixture's table scene reads `Rp 3.863.405.700` and `3,3%` — the
`C-1` figure, in the document's own separators, in a video. The chart scene is
the Go-rendered PNG with Indonesian axis labels, under a caption and a footer
that say `Dibuat dengan Argentum` and `Rahasia — Internal`.

### 7. What is not done

- **`RevealGrow` is defined and never chosen**, and the chart's own white ground
  sits on the cream page exactly as it does in the PDF. Both are `T-V5`'s.
- **No still-comparison gate yet.** The stills are produced; comparing them
  against goldens within a perceptual tolerance is `T-V5`.
- **The cluster half of the deployment is unexercised.** The image builds and
  runs (§8); the Helm chart's readiness probe, the `egress: []` NetworkPolicy
  and the emptyDir sizing have never met a cluster.

### 8. The image, built — and four defects, 2026-08-09

The Dockerfile and the chart shipped with `T-V2` "written and unexercised".
Building them found **four defects in fourteen lines**, three of which would
have failed in a cluster and passed on a laptop, and one that could never have
worked anywhere.

| # | What | Why it was invisible |
| - | ---- | -------------------- |
| 1 | `COPY … package.json` — **there is no root `package.json` in this repository** | Every workspace has one. pnpm reads `pnpm-workspace.yaml` for the member list and the lockfile for what to install, so the root manifest was never needed — it was named because it usually exists |
| 2 | `ENV PNPM_HOME=/pnpm PATH=$PNPM_HOME:$PATH` on one line | A variable set in an `ENV` is not expanded by the same `ENV`, so `PATH` was prepended with an empty string. Docker prints this as a warning nobody reads |
| 3 | Unpinned corepack met a pnpm whose `minimumReleaseAge` refuses any package published in the last 24 hours | Which is a description of a lockfile somebody updated yesterday — this one, by `T-V2`, the day before. The image would have started failing on a day nobody touched it |
| 4 | `npx remotion browser ensure` | This service depends on `@remotion/bundler` and `@remotion/renderer`, never on `@remotion/cli`. There is no `remotion` binary to run: `npm error could not determine executable to run` |

**And a fifth, at runtime, which is the one worth reading twice.** The
entrypoint was `pnpm --filter @argentum/render start`. corepack's cache belongs
to root; the process runs as `render`; so the first container started from this
image **downloaded a package manager from the npm registry on boot** — in the
one image this repository deploys behind a NetworkPolicy with `egress: []`. It
would have failed in the cluster and worked on every developer machine, which
is precisely the class `T-02`'s zoneinfo finding named. pnpm 11 then wanted to
purge and reinstall the `node_modules` the build had already written.

The entrypoint is now `node --import tsx src/server.ts`: no package manager at
runtime, and the browser Remotion downloaded at build time sits in
`/app/apps/render/node_modules/.remotion/`, owned by the runtime user.

**The gate, run against the built image:**

```
$ docker build -f apps/render/Dockerfile -t argentum-render:local .   → ok
$ docker run -d -p 8090:8090 -e RENDER_SHARED_SECRET=… argentum-render:local
  {"svc":"render","event":"ready","port":8090}
$ curl -s localhost:8090/healthz                       → 200 {"status":"ok","jobs":0}
$ curl -sX POST …/v1/render                            → 401  (no secret)
$ curl -sX POST …/v1/render -H 'x-render-secret: nope' → 401  (wrong secret)
$ curl -sX POST …/v1/render -H 'x-render-secret: …' -d '{"plan":{}}'
  {"error":"plan version undefined is not supported; this renderer draws version 1"}
$ …/v1/render with kpi_summary.plan.json               → 202 {"job_id":"…"}
  rendering 0.08 → 0.75 → 0.91
  {"state":"done","size_bytes":5865049,"frames":2623,"render_seconds":181.863}
$ GET …/v1/jobs/:id/result                             → 200, 5 865 049 bytes
  ISO Media, MP4 Base Media v1
```

Two numbers for the next estimate: **2 623 frames in 182 seconds** on an
8-core developer machine under Docker, and **5.9 MB** for 87 seconds of 1080p.
That ratio is what the channel-delivery threshold in `T-V3` is sized against.

---

## §T-V3 — `mp4` end to end

Shipped 2026-08-09. `T-V1` projected a spec onto a plan and `T-V2` drew one;
neither could be reached, because no door accepted the format. This is where a
tenant, an agent and an integrator can each ask for a video.

### 1. Every door is asynchronous, and one is closed

| Door | What it does |
| ---- | ------------ |
| `POST /v1/reports/render` | `202` with a report id **whatever `API_V1_SYNC_RENDER_TIMEOUT` says**. Collected by poll, by SSE, or by signed callback — `T-A2`'s three ways, unchanged |
| `Accept: video/mp4` on that door | Refused in milliseconds with `async_format`, naming the collection routes |
| `generate_document` | Queues the render, answers *"it is rendering"*, and the worker posts the file into the thread |
| `POST /v1/reports` (agentic) | **Refused** with `format_not_supported_here` |

That last row is the one decision worth defending. It could accept a video —
the directive would ask for one and the tool would queue it. What it could not
do is *finish*: that job completes when the turn does and attaches whatever
document the turn produced, so a video arriving four minutes later would leave
a report reading `completed` with **no document and no error**. That is
`T-A2b`'s silent shape arriving by a new road. Making it work means the report
row waiting on a render rather than on a turn, which changes what `completed`
means on a published contract — a ticket, not a branch.

### 2. The three failures, told apart

`internal/report/video` is the first outbound dependency `docgen` has ever had,
and most of the package is about that. A PDF fails in-process in milliseconds;
a video fails in another container after minutes, in one of three ways that
mean completely different things to whoever reads the message:

- **not configured** — a deployment with no render service, which is a valid
  deployment. `mp4` is left out of the tool's format enum entirely and refused
  with a sentence saying every other format works.
- **unavailable** — configured and unreachable. The only one a retry could fix.
- **rejected** — the plan was refused. Deterministic, so nothing retries it.

The service already decides which of the last two it was — 400 on a plan it
refused, 500 on its own failure — so a failed job is resolved by asking
`/result` rather than by parsing the message. A second classifier would be one
that can disagree with the first.

### 3. Bounded before, metered after

Checked **before the job is queued**: the scene and frame caps
(`videoplan.Limits`, read from config as `T-V1` §6 said `T-V3` would have to),
the spec's own validation, and the tenant's credit balance. That last one is
new for a render — a PDF is a millisecond of this process and has never been
worth a balance lookup, while a video is minutes of a pod that does one job at
a time, which is the unbounded spend `T-03` exists to stop. A refusal after
the frames are drawn has already cost everything it was going to.

Metered after: a `video_render` usage event carrying the service's own wall
clock, **beside** the document event rather than instead of it. One video is
one document and also three minutes of somebody's CPU. The per-second price is
a placeholder; what the event does is make the number exist before anyone has
to price it.

### 4. Two decisions the ticket did not contain

**A video is refused for a document that is a record.** An invoice, an
agreement, an export: a viewer cannot scan a video, cannot compare two numbers
side by side, and cannot find one line. `Analytical` — a `kpi_row` or a
`chart` — is the same predicate `CheckNarrative` uses for the mirror-image
judgement, so the two cannot disagree about which documents make an argument.

**The format enum follows what the process can *finish*.** Both halves are
required: a render service to draw it, and a queue to hand it to. The eval
harness and `cmd/mcp` build the same tool and have no queue, so neither offers
`mp4` — advertising a format nothing can complete is the `list_watchers`
failure of 2026-08-04 one door further out, where the promise is made to a
customer rather than to an MCP client.

### 5. What the tests found

`CheckNarrative` skipped `mp4` entirely — it fired for `pdf` and `pptx` only.
The format that most needs the reading was the one allowed to be bare figures:
a PDF of unexplained numbers can at least be scanned, and a video of them
cannot be paused.

### 6. Gate

```
$ go build ./...      → clean
$ go test ./...       → all packages pass
$ make lint-go        → 0 issues
$ make types-check    → 6 generated files are current
$ make openapi-check  → 4 artifacts current, quickstart's 13 examples byte-equal
$ helm template … --set render.enabled=true | grep RENDER_BASE_URL → api + worker
$ helm template …                                                  → absent
```

~~**The live half has not run.**~~ **Run 2026-08-09**, against the compose
stack with the API and worker on the host, `argentum-render:local` in Docker,
and a fresh tenant on the seeded demo warehouse. Every acceptance item was
exercised. **Two defects, both fixed in the same sitting**, and both of the
kind this file's own §8 predicts: each is a seam that no unit test crosses.

| The item | Outcome |
| -------- | ------- |
| A video through `POST /v1/reports/render` | **202** in 17 ms, then 55 `render_progress` events over SSE and a terminal `report` event carrying the document. 1 844 851 bytes, `ISO Media, MP4 Base Media v1`, downloaded from the presigned URL. 901 frames over 7 scenes, **71 s** wall clock |
| The progress events | **Defect 1, fixed** — see below. As shipped they climbed to 0.94 and the stream never ended |
| One through a real turn | **Pass.** `get_schema` → `run_sql` → `run_sql` → `generate_document`, the reply *"Video sedang dirender dan akan diposting ke percakapan ini … dalam beberapa menit"*, and the file in the thread minutes later as `Your video is ready: [ … .mp4](…)` |
| The invoice refusal | **Pass.** 400 `invalid_spec`, naming `pdf` as the way out |
| The cap refusal, with an empty render access log | **Defect 2, fixed** — the log stayed empty, but the refusal arrived from the worker after a `202`, not from the door |
| The 402 | **Pass.** Balance zeroed, 400→402 `credits_exhausted`, nothing queued and nothing rendered |
| The unconfigured-service message | **Pass.** A second API with `RENDER_BASE_URL=` answers 400 `format_unavailable` — *"Every other format is available"* — and a PDF posted to the same deployment came back 200 |

#### Defect 1 — the stream never ends for a threadless render

`GET /v1/reports/:id/events` forwards progress and closes on `final` or
`error`. A **threaded** job gets one: `ChatRunner` publishes `final` on the
thread's channel. A render job has no thread, publishes on
`ReportChannelFor(id)`, and nothing ever published anything terminal there — so
the first gate run watched progress reach 0.94 and then heartbeat for **ten
minutes** against a report that had been `completed`, with its file
downloadable, since second seventy-one. `curl` gave up at its own 600-second
cap.

It is the branch's first reachable day. The handler's own comment says so:
*"Until a format took minutes this branch answered once and closed"* — every
earlier render was terminal before a subscriber could attach, so the early
return answered and the loop was dead code. `T-V3` made it live and left
nothing to end it.

`APIReportService.settled` now publishes `final` after `Complete` and `error`
after `fail`, **after** the row is terminal and never before — the ordering
`CompleteReport` already documents, and what lets the handler answer by
re-reading the row rather than by trusting the event. Re-run: 55 progress
events, one terminal `report`, and the connection closed by the server at
**55.5 s**.

#### Defect 2 — the video caps were the worker's, not the door's

§3 of this file says the scene and frame caps are *"checked before the job is
queued"*. They were not. The door ran `doc.Validate()` and `spec.CheckLimits`
— rows, columns, strings, chart points — and the caps that decide whether a
video can exist at all live in `videoplan` and were reached only inside
`Build`, in the worker. A 242-section spec was answered **`202 queued`** and
refused a minute later, so a caller learned that their document can never be a
video only by writing a collection path to be told so. The handler's own
comment claims the opposite: *"a spec that can never render is a 400 the caller
reads now rather than a failed job they poll for."*

`videoplan.CheckLimits` exposes `Build`'s existing precheck — the same
estimate, so there is no second implementation of the caps to disagree with the
first — and `docgen.CheckVideoLimits` applies it at the door for an async
format only. Re-run: **400** `invalid_spec`, *"this document needs at least 243
scenes and the limit is 60"*, with the render service's access log unchanged at
three lines across the whole gate.

**What the empty access log proves, and it did before the fix too:** nothing
reached the renderer in either version. The cost of defect 2 was a caller's
minute and a queue slot, not a render.

**One thing the run needed that no document names:** the API refuses to boot
without WhatsApp credentials — `WHATSAPP_ACCESS_TOKEN` for the default
provider, or all three Twilio variables — on a deployment that uses neither.
`config.Validate`'s switch has no "no WhatsApp" branch. Placeholders get you
past it; it is filed here rather than fixed because it belongs to whoever owns
the channel config, and it costs a newcomer twenty minutes.

### 7. What is not done in `T-V3`

- ~~**Channel delivery above a size threshold.**~~ **Done, same day** — see §8
  below, and note what it turned out to be: the threshold does not exist,
  because the upload path never did.
- **The dashboard does not render a video inline.** A document is a markdown
  link today, whatever its format. Nothing broke — there is no exhaustive
  switch over `DocumentFormat` in the dashboard, which is also why widening the
  union did not fail its build the way `T-V3`'s ticket predicted it would.

### 8. `attach_document_id` stops being a field that does nothing

The ticket asks for *"a link above the threshold"*: `send_message` with an
`attach_document_id` pointing at an mp4 should post the presigned URL rather
than the file whenever the file exceeds the channel's limit.

**There is no threshold, because there was no upload path.** The field has been
on `sendMessageParams` since `T-12a` and its own comment said so — *"accepted
for forward compatibility … validated as a well-formed id if present, never
fetched"*, with `Describe` rendering *"(a document was requested but is not
attached in this version)"*. So the honest reading of the ticket is that every
case is the above-threshold case, and the numbers agree: §8's gate measured
**5.9 MB for 87 seconds** of 1080p, which puts an ordinary three-minute report
past Discord's free limit on its own. A link that always works beats an upload
that works until a report gets longer.

What ships is the link, and the decisions are about what happens when it cannot
be produced:

- **A document that will not resolve refuses the whole action.** Sending the
  message without it delivers a sentence about a report with no report in it,
  and the approver cannot check that afterwards because the message has gone.
- **The lookup is company-scoped by the query.** The id comes from a model and
  the action runs in a worker on a tenant's behalf, so `GetForCompany` is what
  makes another tenant's document a not-found — rather than a fetch followed by
  a comparison somebody can forget.
- **The allowlist still runs first.** An attachment must not become a way to
  make the action fetch something before the recipient has been checked. Pinned
  by a test that asserts the linker was never called on a refused target.
- **No linker, no proposal.** A deployment without object storage refuses an
  `attach_document_id` at `Validate`, so a proposal nothing could honour is
  never stored and never put in front of a human. Same rule as
  `generate_document`'s format enum: do not offer what this process cannot
  finish.
- **The message states the expiry.** A presigned URL that has lapsed answers
  with a signature error, which reads to a recipient as the product being
  broken rather than as a link having had a lifetime.

`docgen.Service` satisfies the linker, because it is already where a document
becomes a URL — `GET /v1/documents/:id` re-presigns through the same `Presign`,
and a second presigner would be a second answer to how long a link lasts.

Eleven tests. The live half — a real WhatsApp message with a real link, opened
on a handset — joins `T-12a`'s own gate in
[`live-gate-backlog.md`](live-gate-backlog.md) §3, which the repo owner
deferred: closing it sends a real message to a real phone.
