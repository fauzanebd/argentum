# Social carousels — the video with the time axis removed (T-G1 → T-G6)

Built 2026-09-03/04 against the plan in
[`../plan/08-social-carousel-roadmap.md`](../plan/08-social-carousel-roadmap.md);
the research is [`../research/04-social-carousel.md`](../research/04-social-carousel.md).
Phase 1 — the six tickets the cut order marks *never* — is code-complete and
unit-gated, with the two visual gates run on a real browser and the render
service's stills mode driven live. What is owed is the one gate that needs the
compose stack and a model: a real turn producing a carousel into a thread.
**Finding 6 was decided and closed the next day (§7): a render result now
reaches the channel that asked for it, and the shipped social-post procedure
§6 waited for is in `config/skills/`.**

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
   channel delivery it has never had. **Decided and built 2026-09-04 —
   every render result.** See §7.
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
| ~~`T-G6`~~ | ~~Channel delivery of a render result (finding 6) — a decision, not a gate: every render result, or none~~ **Decided 2026-09-04: every result. Built the same day (§7); the live arm is one carousel asked for from a WhatsApp number, read on the handset — filed in `live-gate-backlog.md` §3** | a real phone |

## 6. What needs no ticket, confirmed

A tenant's caption voice is a skill (`when_to_use: "The user asks for a social
post, caption or carousel"`); delivery to a group is `send_message` with
`attach_document_id`, which resolves a carousel to its zip; scheduling is
`schedule_task` with a spec whose format is `carousel`. None of the three
needed a line of code in this track.

**The shipped procedure the roadmap held back (§6, *"How we write a post"*)
is now the third file in `config/skills/`** — `social-post.md`, written
2026-09-04 once `format: "carousel"` existed for it to open for. It is a
method, not a voice: one finding a post, query before caption, the period
named on the cover, five to seven slides, a hook that survives the 125-char
fold, percentages beside their base, specific hashtags, do not post. A tenant's
own skill of the same name shadows it, which is how a house style overrides the
method. The always-on index cost moved from 691 to **877 runes**
(`builtin_test.go`), which is the number a fourth file has to argue against.

## 7. Finding 6, closed: a render result reaches the channel that asked (2026-09-04)

The decision was *every render result, or none*, and it is every. What the
carousel's own value claim needed — "an owner who receives five branded slides
and a caption in WhatsApp posts them from the phone" — was not reachable by
anything in `T-G6`, and neither was the mp4's link since `T-V3`: the worker
wrote the result to the thread, published it on the dashboard bus, and the
only code that delivers to a phone runs inside a turn that had ended minutes
earlier.

**The target travels with the job.** `tenantctx.ReplyTarget` is the channel
and the refs its reply needs — the same eight fields `completeWith`'s switch
reads — set on the turn's context by `ChatRunner.Run` and copied into
`ReportRenderPayload.Target` by `generate_document` when it enqueues. It is a
payload field rather than a thread lookup for one concrete reason: a Discord
thread row is keyed by user and holds no channel id, so the row could not
have answered for every channel. A job with no target — from `/v1`, or queued
before the field existed — behaves exactly as before.

**Delivery is the channel half of `completeWith`, on the report service.**
`APIReportService.WithChannelDelivery` takes the same four providers the
watcher service holds, and `deliver` sends: WhatsApp with links flattened to
`text: url`; Discord over the outbound bus; Lark as a reply to the message
that asked, or a new message in the chat when there is none; Slack into the
thread. Dashboard, API and widget targets send nothing — the thread message
and the `final` event already reached them. A channel whose provider is not
installed is a Warn line, not a nil dereference. The thread is written first
and the channel second, so a failed send leaves the record intact.

