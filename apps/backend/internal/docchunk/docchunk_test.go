package docchunk

import (
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/docparse"
)

// sidecarPage is a page shaped the way `apps/docparse` actually emits one:
// the page's text, then its tables as GFM pipe tables, and no markdown heading
// anywhere. Every test that claims something about real parses builds from
// this, because the defect this file exists to pin was the gap between the
// fixture the package was written against and the bytes it is given.
func sidecarPage(number int, markdown string) docparse.Page {
	return docparse.Page{Number: number, Kind: docparse.KindText, Markdown: markdown}
}

const salesReportPage = `LAPORAN PENJUALAN Q4 2024
PT Sumber Rejeki

Angka di bawah ini adalah realisasi penjualan per kanal untuk kuartal keempat
tahun 2024, sebelum audit. Catatan: angka sementara.

| Kanal | Oktober | November | Desember |
| --- | --- | --- | --- |
| Retail | 1.200.000 | 1.350.000 | 1.410.000 |
| Grosir | 900.000 | 940.000 | 1.010.000 |
| Online | 1.277.718 | 1.418.552 | 1.443.405 |`

// TestSidecarOutputProducesOneHeadinglessSection is the regression test for the
// finding of 2026-08-19: the parser emits no `#`, so heading-boundary chunking
// never fired and every heading_path was empty. That is still the shipped
// default — this test pins it as *known*, which is the thing that was missing.
func TestSidecarOutputProducesOneHeadinglessSection(t *testing.T) {
	got := Build([]docparse.Page{sidecarPage(1, salesReportPage)}, Options{})

	if len(got) != 1 {
		t.Fatalf("chunks = %d, want 1 (one section, under budget)", len(got))
	}
	if got[0].HeadingPath != "" {
		t.Errorf("HeadingPath = %q, want empty: the sidecar emits no markdown headings", got[0].HeadingPath)
	}
	if !got[0].HasTable {
		t.Error("HasTable = false, want true: the chunk holds a pipe table")
	}
	if !strings.Contains(got[0].Content, "LAPORAN PENJUALAN Q4 2024") {
		t.Error("the title line is missing from the chunk")
	}
}

// TestMarkdownHeadingsStillCut covers the branch that exists for a parser that
// does emit headings — the hosted swap behind docparse.Parser.
func TestMarkdownHeadingsStillCut(t *testing.T) {
	page := sidecarPage(1, `# Perjanjian Kerja Sama

Pembukaan.

## Ketentuan Pembayaran

Pembayaran dilakukan dalam 30 hari.

## Ketentuan Pengiriman

Pengiriman dilakukan dalam 7 hari.`)

	got := Build([]docparse.Page{page}, Options{})
	if len(got) != 3 {
		t.Fatalf("chunks = %d, want 3", len(got))
	}
	want := []string{
		"Perjanjian Kerja Sama",
		"Perjanjian Kerja Sama › Ketentuan Pembayaran",
		"Perjanjian Kerja Sama › Ketentuan Pengiriman",
	}
	for i, w := range want {
		if got[i].HeadingPath != w {
			t.Errorf("chunk %d HeadingPath = %q, want %q", i, got[i].HeadingPath, w)
		}
	}
}

// TestHeadingTrailPopsDeeperLevels: a level-2 heading after a deeper one
// replaces it rather than nesting under it.
func TestHeadingTrailPopsDeeperLevels(t *testing.T) {
	page := sidecarPage(1, `# Bab 1

Isi bab.

### Sub sub

Isi sub sub.

## Bagian A

Isi bagian.`)

	got := Build([]docparse.Page{page}, Options{})
	if len(got) != 3 {
		t.Fatalf("chunks = %d, want 3", len(got))
	}
	if got[2].HeadingPath != "Bab 1 › Bagian A" {
		t.Errorf("HeadingPath = %q, want %q", got[2].HeadingPath, "Bab 1 › Bagian A")
	}
}

// TestOCRPagesAreChunked pins the defect found on 2026-08-19 while this file
// was being written: T-P3 sets kind to `ocr` and the markdown to what the model
// read, and Build accepted only `text` — so a scanned document was rendered,
// sent to a model, billed per page, and then held no retrievable prose at all.
func TestOCRPagesAreChunked(t *testing.T) {
	pages := []docparse.Page{
		{Number: 1, Kind: docparse.KindOCR, Markdown: "Surat Perjanjian\n\nDibaca oleh model."},
		{Number: 2, Kind: docparse.KindNeedsOCR, Markdown: ""},
		{Number: 3, Kind: docparse.KindFailed, Markdown: "ini tidak boleh terbaca"},
	}

	got := Build(pages, Options{})
	if len(got) != 1 {
		t.Fatalf("chunks = %d, want 1: the OCR page carries text and must be chunked", len(got))
	}
	if !strings.Contains(got[0].Content, "Dibaca oleh model.") {
		t.Errorf("the OCR page's text is missing: %q", got[0].Content)
	}
	if strings.Contains(got[0].Content, "ini tidak boleh terbaca") {
		t.Error("a `failed` page reached a chunk; only text and ocr pages hold readable text")
	}
	if got[0].PageFrom != 1 || got[0].PageTo != 1 {
		t.Errorf("pages = %d-%d, want 1-1", got[0].PageFrom, got[0].PageTo)
	}
}

