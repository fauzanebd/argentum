# PPTX deck renderer — `T-R4` Record

Shipped 2026-07-28. The same spec that renders as a branded PDF now also renders
as a PowerPoint deck, and the prose the model wrote lands in the speaker notes
rather than on the slide.

```
internal/report/pptx/
   render.go     Render, Options, the OPC package assembly, notesSlide
   deck.go       spec sections → slides: the layout mapping and the chunking
   slides.go     slide model → shapes: cover, divider, KPI, bullets, facts, closing
   table.go      column typing, formatting, measuring, paging
   tabledraw.go  the DrawingML <a:tbl>
   chart.go      the chart slide, rasterised at slide width
   drawing.go    DrawingML primitives, escaping, the text model
   parts.go      theme, masters, layout, docProps — generated from tokens.json
   ooxml.go      the OPC package: parts, relationships, content types, the zip
   geometry.go   16:9 geometry and the deck's type scale
```

## What "the same spec" means, exactly

The test fixtures are the PDF renderer's own, read from `../pdf/testdata` rather
than copied. Only `format` is changed. That is the acceptance criterion — "the
same spec renders as both PDF and PPTX with no format-specific authoring" —
expressed as a file path instead of as a promise: a copy in this package would
let the two drift the first time somebody tuned a deck by editing its fixture,
and the test would still pass while the claim stopped being true.

The mapping:

| Spec section | Slide |
| ------------ | ----- |
| `cover` | Title slide, dark ground: mark, period, title, subtitle, prepared-for/by, confidentiality |
| `heading` level 1 | Section divider, dark ground |
| `heading` level 2 | Titles the next content slide; no slide of its own |
| `kpi_row` | 2–4 stat tiles, rounded, with the delta arrow |
| `chart` | Chart slide, title + caption, image rasterised at slide width |
| `table` | Table slide; continues onto `(cont.)` slides past what fits |
| `paragraph` / `callout` | Bullet slide — lead sentence as the bullet, prose in the notes |
| `key_value` | Two-column fact list, values wrapping to three lines |
| `footnote` | Appended to the preceding slide's notes as a source line |
| end of deck | Closing slide with the confidentiality label |

`page_break` flushes what is buffered and draws nothing. An empty slide in the
middle of a deck is worse than a missing one.

## Three things that decided the design

**Nothing is inherited.** No placeholders, one blank layout, a master with no
shapes on it, and every table cell carrying its own fill and its own rules
rather than naming a table style. Placeholder and style inheritance is where the
four target applications disagree most — the same empty title placeholder is
drawn by PowerPoint, ignored by Google Slides and given a default outline by
some LibreOffice builds — so a slide is a list of absolutely positioned shapes
and there is nothing for a consumer to interpret differently.

**Fonts are named, never embedded.** OOXML font embedding works in PowerPoint on
Windows and nowhere else, and doubles the file. So every run names `Space
Grotesk` with `pitchFamily="34"` beside it — the low nibble is variable pitch,
the high nibble is the Swiss family — which is as close to a declared fallback
chain as the format gets: it tells a consumer what *kind* of face to substitute,
which is why a machine without Space Grotesk lands on Arial or Helvetica or
Liberation Sans and not on Times. Every text measurement is then taken against
94% of the real box (`substitutionMargin`), because the direction that matters
in a substitution is wider.

**There is no layout engine on the other side.** PowerPoint draws the boxes it
is given and silently clips whatever overflows them. So every block is measured
before it is placed, against the same metrics the PDF measures with
(`internal/report/measure`), and anything that does not fit continues onto a
slide that says so. `<a:normAutofit/>` is declared on top of that as the second
line of defence, not the first.

## What was extracted, and why it had to be

Three packages came out of `internal/report/pdf` so that both renderers read one
copy:

- **`internal/report/measure`** — the gofpdf-backed text measurer. Two
  measurers would have been two answers to "how wide is this column", and the
  first table proportioned one way in the report and another way in the deck
  attached to it is a discrepancy a reader notices and cannot explain.
- **`internal/report/layout`** — the column solver: `Allocate` (rigid columns
  paid first, flexible ones reflow into what is left) and `Distribute`
  (largest-remainder onto an integer grid). `Scale` was added for the deck,
  which measures columns in EMU and has no grid to round to.
- **`internal/report/labels`** — the five words a renderer contributes. The same
  document rendered both ways should not say "Prepared for" on one and
  "Disiapkan untuk" on the other.

The PDF renderer's public behaviour is unchanged; its own tests pass against the
extracted packages untouched, which is what makes the extraction reviewable.

## Determinism

Two renders of one spec are byte-identical. Entry order is fixed, relationship
ids are assigned by position, and every zip entry's timestamp is the document's
own `generated_at` rather than the clock.

