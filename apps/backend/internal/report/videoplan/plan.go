// Package videoplan projects a report spec onto a finished video.
//
// **It renders nothing.** What it produces is a Plan: a list of scenes in which
// every string is final, every line is already broken, every duration is already
// counted in frames, and every image is already a data URI. The renderer on the
// other side (apps/render, T-V2) draws it and decides nothing.
//
// That division is T-V1's locked decision 2, and it is the whole reason this
// package exists rather than a JSON encoder somewhere in the handler. The deck
// and the PDF agree about what "Prepared for" is in Indonesian, how many
// decimals a column takes and how wide it is, because neither of them decides —
// internal/report/labels, format and layout do. A third renderer written in
// TypeScript, in another process, cannot reach any of that. So either the
// decisions travel with the data, or the one format a customer *watches* is the
// one that says `Rp 3,863,405,700`.
//
// Two consequences worth stating because they look like over-engineering:
//
//   - **Text arrives pre-wrapped.** Scenes carry lines, not paragraphs. The
//     browser is asked to draw strings at fixed positions, never to lay them
//     out. A line that fits here fits there because the same font metrics
//     decided both — see internal/report/canvas, where the video frame and the
//     PowerPoint slide are shown to be the same surface at 2 px per point.
//   - **Durations are computed from the text.** The model chooses content; it
//     does not choose seconds. spec.Chart.HeightMM's comment makes the general
//     argument — a model asked for a number in millimetres picks one that makes
//     the aspect ratio wrong — and seconds are worse, because a wrong duration
//     is only visible after a two-minute render.
package videoplan

// Version is the plan contract's version.
//
// It exists so apps/render can refuse a plan it does not understand with a
// message naming what it supports, rather than drawing three blank scenes.
// **Additive fields do not bump it**; a change to what an existing field means
// does. The renderer's rule is the mirror image: ignore fields you do not know,
// refuse versions you do not know.
const Version = 1

// Scene kinds. These are the values a renderer switches on to pick a component.
const (
	KindCover     = "cover"
	KindSection   = "section"
	KindStatement = "statement"
	KindQuote     = "quote"
	KindKPI       = "kpi"
	KindTable     = "table"
	KindChart     = "chart"
	KindClosing   = "closing"
)

// Chart reveal styles. How the mask over the chart image moves — never a
// redraw, because the pixels underneath are the ones the PDF and the deck
// already show (locked decision 6).
const (
	RevealNone  = "none"
	RevealWipe  = "wipe"
	RevealGrow  = "grow"
	RevealSweep = "sweep"
)

// Plan is one finished video.
type Plan struct {
	Version int `json:"version"`

	// Width, Height and FPS describe the output. They are here rather than in
	// the renderer's config because a plan measured for 1920×1080 is not a plan
	// for any other size: the line breaks in it were decided against that width.
	Width  int `json:"width"`
	Height int `json:"height"`
	FPS    int `json:"fps"`

	// TotalFrames is the sum of the scenes' frames, carried so the renderer
	// never re-derives the composition's length from a list it might filter.
	TotalFrames int `json:"total_frames"`

	// Still marks a plan whose scenes are frames rather than beats: fps 1, one
	// frame a scene, and every entrance drawn at its end state (T-G3). A
	// carousel is such a plan. The renderer freezes each scene at the end of
	// its entrance instead of animating it, and refuses a stills request
	// against a plan without this flag — a plan built for one output is not a
	// plan for the other. Additive, so Version stays 1: an older renderer
	// ignores it and draws the video the plan also describes.
	Still bool `json:"still,omitempty"`

	// Locale is "id" or "en". The renderer uses it for the document language
	// and for nothing else — every word it would otherwise affect is already
	// resolved.
	Locale string `json:"locale"`

	// Title is the document's title, for the player's page title and the
	// video's metadata.
	Title string `json:"title"`

	Metrics Metrics `json:"metrics"`
	Brand   Brand   `json:"brand"`
	Scenes  []Scene `json:"scenes"`
}

