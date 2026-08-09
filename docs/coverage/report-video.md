# Video and animated decks — `T-V1` → `T-V5` Record

The track that makes the same report spec render as a silent 1080p video and as
an animated deck at a link. Tickets in
[`../plan/01-tickets.md`](../plan/01-tickets.md); the insert and what it slipped
are [`../plan/00-sprint-overview.md`](../plan/00-sprint-overview.md) §8d.

**State: `T-V1` and `T-V2` code complete and gated locally, 2026-08-09.
`T-V3`→`T-V5` not started.** A plan renders to a real MP4 and to a still per
scene; nothing is wired to the backend yet.

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

- **The image has not been built or deployed.** The Dockerfile and the chart are
  written and unexercised; `docker build` needs a run and the readiness probe
  needs a cluster. That is the outstanding half of this ticket.
- **No backend wiring.** `mp4` is not a format anything can ask for yet — `T-V3`.
- **`RevealGrow` is defined and never chosen**, and the chart's own white ground
  sits on the cream page exactly as it does in the PDF. Both are `T-V5`'s.
- **No still-comparison gate yet.** The stills are produced; comparing them
  against goldens within a perceptual tolerance is `T-V5`.
