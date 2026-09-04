# Social-media carousels from the agent — what exists, what is missing, and the shape of the build

Written 2026-09-03 against `main` @ `b58e213`. Every repository claim carries
its file; every external claim carries its source URL in Appendix A, with the
ones that could not be verified against an official page flagged there.

> **The request.** A tenant's agent — the Marketing agent in particular — should
> be able to produce an Instagram (or any social) post made of image slides: a
> carousel of 2–10 branded images plus a caption, and eventually publish it.
>
> **The one-sentence finding.** About four fifths of this is already built for
> the video feature and needs a second output shape rather than a second
> pipeline; the fifth that is missing is a 4:5 canvas, a stills mode on the
> render service, an image that survives in the chat, and — the only part with
> an external dependency — publishing, which belongs in the action framework
> and not in a tool.

---

## 1. What "a carousel" is, precisely

A carousel is the video feature with the time axis removed. Both take a report
spec — cover, KPIs, a chart, a table, a closing line — and turn it into a
sequence of framed, branded surfaces. The video plays them; the carousel hands
each one over as a JPEG and lets the reader swipe.

| | Video (`mp4`, shipped) | Carousel (proposed) |
| --- | --- | --- |
| Input | `spec.Document` from `generate_document` | the same spec |
| Surface | 1920×1080, 16:9 | 1080×1350, 4:5 (the tallest ratio Instagram accepts) |
| Unit | a scene of N frames | a slide: one frame |
| Output | one MP4 | 2–10 JPEGs + a caption + alt text per slide |
| Delivered | link in the thread, minutes later | inline previews in the thread, seconds later |
| Leaves Argentum? | never | only in phase 2, through `propose_action` |

The scene vocabulary already fits. `packages/motion/src/plan.ts:31-40` names
eight kinds — `cover`, `section`, `statement`, `quote`, `kpi`, `table`, `chart`,
`closing` — and a marketing carousel is a `cover` (the hook), two to six
`kpi`/`chart`/`statement` slides (the argument), and a `closing` (the call to
action). Nothing needs a new kind to ship the first version.

---

## 2. What exists today, and what each piece already decided for us

Read this table as the reason the build is small. Each row is something a
from-scratch carousel feature would have had to design, and did not.

| Piece | Where | What it gives the carousel |
| --- | --- | --- |
| Spec → beats walk | `internal/report/flow` (`docs/coverage/report-video.md` §T-V1) | Which sections become which slides, shared with PDF/PPTX/MP4 so a report and its carousel say the same thing |
| Plan contract with images as data URIs | `internal/report/videoplan/plan.go:66-71`, `scenes.go:404` (`pngDataURI`) | A self-contained plan the renderer can draw with **no network**, which is what lets `apps/render` run egress-denied |
| Charts as PNG, drawn in Go | `internal/report/chart/chart.go` (go-analyze/charts, design-token palette) | Chart slides for free, and the same bytes the PDF embeds — one chart, never two that disagree |
| Tenant brand | `internal/domain/branding.go` — `LogoKey`, `PrimaryColor`, `Locale`, `ShowArgentumCredit` | Every slide carries the tenant's mark and accent without a new settings page |
| Remotion compositions | `packages/motion/src/scenes/index.tsx`, `chrome.tsx` | The eight slide kinds, already designed against the tokens |
| **Still rendering** | `apps/render/src/fixture.ts:57` — `renderStill()` is already used to produce mid-scene PNGs for the gate | The exact API a stills mode needs, proven on this bundle |
| Render service, no framework, egress-denied | `apps/render/src/server.ts` (`POST /v1/render`, job store, shared secret) | One new mode, not one new service |
| Async format + queue + announce-back | `domain.DocumentFormat.Async()` (`document.go:34`), `queue.TypeReportRender` (`tasks.go:36`), `APIReportService.runThreadRender` + `announce` (`api_report_service.go:304, 358`) | The "posted into this conversation when done" path, with progress events on the thread channel |
| Format enum narrowed to what this process can finish | `tools/generate_document.go:75` (`formats()`), `:256` | The pattern for adding `"carousel"` without advertising it where no render service is configured |
| Documents + object storage | `documents` table, `storage.PresignKey` (`minio.go:164`), `DOCUMENT_PRESIGN_TTL_SECS` default 3600 (`config.go:703`) | Storage and download links, one row per artifact |
| Marketing agent template | `config/agent_templates.yaml:125-143` | The persona, tool set and source hints already exist; `generate_document` is already in its `suggested_tools` |
| Skills (`T-K1`→`T-K10`) | `internal/skill`, `docs/coverage/skills.md` | Where "how we write posts — voice, hashtags, always end with the store link" lives. **Zero code**: it is a tenant-authored skill |
| Action framework | `internal/actions/action.go` — propose, human approves, execute once; kinds `send_message`, `http_action`, `mcp_call` | Publishing is by definition an action ("changes something outside Argentum") and slots in as a new kind |
| Bounded render limits | `RENDER_MAX_SCENES=60`, `RENDER_MAX_FRAMES=18000` (`config.go:687-688`); `videoplan.Limits` (`build.go:48`) | A carousel is ≤10 slides × 1 frame — three orders of magnitude under the video's budget |

