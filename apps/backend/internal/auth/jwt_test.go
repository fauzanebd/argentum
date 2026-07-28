package auth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "0123456789abcdef0123456789abcdef" // exactly 32 chars

func newSigner(t *testing.T) *TokenSigner {
	t.Helper()
	s, err := NewTokenSigner(testSecret, 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}
	return s
}

func TestNewTokenSignerRejectsAShortSecret(t *testing.T) {
	// HS256 with a guessable secret is a forgeable session for every user in
	// the system, so the length floor is a security control, not hygiene.
	for _, secret := range []string{"", "short", strings.Repeat("a", 31)} {
		if _, err := NewTokenSigner(secret, time.Minute, time.Hour); err == nil {
			t.Errorf("NewTokenSigner(%d-char secret) = nil error, want an error", len(secret))
		}
	}
	if _, err := NewTokenSigner(strings.Repeat("a", 32), time.Minute, time.Hour); err != nil {
		t.Errorf("NewTokenSigner(32-char secret) = %v, want nil", err)
	}
}

func TestNewTokenSignerDefaultsNonPositiveTTLs(t *testing.T) {
	// A zero TTL from an unset env var would mint tokens that expire the
	// instant they are issued — every request a 401, with no clue why.
	s, err := NewTokenSigner(testSecret, 0, 0)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}
	if got := s.AccessTTL(); got != 15*time.Minute {
		t.Errorf("AccessTTL() = %v, want 15m", got)
	}
	if got := s.RefreshTTL(); got != 7*24*time.Hour {
		t.Errorf("RefreshTTL() = %v, want 168h", got)
	}

	s, err = NewTokenSigner(testSecret, -time.Hour, -time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}
	if s.AccessTTL() <= 0 || s.RefreshTTL() <= 0 {
		t.Errorf("negative TTLs survived: access=%v refresh=%v", s.AccessTTL(), s.RefreshTTL())
	}
}

