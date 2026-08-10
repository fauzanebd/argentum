package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// timeNowForTest is a fixed instant. Fixed rather than time.Now() so a failure
// is reproducible from the output alone.
func timeNowForTest() time.Time {
	return time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
}

func TestCanonicalOriginAccepts(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://acme.com", "https://acme.com"},
		{"https://acme.com/", "https://acme.com"},
		{"  https://acme.com  ", "https://acme.com"},
		// Case is not part of an origin's identity for scheme or host.
		{"HTTPS://ACME.COM", "https://acme.com"},
		// The scheme's default port is the same origin as no port at all. A
		// tenant who pastes one and a browser that sends the other must not be
		// two different origins, or the 403 is unexplainable.
		{"https://acme.com:443", "https://acme.com"},
		{"http://localhost:80", "http://localhost"},
		{"https://acme.com:8443", "https://acme.com:8443"},
		{"https://staff.internal.acme.com", "https://staff.internal.acme.com"},
		// Loopback over http is the development exemption.
		{"http://localhost:3002", "http://localhost:3002"},
		{"http://127.0.0.1:5173", "http://127.0.0.1:5173"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := CanonicalOrigin(tc.in)
			if err != nil {
				t.Fatalf("CanonicalOrigin(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("CanonicalOrigin(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCanonicalOriginRejects(t *testing.T) {
	cases := []struct{ name, in string }{
		{"empty", ""},
		{"blank", "   "},
		{"bare wildcard", "*"},
		{"wildcard subdomain", "https://*.acme.com"},
		{"no scheme", "acme.com"},
		{"plain http off localhost", "http://acme.com"},
		{"file scheme", "file:///etc/passwd"},
		{"javascript scheme", "javascript:alert(1)"},
		{"has a path", "https://acme.com/app"},
		{"has a query", "https://acme.com?x=1"},
		{"has credentials", "https://user:pw@acme.com"},
		{"no host", "https://"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CanonicalOrigin(tc.in)
			if err == nil {
				t.Fatalf("CanonicalOrigin(%q) = %q, want an error", tc.in, got)
			}
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("err = %v, want ErrInvalidInput so the handler answers 400", err)
			}
		})
	}
}

// TestAllowsOriginIsNotASuffixTest is the one this whole file exists for. A
// suffix comparison — the obvious implementation — admits an attacker-registered
// domain ending in the tenant's, and that is a full session for somebody else's
// workspace.
func TestAllowsOriginIsNotASuffixTest(t *testing.T) {
	k := &EmbedKey{AllowedOrigins: []string{"https://acme.com", "http://localhost:3002"}}

	allowed := []string{
		"https://acme.com",
		"https://acme.com:443",
		"HTTPS://Acme.com",
		"http://localhost:3002",
	}
	for _, o := range allowed {
		if !k.AllowsOrigin(o) {
			t.Errorf("AllowsOrigin(%q) = false, want true", o)
		}
	}

	refused := []string{
		"https://evil-acme.com",
		"https://acme.com.evil.test",
		"https://notacme.com",
		"https://sub.acme.com", // a subdomain is a different origin, by the spec
		"http://acme.com",      // scheme is part of an origin
		"https://acme.com:8443",
		"http://localhost:5173",
		"",
		"*",
		"null", // what a sandboxed iframe sends
	}
	for _, o := range refused {
		if k.AllowsOrigin(o) {
			t.Errorf("AllowsOrigin(%q) = true, want false", o)
		}
	}
}

// TestNormalizeOriginsRejectsWildcardAndEmpty pins the ticket's acceptance
// criterion: a key with a wildcard or with no origins cannot be saved at all.
func TestNormalizeOriginsRejectsWildcardAndEmpty(t *testing.T) {
	cases := []struct {
		name string
		in   []string
	}{
		{"nil", nil},
		{"empty slice", []string{}},
		{"only blanks", []string{"", "   "}},
		{"bare wildcard", []string{"*"}},
		{"wildcard among valid ones", []string{"https://acme.com", "*"}},
		{"wildcard subdomain", []string{"https://*.acme.com"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeOrigins(tc.in)
			if err == nil {
				t.Fatalf("NormalizeOrigins(%v) = %v, want an error", tc.in, got)
			}
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestNormalizeOriginsCanonicalisesAndDedupes(t *testing.T) {
	got, err := NormalizeOrigins([]string{
		"https://acme.com",
		"https://acme.com:443", // the same origin, written differently
		"HTTPS://ACME.COM/",    // and again
		"  ",
		"http://localhost:3002",
	})
	if err != nil {
		t.Fatalf("NormalizeOrigins: %v", err)
	}
	want := []string{"https://acme.com", "http://localhost:3002"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestEmbedKeyStatus(t *testing.T) {
	now := timeNowForTest()
	cases := []struct {
		name string
		key  EmbedKey
		want string
		use  bool
	}{
		{"fresh", EmbedKey{Enabled: true}, EmbedKeyActive, true},
		{"paused", EmbedKey{Enabled: false}, EmbedKeyDisabled, false},
		{"revoked", EmbedKey{Enabled: true, RevokedAt: &now}, EmbedKeyRevoked, false},
		// Revoked outranks disabled: somebody made the permanent decision.
		{"revoked and paused", EmbedKey{Enabled: false, RevokedAt: &now}, EmbedKeyRevoked, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.key.StatusAt(); got != tc.want {
				t.Errorf("StatusAt() = %q, want %q", got, tc.want)
			}
			if got := tc.key.Usable(); got != tc.use {
				t.Errorf("Usable() = %v, want %v", got, tc.use)
			}
		})
	}
}
