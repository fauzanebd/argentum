// Package docgen is the one path from a report spec to a stored, metered
// document.
//
// **Why it is not in `internal/app`, which is where the ticket put it.**
// `internal/app` already depends on `internal/tools`, so a service living
// there could not be called by `GenerateDocumentTool` without an import cycle
// — the same constraint that put `tools.UsageRecorder` in `run_sql.go` rather
// than beside the other interfaces. The requirement the ticket was actually
// making is that there be exactly one implementation, and a package both
// callers can import is how that holds. `/v1`'s handlers and the agent's tool
// call the same `Generate`, so a divergence between the API surface and the
// agent surface is not a bug that can be introduced by editing one of them.
//
// The split of responsibility with `internal/report`: the renderers are pure
// `(*spec.Document) ([]byte, error)`. Everything around them — resolving the
// tenant's branding and currency, bounding an untrusted spec, choosing a
// storage key, uploading, persisting the row, metering the spend — is here,
// and was previously inlined in the tool where the API could not reach it.
package docgen

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/branding"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/report/brand"
	"github.com/fauzanebd/argentum/internal/report/pdf"
	"github.com/fauzanebd/argentum/internal/report/pptx"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/tools/document"
)

// ObjectStore is the slice of the storage adapter this package needs.
// Declared at the consumer and narrow, like branding.ObjectStore, so the
// service is testable without MinIO.
type ObjectStore interface {
	UploadKey(ctx context.Context, key string, reader io.Reader, contentType string) (string, error)
	PresignKey(ctx context.Context, key string, expiry time.Duration) (string, error)
}

// Meter is the metering half. It matches tools.UsageRecorder's document
// method so the same *app.UsageService satisfies both without an adapter.
//
// It returns nothing, and that is deliberate: a document that rendered,
// uploaded and persisted has been delivered, and failing the call because the
// usage row did not append would refuse the caller something they already
// have.
type Meter interface {
	RecordDocument(ctx context.Context, companyID, threadID, format string)
}

type nopMeter struct{}

func (nopMeter) RecordDocument(context.Context, string, string, string) {}

// Service generates documents. One instance is shared by the worker's
// generate_document tool and by the API's `/v1` report routes.
type Service struct {
	storage    ObjectStore
	repo       domain.DocumentRepository
	companies  domain.CompanyRepository
	branding   *branding.Service
	meter      Meter
	presignTTL time.Duration
	limits     spec.Limits
}

// New wires the service. branding may be nil — a deployment without object
// storage resolves to Argentum's defaults, which is what every document looked
// like before tenant branding existed. meter may be nil.
func New(
	store ObjectStore,
	repo domain.DocumentRepository,
	companies domain.CompanyRepository,
	brandingSvc *branding.Service,
	meter Meter,
	presignTTL time.Duration,
) *Service {
	if meter == nil {
		meter = nopMeter{}
	}
	if presignTTL <= 0 {
		presignTTL = time.Hour
	}
	return &Service{
		storage:    store,
		repo:       repo,
		companies:  companies,
		branding:   brandingSvc,
		meter:      meter,
		presignTTL: presignTTL,
		limits:     spec.DefaultLimits,
	}
}

// WithLimits installs the caps an untrusted spec is checked against. The
// agent's own path leaves them at the defaults; `/v1` sets them from config.
func (s *Service) WithLimits(l spec.Limits) *Service {
	s.limits = l.Normalize()
	return s
}

// PresignTTL is how long a download URL this service issues stays valid.
// Handlers report it to the caller, so they read it from here rather than
// re-deriving it from config and drifting.
func (s *Service) PresignTTL() time.Duration { return s.presignTTL }

// Input is one generation request.
type Input struct {
	// Spec is normalised and validated by Generate; callers hand over what
	// they parsed.
	Spec *spec.Document
	// CompanyID is required. Everything below it is optional and says which
	// door this arrived through.
	CompanyID string
	// ThreadID is empty only for `POST /v1/reports/render`, the one path with
	// no conversation behind it. The agent's tool requires one before it calls
	// here.
	ThreadID  string
	MessageID string
	Source    domain.DocumentSource
	APIKeyID  string
	// EnforceLimits bounds a spec that arrived over HTTP. The agent's own spec
	// skips it: it comes from a model on the other side of a tool description
	// that already asks for small tables, and a turn refused by a row cap the
	// agent cannot see is a turn that fails with nothing to act on.
	EnforceLimits bool
}

