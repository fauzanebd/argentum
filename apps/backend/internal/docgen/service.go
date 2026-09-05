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
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/actions"
	"github.com/fauzanebd/argentum/internal/branding"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/report/brand"
	"github.com/fauzanebd/argentum/internal/report/pdf"
	"github.com/fauzanebd/argentum/internal/report/pptx"
	"github.com/fauzanebd/argentum/internal/report/spec"
	"github.com/fauzanebd/argentum/internal/report/video"
	"github.com/fauzanebd/argentum/internal/report/videoplan"
	"github.com/fauzanebd/argentum/internal/tools/document"
)

// ObjectStore is the slice of the storage adapter this package needs.
// Declared at the consumer and narrow, like branding.ObjectStore, so the
// service is testable without MinIO.
type ObjectStore interface {
	UploadKey(ctx context.Context, key string, reader io.Reader, contentType string) (string, error)
	PresignKey(ctx context.Context, key string, expiry time.Duration) (string, error)
	// DownloadKey reads an object back. Added for T-V4: the player is served
	// the plan's bytes by our own handler rather than a presigned URL, because
	// a presigned URL cannot be revoked, cannot be counted, and outlives the
	// share it was minted for.
	DownloadKey(ctx context.Context, key string) ([]byte, error)
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
	// RecordRenderSeconds meters wall clock spent in the render service
	// (T-V3). A video is not a document-sized cost — a PDF is milliseconds of
	// this process and a video is minutes of another pod's CPU — so the
	// document event says one was produced and this says what it took. Two
	// events rather than a field on one, because the second only exists for the
	// formats that leave this process.
	RecordRenderSeconds(ctx context.Context, companyID, threadID, format string, seconds float64)
}

type nopMeter struct{}

func (nopMeter) RecordDocument(context.Context, string, string, string) {}

func (nopMeter) RecordRenderSeconds(context.Context, string, string, string, float64) {}

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
	// video is nil on a deployment with no render service, which is what makes
	// `mp4` unavailable rather than broken there.
	video *video.Client
	// videoLimits bounds what a spec may turn into as scenes and frames. They
	// are videoplan's rather than spec.Limits' because deriving them means
	// running the timing model, and `spec` cannot import `videoplan` — see
	// videoplan's own note on where the caps live.
	videoLimits videoplan.Limits
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

// WithVideo installs the render service client and the caps a video is bounded
// by (T-V3). A nil client is the supported state, not a broken one: `mp4` is
// then refused with a plain sentence and every other format is untouched.
func (s *Service) WithVideo(c *video.Client, l videoplan.Limits) *Service {
	s.video = c
	s.videoLimits = l.Normalize()
	return s
}

// VideoAvailable reports whether `mp4` can be produced here. Handlers and the
// agent's tool registry both ask, so neither re-derives it from config —
// `generate_document` narrows its own format enum on the answer, the same way
// it is registered at all only where object storage exists.
func (s *Service) VideoAvailable() bool { return s != nil && s.video.Configured() }

// Limits are the caps an untrusted spec is checked against. Exposed because
// the `/v1` render door checks a video's spec *before* it enqueues the job —
// the job is the thing that would otherwise spend minutes of a render pod on a
// spec that was never going to be accepted — and it must check against the
// same numbers Generate will.
func (s *Service) Limits() spec.Limits { return s.limits }

