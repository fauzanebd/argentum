package auth

import (
	"strings"
	"testing"
)

// TestNewAPIKeyShape pins the format the whole ticket depends on. The marker
// is what makes a leaked key recognisable in a log or a secret scanner, and
// the prefix is what authentication looks a key up by — neither can change
// without breaking every key already issued.
func TestNewAPIKeyShape(t *testing.T) {
	token, prefix, hash, err := NewAPIKey()
	if err != nil {
		t.Fatalf("NewAPIKey: %v", err)
	}
	if !strings.HasPrefix(token, "arg_") {
		t.Errorf("token %q does not start with the arg_ marker", token)
	}
	if len(prefix) != 2*apiKeyPrefixBytes {
		t.Errorf("prefix %q is %d chars, want %d", prefix, len(prefix), 2*apiKeyPrefixBytes)
	}
	gotPrefix, secret, ok := ParseAPIKey(token)
	if !ok {
		t.Fatalf("ParseAPIKey rejected a token this package minted: %q", token)
	}
	if gotPrefix != prefix {
		t.Errorf("parsed prefix %q, want %q", gotPrefix, prefix)
	}
	if HashAPIKeySecret(secret) != hash {
		t.Error("the returned hash is not the hash of the returned secret")
	}
	if strings.Contains(token, secret) && len(secret) < 40 {
		t.Errorf("secret %q is shorter than 32 bytes of base64url", secret)
	}
}

// TestNewAPIKeyIsUnique is a smoke test against a mistake that would be
// invisible until two tenants collided: a seeded or reused generator.
func TestNewAPIKeyIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		_, prefix, hash, err := NewAPIKey()
		if err != nil {
			t.Fatalf("NewAPIKey: %v", err)
		}
		if seen[prefix] {
			t.Fatalf("prefix %q minted twice", prefix)
		}
		if seen[hash] {
			t.Fatalf("hash %q minted twice", hash)
		}
		seen[prefix], seen[hash] = true, true
	}
}

// TestParseAPIKey covers the shapes an Authorization header can actually
// carry. The underscore case is the one that matters: base64url includes `_`,
// so a split on every underscore truncates roughly half of all secrets — and
// it would do so intermittently, which is the worst way for it to fail.
func TestParseAPIKey(t *testing.T) {
	cases := []struct {
		name       string
		token      string
		wantOK     bool
		wantPrefix string
		wantSecret string
	}{
		{"well formed", "arg_0123456789_abcdef", true, "0123456789", "abcdef"},
		{"secret containing underscores", "arg_0123456789_ab_cd_ef", true, "0123456789", "ab_cd_ef"},
		{"surrounding whitespace", "  arg_0123456789_abcdef  ", true, "0123456789", "abcdef"},
		{"empty", "", false, "", ""},
		{"a dashboard JWT", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.e30.sig", false, "", ""},
		{"wrong marker", "sk_0123456789_abcdef", false, "", ""},
		{"no secret half", "arg_0123456789", false, "", ""},
		{"empty secret", "arg_0123456789_", false, "", ""},
		{"empty prefix", "arg__abcdef", false, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prefix, secret, ok := ParseAPIKey(tc.token)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if prefix != tc.wantPrefix || secret != tc.wantSecret {
				t.Errorf("got (%q, %q), want (%q, %q)", prefix, secret, tc.wantPrefix, tc.wantSecret)
			}
		})
	}
}

// TestAPIKeySecretMatches is the check that stands between a guessed secret
// and a tenant's data.
func TestAPIKeySecretMatches(t *testing.T) {
	hash := HashAPIKeySecret("the-secret")
	if !APIKeySecretMatches("the-secret", hash) {
		t.Error("the correct secret did not match its own hash")
	}
	if APIKeySecretMatches("the-secreT", hash) {
		t.Error("a one-character difference matched")
	}
	if APIKeySecretMatches("", hash) {
		t.Error("an empty secret matched")
	}
	if APIKeySecretMatches("the-secret", "") {
		t.Error("a corrupt (empty) stored hash matched")
	}
}
