package theme

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
)

// tokensJSON is the generator's input, five levels up from this package. It is
// absent when the backend is built from its own directory alone (the Docker
// images do that), so the tests that read it skip rather than fail.
const tokensJSON = "../../../../../packages/design-tokens/tokens.json"

func TestVerifyFonts(t *testing.T) {
	if err := VerifyFonts(); err != nil {
		t.Fatalf("embedded fonts are not usable: %v", err)
	}
}

// The faces are embedded, so the only way to exercise the failure path is to
// hand verifyFace something broken. These are the two shapes a bad vendored
// font actually takes: an empty file, and a file that is not a TrueType font
// (an OTF renamed, a Git LFS pointer, a truncated download).
func TestVerifyFaceRejectsBadBytes(t *testing.T) {
	cases := []struct {
		name  string
		bytes []byte
	}{
		{"empty", nil},
		{"truncated", []byte{0x00, 0x01}},
		{"opentype CFF", []byte("OTTO____")},
		{"git-lfs pointer", []byte("version https://git-lfs.github.com/spec/v1\n")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyFace(face{FontBody, fontstyle.Normal, "test face", tc.bytes})
			if err == nil {
				t.Fatal("verifyFace accepted bytes that are not a TrueType font")
			}
		})
	}
}

func TestCustomFontsRegistersEveryFace(t *testing.T) {
	fonts, err := CustomFonts()
	if err != nil {
		t.Fatalf("CustomFonts: %v", err)
	}
	if len(fonts) != len(faces()) {
		t.Fatalf("registered %d faces, want %d", len(fonts), len(faces()))
	}

	// maroto reads Bytes; a face registered without them renders as nothing.
	for _, f := range fonts {
		if len(f.Bytes) == 0 {
			t.Errorf("face %s/%q registered with no bytes", f.Family, f.Style)
		}
	}

	// Both families must be present, or a renderer asking for FontMedium falls
	// back to whatever gofpdf decides — which is the Helvetica regression.
	seen := map[string]bool{}
	for _, f := range fonts {
		seen[f.Family] = true
	}
	for _, family := range []string{FontBody, FontMedium} {
		if !seen[family] {
			t.Errorf("family %q was not registered", family)
		}
	}
}

func TestMarotoConfigUsesTheTheme(t *testing.T) {
	cfg, err := MarotoConfig()
	if err != nil {
		t.Fatalf("MarotoConfig: %v", err)
	}
	if got := cfg.Margins.Left; got != Page.Margin {
		t.Errorf("left margin = %v, want %v", got, Page.Margin)
	}
	if got := cfg.Dimensions.Width; got != Page.Width {
		t.Errorf("page width = %v, want A4 %v", got, Page.Width)
	}
	if got := cfg.DefaultFont.Family; got != FontBody {
		t.Errorf("default font family = %q, want %q", got, FontBody)
	}
	if got := cfg.DefaultFont.Size; got != TypeScale.Body {
		t.Errorf("default font size = %v, want %v", got, TypeScale.Body)
	}
}

func TestColorHex(t *testing.T) {
	if got := ColorPrimary.Hex(); got != "#F25C5C" {
		t.Errorf("ColorPrimary.Hex() = %s, want #F25C5C", got)
	}
	if got := (Color{}).Hex(); got != "#000000" {
		t.Errorf("zero Color.Hex() = %s, want #000000", got)
	}
	p := ColorPrimary.Props()
	if p.Red != 0xF2 || p.Green != 0x5C || p.Blue != 0x5C {
		t.Errorf("ColorPrimary.Props() = %+v, want 242/92/92", p)
	}
}

func TestPageGeometry(t *testing.T) {
	// A4 minus 18mm on each side.
	if got := Page.ContentWidth(); got != 174 {
		t.Errorf("ContentWidth() = %v, want 174", got)
	}
	// 297 - 36 margins - 12 header - 10 footer.
	if got := Page.ContentHeight(); got != 239 {
		t.Errorf("ContentHeight() = %v, want 239", got)
	}
}