// TestTableIsNeverSplit: a table over budget becomes one oversized chunk rather
// than two chunks each holding some of the rows.
func TestTableIsNeverSplit(t *testing.T) {
	var b strings.Builder
	b.WriteString("| Kode | Nama | Harga |\n| --- | --- | --- |\n")
	for i := 0; i < 200; i++ {
		b.WriteString("| SKU-000" + string(rune('0'+i%10)) + " | Nama produk yang cukup panjang | 125000 |\n")
	}
	page := sidecarPage(1, b.String())

	got := Build([]docparse.Page{page}, Options{MaxTokens: 50})
	if len(got) != 1 {
		t.Fatalf("chunks = %d, want 1: the table is one block and must not be cut", len(got))
	}
	if n := strings.Count(got[0].Content, "SKU-"); n != 200 {
		t.Errorf("rows in the chunk = %d, want 200", n)
	}
	if !got[0].HasTable {
		t.Error("HasTable = false on a chunk that is a table")
	}
}

// TestLongProseIsSplitBetweenParagraphs: the budget cuts, and it cuts on a
// paragraph boundary.
func TestLongProseIsSplitBetweenParagraphs(t *testing.T) {
	para := strings.Repeat("kata ", 60)
	page := sidecarPage(1, para+"\n\n"+para+"\n\n"+para)

	got := Build([]docparse.Page{page}, Options{MaxTokens: 100, Overlap: 0})
	if len(got) < 2 {
		t.Fatalf("chunks = %d, want at least 2 at a 100-token budget", len(got))
	}
	for i, c := range got {
		if strings.TrimSpace(c.Content) == "" {
			t.Errorf("chunk %d is empty", i)
		}
		if c.Ordinal != i {
			t.Errorf("chunk %d Ordinal = %d, want %d", i, c.Ordinal, i)
		}
	}
}

// TestOverlapRepeatsTheTail: a sentence that straddles a cut is retrievable
// from either side.
func TestOverlapRepeatsTheTail(t *testing.T) {
	first := strings.Repeat("alpha ", 80)
	second := strings.Repeat("beta ", 80)
	page := sidecarPage(1, first+"\n\n"+second)

	got := Build([]docparse.Page{page}, Options{MaxTokens: 100, Overlap: 20})
	if len(got) < 2 {
		t.Fatalf("chunks = %d, want at least 2", len(got))
	}
	if !strings.HasPrefix(got[1].Content, "alpha") {
		t.Errorf("chunk 1 does not open with the previous chunk's tail: %.40q", got[1].Content)
	}

	none := Build([]docparse.Page{page}, Options{MaxTokens: 100, Overlap: 0})
	if strings.HasPrefix(none[1].Content, "alpha") {
		t.Error("Overlap=0 still repeated the tail")
	}
}

// TestPageRangeStraddlesAPageBreak: a chunk that spans a break carries both
// numbers, which is what makes the citation right when the interesting sentence
// is on the second page.
func TestPageRangeStraddlesAPageBreak(t *testing.T) {
	pages := []docparse.Page{
		sidecarPage(4, "Kalimat pertama pada halaman empat."),
		sidecarPage(5, "Kalimat kedua pada halaman lima."),
	}

	got := Build(pages, Options{})
	if len(got) != 1 {
		t.Fatalf("chunks = %d, want 1: both pages are under budget with no heading between them", len(got))
	}
	if got[0].PageFrom != 4 || got[0].PageTo != 5 {
		t.Errorf("pages = %d-%d, want 4-5", got[0].PageFrom, got[0].PageTo)
	}
}

// TestDetectHeadingsIsOffByDefault: the same page, twice, differing only in the
// option. Off is the shipped behaviour and stays it until `make eval-docs` says
// what on does.
func TestDetectHeadingsIsOffByDefault(t *testing.T) {
	page := sidecarPage(1, `LAPORAN PENJUALAN Q4 2024

Angka realisasi.

KETENTUAN PEMBAYARAN

Dalam 30 hari.`)

	off := Build([]docparse.Page{page}, Options{})
	if len(off) != 1 {
		t.Fatalf("with detection off: chunks = %d, want 1", len(off))
	}
	if off[0].HeadingPath != "" {
		t.Errorf("with detection off: HeadingPath = %q, want empty", off[0].HeadingPath)
	}

	on := Build([]docparse.Page{page}, Options{DetectHeadings: true})
	if len(on) != 2 {
		t.Fatalf("with detection on: chunks = %d, want 2", len(on))
	}
	if on[0].HeadingPath != "LAPORAN PENJUALAN Q4 2024" {
		t.Errorf("chunk 0 HeadingPath = %q", on[0].HeadingPath)
	}
	if on[1].HeadingPath != "KETENTUAN PEMBAYARAN" {
		t.Errorf("chunk 1 HeadingPath = %q", on[1].HeadingPath)
	}
}

