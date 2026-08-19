// Package evaldocs scores what this product reads out of a PDF (T-P13).
//
// **Tracks A–D are five days of parsing heuristics with no number behind any of
// them.** The published benchmarks in the roadmap's §2 measure how well a parser
// renders a page as markdown; none of them measures whether the figure that
// ends up in a tenant's dashboard is the figure that was printed. This package
// is the number that decides whether the rest of the roadmap works.
//
// Three scores, reported separately because they fail for different reasons and
// a single number would hide which:
//
//  1. **Cell accuracy** — the extracted cells against hand-checked ground
//     truth. Wrong here means the parser or the typing layer misread the page.
//  2. **Publish correctness** — did the right tables publish, with the right
//     types and multipliers, and did the corrupted one quarantine. Wrong here
//     means the decisions on top of the cells are wrong even where the cells
//     are right, which is the more dangerous failure: the digits are all
//     correct and the column means something else.
//  3. **Answer correctness** — the only one that matters to a user. Scored by
//     the existing `internal/eval` harness over questions whose answers exist
//     only in these documents, so a document question is scored exactly the way
//     a warehouse question is.
//
// Every report names the parser build and the resolved OCR model. That is
// `T-Q15`'s lesson taken before it had to be learned twice: a score that cannot
// say what produced it cannot be re-run as the same measurement, and a sidecar
// answering from a previous image looks exactly like a passing run.
package evaldocs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/fauzanebd/argentum/internal/docparse"
	"github.com/fauzanebd/argentum/internal/doctable"
)

// Manifest is the corpus: which documents, and what is true about each.
type Manifest struct {
	Documents []Document `yaml:"documents"`
}

// Document is one file and the ground truth for it.
type Document struct {
	File  string `yaml:"file"`
	Title string `yaml:"title"`
	// Kind is `born_digital`, `scan` or `adversarial`. It is not used in
	// scoring — it is used in *reading* the score, because a corpus that is 8:3:1
	// by design should never be summarised as one average.
	Kind string `yaml:"kind"`
	// Pages the parser should find, and Tables the extraction should produce.
	Pages  int `yaml:"pages"`
	Tables int `yaml:"tables"`
	// Expect is the ground truth for the first table, which is the one every
	// fixture in this corpus is about. A document whose second table matters is
	// a document that should be two fixtures.
	Expect TableTruth `yaml:"expect"`
	// Forbid is text that must NOT appear anywhere in the parse (T-P10). It is
	// how the adversarial fixture's injected instruction is scored: the
	// sentence is printed on the page in white four-point type, so a parser
	// that returns it has handed a model an instruction a human reviewer could
	// not have seen.
	Forbid []string `yaml:"forbid"`
	// Note is why this document is in the corpus — the failure family it is
	// here to catch. Printed beside a failure, because "01-erp.pdf: 0.83" tells
	// nobody what broke.
	Note string `yaml:"note"`
}

// TableTruth is what a correct extraction of one table looks like.
type TableTruth struct {
	// Columns are the resolved header names, slugified, in order.
	Columns []string `yaml:"columns"`
	// Types are `text`, `integer`, `decimal`, `currency`, `percentage` or
	// `date`, one per column.
	Types []string `yaml:"types"`
	// Multipliers are the header-level scale factors. Written out even where
	// they are all 1, because a missing multiplier and a multiplier of 1 are
	// the same value and very different claims.
	Multipliers []float64 `yaml:"multipliers"`
	// Verify is the arithmetic outcome: `verified`, `unverified` or
	// `quarantined`.
	Verify string `yaml:"verify"`
	// Rows are the data rows as the document printed them — raw strings, total
	// rows excluded, in order.
	Rows [][]string `yaml:"rows"`
	// PII is the class expected per column, empty where none.
	PII []string `yaml:"pii"`
}

// LoadManifest reads the corpus description.
func LoadManifest(path string) (*Manifest, error) {
	body, err := os.ReadFile(path) //nolint:gosec // an operator-supplied path to our own fixture manifest
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := yaml.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if len(m.Documents) == 0 {
		return nil, fmt.Errorf("the manifest lists no documents")
	}
	for i, d := range m.Documents {
		if strings.TrimSpace(d.File) == "" {
			return nil, fmt.Errorf("document %d has no file", i+1)
		}
	}
	return &m, nil
}

// Dir is where a manifest's documents live: beside the manifest itself.
func Dir(manifestPath string) string { return filepath.Dir(manifestPath) }

