package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/docgen"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// GenerateDocumentTool turns a structured spec into a downloadable PDF /
// XLSX / CSV. The artifact is generic-purpose: invoices, agreements,
// T&Cs, research summaries, exports — anything the agent needs to hand
// the user as a file. The bytes are uploaded to MinIO/S3 under a
// tenant-scoped key and the tool returns a presigned download URL the
// chat UI can render as an attachment.
//
// Since T-A2 the work happens in docgen.Service, which `POST /v1/reports`
// also calls. What is left here is the agent's half: the tool description the
// model reads, the parameter schema, the thread requirement, and the JSON
// shape the agent gets back. Any divergence between what this produces and
// what the API produces would be a bug, and there is now nowhere for one to
// live.
type GenerateDocumentTool struct {
	docs *docgen.Service
}

// NewGenerateDocumentTool wires the tool to the shared generator.
func NewGenerateDocumentTool(docs *docgen.Service) *GenerateDocumentTool {
	return &GenerateDocumentTool{docs: docs}
}

func (t *GenerateDocumentTool) Name() string { return "generate_document" }

func (t *GenerateDocumentTool) Description() string {
	return strings.TrimSpace(`
Generate a downloadable document (PDF, PPTX, XLSX, or CSV) from a structured spec and return a presigned download URL. Generic-purpose: use for invoices, agreements, terms & conditions, research summaries, data exports, ad-hoc reports, slide decks — anything the user wants to download as a file.

Pick the format that matches the request:
- "pdf"  for invoices, agreements, reports, anything print-ready or meant to be forwarded (use content.sections; optionally a single content.table).
- "pptx" for anything that will be presented — reviews, board updates, weekly readouts, "walk me through it", "for the meeting". Same content.sections as a PDF; the renderer projects them onto slides.
- "xlsx" for spreadsheets (use content.table for a single sheet, content.sheets for multi-sheet).
- "csv"  for raw tabular data (must use content.table).

SET "spec_version": 2 FOR ANY PDF OR PPTX. It turns on the branded layout: cover page, running header, "Page N of M" footer, numbered headings, KPI cards, and typed table cells. Without it you get a plain document.

WRITING FOR A DECK: author it exactly as you would a PDF — do not shorten the prose, and do not split content into "slide" sections. The renderer decides where the slides break. Each "paragraph" becomes a slide's speaker notes with its opening sentence as the bullet, so a full explanatory paragraph produces a better deck than a terse one. A table longer than ~12 rows continues onto further slides automatically.

content.sections is an ordered list. Each section has a "type":
- "cover"      {text, subtitle, period, prepared_for, prepared_by, confidentiality} → full cover page. One per document, first in the list.
- "heading"    {text, level: 1|2}            → numbered section heading
- "paragraph"  {text}                        → justified body copy
- "kpi_row"    {items:[{label, value, delta_pct, higher_is_better}, ...]} → 2-4 headline-number cards. delta_pct is a percentage (12.5 = +12.5%). Set higher_is_better:false for metrics where a rise is bad (churn, cost).
- "table"      {columns:[...], rows:[[...]], total_row:[...], caption} → ruled table with zebra bands
- "chart"      {chart:{...}, caption}        → a chart image. See CHARTS below.
- "callout"    {tone: info|warn|good, title, text} → tinted box for a caveat or a headline finding
- "key_value"  {items:[{k,v}, ...]}          → label/value rows (invoice and agreement headers)
- "footnote"   {text}                        → small muted source/methodology line
- "page_break" {}                            → start a new page

CHARTS: put a chart above the table it summarises. A report with a trend in it and no chart is a table with a cover page.
  {"type":"chart","caption":"Source: fact_sales.","chart":{
     "type":"bar","title":"Revenue by month","fmt":"currency",
     "labels":["Jan","Feb","Mar"],
     "series":[{"name":"Direct","values":[412000000,448000000,391000000]}]}}
chart.type is one of:
- "line"         a measure over time. Several series compare trends.
- "bar"          one measure across categories or periods.
- "grouped_bar"  two to four series compared category by category.
- "stacked_bar"  parts that add up to a total per category.
- "pie" / "donut" one series' share of a whole. Up to ~6 slices; beyond that use a bar.
- "sparkline"    a bare trend line with no axes, for a KPI card.
RULES: every series needs one value per label — a mismatch is rejected, not padded. Pass raw numbers, never pre-formatted strings; "fmt" (text|number|currency|percent) formats the axis and the labels in the document's locale. Only "sparkline" may omit labels. A pie or donut takes exactly one series, with "labels" naming the slices. Set "height_mm" only when you have a reason; the default is proportioned for the page.
Series are capped at 8 and categories at 40; above that the renderer keeps the largest and says so in the caption. A chart with no data renders a "no data" panel rather than a blank space.

TABLE CELLS: pass raw values and let the renderer format them. A column may declare its type once:
  "columns": [{"label":"Month","fmt":"date"}, {"label":"Revenue","fmt":"currency"}, {"label":"Growth","fmt":"percent"}]
  "rows": [["2026-05-01", 3863405700, 12.5], ...]
fmt is one of text|number|currency|percent|date. A single cell can override with {"v": 1234, "fmt": "currency"}.
DO NOT pre-format numbers into strings like "Rp 3.863.405.700" or "12.5%" — the renderer applies the tenant's currency, separators and decimal places, and does it consistently down the whole column. Pass 3863405700 and 12.5.
Set "locale": "id" or "en" and "currency" (ISO 4217, e.g. "IDR") when the document's language or currency differs from the company default.

Keep tables under ~8 columns; wider than that does not read on A4.

Returns JSON with download_url (presigned, expires after ~1 hour). Embed the URL as a markdown link, e.g. [Download invoice.pdf](url). Never show the raw URL alone.`)
}

