package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/docgen"
	"github.com/fauzanebd/argentum/internal/domain"
)

// ReportShareService mints, resolves and revokes the links that play a
// document as an animated deck (T-V4).
//
// The player itself is nearly free — `packages/motion` runs the same
// compositions in a browser that the render service runs headlessly — so
// almost everything here is about the link: who can create one, how long it
// lives, how it is taken back, and what is recorded when somebody opens it.
type ReportShareService struct {
	shares domain.ReportShareRepository
	docs   domain.DocumentRepository
	gen    *docgen.Service
	// audit records one row per view. A bearer link is the one surface where
	// "who has seen this" cannot be answered from a session, so it has to be
	// answered from the request.
	audit domain.AgentActionRepository
	now   func() time.Time
	// planLoader is the one seam into docgen, pulled out as a field so the
	// link's own behaviour — expiry, revocation, tenancy, the audit row — is
	// testable without an object store. Everything else in this file is about
	// the link rather than about the plan.
	planLoader func(context.Context, *domain.Document) ([]byte, error)
}

func NewReportShareService(
	shares domain.ReportShareRepository,
	docs domain.DocumentRepository,
	gen *docgen.Service,
	audit domain.AgentActionRepository,
) *ReportShareService {
	s := &ReportShareService{shares: shares, docs: docs, gen: gen, audit: audit, now: time.Now}
	s.planLoader = func(ctx context.Context, d *domain.Document) ([]byte, error) {
		return gen.LoadPlan(ctx, d)
	}
	return s
}

// CreatedShare carries the one and only copy of the token, beside the row that
// outlives it. `T-13`'s precedent, and for the same reason: a token a UI can
// re-read is a token in a browser's history, a screenshot and a support
// ticket.
type CreatedShare struct {
	Share *domain.ReportShare `json:"share"`
	Token string              `json:"token"`
}

// Create mints a link for one document.
//
// `ttl` of zero means the default. Anything over the ceiling is refused rather
// than silently clamped: an admin who typed 365 has a reason, and a link that
// quietly expires 275 days before they expect is worse than being told no.
func (s *ReportShareService) Create(ctx context.Context, companyID, userID, documentID string, ttl time.Duration) (*CreatedShare, error) {
	if companyID == "" || documentID == "" {
		return nil, fmt.Errorf("%w: a company and a document are required", domain.ErrInvalidInput)
	}
	switch {
	case ttl == 0:
		ttl = domain.ShareDefaultTTL()
	case ttl < 0:
		return nil, fmt.Errorf("%w: an expiry cannot be in the past", domain.ErrInvalidInput)
	case ttl > domain.ShareMaxTTL():
		return nil, fmt.Errorf("%w: a share can last at most %d days", domain.ErrInvalidInput,
			domain.ShareMaxDays)
	}

	// Company-scoped by the query. The id came from a URL, and a fetch
	// followed by a comparison is one forgotten comparison away from letting
	// an admin share another tenant's document.
	doc, err := s.docs.GetForCompany(ctx, companyID, documentID)
	if err != nil {
		return nil, err
	}

	// No plan, no share. The page would open on a document it cannot play,
	// which is a broken link that looks like a working one — and the reason a
	// document has no plan is that it is a record or a spreadsheet, where a
	// player was never the right way to read it.
	if _, err := s.planLoader(ctx, doc); err != nil {
		return nil, fmt.Errorf("%w: this document cannot be played as a deck, so there is nothing to share. "+
			"A share link plays the report; use the download link for a file", domain.ErrInvalidInput)
	}

	token, hash, err := auth.NewShareToken()
	if err != nil {
		return nil, fmt.Errorf("mint share token: %w", err)
	}
	sh := &domain.ReportShare{
		CompanyID:  companyID,
		DocumentID: doc.ID,
		TokenHash:  hash,
		CreatedBy:  userID,
		ExpiresAt:  s.now().UTC().Add(ttl),
	}
	if err := s.shares.Insert(ctx, sh); err != nil {
		return nil, err
	}

	logrus.WithFields(logrus.Fields{
		"company_id":  companyID,
		"document_id": doc.ID,
		"share_id":    sh.ID,
		"expires_at":  sh.ExpiresAt,
	}).Info("report share created")

	return &CreatedShare{Share: sh, Token: token}, nil
}

