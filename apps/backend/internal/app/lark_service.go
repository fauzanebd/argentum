package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fauzanebd/argentum/internal/crypto"
	"github.com/fauzanebd/argentum/internal/domain"
)

// LarkService is the use-case layer for per-tenant Lark configuration and
// user allowlist management. No reload signaling: outbound delivery happens
// inside the worker (lark.Client) which refreshes credentials lazily on the
// next call (or on a 401 retry).
type LarkService struct {
	creds  domain.CompanyLarkCredentialRepository
	users  domain.AllowedLarkUserRepository
	cipher *crypto.DSNCipher
}

func NewLarkService(
	creds domain.CompanyLarkCredentialRepository,
	users domain.AllowedLarkUserRepository,
	cipher *crypto.DSNCipher,
) *LarkService {
	return &LarkService{creds: creds, users: users, cipher: cipher}
}

// SaveLarkCredentialsInput is the upsert payload from the dashboard.
type SaveLarkCredentialsInput struct {
	AppID             string
	AppSecret         string // plaintext; encrypted before persist. Empty on rotation keeps existing.
	VerificationToken string
	EncryptKey        string
	BotOpenID         string
	Enabled           bool
}

// SaveCredentials upserts a tenant's Lark app config. AppSecret is required
// on first save; an empty AppSecret on subsequent calls keeps the existing
// encrypted value (so the dashboard can flip Enabled or rotate the bot's
// open_id without re-entering the secret).
func (s *LarkService) SaveCredentials(ctx context.Context, companyID string, in SaveLarkCredentialsInput) (*domain.CompanyLarkCredential, error) {
	in.AppID = strings.TrimSpace(in.AppID)
	in.AppSecret = strings.TrimSpace(in.AppSecret)
	in.VerificationToken = strings.TrimSpace(in.VerificationToken)
	in.EncryptKey = strings.TrimSpace(in.EncryptKey)
	in.BotOpenID = strings.TrimSpace(in.BotOpenID)
	if in.AppID == "" {
		return nil, fmt.Errorf("%w: app_id required", domain.ErrInvalidInput)
	}
	if in.VerificationToken == "" {
		return nil, fmt.Errorf("%w: verification_token required", domain.ErrInvalidInput)
	}

	row := &domain.CompanyLarkCredential{
		CompanyID:         companyID,
		AppID:             in.AppID,
		VerificationToken: in.VerificationToken,
		EncryptKey:        in.EncryptKey,
		BotOpenID:         in.BotOpenID,
		Enabled:           in.Enabled,
	}

	if in.AppSecret != "" {
		enc, err := s.cipher.Encrypt(in.AppSecret)
		if err != nil {
			return nil, fmt.Errorf("encrypt app_secret: %w", err)
		}
		row.AppSecretEncrypted = enc
	} else {
		existing, err := s.creds.Get(ctx, companyID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return nil, fmt.Errorf("%w: app_secret required on first save", domain.ErrInvalidInput)
			}
			return nil, err
		}
		row.AppSecretEncrypted = existing.AppSecretEncrypted
	}

	if err := s.creds.Upsert(ctx, row); err != nil {
		return nil, err
	}
	row.AppSecretEncrypted = nil
	return row, nil
}

// GetCredentials returns the row with the encrypted secret stripped.
func (s *LarkService) GetCredentials(ctx context.Context, companyID string) (*domain.CompanyLarkCredential, error) {
	row, err := s.creds.Get(ctx, companyID)
	if err != nil {
		return nil, err
	}
	row.AppSecretEncrypted = nil
	return row, nil
}

// DeleteCredentials drops a tenant's Lark configuration.
func (s *LarkService) DeleteCredentials(ctx context.Context, companyID string) error {
	return s.creds.Delete(ctx, companyID)
}

// AddUser adds a Lark open_id to the company's allowlist.
func (s *LarkService) AddUser(ctx context.Context, companyID, larkOpenID, label string) error {
	larkOpenID = strings.TrimSpace(larkOpenID)
	if larkOpenID == "" {
		return fmt.Errorf("%w: lark_open_id required", domain.ErrInvalidInput)
	}
	return s.users.Add(ctx, &domain.AllowedLarkUser{
		CompanyID:  companyID,
		LarkOpenID: larkOpenID,
		Label:      label,
	})
}

// RemoveUser drops a Lark open_id from the allowlist.
func (s *LarkService) RemoveUser(ctx context.Context, companyID, larkOpenID string) error {
	return s.users.Remove(ctx, companyID, larkOpenID)
}

// ListUsers returns the company's Lark allowlist.
func (s *LarkService) ListUsers(ctx context.Context, companyID string) ([]*domain.AllowedLarkUser, error) {
	return s.users.ListByCompany(ctx, companyID)
}

// IsUserAllowed reports whether a Lark open_id is permitted to chat with the
// agent on behalf of the company.
func (s *LarkService) IsUserAllowed(ctx context.Context, companyID, larkOpenID string) (bool, error) {
	return s.users.IsAllowed(ctx, companyID, larkOpenID)
}

// ResolveCompanyByAppID looks up the company that owns a Lark app_id.
// Used by the events webhook to find the right verification token and
// encrypt key without trusting other fields in the payload.
func (s *LarkService) ResolveCompanyByAppID(ctx context.Context, appID string) (*domain.CompanyLarkCredential, error) {
	return s.creds.GetByAppID(ctx, appID)
}