func (t *GenerateDocumentTool) Parameters() map[string]interfaces.ParameterSpec {
	return map[string]interfaces.ParameterSpec{
		"format": {
			Type:        "string",
			Description: "Output format: pdf, pptx, xlsx, or csv. Use pptx for anything that will be presented.",
			Required:    true,
			Enum:        []interface{}{"pdf", "pptx", "xlsx", "csv"},
		},
		"filename": {
			Type:        "string",
			Description: "Suggested filename (with extension). Optional — a default is generated if omitted.",
			Required:    false,
		},
		"title": {
			Type:        "string",
			Description: "Document title shown at the top of the PDF / used as default sheet name in XLSX.",
			Required:    false,
		},
		"content": {
			Type:        "object",
			Description: "Document body. One of: {table:{columns,rows}}, {sections:[...]}, or {sheets:[{name,columns,rows}, ...]}. See tool description for shapes.",
			Required:    true,
		},
		"spec_version": {
			Type:        "integer",
			Description: "Set to 2 for any PDF or PPTX: enables the branded layout (cover, running header, page numbers, KPI cards, typed table cells). Omit for the plain legacy layout.",
			Required:    false,
			Enum:        []interface{}{1, 2},
		},
		"locale": {
			Type:        "string",
			Description: "Number, date and currency conventions: \"id\" (1.234.567,89 · Rp · 27 Juli 2026) or \"en\" (1,234,567.89 · $ · 27 July 2026). Defaults to the company's currency convention.",
			Required:    false,
			Enum:        []interface{}{"id", "en"},
		},
		"currency": {
			Type:        "string",
			Description: "ISO 4217 code for currency-formatted cells, e.g. IDR or USD. Defaults to the company's configured currency.",
			Required:    false,
		},
		"meta": {
			Type:        "object",
			Description: "PDF document properties: {author, subject, keywords}. Optional — author defaults to the company name.",
			Required:    false,
		},
	}
}

func (t *GenerateDocumentTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

func (t *GenerateDocumentTool) Execute(ctx context.Context, args string) (string, error) {
	// One decode for both spec versions: spec.Column and spec.Cell unmarshal
	// from the v1 shapes as well as the v2 ones, so there is no branch here
	// and no second parser to keep in step with the first.
	var input spec.Document
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return "", fmt.Errorf("parse parameters: %w", err)
	}

	companyID := tenantctx.CompanyID(ctx)
	if companyID == "" {
		return "", fmt.Errorf("no tenant in context: cannot generate document")
	}
	// The thread requirement stays on the agent path and did not move into the
	// service with everything else (T-A2). A turn without a thread is a bug in
	// the worker's wiring and should fail loudly here; `/v1`'s render door
	// legitimately has none, and a shared check would have had to be satisfied
	// with a fake one.
	threadID := tenantctx.ThreadID(ctx)
	if threadID == "" {
		return "", fmt.Errorf("no thread in context: cannot generate document")
	}

	// Provenance comes off the context, not off a parameter (T-A2).
	//
	// A document produced by a turn that arrived through `POST /v1/reports` is
	// an API document with a thread, and the tool cannot be told so by the
	// caller — the caller is the model, four packages and a queue away from the
	// HTTP request. The actor is already there for T-05's audit rows, which
	// record `actor_kind=api_key` for exactly this turn, so the two now agree
	// about who produced a document rather than disagreeing quietly.
	source, keyID := domain.DocumentSourceAgent, ""
	if kind, ref := tenantctx.Actor(ctx); kind == string(domain.ActorKindAPIKey) {
		source, keyID = domain.DocumentSourceAPI, ref
	}

	res, err := t.docs.Generate(ctx, docgen.Input{
		Spec:      &input,
		CompanyID: companyID,
		ThreadID:  threadID,
		Source:    source,
		APIKeyID:  keyID,
		// The agent's own spec is not checked against the API's caps. It comes
		// from a model on the other side of a tool description that already
		// asks for small tables, and a turn refused by a row cap the agent
		// cannot see is a turn that fails with nothing to act on.
		EnforceLimits: false,
	})
	if err != nil {
		return "", err
	}

	out, _ := json.Marshal(map[string]interface{}{
		"document_id":  res.Document.ID,
		"format":       input.Format,
		"filename":     res.Document.Filename,
		"download_url": res.DownloadURL,
		"expires_at":   res.ExpiresAt.UTC().Format(time.RFC3339),
		"size_bytes":   res.Document.SizeBytes,
		"note":         "Embed download_url as a markdown link with descriptive text, e.g. [Download " + res.Document.Filename + "](download_url). The link expires after about an hour.",
	})
	return string(out), nil
}