**A carousel has two texts.** The thread's message carries `![` images on the
authenticated page route, which is a broken path on a phone. The channel's
message (`carouselChannelMessage`) is the caption as plain text — a fence is
three backticks on a handset — one **presigned link per slide** on the
document's TTL (`docgen.PresignPage`, the same clock as the zip), and the zip.
Pages are signed only when there is a channel to send to; a page that fails
to sign is left out and logged, so the message is shorter rather than absent.
The mp4 and every failure message are the same string on both sides.

Twenty-two new unit tests across `tenantctx`, `queue`, `tools`, `docgen` and
`app`: the target round-trips and carries every delivery ref and none of the
others; the tool copies it and omits it; each of the four channels receives
the result while the thread still gets its one message and one event; a
thread-only target sends nowhere; a missing provider is skipped; the channel
carousel message has no `![`, no fence and no `/api/documents/` path; and the
WhatsApp body reads `Slide 1 — Cover: https://…`. What only a handset can
answer is in `live-gate-backlog.md` §3.

## 8. `T-G11` — a post is not always a carousel, and not always 4:5 (2026-09-04)

Phase 1 built the thing the roadmap scoped: 2–10 portrait slides projected
from figures the agent queried. The owner's next question was the one that
scope does not answer — *"a discount, or introducing a product"* — and it
found three walls, none of them a defect and all of them the same shape: the
pipeline could only make a **report about data**, and a promotion is an
**announcement**.

**The three walls, in the order a request hits them.**

1. **`Analytical` refused it at the door.** A carousel needed a `kpi_row` or a
   `chart`, a predicate written for the video to keep an invoice out of a
   medium nobody can scan. A promo has neither, so *"Diskon 20%"* was refused
   with advice to add figures it does not have.
2. **The slide floor was 2.** A promotion is one image. The floor answered
   *"add a section"* to a spec that was already exactly what the user asked
   for — the pipeline padding a post on the user's behalf.
3. **Every section type was report furniture.** Cover, heading, KPI row,
   table, chart, callout: all of them sit *under* a title band inside a
   report. Nothing filled a frame with one statement.

### What landed

| Piece | What it is |
| --- | --- |
| `spec.SectionHero` | One statement filling the frame: a kicker (`subtitle`), a headline (`title`) at display size, one supporting line (`text`). No title band, no footer, no generated-at date. It is a cover for a post |
| `canvas.Square`, `canvas.Story` | 1080×1080 and 1080×1920 beside `Portrait`. **Story's margins are not Portrait's scaled** — the app's own chrome takes ~250 px above and ~340 px below, so its safe zones are 44 mm and 60 mm against Portrait's 22 and 27 |
| `spec.Social.Size` | `portrait` (default), `square`, `story`, `landscape`. A **name**, never a width and a height: a surface is margins, a type scale, safe zones and a card cap, all argued for against a frame somebody looked at, and a caller who could ask for 1000×1400 would get a layout whose first viewer is the tenant's audience |
| `videoplan.SurfaceFor` | The size named in the spec chooses the frame, in `BuildCarousel` **and** in `CheckCarouselLimits`, so the door refuses what the worker would refuse |
| `MinSlides: 1` | Was 2. Nothing downstream ever needed two — pages, manifest, zip and announcement are all per page |
| The announcement gate | `Analytical(d) || (carousel && HasHero(d))`. It does not weaken the test it widens: an invoice, an agreement and an export are key/value blocks and tables, and none has ever carried a hero. **The video keeps the narrower test** — a promo clip is a format decision nobody has asked for |
| `trimClosing` | A carousel that would be one image loses the sign-off card |

### Two things the build found, both in the still path

1. **The closing card made a one-image post impossible.** The floor moved to 1
   and the first test still got two slides: the hero, then the report's
   sign-off card with the generated-at timestamp. On a seven-slide carousel
   that card is a conventional last slide and it stays; on a single-image
   promotion it is *half the post*, and it is the report chrome a promo most
   obviously should not carry. `trimClosing` drops it when the plan would
   otherwise be one image.
