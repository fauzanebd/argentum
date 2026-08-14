package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/metric"
)

// The metric registry service (T-06/T-07).
//
// It owns the two rules that make a metric trustworthy: a definition is a single
// SELECT whose window is bound as parameters, and it does not save unless it
// actually runs and returns one numeric row. Everything the agent later reads
// through query_metric (T-07) goes through the same evaluate() path, so the
// number a watcher fires on is the number the admin validated.

const (
	metricKeyMax         = 60
	metricLabelMax       = 120
	metricDescriptionMax = 1000
	metricTemplateMax    = 8000
	metricColumnMax      = 120
)

// metricKeyRe is the handle grammar: a lowercase identifier, because the key is
// what the agent names in a tool call and what a URL and a log line carry, and
// "Revenue (Net)" is none of those.
var metricKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// MetricConnResolver is the half of the tenant pool this service needs: a
// read-only connection to one source, carrying the parameterised executor.
// *db.TenantConnPool satisfies it.
type MetricConnResolver interface {
	For(ctx context.Context, companyID, sourceID string) (db.Conn, error)
}

// MetricSourceLookup is the one read the service makes of the connection repo:
// a source by id, to learn its db_type (for the dialect) and confirm it belongs
// to the company. domain.ConnectionRepository satisfies it; narrowed here so the
// service is testable without the whole repository.
type MetricSourceLookup interface {
	GetByID(ctx context.Context, id string) (*domain.DBConnection, error)
}

// MetricService is the admin CRUD surface and the turn-time query path for the
// registry.
type MetricService struct {
	repo  domain.MetricRepository
	conns MetricSourceLookup
	pool  MetricConnResolver
	// now is time.Now, injected so validate-on-save and comparison windows are
	// deterministic in tests.
	now func() time.Time
}

func NewMetricService(repo domain.MetricRepository, conns MetricSourceLookup, pool MetricConnResolver) *MetricService {
	return &MetricService{repo: repo, conns: conns, pool: pool, now: time.Now}
}

// MetricInput is one submitted metric. HigherIsBetter and Enabled are pointers
// so an update that omits them keeps the stored value rather than silently
// flipping a flag a client did not know existed.
type MetricInput struct {
	SourceID       string             `json:"source_id"`
	Key            string             `json:"key"`
	Label          string             `json:"label"`
	Description    string             `json:"description"`
	SQLTemplate    string             `json:"sql_template"`
	ValueColumn    string             `json:"value_column"`
	Grain          domain.MetricGrain `json:"grain"`
	Unit           domain.MetricUnit  `json:"unit"`
	Currency       string             `json:"currency"`
	HigherIsBetter *bool              `json:"higher_is_better"`
	Enabled        *bool              `json:"enabled"`
}

// List returns the company's metrics.
func (s *MetricService) List(ctx context.Context, companyID string) ([]*domain.MetricDefinition, error) {
	out, err := s.repo.ListByCompany(ctx, companyID)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []*domain.MetricDefinition{}
	}
	return out, nil
}

// Get returns one metric, 404 for another company's.
func (s *MetricService) Get(ctx context.Context, companyID, id string) (*domain.MetricDefinition, error) {
	return s.repo.GetByID(ctx, companyID, id)
}

// Create validates and stores a metric. A metric that does not render, does not
// run, or does not return exactly one numeric row is a rejected save (T-06): the
// registry's whole value is that everything in it is known to work.
func (s *MetricService) Create(ctx context.Context, companyID, createdBy string, in MetricInput) (*domain.MetricDefinition, error) {
	m, err := s.validated(ctx, companyID, in, nil)
	if err != nil {
		return nil, err
	}
	m.CreatedBy = createdBy
	if err := s.repo.Create(ctx, m); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			return nil, fmt.Errorf("%w: a metric with key %q already exists", domain.ErrAlreadyExists, m.Key)
		}
		return nil, err
	}
	logrus.WithFields(logrus.Fields{
		"company_id": companyID, "metric_id": m.ID, "key": m.Key, "source_id": m.SourceID,
	}).Info("metric created")
	return m, nil
}