func (s *ReportShareService) List(ctx context.Context, companyID, documentID string) ([]*domain.ReportShare, error) {
	return s.shares.ListForDocument(ctx, companyID, documentID)
}

func (s *ReportShareService) Revoke(ctx context.Context, companyID, shareID string) error {
	return s.shares.Revoke(ctx, companyID, shareID)
}

// Resolved is what a share page needs: the plan to play and the document it
// came from.
type Resolved struct {
	Share    *domain.ReportShare
	Document *domain.Document
	Plan     []byte
	// DownloadURL is present only when the document is a video the visitor may
	// keep. A presigned URL for a PDF is not offered here — the share is a
	// player, and a file that can be downloaded from a page that cannot be
	// revoked afterwards is the thing this whole design avoids.
	DownloadURL string
}

// ErrShareGone is every way a token fails to open a page: unknown, expired,
// revoked, or pointing at a document that is no longer there.
//
// One error for all four, on purpose. A stranger must not be able to tell an
// expired token from a wrong one — a distinguishable "expired" tells somebody
// enumerating tokens that they guessed one correctly, which turns a 404 wall
// into an oracle.
var ErrShareGone = errors.New("share is not available")

// Resolve turns a bearer token into a page, and records the view.
func (s *ReportShareService) Resolve(ctx context.Context, token, ip, userAgent string) (*Resolved, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrShareGone
	}
	sh, err := s.shares.ByTokenHash(ctx, auth.HashShareToken(token))
	if err != nil || !sh.Live(s.now()) {
		return nil, ErrShareGone
	}

	// The company comes off the row, never off the request: the visitor has no
	// session and asserts nothing.
	doc, err := s.docs.GetForCompany(ctx, sh.CompanyID, sh.DocumentID)
	if err != nil {
		return nil, ErrShareGone
	}
	plan, err := s.planLoader(ctx, doc)
	if err != nil {
		return nil, ErrShareGone
	}

	out := &Resolved{Share: sh, Document: doc, Plan: plan}
	if doc.Format == domain.DocumentFormatMP4 && s.gen != nil {
		if url, _, err := s.gen.Presign(ctx, doc); err == nil {
			out.DownloadURL = url
		}
	}

	s.recordView(ctx, sh, ip, userAgent)
	return out, nil
}

// recordView counts the view and writes the audit row.
//
// Both are best-effort and neither can fail the page. A visitor looking at a
// link somebody sent them is not the right person to show a bookkeeping error
// to, and the alternative — refusing to render because a counter did not
// increment — turns a metrics problem into an outage.
func (s *ReportShareService) recordView(ctx context.Context, sh *domain.ReportShare, ip, userAgent string) {
	now := s.now().UTC()
	if err := s.shares.RecordView(ctx, sh.ID, now); err != nil {
		logrus.WithError(err).WithField("share_id", sh.ID).Warn("share view not counted")
	}
	if s.audit == nil {
		return
	}
	// The actor is the link, not a person — that is the honest description of
	// who did this, and `ActorRef` carrying the share id is what makes "revoke
	// the one being read from an address I do not recognise" answerable. No
	// channel: a view is not a conversation on one, and inventing a `share`
	// channel would widen a union that says which surfaces a thread can arrive
	// from.
	args, err := json.Marshal(map[string]any{
		"share_id":    sh.ID,
		"document_id": sh.DocumentID,
		// Both are attacker-controlled text heading for storage, and both are
		// the only description of a visitor we have. Truncated rather than
		// dropped: a 4 KB user agent is somebody probing, and the first 256
		// bytes still say so.
		"ip":         truncate(ip, 64),
		"user_agent": truncate(userAgent, 256),
	})
	if err != nil {
		return
	}
	row := &domain.AgentAction{
		CompanyID:    sh.CompanyID,
		ActorKind:    domain.ActorKindShare,
		ActorRef:     sh.ID,
		ToolName:     "report_share.view",
		ArgsRedacted: args,
		ResultStatus: domain.ActionStatusOK,
	}
	if err := s.audit.Create(ctx, row); err != nil {
		logrus.WithError(err).WithField("share_id", sh.ID).Warn("share view not audited")
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
