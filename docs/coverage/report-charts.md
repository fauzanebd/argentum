# Chart images — `T-R3` Record

Shipped 2026-07-28. A report with a trend in it stopped being a table with a
cover page.

Seven chart types render to a PNG that both document renderers embed: the PDF
today, the deck (`T-R4`) from the same bytes. Everything visible in one — the
series colours, the axis rules, the type — is a design token, so a chart looks
like the document it is in rather than like the library that drew it.

```
internal/report/chart/
   chart.go      Render, geometry, the supersample-and-downscale path
   draw.go       one option builder per type, plus the axis and legend rules
   normalize.go  the caps, the Other buckets, the empty and single-point states
   empty.go      the "no data for this period" panel
   theme.go      tokens → the library's ColorPalette, and the font registry
   labels.go     the handful of words the renderer chooses, per locale
```

## The library

`github.com/go-analyze/charts` v0.6.0. The ticket asked for it to be evaluated
first, and it was kept.

What decided it: it draws all seven of the required types against one option
struct — grouped bars, stacked bars and a doughnut included — and it takes a
caller-supplied `ColorPalette`, `*truetype.Font` and `ValueFormatter`, which is
exactly the three things a chart has to borrow from a design system. It is pure
Go, no CGO and no headless browser.

`wcharczuk/go-chart/v2` was the stated fallback and would have needed grouped
and stacked bars written by hand against its renderer. That is the point where a
chart library stops being a dependency and becomes a fork.

Two things the library does that had to be corrected rather than configured, both
recorded because they are invisible until you look at the output:

- **Axis and legend labels resolve their own font, and ignore the painter's.**
  `prepAxisStyles` fills a `FontStyle` with no font, which falls back to the
  library's bundled Roboto. Every `FontStyle` this package builds therefore names
  the face explicitly. The symptom was a chart set in Roboto inside a document
  set in Space Grotesk — the exact drift `packages/design-tokens` exists to stop,
  arriving through a library instead of through a hand edit.
- **Type is sized at 92 DPI, not 72.** `chartdraw`'s `defaultDPI` is 92, so a
  `FontSize` of N puts N × 92/72 pixels on the canvas. `geometry.ptPx` divides it
  back out, which is what makes `theme.TypeScale.Caption` mean 8 typographic
  points on the printed page.

## Resolution

Charts are drawn at **2× and downscaled**, to a final **200 DPI** over their
printed size.

200 rather than 300: a chart is flat fills and strokes, where the difference is
invisible on paper and doubles the file. 2× rather than 1×: the library
antialiases its geometry but not its type at these sizes, and drawing large then
resampling is what makes a three-pixel axis label crisp instead of furry. The
downscale is CatmullRom from `x/image/draw` — the sharpest resampler there that
does not ring, which matters because the content is high-contrast strokes on a
flat ground.

Output is a raster and not an SVG because neither consumer takes one: maroto
embeds images, and OOXML drawings are not SVG. The sharpness has to come from
resolution.

**It was 3× until 2026-08-21** (`T-R6`), and the factor came down because it
squares into the canvas: 3 rasterises nine times the final pixel area, and each
of those pixels is allocated twice more — decoding the PNG the library returns,
and again as the CatmullRom destination. A nine-chart report killed the worker
mid-turn, and a turn that dies inside a tool call never writes a reply, so what
a customer saw was not an error but a conversation that stopped.

`BenchmarkRenderFullMeasure` now lives in the package, because every other
render in these tests is 90mm — a quarter of the pixels — so nothing here
measured what a real chart costs. One grouped bar, 90mm tall:

| | supersample 3 | supersample 2 | |
| --- | --- | --- | --- |
| PDF, 174mm | 201.7 MB/op, 532 ms | 113.9 MB/op, 299 ms | −44% |
| deck, 254mm | 293.5 MB/op, 790 ms | 165.4 MB/op, 443 ms | −44% |

For a final image of ~35KB either way.

**What 2 gives up is not what the change assumed.** The claim was "a fraction of
a pixel of edge contrast on glyph stems". Both sheets were rendered and the
axis-label region compared: on mean absolute horizontal gradient — acutance —
**supersample 2 scores 1.3% higher than 3**, not lower, because a shorter
downscale blurs less. The crops differ by a mean of 2.8/255, 4.9% of pixels by
more than 8/255, maximum 142/255 on stem edges that land the other side of a
pixel boundary. Magnified 3×, *"Rp 400 Juta"* reads identically at both. What 3
buys is smoother antialiasing on diagonals, which an axis label is not made of.

If the type ever does look furry, the fix is a higher `renderDPI` on a *smaller*
canvas, not a larger multiple of the same one.

