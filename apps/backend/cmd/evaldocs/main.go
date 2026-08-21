// Command evaldocs scores what this product reads out of a PDF (T-P13).
//
// Three numbers, and they are reported separately because they fail for
// different reasons:
//
//	make eval-docs-fixtures   # write the corpus (12 PDFs, generated from code)
//	make eval-docs            # cell accuracy + publish correctness, $0.00
//	go run ./cmd/evaldocs -answers testdata/eval/documents/questions.yaml
//
// **The third score does not seed anything.** It asks the questions and scores
// the answers; getting the corpus into the tenant — uploaded, parsed, reviewed
// and applied — is a person's job through the product's own surfaces, because
// that path *is* half of what the score is measuring. A harness that published
// the tables itself would be scoring the parser twice and the product not at
// all, and it would skip the one step this roadmap made a human decision.
//
// The first two need the parser sidecar and nothing else — no model, no
// database, no money — which is the bucket this repository's own record says
// gets run. The third needs everything a chat turn needs, and it is the only
// one that measures what a user experiences.
//
// Every report names the parser build and the OCR model. T-Q15's lesson, taken
// before it has to be learned twice here: a score that cannot say what produced
// it cannot be re-run as the same measurement, and this repository has already
// lost one gate turn to a sidecar serving from a previous image.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/fauzanebd/argentum/internal/adapters/db/mysql"
	_ "github.com/fauzanebd/argentum/internal/adapters/db/postgres"
	_ "github.com/fauzanebd/argentum/internal/adapters/db/sqlserver"
	"github.com/fauzanebd/argentum/internal/bootstrap"
	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/docparse"
	"github.com/fauzanebd/argentum/internal/doctable"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/eval"
	"github.com/fauzanebd/argentum/internal/evaldocs"
)

func main() {
	// `.env` before the flag defaults below are evaluated. The parser's shared
	// secret lives there, and a default read from the bare process environment
	// left `make eval-docs` on a stock checkout reporting **0% cell accuracy**
	// with "the parser rejected our shared secret" beside it — a number that
	// reads as a broken product and was a missing variable. Found 2026-08-19,
	// and it is the same rule requireDocumentSource already enforces further
	// down: a score nobody can tell from a setup mistake is worse than no score.
	_, _ = config.Load()

	var (
		manifestPath = flag.String("manifest", "testdata/eval/documents/manifest.yaml", "corpus manifest")
		gen          = flag.Bool("gen", false, "write the fixture PDFs and exit")
		parserURL    = flag.String("parser", envOr("DOCPARSE_URL", "http://localhost:8091"), "docparse base URL")
		secret       = flag.String("secret", os.Getenv("DOCPARSE_SHARED_SECRET"), "docparse shared secret")
		answers      = flag.String("answers", "", "question set to score against a live stack (needs the corpus already applied)")
		out          = flag.String("out", "", "write the JSON report here as well as to stdout")
		timeout      = flag.Duration("timeout", 2*time.Minute, "per-document parse timeout")
	)
	flag.Parse()

	if *gen {
		written, err := evaldocs.GenerateCorpus(evaldocs.Dir(*manifestPath))
		if err != nil {
			fail(err)
		}
		for _, path := range written {
			fmt.Println("wrote", path)
		}
		return
	}

	manifest, err := evaldocs.LoadManifest(*manifestPath)
	if err != nil {
		fail(err)
	}
	parser := docparse.New(docparse.Options{BaseURL: *parserURL, Secret: *secret, Timeout: *timeout})
	if parser == nil {
		fail(fmt.Errorf("no parser configured; set DOCPARSE_URL or pass -parser"))
	}

	report := evaldocs.Report{RunAt: time.Now().UTC(), OCRModel: os.Getenv("DOC_OCR_MODEL")}
	dir := evaldocs.Dir(*manifestPath)
	ctx := context.Background()

	for _, doc := range manifest.Documents {
		body, err := os.ReadFile(filepath.Join(dir, doc.File)) //nolint:gosec // our own fixture
		if err != nil {
			report.Scores = append(report.Scores, evaldocs.Score{
				File: doc.File, Title: doc.Title, Kind: doc.Kind,
				ParseError: "could not be read: " + err.Error() +
					" — run `make eval-docs-fixtures` first",
			})
			continue
		}
		parsed, err := parser.Parse(ctx, strings.NewReader(string(body)), 0)
		if err != nil {
			report.Scores = append(report.Scores, evaldocs.Score{
				File: doc.File, Title: doc.Title, Kind: doc.Kind,
				ParseError: err.Error(),
			})
			continue
		}
		if report.Parser == "" {
			report.Parser = parsed.Parser.Name + " " + parsed.Parser.Version
		}
		tables := doctable.Build(parsed.Pages, doctable.Options{MinRows: 2})
		report.Scores = append(report.Scores, evaldocs.ScoreDocument(doc, parsed.Pages, tables))
	}

	if *answers != "" {
		score, err := runAnswers(ctx, *answers)
		if err != nil {
			fail(err)
		}
		report.Answers = score
	}

	evaldocs.Summarize(&report)
	printSummary(report)

	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fail(err)
	}
	if *out != "" {
		if err := os.WriteFile(*out, body, 0o600); err != nil {
			fail(err)
		}
		fmt.Println("\nreport written to", *out)
	}
}

