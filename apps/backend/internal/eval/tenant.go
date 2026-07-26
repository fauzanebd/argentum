package eval

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"

	pgctl "github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/bootstrap"
	"github.com/fauzanebd/argentum/internal/domain"
)

// Identifiers for the tenant the harness runs as. Fixed rather than
// generated: an eval run should reuse one company across runs so its usage
// history is comparable, and so a developer can open the threads in the
// dashboard afterwards and read what the agent actually said.
const (
	tenantSlug     = "argentum-eval"
	tenantName     = "Argentum Eval"
	tenantEmail    = "eval@argentum.local"
	tenantCurrency = "IDR"

	primarySourceLabel = "Demo Retail"
	primarySourceDesc  = "Retail sales star schema for the demo tenant: fact_sales (transactions with sales_amount, cost_amount, profit_amount, quantity, payment_method, sales_channel) joined to dim_date, dim_customers and dim_products. Covers 1 July 2024 to 31 December 2024."

	secondSourceLabel = "Demo People"
	secondSourceDesc  = "HR records for the demo tenant: employees (department, role, join date, monthly salary) and attendance (daily presence per employee). Contains no sales or revenue data."
	secondSourceDB    = "demo_people"
)

// EnsureTenant makes the eval tenant exist and returns its identifiers. It
// is idempotent: run it a hundred times and you still have one company, one
// user and two sources.
//
// The second source is not decoration. Three of the golden categories test
// what the agent does when a question could plausibly hit either database —
// whether it asks instead of guessing — and that behaviour cannot be
// measured against a tenant with one source, because the system prompt
// explicitly says not to ask when only one exists.
func EnsureTenant(ctx context.Context, stack *bootstrap.Stack, demoDSN string) (Tenant, error) {
	users := pgctl.NewUserRepo(stack.ControlDB)

	company, err := stack.Companies.GetBySlug(ctx, tenantSlug)
	if errors.Is(err, domain.ErrNotFound) {
		company = &domain.Company{
			Name:            tenantName,
			Slug:            tenantSlug,
			DefaultCurrency: tenantCurrency,
		}
		if err := stack.Companies.Create(ctx, company); err != nil {
			return Tenant{}, fmt.Errorf("create company: %w", err)
		}
		logrus.WithField("company_id", company.ID).Info("eval: created tenant company")
	} else if err != nil {
		return Tenant{}, fmt.Errorf("lookup company: %w", err)
	}

	user, err := users.GetByEmail(ctx, tenantEmail)
	if errors.Is(err, domain.ErrNotFound) {
		// The password is never used to sign in — threads need a user row
		// because conversation_threads.user_id is a foreign key — but it
		// still goes through the real hasher rather than landing a
		// plaintext or empty credential in the users table.
		hash, err := auth.HashPassword("eval-" + tenantSlug + "-not-for-login")
		if err != nil {
			return Tenant{}, fmt.Errorf("hash eval password: %w", err)
		}
		user = &domain.User{
			CompanyID:    company.ID,
			Email:        tenantEmail,
			PasswordHash: hash,
			Role:         domain.RoleAdmin,
		}
		if err := users.Create(ctx, user); err != nil {
			return Tenant{}, fmt.Errorf("create user: %w", err)
		}
		logrus.WithField("user_id", user.ID).Info("eval: created tenant user")
	} else if err != nil {
		return Tenant{}, fmt.Errorf("lookup user: %w", err)
	}

	if err := ensureSources(ctx, stack, company.ID, demoDSN); err != nil {
		return Tenant{}, err
	}

	return Tenant{
		CompanyID:   company.ID,
		CompanyName: company.Name,
		UserID:      user.ID,
		Currency:    company.DefaultCurrency,
	}, nil
}

func ensureSources(ctx context.Context, stack *bootstrap.Stack, companyID, demoDSN string) error {
	existing, err := stack.Connections.ListByCompany(ctx, companyID)
	if err != nil {
		return fmt.Errorf("list sources: %w", err)
	}
	have := make(map[string]bool, len(existing))
	for _, c := range existing {
		have[c.Label] = true
	}

	if !have[primarySourceLabel] {
		if err := createSource(ctx, stack, companyID, primarySourceLabel, primarySourceDesc, demoDSN, true); err != nil {
			return err
		}
	}

	if !have[secondSourceLabel] {
		peopleDSN, err := ensurePeopleDatabase(demoDSN)
		if err != nil {
			// A missing second source costs three cases, not the run.
			// Report it and carry on rather than blocking a scoring run
			// on a seeding convenience.
			logrus.WithError(err).Warn("eval: could not provision the second demo source; multi_source cases will fail")
			return nil
		}
		if err := createSource(ctx, stack, companyID, secondSourceLabel, secondSourceDesc, peopleDSN, false); err != nil {
			return err
		}
	}
	return nil
}