// Update rewrites a metric, revalidating it against the database exactly as
// Create does — an edit that breaks the SQL is refused, not stored.
func (s *MetricService) Update(ctx context.Context, companyID, id string, in MetricInput) (*domain.MetricDefinition, error) {
	current, err := s.repo.GetByID(ctx, companyID, id)
	if err != nil {
		return nil, err
	}
	m, err := s.validated(ctx, companyID, in, current)
	if err != nil {
		return nil, err
	}
	m.ID = current.ID
	m.CreatedBy = current.CreatedBy
	m.CreatedAt = current.CreatedAt
	if err := s.repo.Update(ctx, m); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			return nil, fmt.Errorf("%w: a metric with key %q already exists", domain.ErrAlreadyExists, m.Key)
		}
		return nil, err
	}
	return m, nil
}

// Delete removes a metric.
func (s *MetricService) Delete(ctx context.Context, companyID, id string) error {
	return s.repo.Delete(ctx, companyID, id)
}

// ListEnabled is what list_metrics and the turn catalog read: only the metrics
// an admin has left switched on reach the agent.
func (s *MetricService) ListEnabled(ctx context.Context, companyID string) ([]*domain.MetricDefinition, error) {
	out, err := s.repo.ListEnabled(ctx, companyID)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []*domain.MetricDefinition{}
	}
	return out, nil
}

// Query is the turn-time path (T-07): resolve a metric by key, evaluate it over
// [from, to), and optionally against a comparison window. An unknown key is an
// error the tool turns into "no metric called that; here are the ones there
// are". The result types live in internal/metric so the tool can name them
// without importing this package.
func (s *MetricService) Query(
	ctx context.Context, companyID, key string, from, to time.Time, compare metric.Comparison,
) (*metric.Result, error) {
	m, err := s.repo.GetByKey(ctx, companyID, key)
	if err != nil {
		return nil, err
	}
	if !m.Enabled {
		return nil, fmt.Errorf("%w: metric %q is disabled", domain.ErrInvalidInput, key)
	}
	primary, err := s.evaluate(ctx, companyID, m, metric.Window{From: from, To: to})
	if err != nil {
		return nil, err
	}
	res := &metric.Result{Metric: m, Primary: primary}
	if compare != "" {
		if !compare.Valid() {
			return nil, fmt.Errorf("%w: unknown comparison %q", domain.ErrInvalidInput, compare)
		}
		cw, err := (metric.Window{From: from, To: to}).Shift(compare)
		if err != nil {
			return nil, err
		}
		cmp, err := s.evaluate(ctx, companyID, m, cw)
		if err != nil {
			return nil, err
		}
		res.Comparison = &cmp
		// No delta when either side matched nothing. Subtracting from an absent
		// number produces the same false statement the zero did — "down 100% on
		// last quarter" for a quarter we hold no data for — and it would carry
		// further, because a watcher fires on a delta.
		if !primary.Empty && !cmp.Empty {
			delta := primary.Value - cmp.Value
			res.Delta = &delta
			if cmp.Value != 0 {
				pct := delta / abs(cmp.Value) * 100
				res.DeltaPct = &pct
			}
		}
	}
	return res, nil
}

// Test renders and runs a metric input over the validation window without
// storing it — the dashboard's "Test" button, so an admin sees the SQL and the
// number before they save. It runs the same validation Create would, so a Test
// that passes is a save that will.
func (s *MetricService) Test(ctx context.Context, companyID string, in MetricInput) (*metric.Evaluation, error) {
	m, err := s.validatedFields(companyID, in, nil)
	if err != nil {
		return nil, err
	}
	ev, err := s.evaluate(ctx, companyID, m, metric.ValidationWindow(s.now()))
	if err != nil {
		return nil, err
	}
	if err := refuseEmptyValidation(ev); err != nil {
		return nil, err
	}
	return &ev, nil
}

