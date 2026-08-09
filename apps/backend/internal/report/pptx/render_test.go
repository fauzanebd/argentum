package pptx

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/report/canvas"
	"github.com/fauzanebd/argentum/internal/report/measure"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// The fixtures are the PDF renderer's own, read from its testdata directory
// rather than copied into this one.
//
// That is the acceptance criterion, not a shortcut: "the same spec renders as
// both PDF and PPTX with no format-specific authoring". A copy here would let
// the two drift the first time somebody tuned a deck by editing its fixture,
// and the test would still pass while the claim stopped being true. The only
// field this test changes is `format`.
var fixtures = []string{
	"monthly_sales.json",
	"invoice.json",
	"kpi_summary.json",
	"export_200.json",
	"v1_legacy.json",
}

func fixturePath(name string) string { return filepath.Join("..", "pdf", "testdata", name) }

func loadFixture(t *testing.T, name string) *spec.Document {
	t.Helper()
	raw, err := os.ReadFile(fixturePath(name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var doc spec.Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal %s: %v", name, err)
	}
	doc.Format = "pptx"
	doc.Normalize()
	if err := doc.Validate(); err != nil {
		t.Fatalf("validate %s: %v", name, err)
	}
	return &doc
}

// fixtureNow pins the clock for every fixture render.
//
// Four of the five fixtures carry their own `generated_at`; `v1_legacy.json`
// does not, and spec.Document.Generated falls back to time.Now() for a spec
// without one. Rendering it through a bare Options{} therefore stamped the zip
// entries and the core properties with the wall clock, and
// TestDeterministicBytes — which renders twice and compares — passed or failed
// depending on whether the two renders straddled a second. It did straddle one
// under `-race`, where the pair takes tens of seconds.
//
// This is the same trap T-R2 recorded for the PDF and it is worth restating,
// because the PPTX renderer's determinism is real: comparing two renders
// catches a clock only if the pair happens to straddle a tick, so the clock has
// to be pinned rather than raced. TestNowPinsAnUnstampedSpec covers Options.Now
// on its own.
var fixtureNow = time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC)

func renderFixture(t *testing.T, name string) []byte {
	t.Helper()
	out, err := Render(loadFixture(t, name), Options{Now: fixtureNow})
	if err != nil {
		t.Fatalf("render %s: %v", name, err)
	}
	return out
}

// deck is a rendered package, opened for inspection.
type deck struct {
	names []string
	parts map[string][]byte
}

func openDeck(t *testing.T, data []byte) *deck {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open package: %v", err)
	}
	d := &deck{parts: map[string][]byte{}}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		d.names = append(d.names, f.Name)
		d.parts[f.Name] = body
	}
	return d
}

func (d *deck) has(name string) bool { _, ok := d.parts[name]; return ok }

func (d *deck) xml(name string) string { return string(d.parts[name]) }

func (d *deck) slideNames() []string {
	var out []string
	for _, n := range d.names {
		if strings.HasPrefix(n, "ppt/slides/slide") && strings.HasSuffix(n, ".xml") {
			out = append(out, n)
		}
	}
	return out
}

// TestFixturesRenderAsDecks is the structural gate.
//
// "It opens in PowerPoint" is not something a unit test can assert, so this
// asserts the things that make it open: every part is well-formed XML, every
// relationship resolves to a part that exists, and every XML part under ppt/
// has a content-type override. Those three are, between them, the cause of
// almost every "PowerPoint found a problem with content" dialog a hand-built
// package produces.
func TestFixturesRenderAsDecks(t *testing.T) {
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			out := renderFixture(t, name)
			d := openDeck(t, out)

			if d.names[0] != "[Content_Types].xml" {
				t.Errorf("first zip entry is %q, want [Content_Types].xml", d.names[0])
			}
			for _, required := range []string{
				"_rels/.rels",
				"ppt/presentation.xml",
				"ppt/_rels/presentation.xml.rels",
				"ppt/slideMasters/slideMaster1.xml",
				"ppt/slideLayouts/slideLayout1.xml",
				"ppt/notesMasters/notesMaster1.xml",
				"ppt/theme/theme1.xml",
				"docProps/core.xml",
				"docProps/app.xml",
			} {
				if !d.has(required) {
					t.Errorf("package is missing %s", required)
				}
			}

			assertWellFormed(t, d)
			assertRelationshipsResolve(t, d)
			assertContentTypesCover(t, d)
			assertSlidesAreListed(t, d)

			slides := d.slideNames()
			if len(slides) < 2 {
				t.Errorf("deck has %d slides", len(slides))
			}
			t.Logf("%s: %d slides, %d parts, %d bytes, sha256 %s",
				name, len(slides), len(d.names), len(out), sum(out))
		})
	}
}