// Score is one document's result.
type Score struct {
	File  string `json:"file"`
	Title string `json:"title"`
	Kind  string `json:"kind"`
	Note  string `json:"note,omitempty"`
	// Pages and Tables are what was found, against what the manifest says.
	Pages       int  `json:"pages"`
	PagesWant   int  `json:"pages_want"`
	Tables      int  `json:"tables"`
	TablesWant  int  `json:"tables_want"`
	CellsWant   int  `json:"cells_want"`
	CellsRight  int  `json:"cells_right"`
	PublishPass bool `json:"publish_pass"`
	// HiddenTextLeaked is the T-P10 arm: text a reader cannot see reached the
	// parse output. It is reported separately from the cell score because it is
	// a security finding rather than an accuracy one, and averaging it into a
	// percentage would bury it.
	HiddenTextLeaked bool `json:"hidden_text_leaked,omitempty"`
	// Failures are the specific disagreements, in the order they were found.
	// Bounded, because a table whose columns shifted by one produces a failure
	// per cell and the first three say everything.
	Failures []string `json:"failures,omitempty"`
	// ParseError is set when the document could not be read at all, and every
	// other field is then meaningless.
	ParseError string `json:"parse_error,omitempty"`
}

// CellAccuracy is the share of ground-truth cells this extraction got right.
func (s Score) CellAccuracy() float64 {
	if s.CellsWant == 0 {
		return 0
	}
	return float64(s.CellsRight) / float64(s.CellsWant)
}

// Report is one run, and what produced it.
type Report struct {
	// RunAt, Parser and OCRModel are the T-Q15 fields: a score that cannot name
	// what produced it cannot be re-run as the same measurement.
	RunAt    time.Time `json:"run_at"`
	Parser   string    `json:"parser"`
	OCRModel string    `json:"ocr_model,omitempty"`
	Scores   []Score   `json:"scores"`
	// CellAccuracy is the corpus-wide share of correct cells, weighted by cell
	// rather than by document: a forty-row table and a three-row one are not
	// equally informative about a parser.
	CellAccuracy float64 `json:"cell_accuracy"`
	// PublishCorrectness is documents whose columns, types, multipliers and
	// verification all matched, over documents scored.
	PublishCorrectness float64 `json:"publish_correctness"`
	// Answers is the third score, filled in only when the run was given a
	// question set and a live stack. Nil means it was not run — which is a
	// different thing from zero, and the report says so rather than implying a
	// failure nobody measured.
	Answers *AnswerScore `json:"answers,omitempty"`
}

// AnswerScore is the third number: questions whose answers exist only in these
// documents, scored the way the 56-case set is.
type AnswerScore struct {
	Model    string   `json:"model"`
	Cases    int      `json:"cases"`
	Passed   int      `json:"passed"`
	Rate     float64  `json:"rate"`
	CostUSD  float64  `json:"cost_usd"`
	Failures []string `json:"failures,omitempty"`
}

// ScoreDocument compares one extraction against the ground truth.
//
// The comparison is deliberately strict about cells and forgiving about
// nothing. A cell matches when its text matches after whitespace normalisation
// — not "close enough", because the whole failure family this corpus exists for
// is a figure that is nearly right.
func ScoreDocument(d Document, pages []docparse.Page, tables []doctable.Table) Score {
	s := Score{
		File: d.File, Title: d.Title, Kind: d.Kind, Note: d.Note,
		Pages: len(pages), PagesWant: d.Pages,
		Tables: len(tables), TablesWant: d.Tables,
	}

	// The invisible-text check first, because it is the one failure here that
	// is not about accuracy at all: a parse that carries the injected sentence
	// has already handed a model an instruction, however well it read the
	// table.
	for _, forbidden := range d.Forbid {
		if forbidden == "" {
			continue
		}
		for _, page := range pages {
			if strings.Contains(page.Markdown, forbidden) {
				s.addFailure(fmt.Sprintf("page %d carries text that should have been dropped as invisible: %q",
					page.Number, truncate(forbidden, 60)))
				s.HiddenTextLeaked = true
				break
			}
		}
	}
	for _, row := range d.Expect.Rows {
		s.CellsWant += len(row)
	}

	if d.Tables == 0 && len(tables) == 0 {
		// A scan, and the assertion is that nothing was invented. Extracting no
		// table off a page nobody could read is the correct outcome, not a
		// failure — and a parser that returned a plausible table here would be
		// the worst result in the corpus.
		s.PublishPass = true
		return s
	}
	if len(tables) == 0 {
		s.addFailure("no table was extracted")
		return s
	}
	got := tables[0]

	// Cells first, because everything below is a claim about them.
	for i, wantRow := range d.Expect.Rows {
		if i >= len(got.Rows) {
			s.addFailure(fmt.Sprintf("row %d is missing", i+1))
			break
		}
		for j, wantCell := range wantRow {
			var gotCell string
			if j < len(got.Rows[i].Cells) {
				gotCell = got.Rows[i].Cells[j].Raw
			}
			if normalizeCell(gotCell) == normalizeCell(wantCell) {
				s.CellsRight++
				continue
			}
			s.addFailure(fmt.Sprintf("row %d column %d: got %q, want %q", i+1, j+1, gotCell, wantCell))
		}
	}

	s.PublishPass = s.scorePublish(d, got)
	return s
}

