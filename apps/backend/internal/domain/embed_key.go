package domain

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Embed keys (T-19): the credential a tenant's own web page holds.
//
// The asymmetry with APIKey is the whole design. An API key is a secret held by
// a server and it authorises by itself. An embed key's public half is *printed
// in someone else's HTML*, so it identifies and authorises nothing; what
// authorises a session is two things the page cannot forge on its own — a
// request from an origin the tenant allowlisted, and an HMAC their backend
// computed over the identity being asserted.
//
// There is deliberately no Scopes field. An embed session reaches exactly the
// `/api/embed` routes T-20 registers and nothing else, so a per-key capability
// set would be a second way to express a decision the route table already
// makes, and the two would drift.

// EmbedKey is a company-scoped, browser-visible credential.
//
// SecretEnc is `json:"-"` for the reason APIKey.KeyHash is: no handler can leak
// it by returning the record. Unlike an API key's hash, this one is reversible
// — see the 051 migration for why an HMAC forces that — which makes keeping it
// off every response shape more load-bearing here, not less.
type EmbedKey struct {
	ID        string `json:"id"`
	CompanyID string `json:"company_id"`
	Name      string `json:"name"`
	// ClientKey is `argw_pub_<hex>`. Public by design: it is shown in the
	// dashboard in full, it ships in the tenant's page source, and it is the
	// column the session mint looks a key up by.
	ClientKey string `json:"client_key"`
	SecretEnc []byte `json:"-"`
	// AllowedOrigins are exact `scheme://host[:port]` strings. Never a suffix
	// pattern and never `*` — see NormalizeOrigins.
	AllowedOrigins []string `json:"allowed_origins"`
	Enabled        bool     `json:"enabled"`
	CreatedBy      string   `json:"created_by,omitempty"`
	// LastUsedAt is written at most once a minute per key. It answers "is any
	// page still using this?" before a revoke, which is the only question it
	// has to be accurate enough for.
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	// Status is derived, not stored. On the struct so the dashboard does not
	// re-implement the three-way read of two fields.
	Status string `json:"status"`
}

// Embed key status values. Revoked outranks disabled: somebody made a permanent
// decision, and that is the more useful fact to show.
const (
	EmbedKeyActive   = "active"
	EmbedKeyDisabled = "disabled"
	EmbedKeyRevoked  = "revoked"
)

// Usable reports whether the key may mint a session. There is no expiry field:
// an embed key's short-lived output *is* the expiry, and a key that stops
// working at 3am takes a tenant's whole site with it.
func (k *EmbedKey) Usable() bool { return k.RevokedAt == nil && k.Enabled }

// StatusAt derives the display status.
func (k *EmbedKey) StatusAt() string {
	switch {
	case k.RevokedAt != nil:
		return EmbedKeyRevoked
	case !k.Enabled:
		return EmbedKeyDisabled
	default:
		return EmbedKeyActive
	}
}

// AllowsOrigin reports whether raw matches one of the key's origins exactly.
//
// **Exact, case-insensitive on scheme and host, never a suffix test.** The
// failure this closes is the one every embed product ships once:
// `strings.HasSuffix(origin, "acme.com")` admits `https://evil-acme.com`, and
// an attacker who can register a domain then holds a valid session for somebody
// else's tenant. Both sides are normalised through the same parser so that a
// stored `https://acme.com:443` and a sent `https://acme.com` are one origin
// rather than two, which is otherwise a support ticket nobody can diagnose.
func (k *EmbedKey) AllowsOrigin(raw string) bool {
	want, err := CanonicalOrigin(raw)
	if err != nil {
		return false
	}
	for _, o := range k.AllowedOrigins {
		if got, err := CanonicalOrigin(o); err == nil && got == want {
			return true
		}
	}
	return false
}

