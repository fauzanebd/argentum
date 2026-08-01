# PDF Renderer v2 — `T-R2` Record

Shipped 2026-07-27. The document that leaves the building stopped looking
generated.

Before: a stock maroto PDF — a bold title, a bold row per heading, and a table
whose columns were the 12-unit grid divided evenly, whatever was in them. After:
a cover, a running header, `Page N of M`, numbered sections, KPI cards, typed
and locale-formatted cells, and columns measured against their own content.

```
internal/report/
   spec/     the document description both renderers read (v2, and v1 by construction)
   format/   numbers and dates, both directions, one set of conventions
   pdf/      the renderer
   theme/    T-R1: the tokens, the fonts, the page geometry
```

`internal/tools/document` keeps the tool's v1 types and the XLSX and CSV
renderers, plus the two conversions between them and the spec.
`render_pdf.go` no longer renders anything.

## What "v2" means

`spec_version: 2` in the tool call. Nothing else changes behaviour, and nothing
about it is required:

| | v1 (absent, or `1`) | v2 |
| --- | --- | --- |
| Cover page | — | when a `cover` section is present |
| Running header / footer | — | from page 2 |
| `Page N of M` | — | yes, in the document's locale |
| Numbered headings | — | when there is more than one top-level section |
| Cell formatting | as written | typed and formatted by the renderer |
| KPI cards, callouts, footnotes, page breaks | — | yes |

**v1 is not a shim at the JSON layer.** `spec.Column` and `spec.Cell` unmarshal
from either shape, so `"columns": ["Item", "Qty"]` and
`"columns": [{"label": "Item"}, {"label": "Qty", "fmt": "number"}]` land in the
same Go value. The version flag only decides what the *renderer* offers. A spec
that has been producing a plain document for three months keeps producing one.

## Gate

### Four fixtures + the v1 shape

`internal/report/pdf/testdata/`. They are JSON, not Go literals, because JSON is
what the model sends — a Go fixture would exercise the renderer without
exercising the two unmarshalers that make v1 compatibility work.

```
=== RUN   TestFixturesRender
monthly_sales.json:  3 pages,  90351 bytes, sha256 c5a36379937efaba400a0d68fabe2578a159be9e0c7648395cf2049e2bd45955
invoice.json:        1 pages,  60598 bytes, sha256 246fd2341db4abcb3fd2befb5113407136fd3e7dff08a31fde3e3fdaa6640687
kpi_summary.json:    3 pages,  74172 bytes, sha256 5f534036b65e8943facefb7ec781fa6e8dbe41980c994637547d8831e3bab97b
export_200.json:    17 pages, 625404 bytes, sha256 ea5b44748661dde1e8a4cdc17f368df27a23792efe0a1ed5198c649adc17cb77
v1_legacy.json:      1 pages,  42025 bytes, sha256 3c2afa7d89bda1bc9546641605f096f39b4ef14b865229a9cd154ac87ac62465
--- PASS: TestFixturesRender (0.09s)
```

### `pdfcpu validate`

```
=== RUN   TestFixturesValidate
    TestFixturesValidate/monthly_sales.json
    TestFixturesValidate/invoice.json
    TestFixturesValidate/kpi_summary.json
    TestFixturesValidate/export_200.json
    TestFixturesValidate/v1_legacy.json
--- PASS: TestFixturesValidate (0.06s)
```

Run in-process through `pdfcpu/pkg/api` rather than as a shell step, so it gates
CI without anyone installing the binary. **Relaxed mode, which is what the
`pdfcpu validate` command itself defaults to.** Strict mode rejects every PDF
gofpdf has ever produced with an embedded UTF-8 font — it requires `/FontFamily`
in the font descriptor, gofpdf writes none, and the spec marks the entry
optional for CID fonts. Not introduced by this ticket and not closable without
forking the writer.

### Byte-identical reruns

