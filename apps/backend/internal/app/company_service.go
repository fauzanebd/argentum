package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/crypto"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tools"
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
	schemaTool  *tools.GetSchemaTool // optional; nil in tests
	describer   *ConnectionDescriber // optional; nil disables LLM autogen
	inference   InferenceEnqueuer    // optional; nil disables business inference
}

// InferenceEnqueuer queues a business-inference pass for one source (T-B2).
//
// A queue rather than a goroutine, unlike the connection describer beside it:
// this pass reads a whole schema and spends an LLM call, and doing that inside
// the API process would tie a tenant's onboarding request to a warehouse's
// introspection time. The API only ever asks; the worker decides whether there
// is anything to do.
type InferenceEnqueuer interface {
	EnqueueBusinessInference(ctx context.Context, companyID, connectionID string, force bool) error
}

// WithInference turns on business inference. Optional wiring: a deployment
// without it keeps every connection working and leaves the tenant typing their
// own profile, which is what T-B1 alone gives them.
func (s *CompanyService) WithInference(enq InferenceEnqueuer) *CompanyService {
	s.inference = enq
	return s
}

// inferSource asks for a draft of what this source says the business is.
//
// Best-effort by construction: a queue that will not take the task must not
// fail the request that triggered it, because every caller here has just done
// something the tenant cares about more (added a source, rotated a DSN) and
// none of them is about the profile.
func (s *CompanyService) inferSource(ctx context.Context, companyID, connID string) {
	if s.inference == nil {
		return
	}
	// force=false: these are the automatic triggers, and the cached schema is
	// the right thing for them to read. The button is the exception.
	if err := s.inference.EnqueueBusinessInference(ctx, companyID, connID, false); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": companyID,
			"source_id":  connID,
		}).Warn("could not queue business inference; the source is connected and the profile stays as it is")
	}
}

// RescanSource re-runs inference for one source on demand — the Re-scan button
// in Settings → Connections (T-B2).
//
// It queues rather than runs, and returns as soon as the task is accepted. The
// task carries force=true, so the worker re-introspects rather than reading the
// hour-old schema cache: a tenant who has just added a table and pressed this
// is asking about their database, not about our copy of it. The fingerprint
// check still applies afterwards, so a schema that really has not moved spends
// no LLM call.
func (s *CompanyService) RescanSource(ctx context.Context, companyID, connID string) error {
	conn, err := s.connections.GetByID(ctx, connID)
	if err != nil {
		return err
	}
	if conn.CompanyID != companyID {
		return domain.ErrUnauthorized
	}
	if s.inference == nil {
		return fmt.Errorf("business inference is not configured")
	}
	return s.inference.EnqueueBusinessInference(ctx, companyID, connID, true)
}

func NewCompanyService(
	companies domain.CompanyRepository,
	conns domain.ConnectionRepository,
	phones domain.PhoneRepository,
	dsnCipher *crypto.DSNCipher,
	pool *db.TenantConnPool,
	mb *MetabaseWarehouseSync,
	schemaTool *tools.GetSchemaTool,
	describer *ConnectionDescriber,
) *CompanyService {
	return &CompanyService{
		companies:   companies,
		connections: conns,
		phones:      phones,
		dsnCipher:   dsnCipher,
		pool:        pool,
		mb:          mb,
		schemaTool:  schemaTool,
		describer:   describer,
	}
}

// AddConnection registers a tenant DB. If markDefault is true (or the company
// has no other connections), the new row becomes the default. The DSN is
// encrypted before persisting. If description is empty, the
// ConnectionDescriber is asked to autogen one in the background.
func (s *CompanyService) AddConnection(ctx context.Context, companyID, dbType, label, description, dsn string, markDefault bool) (*domain.DBConnection, error) {
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

	descSource := ""
	if description != "" {
		descSource = domain.DescriptionSourceManual
	}

	c := &domain.DBConnection{
		CompanyID:         companyID,
		DBType:            dbType,
		Label:             label,
		Description:       description,
		DescriptionSource: descSource,
		DSNEncrypted:      enc,
		IsDefault:         markDefault,
	}
	if err := s.connections.Create(ctx, c); err != nil {
		return nil, err
	}
	if markDefault {
		if err := s.connections.SetDefault(ctx, companyID, c.ID); err != nil {
			return nil, err
		}
		// Default may have changed; drop every cached entry for the company
		// so the next empty-source-id resolve picks the new default.
		s.pool.InvalidateAll(companyID)
	}
	if s.mb != nil {
		id, err := s.mb.SyncCompanyDatabase(ctx, c, dsn)
		if err != nil {
			logrus.WithError(err).Warn("metabase warehouse sync failed; dashboards stay disabled until connection can be synced")
		} else {
			c.MetabaseDatabaseID = &id
			if err := s.connections.Update(ctx, c); err != nil {
				return nil, err
			}
		}
	}
	if description == "" && s.describer != nil {
		s.describer.DescribeAsync(companyID, c.ID)
	}
	// What this source says the business is (T-B2). Queued on create rather
	// than waiting for a test to pass: the worker's own introspection is the
	// test, and a source that cannot be read yet fails there — where it retries
	// — instead of never being asked about.
	s.inferSource(ctx, companyID, c.ID)
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
	s.pool.Invalidate(companyID, conn.ID)
	if s.schemaTool != nil {
		s.schemaTool.Invalidate(companyID, conn.ID)
	}

	if s.mb != nil {
		id, err := s.mb.SyncCompanyDatabase(ctx, conn, dsn)
		if err != nil {
			return fmt.Errorf("connection saved but Metabase sync failed: %w", err)
		}
		conn.MetabaseDatabaseID = &id
		if err := s.connections.Update(ctx, conn); err != nil {
			return err
		}
	}
	// Schema may have changed; refresh the description unless the user has
	// explicitly written one.
	if s.describer != nil && conn.DescriptionSource != domain.DescriptionSourceManual {
		s.describer.DescribeAsync(companyID, conn.ID)
	}
	// A rotated DSN can point at a different database entirely. The fingerprint
	// check decides whether that is true; asking is free when it is not (T-B2).
	s.inferSource(ctx, companyID, conn.ID)
	return nil
}

