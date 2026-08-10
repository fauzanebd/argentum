package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/crypto"
	"github.com/fauzanebd/argentum/internal/domain"
)

// Embed auth (T-19).
//
// This service is the whole security boundary of the widget phase. Everything
// downstream — the channel, the client, the docs — assumes that a session token
// it holds was minted for a real visitor of a real tenant, and this is the only
// place that decision is made. Get it wrong and a tenant's data is one forged
// request away, which is why the mint below reads as a sequence of refusals
// with the success at the end rather than the other way round.

const (
	// embedKeyNameMax bounds the label. A UI string, not a credential.
	embedKeyNameMax = 80
	// embedMaxOrigins bounds the allowlist. A tenant with more than this many
	// distinct sites embedding one workspace is doing something the per-site
	// key model handles better, and an unbounded array is a row somebody can
	// grow until the mint's comparison loop is the slow part of the request.
	embedMaxOrigins = 20
	// embedMaxExpSkew is the ceiling on how far ahead a tenant may sign. The
	// ticket's number, and its reason: a backend that mints eternal signatures
	// has defeated the session TTL entirely, because a page holding one can
	// re-mint forever.
	embedMaxExpSkew = 24 * time.Hour
	// embedUserRefMax bounds the asserted identity. It reaches a thread key, an
	// audit row and a usage report.
	embedUserRefMax = 128
	// embedTouchEvery throttles last_used_at, as api keys do.
	embedTouchEvery = time.Minute
)

// Mint failures. Three values rather than one because the caller answers two
// different statuses and logs a third thing, and because "why did this fail?"
// is the support question this whole surface generates.
//
// What the *caller* is told collapses them again — see the handler. An
// integrator debugging their own page gets the distinction; an attacker
// probing gets one 401 and one 403, and the 403 only after they have already
// proved they hold a valid client key.
var (
	// ErrEmbedKeyUnusable — unknown, revoked or disabled client key.
	ErrEmbedKeyUnusable = errors.New("embed key is not usable")
	// ErrEmbedOriginNotAllowed — the Origin header is not on the key's list.
	ErrEmbedOriginNotAllowed = errors.New("origin is not allowed for this embed key")
	// ErrEmbedIdentityRejected — bad signature, or an exp that is expired or
	// too far in the future. One error for all three: they are the same
	// statement about the same block of identity material, and a caller who can
	// tell "wrong signature" from "stale timestamp" can use the mint as an
	// oracle for the secret.
	ErrEmbedIdentityRejected = errors.New("embed identity was rejected")
)

// EmbedKeyService mints, lists and revokes embed keys, and turns a tenant's
// signed identity assertion into a short-lived session.
type EmbedKeyService struct {
	repo   domain.EmbedKeyRepository
	cipher *crypto.DSNCipher
	signer *auth.TokenSigner
	// sessionTTL is how long a minted token lives. Short by construction: the
	// host page re-signs rather than us issuing a refresh cookie, which is what
	// keeps the widget stateless and keeps revocation meaningful.
	sessionTTL time.Duration
	now        func() time.Time

	mu        sync.Mutex
	lastTouch map[string]time.Time
}

// NewEmbedKeyService wires the dependencies. A zero or negative ttl falls back
// to 15 minutes — the ticket's default, and the number the widget's refresh
// loop is written against.
func NewEmbedKeyService(
	repo domain.EmbedKeyRepository, cipher *crypto.DSNCipher, signer *auth.TokenSigner, ttl time.Duration,
) *EmbedKeyService {
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	return &EmbedKeyService{
		repo:       repo,
		cipher:     cipher,
		signer:     signer,
		sessionTTL: ttl,
		now:        time.Now,
		lastTouch:  map[string]time.Time{},
	}
}

// CreatedEmbedKey carries the one and only copy of the signing secret beside
// the record that outlives it.
type CreatedEmbedKey struct {
	Key *domain.EmbedKey `json:"key"`
	// Secret is shown exactly once. The tenant's *backend* holds it; a secret
	// that reaches their frontend makes every check below decorative, because
	// anyone reading the page can then sign any identity they like.
	Secret string `json:"secret"`
}

