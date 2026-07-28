package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/adapters/storage"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/report/pdf"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/tenantctx"
	"github.com/fauzanebd/argentum/internal/tools/document"
)

// GenerateDocumentTool turns a structured spec into a downloadable PDF /
// XLSX / CSV. The artifact is generic-purpose: invoices, agreements,
// T&Cs, research summaries, exports — anything the agent needs to hand
// the user as a file. The bytes are uploaded to MinIO/S3 under a
// tenant-scoped key and the tool returns a presigned download URL the
// chat UI can render as an attachment.
type GenerateDocumentTool struct {
	storage    *storage.StorageService
	repo       domain.DocumentRepository
	companies  domain.CompanyRepository
	recorder   UsageRecorder
	presignTTL time.Duration
}

func NewGenerateDocumentTool(
	st *storage.StorageService,
	repo domain.DocumentRepository,
	companies domain.CompanyRepository,
	recorder UsageRecorder,
	presignTTL time.Duration,
) *GenerateDocumentTool {
	if recorder == nil {
		recorder = nopRecorder{}
	}
	if presignTTL <= 0 {
		presignTTL = time.Hour
	}
	return &GenerateDocumentTool{
		storage:    st,
		repo:       repo,
		companies:  companies,
		recorder:   recorder,
		presignTTL: presignTTL,
	}
}

func (t *GenerateDocumentTool) Name() string { return "generate_document" }

func (t *GenerateDocumentTool) Description() string {
	return strings.TrimSpace(`
Generate a downloadable document (PDF, XLSX, or CSV) from a structured spec and return a presigned download URL. Generic-purpose: use for invoices, agreements, terms & conditions, research summaries, data exports, ad-hoc reports — anything the user wants to download as a file.

Pick the format that matches the request:
- "pdf"  for invoices, agreements, reports, anything print-ready (use content.sections; optionally a single content.table).
- "xlsx" for spreadsheets (use content.table for a single sheet, content.sheets for multi-sheet).
- "csv"  for raw tabular data (must use content.table).

SET "spec_version": 2 FOR ANY PDF. It turns on the branded layout: cover page, running header, "Page N of M" footer, numbered headings, KPI cards, and typed table cells. Without it you get a plain document.

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
			Description: "Output format: pdf, xlsx, or csv.",
			Required:    true,
			Enum:        []interface{}{"pdf", "xlsx", "csv"},
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
			Description: "Set to 2 for any PDF: enables the branded layout (cover, running header, page numbers, KPI cards, typed table cells). Omit for the plain legacy layout.",
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
	input.Normalize()
	if err := input.Validate(); err != nil {
		return "", err
	}

	companyID := tenantctx.CompanyID(ctx)
	if companyID == "" {
		return "", fmt.Errorf("no tenant in context: cannot generate document")
	}
	threadID := tenantctx.ThreadID(ctx)
	if threadID == "" {
		return "", fmt.Errorf("no thread in context: cannot generate document")
	}

	format := domain.DocumentFormat(input.Format)
	data, err := t.render(ctx, &input, companyID)
	if err != nil {
		return "", err
	}

	// Generate id up-front so the storage key is deterministic and we
	// can upload before persisting metadata. On upload failure we leave
	// no orphan DB row.
	docID := uuid.New().String()
	storageKey := fmt.Sprintf("documents/%s/%s/%s.%s", companyID, threadID, docID, format.Extension())
	filename := normalizeFilename(input.Filename, input.Title, format)

	if _, err := t.storage.UploadKey(ctx, storageKey, bytes.NewReader(data), format.ContentType()); err != nil {
		return "", fmt.Errorf("upload document: %w", err)
	}

	doc := &domain.Document{
		ID:         docID,
		CompanyID:  companyID,
		ThreadID:   threadID,
		Format:     format,
		Filename:   filename,
		StorageKey: storageKey,
		SizeBytes:  int64(len(data)),
	}
	if err := t.repo.Insert(ctx, doc); err != nil {
		return "", fmt.Errorf("persist document: %w", err)
	}

	t.recorder.RecordDocument(ctx, companyID, threadID, input.Format)

	expiresAt := time.Now().Add(t.presignTTL)
	signed, err := t.storage.PresignKey(ctx, storageKey, t.presignTTL)
	if err != nil {
		return "", fmt.Errorf("presign document: %w", err)
	}

	logrus.WithFields(logrus.Fields{
		"company_id":  companyID,
		"thread_id":   threadID,
		"document_id": doc.ID,
		"format":      input.Format,
		"size":        doc.SizeBytes,
	}).Info("Generated document")

	out, _ := json.Marshal(map[string]interface{}{
		"document_id":  doc.ID,
		"format":       input.Format,
		"filename":     filename,
		"download_url": signed,
		"expires_at":   expiresAt.UTC().Format(time.RFC3339),
		"size_bytes":   doc.SizeBytes,
		"note":         "Embed download_url as a markdown link with descriptive text, e.g. [Download " + filename + "](download_url). The link expires after about an hour.",
	})
	return string(out), nil
}

// render dispatches to the format's renderer.
//
// The PDF path is the only one that reads company settings. A spreadsheet and
// a CSV are data, and a tenant's currency symbol pasted into a cell someone
// wants to sum is a formatting decision made in the wrong place.
func (t *GenerateDocumentTool) render(ctx context.Context, doc *spec.Document, companyID string) ([]byte, error) {
	switch doc.Format {
	case "pdf":
		return pdf.Render(doc, t.pdfOptions(ctx, companyID))
	case "xlsx":
		return document.RenderXLSX(document.FromReportSpec(doc))
	case "csv":
		return document.RenderCSV(document.FromReportSpec(doc))
	}
	return nil, fmt.Errorf("unsupported format %q", doc.Format)
}

// pdfOptions fills in what the model does not know: the tenant's legal name
// and its default currency. A failed lookup is logged and not fatal — a
// document with Argentum's own mark on it beats an error where a report was
// asked for, and T-R5 is what turns this into real branding.
func (t *GenerateDocumentTool) pdfOptions(ctx context.Context, companyID string) pdf.Options {
	opts := pdf.Options{}
	if t.companies == nil {
		return opts
	}
	company, err := t.companies.GetByID(ctx, companyID)
	if err != nil || company == nil {
		if err != nil {
			logrus.WithError(err).WithField("company_id", companyID).
				Warn("generate_document: company lookup failed; rendering with defaults")
		}
		return opts
	}
	opts.Brand.Name = company.Name
	opts.Currency = company.DefaultCurrency
	return opts
}

func normalizeFilename(suggested, title string, format domain.DocumentFormat) string {
	ext := "." + format.Extension()
	name := strings.TrimSpace(suggested)
	if name == "" {
		base := strings.TrimSpace(title)
		if base == "" {
			base = "document"
		}
		name = base + "-" + time.Now().UTC().Format("20060102-150405")
	}
	if !strings.EqualFold(filepathExt(name), ext) {
		if i := strings.LastIndexByte(name, '.'); i > 0 {
			name = name[:i]
		}
		name += ext
	}
	repl := strings.NewReplacer("/", "_", "\\", "_", "\x00", "_")
	return repl.Replace(name)
}

func filepathExt(name string) string {
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return ""
	}
	return name[i:]
}
