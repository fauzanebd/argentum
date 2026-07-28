// Package app holds use-case orchestration: services that compose domain
// repositories with crypto + token primitives. HTTP handlers should be a thin
// wrapper around these services.
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/domain"
)

// AuthService implements the signup + login use cases.
type AuthService struct {
	companies domain.CompanyRepository
	users     domain.UserRepository
	signer    *auth.TokenSigner
}

func NewAuthService(c domain.CompanyRepository, u domain.UserRepository, s *auth.TokenSigner) *AuthService {
	return &AuthService{companies: c, users: u, signer: s}
}

// SignupResult bundles the freshly created company + user with an access /
// refresh token pair.
type SignupResult struct {
	Company      *domain.Company `json:"company"`
	User         *domain.User    `json:"user"`
	AccessToken  string          `json:"access_token"`
	RefreshToken string          `json:"refresh_token"`
}

// LoginResult is returned from a successful login.
type LoginResult struct {
	User         *domain.User `json:"user"`
	CompanyID    string       `json:"company_id"`
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"refresh_token"`
}

// Signup creates a new company + admin user atomically, then issues a token
// pair. companyName + email + password are required.
func (s *AuthService) Signup(ctx context.Context, companyName, email, password string) (*SignupResult, error) {
	companyName = strings.TrimSpace(companyName)
	email = strings.ToLower(strings.TrimSpace(email))
	if companyName == "" || email == "" || password == "" {
		return nil, fmt.Errorf("%w: company name, email, password required", domain.ErrInvalidInput)
	}
	if err := validatePassword(password); err != nil {
		return nil, err
	}

	if existing, err := s.users.GetByEmail(ctx, email); err == nil && existing != nil {
		return nil, domain.ErrAlreadyExists
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}

	company := &domain.Company{
		Name:            companyName,
		Slug:            slugify(companyName),
		DefaultCurrency: "USD",
	}
	if err := s.companies.Create(ctx, company); err != nil {
		return nil, fmt.Errorf("create company: %w", err)
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	user := &domain.User{
		CompanyID:    company.ID,
		Email:        email,
		PasswordHash: hash,
		Role:         domain.RoleAdmin,
	}
	if err := s.users.Create(ctx, user); err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}

	access, err := s.signer.IssueAccessToken(user.ID, user.CompanyID, string(user.Role))
	if err != nil {
		return nil, err
	}
	refresh, err := s.signer.IssueRefreshToken(user.ID, user.CompanyID, string(user.Role))
	if err != nil {
		return nil, err
	}
	return &SignupResult{
		Company:      company,
		User:         user,
		AccessToken:  access,
		RefreshToken: refresh,
	}, nil
}

// validatePassword is the one place the password policy lives. Signup and
// invite acceptance both set a password, and a rule enforced in only one of
// them is a rule an attacker picks the other door for.
func validatePassword(password string) error {
	if len(password) < 8 {
		return fmt.Errorf("%w: password must be at least 8 characters", domain.ErrInvalidInput)
	}
	return nil
}

// Login verifies credentials and issues a fresh token pair.
func (s *AuthService) Login(ctx context.Context, email, password string) (*LoginResult, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	user, err := s.users.GetByEmail(ctx, email)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, domain.ErrCredentialsBad
	}
	if err != nil {
		return nil, err
	}
	ok, err := auth.VerifyPassword(password, user.PasswordHash)
	if err != nil || !ok {
		return nil, domain.ErrCredentialsBad
	}
	// Deliberately after the password check: someone who cannot authenticate
	// learns "invalid credentials" and nothing about whether the address is a
	// pending invite or a removed colleague.
	if !user.Active() {
		return nil, domain.ErrAccountInactive
	}
	access, err := s.signer.IssueAccessToken(user.ID, user.CompanyID, string(user.Role))
	if err != nil {
		return nil, err
	}
	refresh, err := s.signer.IssueRefreshToken(user.ID, user.CompanyID, string(user.Role))
	if err != nil {
		return nil, err
	}
	return &LoginResult{
		User:         user,
		CompanyID:    user.CompanyID,
		AccessToken:  access,
		RefreshToken: refresh,
	}, nil
}

// Refresh exchanges a valid refresh token for a fresh access token.
//
// It re-reads the user rather than re-signing the claims it was handed. That
// costs one query per refresh and buys two things RBAC needs: deactivating a
// member ends their session within one access-token lifetime instead of at the
// refresh token's seven-day expiry, and a role change takes effect on the next
// refresh instead of the next login. Access tokens already issued stay valid
// until they expire — 15 minutes is the window, and it is deliberate.
func (s *AuthService) Refresh(ctx context.Context, refreshToken string) (string, error) {
	c, err := s.signer.Verify(refreshToken)
	if err != nil {
		return "", domain.ErrUnauthorized
	}
	if c.TokenType != "refresh" {
		return "", domain.ErrUnauthorized
	}
	user, err := s.users.GetByID(ctx, c.UserID)
	if errors.Is(err, domain.ErrNotFound) {
		return "", domain.ErrUnauthorized
	}
	if err != nil {
		return "", err
	}
	if !user.Active() {
		return "", domain.ErrAccountInactive
	}
	return s.signer.IssueAccessToken(user.ID, user.CompanyID, string(user.Role))
}

// IssueSession mints a token pair for a user the caller has already
// authenticated by another route — today, invite acceptance. Keeping it here
// means "what a session looks like" has one definition.
func (s *AuthService) IssueSession(user *domain.User) (*LoginResult, error) {
	access, err := s.signer.IssueAccessToken(user.ID, user.CompanyID, string(user.Role))
	if err != nil {
		return nil, err
	}
	refresh, err := s.signer.IssueRefreshToken(user.ID, user.CompanyID, string(user.Role))
	if err != nil {
		return nil, err
	}
	return &LoginResult{
		User:         user,
		CompanyID:    user.CompanyID,
		AccessToken:  access,
		RefreshToken: refresh,
	}, nil
}

// slugify returns a URL-safe lowercase slug derived from a company name. We
// don't enforce uniqueness here; callers may need to retry with a suffix on
// collision (out of scope for V1).
func slugify(name string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			prevDash = false
		case !prevDash && b.Len() > 0:
			b.WriteRune('-')
			prevDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "company"
	}
	return out
}