// Result is a generated document and the bytes behind it.
//
// The bytes are returned rather than re-read from storage because
// `POST /v1/reports/render` with `Accept: application/pdf` answers with them
// inline: a round trip through object storage to hand back what is already in
// memory is latency bought for nothing.
type Result struct {
	Document *domain.Document
	Data     []byte
	// DownloadURL is presigned and expires after PresignTTL.
	DownloadURL string
	ExpiresAt   time.Time
}

// Generate renders, uploads, persists and meters one document.
//
// The order is not arbitrary. The id is generated up front so the storage key
// is deterministic and the upload can happen before the row exists — an upload
// that fails then leaves no orphan row pointing at an object that is not
// there. The reverse order leaves a document a caller can list and cannot
// download, which is the worse of the two failures.
func (s *Service) Generate(ctx context.Context, in Input) (*Result, error) {
	if in.Spec == nil {
		return nil, fmt.Errorf("%w: no document spec", domain.ErrInvalidInput)
	}
	if in.CompanyID == "" {
		return nil, fmt.Errorf("no tenant in context: cannot generate document")
	}

	in.Spec.Normalize()
	if err := in.Spec.Validate(); err != nil {
		return nil, err
	}
	if in.EnforceLimits {
		// Before render, not during it: a limit enforced inside the renderer
		// is a limit that has already spent the memory it exists to protect.
		if err := spec.CheckLimits(in.Spec, s.limits); err != nil {
			return nil, err
		}
	}

	format := domain.DocumentFormat(in.Spec.Format)
	data, err := s.render(ctx, in.Spec, in.CompanyID)
	if err != nil {
		return nil, err
	}

	docID := uuid.New().String()
	storageKey := s.storageKey(in.CompanyID, in.ThreadID, docID, format)
	filename := NormalizeFilename(in.Spec.Filename, in.Spec.Title, format)

	if _, err := s.storage.UploadKey(ctx, storageKey, bytes.NewReader(data), format.ContentType()); err != nil {
		return nil, fmt.Errorf("upload document: %w", err)
	}

	source := in.Source
	if source == "" {
		source = domain.DocumentSourceAgent
	}
	doc := &domain.Document{
		ID:         docID,
		CompanyID:  in.CompanyID,
		ThreadID:   in.ThreadID,
		MessageID:  in.MessageID,
		Format:     format,
		Filename:   filename,
		StorageKey: storageKey,
		SizeBytes:  int64(len(data)),
		Source:     source,
		APIKeyID:   in.APIKeyID,
	}
	if err := s.repo.Insert(ctx, doc); err != nil {
		return nil, fmt.Errorf("persist document: %w", err)
	}

	s.meter.RecordDocument(ctx, in.CompanyID, in.ThreadID, in.Spec.Format)

	signed, expiresAt, err := s.Presign(ctx, doc)
	if err != nil {
		return nil, err
	}

	logrus.WithFields(logrus.Fields{
		"company_id":  in.CompanyID,
		"thread_id":   in.ThreadID,
		"document_id": doc.ID,
		"format":      in.Spec.Format,
		"source":      source,
		"rows":        spec.TotalRows(in.Spec),
		"size":        doc.SizeBytes,
	}).Info("Generated document")

	return &Result{Document: doc, Data: data, DownloadURL: signed, ExpiresAt: expiresAt}, nil
}

// Presign issues a fresh download URL for an existing document.
//
// `GET /v1/documents/:id` calls this on every read rather than storing a URL,
// because a presigned URL expires and an integrator who saved one must be able
// to get a working link back without paying to regenerate the document.
func (s *Service) Presign(ctx context.Context, doc *domain.Document) (string, time.Time, error) {
	expiresAt := time.Now().Add(s.presignTTL)
	signed, err := s.storage.PresignKey(ctx, doc.StorageKey, s.presignTTL)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("presign document: %w", err)
	}
	return signed, expiresAt, nil
}