func TestIssueAndVerifyAccessToken(t *testing.T) {
	s := newSigner(t)

	raw, err := s.IssueAccessToken("user-1", "co-1", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	claims, err := s.Verify(raw)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if claims.UserID != "user-1" {
		t.Errorf("UserID = %q, want user-1", claims.UserID)
	}
	// company_id rides in the token so middleware can populate tenantctx
	// without a DB hit — if it is dropped, every tool loses its tenant.
	if claims.CompanyID != "co-1" {
		t.Errorf("CompanyID = %q, want co-1", claims.CompanyID)
	}
	if claims.Role != "admin" {
		t.Errorf("Role = %q, want admin", claims.Role)
	}
	if claims.TokenType != "access" {
		t.Errorf("TokenType = %q, want access", claims.TokenType)
	}
	// Claims.UserID and the embedded RegisteredClaims.Subject both carry the
	// `sub` tag. The shallower field wins in encoding/json, so `sub` is
	// written from and parsed back into UserID, and claims.Subject is always
	// empty after a Verify — issue() setting it has no effect on the wire.
	// Harmless today (both hold the same value) and pinned here so a future
	// reader of claims.Subject finds out from a test rather than from a nil
	// tenant at runtime.
	if claims.Subject != "" {
		t.Errorf("Subject = %q; the embedded field is shadowed by UserID and should stay empty", claims.Subject)
	}
	if claims.Issuer != "argentum" {
		t.Errorf("Issuer = %q, want argentum", claims.Issuer)
	}
	if claims.ExpiresAt == nil || claims.IssuedAt == nil || claims.NotBefore == nil {
		t.Fatal("exp / iat / nbf must all be set")
	}
	if got := claims.ExpiresAt.Sub(claims.IssuedAt.Time); got != 15*time.Minute {
		t.Errorf("exp - iat = %v, want the access TTL of 15m", got)
	}

	// The wire payload is the contract the dashboard and any future consumer
	// read, so assert the claim names directly rather than only through the
	// struct that shadows one of them.
	payload := decodePayload(t, raw)
	for claim, want := range map[string]string{
		"sub":  "user-1",
		"cid":  "co-1",
		"role": "admin",
		"typ":  "access",
		"iss":  "argentum",
	} {
		if got, _ := payload[claim].(string); got != want {
			t.Errorf("payload[%q] = %v, want %q", claim, payload[claim], want)
		}
	}
}

func decodePayload(t *testing.T, raw string) map[string]any {
	t.Helper()
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d segments, want 3", len(parts))
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return out
}

func TestIssueRefreshTokenUsesTheRefreshTTL(t *testing.T) {
	s := newSigner(t)

	raw, err := s.IssueRefreshToken("user-1", "co-1", "member")
	if err != nil {
		t.Fatalf("IssueRefreshToken: %v", err)
	}
	claims, err := s.Verify(raw)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.TokenType != "refresh" {
		t.Errorf("TokenType = %q, want refresh", claims.TokenType)
	}
	if got := claims.ExpiresAt.Sub(claims.IssuedAt.Time); got != 7*24*time.Hour {
		t.Errorf("exp - iat = %v, want the refresh TTL of 168h", got)
	}
}

// The two token types are distinguishable in the claims, which is the whole
// basis on which middleware.Auth rejects a refresh token on an API route and
// AuthService.Refresh rejects an access token on the refresh route. If `typ`
// stopped round-tripping, both checks would pass everything.
func TestTokenTypeDistinguishesAccessFromRefresh(t *testing.T) {
	s := newSigner(t)

	access, err := s.IssueAccessToken("user-1", "co-1", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	refresh, err := s.IssueRefreshToken("user-1", "co-1", "admin")
	if err != nil {
		t.Fatalf("IssueRefreshToken: %v", err)
	}
	if access == refresh {
		t.Fatal("the access and refresh tokens are the same string")
	}

	accessClaims, err := s.Verify(access)
	if err != nil {
		t.Fatalf("Verify(access): %v", err)
	}
	refreshClaims, err := s.Verify(refresh)
	if err != nil {
		t.Fatalf("Verify(refresh): %v", err)
	}
	if accessClaims.TokenType == refreshClaims.TokenType {
		t.Fatalf("both tokens carry typ=%q", accessClaims.TokenType)
	}
}

func TestVerifyRejectsAnExpiredToken(t *testing.T) {
	s := newSigner(t)

	// issue() is used directly because NewTokenSigner clamps a non-positive
	// TTL up to its default — there is no other way to mint an already-dead
	// token without sleeping through a real one.
	raw, err := s.issue("user-1", "co-1", "admin", "access", -time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	claims, err := s.Verify(raw)
	if err == nil {
		t.Fatalf("Verify returned claims for an expired token: %+v", claims)
	}
	if claims != nil {
		t.Errorf("claims = %+v on failure, want nil", claims)
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("err = %q, want it to mention expiry", err)
	}
}

func TestVerifyRejectsATokenSignedWithAnotherSecret(t *testing.T) {
	s := newSigner(t)
	other, err := NewTokenSigner("ffffffffffffffffffffffffffffffff", time.Minute, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}

	raw, err := other.IssueAccessToken("user-1", "co-1", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	if _, err := s.Verify(raw); err == nil {
		t.Fatal("Verify accepted a token signed with a different secret")
	}
}

func TestVerifyRejectsAlgNone(t *testing.T) {
	// The classic JWT downgrade: re-sign the claims with alg=none and the
	// keyfunc hands back the secret anyway unless it checks the method. This
	// is the branch that check exists for.
	s := newSigner(t)

	now := time.Now()
	forged := jwt.NewWithClaims(jwt.SigningMethodNone, Claims{
		UserID:    "attacker",
		CompanyID: "someone-elses-company",
		Role:      "admin",
		TokenType: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "argentum",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			Subject:   "attacker",
		},
	})
	raw, err := forged.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("signing the forgery: %v", err)
	}

	claims, err := s.Verify(raw)
	if err == nil {
		t.Fatalf("Verify accepted an alg=none token: %+v", claims)
	}
	if !strings.Contains(err.Error(), "signing method") {
		t.Errorf("err = %q, want it to name the signing method", err)
	}
}

func TestVerifyRejectsTamperedAndMalformedTokens(t *testing.T) {
	s := newSigner(t)

	valid, err := s.IssueAccessToken("user-1", "co-1", "member")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	parts := strings.Split(valid, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d segments, want 3", len(parts))
	}

	// Flip one character of the payload — the segment carrying role and
	// company_id, i.e. the one an attacker would edit.
	tamperedPayload := []byte(parts[1])
	tamperedPayload[0] ^= 0x01
	tampered := parts[0] + "." + string(tamperedPayload) + "." + parts[2]

	cases := []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"not a jwt", "hunter2"},
		{"two segments", parts[0] + "." + parts[1]},
		{"four segments", valid + "." + parts[2]},
		{"tampered payload", tampered},
		{"truncated signature", parts[0] + "." + parts[1] + "." + parts[2][:len(parts[2])-2]},
		{"empty signature", parts[0] + "." + parts[1] + "."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Verify panicked on %s: %v", tc.name, r)
				}
			}()
			claims, err := s.Verify(tc.raw)
			if err == nil {
				t.Fatalf("Verify(%s) accepted the token: %+v", tc.name, claims)
			}
			if claims != nil {
				t.Errorf("claims = %+v on failure, want nil", claims)
			}
		})
	}
}