// CheckVideoLimits applies the scene and running-time caps a video is bounded
// by, for a door that has not queued anything yet.
//
// The comment above says the render door checks "before it enqueues the job".
// Until 2026-08-09 that was true only of spec.Limits — rows, columns, chart
// points — and the caps that decide whether a video can exist at all were
// applied in the worker, inside videoplan.Build. So an over-long spec was
// answered `202 queued` and failed a minute later, which is the shape the
// door's own comment exists to rule out.
//
// A non-video format is not checked: nothing about a PDF has a scene count,
// and returning nil here keeps the caller from having to ask what format it
// holds before it can validate.
func (s *Service) CheckVideoLimits(doc *spec.Document) error {
	if s == nil || doc == nil {
		return nil
	}
	switch domain.DocumentFormat(spec.FormatOf(doc)) {
	case domain.DocumentFormatMP4:
		return videoplan.CheckLimits(doc, videoplan.Options{Limits: s.videoLimits})
	case domain.DocumentFormatCarousel:
		// The carousel's own caps (T-G6): the slide band rather than scenes
		// and running time. Same door, same reason — an eleven-slide spec is
		// refused in milliseconds where the model can still shorten it, not in
		// a worker a minute later.
		return videoplan.CheckCarouselLimits(doc, videoplan.Options{Limits: s.videoLimits})
	}
	return nil
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
	// Images are the tenant's uploaded pictures, resolved by the caller and
	// keyed by image id (T-G12). The door resolves them — in the turn, where
	// the model can be told a picture was not found — and the bytes travel
	// from there, because this service has no picture library of its own and
	// the worker that renders is a process away from the turn that asked.
	Images map[string]videoplan.PromoImage
	// OnProgress reports 0..1 while a format that is rendered elsewhere is
	// being rendered (T-V3). Nil for every in-process format, because a PDF
	// finishes before a progress event would arrive. It is called from the
	// caller's goroutine and at most once a second — a four-minute silent
	// spinner is the failure a progress channel exists for, and a four-minute
	// firehose is the other one.
	OnProgress func(float64)
	// EnforceNarrative refuses an analytical report that only states figures.
	// It is the mirror image of EnforceLimits: this one is on for the agent and
	// off for the API. A model that has just run the queries is the only author
	// who can say what the numbers mean and the only one who will skip it, while
	// a spec posted to the render door was written by an integrator who is
	// entitled to a document with no prose in it. See spec.CheckNarrative.
	EnforceNarrative bool
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
	// Carousel is the manifest of a carousel — caption, hashtags, one alt text
	// per slide — and nil for every other format (T-G6). The announcement that
	// posts the slides into the thread is written from it.
	Carousel *CarouselManifest
}

// CarouselManifest is what travels with a carousel's pages: everything a
// publisher needs beside the images. It is stored beside them as
// `carousel.json` and inside the zip, so the download is self-describing.
type CarouselManifest struct {
	Caption  string   `json:"caption,omitempty"`
	Hashtags []string `json:"hashtags,omitempty"`
	// Alts is one alt text per page, in page order, from the plan's scenes.
	Alts  []string `json:"alts"`
	Pages int      `json:"pages"`
}