// Metrics is the surface, in CSS pixels, exactly as it was measured.
//
// The renderer positions against these rather than against a stylesheet of its
// own. That is the point: the numbers a line was wrapped against and the
// numbers it is drawn against are the same numbers.
type Metrics struct {
	MarginX      int `json:"margin_x"`
	MarginTop    int `json:"margin_top"`
	MarginBottom int `json:"margin_bottom"`

	ContentWidth int `json:"content_width"`
	BodyTop      int `json:"body_top"`
	BodyHeight   int `json:"body_height"`

	TitleBand  int `json:"title_band"`
	FooterBand int `json:"footer_band"`
	FooterTop  int `json:"footer_top"`

	TitleRuleWidth     int `json:"title_rule_width"`
	TitleRuleThickness int `json:"title_rule_thickness"`

	// Radius is the corner radius on a card, in pixels.
	Radius int `json:"radius"`

	// Spacing is the vertical rhythm, in pixels. Named rather than numbered so
	// a component asks for a gap by role.
	SpacingSM int `json:"spacing_sm"`
	SpacingMD int `json:"spacing_md"`
	SpacingLG int `json:"spacing_lg"`

	// Leading is the multiple of the font size one line occupies. It is a
	// float because it is a ratio, and it is the only float here.
	Leading float64 `json:"leading"`

	Type TypeScale `json:"type"`
}

// TypeScale is the type scale in whole pixels — the print scale at surface
// scale at 2 px per point, so every size lands on a whole pixel by
// construction.
type TypeScale struct {
	Display int `json:"display"`
	H1      int `json:"h1"`
	H2      int `json:"h2"`
	Body    int `json:"body"`
	Caption int `json:"caption"`
}

// Brand is the tenant's identity, flattened.
//
// Colours are `#RRGGBB` and the logo is a data URI, because the renderer has no
// network (locked decision 4) and no theme package. Nothing here is nullable:
// an unset accent is the token's own value, resolved before it got here.
type Brand struct {
	Name string `json:"name"`

	// Primary is the accent on light surfaces; PrimaryOnDark is the same hue
	// lifted far enough to stay legible on the near-black cover, divider and
	// closing scenes. Two fields rather than one because the deck learned this
	// the hard way: a navy that is perfect on paper is invisible on #0A0A0A,
	// and rejecting the navy would be fixing the wrong end.
	Primary       string `json:"primary"`
	PrimaryOnDark string `json:"primary_on_dark"`

	Foreground string `json:"foreground"`
	Background string `json:"background"`
	Muted      string `json:"muted"`
	Border     string `json:"border"`
	Dark       string `json:"dark"`
	OnDark     string `json:"on_dark"`

	// Surface and SurfaceSubtle are the card and the table's header band.
	Surface       string `json:"surface"`
	SurfaceSubtle string `json:"surface_subtle"`

	// Positive and Destructive colour a KPI delta and a callout. They are here
	// rather than in the renderer's stylesheet for the same reason everything
	// else is: **the renderer holds no palette at all.** A colour literal in
	// packages/motion is therefore always a defect, which is a rule a grep can
	// enforce — where "does this hex match the token?" is a rule only a human
	// can.
	Positive    string `json:"positive"`
	Destructive string `json:"destructive"`

	// Tones are the callout fills, keyed by spec.Tone* — info, warn, good.
	Tones map[string]Tone `json:"tones"`

	// LogoDataURI is empty when the tenant has no usable mark. LogoAspect is
	// its width ÷ height, so the renderer places it by height without decoding
	// the image.
	LogoDataURI string  `json:"logo_data_uri,omitempty"`
	LogoAspect  float64 `json:"logo_aspect,omitempty"`

	// Credit is the "Made with Argentum" line, or empty when the tenant has
	// switched it off. Confidentiality and FooterNote are theirs.
	Credit          string `json:"credit,omitempty"`
	Confidentiality string `json:"confidentiality,omitempty"`
	FooterNote      string `json:"footer_note,omitempty"`
}

