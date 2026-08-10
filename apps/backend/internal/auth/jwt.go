package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the JWT payload carried in the access and refresh tokens.
// company_id is duplicated alongside subject so middleware can populate the
// tenant context without a DB hit.
type Claims struct {
	UserID    string `json:"sub"`
	CompanyID string `json:"cid"`
	Role      string `json:"role"`
	TokenType string `json:"typ"` // "access" or "refresh"
	jwt.RegisteredClaims
}

// TokenSigner issues and verifies HS256-signed JWTs.
type TokenSigner struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
}

// NewTokenSigner constructs a signer. accessTTL is typically 15 min;
// refreshTTL typically 7 days.
func NewTokenSigner(secret string, accessTTL, refreshTTL time.Duration) (*TokenSigner, error) {
	if len(secret) < 32 {
		return nil, errors.New("JWT_SECRET must be at least 32 chars")
	}
	if accessTTL <= 0 {
		accessTTL = 15 * time.Minute
	}
	if refreshTTL <= 0 {
		refreshTTL = 7 * 24 * time.Hour
	}
	return &TokenSigner{secret: []byte(secret), accessTTL: accessTTL, refreshTTL: refreshTTL}, nil
}

// IssueAccessToken returns a fresh access token bound to the given identity.
func (s *TokenSigner) IssueAccessToken(userID, companyID, role string) (string, error) {
	return s.issue(userID, companyID, role, "access", s.accessTTL)
}

// IssueRefreshToken returns a fresh refresh token. Stored client-side as an
// httpOnly cookie.
func (s *TokenSigner) IssueRefreshToken(userID, companyID, role string) (string, error) {
	return s.issue(userID, companyID, role, "refresh", s.refreshTTL)
}

func (s *TokenSigner) issue(userID, companyID, role, typ string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:    userID,
		CompanyID: companyID,
		Role:      role,
		TokenType: typ,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "argentum",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			NotBefore: jwt.NewNumericDate(now),
			Subject:   userID,
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(s.secret)
}

// Verify parses and validates a token, returning its claims if valid.
func (s *TokenSigner) Verify(raw string) (*Claims, error) {
	c := &Claims{}
	tok, err := jwt.ParseWithClaims(raw, c, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.secret, nil
	})
	if err != nil {
		return nil, err
	}
	if !tok.Valid {
		return nil, errors.New("invalid token")
	}
	return c, nil
}

// EmbedTokenType is the `typ` on an embed session token (T-19). It exists so
// that the two token families cannot be substituted for each other: Auth
// refuses anything whose typ is not "access", and EmbedAuth refuses anything
// whose typ is not this. One signing secret, two vocabularies, and neither
// middleware can be satisfied by the other's token.
const EmbedTokenType = "embed"

// EmbedClaims is the payload of an embed session token.
//
// **There is no user id and no role on it, deliberately.** The subject is a
// string the *tenant* chose for one of their own people; Argentum has no
// account for them, no role to check and no membership to look up. A claim
// shaped like `sub`+`role` would be the beginning of an embed session growing
// into a dashboard session, and the day it does, every AdminOnly route inherits
// a caller who never signed in here.
type EmbedClaims struct {
	CompanyID string `json:"cid"`
	// EmbedUserRef is the tenant's own identifier for the human. It keys the
	// thread (T-20) and it is what an audit row attributes to.
	EmbedUserRef string `json:"ref"`
	// KeyID names the embed key that minted this session, so revoking a key can
	// be reasoned about and an audit row can say which site the turn came from.
	KeyID     string `json:"kid"`
	TokenType string `json:"typ"`
	jwt.RegisteredClaims
}

// IssueEmbedToken mints a session for one asserted visitor. ttl is short by
// construction — the caller passes minutes, not days — because this token
// travels to a browser we do not control, on a page we did not write.
func (s *TokenSigner) IssueEmbedToken(companyID, embedUserRef, keyID string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := EmbedClaims{
		CompanyID:    companyID,
		EmbedUserRef: embedUserRef,
		KeyID:        keyID,
		TokenType:    EmbedTokenType,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "argentum",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			NotBefore: jwt.NewNumericDate(now),
			// **No Subject, deliberately.** `sub` is the claim Claims.UserID
			// reads, so anything written here — even a namespaced
			// `embed:<ref>` — becomes a user id the moment any code parses this
			// token with the dashboard's own struct. middleware.Auth refuses an
			// embed token on `typ` before it reads a user id, but that is one
			// check standing between a website visitor and an identity, and
			// leaving `sub` empty removes the second half of the problem
			// permanently. The identity lives in `ref`, which no dashboard
			// claim reads.
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.secret)
}

// VerifyEmbed parses an embed session token and refuses anything that is not
// one. The typ check is inside this function rather than in the middleware so
// that no future caller can verify an embed token without it.
func (s *TokenSigner) VerifyEmbed(raw string) (*EmbedClaims, error) {
	c := &EmbedClaims{}
	tok, err := jwt.ParseWithClaims(raw, c, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.secret, nil
	})
	if err != nil {
		return nil, err
	}
	if !tok.Valid {
		return nil, errors.New("invalid token")
	}
	if c.TokenType != EmbedTokenType {
		return nil, errors.New("not an embed token")
	}
	if c.CompanyID == "" || c.EmbedUserRef == "" {
		return nil, errors.New("embed token is missing its identity")
	}
	return c, nil
}

// AccessTTL exposes the configured access TTL for cookie expiry alignment.
func (s *TokenSigner) AccessTTL() time.Duration { return s.accessTTL }

// RefreshTTL exposes the configured refresh TTL.
func (s *TokenSigner) RefreshTTL() time.Duration { return s.refreshTTL }