func TestSeriesColorWraps(t *testing.T) {
	if got := SeriesColor(0); got != ChartPalette[0] {
		t.Errorf("SeriesColor(0) = %v, want the brand red", got)
	}
	if got := SeriesColor(len(ChartPalette)); got != ChartPalette[0] {
		t.Errorf("SeriesColor(%d) did not wrap to the first series", len(ChartPalette))
	}
	if got := SeriesColor(-3); got != ChartPalette[0] {
		t.Errorf("SeriesColor(-3) = %v, want the first series", got)
	}
}

// TestGeneratedTokensMatchSource is the drift check that runs in `go test`.
// CI also regenerates and diffs (the `tokens` job), which catches a stale
// tokens_gen.go; this catches the other direction — a tokens_gen.go edited by
// hand to a value tokens.json never contained.
func TestGeneratedTokensMatchSource(t *testing.T) {
	path := filepath.FromSlash(tokensJSON)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("packages/design-tokens is outside this build tree")
		}
		t.Fatalf("read %s: %v", path, err)
	}

	var file struct {
		Color     map[string]json.RawMessage `json:"color"`
		TypeScale map[string]json.RawMessage `json:"typeScale"`
		Print     map[string]json.RawMessage `json:"print"`
		Chart     struct {
			Palette []struct {
				Hex string `json:"hex"`
			} `json:"palette"`
		} `json:"chart"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("parse tokens.json: %v", err)
	}

	type token struct {
		Hex   string  `json:"hex"`
		Scope string  `json:"scope"`
		PT    float64 `json:"pt"`
		MM    float64 `json:"mm"`
	}
	// Keys starting with $ are the file's own documentation, not tokens.
	decode := func(group map[string]json.RawMessage) map[string]token {
		out := make(map[string]token, len(group))
		for name, raw := range group {
			if len(name) > 0 && name[0] == '$' {
				continue
			}
			var tok token
			if err := json.Unmarshal(raw, &tok); err != nil {
				t.Fatalf("parse token %q: %v", name, err)
			}
			out[name] = tok
		}
		return out
	}
	src := struct {
		Color     map[string]token
		TypeScale map[string]token
		Print     map[string]token
	}{decode(file.Color), decode(file.TypeScale), decode(file.Print)}

	// Spot-check every axis rather than every value: a generator bug shows up
	// in the shape of the output, not in one unlucky token.
	if want := src.Color["primary"].Hex; ColorPrimary.Hex() != want {
		t.Errorf("ColorPrimary = %s, tokens.json says %s", ColorPrimary.Hex(), want)
	}
	if want := src.Color["border"].Hex; ColorBorder.Hex() != want {
		t.Errorf("ColorBorder = %s, tokens.json says %s", ColorBorder.Hex(), want)
	}
	if want := src.TypeScale["body"].PT; TypeScale.Body != want {
		t.Errorf("TypeScale.Body = %v, tokens.json says %v", TypeScale.Body, want)
	}
	if want := src.Print["margin"].MM; Page.Margin != want {
		t.Errorf("Page.Margin = %v, tokens.json says %v", Page.Margin, want)
	}
	if len(ChartPalette) != len(file.Chart.Palette) {
		t.Fatalf("ChartPalette has %d entries, tokens.json has %d",
			len(ChartPalette), len(file.Chart.Palette))
	}
	for i, want := range file.Chart.Palette {
		if got := ChartPalette[i].Hex(); got != want.Hex {
			t.Errorf("ChartPalette[%d] = %s, tokens.json says %s", i, got, want.Hex)
		}
	}

	// Sidebar tokens are scope: "web". If one reaches Go, the scope filter has
	// stopped working and the report theme is carrying dashboard chrome.
	for name, tok := range src.Color {
		if tok.Scope == "web" && name == "sidebarSurface" {
			return // present in tokens.json, absent here: nothing to assert but the compile
		}
	}
	t.Error("tokens.json no longer marks any colour scope: web — the scope filter is untested")
}
