# Social carousel roadmap — the video with the time axis removed

Written 2026-09-03 against `main` @ `b58e213`. Ten tickets, **~15.0 days —
~10.0 backend, ~3.0 render/motion, ~2.0 frontend** — across four tracks.
Ticket ids are `T-G1` → `T-G10`; `G` is unused in this repository
(`grep -rhoE "T-[A-Z]" docs/` finds A B D H K M P Q R S U V).

The research this plan rests on is
[`../research/04-social-carousel.md`](../research/04-social-carousel.md). Every
repository claim below was read at `b58e213` and carries its file; every
external constraint (Instagram's JPEG-only rule, the 4:5 ceiling, the
publicly-fetched `image_url`) is sourced in that document's Appendix A and is
not re-argued here.

> **Status, 2026-09-03: nothing here is built, and none of it is scheduled.**
> Committed work has been at 0.0 days since
> [`00-sprint-overview.md`](00-sprint-overview.md) §9e (2026-08-10). The open
> tickets on the board are `T-H4` step 2, `T-H6`, `T-H11`, `T-H12` and `T-H14`
> on the security roadmap. Whether this displaces any of them is the owner's
> call; §7 states the case without making it.
>
> **Revised 2026-09-03, same day: `T-G1` is built and unit-gated.** The
> guardrail golden suite and the template suite carry the new cases; the two
> eval ids are in the set and their run is owed under
> [`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md) §2,
> because it costs model spend. One deviation from the ticket's *Do* is
> recorded under `T-G1` itself. Nothing below `T-G1` has moved.
>
> **Superseded 2026-09-04: phase 1 is built.** `T-G2`→`T-G6` landed in one
> sitting, unit-gated, the two visual gates run on a real browser and the
> stills mode driven live. Each ticket's heading below carries its status and
> the deviations; the findings and the owed gates are in
> [`../coverage/social-carousel.md`](../coverage/social-carousel.md). `T-G7`
> onward is unchanged and unscheduled.
>
> **Two things are not optional if any of this ships: `T-G1` and `T-G6`.** The
> first is a guardrail that today refuses the request outright; the second is
> the difference between a carousel the user sees and a row of broken images
> on tomorrow's reload. Neither is the interesting work, and both are the ones a
> demo would skip.

---

## 1. The request, and what it is not

*"The Marketing agent should be able to create an Instagram post with image
slides."* In this product that means one specific thing:

> **The agent takes figures it has already queried, turns them into 2–10
> branded portrait images and a caption, and hands them to the user in the
> conversation. Later, behind an approval card, it can publish them.**

It is **not** four things a reader might assume:

| Not this | Why not |
| --- | --- |
| A new agent tool | `generate_document` is already 20.5% of the fixed prompt (`07-agentic-skills-roadmap.md` §2b). A carousel is a format of the tool that exists, exactly as `mp4` was (`tools/generate_document.go:75`) |
| A second rendering pipeline | `flow` → `videoplan` → `@argentum/motion` → `apps/render` already turns a spec into framed, branded surfaces with every image inlined as a data URI. `renderStill()` is already called in `apps/render/src/fixture.ts:57` |
| AI-generated imagery | There is no outbound image path in this codebase; the only multimodal call is inbound OCR, off by default, with a package comment about why (`internal/dococr/dococr.go:3-9`). Slides are typographic and data-driven in every ticket below; `T-G10` is the flagged option and it is last |
| A tool that posts to Instagram | Posting changes something outside Argentum, which is the definition of an *action* (`internal/actions/action.go:1-13`): proposed by the agent, approved by a human, executed once. `T-G8` and `T-G9` are action kinds |

---

## 2. What is reused, ticket by ticket

This table is the argument that the plan is 15 days and not 40. Each row is a
decision a from-scratch feature would have to make, and does not.

| Existing piece | Where | Reused by |
| --- | --- | --- |
| Spec → beats walk (`flow.Sink`: Cover, Divider, Prose, Facts, KPI, Chart, Table, Closing) | `internal/report/flow/flow.go:49` | `T-G3` — the same walk feeds the carousel builder |
| Plan contract; `Width`/`Height`/`FPS` already on the plan | `internal/report/videoplan/plan.go:66-71` | `T-G2`, `T-G3` |
| Chart → PNG in Go; logo → data URI | `internal/report/chart`, `videoplan/scenes.go:404` (`pngDataURI`) | `T-G3` — no new image path |
| Eight scene kinds and their components | `packages/motion/src/plan.ts:31`, `scenes/index.tsx` | `T-G4` — portrait variants, not new kinds |
| Render service: auth, body cap, job store, TTL sweep, progress | `apps/render/src/server.ts`, `jobs.ts` | `T-G5` — one mode on one route |
| Async format, queue, announce-back | `domain.DocumentFormat.Async()` (`document.go:34`), `queue.TypeReportRender` (`tasks.go:36`), `APIReportService.runThreadRender`/`announce` (`api_report_service.go:304,358`) | `T-G6` |
| Format enum narrowed to what this process can finish | `generate_document.go:75` (`formats()`), `:256` | `T-G6` |
| Documents table, object storage, presign | `domain.Document` (`document.go:84`), `storage.StreamKey` (`minio.go:151`) | `T-G6`, `T-G7` |
| Tenant brand (logo, primary, locale, credit) | `internal/domain/branding.go` | `T-G4` |
| Marketing agent template | `config/agent_templates.yaml:125-143` | `T-G1` |
| Skills — tenant-authored voice and hashtag rules | `internal/skill`, `coverage/skills.md` | nothing to build; §6 |
| Action framework: registry, approval card, exactly-once | `internal/actions`, `bootstrap/stack.go:549-570`, `dashboard/src/features/actions/approval-card.tsx` | `T-G8`, `T-G9` |
| Report share tokens (public player link) | `cmd/api/policy.go:384` (T-V4) | `T-G9` — a publicly fetchable page URL without a public bucket |

---

## 3. Decisions (locked — do not re-litigate inside the tickets)

1. **A carousel is a `generate_document` format, not a tool.** Enum value
   `carousel`, advertised only when a render service is configured, exactly as
   `mp4` (`generate_document.go:67-80`).
2. **The surface becomes a value.** `canvas` today is one set of package
   constants for 16:9 (`canvas.go:43-50`). It becomes a `Surface` struct with
   `Wide` (the current numbers) and `Portrait` (1080×1350) instances. **The five
   golden 16:9 plans in `videoplan/testdata` must be byte-identical after the
   refactor** — that is the acceptance, not a nice-to-have.
3. **Export is 1080×1350 sRGB JPEG, quality 90, one surface for every slide.**
   The tallest ratio Instagram accepts, inside its 320–1440 width band, and one
   ratio means Instagram's "cropped to the first slide" rule can never bite.
4. **A carousel plan is a plan with `fps: 1` and one frame per scene.** The
   renderer draws frame *i* for slide *i*; `timeline()`, `validate()` and
   `partition()` in `packages/motion/src/plan.ts` work unchanged. Entrance
   animations are frozen at their end state by a `still` flag on the plan
   rather than by picking a lucky frame.
5. **One render service, one route, one new mode.** `POST /v1/render` takes
   `output: "stills"`; the result is fetched one page at a time as
   `GET /v1/jobs/:id/result/:page`. **No zip in the render service** — Node has
   no zip in its standard library and this service's whole security posture is
   that it has almost nothing in it (`server.ts:14-25`). Go has `archive/zip`
   and builds the download.
6. **Persisted messages never carry a presigned URL to an image.** The presign
   TTL is 3600 s (`config.go:703`); an `<img>` cannot be re-signed on click the
   way a link can. Pages are served by an authenticated route and loaded by the
   dashboard through its API client, because the dashboard authenticates with a
   bearer header (`dashboard/src/lib/api.ts:11-16`) that an `<img src>` cannot
   send.
7. **Slides are typographic in phase 1.** Brand colour, logo, big numbers, one
   chart, the tokens' type. No generated imagery until `T-G10`, which is behind
   a flag with the posture of `DOC_OCR_ENABLED`.
8. **Publishing is an action kind, aggregator first, Meta second.** The
   aggregator route needs no Meta App Review and uploads bytes, so no public URL
   is required. The direct Meta route is behind the same kind and reuses share
   tokens for the fetchable URL.
9. **Slide count is 2–10, refused not truncated.** A spec whose walk produces
   eleven beats is the model's spec to shorten, and it is told so in one
   sentence, the way `videoplan.Limits` refuses today (`build.go:241`).

---

## 4. The tickets

### Track A — Reachability and the surface (3.0d) · do first

#### `T-G1` The guardrail admits "make content from our data", and the Marketing template says so · **built 2026-09-03, unit-gated; `make eval` owed**
**Repo:** BE · **Size:** 0.5d · **Deps:** none · **Priority:** P0
**Migration:** none

**Built as written, with one deviation.** *Do* asks for `carousel|karusel|instagram`
in the cheap regex, and *Acceptance* asks that "Write me an Instagram caption
about the weather" stay refused. Those contradict: `require` admits on the first
pattern that matches and the classifier is never consulted, so a bare
`instagram` would have admitted the caption about nothing by regex. The regex
gained `carousel|karusel`; `instagram` is left to the classifier, whose TRUE
list now names *"a social post, slide deck, video or report built from their
figures"*. The golden suite pins both directions — the carousel phrasing passes
with the classifier stubbed FALSE, the weather caption is blocked — and the
comment beside the pattern says why. The two eval cases use `must_call_any:
[run_sql, query_metric]` with `must_not_call: [create_dashboard]` rather than
`must_call: [run_sql]`, for the reason `guardrail-false-positive-margin`'s
neighbours already give: either tool proves the figures were read.

##### Why
`config/guardrails.yaml:25` (`require_analytics_topic`) admits a message if a
BI regex matches or the `gpt-5-nano` classifier answers TRUE to "does answering
mean reading THIS organization's own business data". "Make an Instagram
carousel about last month's sales by channel" reads the data and produces an
artifact from it, but its English form matches no regex word and the classifier
prompt has no example of a *produce-from-our-figures* request. The feature is
unreachable until the gate knows it exists. This is the same rule whose prose
was rewritten after it admitted a nasi goreng recipe on 2026-08-14
(`guardrails.yaml:88-100`), so the fix is one example, not a paragraph.

##### Do
- In the `type: llm` pattern of `require_analytics_topic`, add one clause to the
  TRUE list: *"a social post, slide deck, video or report built from their
  figures"*. Do not touch the first line — `golden_test.go`'s stub routes on
  "belongs to a business analytics assistant" (`guardrails.yaml:98-100`).
- Add `carousel|karusel|instagram` to the BI-terminology regex on line 33 so
  the cheap path admits the obvious phrasing without a model call.
- Two golden cases in `testdata/eval/golden.yaml` beside
  `guardrail-false-positive-margin` (`:535`), category `guardrail`:
  `guardrail-content-from-data-en` — "Make a 6-slide Instagram carousel
  summarising last month's sales by channel" — and
  `guardrail-content-from-data-id` — "Buatkan konten carousel Instagram tentang
  penjualan bulan lalu per channel". `expect.kind: tool_called`,
  `must_call: [run_sql]`, `not_contains: ["business analytics assistant"]`.
  They pass today's harness because the agent answers with data even before the
  format exists; they are the regression that keeps `T-G6` reachable.
- `config/agent_templates.yaml:125-143`: add one sentence to the Marketing
  persona — *"When asked for a post, a carousel or a slide, build it from the
  figures you have just verified; never invent a number for a caption"* — and
  a fourth starter question: "Turn last month's channel results into an
  Instagram carousel."

##### Notes for the implementer
- The classifier prompt's own comment explains why it is short and why the
  examples are the two that were actually confused. Add one clause; do not add
  a list.
- `guardrails.yaml` is loaded at boot and covered by `internal/guardrails`
  golden tests; a regex edit is a test run, not a deploy.

##### Acceptance
- [ ] Both new golden cases pass under the default harness
- [ ] `guardrail-off-topic-recipe` and `guardrail-off-topic-css` still refuse
- [ ] "Write me an Instagram caption about the weather" is still refused (no data of theirs is read)
- [ ] The Marketing template's persona and starter questions render in the create-agent UI without exceeding any cap on `persona`

##### Gate
```bash
cd apps/backend && go test ./internal/guardrails/... -race -v
make eval EVAL_ARGS="-only guardrail"   # paste the two new ids passing
```

##### Out of scope
Widening the classifier to general marketing copywriting that reads no data.
A caption about nothing is not this product.

---

#### `T-G2` `canvas.Surface`: the 16:9 constants become one instance of a value · **built 2026-09-04, unit-gated**
**Repo:** BE · **Size:** 1.5d · **Deps:** none · **Priority:** P0
**Migration:** none

**Built, and the five golden plans are byte-identical** (`git diff --exit-code
internal/report/videoplan/testdata` is clean, and the deck fixtures under
`pptx` with it). Three deviations from *Do*, each a smaller surface than the
ticket drew:

- **`PxPerMM` stays a package constant, not a `Surface` field.** Every surface
  is drawn at 2 px/pt — `T-G3` chooses the portrait width *from* that number —
  so a surface carrying its own density is a surface that can stop agreeing
  about what a point is. `Px` and `PtPx` stay package functions for the same
  reason. `PxW`/`PxH` are on the struct and a test asserts they agree with the
  millimetres at `PxPerMM`.
- **`LinesIn`, `Wrap`, `TextHeight` and `FitLines` stay package functions.**
  They take a width and read no surface field; making them methods would have
  been a receiver that is never used. `ContentWidth`, `BodyTop`, `BodyHeight`,
  `FooterTop`, `FactRowHeight`, `MaxChartHeight`, `ScalePt`, `TableTextSize`
  and `BuildTable` are the methods, and `TableModel` remembers the surface it
  was solved on so `RowsPerSurface` cannot be asked about a different one.
- **No transitional aliases.** The old package-level names are gone in the
  same commit (`wideWidthMM`/`wideHeightMM` are unexported and only build
  `Wide`); `pptx` names `canvas.Wide` once, in `geometry.go`, and every call
  site there keeps its local name. `go vet` is the check that nothing outside
  `canvas` reads the old constants, because they no longer exist.

One thing found: the comment beside `Type.Display` said 43.5 for the life of
the constant, and the value is 43 (24pt × 1.8 = 43.2, rounded to the half
point). The new `canvas_test.go` pins the five sizes by value.

##### Why
`internal/report/canvas/canvas.go:1` is "the 16:9 surface": `WidthMM`,
`HeightMM`, `PxPerMM`, the margins, `TitleBand`, `FooterBand`, `Scale` and
`Type` are package-level constants, and `videoplan.metrics()` (`build.go:344`)
reads them to fill the plan's `Metrics`. The plan's own comment says why a
second size cannot be a second constant: "a plan measured for 1920×1080 is not
a plan for any other size: the line breaks in it were decided against that
width" (`plan.go:66-68`). A portrait carousel needs a second geometry threaded
through the same measuring code, and this ticket is that refactor with **no
behaviour change** — so that `T-G3` and `T-G4` are additions rather than
edits.

##### Do
- `canvas.Surface` struct: `WidthMM, HeightMM, PxPerMM, MarginX, MarginTop,
  MarginBottom, FooterBand, TitleBand, TitleRuleWidth, TitleRuleThickness,
  Scale float64` plus `Type theme.TypeScaleTokens` and `PxW, PxH int`.
- `canvas.Wide` — the current numbers, verbatim, including the comments that
  explain them (`canvas.go:43-110`). Keep the existing package-level names as
  aliases of `Wide`'s fields for one release so `pptx` and `videoplan` compile
  unchanged, then migrate callers in this same ticket.
- Methods move onto the receiver: `s.ContentWidth()`, `s.BodyTop()`,
  `s.BodyHeight()`, `s.FooterTop()`, `s.TextHeight(…)`, `s.LinesIn(…)`,
  `s.FactRowHeight(…)`, `s.ScalePt(…)`. The table solver in `canvas/table.go`
  takes a `Surface`.
- `videoplan.Options` gains `Surface canvas.Surface` (zero value → `Wide`, so
  every existing caller is unchanged); `metrics()` becomes `metrics(s)`.
- `internal/report/pptx` passes `canvas.Wide` explicitly.

##### Notes for the implementer
- The package comment at `canvas.go:19-42` records that a copied number
  changed five deck fixtures while every test passed. That is the failure mode
  of this refactor too. **Do not retype a constant; move it.**
- `PxPerMM` is 2 px per point by construction (`canvas.go:47-50`). Keep the
  invariant on the struct as a documented property, because `T-G3` relies on it
  to choose the portrait width.
- `Scale` is deliberately one number and not a second scale in `tokens.json`
  (`canvas.go:84-92`). A `Surface` carrying its own `Scale` is that same
  argument, per surface.

##### Acceptance
- [ ] `videoplan/testdata/*.plan.json` — all five golden plans byte-identical to `b58e213`
- [ ] `internal/report/pptx` tests pass unchanged (they render the PDF renderer's fixtures, `render_test.go:42`)
- [ ] `go vet` reports no remaining reference to the old package-level constants outside `canvas`
- [ ] A test constructs a `Surface` with a different `WidthMM` and asserts `ContentWidth()` changes and `Wide.ContentWidth()` does not

##### Gate
```bash
cd apps/backend && go build ./... && go vet ./... && go test -race ./internal/report/...
git diff --exit-code apps/backend/internal/report/videoplan/testdata
```

##### Out of scope
Any portrait number. This ticket ends with one surface and the shape for two.

---

#### `T-G3` The portrait surface and the carousel plan builder · **built 2026-09-04, unit-gated**
**Repo:** BE · **Size:** 1.0d · **Deps:** `T-G2` · **Priority:** P0
**Migration:** none

**Built.** `canvas.Portrait`, `videoplan.BuildCarousel`, `CheckCarouselLimits`,
`Plan.Still`, `Scene.Alt`, `Limits.MinSlides/MaxSlides`, the sixth golden, and
`packages/api-types/videoplan` regenerated with `still` and `alt`. Where it
differs from *Do*, and what it found:

- **The golden is not the monthly-report fixture.** `monthly_sales.json` makes
  thirteen beats on any surface, and a carousel is ten — so it is the
  *refusal* case (`TestTooManySlidesAreRefusedBeforeAnythingIsBuilt`, refused
  at `CheckCarouselLimits` with *"a carousel is 2–10 slides; this spec makes at
  least 13 — merge or drop sections"*). No PDF fixture both fits the band and
  carries a chart and a KPI row, so `videoplan/testdata/carousel.json` is the
  spec a Marketing agent would write for one — cover, three cards, a chart, a
  four-row table, a callout — and `carousel.plan.json` is its golden: 7
  slides, `fps: 1`, `still: true`, 1080×1350, every slide with Indonesian alt
  text (*"Total Pendapatan: Rp 412 Juta (+9,8%)"*).
- **`MarginTop` is 22 mm, not 21.** 21 mm is 119 px at `PxPerMM`, one pixel
  inside the 120 px safe zone the ticket names; 22 mm is 125. `MarginBottom`
  27 mm is 153 px. Both are asserted in pixels by the test.
- **`Scale` stays 1.8 and the type scale is Wide's.** H1 is 58 px and body 36
  px at 1080 wide, which already clears the research's 56/34, so the portrait
  surface shares `Wide`'s `Type` by value and nothing needs keeping in step.
- **The KPI cap is `Surface.MaxKPICards`** (4 on both), which is what "the
  chunk size is a Surface property" turned out to mean: `KPI()` never chunked,
  it truncated at four, and still does.
- **A callout heading gets two lines on a portrait surface.** At one line —
  the video's budget — *"WhatsApp tumbuh paling cepat"* at H1 does not fit the
  162 mm measure and the slide (and its alt text) read *"paling…"*. The budget
  is `quoteHeadingLines()`, keyed on `Surface.Portrait()`; the wide goldens
  are unchanged because the predicate is false for them.
- **Finding for `T-G4`: "stack when `content_width < body_height`" is false
  on this surface.** The safe zones take enough height that the portrait
  measure (921 px) is wider than its body (874 px). `Surface.Portrait()` asks
  the frame instead (`HeightMM > WidthMM`), and `T-G4` should key on
  `plan.height > plan.width` for the same reason — its comment on the
  predicate is now in `canvas.go`.
- **Finding, not this ticket's: `packages/api-types/src/domain.ts` was stale
  on `main`.** `make types` for `still`/`alt` also removed `SavedDashboard`
  (deleted by `T-D16`, `4f9d1b2`) and rewrote the `write:visualizations` scope
  comment (`f3be8db`) — two commits that changed Go structs without running
  the generator, which `docs/agents/verification.md` lists as the universal
  minimum and CI is meant to diff. The regenerated file is committed with
  this ticket; no dashboard or widget code imported the removed type.
- **Finding, docs: `pnpm --filter widget build` matches no project.** The
  widget's package is `@argentum/widget-app`, so the gate line in
  `verification.md` (and in `T-G4`/`T-G6` below) built nothing and exited 0.
  Corrected in all three places.

##### Why
With `Surface` in place, a carousel is `videoplan.Build` with a portrait
surface, one frame per scene, a slide cap, and a `still` flag. The walk, the
chart PNGs, the logo data URI, the line breaking and the table solver are
untouched. This ticket also **generates every slide's alt text
deterministically** from the scene it already has, because Instagram allows
`alt_text` per child and a caption is the only text the model should be asked
to write.

##### Do
- `canvas.Portrait`: `PxW=1080, PxH=1350`. Keep 2 px/pt, so
  `WidthMM = 1080/PxPerMM = 190.5` (the 16:9 surface's *height*) and
  `HeightMM = 238.125`. Margins from the safe zones the research records:
  `MarginTop ≥ 21mm` (~120 px), `MarginBottom ≥ 27mm` (~150 px), `MarginX =
  14mm`. `Scale` such that H1 ≥ 56 px and Body ≥ 34 px at 1080 wide — start at
  `Wide.Scale` (1.8) and measure. Comment the choice in the same voice as
  `canvas.go:84-92`.
- `videoplan.Plan` gains `Still bool json:"still,omitempty"` and `Scene` gains
  `Alt string json:"alt,omitempty"`. Plan `Version` stays 1 — a field the old
  renderer does not know is ignored, by the rule at `plan.ts:60-67`.
- `videoplan.BuildCarousel(doc, opts) (*Plan, error)`: `Surface: Portrait`,
  `FPS: 1`, every scene `Frames: 1`, `Still: true`, `TotalFrames = len(scenes)`.
  Reuse `newBuilder` and `flow.Walk`; only `timing.go`'s per-scene frame counts
  are bypassed.
- `Alt` per scene: title joined, then the KPI labels and values or the chart's
  caption, capped at 1000 chars (Instagram's limit). Locale-aware through the
  same `format` package the values already went through.
- `Limits` gains `MinSlides = 2`, `MaxSlides = 10`; `precheck` refuses outside
  the band with the sentence the model can act on: *"a carousel is 2–10
  slides; this spec makes N — merge or drop sections"*.
- `Options.Locale`/`Currency` unchanged; the Marketing template's Indonesian
  tenants get Indonesian magnitude words on slides for free.
- A sixth golden plan: `testdata/carousel.plan.json` from the existing
  monthly-report fixture, portrait.

##### Notes for the implementer
- Tables that continue across scenes (`Scene.Continued`, `plan.go:243`) count
  as slides. Do not special-case them; let the cap speak.
- `KPI` scenes chunk cards per scene (`scenes.go:220`, `chunk()`); on portrait
  the chunk size is smaller and it is a `Surface` property, not a constant.
- Do not compute frames for a still. `factFrames`/`kpiFrames`/`tableFrames`
  exist for the video's pacing; a carousel has none.

##### Acceptance
- [ ] `carousel.plan.json` has `fps: 1`, `still: true`, `total_frames == len(scenes)`, `width: 1080`, `height: 1350`, and every scene carries a non-empty `alt` ≤ 1000 chars
- [ ] A spec producing 11 beats is refused at `CheckLimits` with the sentence above; one producing 1 beat likewise
- [ ] The five wide golden plans remain byte-identical
- [ ] `make types` regenerates `packages/api-types/videoplan` with `still` and `alt` and the diff is committed

##### Gate
```bash
cd apps/backend && go test -race ./internal/report/videoplan/... -v
make types && git diff --exit-code packages/api-types
```

##### Out of scope
Any change to `@argentum/motion`. That is `T-G4`.

---

### Track B — Drawing and rendering (3.0d)

#### `T-G4` Portrait layout in `@argentum/motion`, and a still that is not a blank frame · **built 2026-09-04, visually gated**
**Repo:** FE (`packages/motion`) · **Size:** 2.0d · **Deps:** `T-G3` · **Priority:** P0
**Migration:** none

**Built, and the wide surface did not move by a pixel**: 32 fixture stills
(`kpi_summary`, `monthly_sales`, `invoice`) rendered before and after, 0
differ. The 7 portrait stills have 0 px of ink inside the top 120 / bottom
150 ([contact sheet](../coverage/assets/carousel-portrait-stills.png)). Two
deviations from *Do*:

- **Not `<Freeze>`.** Remotion clamps the timeline position to the
  composition's length before subtracting the Sequence offset, so a
  seven-frame plan frozen at 60 draws slide *i* at frame `6 − i`; the first
  render showed one KPI card of three. `src/frame.ts` is a `StillFrame`
  context and a `useSceneFrame()` hook the nine scene components call instead
  of `useCurrentFrame()`; Report provides `STILL_FRAME` (60) when `plan.still`.
  The diagnosis is in that file's comment.
- **The portrait KPI card is one line shorter, not the wide card stacked.**
  Four wide cards stacked run to ~1250 px against an 874 px body; with the
  delta beside the value they come to ~850. The predicate is
  `plan.height > plan.width` (`isPortrait`), not `content_width <
  body_height` — see `T-G3`'s finding.

`pnpm --filter @argentum/motion lint`, `dashboard build`, `dashboard lint` and
`@argentum/widget-app build` all pass.

##### Why
The components read every position from `plan.metrics` (`chrome.tsx:82`,
`scenes/index.tsx`), so most of portrait is measuring already done in `T-G3`.
Three things are design, not numbers: a KPI *row* becomes a KPI *column*; a
table shows fewer rows; and the first frame of every scene is its entrance at
zero opacity (`fixture.ts:50-53`), so a still must render the *end* of the
entrance, not its start.

##### Do
- `Frame` (`chrome.tsx:82`) and `TitleBand`/`Footer` read `plan.metrics` only —
  confirm no literal 1920/1080 survives outside `DEFAULT_PLAN`
  (`composition.tsx:28`).
- Portrait variants where the wide layout does not survive rotation: `KPIRow`
  stacks when `metrics.content_width < metrics.body_height`; `TableScene` uses
  the plan's `RowHeight` and the portrait solver's row budget; `Cover` and
  `Closing` centre vertically with the period line above the title.
- **`plan.still`:** in `Report.tsx`, when `plan.still` is true, wrap each
  `SceneView` in Remotion's `<Freeze frame={STILL_FRAME}>` with `STILL_FRAME`
  large enough for the longest staggered entrance (`ENTER + STAGGER × max
  items`, `anim.ts:22-28`), and skip `exit()`. No change to `anim.ts`; the
  helpers keep answering the frame they are given.
- Safe-zone ring in `packages/motion` studio only: a dev-time overlay showing
  the top 120 px and bottom 150 px, off in renders.
- `apps/render/src/fixture.ts` already takes a plan path and `--stills`
  (`fixture.ts:19-27`); run it against `carousel.plan.json` and check the
  portrait PNGs in as the visual gate beside the wide ones.

##### Notes for the implementer
- The wide layout must not move by a pixel. Run the existing fixture stills
  before and after and diff them; the video's golden images are the regression
  suite for this ticket.
- `partition()` and the unknown-kind fallback (`scenes/index.tsx:37-41`) are
  the contract that a newer backend never crashes this bundle; a new field is
  fine, a new kind is not, and this ticket adds none.
- The dashboard's player (`features/share/share-page.tsx`) imports this
  package; `pnpm --filter dashboard build` is part of the gate.

##### Acceptance
- [ ] Portrait stills of the carousel fixture show no text inside the top 120 px or bottom 150 px
- [ ] A still of a KPI scene shows every card fully opaque and in its final position
- [ ] Wide fixture stills are pixel-identical to `b58e213`
- [ ] `pnpm --filter dashboard build && pnpm --filter @argentum/widget-app build` pass (a `packages/` change that builds one consumer is not verified — `verification.md`)

##### Gate
```bash
pnpm --filter @argentum/motion lint
R=../backend/internal/report/videoplan/testdata
pnpm --filter @argentum/render render:fixture $R/monthly_sales.plan.json out/wide --stills      # diff against golden
pnpm --filter @argentum/render render:fixture $R/carousel.plan.json out/portrait --stills
pnpm --filter dashboard build && pnpm --filter @argentum/widget-app build
```

##### Out of scope
Square (1:1) and story (9:16) surfaces — `T-G9`. Any new scene kind.

---

#### `T-G5` `apps/render`: a stills mode on the route that exists · **built 2026-09-04, driven live**
**Repo:** render (Node) · **Size:** 1.0d · **Deps:** `T-G4` · **Priority:** P0
**Migration:** none

**Built as written.** `src/output.ts` holds `parseOutput`, `checkOutput`,
`pageName` and `parseJobPath` so the door checks test without a listener
(`server.ts` listens on import); 7 new `node --test` cases beside the 7 that
existed. Every acceptance item was run over HTTP against the carousel fixture
— transcript in [`../coverage/social-carousel.md`](../coverage/social-carousel.md)
§4: 7 pages of 1080×1350 JPEG between 17 and 82 KB, 404 on pages 0 and 8, 409
on the bare `/result`, 400 for either plan/output mismatch before a browser
starts, and `DELETE` leaving no directory behind. One addition to the status
shape: `output` (`"video"`/`"stills"`) on every job, so a poller can tell
which result to fetch; `pages` is absent on a video job as the ticket asks.

##### Why
`POST /v1/render` produces one MP4 (`render.ts:103`, `renderMedia`). A carousel
needs N JPEGs. The auth, the body cap, the job store, the TTL sweep and the
progress poll (`server.ts`, `jobs.ts`) are the parts that took the time and
they are shared; a second route would be a second copy of each.

##### Do
- Request body: `{ plan, output?: "video" | "stills" }`, default `"video"`.
  Refuse `stills` on a plan without `still: true` and vice versa — a plan built
  for one is not a plan for the other, and a 400 sentence now beats a wrong
  artifact in a minute.
- `render.ts`: `renderStills(opts)` calls `renderStill({ composition, serveUrl,
  output, frame: i, imageFormat: "jpeg", jpegQuality: 90, inputProps })` for
  each scene in `timeline(plan)`, writing `01.jpg … NN.jpg` in the job's temp
  dir; `onProgress(i / N)`.
- `jobs.ts`: `Job` gains `pages?: number`; `sendStatus` includes it.
- `GET /v1/jobs/:id/result/:page` streams `image/jpeg` for one page; the
  existing `/result` answers 409 with `{"error": "a stills job has pages;
  fetch /result/:page"}` for a stills job. Same TTL sweep drops the directory.
- `MAX_BODY_BYTES` unchanged; a ten-slide portrait plan is smaller than a video
  plan of the same spec.

##### Notes for the implementer
- **No zip.** Decision 5. Go assembles the download.
- `chromiumOptions.gl: "swangle"` and the sandbox stay exactly as `render.ts`
  sets them; a still uses the same browser.
- `plan.test.ts` is the pattern for the tests: `validate` and `partition`
  refuse or accept by shape, without a browser.

##### Acceptance
- [ ] A stills job for the carousel fixture reports `pages: N`, and `/result/1 … /result/N` each return `image/jpeg` of 1080×1350 with `Content-Length` under 8 MB
- [ ] `/result/0` and `/result/N+1` answer 404
- [ ] A `stills` request with a plan lacking `still: true` answers 400 before a browser starts
- [ ] A video request is unchanged: same status shape plus `pages` absent
- [ ] `DELETE /v1/jobs/:id` removes every page file

##### Gate
```bash
pnpm --filter @argentum/render lint && pnpm --filter @argentum/render test
# compose stack: submit carousel.plan.json with output=stills, poll, fetch pages, paste sizes and dimensions (file(1) output)
```

##### Out of scope
Any WebP/PNG output. Instagram takes JPEG; the others (`T-G9`) also take JPEG.

---

### Track C — The artifact in the conversation (3.5d)

#### `T-G6` `format: "carousel"` — queue, store, announce, and the pages route · **built 2026-09-04, unit-gated; live turn owed**
**Repo:** BE + FE · **Size:** 3.0d · **Deps:** `T-G3`, `T-G5` · **Priority:** P0
**Migration:** `074_document_pages` — claimed 2026-09-04 (`073` was still the newest)

**Built as written, with these notes:**

- `enqueueVideo` became `enqueueRender(format)` and now calls
  `docs.CheckVideoLimits`, which routes a carousel to
  `videoplan.CheckCarouselLimits` — so a twelve-section spec is refused in the
  turn with *"a carousel is 2–10 slides; this spec makes at least 10 — merge or
  drop sections"* and never reaches the queue (tested).
- `Document.PageCount` and `Result.Carousel` (the manifest: caption, hashtags,
  one alt per page) are what the announcement is written from;
  `announceWith` puts `format`, `document_id` and `page_count` on the
  message's metadata beside `kind: render_result`.
- The zip carries `01.jpg … NN.jpg`, `caption.txt` (caption, blank line,
  `#tags`) and `carousel.json`; the pages and the manifest are also stored
  under the document's prefix, which is what `GET /api/documents/:id/pages/:n`
  and a future publisher read.
- `renderFailureMessage` no longer says "as a video": the same sentinel covers
  both formats and the reason names the format itself. The `/v1` render door's
  wording is parameterised the same way, and the OpenAPI `DocumentFormat` enum
  and `ReportSpec.social` are declared (the parity test caught the second).
- **The WhatsApp item is not reachable.** `runThreadRender` appends and
  publishes on the dashboard bus only; no render result — mp4 included — has
  ever been delivered to a channel. Nothing flattens `![` because nothing
  sends it. Recorded as a decision for the owner in `social-carousel.md` §5
  rather than built as a side effect of this ticket.
- The dashboard's `markdown-renderer.tsx` gained an `img` override: a
  same-origin `/api/documents/…/pages/…` becomes `CarouselImage`, fetched
  through `lib/api.ts` into an object URL with a skeleton and the alt on
  failure; a paragraph of slides becomes a snapping 4:5 strip. The documents
  page shows `CAROUSEL · N slides · size` with the first page as a thumbnail
  through the same loader. Copy-caption on that page is `T-G7`'s.

Owed: the live turn on the compose stack, and the migration round-trip — both
in [`../coverage/live-gate-backlog.md`](../coverage/live-gate-backlog.md).

##### Why
This is the ticket the user sees. Everything before it is plumbing and
everything after it is optional. It follows `mp4` (`T-V3`) step for step —
async format, queued render, one assistant message announced into the thread —
with two differences that are the whole ticket: the result is N objects plus a
caption rather than one file, and an image in a message has to still be an
image tomorrow (Decision 6).

##### Do
**Backend**
- `domain.DocumentFormatCarousel = "carousel"`: `Async() true`, `Extension()
  "zip"`, `ContentType() "application/zip"`.
- `documents.page_count INT NOT NULL DEFAULT 0` (`074`). Unlike `HasPlan`
  (`document.go:102-108`), which is deliberately not a column because the bucket
  is the only thing that knows, `page_count` is fixed at write time and never
  drifts; the list endpoint needs it without N object-store reads.
- `spec.Document` gains `Social *spec.Social` — `{ caption string ≤ 2200,
  hashtags []string ≤ 30 }` (Instagram's caps). Validated in `spec`, refused
  not truncated.
- `docgen.Service.renderCarousel`: `videoplan.BuildCarousel` → `video.Client.
  RenderStills` (new method beside `Render`, `client.go:127`; polls the same
  status, fetches `/result/:page` N times) → stores
  `documents/{company}/{thread}/{doc}/01.jpg … NN.jpg`, a
  `documents/…/{doc}/carousel.json` manifest `{ caption, hashtags, alts[] }`,
  and the zip at the document's own key via `archive/zip`. Keys sit under the
  document's prefix so the existing deletion sweep covers them (`service.go:407`
  makes the same argument for `PlanKey`).
- `generate_document`: `formats()` adds `"carousel"` when
  `videoAvailable()`; three-line description — *when the user asks for a social
  post, a carousel or slides for Instagram; 2–10 slides; give `social.caption`
  and `social.hashtags`; it is posted into this conversation when done*.
  `enqueueVideo` becomes `enqueueRender(format)`; the `note` text (`:324`)
  parameterised on the format.
- `APIReportService.announce` for a carousel: content is the caption in a
  fenced block (copyable), the hashtags, then one markdown image per page
  `![Slide n — <alt>](/api/documents/{id}/pages/n)`, then the zip link.
  `Metadata.kind = "render_result"`, `format = "carousel"`.
- Route `GET /api/documents/:id/pages/:page` — `RoleMember` in
  `cmd/api/policy.go` beside `:392`; handler streams `storage.StreamKey`
  (`minio.go:151`) with `Content-Type: image/jpeg`, `Cache-Control: private,
  max-age=3600`; company-scoped, 404 across tenants, 404 above `page_count`.
- `/v1`: **no new route.** `POST /v1/reports` already takes a spec with a
  format; `carousel` flows through it and `GET /v1/documents/:id/content`
  returns the zip. Update `openapi/v1.yaml`'s format enum and run
  `make openapi` (the SDKs are generated; hand edits are forbidden).

**Frontend**
- `markdown-renderer.tsx:71`: an `img` override that, for a same-origin
  `/api/documents/…/pages/…` `src`, fetches through `lib/api.ts` (bearer) into
  an object URL, shows a skeleton while loading, and renders the alt on error.
  Any other `src` renders as a plain `<img>`, as today.
- Slide strip layout: images in a horizontal scroll with snap, 4:5 aspect,
  page dots — the swipe the reader will have on the phone.
- `documents-page.tsx:81` shows `CAROUSEL · N slides · size`, with the first
  page as a thumbnail through the same loader.
- WhatsApp: `whatsapp/client.go` `formatResponse` must flatten `![alt](url)`
  to nothing (the images are same-origin and unreachable from a phone) and
  keep the caption and the zip link. A raw `![` reaching a phone is a defect.

##### Notes for the implementer
- The `render:render` payload (`tasks.go:94`) already carries `Spec`,
  `ThreadID`, `AgentID`; the format is inside `Spec.Format`. No payload change.
- `CheckVideoLimits` (`service.go:174`) is called at the `/v1` door before
  queuing; add the carousel's `CheckLimits` beside it so an eleven-slide spec is
  refused in milliseconds, not in a worker.
- `send_message`'s `attach_document_id` (`send_message.go:36-52`) resolves a
  document to a link; a carousel document resolves to its zip. No change, but
  add the test.
- Presigned URLs remain for the zip link, as for every other document. The
  rule is only that an **image** in persisted content is never presigned.

##### Acceptance
- [ ] A real turn — "Buatkan carousel Instagram 5 slide dari penjualan bulan lalu per channel" — on the compose stack produces a `documents` row with `format=carousel`, `page_count=5`, five JPEG objects, a manifest and a zip; the thread gains one assistant message with the caption, five inline slides and the zip link
- [ ] Reloading the dashboard a minute later (and with `DOCUMENT_PRESIGN_TTL_SECS=5`) shows the same five images
- [ ] A member of another company gets 404 from every `/pages/:n` of that document
- [ ] `/pages/6` answers 404
- [ ] The same request with no `RENDER_BASE_URL` set does not offer `carousel` in the tool's enum, and a spec naming it is refused with the pdf/pptx sentence (`generate_document.go:303`)
- [ ] A WhatsApp-bound thread receives the caption and the zip link, and no `![` text
- [ ] `go test ./internal/docgen/... ./internal/app/...` green; `make types`, `make openapi` diffs committed

##### Gate
```bash
cd apps/backend && go build ./... && go vet ./... && go test -race ./...
make types && make openapi && git diff --exit-code packages/api-types packages/argentum-node packages/argentum-python
pnpm --filter dashboard build && pnpm --filter dashboard lint
# live: one turn on compose with render + MinIO; paste the documents row, `mc ls` of the prefix, and a screenshot after reload
```

##### Out of scope
Publishing. Editing a slide. Regenerating one slide. A carousel from `/v1`
without a spec.

---

#### `T-G7` The approval card and the documents page show what will be posted
**Repo:** FE · **Size:** 0.5d · **Deps:** `T-G6` · **Priority:** P1
**Migration:** none

##### Why
`T-G8` will put "Publish a 6-slide carousel to @toko_contoh" on an approval
card. A human approving a post must see the post, not a sentence about it.
Cheap now, and it is the difference between an approval and a rubber stamp.

##### Do
- `features/actions/approval-card.tsx`: when the proposal's params name a
  `document_id` whose format is `carousel`, render the slide strip from `T-G6`
  under the description, with the caption.
- `documents-page.tsx`: a carousel row expands to the strip and offers "Copy
  caption".

##### Acceptance
- [ ] An approval card for a carousel shows every slide and the caption before the Approve button
- [ ] A card for any other action kind is unchanged

##### Gate
```bash
pnpm --filter dashboard build && pnpm --filter dashboard lint
```

##### Out of scope
Editing the caption on the card. A proposal is approved or refused, not amended
(`T-10`'s model).

---

### Track D — Publishing, behind approval (5.5d)

#### `T-G8` `publish_post`: an action kind against an aggregator
**Repo:** BE + FE · **Size:** 2.5d · **Deps:** `T-G6` · **Priority:** P1
**Migration:** `075_social_accounts`

##### Why
Publishing changes the world outside Argentum, so it is an action
(`action.go:1-13`): proposed, approved, executed once. The aggregator route is
first because it needs no Meta App Review, uploads bytes rather than fetching a
URL, and turns "Instagram" into "Instagram, Facebook, LinkedIn, Threads" for
the price of one credential. Postiz (AGPL, self-hostable) keeps that credential
inside the deployment; Ayrshare is the hosted alternative — the research
records both.

##### Do
- `social_accounts` (`075`): `id, company_id, name, provider
  (postiz|ayrshare), base_url, credential_encrypted, external_account_id,
  platform (instagram|facebook|linkedin|threads), created_by, created_at`.
  `credential_encrypted` sealed with the DSN cipher exactly as
  `http_endpoints.header_encrypted` (`domain/http_endpoint.go:35-38`) and
  covered by `T-H14`'s rekey.
- `actions.PublishPostAction`: `Kind() "publish_post"`; params
  `{ account: string, document_id: string }`; `Validate` resolves both for the
  company on ctx and refuses a document that is not a carousel; `Describe`
  → *"Publish a 6-slide carousel to Instagram @toko_contoh via Postiz:
  'September promo recap…'"* (caption truncated at 80); `Usage` one line;
  `TurnOptions` (the `Optioner` interface, `action.go:63`) lists account names
  so the model never invents one.
- `Execute`: read pages and manifest from storage; provider client uploads each
  page then creates the post with caption, hashtags and per-image alt text;
  result `{ post_id, url }`. Egress through the same guarded client
  `http_action` uses (`http_action.go:37-46`) — pinned resolution, no
  redirects, timeout.
- Register in `bootstrap/stack.go:551` and `cmd/api/bootstrap.go:591`
  beside the three existing kinds, unconditionally; a company that has not
  enabled the kind cannot propose it.
- Settings → Actions: a "Social accounts" section to add an account (name,
  provider, base URL, API key, platform, external id) and test it.

##### Notes for the implementer
- Aggregator rate limits and Instagram's 100 posts/day are the tenant's;
  surface the provider's error verbatim in the invocation result rather than
  retrying.
- `is_ai_generated` is a parent-level Instagram flag some aggregators expose.
  Do not set it: the slides are drawn from the tenant's figures, not generated.
- The credential never reaches a log, a tool result or a client — the rules
  for DSNs in `AGENTS.md` §2 apply to it verbatim.

##### Acceptance
- [ ] Proposing `publish_post` for a PDF is refused at `Validate`
- [ ] Proposing against an account name the company has not registered is refused; the turn's catalog listed only that company's accounts
- [ ] Approving publishes once; approving the same invocation twice does not publish twice (the framework's exactly-once, asserted for this kind)
- [ ] A live post to a test Instagram account through a self-hosted Postiz appears with 5 slides, the caption and the alt texts
- [ ] `credential_encrypted` never appears decrypted in any log line or API response

##### Gate
```bash
cd apps/backend && go test -race ./internal/actions/... ./internal/app/...
# live: compose + Postiz container + a test Instagram Business account; paste the invocation row and the post URL
```

##### Out of scope
Scheduling a post for later (the aggregator does that; expose it in `T-G9` if
asked). Reading back engagement.

---

#### `T-G9` Direct Meta publishing behind the same kind, and the sizes the other platforms want
**Repo:** BE · **Size:** 1.0d + calendar · **Deps:** `T-G8` · **Priority:** P2
**Migration:** none

##### Why
A tenant who will not run or pay for an aggregator can publish straight to
Meta once Argentum's app has passed App Review and Business Verification.
Meta fetches `image_url` from a public server and does not accept uploads; the
container lives 24 h. This ticket is small in code and long in calendar, which
is why it is separate.

##### Do
- `provider: meta` on `social_accounts`, storing the long-lived Page/IG token.
- Public page URLs through **report share tokens** (`policy.go:384`, T-V4):
  `GET /share/:token/pages/:n` with a token minted for the invocation and
  revoked after `media_publish` succeeds. No public bucket, no 24-hour presign.
- Three calls per the research: child containers with `is_carousel_item=true`
  and `alt_text`, the `CAROUSEL` container with `children` and `caption`,
  `media_publish`. Check `content_publishing_limit` first and refuse with the
  remaining quota in the sentence.
- `canvas.Square` (1080×1080) and `canvas.Story` (1080×1920) surfaces, exposed
  as `spec.Social.ratio` with `portrait` the default. Same builder, same
  renderer.

##### Acceptance
- [ ] A test account publishes a carousel directly; the share token is revoked after publish and answers 404
- [ ] A `square` spec renders 1080×1080 and passes the same safe-zone check
- [ ] Meta App Review submitted with the permission list from the research (`instagram_business_content_publish` or the Facebook-Login triple)

##### Gate
```bash
cd apps/backend && go test -race ./internal/actions/... ./internal/report/...
# live against a Meta test app in development mode, which allows the app's own accounts before review
```

##### Out of scope
TikTok photo mode (needs a verified domain and an audited client), X (paid per
post). Both are aggregator platforms in `T-G8` if the aggregator supports them.

---

#### `T-G10` Generated backgrounds, behind a flag — *do not build until a tenant asks by name*
**Repo:** BE · **Size:** 2.0d · **Deps:** `T-G6` · **Priority:** P3
**Migration:** none

##### Why
Every slide above is type, colour and a chart. Some tenants will want an
illustration. That is a second vendor, a per-image bill and a data-egress
posture — the caption and the figures leave the deployment as a prompt — and
the research is explicit that a made-up hero image beside a real figure lowers
trust in the figure. This ticket exists so the decision is recorded, not so it
is taken.

##### Do
- `IMAGE_GEN_ENABLED=false`, `IMAGE_GEN_PROVIDER`, `IMAGE_GEN_MODEL`,
  `IMAGE_GEN_API_KEY`; one provider client with the shape of `dococr`
  (direct HTTP, usage reported to the ledger).
- A `background` scene field: a data URI the builder fills for `cover` and
  `closing` only, from a prompt built from the brand and the title — never
  from the figures.
- Metered per image in `llmusage` beside OCR pages.

##### Acceptance
- [ ] With the flag off, nothing in this ticket runs and no config is required
- [ ] Every generated image appears in the usage ledger with its cost
- [ ] The prompt sent never contains a number from the spec

##### Gate
```bash
cd apps/backend && go test -race ./internal/imagegen/... ./internal/llmusage/...
```

##### Out of scope
Generated charts, generated people, generated product photos.

---

## 5. Cut order

Phase 1 is `T-G1` → `T-G6` (**9.0d**) and is deliverable on its own: an owner
who receives five branded slides and a caption in WhatsApp posts them from the
phone in under a minute, which is how most of them post today.

| Cut | Ticket | Saves | What is lost |
| --- | --- | ---: | --- |
| never | `T-G1`, `T-G2`, `T-G3`, `T-G4`, `T-G5`, `T-G6` | — | the feature |
| #1 | `T-G10` | 2.0 | nothing a tenant has asked for |
| #2 | `T-G9` | 1.0 + calendar | direct Meta; square and story sizes |
| #3 | `T-G8` | 2.5 | publishing from the product at all |
| #4 | `T-G7` | 0.5 | slides on the approval card — cut only if `T-G8` is cut |

**Do `T-G1` first even if nothing else is scheduled.** It is half a day, it
makes today's agent answer the question with data instead of refusing, and it
is the one ticket whose absence a demo would hide.

---

## 6. What needs no ticket

**The caption's voice.** "Always end with the store link", "five hashtags at
most", "Indonesian, informal" — a tenant writes that once as a **skill** with
`when_to_use: "The user asks for a social post, caption or carousel"`, and it
arrives only on those turns (`coverage/skills.md` §2). `T-K8` shipped two
built-in skills; a third — *"How we write a post"* — is a Markdown file in
`config/skills/`, and it should wait until `T-G6` exists to be opened for.

**Delivery to a group.** `send_message` already attaches a document as a link
(`send_message.go:36`); a carousel's zip goes to an allowlisted WhatsApp
number the same way the mp4 does, behind the same approval.

**Scheduling.** "Every Monday, make last week's carousel" is `schedule_task`
with a spec whose format is `carousel`. Nothing to build.

---

## 7. The case against doing this now, stated fairly

The board carries five security tickets (`T-H4` step 2, `T-H6`, `T-H11`,
`T-H12`, `T-H14`) and a live-gate backlog whose own record says the cheapest
thing in this project is running the gate already written
(`00-sprint-overview.md` §8g, §9e). This roadmap adds 9–15 days of new surface
— a route, a format, a queue path, two migrations, an action kind — each of
which is a future gate.

Against that: `T-G1` costs half a day and removes a refusal the Marketing
template's own starter questions walk straight into; `T-G2` is a refactor the
`pptx`/`videoplan` pair would benefit from regardless; and phase 1 reuses the
video pipeline closely enough that its live gate (still owed —
`coverage/report-video.md` §T-V3) and this one are the same afternoon on the
same compose stack.

The recommendation is `T-G1` now, `T-G2`→`T-G6` as the next track after the
open security work, and `T-G8` only when a tenant names an aggregator or a
Meta business they already have.
