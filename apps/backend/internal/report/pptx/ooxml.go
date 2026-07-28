package pptx

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"time"
)

// The OPC package: a zip of XML parts joined by relationship files.
//
// It is built by hand rather than through a library because there is no Go
// library that writes PresentationML — the ones that exist read and edit
// existing decks, or write spreadsheets. What that costs is this file. What it
// buys is that every byte in the output is a byte this repository chose, which
// is what makes the deck deterministic and what makes a compatibility problem
// debuggable by reading the XML rather than by bisecting a dependency.
//
// Namespaces, once, because they are written into every part below:
//
//	p = presentationml/2006/main   the deck, its slides, its masters
//	a = drawingml/2006/main        every shape, every run of text, every table
//	r = officeDocument/2006/relationships   r:id, r:embed — the pointers between parts

const (
	nsP = `http://schemas.openxmlformats.org/presentationml/2006/main`
	nsA = `http://schemas.openxmlformats.org/drawingml/2006/main`
	nsR = `http://schemas.openxmlformats.org/officeDocument/2006/relationships`

	relTypeOfficeDocument = `http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument`
	relTypeCoreProps      = `http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties`
	relTypeExtendedProps  = `http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties`
	relTypeSlide          = `http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide`
	relTypeSlideMaster    = `http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster`
	relTypeSlideLayout    = `http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout`
	relTypeNotesMaster    = `http://schemas.openxmlformats.org/officeDocument/2006/relationships/notesMaster`
	relTypeNotesSlide     = `http://schemas.openxmlformats.org/officeDocument/2006/relationships/notesSlide`
	relTypeTheme          = `http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme`
	relTypePresProps      = `http://schemas.openxmlformats.org/officeDocument/2006/relationships/presProps`
	relTypeTableStyles    = `http://schemas.openxmlformats.org/officeDocument/2006/relationships/tableStyles`
	relTypeImage          = `http://schemas.openxmlformats.org/officeDocument/2006/relationships/image`
)

const xmlDecl = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

// part is one file in the package. Order is preserved, which is half of what
// makes the output byte-identical between runs.
type part struct {
	name string
	data []byte
}

// pkg accumulates parts and writes the zip.
type pkg struct {
	parts []part

	// overrides are the content-type declarations for parts whose extension
	// does not identify them. Every XML part in a presentation needs one; a
	// missing override is the single most common way a hand-built deck opens
	// as "repair needed".
	overrides []string
}

func (p *pkg) add(name string, data []byte) {
	p.parts = append(p.parts, part{name: name, data: data})
}

func (p *pkg) addXML(name, body string) {
	p.add(name, []byte(xmlDecl+body))
}

func (p *pkg) override(partName, contentType string) {
	p.overrides = append(p.overrides,
		fmt.Sprintf(`<Override PartName="%s" ContentType="%s"/>`, partName, contentType))
}

// rel is one relationship: an id local to the part that declares it, a type,
// and a target relative to that part.
type rel struct {
	id     string
	typ    string
	target string
}

func relsXML(rels []rel) string {
	var sb strings.Builder
	sb.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for _, r := range rels {
		fmt.Fprintf(&sb, `<Relationship Id="%s" Type="%s" Target="%s"/>`, r.id, r.typ, r.target)
	}
	sb.WriteString(`</Relationships>`)
	return sb.String()
}

// contentTypes is written last, once every part is known.
func (p *pkg) contentTypes() string {
	var sb strings.Builder
	sb.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	sb.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	sb.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	sb.WriteString(`<Default Extension="png" ContentType="image/png"/>`)
	for _, o := range p.overrides {
		sb.WriteString(o)
	}
	sb.WriteString(`</Types>`)
	return sb.String()
}

// zipBytes writes the package.
//
// Determinism is the whole design of this function. Entry order is the order
// parts were added, the modification time on every entry is the document's own
// generated_at rather than the clock, and the compressor is the standard
// Deflate at its default level, which is a pure function of its input. Two
// renders of one spec therefore produce identical bytes — the same property
// T-R2 had to fight gofpdf for, obtained here by construction because nothing
// else is writing the file.
func (p *pkg) zipBytes(modified time.Time) ([]byte, error) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	// [Content_Types].xml is written first. The OPC specification does not
	// require it to be, and every implementation that reads the central
	// directory does not care — but the streaming readers do, and it costs
	// nothing to be first.
	entries := append([]part{{
		name: "[Content_Types].xml",
		data: []byte(xmlDecl + p.contentTypes()),
	}}, p.parts...)

	// Zip carries local times in DOS format with no zone; normalising to UTC
	// keeps a machine in Jakarta and a runner in UTC writing the same bytes.
	modified = modified.UTC()

	for _, e := range entries {
		hdr := &zip.FileHeader{
			Name:     e.name,
			Method:   zip.Deflate,
			Modified: modified,
		}
		f, err := w.CreateHeader(hdr)
		if err != nil {
			return nil, fmt.Errorf("pptx: zip %s: %w", e.name, err)
		}
		if _, err := f.Write(e.data); err != nil {
			return nil, fmt.Errorf("pptx: write %s: %w", e.name, err)
		}
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("pptx: close zip: %w", err)
	}
	return buf.Bytes(), nil
}