// Create mints a key for one company.
func (s *EmbedKeyService) Create(
	ctx context.Context, companyID, createdBy, name string, origins []string,
) (*CreatedEmbedKey, error) {
	name = strings.TrimSpace(name)
	switch {
	case companyID == "":
		return nil, fmt.Errorf("%w: a company is required", domain.ErrInvalidInput)
	case name == "":
		return nil, fmt.Errorf("%w: a key needs a name", domain.ErrInvalidInput)
	case len(name) > embedKeyNameMax:
		return nil, fmt.Errorf("%w: name must be %d characters or fewer", domain.ErrInvalidInput, embedKeyNameMax)
	case len(origins) > embedMaxOrigins:
		return nil, fmt.Errorf("%w: at most %d origins per key", domain.ErrInvalidInput, embedMaxOrigins)
	}

	allowed, err := domain.NormalizeOrigins(origins)
	if err != nil {
		return nil, err
	}
	if s.cipher == nil {
		return nil, errors.New("embed keys need ARGENTUM_DSN_KEY to seal their signing secret")
	}

	clientKey, secret, err := auth.NewEmbedKey()
	if err != nil {
		return nil, fmt.Errorf("mint embed key: %w", err)
	}
	sealed, err := s.cipher.Encrypt(secret)
	if err != nil {
		return nil, fmt.Errorf("seal embed secret: %w", err)
	}

	k := &domain.EmbedKey{
		CompanyID:      companyID,
		Name:           name,
		ClientKey:      clientKey,
		SecretEnc:      sealed,
		AllowedOrigins: allowed,
		CreatedBy:      createdBy,
	}
	if err := s.repo.Create(ctx, k); err != nil {
		return nil, err
	}
	k.Status = k.StatusAt()

	logrus.WithFields(logrus.Fields{
		"company_id": companyID,
		"client_key": clientKey,
		"origins":    allowed,
	}).Info("embed key created")

	return &CreatedEmbedKey{Key: k, Secret: secret}, nil
}

// List returns the company's keys, newest first, revoked ones included.
func (s *EmbedKeyService) List(ctx context.Context, companyID string) ([]*domain.EmbedKey, error) {
	keys, err := s.repo.ListByCompany(ctx, companyID)
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		k.Status = k.StatusAt()
	}
	return keys, nil
}

// Update rewrites the origin allowlist and the enabled switch. The secret and
// the client key are not editable: rotating either is minting a new key, and
// making that explicit is what stops a tenant silently breaking every page that
// holds the old one.
func (s *EmbedKeyService) Update(
	ctx context.Context, companyID, id string, origins []string, enabled bool,
) (*domain.EmbedKey, error) {
	if len(origins) > embedMaxOrigins {
		return nil, fmt.Errorf("%w: at most %d origins per key", domain.ErrInvalidInput, embedMaxOrigins)
	}
	allowed, err := domain.NormalizeOrigins(origins)
	if err != nil {
		return nil, err
	}
	if err := s.repo.Update(ctx, companyID, id, allowed, enabled); err != nil {
		return nil, err
	}
	k, err := s.repo.GetByID(ctx, companyID, id)
	if err != nil {
		return nil, err
	}
	k.Status = k.StatusAt()
	logrus.WithFields(logrus.Fields{
		"company_id": companyID,
		"key_id":     id,
		"origins":    allowed,
		"enabled":    enabled,
	}).Info("embed key updated")
	return k, nil
}

// Revoke stops a key minting sessions. Tokens already issued stay valid until
// they expire — at most sessionTTL, which is the ceiling that makes a
// short-lived token worth the refresh traffic it costs.
func (s *EmbedKeyService) Revoke(ctx context.Context, companyID, id string) error {
	if err := s.repo.Revoke(ctx, companyID, id, s.now().UTC()); err != nil {
		return err
	}
	logrus.WithFields(logrus.Fields{"company_id": companyID, "key_id": id}).Info("embed key revoked")
	return nil
}

// SessionRequest is one identity assertion from a tenant's backend.
type SessionRequest struct {
	ClientKey string
	// Origin is the browser-sent `Origin` header, verbatim. Not a field the
	// caller may choose: it is checked against the allowlist, so accepting it
	// from the body would let a page claim to be anywhere.
	Origin string
	// UserRef is who the tenant says this visitor is.
	UserRef string
	// Exp is the deadline the tenant signed over, as a unix timestamp.
	Exp int64
	// Signature is `HMAC-SHA256(secret, "<user_ref>:<exp>")`, hex.
	Signature string
}

// MintedSession is what a valid assertion buys.
type MintedSession struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	CompanyID string    `json:"-"`
	UserRef   string    `json:"-"`
	KeyID     string    `json:"-"`
}