// TestDetectHeadingsLeavesTheTableAlone: the detector runs over a real parse,
// and the pipe table underneath it survives whole.
func TestDetectHeadingsLeavesTheTableAlone(t *testing.T) {
	got := Build([]docparse.Page{sidecarPage(1, salesReportPage)}, Options{DetectHeadings: true})

	var withTable int
	for _, c := range got {
		if c.HasTable {
			withTable++
			if n := strings.Count(c.Content, "| Retail"); n != 1 {
				t.Errorf("the table's first data row appears %d times in one chunk", n)
			}
			for _, row := range []string{"| Retail", "| Grosir", "| Online"} {
				if !strings.Contains(c.Content, row) {
					t.Errorf("row %q is not in the chunk holding the table", row)
				}
			}
		}
	}
	if withTable != 1 {
		t.Errorf("chunks holding the table = %d, want exactly 1", withTable)
	}
}

func TestLooksLikeHeading(t *testing.T) {
	cases := []struct {
		name string
		line string
		want bool
	}{
		{"all caps title", "LAPORAN PENJUALAN Q4 2024", true},
		{"title case", "Ketentuan Pembayaran", true},
		{"trailing colon", "Ketentuan Pembayaran:", true},
		{"numbered", "3. Ketentuan Pembayaran", true},
		{"multi-level numbered", "3.1) Denda Keterlambatan", true},

		{"a sentence", "Pembayaran dilakukan dalam waktu 30 hari sejak faktur.", false},
		{"a short sentence", "Angka sementara.", false},
		{"a question", "Berapa total penjualan?", false},
		{"a continuation line", "dan seterusnya sampai akhir kuartal", false},
		{"a table row", "| Retail | 1.200.000 | 1.350.000 |", false},
		{"a total sitting alone", "Rp 3.377.718.500", false},
		{"a bare figure with separators", "10.949.676.500", false},
		{"a page number", "12", false},
		{"a bare enumerator", "4.", false},
		{"blank", "   ", false},
		{"too long", strings.Repeat("Kata ", 12), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikeHeading(tc.line); got != tc.want {
				t.Errorf("looksLikeHeading(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}

// TestDetectHeadingsNeedsTheLineSetApart: the same words mid-paragraph are not
// a heading. This is the guard that keeps a paragraph from being cut in half.
//
// The opening line is deliberately long: the top of a page counts as set apart
// (a title is the first line of page one), so a short first line would be a
// heading candidate on its own and would not test what this test is for.
func TestDetectHeadingsNeedsTheLineSetApart(t *testing.T) {
	page := sidecarPage(1, `Pembukaan perjanjian ini menyebutkan beberapa hal yang harus dibaca lebih dahulu oleh kedua pihak
Ketentuan Pembayaran
sebagai bagian dari lampiran.`)

	got := Build([]docparse.Page{page}, Options{DetectHeadings: true})
	if len(got) != 1 {
		t.Fatalf("chunks = %d, want 1: no line here is set apart by a blank line", len(got))
	}
	if got[0].HeadingPath != "" {
		t.Errorf("HeadingPath = %q, want empty", got[0].HeadingPath)
	}
}

func TestEmptyInput(t *testing.T) {
	if got := Build(nil, Options{}); len(got) != 0 {
		t.Errorf("Build(nil) = %d chunks, want 0", len(got))
	}
	blank := []docparse.Page{sidecarPage(1, "   \n\n  ")}
	if got := Build(blank, Options{}); len(got) != 0 {
		t.Errorf("a whitespace-only page produced %d chunks, want 0", len(got))
	}
}

func TestOptionDefaults(t *testing.T) {
	// The zero value is 500 tokens and NO overlap: 60 is the fallback for an
	// unusable overlap, not the default for an unset one. The shipped 60 comes
	// from DOC_CHUNK_OVERLAP, so a caller that builds Options by hand and omits
	// Overlap gets chunks that do not overlap at all.
	got := Options{}.withDefaults()
	if got.MaxTokens != 500 || got.Overlap != 0 {
		t.Errorf("defaults = %+v, want MaxTokens 500 / Overlap 0", got)
	}
	// An overlap at or above the budget would repeat the whole chunk forever;
	// a negative one is meaningless. Both fall back.
	if fixed := (Options{MaxTokens: 40, Overlap: 40}).withDefaults(); fixed.Overlap != 60 {
		t.Errorf("Overlap >= MaxTokens = %d, want the 60 fallback", fixed.Overlap)
	}
	if fixed := (Options{MaxTokens: 500, Overlap: -1}).withDefaults(); fixed.Overlap != 60 {
		t.Errorf("Overlap < 0 = %d, want the 60 fallback", fixed.Overlap)
	}
}
