package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/crypto"
	"github.com/fauzanebd/argentum/internal/domain"
)

// validCurrencies is a lookup of common ISO 4217 codes. We keep it small
// and practical — users pick from a dropdown, not free-text.
var validCurrencies = map[string]bool{
	"USD": true, "EUR": true, "GBP": true, "JPY": true, "CNY": true,
	"IDR": true, "SGD": true, "MYR": true, "THB": true, "PHP": true,
	"VND": true, "INR": true, "AUD": true, "NZD": true, "CAD": true,
	"CHF": true, "HKD": true, "KRW": true, "TWD": true, "BRL": true,
	"MXN": true, "ZAR": true, "AED": true, "SAR": true, "SEK": true,
	"NOK": true, "DKK": true, "PLN": true, "TRY": true, "RUB": true,
	"COP": true, "ARS": true, "CLP": true, "PEN": true, "EGP": true,
	"NGN": true, "KES": true, "GHS": true, "BDT": true, "PKR": true,
	"LKR": true, "MMK": true, "KHR": true, "LAK": true,
}

// IsValidCurrency reports whether code is a recognised ISO 4217 currency.
func IsValidCurrency(code string) bool { return validCurrencies[code] }

// SupportedCurrencies returns all recognised currency codes (unsorted).
func SupportedCurrencies() []string {
	out := make([]string, 0, len(validCurrencies))
	for c := range validCurrencies {
		out = append(out, c)
	}
	return out
}

// CompanyService is the use case layer for managing a company's
// configuration: DB connections, phone allowlist, and company settings.
type CompanyService struct {
	companies   domain.CompanyRepository
	connections domain.ConnectionRepository
	phones      domain.PhoneRepository
	dsnCipher   *crypto.DSNCipher
	pool        *db.TenantConnPool
	mb          *MetabaseWarehouseSync
}

func NewCompanyService(
	companies domain.CompanyRepository,
	conns domain.ConnectionRepository,
	phones domain.PhoneRepository,
	dsnCipher *crypto.DSNCipher,
	pool *db.TenantConnPool,
	mb *MetabaseWarehouseSync,
) *CompanyService {
	return &CompanyService{companies: companies, connections: conns, phones: phones, dsnCipher: dsnCipher, pool: pool, mb: mb}
}

// AddConnection registers a tenant DB. If markDefault is true (or the company
// has no other connections), the new row becomes the default. The DSN is
// encrypted before persisting.
func (s *CompanyService) AddConnection(ctx context.Context, companyID, dbType, label, dsn string, markDefault bool) (*domain.DBConnection, error) {
	if !db.IsSupported(dbType) {
		return nil, fmt.Errorf("%w: %s", domain.ErrUnsupportedDB, dbType)
	}
	enc, err := s.dsnCipher.Encrypt(dsn)
	if err != nil {
		return nil, fmt.Errorf("encrypt dsn: %w", err)
	}

	existing, _ := s.connections.ListByCompany(ctx, companyID)
	if len(existing) == 0 {
		markDefault = true
	}

	c := &domain.DBConnection{
		CompanyID:    companyID,
		DBType:       dbType,
		Label:        label,
		DSNEncrypted: enc,
		IsDefault:    markDefault,
	}
	if err := s.connections.Create(ctx, c); err != nil {
		return nil, err
	}
	if markDefault {
		if err := s.connections.SetDefault(ctx, companyID, c.ID); err != nil {
			return nil, err
		}
		s.pool.Invalidate(companyID)
	}
	if s.mb != nil && dbType == db.Postgres {
		id, err := s.mb.SyncCompanyPostgres(ctx, c, dsn)
		if err != nil {
			logrus.WithError(err).Warn("metabase warehouse sync failed; dashboards stay disabled until connection can be synced")
		} else {
			c.MetabaseDatabaseID = &id
			if err := s.connections.Update(ctx, c); err != nil {
				return nil, err
			}
		}
	}
	return c, nil
}