// printSummary is what somebody actually reads. Per document first, because a
// corpus-wide average tells nobody which family broke — and the note beside
// each row is the failure family the fixture is in the corpus for.
func printSummary(r evaldocs.Report) {
	fmt.Printf("Document extraction — %s\n", r.RunAt.Format(time.RFC3339))
	fmt.Printf("parser: %s\n", orNone(r.Parser))
	fmt.Printf("ocr model: %s\n\n", orNone(r.OCRModel))

	fmt.Printf("%-28s %-13s %8s %9s  %s\n", "document", "kind", "cells", "publish", "note")
	for _, s := range r.Scores {
		if s.ParseError != "" {
			fmt.Printf("%-28s %-13s %8s %9s  %s\n", s.File, s.Kind, "-", "-", "PARSE: "+s.ParseError)
			continue
		}
		publish := "fail"
		if s.PublishPass {
			publish = "pass"
		}
		// A scan has no cells to get right, and printing 0% for it would read
		// as a failure of the thing that in fact worked: nothing was invented
		// off a page nobody could read.
		cells := "     n/a"
		if s.CellsWant > 0 {
			cells = fmt.Sprintf("%7.0f%%", s.CellAccuracy()*100)
		}
		fmt.Printf("%-28s %-13s %s %9s  %s\n", s.File, s.Kind, cells, publish, s.Note)
		if s.HiddenTextLeaked {
			fmt.Printf("%-28s   ↳ INVISIBLE TEXT REACHED THE PARSE OUTPUT\n", "")
		}
		for _, f := range s.Failures {
			fmt.Printf("%-28s   ↳ %s\n", "", f)
		}
	}

	// Nothing parsed means nothing was measured. Printing 0.0% here would
	// publish a failure of the product for what is a failure of the rig — the
	// mistake this run's own gate found.
	if parsed := countParsed(r); parsed == 0 {
		fmt.Printf("\ncell accuracy:       not run (no document parsed: %s)\n", firstParseError(r))
		fmt.Printf("publish correctness: not run\n")
	} else {
		fmt.Printf("\ncell accuracy:       %.1f%%\n", r.CellAccuracy*100)
		fmt.Printf("publish correctness: %.1f%%\n", r.PublishCorrectness*100)
	}
	if r.Answers == nil {
		// Said rather than left blank: "not run" and "zero" are different
		// facts, and a report that showed 0% here would be claiming a failure
		// nobody measured.
		fmt.Printf("answer correctness:  not run (needs a live stack and model spend)\n")
		return
	}
	fmt.Printf("answer correctness:  %.1f%% (%d/%d, $%.4f, %s)\n",
		r.Answers.Rate*100, r.Answers.Passed, r.Answers.Cases, r.Answers.CostUSD, r.Answers.Model)
}

