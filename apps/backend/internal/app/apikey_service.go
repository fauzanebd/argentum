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
	"github.com/fauzanebd/argentum/internal/domain"
)

// API keys (T-13, finding P-2).
//
// Every route in this product requires a human session JWT, so nothing can
// integrate with Argentum at all: not the tenant's own backend, not another
// agent, not a nightly job. This service owns the only machine credential
// there is, which makes it the thing standing between `/v1` and a tenant's
// data.

const (
	// apiKeyNameMax bounds the label an admin gives a key. It is a UI string,
	// not a credential.
	apiKeyNameMax = 80
	// apiKeyTouchEvery throttles last_used_at. Once a minute per key per
	// process is enough to answer "is anything still using this key?" before
	// a revoke, and it keeps a write off the authentication path of every
	// request.
	apiKeyTouchEvery = time.Minute
)

// APIKeyService mints, lists, revokes and authenticates API keys.
type APIKeyService struct {
	repo domain.APIKeyRepository
	now  func() time.Time

	mu        sync.Mutex
	lastTouch map[string]time.Time
}

// NewAPIKeyService wires the repository. now is injectable so tests can drive
// expiry without sleeping.
func NewAPIKeyService(repo domain.APIKeyRepository) *APIKeyService {
	return &APIKeyService{repo: repo, now: time.Now, lastTouch: map[string]time.Time{}}
}

// CreatedAPIKey carries the one and only copy of the plaintext token, beside
// the record that will outlive it.
type CreatedAPIKey struct {
	Key *domain.APIKey `json:"key"`
	// Token is `arg_<prefix>_<secret>`. It is returned by exactly one response
	// in the system's lifetime; nothing stores it and no read path can
	// reconstruct it.
	Token string `json:"token"`
}

// Create mints a key for one company. expiresInDays of 0 means no expiry:
// a server-to-server credential with no rotation tooling behind it is better
// off never expiring than expiring unattended at 3am.
func (s *APIKeyService) Create(
	ctx context.Context, companyID, createdBy, name string, scopes []string, expiresInDays int,
) (*CreatedAPIKey, error) {
	name = strings.TrimSpace(name)
	switch {
	case companyID == "":
		return nil, fmt.Errorf("%w: a company is required", domain.ErrInvalidInput)
	case name == "":
		return nil, fmt.Errorf("%w: a key needs a name", domain.ErrInvalidInput)
	case len(name) > apiKeyNameMax:
		return nil, fmt.Errorf("%w: name must be %d characters or fewer", domain.ErrInvalidInput, apiKeyNameMax)
	case expiresInDays < 0:
		return nil, fmt.Errorf("%w: expiry cannot be in the past", domain.ErrInvalidInput)
	}

	parsed, err := domain.NormalizeScopes(scopes)
	if err != nil {
		return nil, err
	}
	if len(parsed) == 0 {
		// Deny by default is what makes this an error rather than a default.
		// A key with no scopes authenticates and then reaches nothing, which
		// looks exactly like a bug to whoever deploys it.
		return nil, fmt.Errorf("%w: a key needs at least one scope", domain.ErrInvalidInput)
	}

	token, prefix, hash, err := auth.NewAPIKey()
	if err != nil {
		return nil, fmt.Errorf("mint api key: %w", err)
	}

	k := &domain.APIKey{
		CompanyID: companyID,
		Name:      name,
		KeyPrefix: prefix,
		KeyHash:   hash,
		Scopes:    parsed,
		CreatedBy: createdBy,
	}
	if expiresInDays > 0 {
		exp := s.now().UTC().AddDate(0, 0, expiresInDays)
		k.ExpiresAt = &exp
	}
	if err := s.repo.Create(ctx, k); err != nil {
		return nil, err
	}
	k.Status = k.StatusAt(s.now())

	logrus.WithFields(logrus.Fields{
		"company_id": companyID,
		"key_prefix": prefix,
		"scopes":     k.SortedScopeStrings(),
	}).Info("api key created")

	return &CreatedAPIKey{Key: k, Token: token}, nil
}

// List returns the company's keys, newest first, revoked ones included.
func (s *APIKeyService) List(ctx context.Context, companyID string) ([]*domain.APIKey, error) {
	keys, err := s.repo.ListByCompany(ctx, companyID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	for _, k := range keys {
		k.Status = k.StatusAt(now)
	}
	return keys, nil
}

// Revoke stops a key working. It is a tombstone rather than a delete: the
// audit log attributes rows to a key id, and a deleted key turns every one of
// those rows into an unanswerable question.
func (s *APIKeyService) Revoke(ctx context.Context, companyID, id string) error {
	if err := s.repo.Revoke(ctx, companyID, id, s.now().UTC()); err != nil {
		return err
	}
	logrus.WithFields(logrus.Fields{"company_id": companyID, "key_id": id}).Info("api key revoked")
	return nil
}

// Authenticate resolves a presented token to a usable key, or
// domain.ErrUnauthorized. Every failure — malformed, unknown, wrong secret,
// revoked, expired — returns that one error, because a caller holding a
// broken credential has no business learning which of the five it is.
//
// **There is no cache here, deliberately.** T-03 caches the credit verdict for
// 60s and accepts that a topped-up tenant stays refused for a minute; the same
// trade on a credential means a revoked key keeps working for a minute after
// an admin has decided it should not, which is the exact moment the key is
// most likely to be in the wrong hands. The cost is one indexed read on a
// UNIQUE column per request.
func (s *APIKeyService) Authenticate(ctx context.Context, token string) (*domain.APIKey, error) {
	prefix, secret, ok := auth.ParseAPIKey(token)
	if !ok {
		return nil, domain.ErrUnauthorized
	}

	k, err := s.repo.GetByPrefix(ctx, prefix)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// Hash the presented secret anyway. An unknown prefix and a wrong
			// secret otherwise take measurably different amounts of time, and
			// that difference is a free oracle for enumerating which prefixes
			// exist.
			_ = auth.HashAPIKeySecret(secret)
			return nil, domain.ErrUnauthorized
		}
		return nil, err
	}

	if !auth.APIKeySecretMatches(secret, k.KeyHash) {
		return nil, domain.ErrUnauthorized
	}
	now := s.now()
	if !k.Usable(now) {
		// Logged, because "my key stopped working" is the support ticket this
		// line answers, and the caller is told nothing.
		logrus.WithFields(logrus.Fields{
			"company_id": k.CompanyID,
			"key_id":     k.ID,
			"status":     k.StatusAt(now),
		}).Info("api key refused")
		return nil, domain.ErrUnauthorized
	}

	s.touch(ctx, k.ID, now)
	k.Status = domain.APIKeyActive
	return k, nil
}

// touch records last_used_at at most once per apiKeyTouchEvery per key. The
// write is best-effort: losing the timestamp must never fail the request that
// was going to produce it.
func (s *APIKeyService) touch(ctx context.Context, id string, now time.Time) {
	s.mu.Lock()
	last, seen := s.lastTouch[id]
	if seen && now.Sub(last) < apiKeyTouchEvery {
		s.mu.Unlock()
		return
	}
	s.lastTouch[id] = now
	s.mu.Unlock()

	if err := s.repo.TouchLastUsed(ctx, id, now.UTC()); err != nil {
		logrus.WithError(err).WithField("key_id", id).Debug("api key last-used update failed")
	}
}
