package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/domain"
)

// fakeShares is the repository, with the two properties the real one gets from
// SQL: the token lookup is unscoped, and every other read is bounded by
// company.
type fakeShares struct {
	rows    []*domain.ReportShare
	nextID  int
	viewsOf map[string]int
}

func newFakeShares() *fakeShares {
	return &fakeShares{viewsOf: map[string]int{}}
}

func (f *fakeShares) Insert(_ context.Context, s *domain.ReportShare) error {
	f.nextID++
	s.ID = "share-" + string(rune('a'+f.nextID-1))
	s.CreatedAt = shareNow
	f.rows = append(f.rows, s)
	return nil
}

func (f *fakeShares) ByTokenHash(_ context.Context, hash string) (*domain.ReportShare, error) {
	for _, s := range f.rows {
		if s.TokenHash == hash {
			cp := *s
			return &cp, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (f *fakeShares) ListForDocument(_ context.Context, companyID, documentID string) ([]*domain.ReportShare, error) {
	var out []*domain.ReportShare
	for _, s := range f.rows {
		if s.CompanyID == companyID && s.DocumentID == documentID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeShares) Revoke(_ context.Context, companyID, id string) error {
	for _, s := range f.rows {
		if s.ID == id && s.CompanyID == companyID {
			if s.RevokedAt == nil {
				t := shareNow
				s.RevokedAt = &t
			}
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *fakeShares) RecordView(_ context.Context, id string, _ time.Time) error {
	f.viewsOf[id]++
	return nil
}

// fakeAudit captures the rows a view writes.
type fakeAudit struct{ rows []*domain.AgentAction }

func (f *fakeAudit) Create(_ context.Context, a *domain.AgentAction) error {
	f.rows = append(f.rows, a)
	return nil
}

func (f *fakeAudit) ListByCompany(context.Context, string, domain.AgentActionFilter) ([]*domain.AgentAction, error) {
	return nil, nil
}

var shareNow = time.Unix(1_800_000_000, 0).UTC()

// shareFixture wires the service against a document that has a plan. gen is
// nil, which is what makes these tests about the share rather than about
// docgen — see the note on each test that needs a plan.
type shareFixture struct {
	svc    *ReportShareService
	shares *fakeShares
	audit  *fakeAudit
	docs   *fakeDocLookup
}

func newShareFixture(t *testing.T) *shareFixture {
	t.Helper()
	docs := &fakeDocLookup{docs: []*domain.Document{{
		ID: "doc-1", CompanyID: "co-1", Format: domain.DocumentFormatMP4,
		Filename: "revenue.mp4", StorageKey: "documents/co-1/api/doc-1.mp4",
	}}}
	f := &shareFixture{shares: newFakeShares(), audit: &fakeAudit{}, docs: docs}
	f.svc = NewReportShareService(f.shares, docs, nil, f.audit)
	f.svc.now = func() time.Time { return shareNow }
	// The plan lookup goes through docgen, which needs an object store. These
	// tests are about the link, so the seam is stubbed to "there is a plan"
	// and the "no plan, no share" case has its own test below.
	f.svc.planLoader = func(context.Context, *domain.Document) ([]byte, error) {
		return []byte(`{"version":1}`), nil
	}
	return f
}

func TestAShareIsMintedOnceAndHashedAtRest(t *testing.T) {
	f := newShareFixture(t)

	created, err := f.svc.Create(t.Context(), "co-1", "user-1", "doc-1", 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Token == "" {
		t.Fatal("no token returned; the link cannot be sent to anybody")
	}
	// The row must not be able to reconstruct it. This is the property the
	// whole hashing argument is for: a dump of report_shares is not a set of
	// working links.
	if created.Share.TokenHash == created.Token {
		t.Fatal("the token was stored in the clear")
	}
	if created.Share.TokenHash != auth.HashShareToken(created.Token) {
		t.Error("the stored hash is not the hash of the token that was handed out")
	}
	if want := shareNow.Add(domain.ShareDefaultTTL()); !created.Share.ExpiresAt.Equal(want) {
		t.Errorf("expires_at = %v, want the 30-day default %v", created.Share.ExpiresAt, want)
	}
}

// Over the ceiling is a refusal, not a silent clamp. An admin who typed 365
// has a reason, and a link that quietly dies 275 days early is worse than one
// they were told they could not have.
func TestAShareCannotOutliveTheCeiling(t *testing.T) {
	f := newShareFixture(t)

	if _, err := f.svc.Create(t.Context(), "co-1", "user-1", "doc-1", 365*24*time.Hour); err == nil {
		t.Fatal("a 365-day share was accepted")
	}
}

// The cross-tenant bound, and it is in the query rather than in a comparison
// afterwards: the document id came from a URL.
func TestOneCompanyCannotShareAnothersDocument(t *testing.T) {
	f := newShareFixture(t)

	if _, err := f.svc.Create(t.Context(), "co-2", "user-9", "doc-1", 0); err == nil {
		t.Fatal("an admin shared another company's document")
	}
}

// A document with no plan cannot be shared. The page would open on something
// it cannot play, which is a broken link that looks like a working one.
func TestADocumentWithNoPlanCannotBeShared(t *testing.T) {
	f := newShareFixture(t)
	f.svc.planLoader = func(context.Context, *domain.Document) ([]byte, error) {
		return nil, domain.ErrNotFound
	}

	if _, err := f.svc.Create(t.Context(), "co-1", "user-1", "doc-1", 0); err == nil {
		t.Fatal("a document with no plan was shared")
	}
}

func TestResolvingAShareCountsAndAuditsTheView(t *testing.T) {
	f := newShareFixture(t)
	created, _ := f.svc.Create(t.Context(), "co-1", "user-1", "doc-1", 0)

	res, err := f.svc.Resolve(t.Context(), created.Token, "203.0.113.9", "Mozilla/5.0")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Plan) == 0 {
		t.Error("the page has no plan to play")
	}
	if f.shares.viewsOf[created.Share.ID] != 1 {
		t.Errorf("view count = %d, want 1", f.shares.viewsOf[created.Share.ID])
	}
	if len(f.audit.rows) != 1 {
		t.Fatalf("%d audit rows, want 1 — 'who has seen this' is the question a bearer link cannot answer from a session", len(f.audit.rows))
	}
	row := f.audit.rows[0]
	if row.ActorKind != domain.ActorKindShare || row.ActorRef != created.Share.ID {
		t.Errorf("the row does not name the link that was used: kind=%q ref=%q", row.ActorKind, row.ActorRef)
	}
	if row.CompanyID != "co-1" {
		t.Errorf("the row is not scoped to the tenant: %q", row.CompanyID)
	}
	var args map[string]any
	if err := json.Unmarshal(row.ArgsRedacted, &args); err != nil {
		t.Fatalf("args: %v", err)
	}
	if args["ip"] != "203.0.113.9" || args["user_agent"] != "Mozilla/5.0" {
		t.Errorf("the row does not describe the visitor: %v", args)
	}
}

// Revoking kills the link on the next request, not on the next expiry.
func TestRevokingEndsAShareImmediately(t *testing.T) {
	f := newShareFixture(t)
	created, _ := f.svc.Create(t.Context(), "co-1", "user-1", "doc-1", 0)

	if err := f.svc.Revoke(t.Context(), "co-1", created.Share.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := f.svc.Resolve(t.Context(), created.Token, "", ""); err != ErrShareGone {
		t.Fatalf("a revoked link still opened: %v", err)
	}
	// And a revoked view is not counted: the page never rendered.
	if f.shares.viewsOf[created.Share.ID] != 0 {
		t.Error("a refused request was counted as a view")
	}
}

// Revoking twice is what a nervous admin does. The second call is a success,
// because the link is revoked either way.
func TestRevokingTwiceIsNotAnError(t *testing.T) {
	f := newShareFixture(t)
	created, _ := f.svc.Create(t.Context(), "co-1", "user-1", "doc-1", 0)

	_ = f.svc.Revoke(t.Context(), "co-1", created.Share.ID)
	if err := f.svc.Revoke(t.Context(), "co-1", created.Share.ID); err != nil {
		t.Fatalf("the second revoke errored: %v", err)
	}
}

// Another tenant cannot revoke a link they can guess the id of.
func TestOneCompanyCannotRevokeAnothersShare(t *testing.T) {
	f := newShareFixture(t)
	created, _ := f.svc.Create(t.Context(), "co-1", "user-1", "doc-1", 0)

	if err := f.svc.Revoke(t.Context(), "co-2", created.Share.ID); err == nil {
		t.Fatal("another company revoked this share")
	}
	if _, err := f.svc.Resolve(t.Context(), created.Token, "", ""); err != nil {
		t.Error("the link stopped working after a foreign revoke attempt")
	}
}

// An expired link and a wrong one answer identically. A distinguishable
// "expired" tells somebody trying tokens that they guessed one correctly,
// which turns a wall into an oracle.
func TestAnExpiredShareIsIndistinguishableFromAWrongOne(t *testing.T) {
	f := newShareFixture(t)
	created, _ := f.svc.Create(t.Context(), "co-1", "user-1", "doc-1", time.Hour)

	f.svc.now = func() time.Time { return shareNow.Add(2 * time.Hour) }

	expired := f.svc.mustResolveErr(t, created.Token)
	unknown := f.svc.mustResolveErr(t, "not-a-real-token")
	if expired != unknown {
		t.Errorf("expired (%v) and unknown (%v) are told apart", expired, unknown)
	}
}

func (s *ReportShareService) mustResolveErr(t *testing.T, token string) error {
	t.Helper()
	_, err := s.Resolve(t.Context(), token, "", "")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	return err
}