2. **A spec with nothing in it produced a plan.** With the floor at 1, an
   empty hero left the closing card as the only scene — one slide, inside the
   band, "your carousel is ready", a single card saying when it was made.
   Refused now at `finishStill`, and the hero is refused earlier still by
   `spec` (`hero requires title or text`), because the builder drops an empty
   section silently and a post that quietly lost its only slide is worse than
   a spec the model is told to fix.

### The degradations, written down rather than discovered

A `hero` is a section type, so a spec carrying one can be asked for as any
format. `flow.Sink` gained a `Hero` beat and both implementations answer it:
the **deck** draws a divider carrying the supporting line, with the kicker in
the speaker notes; the **PDF** draws its callout, headline first. Neither
format has a hero and neither pretends to — what matters is that a spec
written for an image and also asked for as a PDF says the same words instead
of failing on an unknown section type, which is what `pdf/sections.go` did to
every type it did not know.

### Driven live (2026-09-04)

`tsx src/server.ts`, one hero, `output: "stills"`, all four sizes:

```
portrait   pages=1 1080x1350  53 697 bytes  0.70s
square     pages=1 1080x1080  49 465 bytes  0.65s
story      pages=1 1080x1920  55 453 bytes  0.63s
landscape  pages=1 1920x1080  57 343 bytes  0.70s
```

One page each, the frame the size named, the kicker above the rule and the
headline at display size ([contact sheet](assets/promo-single-image-sizes.png)).
Sub-second a frame against 8.4s for the seven-slide fixture, because a hero
draws no chart.

**What is not built, deliberately.** No product photograph — there is no
inbound image path in a spec, and `T-G10` (generated imagery) stays unbuilt
and last. An uploaded photo inlined as a data URI the way charts already are
is the honest version of that request and it is a ticket, not a line. Arbitrary
pixel sizes stay out for the reason `Social.Size` is a name.

**What is owed:** the guardrail clause. The topic classifier's TRUE list says
*"a social post … built from their figures"*, and a promotion built from a
price is a coin flip on that wording. Two eval cases and one clause, with the
recipe still refused — filed in `live-gate-backlog.md` §2 beside `T-G1`.

## 9. `T-G12` — the promotion card, and a picture the tenant supplies (2026-09-04)

The owner showed a real shelf-edge poster — a supermarket's *CRAZY DEAL* on
Sunkist Cara Cara oranges, Rp 5.980 struck through, Rp 3.370 per 100 gram —
and asked for *"something like that"*, with the tenant supplying the image.

**What that poster is made of, and where each part could come from.** Four of
its seven parts were already data or already built: the two prices, the unit
and the product name are columns in a price table, and the logo already travels
in every plan. The sunburst and the starburst are shapes a browser draws from
the brand colour and need no artwork at all. What was genuinely missing was a
photograph — there is no image path into a spec, and the only image ingest in
the product was the branding logo — a layout that is a shelf-edge label rather
than a report block, and a second typeface, which is the one thing here that
is **not** built (see below).

### The shape of the answer