```
=== RUN   TestDeterministicBytes
monthly_sales.json:  90351 bytes, sha256 c5a3…5955 (identical across two runs)
invoice.json:        60598 bytes, sha256 246f…0687 (identical across two runs)
kpi_summary.json:    74172 bytes, sha256 5f53…b97b (identical across two runs)
export_200.json:    625404 bytes, sha256 ea5b…cb77 (identical across two runs)
v1_legacy.json:      skipped — no generated_at, so bytes are not reproducible by design
--- PASS: TestDeterministicBytes (0.08s)
```

Three things had to be true for this, and the third was found by CI after the
first two had been called done.

`generated_at` is a spec field, so the creation date in the trailer is the
spec's and not the clock's. `gofpdf.SetDefaultCatalogSort(true)` is set in the
`pdf` package's `init` — gofpdf writes its font catalogue in Go map order, so
the same spec rendered twice produced the same pages with the font objects
numbered differently.

**And `/ModDate` is pinned too.** gofpdf writes a modification date as well as a
creation date, from the wall clock, and maroto's config has no field for it. So
two renders of one spec were identical whenever they fell inside the same second
and differed whenever they straddled one. Six local runs said reproducible; the
first CI run said otherwise on two of four fixtures, and CI was right. The fix
is the same shape as the catalog-sort one — `SetDefaultModificationDate` is a
package-level default each new `Fpdf` copies at construction — so the global is
set and the document built under one lock, held for the length of a constructor.

The test no longer relies on the two renders happening to straddle a second: it
asserts both `/CreationDate` and `/ModDate` are literally the spec's
`generated_at`. Comparing two renders would have caught this only by luck, which
is exactly what it did.

`v1_legacy.json` is skipped by that test and should be: with no `generated_at`
it stamps `time.Now()`, and a v1 spec has no field to pin it with.

### 200 rows, header repeated, no orphans

```
=== RUN   TestTableHeaderRepeatsWithoutOrphans
200-row export: 17 pages, header repeated on 16 of them
--- PASS: TestTableHeaderRepeatsWithoutOrphans (0.01s)
```

16 of 17 because page 1 is the cover. The test asserts the header appears at
most once per page, and that every page carrying it also carries a data row.

### Column weighting

```
=== RUN   TestColumnWidthsAreWeighted
invoice column units: Description=54 Period=28 Qty=8 Unit price=15 Amount=15
--- PASS: TestColumnWidthsAreWeighted (0.00s)
```

Out of 120. The old renderer would have given all five 24 units each.

### Pages, seen

`ARGENTUM_PDF_OUT=/tmp/pdf go test ./internal/report/pdf/ -run WriteFixture`
writes the fixtures out; `pdftoppm -png -r 90` renders them. Nothing in a test
file can tell you a document is ugly.

## Layout decisions that are not obvious from the output

**The cover has no running header because of the order things happen in.**
maroto's `RegisterHeader` adds the header rows to whichever page is current, so
there is no "from page 2" option. The cover is drawn, the page is closed with an
empty `AddPages(page.New())`, and only then are the footer and header registered
— footer first, because `RegisterHeader`'s own fit check reads the footer height.

**Every measurement comes from a second gofpdf document that draws nothing.**
maroto's text provider is internal and only exists once `Generate()` is running,
by which time every layout decision has been made. `internal/report/measure`
constructs an `Fpdf` exactly the way maroto constructs its own — millimetres, A4,
the same embedded faces — so `GetStringWidth` returns the number the real
renderer will use. The line-breaking function is transcribed from maroto's
unexported one for the same reason: if this package counts two lines where maroto
draws three, the third is drawn on top of the next row.

> Moved out of this package by `T-R4` (2026-07-28), along with the column solver
> (`internal/report/layout`) and the renderer's own vocabulary
> (`internal/report/labels`). The deck renderer needs the same answers, and two
> measurers would eventually give two — proportioning the same table one way in
> the report and another way in the deck attached to it. `internal/report/pdf/measure.go`
> is now the maroto-shaped wrapper over them; this package's behaviour and its
> tests are unchanged. See [`report-deck.md`](report-deck.md).

**Paragraphs are emitted one line at a time.** maroto cannot split a row, so a
paragraph emitted as one row either fits on the page or moves to the next one
whole, leaving a hole. Emitting lines lets the break fall between them. The last
line is left-aligned: maroto justifies whatever it is given, and its guard
against over-stretched spaces only fires past ten times the normal space width,
so a justified three-word final line would be spread across the full measure.