// evaluate renders the template with the window bound as parameters, runs it in
// a read-only transaction, and reads the single numeric value out. It is the
// one place a metric touches the database, shared by validate-on-save, Test and
// Query, so all three agree on what a metric's number is.
func (s *MetricService) evaluate(ctx context.Context, companyID string, m *domain.MetricDefinition, w metric.Window) (metric.Evaluation, error) {
	source, err := s.conns.GetByID(ctx, m.SourceID)
	if err != nil {
		return metric.Evaluation{}, fmt.Errorf("resolve metric source: %w", err)
	}
	if source.CompanyID != companyID {
		// The metric was read scoped to the company, so this should be
		// impossible; refusing rather than trusting keeps a mis-scoped read from
		// running one tenant's SQL on another's word.
		return metric.Evaluation{}, domain.ErrNotFound
	}
	driver, err := db.Get(source.DBType)
	if err != nil {
		return metric.Evaluation{}, fmt.Errorf("metric source driver: %w", err)
	}
	sql, args, err := metric.Render(m.SQLTemplate, driver.Dialect().Placeholder, w.From, w.To)
	if err != nil {
		return metric.Evaluation{}, fmt.Errorf("render metric: %w", err)
	}
	conn, err := s.pool.For(ctx, companyID, m.SourceID)
	if err != nil {
		return metric.Evaluation{}, fmt.Errorf("connect to metric source: %w", err)
	}
	// maxRows 2: enough to tell "exactly one" from "more than one" without
	// pulling a template that forgot to aggregate.
	result, err := conn.ExecuteReadOnlyParams(ctx, sql, args, 2)
	if err != nil {
		return metric.Evaluation{}, fmt.Errorf("run metric: %w", err)
	}
	if result.Count != 1 {
		return metric.Evaluation{}, fmt.Errorf(
			"%w: the metric returned %d rows, not one — a metric must aggregate to a single row",
			domain.ErrInvalidInput, result.Count)
	}
	raw, ok := result.Rows[0][m.ValueColumn]
	if !ok {
		return metric.Evaluation{}, fmt.Errorf(
			"%w: the result has no column %q — value_column must name a selected column",
			domain.ErrInvalidInput, m.ValueColumn)
	}
	// NULL is not a broken template: it is what SUM, AVG and MAX return over an
	// empty set, on all three dialects. Reported rather than refused, so the
	// caller can say "no data in this window" instead of either a wrong zero or
	// an error message about types (T-Q9's distinction, which run_sql has had
	// since 2026-08-11 and this path had not). The save and Test paths still
	// refuse it — see validated() and Test() — because a metric that matches
	// nothing over the validation window is a definition nobody should keep.
	if raw == nil {
		return metric.Evaluation{From: w.From, To: w.To, RenderedSQL: sql, Empty: true}, nil
	}
	value, err := toFloat(raw)
	if err != nil {
		return metric.Evaluation{}, fmt.Errorf("%w: column %q is not a number (%v)", domain.ErrInvalidInput, m.ValueColumn, err)
	}
	return metric.Evaluation{Value: value, From: w.From, To: w.To, RenderedSQL: sql}, nil
}

// validated turns input into a metric, checked all the way through an actual
// run. current is nil on create, the stored row on update.
func (s *MetricService) validated(ctx context.Context, companyID string, in MetricInput, current *domain.MetricDefinition) (*domain.MetricDefinition, error) {
	m, err := s.validatedFields(companyID, in, current)
	if err != nil {
		return nil, err
	}
	// The save-defining check: render it, run it, and require one numeric row.
	ev, err := s.evaluate(ctx, companyID, m, metric.ValidationWindow(s.now()))
	if err != nil {
		return nil, err
	}
	if err := refuseEmptyValidation(ev); err != nil {
		return nil, err
	}
	return m, nil
}

// refuseEmptyValidation keeps the save and Test paths exactly as strict as they
// were before evaluate started reporting NULL instead of erroring on it.
//
// The turn-time reading changed and this one did not, on purpose. A metric that
// matches nothing over the *validation* window is a definition an admin should
// fix before it is stored — a filter that never matches, a column that is
// always NULL, a table that has not been loaded — and a stored metric that
// silently answers "no data" every time is the shape nobody notices. What
// changed is the sentence: "matched no rows" says what to look at, where "is
// not a number (value is null)" reads as a type error in a template that has
// none.
func refuseEmptyValidation(ev metric.Evaluation) error {
	if !ev.Empty {
		return nil
	}
	return fmt.Errorf(
		"%w: the metric ran but matched no rows over the validation window (%s to %s), so it has no "+
			"value — check the filters and the date column before saving it",
		domain.ErrInvalidInput,
		ev.From.Format("2006-01-02"), ev.To.Format("2006-01-02"))
}