// storageKey picks where the object lives.
//
// One branch, not a new scheme for everything: the threaded key is what every
// document written before T-A2 uses, and rewriting it would strand them. The
// render door has no thread, so it gets `api` in that position — a literal
// rather than an empty segment, because `documents/{company}//{id}.pdf` is a
// key that works and reads like a bug forever after.
func (s *Service) storageKey(companyID, threadID, docID string, format domain.DocumentFormat) string {
	if threadID == "" {
		return fmt.Sprintf("documents/%s/api/%s.%s", companyID, docID, format.Extension())
	}
	return fmt.Sprintf("documents/%s/%s/%s.%s", companyID, threadID, docID, format.Extension())
}

// render dispatches to the format's renderer.
//
// The PDF and PPTX paths are the only ones that read company settings. A
// spreadsheet and a CSV are data, and a tenant's currency symbol pasted into a
// cell someone wants to sum is a formatting decision made in the wrong place.
func (s *Service) render(ctx context.Context, doc *spec.Document, companyID string) ([]byte, error) {
	switch doc.Format {
	case "pdf":
		return pdf.Render(doc, s.pdfOptions(ctx, companyID))
	case "pptx":
		return pptx.Render(doc, s.pptxOptions(ctx, companyID))
	case "xlsx":
		return document.RenderXLSX(document.FromReportSpec(doc))
	case "csv":
		return document.RenderCSV(document.FromReportSpec(doc))
	}
	return nil, fmt.Errorf("unsupported format %q", doc.Format)
}

// currencyFor is the tenant's default currency, used when the spec names none.
// A failed lookup is logged and not fatal: a document with bare numbers beats
// an error where a report was asked for.
func (s *Service) currencyFor(ctx context.Context, companyID string) string {
	if s.companies == nil {
		return ""
	}
	company, err := s.companies.GetByID(ctx, companyID)
	if err != nil || company == nil {
		if err != nil {
			logrus.WithError(err).WithField("company_id", companyID).
				Warn("docgen: company lookup failed; rendering with defaults")
		}
		return ""
	}
	return company.DefaultCurrency
}

// brandFor resolves the tenant's report branding — logo, accent, footer line,
// locale (T-R5). It goes through the same branding.Service the dashboard's
// preview does, which is what makes "what I approved is what my customer
// receives" true rather than intended — and now covers the API's documents
// too, which is the point of there being one service.
func (s *Service) brandFor(ctx context.Context, companyID string) brand.Config {
	if s.branding == nil {
		return brand.Resolve(brand.Input{
			CompanyName: s.companyName(ctx, companyID),
			ShowCredit:  true,
		})
	}
	return s.branding.Resolve(ctx, companyID, func(err error) {
		logrus.WithError(err).WithField("company_id", companyID).
			Warn("docgen: branding partially unresolved; rendering the rest")
	})
}

func (s *Service) companyName(ctx context.Context, companyID string) string {
	if s.companies == nil {
		return ""
	}
	company, err := s.companies.GetByID(ctx, companyID)
	if err != nil || company == nil {
		return ""
	}
	return company.Name
}

func (s *Service) pdfOptions(ctx context.Context, companyID string) pdf.Options {
	cfg := s.brandFor(ctx, companyID)
	return pdf.Options{
		Brand:    cfg.PDF(),
		Currency: s.currencyFor(ctx, companyID),
		Locale:   cfg.Locale,
	}
}

// pptxOptions is pdfOptions for the deck. The two Options types are separate
// because the renderers are, and they read one resolved branding so a report
// and the deck attached to it cannot disagree about whose document it is.
func (s *Service) pptxOptions(ctx context.Context, companyID string) pptx.Options {
	cfg := s.brandFor(ctx, companyID)
	return pptx.Options{
		Brand:    cfg.PPTX(),
		Currency: s.currencyFor(ctx, companyID),
		Locale:   cfg.Locale,
	}
}

// NormalizeFilename produces a safe filename with the format's extension.
// Exported because the `/v1` handlers put it in a Content-Disposition header
// for the inline-bytes response, and a second implementation of the same
// sanitisation is a second thing to get wrong.
func NormalizeFilename(suggested, title string, format domain.DocumentFormat) string {
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

// AsLimitError extracts the typed limit failure from an error returned by
// Generate, so a handler can name the offending field in the envelope's
// `param` rather than pasting a sentence into it.
func AsLimitError(err error) (*spec.LimitError, bool) {
	var le *spec.LimitError
	if errors.As(err, &le) {
		return le, true
	}
	return nil, false
}
