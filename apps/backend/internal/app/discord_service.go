package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/crypto"
	"github.com/fauzanebd/argentum/internal/domain"
)

// DiscordReloadNotifier broadcasts "this company's credentials changed,
// reload the gateway session" signals. The Redis-backed impl in
// internal/transport/eventbus publishes to a fixed channel; cmd/discord
// subscribes and calls SessionManager.Reload.
type DiscordReloadNotifier interface {
	NotifyDiscordReload(ctx context.Context, companyID string) error
}

// DiscordService is the use-case layer for per-tenant Discord configuration
// and user allowlist management.
type DiscordService struct {
	creds    domain.CompanyDiscordCredentialRepository
	users    domain.AllowedDiscordUserRepository
	cipher   *crypto.DSNCipher
	notifier DiscordReloadNotifier // optional; nil disables hot reload
}

func NewDiscordService(
	creds domain.CompanyDiscordCredentialRepository,
	users domain.AllowedDiscordUserRepository,
	cipher *crypto.DSNCipher,
	notifier DiscordReloadNotifier,
) *DiscordService {
	return &DiscordService{creds: creds, users: users, cipher: cipher, notifier: notifier}
}

// SaveCredentialsInput is the upsert payload from the dashboard.
type SaveCredentialsInput struct {
	ApplicationID string
	PublicKey     string
	BotToken      string // plaintext; encrypted before persist
	GuildID       string
	Enabled       bool
}

// SaveCredentials upserts a tenant's Discord bot config. BotToken is required
// on first save; on rotation an empty BotToken keeps the existing encrypted
// value (so the dashboard can toggle Enabled without re-entering the token).
func (s *DiscordService) SaveCredentials(ctx context.Context, companyID string, in SaveCredentialsInput) (*domain.CompanyDiscordCredential, error) {
	in.ApplicationID = strings.TrimSpace(in.ApplicationID)
	in.PublicKey = strings.TrimSpace(in.PublicKey)
	in.BotToken = strings.TrimSpace(in.BotToken)
	if in.ApplicationID == "" {
		return nil, fmt.Errorf("%w: application_id required", domain.ErrInvalidInput)
	}
	if in.PublicKey == "" {
		return nil, fmt.Errorf("%w: public_key required", domain.ErrInvalidInput)
	}

	row := &domain.CompanyDiscordCredential{
		CompanyID:     companyID,
		ApplicationID: in.ApplicationID,
		PublicKey:     in.PublicKey,
		GuildID:       in.GuildID,
		Enabled:       in.Enabled,
	}

	if in.BotToken != "" {
		enc, err := s.cipher.Encrypt(in.BotToken)
		if err != nil {
			return nil, fmt.Errorf("encrypt bot token: %w", err)
		}
		row.BotTokenEncrypted = enc
	} else {
		existing, err := s.creds.Get(ctx, companyID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return nil, fmt.Errorf("%w: bot_token required on first save", domain.ErrInvalidInput)
			}
			return nil, err
		}
		row.BotTokenEncrypted = existing.BotTokenEncrypted
	}

	if err := s.creds.Upsert(ctx, row); err != nil {
		return nil, err
	}
	row.BotTokenEncrypted = nil
	if s.notifier != nil {
		if err := s.notifier.NotifyDiscordReload(ctx, companyID); err != nil {
			logrus.WithError(err).WithField("company_id", companyID).
				Warn("discord reload notify failed; cmd/discord will pick up the new creds on restart")
		}
	}
	return row, nil
}

// GetCredentials returns the row with the encrypted token stripped.
func (s *DiscordService) GetCredentials(ctx context.Context, companyID string) (*domain.CompanyDiscordCredential, error) {
	row, err := s.creds.Get(ctx, companyID)
	if err != nil {
		return nil, err
	}
	row.BotTokenEncrypted = nil
	return row, nil
}

// DeleteCredentials removes a tenant's Discord configuration and signals
// cmd/discord to close the session.
func (s *DiscordService) DeleteCredentials(ctx context.Context, companyID string) error {
	if err := s.creds.Delete(ctx, companyID); err != nil {
		return err
	}
	if s.notifier != nil {
		if err := s.notifier.NotifyDiscordReload(ctx, companyID); err != nil {
			logrus.WithError(err).WithField("company_id", companyID).
				Warn("discord reload notify failed after delete")
		}
	}
	return nil
}

// AddUser adds a Discord user to the company's allowlist.
func (s *DiscordService) AddUser(ctx context.Context, companyID, discordUserID, label string) error {
	discordUserID = strings.TrimSpace(discordUserID)
	if discordUserID == "" {
		return fmt.Errorf("%w: discord_user_id required", domain.ErrInvalidInput)
	}
	return s.users.Add(ctx, &domain.AllowedDiscordUser{
		CompanyID:     companyID,
		DiscordUserID: discordUserID,
		Label:         label,
	})
}

// RemoveUser drops a Discord user from the allowlist.
func (s *DiscordService) RemoveUser(ctx context.Context, companyID, discordUserID string) error {
	return s.users.Remove(ctx, companyID, discordUserID)
}

// ListUsers returns the company's Discord allowlist.
func (s *DiscordService) ListUsers(ctx context.Context, companyID string) ([]*domain.AllowedDiscordUser, error) {
	return s.users.ListByCompany(ctx, companyID)
}

// IsUserAllowed reports whether a Discord user is permitted to chat with the
// agent on behalf of the company.
func (s *DiscordService) IsUserAllowed(ctx context.Context, companyID, discordUserID string) (bool, error) {
	return s.users.IsAllowed(ctx, companyID, discordUserID)
}

// ResolveCompanyByApplication looks up the company that owns a Discord
// application id. Used by the interactions webhook to find the right public
// key without trusting headers.
func (s *DiscordService) ResolveCompanyByApplication(ctx context.Context, applicationID string) (*domain.CompanyDiscordCredential, error) {
	return s.creds.GetByApplicationID(ctx, applicationID)
}
