package discord

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/crypto"
	"github.com/fauzanebd/argentum/internal/domain"
)

// SessionManager owns the live discordgo.Session for each enabled tenant.
// Reload(companyID) is the rotation hook: cmd/api publishes a "discord
// credential changed" event after a PUT and cmd/discord calls Reload to swap
// the session without restarting the process.
type SessionManager struct {
	repo       domain.CompanyDiscordCredentialRepository
	cipher     *crypto.DSNCipher
	dispatcher Dispatcher

	mu       sync.RWMutex
	sessions map[string]*Session // keyed by company_id
}

func NewSessionManager(repo domain.CompanyDiscordCredentialRepository, cipher *crypto.DSNCipher, dispatcher Dispatcher) *SessionManager {
	return &SessionManager{
		repo:       repo,
		cipher:     cipher,
		dispatcher: dispatcher,
		sessions:   make(map[string]*Session),
	}
}

// Start opens a session for every enabled credential row. Failures are logged
// per-tenant so one bad token doesn't kill the whole process.
func (m *SessionManager) Start(ctx context.Context) error {
	rows, err := m.repo.ListEnabled(ctx)
	if err != nil {
		return fmt.Errorf("list discord credentials: %w", err)
	}
	for _, c := range rows {
		if err := m.openLocked(c); err != nil {
			logrus.WithError(err).WithField("company_id", c.CompanyID).
				Error("discord session open failed")
		}
	}
	logrus.WithField("count", len(m.sessions)).Info("discord sessions started")
	return nil
}

// Reload re-opens the session for one tenant. If the tenant row no longer
// exists or is disabled, the existing session is closed and dropped.
func (m *SessionManager) Reload(ctx context.Context, companyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if old, ok := m.sessions[companyID]; ok {
		_ = old.Close()
		delete(m.sessions, companyID)
	}
	c, err := m.repo.Get(ctx, companyID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			logrus.WithField("company_id", companyID).Info("discord reload: row absent; session dropped")
			return nil
		}
		return err
	}
	if !c.Enabled {
		logrus.WithField("company_id", companyID).Info("discord reload: row disabled; session dropped")
		return nil
	}
	return m.openLocked(c)
}

// openLocked must be called with m.mu held OR before m has been published to
// any other goroutine (i.e. from Start).
func (m *SessionManager) openLocked(c *domain.CompanyDiscordCredential) error {
	token, err := m.cipher.Decrypt(c.BotTokenEncrypted)
	if err != nil {
		return fmt.Errorf("decrypt bot token: %w", err)
	}
	s, err := openSession(c.CompanyID, c.ApplicationID, token, m.dispatcher)
	if err != nil {
		return err
	}
	m.sessions[c.CompanyID] = s
	return nil
}

// Send writes a message through the tenant's session. Implements Provider.
func (m *SessionManager) Send(companyID, channelID, content string) error {
	m.mu.RLock()
	s := m.sessions[companyID]
	m.mu.RUnlock()
	if s == nil {
		return fmt.Errorf("no discord session for company %s", companyID)
	}
	return s.Send(context.Background(), channelID, content)
}

// Close shuts every session down. Safe to call multiple times.
func (m *SessionManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, s := range m.sessions {
		if err := s.Close(); err != nil {
			logrus.WithError(err).WithField("company_id", id).Warn("discord session close failed")
		}
		delete(m.sessions, id)
	}
}

var _ Provider = (*SessionManager)(nil)