// validatedFields does the structural validation without touching the database,
// so Test and validated share it.
func (s *MetricService) validatedFields(companyID string, in MetricInput, current *domain.MetricDefinition) (*domain.MetricDefinition, error) {
	key := strings.TrimSpace(in.Key)
	label := strings.TrimSpace(in.Label)
	description := strings.TrimSpace(in.Description)
	template := strings.TrimSpace(in.SQLTemplate)
	valueColumn := strings.TrimSpace(in.ValueColumn)
	currency := strings.TrimSpace(in.Currency)

	switch {
	case companyID == "":
		return nil, fmt.Errorf("%w: a company is required", domain.ErrInvalidInput)
	case key == "":
		return nil, fmt.Errorf("%w: a metric needs a key", domain.ErrInvalidInput)
	case len(key) > metricKeyMax:
		return nil, fmt.Errorf("%w: key must be %d characters or fewer", domain.ErrInvalidInput, metricKeyMax)
	case !metricKeyRe.MatchString(key):
		return nil, fmt.Errorf("%w: key must be a lowercase identifier like %q or %q", domain.ErrInvalidInput, "revenue", "active_customers")
	case label == "":
		return nil, fmt.Errorf("%w: a metric needs a label", domain.ErrInvalidInput)
	case len([]rune(label)) > metricLabelMax:
		return nil, fmt.Errorf("%w: label must be %d characters or fewer", domain.ErrInvalidInput, metricLabelMax)
	case len([]rune(description)) > metricDescriptionMax:
		return nil, fmt.Errorf("%w: description must be %d characters or fewer", domain.ErrInvalidInput, metricDescriptionMax)
	case in.SourceID == "":
		return nil, fmt.Errorf("%w: a metric needs a source", domain.ErrInvalidInput)
	case template == "":
		return nil, fmt.Errorf("%w: a metric needs a SQL template", domain.ErrInvalidInput)
	case len(template) > metricTemplateMax:
		return nil, fmt.Errorf("%w: that SQL template is too long", domain.ErrInvalidInput)
	case valueColumn == "":
		return nil, fmt.Errorf("%w: a metric needs a value_column", domain.ErrInvalidInput)
	case len(valueColumn) > metricColumnMax:
		return nil, fmt.Errorf("%w: value_column is too long", domain.ErrInvalidInput)
	case !in.Grain.Valid():
		return nil, fmt.Errorf("%w: grain must be one of day, week, month, quarter, year", domain.ErrInvalidInput)
	case !in.Unit.Valid():
		return nil, fmt.Errorf("%w: unit must be one of currency, count, percent, ratio", domain.ErrInvalidInput)
	case in.Unit == domain.MetricUnitCurrency && currency == "":
		return nil, fmt.Errorf("%w: a currency metric needs a currency code", domain.ErrInvalidInput)
	}

	if err := metric.ValidateTemplate(template); err != nil {
		return nil, fmt.Errorf("%w: %s", domain.ErrInvalidInput, err.Error())
	}

	higherIsBetter := true
	if in.HigherIsBetter != nil {
		higherIsBetter = *in.HigherIsBetter
	} else if current != nil {
		higherIsBetter = current.HigherIsBetter
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	} else if current != nil {
		enabled = current.Enabled
	}
	if in.Unit != domain.MetricUnitCurrency {
		currency = "" // a non-currency metric carries no currency, whatever was sent
	}

	return &domain.MetricDefinition{
		CompanyID:      companyID,
		SourceID:       in.SourceID,
		Key:            key,
		Label:          label,
		Description:    description,
		SQLTemplate:    template,
		ValueColumn:    valueColumn,
		Grain:          in.Grain,
		Unit:           in.Unit,
		Currency:       currency,
		HigherIsBetter: higherIsBetter,
		Enabled:        enabled,
	}, nil
}

// toFloat coerces the many shapes a driver returns a numeric column as into a
// float64. A null value is an error, not a zero: "no rows matched" and "the sum
// is zero" are different facts, and a metric that silently reports 0 for a
// broken window is the fabrication this whole registry exists to prevent.
func toFloat(v any) (float64, error) {
	switch n := v.(type) {
	case nil:
		return 0, fmt.Errorf("value is null")
	case float64:
		return n, nil
	case float32:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case int32:
		return float64(n), nil
	case int:
		return float64(n), nil
	case []byte:
		return parseFloat(string(n))
	case string:
		return parseFloat(n)
	default:
		return 0, fmt.Errorf("unexpected type %T", v)
	}
}

func parseFloat(s string) (float64, error) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, fmt.Errorf("not numeric")
	}
	return f, nil
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
