package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/domain"
)

// stubAPIKeys is an in-memory APIKeyRepository. It counts touches so the
// throttle test can prove the write did not happen twice.
type stubAPIKeys struct {
	byPrefix map[string]*domain.APIKey
	createErr,
	getErr error
	touches int
	revoked []string
}

func newStubAPIKeys() *stubAPIKeys {
	return &stubAPIKeys{byPrefix: map[string]*domain.APIKey{}}
}

func (s *stubAPIKeys) Create(_ context.Context, k *domain.APIKey) error {
	if s.createErr != nil {
		return s.createErr
	}
	k.ID = "key-" + k.KeyPrefix
	k.CreatedAt = time.Now()
	s.byPrefix[k.KeyPrefix] = k
	return nil
}

func (s *stubAPIKeys) GetByPrefix(_ context.Context, prefix string) (*domain.APIKey, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	k, ok := s.byPrefix[prefix]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return k, nil
}

func (s *stubAPIKeys) ListByCompany(_ context.Context, companyID string) ([]*domain.APIKey, error) {
	var out []*domain.APIKey
	for _, k := range s.byPrefix {
		if k.CompanyID == companyID {
			out = append(out, k)
		}
	}
	return out, nil
}

func (s *stubAPIKeys) Revoke(_ context.Context, companyID, id string, at time.Time) error {
	for _, k := range s.byPrefix {
		if k.ID == id && k.CompanyID == companyID && k.RevokedAt == nil {
			t := at
			k.RevokedAt = &t
			s.revoked = append(s.revoked, id)
			return nil
		}
	}
	return domain.ErrNotFound
}

func (s *stubAPIKeys) TouchLastUsed(_ context.Context, id string, at time.Time) error {
	s.touches++
	for _, k := range s.byPrefix {
		if k.ID == id {
			t := at
			k.LastUsedAt = &t
		}
	}
	return nil
}