// CanonicalOrigin reduces an origin to `scheme://host[:port]`, lowercased, with
// the scheme's default port removed. It rejects anything that is not an origin:
// a path, a query, a credential, a wildcard, or a scheme that is not http(s).
//
// `http://localhost…` is allowed and `http://` anywhere else is not. A tenant
// develops against localhost and there is no way for them to have TLS there,
// while an `http` origin in production means the session token crosses the
// network in clear text — which is exactly the credential this whole ticket
// exists to protect.
func CanonicalOrigin(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("%w: an origin is required", ErrInvalidInput)
	}
	if strings.Contains(s, "*") {
		return "", fmt.Errorf("%w: %q — a wildcard origin is not allowed, list each site in full", ErrInvalidInput, raw)
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("%w: %q is not a valid origin", ErrInvalidInput, raw)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("%w: %q — an origin must start with https:// (or http:// for localhost)", ErrInvalidInput, raw)
	}
	if u.Host == "" {
		return "", fmt.Errorf("%w: %q has no host", ErrInvalidInput, raw)
	}
	if u.User != nil {
		return "", fmt.Errorf("%w: %q — an origin carries no credentials", ErrInvalidInput, raw)
	}
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("%w: %q — an origin is scheme://host, with no path", ErrInvalidInput, raw)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%w: %q — an origin carries no query or fragment", ErrInvalidInput, raw)
	}

	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", fmt.Errorf("%w: %q has no host", ErrInvalidInput, raw)
	}
	if scheme == "http" && !isLoopbackHost(host) {
		return "", fmt.Errorf("%w: %q — http is allowed only for localhost; a session token on a plain-text origin is a session token in transit", ErrInvalidInput, raw)
	}

	port := u.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if port == "" {
		return scheme + "://" + host, nil
	}
	return scheme + "://" + host + ":" + port, nil
}

// isLoopbackHost is the http exemption's whole surface. Named hosts only —
// `127.0.0.1` and `::1` are here, and a private-range address like
// `http://10.0.0.5` is not, because "our internal network" is exactly the claim
// an SSRF-adjacent mistake makes on somebody's behalf.
func isLoopbackHost(host string) bool {
	switch host {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	return strings.HasSuffix(host, ".localhost")
}

// NormalizeOrigins validates, canonicalises and de-duplicates an allowlist.
//
// An empty result is an error rather than an empty list, and this is the
// ticket's own acceptance criterion: a key with no origins would either mint
// for nobody (a broken integration with no message) or, if the check were ever
// written as "no list means no restriction", mint for everybody. Making it
// impossible to store removes the second reading permanently.
func NormalizeOrigins(raw []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		if strings.TrimSpace(r) == "" {
			continue
		}
		o, err := CanonicalOrigin(r)
		if err != nil {
			return nil, err
		}
		if seen[o] {
			continue
		}
		seen[o] = true
		out = append(out, o)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: list at least one site this key may be used from", ErrInvalidInput)
	}
	return out, nil
}

// EmbedKeyRepository is the persistence contract.
//
// Like APIKeyRepository there is no secret rotation: re-signing is the tenant's
// backend's job and a rotated secret invalidates every page holding the old
// one. Mint a second key, move the site, revoke the first.
type EmbedKeyRepository interface {
	Create(ctx context.Context, k *EmbedKey) error
	// GetByClientKey returns the key whether or not it is revoked or disabled.
	// The caller decides, which is what lets the mint distinguish the cases in
	// its own log while answering all of them identically.
	GetByClientKey(ctx context.Context, clientKey string) (*EmbedKey, error)
	GetByID(ctx context.Context, companyID, id string) (*EmbedKey, error)
	ListByCompany(ctx context.Context, companyID string) ([]*EmbedKey, error)
	// Update carries the two mutable fields — the origin allowlist and the
	// enabled switch. Company-scoped so an id from another tenant is a
	// not-found rather than a cross-tenant edit.
	Update(ctx context.Context, companyID, id string, origins []string, enabled bool) error
	Revoke(ctx context.Context, companyID, id string, at time.Time) error
	TouchLastUsed(ctx context.Context, id string, at time.Time) error
}
