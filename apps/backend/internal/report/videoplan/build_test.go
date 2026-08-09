package videoplan

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/report/pptx"
	"github.com/fauzanebd/argentum/internal/report/spec"
)

// The fixtures are the PDF renderer's own, read from its testdata directory
// rather than copied into this one.
//
// That is the point of the whole track, not a shortcut: the same spec renders
// as a PDF, a deck and a video with no format-specific authoring. A copy here
// would let the three drift the first time somebody tuned a video by editing
// its fixture, and the test would still pass while the claim stopped being
// true. The only field this test changes is `format`.
var fixtures = []string{
	"monthly_sales.json",
	"invoice.json",
	"kpi_summary.json",
	"export_200.json",
	"v1_legacy.json",
}

var update = flag.Bool("update", false, "rewrite the golden plans")

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
	doc.Format = "mp4"
	doc.Normalize()
	return &doc
}

// fixtureNow pins the clock. v1_legacy.json carries no generated_at and
// spec.Document.Generated falls back to time.Now() without one, which would
// make the golden depend on the day it was written. The deck renderer records
// the same trap.
var fixtureNow = time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC)

func build(t *testing.T, name string) *Plan {
	t.Helper()
	p, err := Build(loadFixture(t, name), Options{Now: fixtureNow})
	if err != nil {
		t.Fatalf("build %s: %v", name, err)
	}
	return p
}

// TestGoldenPlans is the contract test.
//
// Chart images are replaced by a digest before comparison. A golden nobody can
// read is a golden nobody checks — 60 KB of base64 in a diff hides the one line
// that changed, and the digest proves the image is stable without printing it.
func TestGoldenPlans(t *testing.T) {
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			got := mustJSON(t, digestImages(build(t, name)))
			golden := filepath.Join("testdata", strings.TrimSuffix(name, ".json")+".plan.json")

			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("wrote %s (%d bytes)", golden, len(got))
				return
			}

			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden (run `go test ./internal/report/videoplan -update`): %v", err)
			}
			if !bytes.Equal(bytes.TrimSpace(want), bytes.TrimSpace(got)) {
				t.Errorf("plan differs from %s; run with -update to see the change as a diff", golden)
			}
		})
	}
}

// TestBuildIsPure is decision 9's foundation: the video is not byte-stable, so
// the plan has to be.
func TestBuildIsPure(t *testing.T) {
	for _, name := range fixtures {
		a := mustJSON(t, build(t, name))
		b := mustJSON(t, build(t, name))
		if !bytes.Equal(a, b) {
			t.Errorf("%s: two builds differ", name)
		}
	}
}

// TestIndonesianFixtureIsIndonesianThroughout covers the reason this package
// exists: a renderer that formatted its own numbers would produce
// "Rp 3,863,405,700" in the one format a customer watches.
func TestIndonesianFixtureIsIndonesianThroughout(t *testing.T) {
	p := build(t, "monthly_sales.json")

	if p.Locale != "id" {
		t.Errorf("locale is %q, want id", p.Locale)
	}

	cover := p.Scenes[0]
	if cover.Kind != KindCover {
		t.Fatalf("first scene is %s, want cover", cover.Kind)
	}
	var labels []string
	for _, f := range cover.Facts {
		labels = append(labels, f.Label)
	}
	joined := strings.Join(labels, " | ")
	if !strings.Contains(joined, "Disiapkan untuk") {
		t.Errorf("cover facts are %q; the renderer's own words are not in the document's language", joined)
	}

	// Every figure in the plan uses "." as the thousands separator and "," as
	// the decimal one. An English-formatted rupiah figure would carry a comma
	// between groups of three digits.
	englishGrouped := regexp.MustCompile(`\d,\d{3}`)
	for i, s := range p.Scenes {
		for _, cell := range allStrings(s) {
			if !strings.Contains(cell, "Rp") {
				continue
			}
			if englishGrouped.MatchString(cell) {
				t.Errorf("scene %d formats a rupiah figure in English: %q", i, cell)
			}
		}
	}
}