// Tone is one callout's colours: the rule down its edge and the ground behind
// it. Two fields because a tone that is only a fill is invisible on a coloured
// ground and a tone that is only a rule is a hairline nobody notices.
type Tone struct {
	Accent string `json:"accent"`
	Fill   string `json:"fill"`
}

// Scene is one beat of the video.
//
// Every payload field for every kind lives on this one struct rather than in a
// union, for the same reason spec.Section does: the shapes are small, the
// dispatch is one switch, and a renderer that reads a field another kind wrote
// draws nothing rather than failing.
type Scene struct {
	Kind string `json:"kind"`

	// Frames is how long this scene is on screen. Always ≥ 1.
	Frames int `json:"frames"`

	// Title and Subtitle are pre-wrapped: each element is one drawn line.
	Title    []string `json:"title,omitempty"`
	Subtitle []string `json:"subtitle,omitempty"`

	// Period is the cover's date range, already formatted.
	Period string `json:"period,omitempty"`

	// Lines is the scene's body copy, pre-wrapped. A statement scene carries
	// the lead of one paragraph; a quote scene carries the callout's text.
	Lines []string `json:"lines,omitempty"`

	// Facts are the cover's and closing scene's label/value pairs, and a
	// key_value block's rows.
	Facts []Fact `json:"facts,omitempty"`

	KPIs  []KPI  `json:"kpis,omitempty"`
	Table *Table `json:"table,omitempty"`
	Chart *Chart `json:"chart,omitempty"`

	// Tone is a quote scene's callout tone: info, warn or good.
	Tone string `json:"tone,omitempty"`

	// Caption sits under a table or a chart, pre-wrapped.
	Caption []string `json:"caption,omitempty"`

	// Notes is the prose this scene is the headline of, whole and unwrapped.
	// The video never draws it — T-V4's player shows it beside the frame, which
	// is where the deck's speaker notes went and the same content.
	Notes string `json:"notes,omitempty"`

	// Continued marks a scene carrying the overflow of the one before it. The
	// renderer draws the marker; this says whether to.
	Continued bool `json:"continued,omitempty"`

	// Alt is the scene described in words, for a still that will be published
	// as an image: Instagram takes alt text per child, and a screen reader is
	// the only reader a slide has once it is a JPEG. Built from the scene's own
	// final strings — the title, then the cards, the facts or the caption — so
	// it says what the slide says in the language the slide says it, and the
	// model is never asked to write it. Capped at 1000 characters, the
	// platform's limit. Empty on a video plan.
	Alt string `json:"alt,omitempty"`
}

// Fact is a label and a value, both final.
type Fact struct {
	Label string   `json:"label"`
	Value []string `json:"value"`
}

// KPI is one card.
type KPI struct {
	Label string `json:"label"`

	// Value is formatted — currency symbol, separators, decimals, all decided.
	Value string `json:"value"`

	// Delta is the period-over-period change as a signed percentage string, or
	// empty. Rising says which way the arrow points and Good says what colour
	// it is: churn going up is not good news, and a renderer that colours every
	// rise green is telling the reader something false.
	Delta  string `json:"delta,omitempty"`
	Rising bool   `json:"rising,omitempty"`
	Good   bool   `json:"good,omitempty"`
}

// Table is a table resolved to strings and pixel widths.
type Table struct {
	Header []string   `json:"header"`
	Aligns []string   `json:"aligns"`
	Widths []int      `json:"widths"`
	Rows   [][]string `json:"rows"`
	Total  []string   `json:"total,omitempty"`

	// FontSize, RowHeight and HeaderHeight are pixels. The renderer draws the
	// grid these describe; it does not measure a cell.
	FontSize     int `json:"font_size"`
	RowHeight    int `json:"row_height"`
	HeaderHeight int `json:"header_height"`
}

// Chart is a rendered chart image and the box it occupies.
type Chart struct {
	// DataURI is the PNG internal/report/chart drew — the same image the PDF
	// embeds for the same spec.
	DataURI string `json:"data_uri"`

	Width  int `json:"width"`
	Height int `json:"height"`

	// Reveal is how the mask over the image moves. See the Reveal* constants.
	Reveal string `json:"reveal"`
}
