package eval

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/sirupsen/logrus"

	pgctl "github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/app"
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

// SeedOpts is what the tenant should look like for this run. A struct rather
// than five positional parameters, two of which are bools: at the call site
// `EnsureTenant(ctx, stack, dsn, host, true, false)` says nothing about which
// `true` is the metric registry, and cmd/eval already learned this lesson once
// (see runOpts there).
type SeedOpts struct {
	// DemoDSN is the demo warehouse. The other seeded databases are derived
	// from it by swapping the database name, so it is also how they are found.
	DemoDSN string
	// MetabaseHostPort is the host:port Metabase should use to reach the demo
	// database, which is not the one this process uses. Empty registers the
	// DSN unchanged.
	MetabaseHostPort string
	// WithMetrics defines the tenant's three metrics; false removes them.
	// State on a reused tenant, so a run that wants them absent has to say so
	// — otherwise T-07's before/after is the same run twice.
	WithMetrics bool
	// WithAdversarial registers the support source the `security` cases read
	// (T-H11); false removes it. Same reasoning as WithMetrics and one more
	// besides: a third source changes `list_sources` for every case, so a run
	// that does not need it must not carry it. See adversarial.go.
	WithAdversarial bool
}

// EnsureTenant makes the eval tenant exist and returns its identifiers. It
// is idempotent: run it a hundred times and you still have one company, one
// user and two sources — three when opts.WithAdversarial asks for the support
// fixtures.
//
// The second source is not decoration. Three of the golden categories test
// what the agent does when a question could plausibly hit either database —
// whether it asks instead of guessing — and that behaviour cannot be
// measured against a tenant with one source, because the system prompt
// explicitly says not to ask when only one exists.
func EnsureTenant(ctx context.Context, stack *bootstrap.Stack, opts SeedOpts) (Tenant, error) {
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

	if err := ensureSources(ctx, stack, company.ID, opts.DemoDSN, opts.MetabaseHostPort); err != nil {
		return Tenant{}, err
	}
	ensureDefaultAgent(ctx, stack, company.ID)
	ensureMetrics(ctx, stack, company.ID, user.ID, opts.WithMetrics)
	ensureAdversarialSource(ctx, stack, company.ID, opts.DemoDSN, opts.WithAdversarial)

	return Tenant{
		CompanyID:   company.ID,
		CompanyName: company.Name,
		UserID:      user.ID,
		Currency:    company.DefaultCurrency,
	}, nil
}

// ensureDefaultAgent gives the eval tenant the same unrestricted default agent
// that 030's backfill gave every real company and that signup gives every new
// one (T-S2).
//
// Without it the harness would score a turn that resolves to *no* agent, and
// the regression this ticket has to prove — that an agent with empty
// allowlists behaves exactly as the agent did before the roster existed — is
// precisely the one the harness would then not be exercising. The tenant is
// created through the repositories rather than through signup, so nothing else
// seeds it.
//
// Idempotent, and non-fatal: a harness that refuses to run because it could
// not write a settings row is worse than one that runs unscoped and says so.
func ensureDefaultAgent(ctx context.Context, stack *bootstrap.Stack, companyID string) {
	if _, err := stack.Agents.GetDefault(ctx, companyID); err == nil {
		return
	} else if !errors.Is(err, domain.ErrNotFound) {
		logrus.WithError(err).Warn("eval: default agent lookup failed; the run will be unscoped")
		return
	}
	a := &domain.Agent{
		CompanyID:    companyID,
		Name:         "Analyst",
		Description:  "General analytics assistant",
		AllowedTools: []string{},
		SourceIDs:    []string{},
		IsDefault:    true,
		Enabled:      true,
	}
	if err := stack.Agents.Create(ctx, a); err != nil {
		logrus.WithError(err).Warn("eval: default agent seed failed; the run will be unscoped")
		return
	}
	logrus.WithField("agent_id", a.ID).Info("eval: seeded the tenant's default agent")
}