// TestChartIsTheDeckImage is locked decision 6, asserted across two renderers
// rather than trusted: the video embeds the picture the deck embeds, so a chart
// cannot say two things about one series.
func TestChartIsTheDeckImage(t *testing.T) {
	doc := loadFixture(t, "monthly_sales.json")
	p, err := Build(doc, Options{Now: fixtureNow})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	var planPNGs [][]byte
	for _, s := range p.Scenes {
		if s.Chart == nil {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s.Chart.DataURI, "data:image/png;base64,"))
		if err != nil {
			t.Fatalf("decode chart data uri: %v", err)
		}
		planPNGs = append(planPNGs, raw)
	}
	if len(planPNGs) == 0 {
		t.Fatal("the monthly sales fixture produced no chart scene")
	}

	deckDoc := loadFixture(t, "monthly_sales.json")
	deckDoc.Format = "pptx"
	deck, err := pptx.Render(deckDoc, pptx.Options{Now: fixtureNow})
	if err != nil {
		t.Fatalf("render deck: %v", err)
	}
	deckPNGs := deckImages(t, deck)

	if len(deckPNGs) != len(planPNGs) {
		t.Fatalf("deck embeds %d images, plan carries %d", len(deckPNGs), len(planPNGs))
	}
	for i := range planPNGs {
		if !bytes.Equal(planPNGs[i], deckPNGs[i]) {
			t.Errorf("chart %d differs between the deck and the video (%d vs %d bytes)",
				i, len(deckPNGs[i]), len(planPNGs[i]))
		}
	}
}

// TestLongTableContinues is the 200-row export: it pages, it says so, and every
// scene stays inside the ceiling.
func TestLongTableContinues(t *testing.T) {
	p := build(t, "export_200.json")

	tables, continued := 0, 0
	for _, s := range p.Scenes {
		if s.Table == nil {
			continue
		}
		tables++
		if s.Continued {
			continued++
		}
	}
	if tables < 2 {
		t.Fatalf("the 200-row export produced %d table scenes", tables)
	}
	if continued != tables-1 {
		t.Errorf("%d of %d table scenes are marked continued; every one but the first should be",
			continued, tables)
	}

	// The total row lands once, on the last table scene of the run.
	totals := 0
	for _, s := range p.Scenes {
		if s.Table != nil && len(s.Table.Total) > 0 {
			totals++
		}
	}
	if totals > 1 {
		t.Errorf("the total row appears on %d scenes", totals)
	}
}

// TestNoSceneOutstaysItsWelcome bounds every scene at both ends. A scene under
// the floor reads as a flash; one over the ceiling is a still image.
func TestNoSceneOutstaysItsWelcome(t *testing.T) {
	floor := frames(MinSceneSeconds)
	ceiling := frames(MaxSceneSeconds)
	for _, name := range fixtures {
		p := build(t, name)
		for i, s := range p.Scenes {
			switch s.Kind {
			case KindSection, KindClosing, KindCover:
				continue // fixed durations, deliberately outside the range
			}
			if s.Frames < floor {
				t.Errorf("%s scene %d (%s) is %d frames, under the %d floor", name, i, s.Kind, s.Frames, floor)
			}
			if s.Frames > ceiling {
				t.Errorf("%s scene %d (%s) is %d frames, over the %d ceiling", name, i, s.Kind, s.Frames, ceiling)
			}
		}
	}
}

// TestTotalFramesMatchesTheScenes catches the field going stale, which is the
// failure mode of every summary number in this repository.
func TestTotalFramesMatchesTheScenes(t *testing.T) {
	for _, name := range fixtures {
		p := build(t, name)
		sum := 0
		for _, s := range p.Scenes {
			sum += s.Frames
		}
		if sum != p.TotalFrames {
			t.Errorf("%s: TotalFrames is %d, scenes sum to %d", name, p.TotalFrames, sum)
		}
	}
}