---

## 3. What is missing — the six gaps, ranked by how much they decide

### 3a. A 4:5 canvas (the one real design change)

`internal/report/canvas/canvas.go:1` is "the 16:9 surface", with `WidthMM`,
`HeightMM` and `PxPerMM = 1920/WidthMM` as package constants. The plan does
carry `width`/`height` (`plan.go:66`), but the `Metrics` block — margins,
`content_width`, `body_height`, the type scale — is measured against 16:9, and
the plan's own comment says why that matters: "a plan measured for 1920×1080 is
not a plan for any other size: the line breaks in it were decided against that
width."

So a carousel needs a second geometry, not a second constant. The right move is
a `canvas.Surface` value (width, height, margins, type scale) with the 16:9
surface as one instance and a 4:5 portrait surface as another, threaded through
`flow` → `videoplan` → the Remotion `Frame`. Portrait changes the design, not
just the numbers: the KPI row becomes a KPI column, a table has fewer visible
rows, the title band takes proportionally more height, and the type scale goes
up because a phone screen is the target. The `chrome.tsx` components read every
position from `metrics`, so most of this is measuring, not redrawing.

**Safe zones.** Instagram's UI overlays the bottom ~150 px (like/comment/save,
swipe dots) and its grid preview crops 4:5 to 3:4. Keep type inside
~1000×1270 centred; put nothing load-bearing in the bottom band. These pixel
values are community guidance, not Meta's (Appendix A, D).

### 3b. A stills mode on the render service

`POST /v1/render` returns one MP4. A carousel needs N JPEGs. Two options:

1. **`output: "stills"` on the same route.** The job renders `renderStill()` once
   per scene at its midpoint frame (exactly what `fixture.ts` does today, with
   the same "mid-scene, not frame 0" reasoning — frame 0 is the entrance at zero
   opacity), writes `01.jpg … NN.jpg`, and `GET /v1/jobs/:id/result/:page`
   returns one page at a time. `imageFormat: "jpeg"`, `jpegQuality: 90`. No
   zip here: Node has none in its standard library and this service's posture is
   that it has almost nothing in it; Go has `archive/zip` and builds the
   download (roadmap decision 5).
2. A new route. Not worth it: the auth, body cap, job store, TTL sweep and
   progress poll are the parts that took the time, and they are shared.

