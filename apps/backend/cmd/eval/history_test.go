package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/eval"
)

func writeReport(t *testing.T, dir, name string, rep eval.Report) {
	t.Helper()
	raw, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), raw, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func report(model string, started time.Time, passed, total int) eval.Report {
	return eval.Report{
		Set:       "demo-retail-v1",
		Model:     model,
		StartedAt: started,
		Total:     total,
		Passed:    passed,
		Failed:    total - passed,
		PassRat:   float64(passed) / float64(total),
	}
}

func TestLatestReportPicksTheNewestForTheSameModel(t *testing.T) {
	dir := t.TempDir()
	old := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)

	// Written oldest-last on purpose: the newest run must be found by its
	// StartedAt, not by directory order or by filename.
	writeReport(t, dir, "b.json", report("deepseek/deepseek-v3.2", recent, 44, 56))
	writeReport(t, dir, "a.json", report("deepseek/deepseek-v3.2", old, 50, 56))
	writeReport(t, dir, "other-model.json", report("moonshotai/kimi-k2.6", recent, 53, 56))

	got, name, ok := latestReport(dir, "demo-retail-v1", "deepseek/deepseek-v3.2")
	if !ok {
		t.Fatalf("no prior report found")
	}
	if name != "b.json" || got.Passed != 44 {
		t.Fatalf("picked %s (%d passed), want b.json (44)", name, got.Passed)
	}
}

func TestLatestReportIgnoresOtherSetsAndUnreadableFiles(t *testing.T) {
	dir := t.TempDir()
	when := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)

	writeReport(t, dir, "other-set.json", eval.Report{
		Set: "something-else", Model: "m", StartedAt: when, Total: 10, Passed: 10,
	})
	// A matrix file unmarshals into a Report with no cases in it. Comparing a
	// run against a zero-total report would print "+56 cases" and mean nothing.
	writeReport(t, dir, "matrix.json", eval.Report{Set: "demo-retail-v1", Model: "m", StartedAt: when})
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{{{"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, name, ok := latestReport(dir, "demo-retail-v1", "m"); ok {
		t.Fatalf("compared against %s, which carries no cases", name)
	}
	if _, _, ok := latestReport(filepath.Join(dir, "nope"), "demo-retail-v1", "m"); ok {
		t.Fatalf("a missing directory must not report a prior run")
	}
}

func TestReportFilenameSortsByTimeWithinAModel(t *testing.T) {
	first := reportFilename(report("moonshotai/kimi-k2.6",
		time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC), 53, 56))
	second := reportFilename(report("moonshotai/kimi-k2.6",
		time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC), 55, 56))

	if first == second {
		t.Fatalf("two runs of the same model share a filename: %s", first)
	}
	if first >= second {
		t.Fatalf("filenames do not sort chronologically: %s then %s", first, second)
	}
	if want := "demo-retail-v1-moonshotai-kimi-k2-6-20260818T090000Z.json"; first != want {
		t.Fatalf("filename = %s, want %s", first, want)
	}
}

func TestNoiseBandIsTheDocumentedTwoCases(t *testing.T) {
	// The band is a measured property of the set (Phase 2s §4). If somebody
	// changes it, this test is where they have to say so.
	if noiseBandCases != 2 {
		t.Fatalf("noise band = %d; the set's measured band is 2 cases", noiseBandCases)
	}
}