// TestOverlongSpecIsRefusedBeforeAnythingIsBuilt is the limit that protects a
// worker's minutes rather than its memory.
//
// **Every chart in the document is deliberately unrenderable.** That is the
// proof rather than a flourish: if the projection had reached one, the error
// would be the chart's. Getting the limit's error back is the only way to show
// from outside the package that nothing was rasterised — the same trick T-A2
// uses when it asserts that nothing was uploaded.
func TestOverlongSpecIsRefusedBeforeAnythingIsBuilt(t *testing.T) {
	long := strings.Repeat("Pendapatan bulan ini naik dibandingkan bulan lalu karena permintaan ritel meningkat. ", 40)
	sections := []spec.Section{{Type: spec.SectionCover, Text: "Too long"}}
	for range 60 {
		sections = append(sections, spec.Section{Type: spec.SectionParagraph, Text: long})
		sections = append(sections, spec.Section{Type: spec.SectionChart, Chart: &spec.Chart{
			Type: "not_a_chart_type", Labels: []string{"a", "b"},
			Series: []spec.Series{{Name: "s", Values: []float64{1, 2}}},
		}})
	}
	doc := &spec.Document{
		SpecVersion: 2, Format: "mp4", Title: "Too long",
		GeneratedAt: "2026-07-28T08:00:00Z",
		Content:     spec.Content{Sections: sections},
	}
	doc.Normalize()

	_, err := Build(doc, Options{Now: fixtureNow})
	if err == nil {
		t.Fatal("a 60-section document was accepted")
	}
	if !strings.Contains(err.Error(), "limit is") {
		t.Errorf("the refusal does not name the cap: %v", err)
	}
	if strings.Contains(err.Error(), "chart") {
		t.Errorf("a chart was reached before the limit was checked: %v", err)
	}
}

// TestAnUnrenderableChartIsAnError is the control for the test above: without
// the limit in the way, that same chart does fail the build. Without this, the
// proof there would hold just as well if charts never failed at all.
func TestAnUnrenderableChartIsAnError(t *testing.T) {
	doc := &spec.Document{
		SpecVersion: 2, Format: "mp4", Title: "One bad chart",
		GeneratedAt: "2026-07-28T08:00:00Z",
		Content: spec.Content{Sections: []spec.Section{
			{Type: spec.SectionChart, Chart: &spec.Chart{
				Type: "not_a_chart_type", Labels: []string{"a", "b"},
				Series: []spec.Series{{Name: "s", Values: []float64{1, 2}}},
			}},
		}},
	}
	doc.Normalize()

	if _, err := Build(doc, Options{Now: fixtureNow}); err == nil {
		t.Fatal("an unrenderable chart was accepted")
	} else if !strings.Contains(err.Error(), "chart") {
		t.Errorf("the error does not name the chart: %v", err)
	}
}

// TestTightLimitsAreEnforced checks the caps are read rather than assumed.
func TestTightLimitsAreEnforced(t *testing.T) {
	doc := loadFixture(t, "monthly_sales.json")
	if _, err := Build(doc, Options{Now: fixtureNow, Limits: Limits{MaxScenes: 3}}); err == nil {
		t.Error("MaxScenes 3 accepted a full report")
	}
	if _, err := Build(loadFixture(t, "monthly_sales.json"),
		Options{Now: fixtureNow, Limits: Limits{MaxTotalFrames: 90}}); err == nil {
		t.Error("MaxTotalFrames 90 accepted a full report")
	}
}

// TestMetricsAreTheSurface pins the one relationship the whole package rests
// on: the video frame is the PowerPoint slide at 2 px per point.
func TestMetricsAreTheSurface(t *testing.T) {
	p := build(t, "kpi_summary.json")
	if p.Width != 1920 || p.Height != 1080 {
		t.Fatalf("frame is %d×%d", p.Width, p.Height)
	}
	// canvas.Type.H1 is 29pt; at 2 px/pt that is 58.
	if p.Metrics.Type.H1 != 58 {
		t.Errorf("H1 is %dpx, want 58 — the 2 px/pt relationship has moved", p.Metrics.Type.H1)
	}
	if p.Metrics.ContentWidth != 1648 {
		t.Errorf("content width is %dpx, want 1648 (290.667mm)", p.Metrics.ContentWidth)
	}
}

