package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fauzanebd/argentum/internal/docparse"
	"github.com/fauzanebd/argentum/internal/domain"
)

// DocumentPageService serves one page of a parse to the review surface (T-P7).
//
// It is a separate, tiny service rather than a method on the table service
// because it answers a different question — *what does this page say?* rather
// than *what did we extract?* — and because the review surface fetches pages
// one at a time. A hundred-page artifact fetched to render page four is the
// design the parse service already refused when it wrote one object per page;
// this is the read side of that decision.
type DocumentPageService struct {
	docs  domain.SourceDocumentRepository
	blobs DocumentArtifactStore
}

func NewDocumentPageService(docs domain.SourceDocumentRepository, blobs DocumentArtifactStore) *DocumentPageService {
	return &DocumentPageService{docs: docs, blobs: blobs}
}

// Page returns one page artifact, scoped to the tenant in the document lookup.
//
// The scoping is done by fetching the document through the company-scoped read
// and building the key from what comes back — never from a caller-supplied
// company id or hash. An artifact key holds the company and the content hash,
// so a handler that built one from request parameters would be a path to any
// tenant's page for anyone who can guess a sha256.
func (s *DocumentPageService) Page(ctx context.Context, companyID, documentID string, number int) (*docparse.Page, error) {
	if s == nil || s.docs == nil || s.blobs == nil {
		return nil, fmt.Errorf("document review is not configured on this deployment")
	}
	doc, err := s.docs.GetForCompany(ctx, companyID, documentID)
	if err != nil {
		return nil, err
	}
	if number < 1 || (doc.PageCount > 0 && number > doc.PageCount) {
		return nil, fmt.Errorf("%w: this document has %d pages", domain.ErrNotFound, doc.PageCount)
	}
	body, err := s.blobs.DownloadKey(ctx, PageArtifactKey(doc.CompanyID, doc.ContentSHA256, number))
	if err != nil {
		return nil, fmt.Errorf("%w: page %d has not been read", domain.ErrNotFound, number)
	}
	var page docparse.Page
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("decode page artifact: %w", err)
	}
	return &page, nil
}
