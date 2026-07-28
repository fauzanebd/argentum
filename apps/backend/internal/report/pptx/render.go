// Package pptx renders a report spec into a PowerPoint deck.
//
// It is the same spec the PDF renderer reads, projected onto slides rather than
// onto pages. A deck is not a second content model — it is what gets shown in
// the meeting the report was attached to — so nothing in the spec is
// deck-specific and nothing about a document has to be authored twice. What
// changes is the projection: one idea per slide, the prose moved into the
// speaker notes, and a table that pages across seventeen sheets of A4 turned
// into a run of continuation slides.
//
// Three decisions that are not obvious from the code:
//
//   - The OOXML is written by hand — archive/zip plus string templates over the
//     parts — because no Go library writes PresentationML. That is the cost; the
//     return is that every byte is one this repository chose, so the output is
//     deterministic by construction and a compatibility problem is debugged by
//     reading XML rather than by bisecting a dependency.
//   - Fonts are named, never embedded. OOXML font embedding works in PowerPoint
//     on Windows and nowhere else, and doubles the file. So every run names
//     Space Grotesk with a declared substitution class (see pitchFamily), and
//     every text measurement is discounted by substitutionMargin so a line that
//     just fits in the embedded face still fits in whatever the reader's machine
//     substitutes.
//   - There is no layout engine on the other side. PowerPoint draws exactly the
//     boxes it is given and silently clips whatever does not fit in them. So
//     every block is measured here, before it is placed, and anything that does
//     not fit continues onto a slide that says "(cont.)". Silent clipping is a
//     bug in this renderer; a continuation slide is not.
package pptx

import (
	"fmt"
	"strings"
	"time"

	"github.com/fauzanebd/argentum/internal/report/format"
	"github.com/fauzanebd/argentum/internal/report/labels"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/report/theme"
)

// Brand is the per-tenant identity on a deck. It is the PDF renderer's Brand,
// field for field, so T-R5 fills one struct and both renderers read it.
type Brand struct {
	// Name is the legal entity the deck belongs to. It goes on the cover, into
	// the footer when there is no confidentiality label, and into the file's
	// Author property.
	Name string

	// LogoPNG is reserved for T-R5. The deck draws the wordmark until then; a
	// logo on the dark cover needs a light-on-dark variant, which is a question
	// for the branding ticket rather than for this one.
	LogoPNG []byte

	// Confidentiality is the default label for decks that do not set one.
	Confidentiality string

	// FooterNote is a legal line some tenants must carry on every slide.
	FooterNote string
}

// Options is everything the renderer needs that does not come from the model.
type Options struct {
	Brand Brand

	// Currency is the company's ISO 4217 default, used when the spec does not
	// name one.
	Currency string

	// Locale overrides the locale derived from the currency.
	Locale string

	// Now is the fallback generated-at when the spec omits one. Zero means
	// time.Now(); tests set it to keep bytes stable.
	Now time.Time
}

// Render turns a document spec into PPTX bytes.
func Render(doc *spec.Document, opts Options) ([]byte, error) {
	if doc == nil {
		return nil, fmt.Errorf("pptx: nil spec")
	}
	r, err := newRenderer(doc, opts)
	if err != nil {
		return nil, err
	}
	return r.run()
}

type renderer struct {
	doc  *spec.Document
	opts Options

	fmt    format.Options
	words  labels.Set
	title  string
	confid string
	genAt  time.Time

	sections []spec.Section
	slides   []slide
}

func newRenderer(doc *spec.Document, opts Options) (*renderer, error) {
	r := &renderer{doc: doc, opts: opts}

	currency := doc.Currency
	if currency == "" {
		currency = opts.Currency
	}
	locale := doc.Locale
	if locale == "" {
		locale = opts.Locale
	}
	loc := format.LocaleForCurrency(currency)
	if locale != "" {
		loc = format.ParseLocale(locale)
	}
	r.fmt = format.Options{
		Locale:   loc,
		Currency: strings.ToUpper(strings.TrimSpace(currency)),
		Decimals: format.AutoDecimals,
	}
	r.words = labels.For(loc)

	r.title = strings.TrimSpace(doc.Title)
	r.sections = doc.Content.Sections

	// A spec with a bare content.table and no sections is the shape the model
	// uses for "just give me the data". It becomes a one-section document
	// rather than a special case, so the paging and the column solver are the
	// same code.
	if len(r.sections) == 0 && doc.Content.Table != nil {
		t := doc.Content.Table
		r.sections = []spec.Section{{
			Type:     spec.SectionTable,
			Columns:  t.Columns,
			Rows:     t.Rows,
			TotalRow: t.TotalRow,
			Caption:  t.Caption,
		}}
	}
	if len(r.sections) == 0 {
		return nil, fmt.Errorf("pptx: content.sections or content.table required")
	}

	genAt, explicit := doc.Generated()
	if !explicit && !opts.Now.IsZero() {
		genAt = opts.Now
	}
	r.genAt = genAt

	r.confid = strings.TrimSpace(opts.Brand.Confidentiality)
	if c := doc.Cover(); c != nil && strings.TrimSpace(c.Confidentiality) != "" {
		r.confid = strings.TrimSpace(c.Confidentiality)
	}
	return r, nil
}