Remotion's own stills API is `renderStill({composition, serveUrl, output, frame,
imageFormat, jpegQuality, inputProps})` (Appendix A, F). One consideration the
video did not have: **the entrance animations must be skipped for a still**, or
each slide has to be sampled at its final frame rather than its midpoint. A
`static: true` prop on the composition that makes `enter()`/`rise()` return
their end state is cleaner than picking frames.

**Instagram's constraints decide the export.** JPEG only; 8 MB max; ratio between
4:5 and 1.91:1; width 320–1440; sRGB; all slides cropped to the first slide's
ratio (Appendix A, A). 1080×1350 sRGB JPEG at quality 85–90 lands well inside all
of them, and one surface for every slide means the cropping rule can never bite.

### 3c. Images the chat can actually show

This is the gap most likely to be missed and to embarrass the feature.

- `markdown-renderer.tsx:71` overrides only `a` (for embedded dashboards).
  react-markdown renders `![alt](url)` as an `<img>` by default, so inline
  previews work **on the turn they arrive**.
- But the URL the tool returns is presigned for 3600 s (`config.go:703`). The
  message is persisted with that URL in its content, so on tomorrow's reload
  every slide is a broken image. The video did not hit this because a link a
  user clicks can be re-presigned on click; an `<img>` cannot.

The fix is a stable, authenticated route — `GET /api/documents/:id/pages/:n` —
that streams the object (`storage.StreamKey` exists, `minio.go:151`) under the
session's tenant, and messages that reference *that* URL. Pages of a carousel
are then also the natural thumbnail for the Reports tab, which today lists
documents by name only.

### 3d. The topic guardrail will refuse the request

`config/guardrails.yaml:25` — `require_analytics_topic` — admits a message if
any regex matches or the LLM classifier says TRUE. The classifier's rule is:
"If answering means reading THIS organization's own business data … TRUE.
Otherwise FALSE."

"Make an Instagram carousel about our September promo" reads the tenant's data
(the promo's numbers) and produces a marketing artifact from it, but the words
that would satisfy the regex are Indonesian (`pemasaran`, `iklan`, `kampanye`,
`promosi`) or BI nouns, and the classifier prompt has no example of a *produce
content from our data* request. `gpt-5-nano` will get this wrong often enough
to matter, and the eval history of that rule (the nasi goreng case, 2026-08-14)
is exactly the kind of drift a new capability reintroduces.

**This is a one-line prompt change plus eval cases, but it is a gate, not a
nice-to-have:** the feature is unreachable until the guardrail knows it exists.
Add one TRUE example — "a social post, slide deck or report built from their
figures" — and two eval cases (EN and ID) before anything else ships.

### 3e. Publishing — the only part that leaves the building

Nothing in the product can post to Instagram today, and nothing should be able
to without an approval card. The action framework was built for exactly this:
"An Action changes something outside Argentum … the one capability the agent
may *propose* but never *perform*" (`actions/action.go:1-13`).

Three routes to a published post, from zero code to the most:

| Route | Code here | External prerequisite | Verdict |
| --- | --- | --- | --- |
| **Tenant's own MCP server** via `mcp_call` (`mcp_call.go:86`) | none | the tenant runs or subscribes to an Instagram-capable MCP server | Works today for a technical tenant; not the product's story |
| **Aggregator** (Ayrshare, Postiz, Zernio) as an `http_action` endpoint or a first-class `publish_post` kind | small | the tenant's aggregator account; Argentum needs no Meta app review | **Right first step.** Postiz is AGPL and self-hostable; Ayrshare is $149/mo+; Zernio is per-account pricing (Appendix A, C) |
| **Meta Graph API directly** as `publish_post` | medium | Instagram Business/Creator account, `instagram_business_content_publish`, Meta **App Review + Business Verification** to serve accounts Argentum does not own | The eventual destination; the review is weeks and is not engineering |

The direct Graph flow is three calls — one container per child with
`is_carousel_item=true` and a **publicly reachable `image_url`** (Meta fetches
it; there is no upload), one `CAROUSEL` container with `children`, one
`media_publish` — with containers expiring after 24 h and a limit of 100
API-published posts per rolling day (Appendix A, A). "Publicly reachable" means
the MinIO endpoint must be reachable from Meta's fetchers, and the presign TTL
for that call must outlive the container (≥ 24 h, not 1 h). That is a policy
question for the deployment, not a code problem, and it is why the aggregator
route is first: an aggregator uploads.

Whatever the route, the approval card's `Describe()` must show the caption and
the slide count — "Publish a 6-slide carousel to @toko_contoh: 'September promo
recap…'" — because that sentence is what a human is approving.

### 3f. AI-generated imagery — deliberately not in the first version

Argentum has no outbound image path. The only multimodal call in the codebase is
`internal/dococr` — inbound OCR, off by default, and its package comment is a
paragraph about why a tenant's bytes leaving the deployment is an operator's
decision (`dococr.go:3-9`). Claude models do not generate images, so any
illustration or background would be a second vendor (OpenAI `gpt-image-1.5` at
roughly $0.01–0.20/image, Google Gemini image models with a SynthID watermark,
FLUX, Ideogram — Appendix A, E).

The first version should be **typographic and data-driven**: brand colour, the
tenant's logo, big numbers, one chart, the tokens' type. That is what the
video already looks like, it is deterministic, it costs nothing per slide, and
it is what a business-data carousel *should* look like — a made-up hero image
next to a real revenue figure lowers trust in the figure. Generated backgrounds
are a phase-3 option behind a flag with the same posture as `DOC_OCR_ENABLED`.

---

## 4. Two things that are not gaps, because they are already the right shape

**The copy.** Caption, hook line, hashtags and per-slide alt text are text the
model writes from the numbers it already has. `generate_document` already asks
the model for a spec with titles and paragraphs; a carousel spec asks for a
caption alongside. The tenant's voice — "always end with the store link, never
use more than five hashtags, write in Indonesian" — is a **skill**, which is the
feature `T-K` shipped for exactly this conditional instruction, and it costs one
index line per turn (`docs/coverage/skills.md` §2).

**The tool surface.** Do not add a tool. `generate_document` is 20.5% of the
fixed prompt overhead already (`docs/plan/07-agentic-skills-roadmap.md` §2b) and
the always-on channel is full. `"carousel"` joins the `format` enum the way
`"mp4"` did — advertised only when a render service is configured, with a
three-line description of when to choose it — and the model already knows how
to build a spec.

---

## 5. The build, sized

Sized as ten tickets, `T-G1` → `T-G10`, in
[`../plan/08-social-carousel-roadmap.md`](../plan/08-social-carousel-roadmap.md)
(written the same day; its ticket ids and numbers supersede any earlier draft
of this section). The shape:

| Phase | Tickets | Days | What the user gets |
| --- | --- | ---: | --- |
| 1 | `T-G1` guardrail · `T-G2` surface refactor · `T-G3` portrait plan · `T-G4` portrait layout · `T-G5` stills mode · `T-G6` the format, queue, announce and pages route | 9.0 | 2–10 branded slides and a caption in the conversation, downloaded and posted from the phone |
| 2 | `T-G7` slides on the approval card · `T-G8` `publish_post` via an aggregator | 3.0 | approve a card, and it is live |
| 3 | `T-G9` direct Meta + square/story sizes · `T-G10` generated backgrounds behind a flag | 3.0 + calendar | optional |

Phase 1 is deliverable on its own and is most of the value: an Indonesian SMB
owner who gets six branded slides and a caption in WhatsApp will post them from
the phone in under a minute, which is how most of them post today anyway.

---

## 6. Decisions for the owner

1. **Ship phase 1 without publishing?** The recommendation is yes — publishing
   depends on a Meta review or an aggregator contract, and neither should hold
   up the artifact.
2. **Which aggregator, if any, for phase 2.** Postiz (AGPL, self-hostable, REST
   API on every plan) keeps tenant credentials inside the deployment; Ayrshare
   is the mature hosted option at $149/mo and up. Buffer's and Publer's carousel
   support over API could not be verified.
3. **Remotion licensing.** Free for companies of up to three people; a Company
   License is required at four or more (Appendix A, F). The video feature
   already carries this question; the carousel does not change the answer but
   makes it more visible.
4. **Generated imagery** is a vendor, a bill and a data-egress posture. Defer
   until a tenant asks for it by name.
5. **Whether this displaces anything.** The board carries `T-H4` step 2, `T-H6`,
   `T-H11`, `T-H12` (security). This document states the case; it does not
   make the call.

---

## Appendix A — external facts, with sources

Gathered 2026-09-03. Official documentation unless marked *(third-party)* or
*(unverified)*.

### A. Instagram carousel publishing (Meta Graph API)

- Flow: `POST /{ig-id}/media` per child with `image_url` and `is_carousel_item=true`;
  `POST /{ig-id}/media` with `media_type=CAROUSEL`, `children=<ids>`, `caption`;
  `POST /{ig-id}/media_publish` with `creation_id`. Host `graph.instagram.com`
  (Instagram Login) or `graph.facebook.com` (Facebook Login).
  https://developers.facebook.com/docs/instagram-platform/content-publishing
- Up to 10 items; all cropped to the first item's ratio; Reels cannot be
  children. Minimum count is not stated numerically *(unverified: 2 is implied)*.
  Same URL.
- Image: JPEG only, ≤ 8 MB, aspect 4:5 to 1.91:1, width 320–1440 px, sRGB.
  https://developers.facebook.com/docs/instagram-platform/instagram-graph-api/reference/ig-user/media
- Media must be on a publicly accessible server; containers expire after 24 h;
  400 containers per rolling 24 h. Same two URLs.
- 100 API-published posts per rolling 24 h; a carousel counts as one; query
  `GET /{ig-id}/content_publishing_limit`. The same page's carousel section says
  50 *(inconsistent in Meta's own docs — check the endpoint at runtime)*.
- Account must be Instagram Business or Creator. Permissions:
  `instagram_business_basic` + `instagram_business_content_publish` (Instagram
  Login) or `instagram_basic` + `instagram_content_publish` +
  `pages_read_engagement` (Facebook Login). Serving accounts you do not own
  requires Advanced Access → App Review + Business Verification.
  https://developers.facebook.com/docs/instagram-platform/overview
- Per child: `alt_text` (≤ 1000 chars) and `user_tags` allowed; `caption`,
  `location_id`, `product_tags` not allowed on children — set on the parent.
  https://developers.facebook.com/docs/instagram-platform/instagram-api-with-instagram-login/content-publishing

### B. Other platforms

- **Facebook Page**: `POST /{page-id}/photos` with `published=false` per image,
  then `POST /{page-id}/feed` with `attached_media`. JPEG/PNG/GIF/TIFF/BMP,
  ≤ 4 MB. Max `attached_media` count not documented *(unverified)*.
  https://developers.facebook.com/docs/graph-api/reference/page/photos/
- **LinkedIn** multi-image post: `POST /rest/images?action=initializeUpload`,
  PUT binary, `POST /rest/posts` with `content.multiImage.images[]`; 2–20 images;
  JPG/GIF/PNG. Organic carousels unsupported (ads only).
  https://learn.microsoft.com/en-us/linkedin/marketing/community-management/shares/multiimage-post-api
- **X**: chunked media upload then `POST /2/tweets` with `media.media_ids`;
  up to 4 images ≤ 5 MB. Pay-per-use: $0.015 per post created.
  https://docs.x.com/x-api/media/quickstart/media-upload-chunked ,
  https://docs.x.com/x-api/getting-started/pricing
- **TikTok** photo mode: `POST /v2/post/publish/content/init/`, `media_type=PHOTO`,
  `PULL_FROM_URL` only, up to 35 images, WebP/JPEG ≤ 20 MB; URL domain must be
  verified; unaudited clients are restricted to private posts.
  https://developers.tiktok.com/doc/content-posting-api-reference-photo-post
- **Threads**: same container pattern on `graph.threads.net`; 2–20 items;
  JPEG/PNG ≤ 8 MB; 250 posts/24 h; `threads_content_publish`.
  https://developers.facebook.com/docs/threads/posts

### C. Aggregators with an API

- Ayrshare — $149/$299/$599 per month; carousel via `mediaUrls` (≤ 10).
  https://www.ayrshare.com/pricing/ ,
  https://www.ayrshare.com/docs/apis/post/social-networks/instagram
- Postiz — AGPL-3.0, self-hostable; hosted $29–99/month; REST API on all plans;
  Instagram carousels supported. https://github.com/gitroomhq/postiz-app ,
  https://docs.postiz.com/providers/instagram
- Zernio (formerly Late) — per-account pricing from $6; carousel via
  `mediaItems` ≤ 10. https://zernio.com/pricing ,
  https://docs.zernio.com/platforms/instagram
- Buffer — legacy REST API closed to new apps, retiring 2027-02-01; new GraphQL
  API launched 2026-05; carousel support *(unverified)*.
  https://buffer.com/resources/legacy-rest-api-retired/
- Publer — API on Business/Enterprise only; carousel *(unverified)*.
  SocialBee — no public API found.

### D. Export specification

1080×1350 (4:5) sRGB JPEG, quality 80–90: the tallest ratio the API accepts,
inside the 320–1440 width band, and stored at native resolution. Grid preview
crops 4:5 to 3:4, so keep critical content in the central ~1013 px. Community
safe-zone guidance: ~120 px top, ~150 px bottom clear of type *(third-party,
not Meta)*. https://www.facebook.com/help/instagram/1631821640426723 ,
https://www.outfy.com/blog/instagram-safe-zone/

### E. Server-side image generation (Claude does not generate images)

| Model | ~Price per image | Text in image | Note |
| --- | --- | --- | --- |
| OpenAI `gpt-image-1.5` | $0.009 / $0.034 / $0.133 at 1024² (low/med/high) | strong | https://developers.openai.com/api/docs/models/gpt-image-1.5 |
| OpenAI `gpt-image-2` | token-billed, ≈ $0.006–0.21 *(third-party calc)* | strong | https://developers.openai.com/api/docs/pricing |
| Google `gemini-3.1-flash-image`, `gemini-3-pro-image` | Pro $0.134 (1K/2K); Flash-class ≈ $0.039 | advanced | SynthID watermark; Imagen 4 shuts down 2026-08-17. https://ai.google.dev/gemini-api/docs/pricing |
| BFL FLUX.2 pro/flex/max | from $0.03 / $0.05 / $0.07 | good | https://docs.bfl.ml/quick_start/pricing |
| Ideogram 3.0 | $0.03 / $0.06 / $0.09 *(third-party)* | strongest claim | commercial use permitted. https://ideogram.ai/pricing/?pricing_tab=api |
| Stability Core / Ultra | ≈ $0.03 / $0.08 *(third-party)* | moderate | free under $1M revenue. https://stability.ai/community-license-agreement |

### F. Rendering

- Remotion `renderStill({composition, serveUrl, output, frame, imageFormat:
  'png'|'jpeg'|'webp'|'pdf', jpegQuality (default 80), scale, inputProps})`;
  `renderFrames()` for ranges. https://www.remotion.dev/docs/renderer/render-still
- Remotion licence: free for individuals, ≤ 3-employee companies and
  non-profits; Company License required at 4+ employees ($25/seat/month).
  https://github.com/remotion-dev/remotion/blob/main/LICENSE.md ,
  https://www.remotion.pro/license
- Alternatives, not recommended here because the compositions already exist:
  Satori/@vercel/og (JSX → SVG → PNG, flexbox subset) https://github.com/vercel/satori ;
  Playwright `page.screenshot` https://playwright.dev/docs/api/class-page#page-screenshot