// evalMetrics are the three metrics the metric_registry cases are scored
// against (T-07). They are the ones a retailer would actually define, and their
// numbers are the ones the golden file asserts.
//
// **Every aggregate is wrapped in COALESCE, and it is not decoration.**
// Validate-on-save runs the template over a trailing-7-day window; the demo
// warehouse stops on 31 December 2024, so a bare `sum(...)` returns NULL there
// and the save is refused with "column value is not a number (value is null)".
// That is a real edge recorded in coverage/metric-registry.md §4 — a metric
// nobody could save against historical data — and until it is fixed the
// workaround belongs here, visibly, rather than as a mystery in the SQL.
var evalMetrics = []struct {
	key, label, description, template, unit, currency string
}{
	{
		key: "revenue", label: "Revenue",
		description: "Total sales amount over the window, from fact_sales joined to dim_date. The authoritative revenue figure.",
		template: "SELECT COALESCE(sum(fs.sales_amount),0) AS value FROM fact_sales fs " +
			"JOIN dim_date d ON d.date_id = fs.date_id WHERE d.full_date >= {{from}} AND d.full_date <= {{to}}",
		unit: "currency", currency: tenantCurrency,
	},
	{
		key: "order_count", label: "Order count",
		description: "Distinct transactions in the window — how many orders were placed, not how many line items.",
		template: "SELECT count(DISTINCT fs.transaction_id) AS value FROM fact_sales fs " +
			"JOIN dim_date d ON d.date_id = fs.date_id WHERE d.full_date >= {{from}} AND d.full_date <= {{to}}",
		unit: "count",
	},
	{
		key: "aov", label: "Average order value",
		description: "Revenue divided by distinct transactions in the window — the average value of one order.",
		template: "SELECT COALESCE(sum(fs.sales_amount)/NULLIF(count(DISTINCT fs.transaction_id),0),0) AS value " +
			"FROM fact_sales fs JOIN dim_date d ON d.date_id = fs.date_id " +
			"WHERE d.full_date >= {{from}} AND d.full_date <= {{to}}",
		unit: "currency", currency: tenantCurrency,
	},
}

// ensureMetrics brings the eval tenant's metric registry to the state this run
// wants: the three metrics above when want is true, none of them when it is
// false.
//
// It removes as well as creates because the registry is *state on the tenant*,
// and the tenant is reused across runs by design. Without the removing half,
// "run once with metrics, once without" would silently be "run twice with
// metrics" — the before/after this ticket asks for would compare a run to
// itself, and the token delta would read as zero for a reason that has nothing
// to do with the feature.
//
// Non-fatal, like ensureDefaultAgent: a metric that will not save costs the
// metric_registry cases, not the run, and the failure is named in the log.
func ensureMetrics(ctx context.Context, stack *bootstrap.Stack, companyID, userID string, want bool) {
	if stack.Metrics == nil {
		if want {
			logrus.Warn("eval: no metric service on the stack; metric_registry cases will fail")
		}
		return
	}
	existing, err := stack.Metrics.List(ctx, companyID)
	if err != nil {
		logrus.WithError(err).Warn("eval: could not list metrics; metric_registry cases may not score what they claim")
		return
	}
	have := make(map[string]string, len(existing))
	for _, m := range existing {
		have[m.Key] = m.ID
	}

	if !want {
		for key, id := range have {
			if err := stack.Metrics.Delete(ctx, companyID, id); err != nil {
				logrus.WithError(err).WithField("key", key).Warn("eval: could not remove metric for a no-metrics run")
				continue
			}
			logrus.WithField("key", key).Info("eval: removed metric (running without the registry)")
		}
		return
	}

	source, err := primarySourceID(ctx, stack, companyID)
	if err != nil {
		logrus.WithError(err).Warn("eval: no primary source to define metrics against")
		return
	}
	for _, m := range evalMetrics {
		if _, ok := have[m.key]; ok {
			continue
		}
		if _, err := stack.Metrics.Create(ctx, companyID, userID, app.MetricInput{
			SourceID:    source,
			Key:         m.key,
			Label:       m.label,
			Description: m.description,
			SQLTemplate: m.template,
			ValueColumn: "value",
			Grain:       domain.MetricGrainMonth,
			Unit:        domain.MetricUnit(m.unit),
			Currency:    m.currency,
		}); err != nil {
			// Create validates by executing, so this is also the check that the
			// template still works against the demo warehouse.
			logrus.WithError(err).WithField("key", m.key).Warn("eval: could not define metric")
			continue
		}
		logrus.WithField("key", m.key).Info("eval: defined metric")
	}
}