func (r *renderer) run() ([]byte, error) {
	if err := r.buildSlides(); err != nil {
		return nil, err
	}
	if len(r.slides) == 0 {
		return nil, fmt.Errorf("pptx: nothing to render")
	}
	return r.pack()
}

// pack assembles the OPC package.
//
// The order of everything in here is fixed rather than incidental: parts are
// added in a stable order, relationship ids are assigned by position, and the
// zip is written with the document's own generated_at as every entry's
// timestamp. Two renders of one spec therefore produce identical bytes, which
// is what makes a golden test possible and what makes "did this change the
// output" a question a diff can answer.
func (r *renderer) pack() ([]byte, error) {
	p := &pkg{}

	// Package-level parts.
	p.addXML("_rels/.rels", relsXML([]rel{
		{"rId1", relTypeOfficeDocument, "ppt/presentation.xml"},
		{"rId2", relTypeCoreProps, "docProps/core.xml"},
		{"rId3", relTypeExtendedProps, "docProps/app.xml"},
	}))

	author := strings.TrimSpace(r.doc.Meta.Author)
	if author == "" {
		author = strings.TrimSpace(r.opts.Brand.Name)
	}
	title := r.title
	if title == "" {
		title = "Report"
	}
	notesCount := 0
	for _, s := range r.slides {
		if strings.TrimSpace(s.notes) != "" {
			notesCount++
		}
	}

	p.addXML("docProps/core.xml", corePropsXML(title, author, r.doc.Meta.Subject, r.doc.Meta.Keywords, r.genAt))
	p.override("/docProps/core.xml", "application/vnd.openxmlformats-package.core-properties+xml")
	p.addXML("docProps/app.xml", appPropsXML(r.opts.Brand.Name, len(r.slides), notesCount))
	p.override("/docProps/app.xml", "application/vnd.openxmlformats-officedocument.extended-properties+xml")

	// Theme, master, layout, notes master. The notes master gets its own copy
	// of the theme rather than sharing the slide master's: PowerPoint writes it
	// that way, and a part with two independent owners is a class of validator
	// complaint that costs nothing to avoid.
	p.addXML("ppt/theme/theme1.xml", themeXML("Argentum"))
	p.override("/ppt/theme/theme1.xml", "application/vnd.openxmlformats-officedocument.theme+xml")
	p.addXML("ppt/theme/theme2.xml", themeXML("Argentum Notes"))
	p.override("/ppt/theme/theme2.xml", "application/vnd.openxmlformats-officedocument.theme+xml")

	p.addXML("ppt/slideMasters/slideMaster1.xml", slideMasterXML())
	p.override("/ppt/slideMasters/slideMaster1.xml", "application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml")
	p.addXML("ppt/slideMasters/_rels/slideMaster1.xml.rels", relsXML([]rel{
		{"rId1", relTypeSlideLayout, "../slideLayouts/slideLayout1.xml"},
		{"rId2", relTypeTheme, "../theme/theme1.xml"},
	}))

	p.addXML("ppt/slideLayouts/slideLayout1.xml", slideLayoutXML())
	p.override("/ppt/slideLayouts/slideLayout1.xml", "application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml")
	p.addXML("ppt/slideLayouts/_rels/slideLayout1.xml.rels", relsXML([]rel{
		{"rId1", relTypeSlideMaster, "../slideMasters/slideMaster1.xml"},
	}))

	p.addXML("ppt/notesMasters/notesMaster1.xml", notesMasterXML())
	p.override("/ppt/notesMasters/notesMaster1.xml", "application/vnd.openxmlformats-officedocument.presentationml.notesMaster+xml")
	p.addXML("ppt/notesMasters/_rels/notesMaster1.xml.rels", relsXML([]rel{
		{"rId1", relTypeTheme, "../theme/theme2.xml"},
	}))

	p.addXML("ppt/presProps.xml", presPropsXML)
	p.override("/ppt/presProps.xml", "application/vnd.openxmlformats-officedocument.presentationml.presProps+xml")
	p.addXML("ppt/tableStyles.xml", tableStylesXML)
	p.override("/ppt/tableStyles.xml", "application/vnd.openxmlformats-officedocument.presentationml.tableStyles+xml")

	// Slides, their notes and their images.
	slideRelIDs := make([]string, 0, len(r.slides))
	imageIndex := 0

	for i, s := range r.slides {
		n := i + 1
		slideName := fmt.Sprintf("ppt/slides/slide%d.xml", n)

		rels := []rel{{"rId1", relTypeSlideLayout, "../slideLayouts/slideLayout1.xml"}}
		notes := strings.TrimSpace(s.notes)
		if notes != "" {
			rels = append(rels, rel{"rId2", relTypeNotesSlide, fmt.Sprintf("../notesSlides/notesSlide%d.xml", n)})
		}

		imageRel := ""
		if s.chart != nil && len(s.chart.png) > 0 {
			imageIndex++
			imageRel = fmt.Sprintf("rId%d", len(rels)+1)
			imageName := fmt.Sprintf("image%d.png", imageIndex)
			p.add("ppt/media/"+imageName, s.chart.png)
			rels = append(rels, rel{imageRel, relTypeImage, "../media/" + imageName})
		}

		p.addXML(slideName, r.slideXML(s, n, imageRel))
		p.override("/"+slideName, "application/vnd.openxmlformats-officedocument.presentationml.slide+xml")
		p.addXML(fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", n), relsXML(rels))

		if notes != "" {
			notesName := fmt.Sprintf("ppt/notesSlides/notesSlide%d.xml", n)
			p.addXML(notesName, notesSlideXML(notes))
			p.override("/"+notesName, "application/vnd.openxmlformats-officedocument.presentationml.notesSlide+xml")
			p.addXML(fmt.Sprintf("ppt/notesSlides/_rels/notesSlide%d.xml.rels", n), relsXML([]rel{
				{"rId1", relTypeNotesMaster, "../notesMasters/notesMaster1.xml"},
				{"rId2", relTypeSlide, fmt.Sprintf("../slides/slide%d.xml", n)},
			}))
		}

		// rId1 and rId2 on the presentation are the slide master and the notes
		// master; the fixed parts take rId3 to rId5, so slides start at rId6.
		slideRelIDs = append(slideRelIDs, fmt.Sprintf("rId%d", 6+i))
	}

	presRels := []rel{
		{"rId1", relTypeSlideMaster, "slideMasters/slideMaster1.xml"},
		{"rId2", relTypeNotesMaster, "notesMasters/notesMaster1.xml"},
		{"rId3", relTypePresProps, "presProps.xml"},
		{"rId4", relTypeTheme, "theme/theme1.xml"},
		{"rId5", relTypeTableStyles, "tableStyles.xml"},
	}
	for i := range r.slides {
		presRels = append(presRels, rel{
			id:     fmt.Sprintf("rId%d", 6+i),
			typ:    relTypeSlide,
			target: fmt.Sprintf("slides/slide%d.xml", i+1),
		})
	}

	p.addXML("ppt/presentation.xml", presentationXML(slideRelIDs))
	p.override("/ppt/presentation.xml", "application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml")
	p.addXML("ppt/_rels/presentation.xml.rels", relsXML(presRels))

	return p.zipBytes(r.genAt)
}

// notesSlideXML writes the speaker notes for one slide.
//
// This is where the narrative lives. The model's paragraphs arrive here whole —
// the slide itself carries only their lead sentence — which is the single
// change that makes a generated deck feel authored: the presenter has something
// to say, and the audience has something they can read from the back of the
// room.
func notesSlideXML(notes string) string {
	var body strings.Builder
	for i, block := range strings.Split(notes, "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		p := para{
			runs:        []run{{text: block, size: 12, color: theme.ColorForeground}},
			align:       alignLeft,
			lineSpacing: 1.2,
		}
		if i > 0 {
			p.spaceBefore = 8
		}
		body.WriteString(paraXML(p))
	}
	if body.Len() == 0 {
		body.WriteString(`<a:p><a:endParaRPr lang="en-US"/></a:p>`)
	}

	return fmt.Sprintf(`<p:notes xmlns:a="%s" xmlns:r="%s" xmlns:p="%s">`+
		`<p:cSld><p:spTree>%s`+
		`<p:sp><p:nvSpPr><p:cNvPr id="2" name="Notes Placeholder 1"/>`+
		`<p:cNvSpPr><a:spLocks noGrp="1"/></p:cNvSpPr><p:nvPr><p:ph type="body" idx="1"/></p:nvPr></p:nvSpPr>`+
		`<p:spPr/><p:txBody><a:bodyPr/><a:lstStyle/>%s</p:txBody></p:sp>`+
		`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:notes>`,
		nsA, nsR, nsP, emptyShapeTree, body.String())
}