// rendered is what one format's renderer hands back: the document's bytes,
// the seconds it cost elsewhere, and — for a carousel — the pages that are
// stored beside the document rather than inside it.
type rendered struct {
	data    []byte
	seconds float64
	pages   [][]byte
	// manifest is non-nil only for a carousel.
	manifest *CarouselManifest
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
	if in.EnforceNarrative {
		// Also before render, and for a blunter reason: everything below this
		// line bills. A report refused here costs the caller nothing, while the
		// same report refused after the upload has already been metered.
		if err := spec.CheckNarrative(in.Spec); err != nil {
			return nil, fmt.Errorf("%w: %s", domain.ErrInvalidInput, err)
		}
	}

	format := domain.DocumentFormat(in.Spec.Format)
	out, err := s.render(ctx, in.Spec, in.CompanyID, in.Images, in.OnProgress)
	if err != nil {
		return nil, err
	}
	data, renderSeconds := out.data, out.seconds

	docID := uuid.New().String()
	storageKey := s.storageKey(in.CompanyID, in.ThreadID, docID, format)
	filename := NormalizeFilename(in.Spec.Filename, in.Spec.Title, format)

	if _, err := s.storage.UploadKey(ctx, storageKey, bytes.NewReader(data), format.ContentType()); err != nil {
		return nil, fmt.Errorf("upload document: %w", err)
	}
	// A carousel's pages and manifest sit under the document's own prefix
	// (T-G6), before the row exists, for the reason the zip does: a page that
	// fails to upload leaves no row promising a slide that is not there. Keys
	// under the prefix rather than a second scheme — PlanKey's argument — so a
	// deletion sweep written against `documents/{company}/` covers them.
	for i, page := range out.pages {
		if _, err := s.storage.UploadKey(ctx, PageKey(storageKey, i+1), bytes.NewReader(page), "image/jpeg"); err != nil {
			return nil, fmt.Errorf("upload page %d: %w", i+1, err)
		}
	}
	if out.manifest != nil {
		body, err := json.Marshal(out.manifest)
		if err != nil {
			return nil, fmt.Errorf("marshal manifest: %w", err)
		}
		if _, err := s.storage.UploadKey(ctx, ManifestKey(storageKey), bytes.NewReader(body), "application/json"); err != nil {
			return nil, fmt.Errorf("upload manifest: %w", err)
		}
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
		PageCount:  len(out.pages),
	}
	if err := s.repo.Insert(ctx, doc); err != nil {
		return nil, fmt.Errorf("persist document: %w", err)
	}

	// The plan, beside the document (T-V4). The player replays the
	// compositions client-side from this, never from the mp4, so a document
	// shared as a link plays without a video render having happened at all —
	// and a document that *was* rendered plays the identical scenes, because
	// this is the same projection the renderer was sent.
	//
	// After the row and never before: a plan is an enhancement to a document
	// that already exists, and a document that failed to store because its
	// plan did not would be a worse product than one that cannot be shared.
	s.storePlan(ctx, in.Spec, doc)

	s.meter.RecordDocument(ctx, in.CompanyID, in.ThreadID, in.Spec.Format)
	if renderSeconds > 0 {
		s.meter.RecordRenderSeconds(ctx, in.CompanyID, in.ThreadID, in.Spec.Format, renderSeconds)
	}

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

	return &Result{Document: doc, Data: data, DownloadURL: signed, ExpiresAt: expiresAt, Carousel: out.manifest}, nil
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

// LinkForDocument resolves a document to a link a recipient can open (T-V3).
//
// It is `actions.DocumentLinker`, satisfied here because this is already where
// a document becomes a URL — `GET /v1/documents/:id` re-presigns through the
// same `Presign`, and a second presigner would be a second answer to how long
// a link lasts.
//
// **Company-scoped by the query, not by a comparison afterwards.** An action
// executes in a worker on behalf of a tenant, and the id in its parameters came
// from a model: `GetForCompany` is what makes another tenant's document a
// not-found rather than a fetch followed by a check somebody can forget.
func (s *Service) LinkForDocument(ctx context.Context, companyID, documentID string) (actions.Attachment, error) {
	if s == nil || s.repo == nil {
		return actions.Attachment{}, fmt.Errorf("documents are not available on this deployment")
	}
	if companyID == "" {
		return actions.Attachment{}, fmt.Errorf("no tenant in context: cannot resolve a document")
	}
	doc, err := s.repo.GetForCompany(ctx, companyID, documentID)
	if err != nil {
		return actions.Attachment{}, err
	}
	url, _, err := s.Presign(ctx, doc)
	if err != nil {
		return actions.Attachment{}, err
	}
	return actions.Attachment{
		Filename:  doc.Filename,
		URL:       url,
		SizeBytes: doc.SizeBytes,
		ExpiresIn: s.presignTTL,
	}, nil
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

// PlanKey is where a document's video plan lives, beside the document itself.
//
// A suffix on the document's own key rather than a second scheme: the two
// objects share a lifetime, and a bucket policy or a deletion sweep written
// against `documents/{company}/` covers both without having to know this
// exists.
func PlanKey(storageKey string) string {
	return strings.TrimSuffix(storageKey, path.Ext(storageKey)) + ".plan.json"
}

// PageKey is where page n of a carousel lives: under the document's own key
// with the extension replaced by a directory, `…/{id}/01.jpg`, 1-based and
// two digits wide so a listing sorts in page order (T-G6).
func PageKey(storageKey string, page int) string {
	return strings.TrimSuffix(storageKey, path.Ext(storageKey)) + fmt.Sprintf("/%02d.jpg", page)
}

// ManifestKey is where a carousel's manifest lives, beside its pages.
func ManifestKey(storageKey string) string {
	return strings.TrimSuffix(storageKey, path.Ext(storageKey)) + "/carousel.json"
}

// LoadPage reads one page of a carousel.
//
// Returns domain.ErrNotFound for a document that is not a carousel, a page
// under 1 or over the count — the same answer as a missing document, because
// to the route that serves it those are the same thing: nothing at this path.
// The count is the row's, not the bucket's, so a made-up page number never
// reaches storage.
func (s *Service) LoadPage(ctx context.Context, doc *domain.Document, page int) ([]byte, error) {
	if s == nil || doc == nil || doc.Format != domain.DocumentFormatCarousel {
		return nil, domain.ErrNotFound
	}
	if page < 1 || page > doc.PageCount {
		return nil, domain.ErrNotFound
	}
	body, err := s.storage.DownloadKey(ctx, PageKey(doc.StorageKey, page))
	if err != nil {
		return nil, fmt.Errorf("read page %d: %w", page, err)
	}
	return body, nil
}

// PresignPage issues a download URL for one page of a carousel, valid for
// PresignTTL — the same clock as the document's own link (T-G6, finding 6).
//
// This is for a channel, not the dashboard. The dashboard reads pages through
// its authenticated route so a persisted message never carries a link that
// expires; a phone has no session to present, so it gets what the zip link
// already is: a signed URL that works for an hour. The same page bounds as
// LoadPage, and the same refusal for anything that is not a carousel.
func (s *Service) PresignPage(ctx context.Context, doc *domain.Document, page int) (string, error) {
	if s == nil || doc == nil || doc.Format != domain.DocumentFormatCarousel {
		return "", domain.ErrNotFound
	}
	if page < 1 || page > doc.PageCount {
		return "", domain.ErrNotFound
	}
	signed, err := s.storage.PresignKey(ctx, PageKey(doc.StorageKey, page), s.presignTTL)
	if err != nil {
		return "", fmt.Errorf("presign page %d: %w", page, err)
	}
	return signed, nil
}

// storePlan writes the video plan beside a document, when it has one.
//
// Three things decide whether it does, and all three are refusals rather than
// failures:
//
//   - **The format has to be a narrative one.** A CSV and a spreadsheet are
//     data; there is nothing to animate and nobody would watch it.
//   - **The document has to make an argument** — the same `Analytical`
//     predicate the mp4 door refuses a record with, so a shared invoice is
//     impossible for exactly the reason a video of one is.
//   - **The plan has to build.** Over the scene cap, too long, a chart that
//     will not draw: all deterministic, all the caller's spec, none of them a
//     reason to fail a PDF that rendered perfectly well.
//
// Every outcome is logged at debug or warn and swallowed. The document exists,
// and a Share control that is absent is a smaller failure than a report that
// did not arrive.
func (s *Service) storePlan(ctx context.Context, doc *spec.Document, stored *domain.Document) {
	switch stored.Format {
	case domain.DocumentFormatPDF, domain.DocumentFormatPPTX, domain.DocumentFormatMP4:
	default:
		return
	}
	if !spec.Analytical(doc) {
		return
	}

	cfg := s.brandFor(ctx, stored.CompanyID)
	plan, err := videoplan.Build(doc, videoplan.Options{
		Brand:    cfg.Video(),
		Currency: s.currencyFor(ctx, stored.CompanyID),
		Locale:   cfg.Locale,
		Limits:   s.videoLimits,
	})
	if err != nil {
		logrus.WithError(err).WithField("document_id", stored.ID).
			Debug("no video plan stored for this document; it cannot be shared as a player")
		return
	}
	body, err := json.Marshal(plan)
	if err != nil {
		logrus.WithError(err).WithField("document_id", stored.ID).Warn("video plan not marshalled")
		return
	}
	if _, err := s.storage.UploadKey(ctx, PlanKey(stored.StorageKey),
		bytes.NewReader(body), "application/json"); err != nil {
		logrus.WithError(err).WithField("document_id", stored.ID).Warn("video plan not stored")
		return
	}
	stored.HasPlan = true
}

// LoadPlan reads a document's stored plan.
//
// Returns domain.ErrNotFound when there is none, which is the ordinary case
// for a spreadsheet, an invoice, or anything generated before T-V4.
func (s *Service) LoadPlan(ctx context.Context, doc *domain.Document) ([]byte, error) {
	if s == nil || doc == nil {
		return nil, domain.ErrNotFound
	}
	body, err := s.storage.DownloadKey(ctx, PlanKey(doc.StorageKey))
	if err != nil {
		return nil, domain.ErrNotFound
	}
	return body, nil
}

// render dispatches to the format's renderer, returning the bytes and — for a
// format rendered somewhere else — how many seconds that took.
//
// The PDF, PPTX and MP4 paths are the ones that read company settings. A
// spreadsheet and a CSV are data, and a tenant's currency symbol pasted into a
// cell someone wants to sum is a formatting decision made in the wrong place.
//
// **The mp4 branch is the first that is not `(*spec.Document) ([]byte, error)`
// in this process.** The seam is kept as narrow as it can be: build the plan,
// call the service, hand back bytes. Everything after this line — the storage
// key, the row, the presign, the metering — is the code that already runs for
// the other four, which is the whole reason there is one `Generate`.
func (s *Service) render(ctx context.Context, doc *spec.Document, companyID string, images map[string]videoplan.PromoImage, onProgress func(float64)) (*rendered, error) {
	switch doc.Format {
	case "pdf":
		data, err := pdf.Render(doc, s.pdfOptions(ctx, companyID))
		return &rendered{data: data}, err
	case "pptx":
		data, err := pptx.Render(doc, s.pptxOptions(ctx, companyID))
		return &rendered{data: data}, err
	case "xlsx":
		data, err := document.RenderXLSX(document.FromReportSpec(doc))
		return &rendered{data: data}, err
	case "csv":
		data, err := document.RenderCSV(document.FromReportSpec(doc))
		return &rendered{data: data}, err
	case "mp4":
		data, seconds, err := s.renderVideo(ctx, doc, companyID, onProgress)
		return &rendered{data: data, seconds: seconds}, err
	case "carousel":
		return s.renderCarousel(ctx, doc, companyID, images, onProgress)
	}
	return nil, fmt.Errorf("unsupported format %q", doc.Format)
}

// renderCarousel is renderVideo with the time axis removed (T-G6): the same
// brand, currency and locale, the plan built by videoplan.BuildCarousel on the
// portrait surface, the pages drawn by the render service one still at a
// time, and the zip assembled here — Go has archive/zip and the render service
// deliberately has nothing (decision 5).
//
// The zip is the document; the pages are stored beside it by Generate. Both
// exist because they answer different readers: the zip is what a person
// downloads and forwards, the pages are what the dashboard shows inline and
// what a publisher (T-G8) uploads one at a time.
func (s *Service) renderCarousel(ctx context.Context, doc *spec.Document, companyID string, images map[string]videoplan.PromoImage, onProgress func(float64)) (*rendered, error) {
	if !s.VideoAvailable() {
		return nil, video.ErrNotConfigured
	}
	cfg := s.brandFor(ctx, companyID)
	plan, err := videoplan.BuildCarousel(doc, videoplan.Options{
		Brand:    cfg.Video(),
		Currency: s.currencyFor(ctx, companyID),
		Locale:   cfg.Locale,
		Limits:   s.videoLimits,
		Images:   images,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %s", video.ErrPlanRejected, err)
	}
	res, err := s.video.RenderStills(ctx, plan, onProgress)
	if err != nil {
		return nil, err
	}
	if len(res.Pages) != len(plan.Scenes) {
		// The service drew a different number of slides from the plan it was
		// sent. Nothing downstream can label such a set — the alt texts are
		// per scene — so it is a service failure, not a page-count surprise.
		return nil, fmt.Errorf("%w: the render service returned %d pages for %d slides",
			video.ErrUnavailable, len(res.Pages), len(plan.Scenes))
	}

	manifest := &CarouselManifest{Alts: make([]string, len(plan.Scenes)), Pages: len(res.Pages)}
	if doc.Social != nil {
		manifest.Caption = doc.Social.Caption
		manifest.Hashtags = doc.Social.Hashtags
	}
	for i, sc := range plan.Scenes {
		manifest.Alts[i] = sc.Alt
	}
	data, err := zipCarousel(res.Pages, manifest)
	if err != nil {
		return nil, fmt.Errorf("zip carousel: %w", err)
	}
	return &rendered{data: data, seconds: res.Seconds, pages: res.Pages, manifest: manifest}, nil
}

// zipCarousel packs the pages, the caption and the manifest into the download.
//
// The caption is a plain text file as well as a manifest field so a person
// who unzips on a phone can copy it without reading JSON: the way most owners
// post today is by hand, from the phone, and the zip is for them.
func zipCarousel(pages [][]byte, m *CarouselManifest) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i, page := range pages {
		w, err := zw.Create(fmt.Sprintf("%02d.jpg", i+1))
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(page); err != nil {
			return nil, err
		}
	}
	if caption := CaptionText(m); caption != "" {
		w, err := zw.Create("caption.txt")
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(caption)); err != nil {
			return nil, err
		}
	}
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	w, err := zw.Create("carousel.json")
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(body); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// CaptionText is the caption as it would be pasted into the post: the text,
// a blank line, then the hashtags with their "#", which the spec stores
// without so it is never doubled.
func CaptionText(m *CarouselManifest) string {
	if m == nil {
		return ""
	}
	parts := []string{}
	if strings.TrimSpace(m.Caption) != "" {
		parts = append(parts, strings.TrimSpace(m.Caption))
	}
	if len(m.Hashtags) > 0 {
		tags := make([]string, 0, len(m.Hashtags))
		for _, h := range m.Hashtags {
			if h = strings.TrimLeft(strings.TrimSpace(h), "#"); h != "" {
				tags = append(tags, "#"+h)
			}
		}
		if len(tags) > 0 {
			parts = append(parts, strings.Join(tags, " "))
		}
	}
	return strings.Join(parts, "\n\n")
}

// renderVideo projects the spec onto a plan and has the render service draw it.
//
// The projection happens here rather than in the service because everything it
// needs is here: the tenant's branding, their currency and locale, the chart
// images Go already draws for the PDF and the deck. What crosses the wire is a
// finished plan — every string wrapped, every duration counted, every image a
// data URI — which is what lets the render service hold no palette, no theme
// and no network.
func (s *Service) renderVideo(ctx context.Context, doc *spec.Document, companyID string, onProgress func(float64)) ([]byte, float64, error) {
	if !s.VideoAvailable() {
		return nil, 0, video.ErrNotConfigured
	}
	cfg := s.brandFor(ctx, companyID)
	plan, err := videoplan.Build(doc, videoplan.Options{
		Brand:    cfg.Video(),
		Currency: s.currencyFor(ctx, companyID),
		Locale:   cfg.Locale,
		Limits:   s.videoLimits,
	})
	if err != nil {
		// A plan that will not build is the caller's spec, not our renderer:
		// too many scenes, too long, a chart that cannot be drawn. It is
		// wrapped as a rejection so the handler answers 400 rather than
		// inviting a retry of something deterministic.
		return nil, 0, fmt.Errorf("%w: %s", video.ErrPlanRejected, err)
	}
	res, err := s.video.Render(ctx, plan, onProgress)
	if err != nil {
		return nil, 0, err
	}
	return res.Data, res.Seconds, nil
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
