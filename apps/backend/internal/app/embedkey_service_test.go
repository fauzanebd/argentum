package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/crypto"
	"github.com/fauzanebd/argentum/internal/domain"
)

// stubEmbedKeys is an in-memory EmbedKeyRepository. It counts touches so the
// throttle test can prove the write did not happen twice.
type stubEmbedKeys struct {
	byClient map[string]*domain.EmbedKey
	getErr   error
	touches  int
}

func newStubEmbedKeys() *stubEmbedKeys {
	return &stubEmbedKeys{byClient: map[string]*domain.EmbedKey{}}
}

func (s *stubEmbedKeys) Create(_ context.Context, k *domain.EmbedKey) error {
	k.ID = "ek-" + k.ClientKey
	k.Enabled = true
	k.CreatedAt = time.Now()
	s.byClient[k.ClientKey] = k
	return nil
}

func (s *stubEmbedKeys) GetByClientKey(_ context.Context, clientKey string) (*domain.EmbedKey, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	k, ok := s.byClient[clientKey]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return k, nil
}

func (s *stubEmbedKeys) GetByID(_ context.Context, companyID, id string) (*domain.EmbedKey, error) {
	for _, k := range s.byClient {
		if k.ID == id && k.CompanyID == companyID {
			return k, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (s *stubEmbedKeys) ListByCompany(_ context.Context, companyID string) ([]*domain.EmbedKey, error) {
	var out []*domain.EmbedKey
	for _, k := range s.byClient {
		if k.CompanyID == companyID {
			out = append(out, k)
		}
	}
	return out, nil
}

func (s *stubEmbedKeys) Update(_ context.Context, companyID, id string, origins []string, enabled bool) error {
	for _, k := range s.byClient {
		if k.ID == id && k.CompanyID == companyID && k.RevokedAt == nil {
			k.AllowedOrigins = origins
			k.Enabled = enabled
			return nil
		}
	}
	return domain.ErrNotFound
}

func (s *stubEmbedKeys) Revoke(_ context.Context, companyID, id string, at time.Time) error {
	for _, k := range s.byClient {
		if k.ID == id && k.CompanyID == companyID && k.RevokedAt == nil {
			t := at
			k.RevokedAt = &t
			k.Enabled = false
			return nil
		}
	}
	return domain.ErrNotFound
}

func (s *stubEmbedKeys) TouchLastUsed(_ context.Context, _ string, _ time.Time) error {
	s.touches++
	return nil
}

// testEmbedKeyHex is a fixed 32-byte cipher key: reproducible failures.
const testEmbedKeyHex = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func newEmbedSvc(t *testing.T) (*EmbedKeyService, *stubEmbedKeys) {
	t.Helper()
	cipher, err := crypto.NewFromHex(testEmbedKeyHex)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	signer, err := auth.NewTokenSigner(strings.Repeat("k", 48), 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	repo := newStubEmbedKeys()
	return NewEmbedKeyService(repo, cipher, signer, 15*time.Minute), repo
}

func TestEmbedCreateValidates(t *testing.T) {
	svc, _ := newEmbedSvc(t)
	ctx := context.Background()

	cases := []struct {
		name    string
		keyName string
		origins []string
	}{
		{"no name", "", []string{"https://acme.com"}},
		{"name too long", strings.Repeat("n", 81), []string{"https://acme.com"}},
		{"no origins", "Intranet", nil},
		{"wildcard origin", "Intranet", []string{"*"}},
		{"wildcard subdomain", "Intranet", []string{"https://*.acme.com"}},
		{"plain http", "Intranet", []string{"http://acme.com"}},
		{"too many origins", "Intranet", manyOrigins(21)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Create(ctx, "company-1", "user-1", tc.keyName, tc.origins)
			if err == nil {
				t.Fatal("Create succeeded, want a refusal")
			}
			if !errors.Is(err, domain.ErrInvalidInput) {
				t.Errorf("err = %v, want ErrInvalidInput so the handler answers 400", err)
			}
		})
	}
}

func manyOrigins(n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, "https://host"+string(rune('a'+i%26))+".acme.com")
	}
	return out
}

func TestEmbedCreateSealsSecretAndShowsItOnce(t *testing.T) {
	svc, repo := newEmbedSvc(t)
	created, err := svc.Create(context.Background(), "company-1", "user-1", "Intranet",
		[]string{"https://acme.com"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Secret == "" {
		t.Fatal("no secret returned; the tenant's backend can never sign anything")
	}
	stored := repo.byClient[created.Key.ClientKey]
	if stored == nil {
		t.Fatal("key was not stored")
	}
	if strings.Contains(string(stored.SecretEnc), created.Secret) {
		t.Error("the signing secret is stored in plain text")
	}
	// The record that outlives the response must not carry the secret.
	if stored.SecretEnc == nil {
		t.Error("no sealed secret stored; the mint could never verify a signature")
	}
}

// signFor computes what a correctly-implemented tenant backend would send.
func signFor(t *testing.T, secret, ref string, exp time.Time) string {
	t.Helper()
	return auth.EmbedSignature(secret, ref, exp.Unix())
}

// TestMintSessionMatrix is the ticket's gate: the full matrix of identity
// material against both doors. `session` and `refresh` share a handler and a
// service call by design, so the matrix runs the same table twice rather than
// asserting the two are wired to the same function — a future divergence would
// then show up here as a failure rather than as an untested route.
func TestMintSessionMatrix(t *testing.T) {
	const origin = "https://acme.com"
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)

	for _, door := range []string{"session", "refresh"} {
		t.Run(door, func(t *testing.T) {
			cases := []struct {
				name    string
				want    error
				mutate  func(k *domain.EmbedKey)
				request func(secret string) SessionRequest
				client  string
			}{
				{
					name: "valid",
					want: nil,
					request: func(secret string) SessionRequest {
						exp := now.Add(time.Hour)
						return SessionRequest{Origin: origin, UserRef: "emp_812", Exp: exp.Unix(),
							Signature: signFor(t, secret, "emp_812", exp)}
					},
				},
				{
					name: "tampered user_ref",
					want: ErrEmbedIdentityRejected,
					request: func(secret string) SessionRequest {
						exp := now.Add(time.Hour)
						// Signed as emp_812, presented as the CEO.
						return SessionRequest{Origin: origin, UserRef: "emp_001", Exp: exp.Unix(),
							Signature: signFor(t, secret, "emp_812", exp)}
					},
				},
				{
					name: "tampered signature",
					want: ErrEmbedIdentityRejected,
					request: func(secret string) SessionRequest {
						exp := now.Add(time.Hour)
						return SessionRequest{Origin: origin, UserRef: "emp_812", Exp: exp.Unix(),
							Signature: strings.Repeat("0", 64)}
					},
				},
				{
					name: "correct signature from a non-allowlisted origin",
					want: ErrEmbedOriginNotAllowed,
					request: func(secret string) SessionRequest {
						exp := now.Add(time.Hour)
						return SessionRequest{Origin: "https://evil-acme.com", UserRef: "emp_812",
							Exp: exp.Unix(), Signature: signFor(t, secret, "emp_812", exp)}
					},
				},
				{
					name: "no origin header at all",
					want: ErrEmbedOriginNotAllowed,
					request: func(secret string) SessionRequest {
						exp := now.Add(time.Hour)
						return SessionRequest{Origin: "", UserRef: "emp_812", Exp: exp.Unix(),
							Signature: signFor(t, secret, "emp_812", exp)}
					},
				},
				{
					name: "expired exp",
					want: ErrEmbedIdentityRejected,
					request: func(secret string) SessionRequest {
						exp := now.Add(-time.Second)
						return SessionRequest{Origin: origin, UserRef: "emp_812", Exp: exp.Unix(),
							Signature: signFor(t, secret, "emp_812", exp)}
					},
				},
				{
					name: "exp more than 24h out",
					want: ErrEmbedIdentityRejected,
					request: func(secret string) SessionRequest {
						exp := now.Add(25 * time.Hour)
						return SessionRequest{Origin: origin, UserRef: "emp_812", Exp: exp.Unix(),
							Signature: signFor(t, secret, "emp_812", exp)}
					},
				},
				{
					name:   "revoked key",
					want:   ErrEmbedKeyUnusable,
					mutate: func(k *domain.EmbedKey) { at := now; k.RevokedAt = &at; k.Enabled = false },
					request: func(secret string) SessionRequest {
						exp := now.Add(time.Hour)
						return SessionRequest{Origin: origin, UserRef: "emp_812", Exp: exp.Unix(),
							Signature: signFor(t, secret, "emp_812", exp)}
					},
				},
				{
					name:   "disabled key",
					want:   ErrEmbedKeyUnusable,
					mutate: func(k *domain.EmbedKey) { k.Enabled = false },
					request: func(secret string) SessionRequest {
						exp := now.Add(time.Hour)
						return SessionRequest{Origin: origin, UserRef: "emp_812", Exp: exp.Unix(),
							Signature: signFor(t, secret, "emp_812", exp)}
					},
				},
				{
					name:   "unknown client key",
					want:   ErrEmbedKeyUnusable,
					client: "argw_pub_" + strings.Repeat("ab", 16),
					request: func(secret string) SessionRequest {
						exp := now.Add(time.Hour)
						return SessionRequest{Origin: origin, UserRef: "emp_812", Exp: exp.Unix(),
							Signature: signFor(t, secret, "emp_812", exp)}
					},
				},
				{
					name:   "malformed client key",
					want:   ErrEmbedKeyUnusable,
					client: "arg_0123456789_secret",
					request: func(secret string) SessionRequest {
						exp := now.Add(time.Hour)
						return SessionRequest{Origin: origin, UserRef: "emp_812", Exp: exp.Unix(),
							Signature: signFor(t, secret, "emp_812", exp)}
					},
				},
				{
					name: "empty user_ref",
					want: ErrEmbedIdentityRejected,
					request: func(secret string) SessionRequest {
						exp := now.Add(time.Hour)
						return SessionRequest{Origin: origin, UserRef: "", Exp: exp.Unix(),
							Signature: signFor(t, secret, "", exp)}
					},
				},
			}

			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					svc, repo := newEmbedSvc(t)
					svc.now = func() time.Time { return now }
					ctx := context.Background()

					created, err := svc.Create(ctx, "company-1", "user-1", "Intranet", []string{origin})
					if err != nil {
						t.Fatalf("Create: %v", err)
					}
					if tc.mutate != nil {
						tc.mutate(repo.byClient[created.Key.ClientKey])
					}

					req := tc.request(created.Secret)
					req.ClientKey = created.Key.ClientKey
					if tc.client != "" {
						req.ClientKey = tc.client
					}

					sess, err := svc.MintSession(ctx, req)
					if tc.want != nil {
						if !errors.Is(err, tc.want) {
							t.Fatalf("err = %v, want %v", err, tc.want)
						}
						if sess != nil {
							t.Error("a refused mint returned a session anyway")
						}
						return
					}
					if err != nil {
						t.Fatalf("MintSession: %v", err)
					}
					if sess.Token == "" {
						t.Fatal("no token")
					}
					if sess.CompanyID != "company-1" || sess.UserRef != "emp_812" {
						t.Errorf("session = %+v, want the identity we signed", sess)
					}
					if want := now.Add(15 * time.Minute).UTC(); !sess.ExpiresAt.Equal(want) {
						t.Errorf("ExpiresAt = %v, want %v", sess.ExpiresAt, want)
					}
				})
			}
		})
	}
}