func primarySourceID(ctx context.Context, stack *bootstrap.Stack, companyID string) (string, error) {
	sources, err := stack.Connections.ListByCompany(ctx, companyID)
	if err != nil {
		return "", fmt.Errorf("list sources: %w", err)
	}
	for _, c := range sources {
		if c.Label == primarySourceLabel {
			return c.ID, nil
		}
	}
	return "", fmt.Errorf("no source labelled %q", primarySourceLabel)
}

func ensureSources(ctx context.Context, stack *bootstrap.Stack, companyID, demoDSN, metabaseHostPort string) error {
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
	return syncToMetabase(ctx, stack, companyID, metabaseHostPort)
}

// syncToMetabase registers every source of the eval tenant as a Metabase
// database, which is what create_visualization needed and what creating a
// source through this file — rather than through the HTTP API — skips.
//
// Found while gating T-16: every chart case had been failing with "warehouse
// not synced to Metabase", so the three chart_dashboard cases were scoring
// the agent's reaction to a broken tool rather than its ability to build a
// dashboard. Idempotent — sources that already carry an ID are left alone,
// and a Metabase that is down costs three cases, not the run.
//
// **Vestigial since T-D11 (2026-08-17).** create_visualization is deleted and
// create_dashboard is native, so no case in golden.yaml needs a Metabase
// database id any more. The step is left in place rather than removed because
// it is idempotent, costs one round trip per source at setup, and the tenant
// rows it writes are still read by the Metabase-backed surfaces this repo has
// not decommissioned yet. Delete it with T-D15.
func syncToMetabase(ctx context.Context, stack *bootstrap.Stack, companyID, metabaseHostPort string) error {
	if stack.MetabaseSync == nil {
		logrus.Debug("eval: no Metabase client configured; harmless since T-D11 — no case needs one")
		return nil
	}
	sources, err := stack.Connections.ListByCompany(ctx, companyID)
	if err != nil {
		return fmt.Errorf("list sources for metabase sync: %w", err)
	}
	for _, conn := range sources {
		if conn.MetabaseDatabaseID != nil && *conn.MetabaseDatabaseID != 0 {
			continue
		}
		dsn, err := stack.DSNCipher.Decrypt(conn.DSNEncrypted)
		if err != nil {
			logrus.WithError(err).WithField("label", conn.Label).Warn("eval: decrypt DSN for metabase sync")
			continue
		}
		id, err := stack.MetabaseSync.SyncCompanyDatabase(ctx, conn, swapHostPort(dsn, metabaseHostPort))
		if err != nil {
			logrus.WithError(err).WithField("label", conn.Label).
				Warn("eval: metabase warehouse sync failed; harmless since T-D11 — no case needs this id")
			continue
		}
		conn.MetabaseDatabaseID = &id
		if err := stack.Connections.Update(ctx, conn); err != nil {
			return fmt.Errorf("persist metabase id for %s: %w", conn.Label, err)
		}
		logrus.WithFields(logrus.Fields{"label": conn.Label, "metabase_database_id": id}).
			Info("eval: source registered with Metabase")
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

// swapHostPort replaces host:port in a postgres URL, leaving credentials,
// database and query string alone.
//
// Needed because Metabase and this process reach the demo database by
// different names. The harness runs on the host and connects to
// localhost:5433; Metabase runs inside compose and must be handed
// postgres_demo:5432, or it rejects the registration with "check your host
// settings" and every chart case fails on a tool that never worked.
func swapHostPort(dsn, hostPort string) string {
	if hostPort == "" {
		return dsn
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	u.Host = hostPort
	return u.String()
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