// runAnswers scores questions whose answers exist only in the documents.
//
// It runs the real agent — same factory, tools, guardrails and prompt as the
// worker — through `internal/eval`, which is the same harness the 56-case set
// is scored with. Reused rather than rebuilt on purpose: a document question
// scored by a second harness would produce a number nobody could compare with
// the warehouse ones, and comparing them is the whole point.
func runAnswers(ctx context.Context, setPath string) (*evaldocs.AnswerScore, error) {
	set, err := eval.LoadSet(setPath)
	if err != nil {
		return nil, fmt.Errorf("load question set: %w", err)
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	stack, err := bootstrap.New(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: %w", err)
	}
	defer stack.Close()

	tenant, err := eval.EnsureTenant(ctx, stack, eval.SeedOpts{
		DemoDSN:          os.Getenv("EVAL_DEMO_DSN"),
		MetabaseHostPort: os.Getenv("EVAL_METABASE_HOST"),
	})
	if err != nil {
		return nil, fmt.Errorf("eval tenant: %w", err)
	}
	if err := requireDocumentSource(ctx, stack, tenant.CompanyID); err != nil {
		return nil, err
	}

	runner := eval.NewRunner(stack, tenant, 3*time.Minute)
	started := time.Now()
	results := make([]eval.Result, 0, len(set.Cases))
	for i, c := range set.Cases {
		fmt.Printf("[%2d/%2d] %-32s ", i+1, len(set.Cases), c.ID)
		res := runner.RunCase(ctx, c)
		results = append(results, res)
		if res.Passed {
			fmt.Printf("PASS  (%.1fs)\n", float64(res.DurationMS)/1000)
		} else {
			fmt.Printf("FAIL  (%.1fs)\n", float64(res.DurationMS)/1000)
		}
	}
	rep := eval.Summarize(set.Name, cfg.LLMModel, started, results)

	score := &evaldocs.AnswerScore{
		Model: rep.Model, Cases: rep.Total, Passed: rep.Passed,
		Rate: rep.PassRat, CostUSD: rep.TotalCostUSD,
	}
	for _, r := range rep.Results {
		if !r.Passed {
			score.Failures = append(score.Failures, r.ID)
		}
	}
	return score, nil
}

// requireDocumentSource refuses to score answers against a tenant that has no
// published document tables.
//
// Without this the run would score a set of questions about documents against a
// tenant that holds none, every case would fail, and the number would be read
// as "the feature does not work" rather than as "nobody uploaded anything". A
// score that cannot be told from a setup mistake is worse than no score.
func requireDocumentSource(ctx context.Context, stack *bootstrap.Stack, companyID string) error {
	conns, err := stack.Connections.ListByCompany(ctx, companyID)
	if err != nil {
		return fmt.Errorf("list sources: %w", err)
	}
	for _, c := range conns {
		if c.Origin == domain.OriginDocument {
			return nil
		}
	}
	return fmt.Errorf(
		"the eval tenant has no document source: upload the corpus in testdata/eval/documents, " +
			"review it and apply its tables before scoring answers")
}

func countParsed(r evaldocs.Report) int {
	n := 0
	for _, s := range r.Scores {
		if s.ParseError == "" {
			n++
		}
	}
	return n
}

func firstParseError(r evaldocs.Report) string {
	for _, s := range r.Scores {
		if s.ParseError != "" {
			return s.ParseError
		}
	}
	return "no documents in the manifest"
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(none)"
	}
	return s
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "eval-docs:", err)
	os.Exit(1)
}
