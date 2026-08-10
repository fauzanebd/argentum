package auth

import (
	"strings"
	"testing"
	"time"
)

func TestNewEmbedKeyShape(t *testing.T) {
	clientKey, secret, err := NewEmbedKey()
	if err != nil {
		t.Fatalf("NewEmbedKey: %v", err)
	}
	if !strings.HasPrefix(clientKey, "argw_pub_") {
		t.Errorf("client key %q does not carry the public marker — a secret scanner and a human both read that prefix", clientKey)
	}
	if !ValidEmbedClientKey(clientKey) {
		t.Errorf("ValidEmbedClientKey(%q) = false on a key we just minted", clientKey)
	}
	if len(secret) != 64 { // 32 bytes, hex
		t.Errorf("secret is %d chars, want 64 (32 bytes hex)", len(secret))
	}
	if strings.Contains(clientKey, secret) {
		t.Error("the public half contains the secret")
	}

	other, _, err := NewEmbedKey()
	if err != nil {
		t.Fatalf("NewEmbedKey: %v", err)
	}
	if other == clientKey {
		t.Error("two mints produced the same client key")
	}
}

func TestValidEmbedClientKeyRejects(t *testing.T) {
	cases := []struct{ name, in string }{
		{"empty", ""},
		{"api key", "arg_0123456789_abcdef"},
		{"wrong marker", "argw_sec_" + strings.Repeat("a", 32)},
		{"too short", "argw_pub_" + strings.Repeat("a", 31)},
		{"too long", "argw_pub_" + strings.Repeat("a", 33)},
		{"not hex", "argw_pub_" + strings.Repeat("z", 32)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if ValidEmbedClientKey(tc.in) {
				t.Errorf("ValidEmbedClientKey(%q) = true, want false", tc.in)
			}
		})
	}
}

// TestEmbedSignatureBindsIdentityAndDeadline proves the signed string is both
// halves. Signing only the ref would make a signature eternal; signing only the
// exp would make one signature stand in for any user.
func TestEmbedSignatureBindsIdentityAndDeadline(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	exp := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC).Unix()

	sig := EmbedSignature(secret, "emp_812", exp)
	if sig == "" {
		t.Fatal("empty signature")
	}
	if !EmbedSignatureValid(secret, "emp_812", exp, sig) {
		t.Fatal("a signature we just computed does not verify")
	}
	// Whitespace from a copy-paste out of a config file is not a forgery.
	if !EmbedSignatureValid(secret, "emp_812", exp, "  "+sig+"\n") {
		t.Error("a signature with surrounding whitespace was refused")
	}

	cases := []struct {
		name, secret, ref string
		exp               int64
		sig               string
	}{
		{"different user", secret, "emp_813", exp, sig},
		{"different deadline", secret, "emp_812", exp + 1, sig},
		{"different secret", strings.Repeat("f", 64), "emp_812", exp, sig},
		{"empty signature", secret, "emp_812", exp, ""},
		{"truncated signature", secret, "emp_812", exp, sig[:len(sig)-1]},
		{"flipped last character", secret, "emp_812", exp, flipLast(sig)},
		// The separator must not be movable: `("a:b", 1)` and `("a", ":b:1")`
		// are different assertions and must not collide.
		{"separator shifted", secret, "emp", 812, EmbedSignature(secret, "emp:812", exp)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if EmbedSignatureValid(tc.secret, tc.ref, tc.exp, tc.sig) {
				t.Errorf("a forged assertion verified: %s", tc.name)
			}
		})
	}
}

func flipLast(s string) string {
	if s == "" {
		return s
	}
	last := s[len(s)-1]
	if last == 'a' {
		return s[:len(s)-1] + "b"
	}
	return s[:len(s)-1] + "a"
}

// TestEmbedAndAccessTokensAreNotInterchangeable is the acceptance criterion
// that keeps an embed session out of the dashboard. Both families are signed
// with the same secret, so nothing but the `typ` check separates them.
func TestEmbedAndAccessTokensAreNotInterchangeable(t *testing.T) {
	signer, err := NewTokenSigner(strings.Repeat("k", 48), 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}

	embedTok, err := signer.IssueEmbedToken("company-1", "emp_812", "key-1", 15*time.Minute)
	if err != nil {
		t.Fatalf("IssueEmbedToken: %v", err)
	}
	accessTok, err := signer.IssueAccessToken("user-1", "company-1", "admin")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	// The embed token verifies as itself, and carries no user and no role.
	claims, err := signer.VerifyEmbed(embedTok)
	if err != nil {
		t.Fatalf("VerifyEmbed on our own token: %v", err)
	}
	if claims.CompanyID != "company-1" || claims.EmbedUserRef != "emp_812" || claims.KeyID != "key-1" {
		t.Errorf("claims = %+v, want the identity we issued", claims)
	}
	if claims.TokenType != EmbedTokenType {
		t.Errorf("typ = %q, want %q", claims.TokenType, EmbedTokenType)
	}

	// An embed token presented to the dashboard's verifier parses — same secret
	// — and is refused by the middleware's `typ != "access"` check. Proven here
	// at the claim level; middleware/embedauth_test.go proves the refusal.
	dash, err := signer.Verify(embedTok)
	if err != nil {
		t.Fatalf("Verify(embed token): %v", err)
	}
	if dash.TokenType == "access" {
		t.Fatal("an embed token claims to be an access token — middleware.Auth would admit it")
	}
	if dash.UserID != "" {
		t.Errorf("an embed token carries a user id (%q); every handler reading one would attribute a stranger's turn to it", dash.UserID)
	}
	if dash.Role != "" {
		t.Errorf("an embed token carries role %q; RequireRole would admit a website visitor", dash.Role)
	}

	// And the other direction: a dashboard session is not an embed session.
	if _, err := signer.VerifyEmbed(accessTok); err == nil {
		t.Fatal("VerifyEmbed accepted a dashboard access token")
	}
}

func TestVerifyEmbedRejectsExpiredAndForged(t *testing.T) {
	signer, err := NewTokenSigner(strings.Repeat("k", 48), 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}
	other, err := NewTokenSigner(strings.Repeat("j", 48), 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}

	expired, err := signer.IssueEmbedToken("company-1", "emp_812", "key-1", -1*time.Minute)
	if err != nil {
		t.Fatalf("IssueEmbedToken: %v", err)
	}
	if _, err := signer.VerifyEmbed(expired); err == nil {
		t.Error("VerifyEmbed accepted an expired session")
	}

	foreign, err := other.IssueEmbedToken("company-1", "emp_812", "key-1", 15*time.Minute)
	if err != nil {
		t.Fatalf("IssueEmbedToken: %v", err)
	}
	if _, err := signer.VerifyEmbed(foreign); err == nil {
		t.Error("VerifyEmbed accepted a token signed with another deployment's secret")
	}

	for _, raw := range []string{"", "not-a-jwt", expired + "x"} {
		if _, err := signer.VerifyEmbed(raw); err == nil {
			t.Errorf("VerifyEmbed(%q) accepted a malformed token", raw)
		}
	}
}