// TestEveryLineFitsItsBox is the video's version of the deck's overflow gate.
// Nothing here re-measures: the lines came out of the same measurement, so what
// this catches is a builder that forgot to wrap at all.
func TestEveryLineFitsItsBox(t *testing.T) {
	for _, name := range fixtures {
		p := build(t, name)
		for i, s := range p.Scenes {
			for _, line := range s.Lines {
				if strings.Contains(line, "\n") {
					t.Errorf("%s scene %d carries an unwrapped line", name, i)
				}
			}
			if len(s.Title) > maxTitleLines {
				t.Errorf("%s scene %d has a %d-line title", name, i, len(s.Title))
			}
			if s.Kind == KindStatement && len(s.Lines) > maxStatementLines {
				t.Errorf("%s scene %d has %d statement lines", name, i, len(s.Lines))
			}
		}
	}
}

// TestNotesSurviveTheProjection: the prose the deck puts in speaker notes is
// carried here for T-V4's player. Losing it would make the video the one format
// that drops the argument.
func TestNotesSurviveTheProjection(t *testing.T) {
	p := build(t, "monthly_sales.json")
	notes := 0
	for _, s := range p.Scenes {
		if strings.TrimSpace(s.Notes) != "" {
			notes++
		}
	}
	if notes == 0 {
		t.Error("no scene carries notes; the narrative was dropped")
	}
}

// TestWritePlans writes the fixture plans, undigested, for apps/render.
//
// **The goldens in testdata are not renderable.** Their chart images are
// replaced by a digest so the golden stays reviewable — a diff hiding one
// changed line under 60 KB of base64 is a diff nobody reads — and feeding one
// to the renderer produces `net::ERR_UNKNOWN_URL_SCHEME` on `sha256:…`, which
// is what happened the first time somebody tried. This is the way to get a real
// one.
//
//	ARGENTUM_PLAN_OUT=/tmp/plans go test ./internal/report/videoplan
//	pnpm --filter @argentum/render render:fixture /tmp/plans/monthly_sales.plan.json out --stills
//
// It lives here rather than in a cmd/ because the package is internal and a
// throwaway binary to reach it is a binary somebody has to maintain — the same
// reasoning as the deck's TestWriteDecks, which is the prior art.
func TestWritePlans(t *testing.T) {
	dir := os.Getenv("ARGENTUM_PLAN_OUT")
	if dir == "" {
		t.Skip("set ARGENTUM_PLAN_OUT to write the fixture plans to a directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range fixtures {
		out := mustJSON(t, build(t, name))
		path := filepath.Join(dir, strings.TrimSuffix(name, ".json")+".plan.json")
		if err := os.WriteFile(path, out, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		t.Logf("wrote %s (%d bytes)", path, len(out))
	}
}

// --- helpers ---

func allStrings(s Scene) []string {
	out := append([]string{}, s.Title...)
	out = append(out, s.Subtitle...)
	out = append(out, s.Lines...)
	out = append(out, s.Caption...)
	out = append(out, s.Period)
	for _, f := range s.Facts {
		out = append(out, f.Label)
		out = append(out, f.Value...)
	}
	for _, k := range s.KPIs {
		out = append(out, k.Label, k.Value, k.Delta)
	}
	if s.Table != nil {
		out = append(out, s.Table.Header...)
		out = append(out, s.Table.Total...)
		for _, row := range s.Table.Rows {
			out = append(out, row...)
		}
	}
	return out
}

// digestImages replaces every data URI with a digest so a golden stays
// reviewable. The digest still fails when the image changes, which is the
// property the golden is for.
func digestImages(p *Plan) *Plan {
	out := *p
	out.Scenes = append([]Scene{}, p.Scenes...)
	if out.Brand.LogoDataURI != "" {
		out.Brand.LogoDataURI = digest(out.Brand.LogoDataURI)
	}
	for i := range out.Scenes {
		if c := out.Scenes[i].Chart; c != nil {
			cc := *c
			cc.DataURI = digest(cc.DataURI)
			out.Scenes[i].Chart = &cc
		}
	}
	return &out
}

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return append(out, '\n')
}

// deckImages pulls the chart images out of a rendered deck, in slide order.
func deckImages(t *testing.T, data []byte) [][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open deck: %v", err)
	}
	var out [][]byte
	for i := 1; ; i++ {
		name := fmt.Sprintf("ppt/media/image%d.png", i)
		found := false
		for _, f := range zr.File {
			if f.Name != name {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open %s: %v", name, err)
			}
			body, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			out = append(out, body)
			found = true
			break
		}
		if !found {
			return out
		}
	}
}