func assertWellFormed(t *testing.T, d *deck) {
	t.Helper()
	for _, name := range d.names {
		if !strings.HasSuffix(name, ".xml") && !strings.HasSuffix(name, ".rels") {
			continue
		}
		dec := xml.NewDecoder(bytes.NewReader(d.parts[name]))
		for {
			_, err := dec.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Errorf("%s is not well-formed XML: %v", name, err)
				break
			}
		}
	}
}

var relRefPattern = regexp.MustCompile(`r:(?:id|embed)="(rId\d+)"`)

// assertRelationshipsResolve checks both directions: every r:id a part uses is
// declared in that part's own rels file, and every target those rels name is a
// part that exists in the package. A dangling r:embed is how a chart becomes a
// grey box; a dangling slide relationship is how the whole deck refuses to
// open.
func assertRelationshipsResolve(t *testing.T, d *deck) {
	t.Helper()
	for _, name := range d.names {
		if strings.HasSuffix(name, ".rels") || !strings.HasSuffix(name, ".xml") {
			continue
		}
		rels := parseRels(t, d, name)
		for _, m := range relRefPattern.FindAllStringSubmatch(d.xml(name), -1) {
			if _, ok := rels[m[1]]; !ok {
				t.Errorf("%s references %s, which its rels part does not declare", name, m[1])
			}
		}
	}
}