This was free here and cost `T-R2` two rounds against gofpdf, for one reason:
nothing but this package writes the file. That is the return on hand-rolling the
OOXML, and it is worth stating because the cost — `parts.go` — is visible and
the return is not.

## Gate

`libreoffice --headless --convert-to pdf` on every fixture, LibreOffice 7.4.7.2,
run in a `debian:bookworm-slim` container so nothing was installed on the
development machine:

```
LibreOffice 7.4.7.2 40(Build:2)
== export_200.pptx
convert /work/export_200.pptx -> /work/export_200.pdf using filter : impress_pdf_Export
== invoice.pptx
convert /work/invoice.pptx -> /work/invoice.pdf using filter : impress_pdf_Export
== kpi_summary.pptx
convert /work/kpi_summary.pptx -> /work/kpi_summary.pdf using filter : impress_pdf_Export
== monthly_sales.pptx
convert /work/monthly_sales.pptx -> /work/monthly_sales.pdf using filter : impress_pdf_Export
== v1_legacy.pptx
convert /work/v1_legacy.pptx -> /work/v1_legacy.pdf using filter : impress_pdf_Export
```

What each fixture produces:

| Fixture | Slides | Notes pages | Images |
| ------- | -----: | ----------: | -----: |
| `monthly_sales` | 11 | 3 | 1 |
| `kpi_summary` | 13 | 4 | 0 |
| `invoice` | 6 | 1 | 0 |
| `export_200` | 53 | 2 | 0 |
| `v1_legacy` | 5 | 1 | 0 |

`go test ./internal/report/...` is green, including the PDF renderer's own suite
against the extracted packages.

The structural half of the gate runs in-process, because "it opens in
PowerPoint" is not something a unit test can assert. What it asserts instead is
the three things that make it open, which between them cause almost every
"PowerPoint found a problem with content" dialog a hand-built package produces:
every part is well-formed XML, every `r:id` resolves to a relationship that
resolves to a part that exists, and every XML part under `ppt/` has a
content-type override.

## What the conversion found

Three defects, all of them invisible to the tests and visible in the first
render:

- **The cover chained its offsets off estimated text heights.** A subtitle the
  estimate put on one line came back on two, and the brand rule was drawn
  straight through the second line. The fix is not a better estimate — the face
  is substituted, so being one line out is a certainty over enough documents —
  it is a fixed vertical grid whose bands do not move, with the text anchored to
  the edge the next band abuts.
- **A spec with no headings produced untitled slides.** That is the shape the
  model uses for "just give me the data", and it left a 22mm hole at the top of
  every slide and nowhere for the `(cont.)` marker to go: a reader looking at
  slide 9 of a paged table had no way to tell it continued slide 8. Slide titles
  now fall back to the document title.
- **An invoice's billing address was truncated to one line.** `key_value` is
  what an invoice header uses and an address is what it carries first, so fact
  values now wrap to three lines and rows are packed by measured height.

None of these would have been caught by asserting on the XML, and all three were
obvious in the rendered page. That is the argument for the conversion step being
in CI rather than in a checklist.

## Known limits

- **The eight-column export is cramped, and now long.** `export_200` renders
  with visible ellipses in its two prose columns — the same limit `T-R2`
  recorded for the PDF, at slide scale. It also becomes 53 slides, because rows
  are a uniform height and one wrapped cell makes every row two lines tall. A
  200-row export is a spreadsheet; the tool description says `xlsx` for raw
  tabular data, and that guidance is now load-bearing rather than advisory.
- **Row heights are uniform within a table.** Rows of two different heights read
  as a formatting error on a slide, where there is no page break to explain
  them. The cost is the slide count above.
- **Charts sit on white against the cream slide.** The chart image carries the
  plot background it was drawn with for the PDF, so on a deck it reads as a
  white card. It looks deliberate; it was not chosen.
- **Only LibreOffice is checked automatically.** PowerPoint, Keynote and Google
  Slides cannot be driven from a headless runner. LibreOffice is the strictest
  of the four about malformed OOXML, which is why it is the one in CI, but it is
  a proxy — see below.
- **The tenant logo is not drawn on the deck.** A logo on the dark cover needs a
  light-on-dark variant, which is a question for `T-R5`.

## Not yet verified

The ticket's gate asks for screenshots from PowerPoint, Keynote, Google Slides
and LibreOffice. **Only LibreOffice has been run** — the other three are desktop
and browser applications that cannot be driven from this environment. The
renderer is built against what makes those three differ (no inherited
placeholders, no table styles, explicit fills, named fonts with a substitution
class), and the LibreOffice conversion is the strictest automated check
available, but the three-application check itself is outstanding and is the one
remaining acceptance item.

`ARGENTUM_DECK_OUT=/tmp/decks go test ./internal/report/pptx -run TestWriteDecks`
writes the five fixture decks for that check.
