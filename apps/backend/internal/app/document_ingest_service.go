package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
)

// DocumentIngestService takes a PDF from a tenant and makes it a durable,
// deduplicated, queued row (T-P1).
//
// **What it deliberately does not do.** It does not read the file. Nothing here
// knows how many pages a PDF has or what is on them — that is the parser's job
// (T-P2), and keeping it out of this service is what lets an upload succeed on a
// deployment with no parser at all. The resting state that produces is a
// document that says `uploaded`, which is the honest description of a file
// nothing has looked at.
//
// The dedupe is the property worth stating: the same bytes uploaded twice return
// the first document. Parsing costs money on the OCR path, a monthly report gets
// sent twice, and two rows nobody can tell apart is a support conversation.
type DocumentIngestService struct {
	docs  domain.SourceDocumentRepository
	blobs DocumentBlobStore
	// queue is nil on a deployment with no parser, and that is a supported
	// configuration rather than a broken one — see the service docblock.
	queue DocumentParseQueue
	// tables removes what this document published into the document warehouse
	// when it is deleted. Nil where there is no warehouse, which is also where
	// nothing was ever published.
	tables   DocumentTableCleaner
	maxBytes int64
}

// DocumentBlobStore is the half of storage.StorageService this service needs.
//
// Declared at the consumer, like CookbookService's EmbeddingResolver and for the
// same reason: the concrete client dials MinIO in its constructor, so a service
// that took it could not be exercised in a test — and what needs exercising here
// is the ordering around a failed write, which is precisely the path a live
// gate will not produce on demand.
type DocumentBlobStore interface {
	UploadKey(ctx context.Context, key string, r io.Reader, contentType string) (string, error)
	RemoveKey(ctx context.Context, key string) error
}

// DocumentParseQueue is the one method of queue.Enqueuer this service calls.
type DocumentParseQueue interface {
	EnqueueDocumentParse(ctx context.Context, documentID string) error
}

// DocumentTableCleaner removes what a document published into the warehouse.
//
// Declared here because delete is this service's method and the warehouse is
// not its dependency: `document_tables` rows go with the document through ON
// DELETE CASCADE, and the *materialized* tables are in another database that no
// foreign key reaches. Without this hook a deleted document would leave the
// agent able to query its figures — which is the one failure a deletion feature
// must not have.
type DocumentTableCleaner interface {
	DropForDocument(ctx context.Context, companyID, documentID string) error
}

// ErrDocumentTooLarge is returned instead of storing a file over the configured
// cap. Distinct from ErrInvalidInput because it maps to 413 rather than 400: the
// caller sent a valid PDF and this deployment declines to hold it, which is a
// different sentence from "that is not a PDF".
var ErrDocumentTooLarge = errors.New("document exceeds the upload limit")

// DocumentContentType is what an uploaded PDF is stored as. Fixed rather than
// taken from the request: the browser's declared type is the caller's claim and
// LooksLikePDF has already checked the bytes.
const DocumentContentType = "application/pdf"

// NewDocumentIngestService builds the service. maxUploadMB <= 0 falls back to
// 25, matching DOC_MAX_UPLOAD_MB's default — constructors here normalize bad
// input rather than erroring on it.
func NewDocumentIngestService(
	docs domain.SourceDocumentRepository, blobs DocumentBlobStore, maxUploadMB int,
) *DocumentIngestService {
	if maxUploadMB <= 0 {
		maxUploadMB = 25
	}
	return &DocumentIngestService{
		docs:     docs,
		blobs:    blobs,
		maxBytes: int64(maxUploadMB) << 20,
	}
}

// WithParseQueue enables queueing uploads for parsing. Passing nil leaves the
// feature off, which is what a deployment with no parser does.
func (s *DocumentIngestService) WithParseQueue(q DocumentParseQueue) *DocumentIngestService {
	if q == nil {
		return s
	}
	s.queue = q
	return s
}

// WithTableCleanup makes delete remove what the document published (T-P6).
func (s *DocumentIngestService) WithTableCleanup(c DocumentTableCleaner) *DocumentIngestService {
	if c == nil {
		return s
	}
	s.tables = c
	return s
}

