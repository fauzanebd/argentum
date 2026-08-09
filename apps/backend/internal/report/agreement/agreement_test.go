// Package agreement holds T-V5's three-format agreement gate.
//
// It is a package of its own because it is the only place that imports all
// three renderers at once, and because what it asserts is a property of the
// set rather than of any one of them: **a figure reads the same in the PDF, in
// the deck and in the video.**
//
// That is locked decision 2 stated as a test. The decision says formatting
// happens in Go, once, and every renderer draws the strings it is handed —
// `T-R2` moved formatting out of the model, `T-R4` extracted `measure`,
// `layout` and `labels` so the two document renderers could not disagree about
// a column width or the Indonesian for "Prepared for", and `T-V1` projected
// the same spec onto a plan whose every string is final. None of that is
// enforced by anything. A React component that called `toLocaleString` on a
// number would produce a video whose figures disagree with the PDF attached to
// the same email, and no test in the tree would notice.
//
// **What each format contributes, and the one asymmetry.** The PDF's strings
// come from maroto's component tree via `pdf.Texts`; the deck's come out of
// the `.pptx` itself, unzipped and read from the OOXML text runs; the video's
// come from the plan, which is the video's text in final form — `videoplan`
// wraps every line and formats every figure in Go precisely so the browser has
// nothing left to decide. The asymmetry is that the first two are read back
// from what was produced and the third is read from what will be drawn. The
// half that closes it is in `packages/motion`, where a test renders the
// components and asserts every plan string survives verbatim; together they
// span spec → plan → pixels. Neither half is sufficient alone, and this
// comment is where that is written down.
package agreement

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/report/pdf"
	"github.com/fauzanebd/argentum/internal/report/pptx"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/report/videoplan"
)

// The fixture set is the same one `T-R2`, `T-R4` and `T-V1` are gated on, less
// the two that cannot be all three formats: `invoice` is a record, which the
// video door refuses, and `v1_legacy` predates the whole spec version.
var fixtures = []string{"monthly_sales", "kpi_summary", "export_200"}

// fixedNow keeps "Generated …" identical across the three renders. Without it
// the three formats disagree on a date string for a reason that is about the
// clock rather than about formatting.
var fixedNow = time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)

func loadFixture(t *testing.T, name string) *spec.Document {
	t.Helper()
	// The fixtures live with the PDF renderer, which is where they were
	// written. Copying them here would be a second set to keep in step.
	path := filepath.Join("..", "pdf", "testdata", name+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	doc := &spec.Document{}
	if err := json.Unmarshal(raw, doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	doc.Normalize()
	if err := doc.Validate(); err != nil {
		t.Fatalf("%s is not a valid spec: %v", name, err)
	}
	return doc
}

// figure matches a string that **is** a quantity, rather than one that
// contains digits: an optional currency word, an optional sign, a grouped
// number, an optional percent or unit suffix.
//
// Whole-string and not a substring search, and the first run of this gate is
// why. A substring pattern pulled `-42` out of the order id `SO-2026-42…` in
// the video's table and reported it as a figure the PDF was missing. Two
// things were wrong with that and neither was a formatting bug: an order id is
// not a figure, and the `…` is the video truncating a cell its narrower column
// cannot fit — a layout decision each format makes for itself and is supposed
// to make differently. What this gate is about is the *characters of a number*,
// and a cell is the unit that carries one.
var figure = regexp.MustCompile(`^(?:[A-Z][a-z]?\p{L}*\s*)?-?\d[\d.,]*\s*%?$`)

// figuresIn returns the distinct figure-shaped strings in a set of strings.
func figuresIn(all []string) map[string]bool {
	out := map[string]bool{}
	for _, s := range all {
		s = strings.TrimSpace(s)
		// A leading direction glyph is chrome, not part of the number. The PDF
		// draws a delta as `↓ -14.0%`; the plan carries `-14.0%` and a
		// `Rising` boolean, and the video draws its own arrow from it. The
		// second run of this gate reported all three deltas as disagreements
		// on that basis alone — which is the gate being too literal about
		// where a format's own decoration ends and the figure begins.
		s = strings.TrimSpace(strings.TrimLeft(s, "↑↓▲▼"))
		if !figure.MatchString(s) {
			continue
		}
		// A bare one- or two-digit number is a page number, a row index, a
		// section number — chrome each renderer decides for itself, and `T-R4`
		// established that the deck numbers nothing while the PDF numbers its
		// headings. Those are meant to differ.
		if len(strings.Trim(s, "%")) < 3 {
			continue
		}
		out[s] = true
	}
	return out
}

func pdfStrings(t *testing.T, doc *spec.Document) []string {
	t.Helper()
	pages, err := pdf.Texts(doc, pdf.Options{Now: fixedNow})
	if err != nil {
		t.Fatalf("pdf.Texts: %v", err)
	}
	var all []string
	for _, p := range pages {
		all = append(all, p...)
	}
	return all
}

// pptxStrings reads the deck back out of the file, rather than out of the
// renderer. It is the one format where that is cheap — a `.pptx` is a zip of
// XML — and it is worth doing on the produced bytes: it proves the text
// survived the OOXML writer, which is hand-rolled here.
func pptxStrings(t *testing.T, doc *spec.Document) []string {
	t.Helper()
	data, err := pptx.Render(doc, pptx.Options{Now: fixedNow})
	if err != nil {
		t.Fatalf("pptx.Render: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open pptx: %v", err)
	}
	var all []string
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "ppt/slides/slide") || !strings.HasSuffix(f.Name, ".xml") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		body, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		all = append(all, textRuns(t, body)...)
	}
	return all
}