// MintSession is the security boundary. The order of its refusals is the
// ticket's, and it is not arbitrary:
//
//  1. Resolve the key. Unknown, revoked or disabled all end here.
//  2. Check the Origin against the allowlist. Before any crypto, because a
//     request from an origin we never allowlisted should not get to exercise
//     the signature path at all.
//  3. Verify the HMAC, in constant time.
//  4. Check the deadline: not in the past, and not more than 24h ahead.
//
// A failure at any step returns one of three sentinel errors and never a
// partial result.
func (s *EmbedKeyService) MintSession(ctx context.Context, req SessionRequest) (*MintedSession, error) {
	if s.signer == nil || s.cipher == nil {
		return nil, errors.New("embed sessions are not configured on this deployment")
	}

	clientKey := strings.TrimSpace(req.ClientKey)
	if !auth.ValidEmbedClientKey(clientKey) {
		// Shape check first: a malformed value costs a string comparison
		// rather than a database round trip, and it is the most common thing a
		// misconfigured host page sends.
		return nil, ErrEmbedKeyUnusable
	}

	k, err := s.repo.GetByClientKey(ctx, clientKey)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, ErrEmbedKeyUnusable
		}
		return nil, err
	}
	if !k.Usable() {
		logrus.WithFields(logrus.Fields{
			"company_id": k.CompanyID,
			"key_id":     k.ID,
			"status":     k.StatusAt(),
		}).Info("embed session refused: key not usable")
		return nil, ErrEmbedKeyUnusable
	}

	if !k.AllowsOrigin(req.Origin) {
		// Logged **with the offending origin**, because this is the one refusal
		// a legitimate integrator hits constantly — a staging host nobody added,
		// a port that changed — and the whole cost of debugging it is knowing
		// what we compared against what.
		logrus.WithFields(logrus.Fields{
			"company_id":      k.CompanyID,
			"key_id":          k.ID,
			"origin":          req.Origin,
			"allowed_origins": k.AllowedOrigins,
		}).Warn("embed session refused: origin not allowed")
		return nil, ErrEmbedOriginNotAllowed
	}

	userRef := strings.TrimSpace(req.UserRef)
	if userRef == "" || len(userRef) > embedUserRefMax {
		return nil, ErrEmbedIdentityRejected
	}

	secret, err := s.cipher.Decrypt(k.SecretEnc)
	if err != nil {
		// The row is unreadable with this deployment's key — a key rotation
		// without a re-encrypt, most likely. That is ours, not the caller's, so
		// it is an error rather than a refusal.
		return nil, fmt.Errorf("open embed secret: %w", err)
	}

	if !auth.EmbedSignatureValid(secret, userRef, req.Exp, req.Signature) {
		logrus.WithFields(logrus.Fields{
			"company_id": k.CompanyID,
			"key_id":     k.ID,
			"user_ref":   userRef,
		}).Warn("embed session refused: signature mismatch")
		return nil, ErrEmbedIdentityRejected
	}

	// The deadline is checked *after* the signature so that a caller who does
	// not hold the secret cannot use the mint to discover which timestamps are
	// acceptable. Both bounds matter: the past one is what makes an old
	// signature stop working, and the future one is what stops a tenant minting
	// a signature that outlives the employee it names.
	now := s.now()
	exp := time.Unix(req.Exp, 0)
	if !exp.After(now) || exp.Sub(now) > embedMaxExpSkew {
		logrus.WithFields(logrus.Fields{
			"company_id": k.CompanyID,
			"key_id":     k.ID,
			"exp":        exp.UTC(),
		}).Info("embed session refused: exp out of range")
		return nil, ErrEmbedIdentityRejected
	}

	token, err := s.signer.IssueEmbedToken(k.CompanyID, userRef, k.ID, s.sessionTTL)
	if err != nil {
		return nil, fmt.Errorf("issue embed session: %w", err)
	}
	s.touch(ctx, k.ID, now)

	return &MintedSession{
		Token:     token,
		ExpiresAt: now.Add(s.sessionTTL).UTC(),
		CompanyID: k.CompanyID,
		UserRef:   userRef,
		KeyID:     k.ID,
	}, nil
}

// SessionTTL is what the handler reports to the caller so a host page can
// schedule its own re-sign rather than waiting for a 401.
func (s *EmbedKeyService) SessionTTL() time.Duration { return s.sessionTTL }

// EmbedMaxSignatureLifetime is the ceiling on `exp - now` in a tenant's
// signature. Exported so the dashboard's install snippet can state the limit it
// is generating code against rather than hardcoding a second copy of it.
func EmbedMaxSignatureLifetime() time.Duration { return embedMaxExpSkew }

// touch records last_used_at at most once per embedTouchEvery per key,
// best-effort. Losing it must never fail the mint that produced it.
func (s *EmbedKeyService) touch(ctx context.Context, id string, now time.Time) {
	s.mu.Lock()
	last, seen := s.lastTouch[id]
	if seen && now.Sub(last) < embedTouchEvery {
		s.mu.Unlock()
		return
	}
	s.lastTouch[id] = now
	s.mu.Unlock()

	if err := s.repo.TouchLastUsed(ctx, id, now.UTC()); err != nil {
		logrus.WithError(err).WithField("key_id", id).Debug("embed key last-used update failed")
	}
}