func (s *DocumentIngestService) configured() error {
	if s == nil || s.docs == nil || s.blobs == nil {
		return fmt.Errorf("document upload is not configured on this deployment")
	}
	return nil
}

// UploadInput is one upload request.
type UploadInput struct {
	CompanyID string
	// UserID is who pressed upload. Optional — a document uploaded through an
	// automation has no person behind it — and stored as NULL when empty.
	UserID   string
	Filename string
	Body     io.Reader
}

// UploadResult is what happened, in the shape the handler answers with.
type UploadResult struct {
	Document *domain.SourceDocument `json:"document"`
	// Deduplicated says this file was already here. The handler answers 200
	// rather than 201 for it, so a client can tell "stored" from "already
	// stored" without comparing timestamps.
	Deduplicated bool `json:"deduplicated"`
	// Queued says a parse was enqueued. False on a deployment with no parser,
	// and false when the enqueue failed — in both cases the document is stored
	// and can be queued later, which is why neither costs the upload.
	Queued bool `json:"queued"`
}

// Upload stores the bytes, writes the row, and queues the parse.
//
// The order is bytes first, row second, queue third, and it is the only order
// that fails safely. A row written before the object exists is a document the
// parser will fail on for a reason nobody can see; an object with no row is
// invisible and is cleaned up here; a queued id whose row is missing is a task
// that skips itself.
func (s *DocumentIngestService) Upload(ctx context.Context, in UploadInput) (*UploadResult, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.CompanyID) == "" {
		return nil, fmt.Errorf("%w: company is required", domain.ErrInvalidInput)
	}
	if in.Body == nil {
		return nil, fmt.Errorf("%w: no file in the request", domain.ErrInvalidInput)
	}

	// One byte over the cap is read on purpose: reading exactly maxBytes cannot
	// distinguish a file at the limit from one that was truncated at it.
	data, err := io.ReadAll(io.LimitReader(in.Body, s.maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read upload: %w", err)
	}
	if int64(len(data)) > s.maxBytes {
		return nil, ErrDocumentTooLarge
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: the file is empty", domain.ErrInvalidInput)
	}
	if !domain.LooksLikePDF(data) {
		// Checked on content, not on the extension or the declared content type:
		// both are the caller's claim, and a .docx renamed to .pdf would
		// otherwise reach the parser and fail there with a worse message.
		return nil, fmt.Errorf("%w: only PDF files can be uploaded", domain.ErrInvalidInput)
	}

	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])

	if existing, err := s.docs.GetBySHA(ctx, in.CompanyID, sha); err == nil && existing != nil {
		logrus.WithFields(logrus.Fields{
			"company_id":  in.CompanyID,
			"document_id": existing.ID,
		}).Info("document already uploaded; returning the existing row")
		return &UploadResult{Document: existing, Deduplicated: true}, nil
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, fmt.Errorf("check for an existing document: %w", err)
	}

	doc := &domain.SourceDocument{
		CompanyID:     in.CompanyID,
		Filename:      in.Filename,
		ContentSHA256: sha,
		ByteSize:      int64(len(data)),
		StorageKey:    documentStorageKey(in.CompanyID, sha),
		Status:        domain.SourceDocumentUploaded,
		UploadedBy:    strings.TrimSpace(in.UserID),
	}
	doc.Normalize()
	if doc.Filename == "" {
		doc.Filename = sha[:12] + ".pdf"
	}

	// The bytes are read into memory once, bounded by maxBytes, rather than
	// streamed: the content hash decides the key, so it has to be known before
	// there is a key to stream to.
	if _, err := s.blobs.UploadKey(ctx, doc.StorageKey, bytes.NewReader(data), DocumentContentType); err != nil {
		return nil, fmt.Errorf("store document: %w", err)
	}

	if err := s.docs.Create(ctx, doc); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			// Two uploads of the same file raced. The object is not removed: the
			// key is derived from the content hash, so the row that won points at
			// exactly these bytes, and deleting them would break it.
			existing, getErr := s.docs.GetBySHA(ctx, in.CompanyID, sha)
			if getErr != nil {
				return nil, fmt.Errorf("resolve the duplicate document: %w", getErr)
			}
			return &UploadResult{Document: existing, Deduplicated: true}, nil
		}
		// The row is what makes the object findable. Without one the bytes are
		// unreachable by every path in this product, so they go.
		if rmErr := s.blobs.RemoveKey(ctx, doc.StorageKey); rmErr != nil {
			logrus.WithError(rmErr).WithField("key", doc.StorageKey).
				Warn("could not remove the object for a document row that failed to write; it is orphaned")
		}
		return nil, fmt.Errorf("record document: %w", err)
	}

	out := &UploadResult{Document: doc}
	if s.queue != nil {
		if err := s.queue.EnqueueDocumentParse(ctx, doc.ID); err != nil {
			// Degrade rather than fail: the document is stored and the parse can
			// be queued again. Losing the upload because the queue was briefly
			// unreachable would ask the tenant to send the file a second time.
			logrus.WithError(err).WithField("document_id", doc.ID).
				Warn("document stored but the parse could not be queued; it can be queued again")
		} else {
			out.Queued = true
		}
	}
	logrus.WithFields(logrus.Fields{
		"company_id":  doc.CompanyID,
		"document_id": doc.ID,
		"bytes":       doc.ByteSize,
		"queued":      out.Queued,
	}).Info("source document uploaded")
	return out, nil
}