func createSource(ctx context.Context, stack *bootstrap.Stack, companyID, label, description, dsn string, isDefault bool) error {
	enc, err := stack.DSNCipher.Encrypt(dsn)
	if err != nil {
		return fmt.Errorf("encrypt DSN for %s: %w", label, err)
	}
	conn := &domain.DBConnection{
		CompanyID:         companyID,
		DBType:            "postgres",
		DSNEncrypted:      enc,
		IsDefault:         isDefault,
		Label:             label,
		Description:       description,
		DescriptionSource: "manual",
	}
	if err := stack.Connections.Create(ctx, conn); err != nil {
		return fmt.Errorf("create source %s: %w", label, err)
	}
	logrus.WithFields(logrus.Fields{"source_id": conn.ID, "label": label}).Info("eval: registered source")
	return nil
}

// ensurePeopleDatabase creates and seeds the small HR database the
// multi-source cases disambiguate against, and returns its DSN.
//
// Seeded here rather than in migrations/demo_tenant because those only run
// on a fresh Docker volume — a developer who already has the demo container
// would never get it. Everything is CREATE IF NOT EXISTS and fixed values,
// so a second run is a no-op.
func ensurePeopleDatabase(demoDSN string) (string, error) {
	adminDB, err := sql.Open("postgres", demoDSN)
	if err != nil {
		return "", fmt.Errorf("open demo DSN: %w", err)
	}
	defer adminDB.Close()

	var exists bool
	if err := adminDB.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, secondSourceDB,
	).Scan(&exists); err != nil {
		return "", fmt.Errorf("check for %s: %w", secondSourceDB, err)
	}
	if !exists {
		// CREATE DATABASE cannot run inside a transaction, hence Exec on
		// the plain handle.
		if _, err := adminDB.Exec(`CREATE DATABASE ` + secondSourceDB); err != nil {
			return "", fmt.Errorf("create %s: %w", secondSourceDB, err)
		}
	}

	peopleDSN := swapDatabase(demoDSN, secondSourceDB)
	peopleDB, err := sql.Open("postgres", peopleDSN)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", secondSourceDB, err)
	}
	defer peopleDB.Close()

	if _, err := peopleDB.Exec(peopleSchema); err != nil {
		return "", fmt.Errorf("seed %s: %w", secondSourceDB, err)
	}
	return peopleDSN, nil
}

// swapDatabase replaces the database name in a postgres URL.
func swapDatabase(dsn, dbName string) string {
	q := ""
	if i := strings.Index(dsn, "?"); i >= 0 {
		q = dsn[i:]
		dsn = dsn[:i]
	}
	if i := strings.LastIndex(dsn, "/"); i >= 0 {
		dsn = dsn[:i+1] + dbName
	}
	return dsn + q
}

// peopleSchema is deliberately tiny: it exists to be a second, clearly
// different source the agent must choose between, not to be analysed.
const peopleSchema = `
CREATE TABLE IF NOT EXISTS employees (
    employee_id   serial PRIMARY KEY,
    full_name     varchar(120) NOT NULL,
    department    varchar(60)  NOT NULL,
    role_title    varchar(80)  NOT NULL,
    join_date     date         NOT NULL,
    monthly_salary numeric(12,2) NOT NULL
);

CREATE TABLE IF NOT EXISTS attendance (
    attendance_id serial PRIMARY KEY,
    employee_id   integer NOT NULL REFERENCES employees(employee_id),
    work_date     date    NOT NULL,
    status        varchar(20) NOT NULL
);

INSERT INTO employees (employee_id, full_name, department, role_title, join_date, monthly_salary)
SELECT * FROM (VALUES
    (1, 'Dewi Lestari',    'Sales',       'Account Executive', DATE '2023-02-01', 12500000.00),
    (2, 'Budi Santoso',    'Sales',       'Sales Manager',     DATE '2022-08-15', 21000000.00),
    (3, 'Siti Rahmawati',  'Operations',  'Ops Analyst',       DATE '2023-11-06', 10500000.00),
    (4, 'Andi Prasetyo',   'Engineering', 'Backend Engineer',  DATE '2024-01-08', 18500000.00),
    (5, 'Maya Kusuma',     'Finance',     'Finance Officer',   DATE '2021-05-17', 14000000.00),
    (6, 'Rizky Hidayat',   'Engineering', 'Data Engineer',     DATE '2024-03-25', 17500000.00)
) AS seed(employee_id, full_name, department, role_title, join_date, monthly_salary)
WHERE NOT EXISTS (SELECT 1 FROM employees);

INSERT INTO attendance (employee_id, work_date, status)
SELECT e.employee_id, d::date, 'present'
FROM employees e
CROSS JOIN generate_series(DATE '2024-12-02', DATE '2024-12-06', INTERVAL '1 day') d
WHERE NOT EXISTS (SELECT 1 FROM attendance);

SELECT setval(pg_get_serial_sequence('employees', 'employee_id'), (SELECT COALESCE(MAX(employee_id), 1) FROM employees));
`
