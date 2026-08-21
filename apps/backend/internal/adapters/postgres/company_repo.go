package postgres

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fauzanebd/argentum/internal/domain"
)

type CompanyRepo struct{ db *sql.DB }

func NewCompanyRepo(db *sql.DB) *CompanyRepo { return &CompanyRepo{db: db} }

func (r *CompanyRepo) Create(ctx context.Context, c *domain.Company) error {
	const q = `
		INSERT INTO companies (name, slug, default_currency)
		VALUES ($1, $2, $3)
		RETURNING id, created_at
	`
	currency := c.DefaultCurrency
	if currency == "" {
		currency = "USD"
	}
	if err := r.db.QueryRowContext(ctx, q, c.Name, c.Slug, currency).Scan(&c.ID, &c.CreatedAt); err != nil {
		return fmt.Errorf("insert company: %w", err)
	}
	c.DefaultCurrency = currency
	return nil
}

func (r *CompanyRepo) GetByID(ctx context.Context, id string) (*domain.Company, error) {
	const q = `SELECT id, name, slug, default_currency, pii_redaction_mode, message_retention_days, created_at FROM companies WHERE id = $1`
	c := &domain.Company{}
	if err := r.db.QueryRowContext(ctx, q, id).Scan(
		&c.ID, &c.Name, &c.Slug, &c.DefaultCurrency, &c.PIIRedactionMode, &c.MessageRetentionDays, &c.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return c, nil
}

func (r *CompanyRepo) GetBySlug(ctx context.Context, slug string) (*domain.Company, error) {
	const q = `SELECT id, name, slug, default_currency, pii_redaction_mode, message_retention_days, created_at FROM companies WHERE slug = $1`
	c := &domain.Company{}
	if err := r.db.QueryRowContext(ctx, q, slug).Scan(
		&c.ID, &c.Name, &c.Slug, &c.DefaultCurrency, &c.PIIRedactionMode, &c.MessageRetentionDays, &c.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return c, nil
}

// Update writes the whole editable record. An empty PIIRedactionMode is written
// as `strict` rather than as the empty string — the column has a CHECK
// constraint, and a caller that loaded a row, changed the name and wrote it back
// must not have to know about a policy field to avoid failing the write.
//
// A retention out of range is clamped to forever for the same reason and a
// sharper one: this method writes every column, so a caller that loaded a row
// and changed the name must not be able to hand a negative day count to the
// purge. The settable path validates and rejects (app.CompanyService); this is
// the floor under it.
func (r *CompanyRepo) Update(ctx context.Context, c *domain.Company) error {
	const q = `UPDATE companies SET name = $1, slug = $2, default_currency = $3, pii_redaction_mode = $4, message_retention_days = $5 WHERE id = $6`
	mode := c.PIIRedactionMode
	if !mode.Valid() {
		mode = domain.PIIRedactionStrict
	}
	retention := c.MessageRetentionDays
	if !domain.ValidRetentionDays(retention) {
		retention = domain.RetentionForever
	}
	_, err := r.db.ExecContext(ctx, q, c.Name, c.Slug, c.DefaultCurrency, string(mode), retention, c.ID)
	return err
}

// EnsureWebhookSecret returns the tenant's callback signing secret, minting
// one on first use (T-A2).
//
// Lazy rather than at signup because a column of secrets for companies that
// will never receive a callback is a liability with no user. The UPDATE is
// conditional on the column still being empty and returns whatever the row
// holds afterwards, so two concurrent first callbacks converge on one secret
// instead of the second overwriting a value the first already signed with.
//
// The secret is never put on domain.Company: that struct is serialised
// straight to the dashboard, and a field that exists is a field that leaks the
// first time somebody returns the record from a new handler.
func (r *CompanyRepo) EnsureWebhookSecret(ctx context.Context, companyID string) (string, error) {
	const read = `SELECT webhook_secret FROM companies WHERE id = $1`
	var secret string
	err := r.db.QueryRowContext(ctx, read, companyID).Scan(&secret)
	if errors.Is(err, sql.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if secret != "" {
		return secret, nil
	}

	minted, err := newWebhookSecret()
	if err != nil {
		return "", err
	}
	const claim = `
		UPDATE companies SET webhook_secret = $2
		WHERE id = $1 AND webhook_secret = ''
		RETURNING webhook_secret
	`
	err = r.db.QueryRowContext(ctx, claim, companyID, minted).Scan(&secret)
	if errors.Is(err, sql.ErrNoRows) {
		// Another request minted one between the read and the claim. Re-read
		// rather than returning ours: theirs is the one already in use.
		if err := r.db.QueryRowContext(ctx, read, companyID).Scan(&secret); err != nil {
			return "", err
		}
		return secret, nil
	}
	if err != nil {
		return "", err
	}
	return secret, nil
}

// newWebhookSecret mints `whsec_` + 32 random bytes, base64url.
//
// The prefix is not decoration: a secret that leaks into a paste, a log or a
// commit is recognisable as one at a glance, which is what makes a secret
// scanner — ours or GitHub's — able to find it.
func newWebhookSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("mint webhook secret: %w", err)
	}
	return "whsec_" + base64.RawURLEncoding.EncodeToString(buf), nil
}

// GetBranding reads the report branding record. A company that has never
// configured one has `{}` in the column (migration 022's default), which
// unmarshals into the zero value — so this returns a usable branding and never
// a nil pointer for a company that exists.
func (r *CompanyRepo) GetBranding(ctx context.Context, companyID string) (*domain.ReportBranding, error) {
	const q = `SELECT report_branding FROM companies WHERE id = $1`
	var raw []byte
	if err := r.db.QueryRowContext(ctx, q, companyID).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	b := &domain.ReportBranding{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, b); err != nil {
			// A branding row that will not parse must not take the document
			// down with it: the renderer's whole contract is that branding is
			// optional. Report it so it can be fixed, and render Argentum's
			// defaults meanwhile.
			return nil, fmt.Errorf("decode report_branding for company %s: %w", companyID, err)
		}
	}
	return b, nil
}

