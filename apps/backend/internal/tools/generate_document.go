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
	recorder   UsageRecorder
	presignTTL time.Duration
}

func NewGenerateDocumentTool(
	st *storage.StorageService,
	repo domain.DocumentRepository,
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
		recorder:   recorder,
		presignTTL: presignTTL,
	}
}

func (t *GenerateDocumentTool) Name() string { return "generate_document" }

func (t *GenerateDocumentTool) Description() string {
	return strings.TrimSpace(`
Generate a downloadable document (PDF, XLSX, or CSV) from a structured spec and return a presigned download URL. Generic-purpose: use for invoices, agreements, terms & conditions, research summaries, data exports, ad-hoc reports — anything the user wants to download as a file.

Pick the format that matches the request:
- "pdf"  for invoices, agreements, narrative documents, anything print-ready (use content.sections; optionally a single content.table).
- "xlsx" for spreadsheets (use content.table for a single sheet, content.sheets for multi-sheet).
- "csv"  for raw tabular data (must use content.table).

content.sections is an ordered list. Each section has a "type":
- "heading"     {text}                       → section title
- "paragraph"   {text}                       → wrapped paragraph
- "key_value"   {items:[{k,v}, ...]}         → label/value rows (great for invoice / agreement headers)
- "table"       {columns:[...], rows:[[...]]} → bordered table
- "spacer"      {size}                       → vertical gap in mm

Always pass row cells as strings; format numbers/dates yourself before calling. Keep tables under ~8 columns for readability in PDF.

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
	}
}

func (t *GenerateDocumentTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

func (t *GenerateDocumentTool) Execute(ctx context.Context, args string) (string, error) {
	var spec document.Spec
	if err := json.Unmarshal([]byte(args), &spec); err != nil {
		return "", fmt.Errorf("parse parameters: %w", err)
	}
	spec.Format = strings.ToLower(strings.TrimSpace(spec.Format))
	if err := spec.Validate(); err != nil {
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

	format := domain.DocumentFormat(spec.Format)
	data, err := renderForFormat(&spec)
	if err != nil {
		return "", err
	}

	// Generate id up-front so the storage key is deterministic and we
	// can upload before persisting metadata. On upload failure we leave
	// no orphan DB row.
	docID := uuid.New().String()
	storageKey := fmt.Sprintf("documents/%s/%s/%s.%s", companyID, threadID, docID, format.Extension())
	filename := normalizeFilename(spec.Filename, spec.Title, format)

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

	t.recorder.RecordDocument(ctx, companyID, threadID, spec.Format)

	expiresAt := time.Now().Add(t.presignTTL)
	signed, err := t.storage.PresignKey(ctx, storageKey, t.presignTTL)
	if err != nil {
		return "", fmt.Errorf("presign document: %w", err)
	}

	logrus.WithFields(logrus.Fields{
		"company_id":  companyID,
		"thread_id":   threadID,
		"document_id": doc.ID,
		"format":      spec.Format,
		"size":        doc.SizeBytes,
	}).Info("Generated document")

	out, _ := json.Marshal(map[string]interface{}{
		"document_id":  doc.ID,
		"format":       spec.Format,
		"filename":     filename,
		"download_url": signed,
		"expires_at":   expiresAt.UTC().Format(time.RFC3339),
		"size_bytes":   doc.SizeBytes,
		"note":         "Embed download_url as a markdown link with descriptive text, e.g. [Download " + filename + "](download_url). The link expires after about an hour.",
	})
	return string(out), nil
}

func renderForFormat(spec *document.Spec) ([]byte, error) {
	switch spec.Format {
	case "pdf":
		return document.RenderPDF(spec)
	case "xlsx":
		return document.RenderXLSX(spec)
	case "csv":
		return document.RenderCSV(spec)
	}
	return nil, fmt.Errorf("unsupported format %q", spec.Format)
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