// UpdateConnectionDSN rotates the encrypted DSN on an existing row.
func (s *CompanyService) UpdateConnectionDSN(ctx context.Context, companyID, connID, dsn string) error {
	conn, err := s.connections.GetByID(ctx, connID)
	if err != nil {
		return err
	}
	if conn.CompanyID != companyID {
		return domain.ErrUnauthorized
	}
	enc, err := s.dsnCipher.Encrypt(dsn)
	if err != nil {
		return err
	}
	conn.DSNEncrypted = enc
	if err := s.connections.Update(ctx, conn); err != nil {
		return err
	}
	s.pool.Invalidate(companyID)

	if s.mb != nil && conn.DBType == db.Postgres {
		id, err := s.mb.SyncCompanyPostgres(ctx, conn, dsn)
		if err != nil {
			return fmt.Errorf("connection saved but Metabase sync failed: %w", err)
		}
		conn.MetabaseDatabaseID = &id
		if err := s.connections.Update(ctx, conn); err != nil {
			return err
		}
	}
	return nil
}

// SetDefaultConnection switches the active connection.
func (s *CompanyService) SetDefaultConnection(ctx context.Context, companyID, connID string) error {
	if err := s.connections.SetDefault(ctx, companyID, connID); err != nil {
		return err
	}
	s.pool.Invalidate(companyID)
	return nil
}

// DeleteConnection removes a connection. If it was default, the company has
// no default afterwards (caller should set a new one).
func (s *CompanyService) DeleteConnection(ctx context.Context, companyID, connID string) error {
	conn, err := s.connections.GetByID(ctx, connID)
	if err != nil {
		return err
	}
	if conn.CompanyID != companyID {
		return domain.ErrUnauthorized
	}
	if s.mb != nil && conn.MetabaseDatabaseID != nil && *conn.MetabaseDatabaseID > 0 {
		s.mb.DeleteWarehouse(ctx, *conn.MetabaseDatabaseID)
	}
	if err := s.connections.Delete(ctx, connID); err != nil {
		return err
	}
	s.pool.Invalidate(companyID)
	return nil
}

// ListConnections returns the company's connections (DSN field is empty
// because we never expose plaintext DSNs in the API).
func (s *CompanyService) ListConnections(ctx context.Context, companyID string) ([]*domain.DBConnection, error) {
	out, err := s.connections.ListByCompany(ctx, companyID)
	if err != nil {
		return nil, err
	}
	for _, c := range out {
		c.DSNEncrypted = nil
	}
	return out, nil
}

// TestConnection opens a one-shot connection through the supplied driver and
// pings it. Used by the dashboard's "Test connection" button.
func (s *CompanyService) TestConnection(ctx context.Context, dbType, dsn string) error {
	if !db.IsSupported(dbType) {
		return fmt.Errorf("%w: %s", domain.ErrUnsupportedDB, dbType)
	}
	return db.PingDSN(ctx, dbType, dsn)
}

// AddPhoneNumber adds a number to the company's allowlist.
func (s *CompanyService) AddPhoneNumber(ctx context.Context, companyID, phone, label string) error {
	if phone == "" {
		return domain.ErrInvalidInput
	}
	return s.phones.Add(ctx, &domain.AllowedPhoneNumber{
		CompanyID:   companyID,
		PhoneNumber: phone,
		Label:       label,
	})
}

// RemovePhoneNumber drops a number from the allowlist.
func (s *CompanyService) RemovePhoneNumber(ctx context.Context, companyID, phone string) error {
	return s.phones.Remove(ctx, companyID, phone)
}

// ListPhoneNumbers returns all numbers on the company's allowlist.
func (s *CompanyService) ListPhoneNumbers(ctx context.Context, companyID string) ([]*domain.AllowedPhoneNumber, error) {
	return s.phones.ListByCompany(ctx, companyID)
}

// ResolveCompanyByPhone looks up which company owns an inbound phone number.
// Used by the WhatsApp webhook handler before queueing the message.
func (s *CompanyService) ResolveCompanyByPhone(ctx context.Context, phone string) (string, error) {
	rec, err := s.phones.FindCompanyByPhone(ctx, phone)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return "", domain.ErrNotFound
		}
		return "", err
	}
	return rec.CompanyID, nil
}

// GetCompany returns the company for the given ID.
func (s *CompanyService) GetCompany(ctx context.Context, companyID string) (*domain.Company, error) {
	return s.companies.GetByID(ctx, companyID)
}

// UpdateCurrency validates and persists a new default currency for a company.
func (s *CompanyService) UpdateCurrency(ctx context.Context, companyID, currencyCode string) error {
	if !IsValidCurrency(currencyCode) {
		return fmt.Errorf("%w: unsupported currency %q", domain.ErrInvalidInput, currencyCode)
	}
	c, err := s.companies.GetByID(ctx, companyID)
	if err != nil {
		return err
	}
	c.DefaultCurrency = currencyCode
	return s.companies.Update(ctx, c)
}