// UpdateConnectionMeta sets user-editable metadata (label, description). An
// explicit description here is treated as manual, blocking future autogen
// from overwriting it. Passing an empty description clears the row and
// re-queues an autogen pass.
func (s *CompanyService) UpdateConnectionMeta(ctx context.Context, companyID, connID, label, description string) error {
	conn, err := s.connections.GetByID(ctx, connID)
	if err != nil {
		return err
	}
	if conn.CompanyID != companyID {
		return domain.ErrUnauthorized
	}
	conn.Label = label
	conn.Description = description
	if description == "" {
		conn.DescriptionSource = ""
	} else {
		conn.DescriptionSource = domain.DescriptionSourceManual
	}
	if err := s.connections.Update(ctx, conn); err != nil {
		return err
	}
	if description == "" && s.describer != nil {
		s.describer.DescribeAsync(companyID, conn.ID)
	}
	return nil
}

// connectionEmbeddingToggler is the focused capability we need on the repo
// to flip the per-source flag without round-tripping the DSN.
type connectionEmbeddingToggler interface {
	SetEmbeddingToggle(ctx context.Context, id string, on bool) error
}

// SetConnectionEmbeddingToggle flips the embedding-based table picker on or
// off for a source. Validates ownership so a tenant can't toggle another
// tenant's connection.
func (s *CompanyService) SetConnectionEmbeddingToggle(ctx context.Context, companyID, connID string, on bool) error {
	conn, err := s.connections.GetByID(ctx, connID)
	if err != nil {
		return err
	}
	if conn.CompanyID != companyID {
		return domain.ErrUnauthorized
	}
	toggler, ok := s.connections.(connectionEmbeddingToggler)
	if !ok {
		return fmt.Errorf("connection repository does not support embedding toggle")
	}
	return toggler.SetEmbeddingToggle(ctx, connID, on)
}

// RegenerateDescription forces a fresh LLM-generated description on the
// connection, overwriting any existing manual or auto text. Returns the
// updated row (DSN scrubbed). Synchronous — blocks until the LLM call is
// done; the caller is expected to set a request-level timeout.
func (s *CompanyService) RegenerateDescription(ctx context.Context, companyID, connID string) (*domain.DBConnection, error) {
	conn, err := s.connections.GetByID(ctx, connID)
	if err != nil {
		return nil, err
	}
	if conn.CompanyID != companyID {
		return nil, domain.ErrUnauthorized
	}
	if s.describer == nil {
		return nil, fmt.Errorf("description regeneration is unavailable: LLM not configured")
	}
	if _, err := s.describer.Regenerate(ctx, companyID, connID); err != nil {
		return nil, err
	}
	updated, err := s.connections.GetByID(ctx, connID)
	if err != nil {
		return nil, fmt.Errorf("read back connection: %w", err)
	}
	updated.DSNEncrypted = nil
	return updated, nil
}

// SetDefaultConnection switches the active connection.
func (s *CompanyService) SetDefaultConnection(ctx context.Context, companyID, connID string) error {
	if err := s.connections.SetDefault(ctx, companyID, connID); err != nil {
		return err
	}
	// Empty-source-id resolves to the new default; drop everything so the
	// pool's empty-key path can repopulate against the new winner.
	s.pool.InvalidateAll(companyID)
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
	s.pool.Invalidate(companyID, conn.ID)
	if s.schemaTool != nil {
		s.schemaTool.Invalidate(companyID, conn.ID)
	}
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

// TestConnectionByID pings a saved connection by decrypting its stored DSN.
// Used by the dashboard's row-level "Test" button — clients can't resubmit
// credentials they never received.
func (s *CompanyService) TestConnectionByID(ctx context.Context, companyID, connID string) error {
	conn, err := s.connections.GetByID(ctx, connID)
	if err != nil {
		return err
	}
	if conn.CompanyID != companyID {
		return domain.ErrUnauthorized
	}
	dsn, err := s.dsnCipher.Decrypt(conn.DSNEncrypted)
	if err != nil {
		return fmt.Errorf("decrypt dsn: %w", err)
	}
	if err := s.TestConnection(ctx, conn.DBType, dsn); err != nil {
		return err
	}
	// A successful test is the other trigger for business inference (T-B2), and
	// the one that catches the source added while its database was unreachable:
	// the create-time pass failed and retired, and nothing else would ask again.
	// Repeats are free — the enqueuer drops duplicates inside a two-minute
	// window and an unchanged schema spends no LLM call.
	s.inferSource(ctx, companyID, conn.ID)
	return nil
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

// UpdatePIIRedactionMode validates and persists the tenant's redaction policy
// (T-07b). Admin-gated at the router like every other company setting: this
// widens what the agent may print, and it is not a per-user preference.
func (s *CompanyService) UpdatePIIRedactionMode(ctx context.Context, companyID string, mode domain.PIIRedactionMode) error {
	if !mode.Valid() {
		return fmt.Errorf("%w: unsupported pii_redaction_mode %q", domain.ErrInvalidInput, mode)
	}
	c, err := s.companies.GetByID(ctx, companyID)
	if err != nil {
		return err
	}
	c.PIIRedactionMode = mode
	return s.companies.Update(ctx, c)
}
