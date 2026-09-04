# Social carousels — the video with the time axis removed (T-G1 → T-G6)

Built 2026-09-03/04 against the plan in
[`../plan/08-social-carousel-roadmap.md`](../plan/08-social-carousel-roadmap.md);
the research is [`../research/04-social-carousel.md`](../research/04-social-carousel.md).
Phase 1 — the six tickets the cut order marks *never* — is code-complete and
unit-gated, with the two visual gates run on a real browser and the render
service's stills mode driven live. What is owed is the one gate that needs the
compose stack and a model: a real turn producing a carousel into a thread.

## 1. What a carousel is here

`generate_document` with `format: "carousel"`: the same report spec the PDF,
the deck and the video read, projected by `videoplan.BuildCarousel` onto
`canvas.Portrait` (1080×1350, 4:5), drawn by `apps/render` one still a scene at
`output: "stills"`, and stored as N JPEG pages beside a zip. The thread gets
one assistant message: the caption in a fenced block, the hashtags, one image
a slide on the authenticated page route, the zip link presigned. No new tool,
no second rendering pipeline, no generated imagery, no posting — the four
things §1 of the roadmap says it is not.

## 2. Ticket by ticket

| Ticket | What landed | Gate |
| --- | --- | --- |
| `T-G1` | `carousel\|karusel` in the topic regex, one clause in the classifier's TRUE list, two eval cases, the Marketing persona and a fourth starter question | `go test ./internal/guardrails ./internal/agenttemplates` green; `make eval -only guardrail` **owed** (model spend) |
| `T-G2` | `canvas.Surface`; `Wide` is the old constants by value; `pptx` names it once; five wide goldens byte-identical | `git diff --exit-code videoplan/testdata` clean; pptx fixtures unchanged |
| `T-G3` | `canvas.Portrait`, `BuildCarousel`, `CheckCarouselLimits`, `Plan.Still`, `Scene.Alt`, `Limits.MinSlides/MaxSlides`, the sixth golden, `make types` | videoplan suite green; `carousel.plan.json` 7 slides with Indonesian alt text |
| `T-G4` | `useSceneFrame` + `StillFrame` context (not `<Freeze>`, §3), portrait KPI column, safe-zone overlay in Studio, wrapping fact strip | 32 wide stills pixel-identical before/after; 7 portrait stills with 0 px of ink in either safe zone ([contact sheet](assets/carousel-portrait-stills.png)) |
| `T-G5` | `POST /v1/render {output: "stills"}`, `GET /v1/jobs/:id/result/:page`, 409 on the bare `/result`, `pages` in status, 400 for a plan/output mismatch | 14 `node --test` cases; driven live over HTTP (§4) |
| `T-G6` | `domain.DocumentFormatCarousel`, `documents.page_count` (`074`), `spec.Social`, `video.Client.RenderStills`, `docgen.renderCarousel` + zip + manifest + `LoadPage`, the tool's format, the announcement, `GET /api/documents/:id/pages/:page`, the dashboard's authenticated slide strip and documents row, OpenAPI + SDKs | 20 new unit tests across seven packages; the live turn **owed** (§5) |

## 3. Findings, in the order they were found

1. **`<Freeze>` cannot freeze a short plan.** Remotion clamps the timeline
   position to the composition's `durationInFrames - 1` before it subtracts
   the Sequence offset, so `<Freeze frame={60}>` inside slide *i* of a
   seven-frame plan draws frame `6 − i`. The first portrait render showed one
   KPI card of three and a table with no rows, and freezing at 6, 60 and 1000
   produced identical pixels — the clamp's signature. `packages/motion/src/frame.ts`
   is the override, a React context the scene components read instead of the
   raw hook; the comment there records the diagnosis.
2. **"Stack when `content_width < body_height`" is false on the 4:5 surface.**
   The safe zones take enough height that the portrait measure (921 px) is
   wider than its body (874 px). `canvas.Surface.Portrait()` and
   `plan.isPortrait()` ask the frame instead.
3. **A one-line callout heading truncates on the portrait measure** —
   *"WhatsApp tumbuh paling…"* in the first golden. Two lines on a portrait
   surface, keyed on the same predicate; the wide goldens did not move.
4. **`packages/api-types/src/domain.ts` was stale on `main`**: `T-D16` and the
   api-keys fix changed Go structs without `make types`. Regenerated with
   `T-G3`; nothing imported the removed type.
5. **`pnpm --filter widget build` built nothing** since `T-V1`: the package is
   `@argentum/widget-app`, and pnpm exits 0 on an empty filter. Corrected in
   `verification.md` and the roadmap's gate lines.
6. **Render announcements never reach a channel.** `runThreadRender` appends
   the message and publishes on the dashboard bus; WhatsApp, Discord and Lark
   deliver only inside a turn. That was already true of the mp4 announcement.
   The roadmap's WhatsApp acceptance item for `T-G6` is therefore not reachable
   by this ticket, and no `![` can reach a phone by this path. Filed in §5
   rather than closed by a change that would give every render result a
   channel delivery it has never had.
7. **`Type.Display` was 43, not the 43.5 its comment claimed**, for the life
   of the constant. Pinned by value in `canvas_test.go`.

## 4. The render service's stills mode, driven live (2026-09-04)

`tsx src/server.ts` on 8097, the carousel fixture plan as `output: "stills"`:

```
status: {"output":"stills","state":"done","progress":1,"size_bytes":410849,"frames":7,"pages":7,"render_seconds":5.329}
/result            → 409 {"error":"a stills job has pages; fetch /result/:page","pages":7}
/result/1 … /7     → 200 image/jpeg, 17 660 – 81 803 bytes each, all 1080x1350 baseline JPEG
/result/0, /8      → 404 {"error":"no page 0; this job has 7"}
stills of a video plan → 400 before a browser starts
video of a still plan  → 400, naming `output: "stills"`
DELETE             → the pages' directory is gone (0 `argentum-stills-*` left)
video job status   → same shape as before plus `output`; `pages` absent
```

## 5. Owed

| Owed by | The gate | Needs |
| --- | --- | --- |
| `T-G6` | One real turn — *"Buatkan carousel Instagram 5 slide dari penjualan bulan lalu per channel"* — producing a `documents` row with `format=carousel`, `page_count=5`, five objects under the prefix, a manifest and a zip; one assistant message with the caption, five inline slides and the zip link; the same five images after a reload with `DOCUMENT_PRESIGN_TTL_SECS=5`; 404 from `/pages/6` and from another company's session; a deployment with no `RENDER_BASE_URL` not offering `carousel` | compose stack with render + MinIO, and a model |
| `T-G6` | `074_document_pages` up/down/up against the control database | compose stack only |
| `T-G1` | `make eval EVAL_ARGS="-only guardrail"` with the two new ids passing and the recipe still refused | ~$0.05 of model spend |
| `T-G6` | Channel delivery of a render result (finding 6) — a decision, not a gate: every render result, or none | the owner |

## 6. What needs no ticket, confirmed

A tenant's caption voice is a skill (`when_to_use: "The user asks for a social
post, caption or carousel"`); delivery to a group is `send_message` with
`attach_document_id`, which resolves a carousel to its zip; scheduling is
`schedule_task` with a spec whose format is `carousel`. None of the three
needed a line of code in this track.