// mintKey creates a key and returns its plaintext token, so the tests below
// exercise the same round trip a caller does rather than a hand-built record.
func mintKey(t *testing.T, svc *APIKeyService, repo *stubAPIKeys, scopes []string) (*domain.APIKey, string) {
	t.Helper()
	res, err := svc.Create(context.Background(), "co-1", "user-1", "CI", scopes, 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return repo.byPrefix[res.Key.KeyPrefix], res.Token
}

// TestCreateValidation is the deny-by-default contract at the door. The
// zero-scope case is the one worth naming: a key with no scopes authenticates
// and then reaches nothing, which looks exactly like a bug to whoever deploys
// it, so it is refused at creation instead.
func TestCreateValidation(t *testing.T) {
	cases := []struct {
		name      string
		company   string
		keyName   string
		scopes    []string
		expiresIn int
		wantErr   error
	}{
		{"ok", "co-1", "CI", []string{"read:usage"}, 0, nil},
		{"ok with expiry", "co-1", "CI", []string{"read:usage"}, 30, nil},
		{"no company", "", "CI", []string{"read:usage"}, 0, domain.ErrInvalidInput},
		{"no name", "co-1", "   ", []string{"read:usage"}, 0, domain.ErrInvalidInput},
		{"name too long", "co-1", string(make([]byte, 81)), []string{"read:usage"}, 0, domain.ErrInvalidInput},
		{"no scopes", "co-1", "CI", nil, 0, domain.ErrInvalidInput},
		{"empty scope list", "co-1", "CI", []string{}, 0, domain.ErrInvalidInput},
		{"unknown scope", "co-1", "CI", []string{"read:everything"}, 0, domain.ErrInvalidInput},
		{"negative expiry", "co-1", "CI", []string{"read:usage"}, -1, domain.ErrInvalidInput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewAPIKeyService(newStubAPIKeys())
			_, err := svc.Create(context.Background(), tc.company, "user-1", tc.keyName, tc.scopes, tc.expiresIn)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("Create: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestCreateReturnsThePlaintextOnceAndStoresOnlyItsHash is the ticket's
// "plaintext appears in exactly one response, ever" criterion at the service
// boundary: the record that persists cannot reconstruct the token.
func TestCreateReturnsThePlaintextOnceAndStoresOnlyItsHash(t *testing.T) {
	repo := newStubAPIKeys()
	svc := NewAPIKeyService(repo)

	res, err := svc.Create(context.Background(), "co-1", "user-1", "CI", []string{"read:usage"}, 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	stored := repo.byPrefix[res.Key.KeyPrefix]
	if stored.KeyHash == res.Token {
		t.Fatal("the token itself was stored")
	}
	_, secret, ok := auth.ParseAPIKey(res.Token)
	if !ok {
		t.Fatal("Create returned a token this system cannot parse")
	}
	if auth.HashAPIKeySecret(secret) != stored.KeyHash {
		t.Error("the stored hash is not the hash of the issued secret")
	}
	// The record the dashboard renders carries the public half and nothing
	// that could be replayed.
	if stored.KeyPrefix == "" || len(stored.KeyPrefix) >= len(res.Token) {
		t.Errorf("key_prefix %q is not the public half of %q", stored.KeyPrefix, res.Token)
	}
}

// TestCreateExpiry pins the two forms of the expiry input: 0 means never, and
// a day count lands in the future.
func TestCreateExpiry(t *testing.T) {
	repo := newStubAPIKeys()
	svc := NewAPIKeyService(repo)
	svc.now = func() time.Time { return time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC) }

	never, err := svc.Create(context.Background(), "co-1", "u", "no expiry", []string{"read:usage"}, 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if never.Key.ExpiresAt != nil {
		t.Errorf("expires_at = %v, want nil", never.Key.ExpiresAt)
	}

	dated, err := svc.Create(context.Background(), "co-1", "u", "30 days", []string{"read:usage"}, 30)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	if dated.Key.ExpiresAt == nil || !dated.Key.ExpiresAt.Equal(want) {
		t.Errorf("expires_at = %v, want %v", dated.Key.ExpiresAt, want)
	}
}

// TestAuthenticate is the ticket's acceptance list, minus the HTTP layer:
// revoked is 401, expired is 401, and every other broken shape is the same
// 401 — a caller holding a bad credential learns nothing about which kind of
// bad it is.
func TestAuthenticate(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	cases := []struct {
		name string
		// mutate adjusts the freshly minted key before the call.
		mutate func(k *domain.APIKey)
		// token overrides the minted token when non-empty.
		token   string
		wantErr error
	}{
		{name: "valid key"},
		{
			name:    "revoked key",
			mutate:  func(k *domain.APIKey) { k.RevokedAt = &past },
			wantErr: domain.ErrUnauthorized,
		},
		{
			name:    "expired key",
			mutate:  func(k *domain.APIKey) { k.ExpiresAt = &past },
			wantErr: domain.ErrUnauthorized,
		},
		{
			name:   "expiry in the future is fine",
			mutate: func(k *domain.APIKey) { k.ExpiresAt = &future },
		},
		{
			name:    "unknown prefix",
			token:   "arg_ffffffffff_QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVpABCDEFGH",
			wantErr: domain.ErrUnauthorized,
		},
		{
			name:    "malformed token",
			token:   "not-an-argentum-key",
			wantErr: domain.ErrUnauthorized,
		},
		{
			name:    "a dashboard JWT",
			token:   "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.e30.sig",
			wantErr: domain.ErrUnauthorized,
		},
		{
			name:    "empty",
			token:   "",
			wantErr: domain.ErrUnauthorized,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newStubAPIKeys()
			svc := NewAPIKeyService(repo)
			svc.now = func() time.Time { return now }

			stored, token := mintKey(t, svc, repo, []string{"read:usage"})
			if tc.mutate != nil {
				tc.mutate(stored)
			}
			if tc.token != "" || tc.name == "empty" {
				token = tc.token
			}

			got, err := svc.Authenticate(context.Background(), token)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				if got != nil {
					t.Error("a refused key still returned a record")
				}
				return
			}
			if err != nil {
				t.Fatalf("Authenticate: %v", err)
			}
			if got.CompanyID != "co-1" {
				t.Errorf("company = %q, want co-1", got.CompanyID)
			}
			if !got.HasScope(domain.ScopeReadUsage) {
				t.Error("the authenticated key lost its scopes")
			}
		})
	}
}

// TestAuthenticateWrongSecretIsRefused covers the case the table above cannot
// express: a real prefix belonging to a real key, with somebody else's secret.
func TestAuthenticateWrongSecretIsRefused(t *testing.T) {
	repo := newStubAPIKeys()
	svc := NewAPIKeyService(repo)

	victim, _ := mintKey(t, svc, repo, []string{"read:usage"})
	_, attackerToken := mintKey(t, svc, repo, []string{"read:usage"})
	_, attackerSecret, _ := auth.ParseAPIKey(attackerToken)

	forged := "arg_" + victim.KeyPrefix + "_" + attackerSecret
	if _, err := svc.Authenticate(context.Background(), forged); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized — a valid prefix admitted the wrong secret", err)
	}
}

// TestAuthenticateRepositoryFailureIsNotAnAuthFailure keeps the two apart:
// T-03 fails open on a credits lookup because a billing check is optional,
// but a credential check that cannot reach its store must not answer "invalid
// key" — that would tell an integrator to rotate a key that is fine.
func TestAuthenticateRepositoryFailureIsNotAnAuthFailure(t *testing.T) {
	repo := newStubAPIKeys()
	svc := NewAPIKeyService(repo)
	_, token := mintKey(t, svc, repo, []string{"read:usage"})

	boom := errors.New("control db is down")
	repo.getErr = boom

	_, err := svc.Authenticate(context.Background(), token)
	if errors.Is(err, domain.ErrUnauthorized) {
		t.Fatal("a database outage was reported as an invalid key")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the repository error", err)
	}
}

// TestRevoke covers the whole lifecycle the dashboard drives, including the
// two failure modes that must not differ: another company's key, and a key
// that is already revoked.
func TestRevoke(t *testing.T) {
	repo := newStubAPIKeys()
	svc := NewAPIKeyService(repo)
	stored, token := mintKey(t, svc, repo, []string{"read:usage"})

	if err := svc.Revoke(context.Background(), "co-1", stored.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := svc.Authenticate(context.Background(), token); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("a revoked key still authenticates: %v", err)
	}
	if err := svc.Revoke(context.Background(), "co-1", stored.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second revoke: err = %v, want ErrNotFound", err)
	}
	if err := svc.Revoke(context.Background(), "co-2", stored.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("cross-tenant revoke: err = %v, want ErrNotFound", err)
	}
}

// TestListDerivesStatus proves the dashboard does not have to re-implement
// the three-way comparison across two nullable timestamps.
func TestListDerivesStatus(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	repo := newStubAPIKeys()
	svc := NewAPIKeyService(repo)
	svc.now = func() time.Time { return now }

	active, _ := mintKey(t, svc, repo, []string{"read:usage"})
	expired, _ := mintKey(t, svc, repo, []string{"read:usage"})
	revoked, _ := mintKey(t, svc, repo, []string{"read:usage"})
	past := now.Add(-time.Hour)
	expired.ExpiresAt = &past
	revoked.RevokedAt = &past
	// Revoked *and* expired reads as revoked: somebody made a decision about
	// it, and that is the more useful fact.
	revoked.ExpiresAt = &past

	keys, err := svc.List(context.Background(), "co-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := map[string]string{
		active.ID:  domain.APIKeyActive,
		expired.ID: domain.APIKeyExpired,
		revoked.ID: domain.APIKeyRevoked,
	}
	if len(keys) != len(want) {
		t.Fatalf("got %d keys, want %d", len(keys), len(want))
	}
	for _, k := range keys {
		if k.Status != want[k.ID] {
			t.Errorf("key %s status = %q, want %q", k.ID, k.Status, want[k.ID])
		}
	}
}

// TestTouchLastUsedIsThrottled is the reason last_used_at is not a write per
// request. Three authentications inside the window produce one write; a
// fourth after it produces a second.
func TestTouchLastUsedIsThrottled(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	repo := newStubAPIKeys()
	svc := NewAPIKeyService(repo)
	svc.now = func() time.Time { return now }
	_, token := mintKey(t, svc, repo, []string{"read:usage"})

	for range 3 {
		if _, err := svc.Authenticate(context.Background(), token); err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
	}
	if repo.touches != 1 {
		t.Errorf("touches = %d after three calls in one window, want 1", repo.touches)
	}

	now = now.Add(2 * apiKeyTouchEvery)
	if _, err := svc.Authenticate(context.Background(), token); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if repo.touches != 2 {
		t.Errorf("touches = %d after the window elapsed, want 2", repo.touches)
	}
}

// TestNormalizeScopesOrdersByVocabulary keeps the stored array stable: two
// keys minted with the same capabilities in different request orders store
// the same thing, so a database read of the table is diffable by eye.
func TestNormalizeScopesOrdersByVocabulary(t *testing.T) {
	a, err := domain.NormalizeScopes([]string{"write:chat", "read:usage", "read:usage"})
	if err != nil {
		t.Fatalf("NormalizeScopes: %v", err)
	}
	b, err := domain.NormalizeScopes([]string{"read:usage", "write:chat"})
	if err != nil {
		t.Fatalf("NormalizeScopes: %v", err)
	}
	if len(a) != 2 || a[0] != domain.ScopeReadUsage || a[1] != domain.ScopeWriteChat {
		t.Fatalf("got %v, want [read:usage write:chat]", a)
	}
	if len(a) != len(b) || a[0] != b[0] || a[1] != b[1] {
		t.Errorf("%v and %v differ; request order changed the stored set", a, b)
	}
}