**KPI cards are one row.** A maroto column can hold several components, each
positioned by its own `Top` offset, so a card is one bordered cell with three
baselines in it rather than three stacked rows pretending to be a card.

**Callout corners are square.** maroto draws rectangles and has no radius, so
the box earns its shape from a 2-unit coloured spine instead. `RadiusBase` stays
in the theme for `T-R4`, where OOXML does have a corner radius.

**Arrows are ↑ and ↓, not ▲ and ▼.** Space Grotesk has the arrows and not the
triangles; a missing glyph renders as nothing at all. Checked against the cmap
of all three faces before choosing.

## Findings

**The 200-row export earned its place in the fixture set.** It found four bugs
that none of the other three would have:

- **Unbreakable tokens overflowed their column.** maroto breaks lines at spaces
  and nowhere else, so `SO-2026-4100` in a 20mm column is one line 26mm wide,
  drawn straight over the column to its right. Line-count truncation never fired
  because the text was already one line. `fitText` now measures each wrapped
  line and cuts by characters when it has to.
- **A stride sampler measured the wrong rows.** Column widths were weighted from
  every k-th row to save work. The saving was nothing next to the per-cell
  wrapping that happens later anyway, and the stride landed on a phase of the
  data: it missed every `Marketplace` in the channel column, measured the column
  against `Reseller`, and clipped a quarter of its own rows. Every row is
  measured now.
- **The grid distribution was biased against wide columns.** It reserved the
  6-unit minimum for every column first and split the remaining 72 units in
  proportion to full widths, so a column asking for 15% of an eight-column table
  got 6 + 10.8 units instead of 18. Every wide column came out narrower than it
  asked for and every narrow one wider. The minimum is enforced afterwards now,
  by taking units from the widest column.
- **Rounding to integer units clipped cells by under a millimetre.** A column
  measured at 24.0mm was handed 23.2mm and truncated the value it had been
  measured from — `$2,400.00` became `$2,400.…`. Columns that cannot reflow now
  carry one unit of slack.

**The fixture's own random data was correlated.** The generator used a
hand-rolled LCG whose low bits cycle with period 4, and the channel column was
one of three draws per row — so the channel repeated on a fixed phase. It is
regenerated from a seeded `random.Random`. Worth recording because the bug it
masked (the stride sampler) is real and the fixture nearly hid it.

**`width_weight` skipped the column cap.** An explicit weight of 3.2 on a
five-column table claimed 111mm of a 174mm measure, the table went 15%
over-subscribed, everything scaled down proportionally, and the currency columns
truncated. An explicit weight now goes through the same 45% cap the measured
columns do.

**Two formatter bugs the golden set would not have caught.** Compact form
honoured the currency's decimal count, so rupiah's zero places rendered
3,863,405,700 as `Rp 4 Miliar`; compaction now takes its precision from the
magnitude. And `Percent` used one decimal place everywhere, so a damage rate of
0.42% printed as `0.4%` beside a paragraph about the nine basis points it had
moved.

**`Parse` could not read what the renderer writes.** `-Rp 1.234` and
`-$1,234.00` failed to parse: the minus sits in front of the currency symbol and
the parser only stripped symbols from the front of the string. Caught by a
round-trip test that formats every combination of locale, currency and compact
mode and reads it back. This is exactly the failure `internal/report/format`
exists to prevent — the eval comparator (`T-01`) and the renderer disagreeing
about what a number is — and it was present from the moment the formatting
direction was written.

## `T-R6` — three ways a document lost content quietly, 2026-08-01

A render audit put adversarial specs through `pdf.Render` rather than the
fixtures. All three findings passed the full suite before the audit, because no
fixture had a non-IDR currency column of large whole figures, a table past seven
columns, or a heading with no spaces in it.

