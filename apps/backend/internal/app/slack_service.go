package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fauzanebd/argentum/internal/crypto"
	"github.com/fauzanebd/argentum/internal/domain"
)

// SlackService is the use-case layer for per-tenant Slack configuration and
// user allowlist management. No reload signaling: outbound delivery happens
// inside the worker (slack.Client) which re-reads credentials lazily once
// the cached token expires (or immediately after an auth failure).
type SlackService struct {
	creds  domain.CompanySlackCredentialRepository
	users  domain.AllowedSlackUserRepository
	cipher *crypto.DSNCipher
}

func NewSlackService(
	creds domain.CompanySlackCredentialRepository,
	users domain.AllowedSlackUserRepository,
	cipher *crypto.DSNCipher,
) *SlackService {
	return &SlackService{creds: creds, users: users, cipher: cipher}
}

// SaveSlackCredentialsInput is the upsert payload from the dashboard.
type SaveSlackCredentialsInput struct {
	AppID         string
	TeamID        string
	BotToken      string // plaintext; encrypted before persist. Empty on rotation keeps existing.
	SigningSecret string
	BotUserID     string
	Enabled       bool
}

// SaveCredentials upserts a tenant's Slack app config. BotToken is required
// on first save; an empty BotToken on subsequent calls keeps the existing
// encrypted value (so the dashboard can flip Enabled or fill in the bot's
// user id without re-entering the token).
func (s *SlackService) SaveCredentials(ctx context.Context, companyID string, in SaveSlackCredentialsInput) (*domain.CompanySlackCredential, error) {
	in.AppID = strings.TrimSpace(in.AppID)
	in.TeamID = strings.TrimSpace(in.TeamID)
	in.BotToken = strings.TrimSpace(in.BotToken)
	in.SigningSecret = strings.TrimSpace(in.SigningSecret)
	in.BotUserID = strings.TrimSpace(in.BotUserID)
	if in.AppID == "" {
		return nil, fmt.Errorf("%w: app_id required", domain.ErrInvalidInput)
	}
	if in.SigningSecret == "" {
		return nil, fmt.Errorf("%w: signing_secret required", domain.ErrInvalidInput)
	}

	row := &domain.CompanySlackCredential{
		CompanyID:     companyID,
		AppID:         in.AppID,
		TeamID:        in.TeamID,
		SigningSecret: in.SigningSecret,
		BotUserID:     in.BotUserID,
		Enabled:       in.Enabled,
	}

	if in.BotToken != "" {
		enc, err := s.cipher.Encrypt(in.BotToken)
		if err != nil {
			return nil, fmt.Errorf("encrypt bot_token: %w", err)
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
	return row, nil
}

// GetCredentials returns the row with the encrypted token stripped.
func (s *SlackService) GetCredentials(ctx context.Context, companyID string) (*domain.CompanySlackCredential, error) {
	row, err := s.creds.Get(ctx, companyID)
	if err != nil {
		return nil, err
	}
	row.BotTokenEncrypted = nil
	return row, nil
}

// DeleteCredentials drops a tenant's Slack configuration.
func (s *SlackService) DeleteCredentials(ctx context.Context, companyID string) error {
	return s.creds.Delete(ctx, companyID)
}

// LearnBotUserID persists the bot's own user id when it was not configured
// by hand. The webhook calls this the first time Slack tells us who the bot
// is (via the event `authorizations` array), so admins never have to hunt
// for the id — the Lark integration's most common setup snag.
func (s *SlackService) LearnBotUserID(ctx context.Context, companyID, botUserID string) error {
	botUserID = strings.TrimSpace(botUserID)
	if botUserID == "" {
		return nil
	}
	existing, err := s.creds.Get(ctx, companyID)
	if err != nil {
		return err
	}
	if existing.BotUserID == botUserID {
		return nil
	}
	existing.BotUserID = botUserID
	return s.creds.Upsert(ctx, existing)
}

// AddUser adds a Slack user id to the company's allowlist.
func (s *SlackService) AddUser(ctx context.Context, companyID, slackUserID, label string) error {
	slackUserID = strings.TrimSpace(slackUserID)
	if slackUserID == "" {
		return fmt.Errorf("%w: slack_user_id required", domain.ErrInvalidInput)
	}
	return s.users.Add(ctx, &domain.AllowedSlackUser{
		CompanyID:   companyID,
		SlackUserID: slackUserID,
		Label:       label,
	})
}

// RemoveUser drops a Slack user id from the allowlist.
func (s *SlackService) RemoveUser(ctx context.Context, companyID, slackUserID string) error {
	return s.users.Remove(ctx, companyID, slackUserID)
}

// ListUsers returns the company's Slack allowlist.
func (s *SlackService) ListUsers(ctx context.Context, companyID string) ([]*domain.AllowedSlackUser, error) {
	return s.users.ListByCompany(ctx, companyID)
}

// IsUserAllowed reports whether a Slack user id is permitted to chat with
// the agent on behalf of the company.
func (s *SlackService) IsUserAllowed(ctx context.Context, companyID, slackUserID string) (bool, error) {
	return s.users.IsAllowed(ctx, companyID, slackUserID)
}

// ResolveCompanyByAppID looks up the company that owns a Slack app id.
// Used by the events webhook to find the right signing secret without
// trusting any field in the payload.
func (s *SlackService) ResolveCompanyByAppID(ctx context.Context, appID string) (*domain.CompanySlackCredential, error) {
	return s.creds.GetByAppID(ctx, appID)
}