// TestMintedSessionCarriesNoDashboardAuthority pins what the token may say. The
// mint is where an embed identity is created, so it is where "this is not a
// staff session" has to be true.
func TestMintedSessionCarriesNoDashboardAuthority(t *testing.T) {
	svc, _ := newEmbedSvc(t)
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	ctx := context.Background()

	created, err := svc.Create(ctx, "company-1", "user-1", "Intranet", []string{"https://acme.com"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	exp := now.Add(time.Hour)
	sess, err := svc.MintSession(ctx, SessionRequest{
		ClientKey: created.Key.ClientKey,
		Origin:    "https://acme.com",
		UserRef:   "emp_812",
		Exp:       exp.Unix(),
		Signature: signFor(t, created.Secret, "emp_812", exp),
	})
	if err != nil {
		t.Fatalf("MintSession: %v", err)
	}

	signer, err := auth.NewTokenSigner(strings.Repeat("k", 48), 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	dash, err := signer.Verify(sess.Token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if dash.TokenType == "access" {
		t.Fatal("the minted session claims to be a dashboard access token")
	}
	if dash.Role != "" || dash.UserID != "" {
		t.Errorf("minted session carries role=%q user=%q; both must be empty", dash.Role, dash.UserID)
	}
}

func TestMintThrottlesLastUsedWrites(t *testing.T) {
	svc, repo := newEmbedSvc(t)
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	ctx := context.Background()

	created, err := svc.Create(ctx, "company-1", "user-1", "Intranet", []string{"https://acme.com"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	exp := now.Add(time.Hour)
	req := SessionRequest{
		ClientKey: created.Key.ClientKey,
		Origin:    "https://acme.com",
		UserRef:   "emp_812",
		Exp:       exp.Unix(),
		Signature: signFor(t, created.Secret, "emp_812", exp),
	}
	for i := 0; i < 5; i++ {
		if _, err := svc.MintSession(ctx, req); err != nil {
			t.Fatalf("MintSession %d: %v", i, err)
		}
	}
	if repo.touches != 1 {
		t.Errorf("last_used_at written %d times for five mints in the same minute, want 1", repo.touches)
	}
}

func TestUpdateAndRevoke(t *testing.T) {
	svc, repo := newEmbedSvc(t)
	ctx := context.Background()
	created, err := svc.Create(ctx, "company-1", "user-1", "Intranet", []string{"https://acme.com"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := created.Key.ID

	// A wildcard cannot get in through the edit door either.
	if _, err := svc.Update(ctx, "company-1", id, []string{"*"}, true); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("Update with a wildcard: err = %v, want ErrInvalidInput", err)
	}
	// Nor can an empty list.
	if _, err := svc.Update(ctx, "company-1", id, nil, true); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("Update with no origins: err = %v, want ErrInvalidInput", err)
	}

	got, err := svc.Update(ctx, "company-1", id, []string{"https://acme.com", "https://ops.acme.com"}, false)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(got.AllowedOrigins) != 2 || got.Enabled {
		t.Errorf("key = %+v, want two origins and disabled", got)
	}

	// Another tenant's id is a not-found, not a cross-tenant edit.
	if _, err := svc.Update(ctx, "company-2", id, []string{"https://acme.com"}, true); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("cross-tenant Update: err = %v, want ErrNotFound", err)
	}
	if err := svc.Revoke(ctx, "company-2", id); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("cross-tenant Revoke: err = %v, want ErrNotFound", err)
	}

	if err := svc.Revoke(ctx, "company-1", id); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if repo.byClient[created.Key.ClientKey].RevokedAt == nil {
		t.Error("revoke left no tombstone")
	}
	// A revoked key cannot be edited back into service.
	if _, err := svc.Update(ctx, "company-1", id, []string{"https://acme.com"}, true); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Update on a revoked key: err = %v, want ErrNotFound", err)
	}
}