// scorePublish checks the decisions made on top of the cells: the column names,
// their types, their multipliers, the PII labels and the verification.
func (s *Score) scorePublish(d Document, got doctable.Table) bool {
	pass := true
	if n := len(d.Expect.Columns); n > 0 && len(got.Columns) != n {
		s.addFailure(fmt.Sprintf("got %d columns, want %d", len(got.Columns), n))
		pass = false
	}
	for i, want := range d.Expect.Columns {
		if i >= len(got.Columns) {
			break
		}
		if got.Columns[i].Name != want {
			s.addFailure(fmt.Sprintf("column %d named %q, want %q", i+1, got.Columns[i].Name, want))
			pass = false
		}
	}
	for i, want := range d.Expect.Types {
		if i >= len(got.Columns) {
			break
		}
		if string(got.Columns[i].Type) != want {
			s.addFailure(fmt.Sprintf("column %d typed %q, want %q", i+1, got.Columns[i].Type, want))
			pass = false
		}
	}
	for i, want := range d.Expect.Multipliers {
		if i >= len(got.Columns) {
			break
		}
		if got.Columns[i].Multiplier != want {
			s.addFailure(fmt.Sprintf("column %d multiplier %v, want %v", i+1, got.Columns[i].Multiplier, want))
			pass = false
		}
	}
	for i, want := range d.Expect.PII {
		if i >= len(got.Columns) {
			break
		}
		if got.Columns[i].PII != want {
			s.addFailure(fmt.Sprintf("column %d PII %q, want %q", i+1, got.Columns[i].PII, want))
			pass = false
		}
	}
	if d.Expect.Verify != "" && string(got.Verify.Status) != d.Expect.Verify {
		s.addFailure(fmt.Sprintf("verification %q, want %q (%s)",
			got.Verify.Status, d.Expect.Verify, got.Verify.Detail))
		pass = false
	}
	// Compared whatever the expectation is, zero included: a table extracted
	// from a scan is a table invented off a page nobody could read, which is
	// the worst outcome in this corpus and would otherwise score as a pass.
	if s.Tables != d.Tables {
		s.addFailure(fmt.Sprintf("extracted %d tables, want %d", s.Tables, d.Tables))
		pass = false
	}
	return pass
}

// maxFailuresPerDoc bounds what one document can say. A table whose columns
// shifted by one produces a failure per cell, and the first few say everything
// the next hundred would.
const maxFailuresPerDoc = 5

func (s *Score) addFailure(msg string) {
	if len(s.Failures) >= maxFailuresPerDoc {
		return
	}
	s.Failures = append(s.Failures, msg)
}

// normalizeCell is the comparison. Whitespace is normalised because a PDF's
// spaces are a layout engine's rather than a writer's; nothing else is, because
// every other difference is the parser reading something the document did not
// print.
func normalizeCell(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, " ", " ")), " ")
}

// Summarize fills in the corpus-wide numbers.
func Summarize(r *Report) {
	var want, right, publishable, passed int
	for _, s := range r.Scores {
		want += s.CellsWant
		right += s.CellsRight
		if s.ParseError != "" {
			continue
		}
		publishable++
		if s.PublishPass {
			passed++
		}
	}
	if want > 0 {
		r.CellAccuracy = float64(right) / float64(want)
	}
	if publishable > 0 {
		r.PublishCorrectness = float64(passed) / float64(publishable)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