// List is one page of a tenant's documents, newest first.
func (s *DocumentIngestService) List(ctx context.Context, companyID string, limit, offset int) ([]*domain.SourceDocument, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	return s.docs.ListByCompany(ctx, companyID, limit, offset)
}

// Get is one document, scoped to the tenant in the query.
func (s *DocumentIngestService) Get(ctx context.Context, companyID, id string) (*domain.SourceDocument, error) {
	if err := s.configured(); err != nil {
		return nil, err
	}
	return s.docs.GetForCompany(ctx, companyID, id)
}

// Delete removes the document and its bytes.
//
// The object goes first and the row second, which is the ordering that stays
// recoverable. If the object cannot be removed nothing has changed and the
// caller can retry; if the row cannot be removed after it, the retry succeeds
// because removing an already-absent object is not an error. The reverse order
// has a failure mode with no recovery path: a deleted row leaves bytes nothing
// references and nothing can name.
func (s *DocumentIngestService) Delete(ctx context.Context, companyID, id string) error {
	if err := s.configured(); err != nil {
		return err
	}
	doc, err := s.docs.GetForCompany(ctx, companyID, id)
	if err != nil {
		return err
	}
	if s.tables != nil {
		// The published rows go first, and a failure here stops the delete.
		// Everything else this method removes is *ours*; the warehouse tables
		// are what the agent answers questions from, so a delete that removed
		// the row and left them behind would leave a deleted document still
		// answering — with nothing left to explain where the figures came from.
		if err := s.tables.DropForDocument(ctx, companyID, id); err != nil {
			return fmt.Errorf("remove published tables: %w", err)
		}
	}
	if err := s.blobs.RemoveKey(ctx, doc.StorageKey); err != nil {
		return fmt.Errorf("remove document bytes: %w", err)
	}
	if err := s.docs.Delete(ctx, companyID, id); err != nil {
		return fmt.Errorf("delete document: %w", err)
	}
	logrus.WithFields(logrus.Fields{"company_id": companyID, "document_id": id}).
		Info("source document deleted")
	return nil
}

// documentStorageKey addresses the object by content, not by document id.
//
// Two things follow from that and both are wanted: a re-upload of identical
// bytes writes the same key rather than a second copy, and a row that failed to
// write leaves an object whose name says what it holds. The company prefix keeps
// one tenant's objects listable without walking another's.
func documentStorageKey(companyID, sha string) string {
	return fmt.Sprintf("source-documents/%s/%s.pdf", companyID, sha)
}
