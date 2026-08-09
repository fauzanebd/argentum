package domain

import (
	"context"
	"time"
)

// ReportShare is a bearer link that plays one document as an animated deck
// (T-V4).
//
// It is deliberately not a presigned URL. A presigned URL cannot be revoked
// before it expires, cannot be counted, and cannot be scoped to a page — and
// "who has seen the Q3 numbers, and can I stop them" is the question a tenant
// asks about a link the moment they have shared one.
//
// The token is never stored. `TokenHash` is a SHA-256 of it, for the same
// reason `api_keys` hashes: a dump of this table must not be a set of working
// links. SHA-256 rather than Argon2id, and for the same argument `T-13` makes
// at length — the input is 256 uniformly random bits, so there is no dictionary
// for a KDF to slow down, and a 64 MiB allocation on every page view is a
// denial-of-service handed to anybody who can guess a prefix.
type ReportShare struct {
	ID         string     `json:"id"`
	CompanyID  string     `json:"company_id"`
	DocumentID string     `json:"document_id"`
	TokenHash  string     `json:"-"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	// ViewCount and LastViewedAt are what makes a link answerable at a glance
	// in the dashboard. The audit log holds the detail — one row per view with
	// the ip and the user agent — and these two are the summary nobody should
	// have to run a query for.
	ViewCount    int        `json:"view_count"`
	LastViewedAt *time.Time `json:"last_viewed_at,omitempty"`
}

// Live reports whether a share still opens.
//
// Expiry and revocation are both here and both required. Expiry is the default
// that bounds the damage nobody notices; revocation is the button pressed at
// 11pm. A share with only one of them is either a link that lives forever or a
// link you cannot take back.
func (s *ReportShare) Live(now time.Time) bool {
	return s != nil && s.RevokedAt == nil && now.Before(s.ExpiresAt)
}

// Share lifetimes, in days. A default that is not forever, and a ceiling an
// admin cannot type their way past.
//
// Days rather than a `time.Duration` for two reasons, one of them the
// generator's. A duration constant comes out of tygo as `30 * 24 * any /*
// time.Hour */`, which does not compile — the same class of problem `T-02b`
// exists to make loud, caught here by the dashboard's build. And days is the
// unit the request field is in (`expires_in_days`) and the unit a person
// picking an expiry thinks in, so the conversion belongs at the one place that
// needs a duration rather than in every place that quotes the limit.
const (
	ShareDefaultDays = 30
	ShareMaxDays     = 90
)

// ShareDefaultTTL and ShareMaxTTL are those limits as durations.
func ShareDefaultTTL() time.Duration { return ShareDefaultDays * 24 * time.Hour }
func ShareMaxTTL() time.Duration     { return ShareMaxDays * 24 * time.Hour }

// ReportShareRepository persists share links.
type ReportShareRepository interface {
	Insert(ctx context.Context, s *ReportShare) error
	// ByTokenHash resolves the bearer token. It is **not** company-scoped, and
	// cannot be: the caller of `GET /share/:token` is logged out and has no
	// tenant. The token is the whole credential, which is why the row carries
	// the company id — everything the page then reads is scoped by what comes
	// back from here rather than by anything the request said.
	ByTokenHash(ctx context.Context, hash string) (*ReportShare, error)
	// ListForDocument is the dashboard's link list, newest first, including
	// revoked and expired rows. A share that has disappeared from the UI is a
	// share nobody can prove was ever created.
	ListForDocument(ctx context.Context, companyID, documentID string) ([]*ReportShare, error)
	// Revoke is company-scoped in the query and idempotent: revoking twice is
	// what a nervous admin does, and the second attempt must not be an error
	// they have to interpret.
	Revoke(ctx context.Context, companyID, id string) error
	// RecordView bumps the counter and the timestamp. Best-effort by contract
	// — a failed count must never stop the page rendering — so it returns an
	// error for logging rather than for the caller to act on.
	RecordView(ctx context.Context, id string, at time.Time) error
}