| Piece | What it is |
| --- | --- |
| `spec.SectionPromo` | Badge, product name, photograph, `was`, `price`, `unit`, supporting line. The one section type whose *layout* is the deliverable: every other type describes content and lets the surface arrange it |
| `post_images` (`075`) | The tenant's picture library. The third kind of file this product takes from a tenant, and deliberately not in either of the other two tables — `source_documents` is a file to be **read**, `documents` is a file the agent **generated**, this is a file to be **drawn** |
| `internal/postimage` | Upload, list, resolve, delete. PNG-normalised and downscaled to 2048px like the logo, 4 MB in (eight times the logo's cap, because a phone photograph is routinely two) |
| `GET/POST/DELETE /api/post-images` | Admin uploads and deletes, any member lists and reads. The read is authenticated and never presigned, which is `T-G6`'s rule: a picker is a screen somebody leaves open, and a presigned URL in one is a broken image an hour later |
| `videoplan.PromoBrand` | Five colours derived in Go from the tenant's accent, carried in the plan because `make motion-guards` forbids the renderer from naming a colour of its own. A shop with a green brand gets a green promotion |
| The `Promo` component | The only scene that draws its own ground, because a sunburst is full-bleed by definition. The safe zones still bind the content |

### The name is the interface

The model writes what it is looking for — *"jeruk cara cara"* — and the
**door** resolves it against that company's library, in the turn. Three
decisions fall out of that and each is load-bearing:

- **Resolved at the door, not in the worker.** A picture that misses comes back
  in the tool result with the name that missed and a sentence about uploading
  one. Four minutes later, in a worker, nobody is left to tell.
- **Exact match on the lower-cased name, nothing cleverer.** A prefix or
  similarity match would let "jeruk" pick one of five citrus photographs and
  give a promotion the wrong product, which is a mistake nobody notices until
  it is public.
- **The bytes travel in the queue payload, not the id.** That is a real cost —
  a job with a photograph in it is a megabyte in Redis — paid for a property:
  the worker draws exactly the picture the turn resolved and reported, where an
  id would be re-read minutes later and a rename or a delete would make the
  card disagree with what the user was told.

**A missing picture is never a failed turn.** No library, no match, or a row
whose object has gone: all three draw the card without a photograph and say so.
A promotion is not worth failing a turn over.

### Two defects the first render found, both mine

1. **The card overflowed its own frame.** The photograph is a flex child, and a
   flex item's default `min-height: auto` refuses to shrink below its content —
   so the product pushed the name and the price panel off the bottom on every
   surface shorter than a story. The first render of this component was a
   promotion **with no price on it**, which is the one thing a promotion is.
2. **The name was broken against a width it is not drawn at.** The builder
   wrapped the product name to 62% of the measure, from an earlier sketch where
   the name and the price sat side by side; the layout that shipped stacks them.
   *"Jeruk Sunkist Cara Cara"* wrapped to two lines on a card with room for one.
   Go measures and the browser draws, and the two agreeing is the whole basis of
   this pipeline — a constant that encoded a layout nobody built is exactly how
   they stop agreeing.

### Driven live (2026-09-04)

The card, one section, through the real render service in three sizes:

```
portrait  1080x1350  113 172 bytes
square    1080x1080   90 596 bytes
story     1080x1920  131 785 bytes
```

The three cards are [here](assets/promo-card-sizes.png), beside the
[four one-image sizes](assets/promo-single-image-sizes.png) `T-G11` produced.

Prices formatted by the document's own machinery and **never compacted** — a
KPI card says "Rp 3,86 Miliar" because the exact figure is in the table it
summarises, and a promotion card *is* the exact figure. The alt text carries
both prices with their labels, because a description of a promotion that omits
them describes a photograph.

### The degradations, and the one gap

A `promo` is a section type, so a spec carrying one can be asked for as any
format. The **deck** draws a divider with the prices on one line; the **PDF**
draws a KPI row, *Before* and *Now*, which is the one shape a page has that
puts two figures next to each other and expects a comparison. Neither pretends
to be a poster.

**Not built, and it is the visible difference from the reference.** That poster
sets the product name in a script face with a white outline; this product ships
one typeface, so the name is set in it. Vendoring a display face is a licensing
decision and a build change, not a line of code, and it is the next thing worth
doing if these cards are used in earnest.

**Style-matching a reference poster is filed and not built.** The owner chose
"photo slot first", and the second half — the tenant uploads a poster they like
and the layout is inferred from it — needs a multimodal call whose output is
not predictable between runs. It is a ticket in `plan/08` rather than a
half-built path here.

**What is owed:** the same live turn every carousel gate owes, now with an
upload in front of it; `075_post_images` up/down/up against the control
database; and the guardrail clause, which this ticket makes more pressing —
*"buatkan promo diskon"* is further from *"a social post built from their
figures"* than a carousel was.