// SaveBranding writes the whole record. It touches one column, so it does not
// race the name/slug/currency writes going through Update.
func (r *CompanyRepo) SaveBranding(ctx context.Context, companyID string, b *domain.ReportBranding) error {
	if b == nil {
		b = &domain.ReportBranding{}
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("encode report_branding: %w", err)
	}
	const q = `UPDATE companies SET report_branding = $1 WHERE id = $2`
	res, err := r.db.ExecContext(ctx, q, raw, companyID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// GetWidgetConfig reads the tenant's widget appearance and content (T-23).
// Same shape as GetBranding above, one column over: a settings blob one tenant
// reads for their own rendering.
func (r *CompanyRepo) GetWidgetConfig(ctx context.Context, companyID string) (*domain.WidgetConfig, error) {
	const q = `SELECT widget_config FROM companies WHERE id = $1`
	var raw []byte
	if err := r.db.QueryRowContext(ctx, q, companyID).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	c := &domain.WidgetConfig{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, c); err != nil {
			// A config that will not parse must not take the widget down with
			// it — the whole contract is that every field is optional. Report
			// it so it can be fixed; the caller renders defaults meanwhile.
			return nil, fmt.Errorf("decode widget_config for company %s: %w", companyID, err)
		}
	}
	return c, nil
}

// SaveWidgetConfig writes the whole record, one column, so it does not race the
// name/slug/currency writes going through Update.
func (r *CompanyRepo) SaveWidgetConfig(ctx context.Context, companyID string, c *domain.WidgetConfig) error {
	if c == nil {
		c = &domain.WidgetConfig{}
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode widget_config: %w", err)
	}
	const q = `UPDATE companies SET widget_config = $1 WHERE id = $2`
	res, err := r.db.ExecContext(ctx, q, raw, companyID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