**One thing this moved that is not a chart.** `videoplan` pins its chart image
by `sha256`, so different pixels are a different golden — it failed with *"plan
differs from testdata/…; run with -update"*, which reads like a stale fixture
and is not one. Anything that hashes a rendered chart moves with the factor.

## The palette, and the green that was not colourblind-safe

The ticket asked for the categorical palette to be verified under deuteranopia
simulation and in greyscale, with the method stated. Writing the verifier found
a real defect in the palette `T-R1` shipped.

The method, now in `packages/design-tokens/scripts/palette.mjs` and gated in CI:

- **Greyscale** — CIE L\* is the greyscale axis, so the printed distance between
  two series is |ΔL\*|. Floor: 5. Below about that, two colours are the same grey
  on an office laser printer.
- **Colour vision** — Brettel/Viénot/Mollon (1999) LMS-space simulation of
  deuteranopia *and* protanopia, then CIE76 ΔE\*ab between the simulated pairs.
  Floor: 12. A just-noticeable difference is ~2.3; 12 is where two series stop
  being separable at the width of a chart line.

CIE76 and not CIEDE2000: sixty lines of arithmetic with no reference
implementation to check it against, in a package that deliberately has no
dependencies. The two disagree most where CIE76 *over*-states a difference, so a
palette clearing a generous CIE76 floor clears the CIEDE2000 one it stands in
for.

Run against the shipped palette, it failed twice:

```
Deuteranopia: #F25C5C (brand red) vs #75AF4B (green) — ΔE*ab 5.0   [floor 12]
Protanopia:   #EAAA3E (amber)     vs #75AF4B (green) — ΔE*ab 9.1   [floor 12]
```

A sweep of the whole HSL cube found **no green at any lightness** that clears
both floors against this palette. The reason is structural: under deuteranopia
red, amber, brown and green all collapse onto the same yellow axis, where only
L\* separates them, and the palette's warm rungs (59.3, 73.9, 32.1) have already
spent the lightness a green would need — and the gaps that remain are inside
teal's greyscale neighbourhood.

So series 8 left the red-green axis entirely: **`#75AF4B` green → `#5CA8E0`
azure**, L\* 66.3, clearing greyscale by 7.0 and the two deficiencies by 24.8 and
31.8. It lost to arithmetic rather than to taste, and the arithmetic is
reproducible with `make palette`.

Current state, all eight series:

```
Greyscale     tightest: brand red vs azure   — ΔL*    6.8  [floor 5]
Deuteranopia  tightest: navy vs purple       — ΔE*ab 18.0  [floor 12]
Protanopia    tightest: brand red vs teal    — ΔE*ab 12.7  [floor 12]
```

Protanopia has the least headroom. A future palette edit that lands near that
pair will fail the gate rather than ship quietly.

### Semantic colour moved out of the series ramp

Replacing the green broke two things that were reading meaning out of a
categorical palette: `ChartPalette[7] // green` was the colour of a good KPI
delta, and `[2]` was a warning callout's spine. Both would have turned blue.

`color.positive`, `color.warning` and `color.info` are now print-scoped tokens,
and `toneColor` and the KPI delta use them. A ramp ordered by separability is not
a vocabulary of meanings, and the comment `// green` beside an index was the
smell.

Unlike the series palette these three are **not** required to be distinguishable
from each other under CVD: each is drawn beside a redundant cue that carries the
meaning on its own — an ↑/↓ arrow, a signed percentage, a callout title. Stated
in `tokens.json` so it is a decision rather than an oversight.

## Gate

`docs/coverage/assets/chart-contact-sheet.png` and
`chart-contact-sheet-greyscale.png` — all seven types in the brand palette, and
the same sheet converted the way a monochrome printer converts it (linear
luminance re-encoded to sRGB, the same transform as `greyscale()` in
`lib/color.mjs`).

Both are produced by `TestContactSheet`, which composes and asserts on them on
every run and writes them only when `CHART_SHEET_DIR` is set:

```
CHART_SHEET_DIR=docs/coverage/assets go test ./internal/report/chart/ -run TestContactSheet
```

A gate artifact produced by a command nobody runs is a gate that stops being
checked, so it is a test.

## Decisions worth knowing

**The category cap does not apply to line charts.** The ticket says cap
categories at 40 and bucket the rest into "Other". That is right for bars and
slices, where a category is an unordered bucket — the smallest twelve product
lines genuinely are "other products". It is wrong for a line, where the x-axis is
a sequence: the smallest twelve days of a month are not "other days", and folding
them would put an invented point on a real timeline. A dense line is legible in a
way a dense bar chart is not; what gets thinned there is the axis labels, above
twelve categories, not the data.