**A currency column printed minor units the data never had.**
`pdf/table.go` computed `format.InferDecimals` for the column and then
discarded it for currency, substituting `AutoDecimals` — which `format.Currency`
turns into the currency's minor units. A column of nine-figure revenue read
`$486,000,000.00`. IDR hid it completely, since rupiah is already zero-decimal,
which is why it shipped and stayed.

The fix is `format.ColumnDecimals`, called by both renderers, and the rule it
encodes is materiality rather than roundness. The first attempt — "every value
is whole, so drop the cents" — was wrong in the other direction and
`invoice.json` caught it: those amounts are all round dollars, and an invoice
reading `$2,400` is a worse document than the one this set out to fix. Cents
come off only when every figure in the column is whole **and** every figure
clears `1e6`, reusing `Compact`'s own threshold rather than inventing a second
number. A currency's minor units cap the count unconditionally, so a fractional
rupiah average still prints `Rp 1.235`.

**A table that ran out of measure cut the figures and said nothing.** At eight
USD columns cells already read `$918,273.…`; at eleven the row labels went too.
A chart that drops series appends a sentence to its caption; a table that drops
digits appended nothing, and that is the worse of the two, because the number is
still there and still wrong. `truncateRow` now reports whether it cut a cell in
a **numeric** column, and the caption carries `labels.Set.CellsTruncated` when
it did. Text cells hitting the three-line cap deliberately do not raise it — that
is the renderer working as designed, it happens on perfectly readable tables,
and a notice that fires on ordinary tables stops being read.

**A heading with no spaces in it ran off the sheet.** `rowList.text` measured a
string's height and then handed the raw text to maroto, which breaks lines at
spaces and nowhere else — so a SKU, a URL or a concatenated key was drawn past
the right margin and lost to the paper, with no ellipsis and no way for a reader
to know. This is the same class as the `SO-2026-4100` finding above, on the one
path that never went through `fitText`. Four sites had it: the cover title, `h1`,
`h2`, and the running header, which did not measure at all. `clipToWidth` fixes
the first three at their shared helper and `headerRows` calls it directly.

Wrapping is untouched — a heading long enough to need two lines still gets two
lines. Clipping is only for what wrapping cannot do.

Tests: `format.TestColumnDecimals` (12 cases, invoice and revenue adjacent on
purpose), `pdf/disclosure_test.go` (6 tests). Ticket: `T-R6`.

## Known limits

**Eight columns of long text do not fit on A4 portrait, and the renderer does
not pretend otherwise.** The export fixture asks for 238mm of content in a 174mm
measure. Numbers, dates and unbreakable keys are served first and stay intact;
the two prose columns are squeezed to about 16mm each and truncate with an
ellipsis. That priority is deliberate — a truncated customer name is a
readability problem, a truncated order number is a wrong document — but the
result is still a cramped table, and the honest fix is fewer columns. The tool
description asks for under eight.

Since `T-R6` the document at least says so when the cells it cut were figures.
It still does not reflow, split or rotate the table; a landscape page and a
column-splitting continuation are both deliberately out of scope, because the
disclosure is what makes the current behaviour honest and the layout work is a
separate ticket.

**Table dates are abbreviated and nothing else is.** `1 Jan 2026` in a cell,
`27 Juli 2026` on a cover and in a footer. A date column written out in full
costs about 12mm of the measure, which in a wide table is taken from the column
beside it.

**~~`chart` sections are rejected, not dropped.~~ Closed 2026-07-28 by `T-R3`.**
A chart section now renders an image with its title and caption set in the
document's own type. A malformed chart is still rejected with an instruction
rather than dropped, for the reason this entry originally gave: a report whose
narrative refers to a figure that silently did not render is worse than an error
the model can act on. See [`report-charts.md`](report-charts.md).

**Branding is a struct with nothing in it.** `pdf.Options.Brand` carries a name,
a PNG logo, a confidentiality label and a footer note; the tool fills the name
and the currency from the company record and leaves the rest empty, which
renders the Argentum wordmark. `T-R5` fills it from tenant settings — the
renderer already treats every field as optional, so that ticket adds no
structural work here.

**`scope: output` guardrails still do not run.** Unchanged from `T-16`, recorded
against `T-07b`.