// parseRels reads the rels part belonging to name and returns id → target,
// resolved against the package root.
func parseRels(t *testing.T, d *deck, name string) map[string]string {
	t.Helper()
	dir, base := filepath.Split(name)
	relsName := dir + "_rels/" + base + ".rels"
	out := map[string]string{}
	body, ok := d.parts[relsName]
	if !ok {
		return out
	}

	var doc struct {
		Rels []struct {
			ID     string `xml:"Id,attr"`
			Type   string `xml:"Type,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	if err := xml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse %s: %v", relsName, err)
	}
	for _, r := range doc.Rels {
		if strings.HasPrefix(r.Target, "http") {
			out[r.ID] = r.Target
			continue
		}
		resolved := filepath.ToSlash(filepath.Clean(filepath.Join(dir, r.Target)))
		if !d.has(resolved) {
			t.Errorf("%s declares %s → %s, which is not in the package", relsName, r.ID, resolved)
		}
		out[r.ID] = resolved
	}
	return out
}

// assertContentTypesCover catches the single most common way a hand-built
// package fails: an XML part with no content type, which every consumer treats
// as "this file is corrupt" rather than as "this file has an unknown type".
func assertContentTypesCover(t *testing.T, d *deck) {
	t.Helper()
	types := d.xml("[Content_Types].xml")
	for _, name := range d.names {
		switch {
		case name == "[Content_Types].xml", strings.HasSuffix(name, ".rels"):
			continue
		case strings.HasSuffix(name, ".png"):
			if !strings.Contains(types, `Extension="png"`) {
				t.Errorf("%s is in the package but png has no default content type", name)
			}
			continue
		case !strings.HasSuffix(name, ".xml"):
			continue
		}
		if !strings.Contains(types, `PartName="/`+name+`"`) {
			t.Errorf("%s has no content-type override", name)
		}
	}
}

// assertSlidesAreListed checks that every slide part is referenced from
// presentation.xml's slide list. A slide that exists in the package but is not
// listed is a slide nobody ever sees, and no consumer reports it.
func assertSlidesAreListed(t *testing.T, d *deck) {
	t.Helper()
	rels := parseRels(t, d, "ppt/presentation.xml")
	listed := map[string]bool{}
	for _, m := range regexp.MustCompile(`<p:sldId id="\d+" r:id="(rId\d+)"/>`).
		FindAllStringSubmatch(d.xml("ppt/presentation.xml"), -1) {
		target, ok := rels[m[1]]
		if !ok {
			t.Errorf("presentation.xml lists %s, which its rels part does not declare", m[1])
			continue
		}
		listed[target] = true
	}
	for _, name := range d.slideNames() {
		if !listed[name] {
			t.Errorf("%s is in the package but not in the slide list", name)
		}
	}
	if len(listed) != len(d.slideNames()) {
		t.Errorf("slide list has %d entries against %d slide parts", len(listed), len(d.slideNames()))
	}
}

// TestDeterministicBytes is the acceptance item "zip is deterministic (fixed
// entry order, fixed timestamps)".
//
// It is also what makes every other assertion in this file worth making: a
// renderer whose output moves between runs cannot be regression-tested at all.
// The PDF had to fight gofpdf for this property twice (see T-R2); here it is
// free, because nothing but this package writes the file.
func TestDeterministicBytes(t *testing.T) {
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			first := renderFixture(t, name)
			second := renderFixture(t, name)
			if !bytes.Equal(first, second) {
				t.Fatalf("two renders differ: %s vs %s", sum(first), sum(second))
			}
		})
	}
}

// A spec with no generated_at has no fixed timestamp of its own, so Options.Now
// is what pins it. Without that the zip entries and the core properties would
// carry the wall clock and the bytes would move every second.
func TestNowPinsAnUnstampedSpec(t *testing.T) {
	doc := loadFixture(t, "monthly_sales.json")
	doc.GeneratedAt = ""

	fixed := mustTime(t, "2026-07-28T08:00:00Z")
	a, err := Render(doc, Options{Now: fixed})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	b, err := Render(doc, Options{Now: fixed})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("two renders of an unstamped spec differ despite a pinned Now")
	}
	if !strings.Contains(openDeck(t, a).xml("docProps/core.xml"), "2026-07-28T08:00:00Z") {
		t.Error("core properties do not carry the pinned timestamp")
	}
}

// TestNarrativeGoesToSpeakerNotes is the acceptance item "speaker notes carry
// the narrative", and it is the ticket's own claim about what makes a generated
// deck feel authored.
//
// It asserts both halves: the paragraph is in the notes *whole*, and the slide
// itself carries only its lead. A deck that puts the whole paragraph on the
// slide passes the first half and fails the product.
func TestNarrativeGoesToSpeakerNotes(t *testing.T) {
	doc := loadFixture(t, "monthly_sales.json")
	out, err := Render(doc, Options{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	d := openDeck(t, out)

	// The closing clause of the fixture's opening paragraph — present in the
	// prose, and far past where a bullet would stop.
	const tail = "bukan dari kenaikan belanja pelanggan lama"

	notes := strings.Join(partsWithPrefix(d, "ppt/notesSlides/"), "\n")
	if notes == "" {
		t.Fatal("the deck has no speaker notes at all")
	}
	if !strings.Contains(notes, tail) {
		t.Error("the paragraph's closing clause is not in the speaker notes")
	}
	slides := strings.Join(partsWithPrefix(d, "ppt/slides/"), "\n")
	if strings.Contains(slides, tail) {
		t.Error("the whole paragraph was put on a slide as well as in the notes")
	}

	// The lead sentence is what survives onto the slide.
	const lead = "Pendapatan tahun berjalan mencapai"
	if !strings.Contains(slides, lead) {
		t.Error("the paragraph's lead sentence is not on any slide")
	}
}

func partsWithPrefix(d *deck, prefix string) []string {
	var out []string
	for _, n := range d.names {
		if strings.HasPrefix(n, prefix) && strings.HasSuffix(n, ".xml") {
			out = append(out, d.xml(n))
		}
	}
	return out
}

// TestLongTableContinues is the acceptance item "long tables continue across
// slides; nothing is silently clipped".
//
// The 200-row export is the fixture that found four separate layout bugs in the
// PDF renderer (T-R2), which is why it is the one used here.
func TestLongTableContinues(t *testing.T) {
	doc := loadFixture(t, "export_200.json")
	r, err := newRenderer(doc, Options{})
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	if err := r.buildSlides(); err != nil {
		t.Fatalf("build: %v", err)
	}

	var (
		tables    int
		continued int
		rows      int
		totals    int
		lastTable = -1
	)
	for i, s := range r.slides {
		if s.kind != kindTable {
			continue
		}
		tables++
		lastTable = i
		rows += len(s.table.Rows)
		if s.continued {
			continued++
		}
		if len(s.table.Total) > 0 {
			totals++
		}
		if n := len(s.table.Rows); n > canvas.MaxRowsPerSurface {
			t.Errorf("slide %d carries %d rows, over the %d cap", i+1, n, canvas.MaxRowsPerSurface)
		}
	}

	if tables < 10 {
		t.Fatalf("a 200-row table produced %d table slides", tables)
	}
	if continued != tables-1 {
		t.Errorf("%d of %d table slides are marked as continuations, want %d", continued, tables, tables-1)
	}
	if rows != 200 {
		t.Errorf("the slides carry %d of the table's 200 rows — the rest were dropped", rows)
	}
	if totals > 1 {
		t.Errorf("the total row appears on %d slides; it belongs on the last one only", totals)
	}
	if totals == 1 && len(r.slides[lastTable].table.Total) == 0 {
		t.Error("the total row is not on the last table slide")
	}

	// The continuation marker has to be visible, because the alternative to
	// saying so is a reader assuming slide 8 is a different table.
	//
	// It is asserted through r.words rather than against the literal "(cont.)":
	// this fixture is Indonesian, so the marker is "(lanjutan)", and a test
	// pinned to the English string would have been asserting that the labels
	// package does not work.
	out, err := Render(doc, Options{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if r.words.Continued != "(lanjutan)" {
		t.Fatalf("the Indonesian fixture resolved its continuation marker to %q", r.words.Continued)
	}
	if !strings.Contains(strings.Join(partsWithPrefix(openDeck(t, out), "ppt/slides/"), "\n"), r.words.Continued) {
		t.Errorf("no slide carries the continuation marker %q", r.words.Continued)
	}
}

// TestNothingOverflowsItsBox is the other half of "nothing is silently
// clipped". PowerPoint will not tell us when a line runs past its frame, so
// every string the renderer places is checked against the box it was placed in,
// using the same metrics the renderer used to decide.
func TestNothingOverflowsItsBox(t *testing.T) {
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			doc := loadFixture(t, name)
			r, err := newRenderer(doc, Options{})
			if err != nil {
				t.Fatalf("renderer: %v", err)
			}
			if err := r.buildSlides(); err != nil {
				t.Fatalf("build: %v", err)
			}

			for i, s := range r.slides {
				where := fmt.Sprintf("slide %d (%v)", i+1, s.kind)

				if n := linesIn(r.titleText(s), theme.FontBody, measure.Bold, deckType.H1, contentWidth()); n > 2 {
					t.Errorf("%s: the title wraps to %d lines: %q", where, n, s.title)
				}
				for _, b := range s.bullets {
					if n := linesIn(b.text, theme.FontBody, measure.Regular, deckType.Body, bulletWidth()); n > maxBulletLines {
						t.Errorf("%s: a bullet wraps to %d lines: %q", where, n, b.text)
					}
				}
				if s.table != nil {
					assertTableFits(t, where, s.table)
				}
				if s.chart != nil && s.chart.heightMM > bodyHeight() {
					t.Errorf("%s: the chart is %vmm tall in a %vmm body area", where, s.chart.heightMM, bodyHeight())
				}
			}
		})
	}
}

func assertTableFits(t *testing.T, where string, m *tableModel) {
	t.Helper()

	sum := 0.0
	for _, w := range m.Widths {
		sum += w
	}
	if diff := sum - contentWidth(); diff > 0.01 || diff < -0.01 {
		t.Errorf("%s: columns sum to %vmm against a %vmm measure", where, sum, contentWidth())
	}

	height := m.HeaderH + float64(len(m.Rows))*m.RowH
	if len(m.Total) > 0 {
		height += m.RowH
	}
	if height > bodyHeight()+0.01 {
		t.Errorf("%s: the table is %vmm tall in a %vmm body area", where, height, bodyHeight())
	}

	rows := append([][]string{m.Header}, m.Rows...)
	if m.Total != nil {
		rows = append(rows, m.Total)
	}
	for r, row := range rows {
		for c, cell := range row {
			if c >= len(m.Widths) {
				t.Errorf("%s: row %d has %d cells against %d columns", where, r, len(row), len(m.Widths))
				break
			}
			n := linesIn(cell, theme.FontBody, measure.Regular, m.Size, m.Widths[c]-2*cellPadX)
			if n > maxCellLines {
				t.Errorf("%s: cell [%d][%d] wraps to %d lines in a %vmm column: %q",
					where, r, c, n, m.Widths[c], cell)
			}
		}
	}
}

// TestChartsAreEmbedded is the acceptance item "charts appear at slide
// resolution without visible artefacts". The resolution half is checkable; the
// artefacts half is what the LibreOffice conversion and the four-application
// check are for.
func TestChartsAreEmbedded(t *testing.T) {
	doc := loadFixture(t, "monthly_sales.json")
	out, err := Render(doc, Options{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	d := openDeck(t, out)

	media := 0
	for _, n := range d.names {
		if strings.HasPrefix(n, "ppt/media/") {
			media++
			if !bytes.HasPrefix(d.parts[n], []byte("\x89PNG")) {
				t.Errorf("%s is not a PNG", n)
			}
		}
	}
	if media != 1 {
		t.Fatalf("the fixture has one chart; the package has %d media parts", media)
	}

	// 200 DPI over the drawn width. Below about 150 the bars alias visibly on a
	// projector, which is the whole reason the chart is re-rasterised at slide
	// size instead of being reused from the PDF at A4 size.
	r, err := newRenderer(doc, Options{})
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	if err := r.buildSlides(); err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, s := range r.slides {
		if s.chart == nil {
			continue
		}
		if s.chart.widthMM < contentWidth()*0.9 {
			t.Errorf("the chart is %vmm wide on a %vmm measure", s.chart.widthMM, contentWidth())
		}
	}
	if !strings.Contains(strings.Join(partsWithPrefix(d, "ppt/slides/"), "\n"), "<a:blip r:embed=") {
		t.Error("no slide embeds the image")
	}
}

// A v1 spec has no cover, no chart and no typed cells. It still has to produce
// a deck, for the same reason it still produces a PDF: a spec that has been
// rendering for three months does not stop working because a new format was
// added beside it.
func TestV1SpecRenders(t *testing.T) {
	doc := loadFixture(t, "v1_legacy.json")
	if doc.V2() {
		t.Fatal("the legacy fixture is not v1")
	}
	out, err := Render(doc, Options{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(openDeck(t, out).slideNames()) == 0 {
		t.Error("a v1 spec produced no slides")
	}
}

// Text arriving from a model can carry anything. A control byte inside a
// paragraph produces a package that opens nowhere, and the error a reader sees
// names a missing part rather than the sentence that caused it.
func TestControlCharactersDoNotBreakThePackage(t *testing.T) {
	doc := &spec.Document{
		SpecVersion: 2,
		Format:      "pptx",
		Title:       "Quarterly \x00review\x07",
		GeneratedAt: "2026-07-28T08:00:00Z",
		Content: spec.Content{Sections: []spec.Section{
			{Type: spec.SectionHeading, Level: 1, Text: "Findings <&> \"quoted\""},
			{Type: spec.SectionParagraph, Text: "Revenue rose 12%  against a plan of 9%. Margin held."},
		}},
	}
	out, err := Render(doc, Options{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	d := openDeck(t, out)
	assertWellFormed(t, d)

	slides := strings.Join(partsWithPrefix(d, "ppt/slides/"), "\n")
	if strings.Contains(slides, "\x00") || strings.Contains(slides, "\x07") {
		t.Error("a control character survived into the XML")
	}
	if !strings.Contains(slides, "&lt;&amp;&gt;") {
		t.Error("markup characters were not escaped")
	}
}

// TestLibreOfficeConverts is the ticket's CI smoke test: a deck LibreOffice
// refuses is a deck PowerPoint may also refuse, and LibreOffice is the
// strictest of the four target applications about malformed OOXML.
//
// It skips when soffice is not installed so `go test ./...` stays runnable on a
// laptop; CI installs it, which is where this actually gates.
func TestLibreOfficeConverts(t *testing.T) {
	soffice := lookLibreOffice()
	if soffice == "" {
		t.Skip("libreoffice not installed; the CI job runs this")
	}
	outDir := t.TempDir()

	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			deckPath := filepath.Join(outDir, strings.TrimSuffix(name, ".json")+".pptx")
			if err := os.WriteFile(deckPath, renderFixture(t, name), 0o600); err != nil {
				t.Fatalf("write deck: %v", err)
			}

			// -env:UserInstallation gives each run its own profile: without it
			// two concurrent conversions share one and the second exits 0
			// having converted nothing.
			cmd := exec.Command(soffice,
				"-env:UserInstallation=file://"+filepath.Join(outDir, "profile-"+name),
				"--headless", "--convert-to", "pdf", "--outdir", outDir, deckPath)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("libreoffice refused the deck: %v\n%s", err, out)
			}

			pdfPath := strings.TrimSuffix(deckPath, ".pptx") + ".pdf"
			info, err := os.Stat(pdfPath)
			if err != nil {
				t.Fatalf("no PDF was produced: %v\n%s", err, out)
			}
			if info.Size() < 1024 {
				t.Fatalf("the converted PDF is %d bytes — the deck rendered as nothing", info.Size())
			}
			t.Logf("%s → %d byte PDF", name, info.Size())
		})
	}
}

// TestWriteDecks is not a test; it is how the decks for the ticket's gate get
// produced. `ARGENTUM_DECK_OUT=/tmp/decks go test ./internal/report/pptx -run
// TestWriteDecks` writes one .pptx per fixture, which is what gets opened in
// PowerPoint, Keynote, Google Slides and LibreOffice.
//
// It lives here rather than in a cmd/ because the renderer is internal and a
// throwaway binary to reach it is a binary somebody has to maintain.
func TestWriteDecks(t *testing.T) {
	dir := os.Getenv("ARGENTUM_DECK_OUT")
	if dir == "" {
		t.Skip("set ARGENTUM_DECK_OUT to write the fixture decks to a directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range fixtures {
		out := renderFixture(t, name)
		path := filepath.Join(dir, strings.TrimSuffix(name, ".json")+".pptx")
		if err := os.WriteFile(path, out, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		t.Logf("wrote %s (%d bytes)", path, len(out))
	}
}

func lookLibreOffice() string {
	for _, candidate := range []string{
		"soffice",
		"libreoffice",
		"/Applications/LibreOffice.app/Contents/MacOS/soffice",
	} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return ts
}
