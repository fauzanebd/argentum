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

Two things had to be true for this. `generated_at` is a spec field, so the
creation date in the trailer is the spec's and not the clock's. And
`gofpdf.SetDefaultCatalogSort(true)` is set in the `pdf` package's `init` —
gofpdf writes its font catalogue in Go map order, so the same spec rendered
twice produced the same pages with the font objects numbered differently. It is
a package-level global and there is no other way to reach it: maroto builds its
`Fpdf` internally.

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
by which time every layout decision has been made. `internal/report/pdf/measure.go`
constructs an `Fpdf` exactly the way maroto constructs its own — millimetres, A4,
the same embedded faces — so `GetStringWidth` returns the number the real
renderer will use. The line-breaking function is transcribed from maroto's
unexported one for the same reason: if this package counts two lines where maroto
draws three, the third is drawn on top of the next row.

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

## Known limits

**Eight columns of long text do not fit on A4 portrait, and the renderer does
not pretend otherwise.** The export fixture asks for 238mm of content in a 174mm
measure. Numbers, dates and unbreakable keys are served first and stay intact;
the two prose columns are squeezed to about 16mm each and truncate with an
ellipsis. That priority is deliberate — a truncated customer name is a
readability problem, a truncated order number is a wrong document — but the
result is still a cramped table, and the honest fix is fewer columns. The tool
description asks for under eight.

**Table dates are abbreviated and nothing else is.** `1 Jan 2026` in a cell,
`27 Juli 2026` on a cover and in a footer. A date column written out in full
costs about 12mm of the measure, which in a wide table is taken from the column
beside it.

**`chart` sections are rejected, not dropped.** `T-R3` owns them. A report whose
narrative refers to a figure that silently did not render is worse than an error
the model can act on, so `Validate` returns an instruction instead.

**Branding is a struct with nothing in it.** `pdf.Options.Brand` carries a name,
a PNG logo, a confidentiality label and a footer note; the tool fills the name
and the currency from the company record and leaves the rest empty, which
renders the Argentum wordmark. `T-R5` fills it from tenant settings — the
renderer already treats every field as optional, so that ticket adds no
structural work here.

**`scope: output` guardrails still do not run.** Unchanged from `T-16`, recorded
against `T-07b`.