**Series over the cap are dropped, except in a stack.** Eight is where the
palette runs out, and a ninth series would wrap onto the first one's red. What
happens to the ninth depends on whether adding it up means anything: in a stacked
bar the bar's height is already a sum, so the remainder becomes an "Other" band
and the total still reconciles. In a grouped bar or a line chart it is not — the
sum of "Direct" and "Referral" is not a line anybody asked for — so the remainder
goes, and the caption says so.

**Every cap writes a sentence into the caption.** `Result.Note` comes back as
text and the PDF renderer joins it to the spec's own caption. A chart quietly
showing eight of eleven series misleads by omission, which is the same failure as
an axis that does not start at zero approached from the other side.

**Bar charts are forced to a zero baseline; line charts are not.** A bar encodes
its value as a length, so an axis starting at 390 million draws a 400-million bar
at a twentieth the height of a 600-million one. A line encodes value as position
and does not have that problem, so it keeps the library's fitted range and the
resolution that comes with it. An explicit `y_axis.min` in the spec still wins
over both.

**A flat series gets a range anyway.** When every finite value is the same
number, the library computes a zero-height range and prints the same tick three
times against a line pinned to the middle of the frame. Anchoring at zero turns
it back into a reading.

**Titles and captions are document text, not pixels.** The chart's title is set
as an H2 in the document's own type and its caption as a muted caption, so both
wrap, both are selectable, and both are the same size as every other heading and
caption on the page. It also means `T-R4` can take the identical image and set
the same words at slide scale.

**`stacked` is a chart type, not a flag.** A `stacked: true` boolean would make
`{type: "pie", stacked: true}` expressible, and every such combination is a
validation rule to write, test and explain to the model.

**Chart validation is strict where the rest of the spec degrades.** Everywhere
else in `spec`, a malformed field renders as something: a cell that will not
parse prints as text, a KPI card written with the wrong key names still gets a
card. A chart cannot degrade. Three values against five labels is not a chart
missing two points, it is a chart whose points are against the wrong labels, and
it draws without complaint — the reader has no way to see that it is wrong. So a
mismatch is an error naming both counts, which the model can act on inside the
same turn.

## The two states that are not a chart

**No data.** Every value non-finite, or no series left after the empty ones are
dropped. Renders a framed panel in the document's border and muted grey saying
"No data for this period" / "Tidak ada data untuk periode ini". Not a blank
rectangle: a reader's first thought at a blank is that the file is broken and
their second is to wonder what else is missing. It is drawn rather than delegated
to the library's own no-data rendering, which comes in the library's grey, the
library's Roboto and the library's size.

**One point.** A line through one point draws no segment, so the point carries
the chart: a filled dot (`SymbolDot` — the library's `SymbolCircle` is a ring
filled with the background, which downsamples to almost nothing), a boundary gap
so it is centred rather than half off the left edge, and its value written beside
it. A one-bar bar chart gets the same value label. Above one point the symbols
and labels come off — at forty points they are the densest ink on the page and
say nothing the line does not.

## Verification

```
make palette                          → 8 series clear every floor
go test ./internal/report/...         → ok (chart, format, pdf, theme)
make tokens && git diff --exit-code   → clean
```

`internal/report/chart` covers: all seven types render and are not blank; the
PNG dimensions match what `Result` reports; the brand palette actually appears in
the pixels; two renders of one chart are byte-identical; the no-data panel and
the single-point dot are drawn; the series cap drops and the stacked cap buckets;
the category cap bucketed a bar chart and left a line chart's sixty points alone;
and five malformed specs are rejected by both `Validate` and `Render`.

The image assertions are ink coverage and palette-colour counts rather than
golden PNGs. A golden bitmap would fail on any freetype or resampler upgrade
while saying nothing about whether the chart is right; what these catch is the
failure that actually happens — an empty frame, because a series was dropped, an
axis collapsed, or a symbol was never drawn.

## The dark ramp (2026-08-17)

The ladder above was verified against paper and against two colour-vision
deficiencies, and **never against the surface it is drawn on** — because when it
was written the dashboard had no charts and a document has one background,
white. The moment the chat transcript started drawing panels (T-D11), series 2
(navy, L\* 24.2) and series 7 (brown, L\* 32.1) were dark marks on a dark card:
1.35:1 and 1.80:1 against `#232427`.

`tokens.json` now carries `chart.paletteDark`, emitted under `.dark` in
`tokens.generated.css`. Four of the eight series did not move — the whole method
was to measure first and lift only what fails, so a reader who switches themes
does not have to re-learn which line is revenue:

| # | Light | Dark | Why |
| --- | --- | --- | --- |
| 1 | `#F25C5C` | `#F25C5C` | 4.77:1 — the brand red survives the move unchanged, which is why it stays the accent in both themes |
| 2 | `#1C3A62` | `#4981CB` | 1.35:1 → 3.91:1 |
| 3 | `#EAAA3E` | `#EAAA3E` | 7.63:1 |
| 4 | `#2E7E71` | `#318578` | 3.21:1 → 3.52:1, lifted to clear the working floor |
| 5 | `#774C96` | `#9C7AB4` | 2.41:1 → 4.33:1 |
| 6 | `#CACCD1` | `#CACCD1` | 9.66:1 |
| 7 | `#713F1C` | `#B9672E` | 1.80:1 → 3.73:1 |
| 8 | `#5CA8E0` | `#5CA8E0` | 6.01:1 |

**The dark ramp carries no greyscale floor, and that is the argument rather than
an oversight.** ΔL\* in greyscale exists for the office laser printer; nothing
prints a dark dashboard. Applying it anyway is what the first two attempts at
this table did, and it pushed three series into near-whites — the band above the
contrast floor is only wide enough for about nine rungs, and spending them on a
reader who does not exist is how a palette becomes illegible in order to satisfy
a check. What replaces it on screen is a normal-vision ΔE\*ab floor of 15.

Measured, both ramps, by `make palette`: dark normal-vision tightest pair 19.6,
deuteranopia 14.2, protanopia 13.0, weakest contrast 3.52:1.

### What the same check found in the light ramp, and did not fix

Adding contrast to the verifier immediately failed three *light* series against
a white card: amber 2.04:1, grey 1.61:1, azure 2.58:1, all below the 3:1 line
for a non-text mark. They have been that way since `T-R3`, because contrast
against a surface was never one of the checks that chose them.

It is reported as a warning and does not fail the build. Raising them changes
the palette every delivered PDF was rendered with, and re-tunes a ladder built
against greyscale and two deficiencies — a deliberate ticket, not something a
dark-mode change should smuggle in. The numbers print on every run so the debt
is visible rather than remembered.

## Known limits

**Determinism is asserted within a build, not across toolchains.** Same input,
same process, same bytes — which is what the PDF's own reproducibility rests on.
A freetype or `x/image` upgrade will change the pixels, and that is expected;
nothing pins bytes across dependency versions.

**The package costs ~90s under `go test -race`, against ~4s without.** It is
rasterising about forty supersampled charts, and `-race` instruments the
rasteriser at roughly 24×. The test fixtures were already cut to 90mm to hold it
down; what remains is the determinism check, which renders all seven types twice
and is the acceptance criterion, so it stays. For comparison,
`internal/report/pdf` costs ~55s in the same run. If CI time becomes the
constraint, the lever is the fixture size, not the coverage.

**Bars have no gap between categories at six categories.** The library's default
bar sizing fills the slot. It reads fine and it is not what the fixture set is
gated on, so it is recorded rather than tuned.

**A sparkline is not yet inside a KPI card.** The type renders and is sized for
one (16mm default, no axes, filled area), but `kpi_row` does not take a
`sparkline` field. That is a card-layout change, not a chart change.

**Charts are rendered once per document, not cached across documents.** The same
spec rendered into a PDF and a deck will run the renderer twice. It is
deterministic, so the two images are identical; `T-R4` can cache by spec hash if
the double render shows up in a profile.

## The light ramp's 3:1 debt, measured — and why it stays a warning (2026-09-03)

`make palette` has warned on three light-ramp series since `T-R3`: amber
`#EAAA3E` at **2.04:1**, grey `#CACCD1` at **1.61:1** and azure `#5CA8E0` at
**2.58:1** against white, where the floor for a non-text mark is 3:1. The
2026-08-17 dark-ramp work recorded the debt and did not price it. This is the
price.

**It cannot be fixed locally, and that is arithmetic rather than taste.** 3:1
against white caps a series at **L\* 61.7**. Eight series that must stay 5 L\*
apart for the greyscale floor need a span of **35 L\***. So every series has to
fit between L\* ~24 and L\* 61.7 — which the current ramp's darkest, navy at
24.2, clears by 2.5 points and nothing else does. Three series are above the cap
today (grey 82.0, amber 73.9, azure 66.3) and there is no room above brand red
at 59.3 to put them: the cap leaves one slot, and three need filling.

**So a compliant ramp exists and every one of the eight moves, brand red
included.** The consequences are not confined to a stylesheet: the light ramp is
the *document* ramp, so adopting it re-renders every chart in every PDF and deck
this product has delivered, and it compresses eight hues into a narrower
lightness band while protanopia separation is already at ΔE 12.7 against a floor
of 12 — the CVD margin would have to be re-won by hue where lightness used to
help.

**Decision, 2026-09-03 (repo owner): leave it as a recorded warning.** The debt
stays visible on every `make palette` run rather than being silently carried,
and the redesign is a brand decision with a delivered-document migration behind
it, not a lint fix. What this section adds is that the fix is now *sized*: it is
a whole-ramp redesign, not three hex edits, and nobody has to re-derive that to
find out.