// textRuns pulls every <a:t> out of a slide part.
func textRuns(t *testing.T, body []byte) []string {
	t.Helper()
	var out []string
	dec := xml.NewDecoder(bytes.NewReader(body))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("parse slide xml: %v", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "t" {
			continue
		}
		var s string
		if err := dec.DecodeElement(&s, &se); err != nil {
			t.Fatalf("decode text run: %v", err)
		}
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

// planStrings is the video's text, in the form the browser receives it.
func planStrings(t *testing.T, doc *spec.Document) []string {
	t.Helper()
	plan, err := videoplan.Build(doc, videoplan.Options{Now: fixedNow})
	if err != nil {
		t.Fatalf("videoplan.Build: %v", err)
	}
	var all []string
	for _, s := range plan.Scenes {
		all = append(all, s.Title...)
		all = append(all, s.Subtitle...)
		all = append(all, s.Lines...)
		all = append(all, s.Caption...)
		all = append(all, s.Period)
		for _, k := range s.KPIs {
			all = append(all, k.Label, k.Value, k.Delta)
		}
		for _, f := range s.Facts {
			all = append(all, f.Label)
			all = append(all, f.Value...)
		}
		if s.Table != nil {
			all = append(all, s.Table.Header...)
			for _, row := range s.Table.Rows {
				all = append(all, row...)
			}
			all = append(all, s.Table.Total...)
		}
	}
	return all
}

// The gate. Every figure the video will show is a figure the PDF and the deck
// show, character for character.
//
// The direction is deliberate: the video is the format under suspicion,
// because it is the one whose renderer is in another language in another
// process. A figure the PDF shows and the video does not is not a
// disagreement — the video is a summary and drops rows the document keeps,
// which `videoplan`'s paging does on purpose.
func TestTheThreeFormatsAgreeOnEveryFigure(t *testing.T) {
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			doc := loadFixture(t, name)

			plan := figuresIn(planStrings(t, doc))
			if len(plan) == 0 {
				t.Fatal("the plan carries no figures; this fixture proves nothing")
			}
			docFigures := figuresIn(pdfStrings(t, doc))
			deckFigures := figuresIn(pptxStrings(t, doc))

			for _, d := range disagreements(plan, docFigures, deckFigures) {
				t.Error(d)
			}
		})
	}
}

// disagreements is the comparison itself, lifted out so the test below can run
// it against a deliberately corrupted input. A gate whose failure path is
// never executed is a gate nobody has checked.
func disagreements(plan, doc, deck map[string]bool) []string {
	var out []string
	for _, fig := range sorted(plan) {
		if !doc[fig] {
			out = append(out, "the video shows "+fig+" and the PDF does not")
		}
		if !deck[fig] {
			out = append(out, "the video shows "+fig+" and the deck does not")
		}
	}
	return out
}

// The renderer-chosen labels — the words no spec contains and every format has
// to pick for itself. `T-R4` extracted `labels` so the deck and the PDF could
// not disagree about the Indonesian for "Prepared for"; this extends that to
// the third renderer, which reads its copy of those words out of the plan.
func TestTheThreeFormatsAgreeOnTheWordsWeChose(t *testing.T) {
	doc := loadFixture(t, "monthly_sales")

	pdfText := strings.Join(pdfStrings(t, doc), "\n")
	deckText := strings.Join(pptxStrings(t, doc), "\n")
	planText := strings.Join(planStrings(t, doc), "\n")

	// Indonesian, because that is the fixture's locale and because a label
	// this codebase got wrong once would be an Indonesian one: the English is
	// the fallback and the translation is the part with a decision in it.
	for _, word := range []string{"Disiapkan untuk", "Dibuat"} {
		for _, c := range []struct {
			format string
			text   string
		}{{"pdf", pdfText}, {"deck", deckText}, {"video", planText}} {
			if !strings.Contains(c.text, word) {
				t.Errorf("%s does not carry the label %q", c.format, word)
			}
		}
	}
}

// The gate has to be able to fail. A test that passes because its inputs
// happen to agree, and would go on passing if they did not, is a comment.
//
// This is the mutation the whole ticket exists to catch, applied to the real
// pipeline: one figure reformatted the way a React component doing its own
// `toLocaleString` would reformat it — Indonesian grouping swapped for
// English — and then the real comparison run over it.
func TestTheGateFailsWhenAFigureIsReformattedInTheVideo(t *testing.T) {
	doc := loadFixture(t, "monthly_sales")

	planFigures := figuresIn(planStrings(t, doc))
	docFigures := figuresIn(pdfStrings(t, doc))
	deckFigures := figuresIn(pptxStrings(t, doc))

	if got := disagreements(planFigures, docFigures, deckFigures); len(got) != 0 {
		t.Fatalf("the fixture does not agree before it is corrupted: %v", got)
	}

	var picked string
	for _, fig := range sorted(planFigures) {
		if strings.Contains(fig, ".") {
			picked = fig
			break
		}
	}
	if picked == "" {
		t.Fatal("no grouped figure in this fixture; the mutation cannot be applied")
	}

	// `Rp 3.863.405.700` → `Rp 3,863,405,700`: the same number, formatted by
	// somebody else. The characters are what a reader compares against the PDF
	// attached to the same email.
	corrupted := map[string]bool{}
	for fig := range planFigures {
		corrupted[fig] = true
	}
	delete(corrupted, picked)
	corrupted[strings.ReplaceAll(picked, ".", ",")] = true

	got := disagreements(corrupted, docFigures, deckFigures)
	if len(got) == 0 {
		t.Fatalf("the gate passed a video that reformatted %q; it proves nothing", picked)
	}
	t.Logf("the gate refuses a reformatted figure, as it must: %v", got)
}

func sorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
