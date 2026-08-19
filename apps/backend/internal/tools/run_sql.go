// Package tools implements the agent's tool surface: read-only SQL, schema
// retrieval, source resolution, Metabase cards and dashboards, document
// generation and scheduled tasks. Every tool resolves its tenant from
// tenantctx rather than from its arguments.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/sqlguard"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// RunSQLTool executes read-only SQL against ONE of the tenant's analytical
// databases. The connection is resolved per-request from
// (tenantctx.CompanyID, source_id) via the injected pool, so the same agent
// instance can serve every company and route to any of their sources.
type RunSQLTool struct {
	pool     *db.TenantConnPool
	repo     domain.ConnectionRepository
	recorder UsageRecorder
	maxRows  int
	maxBytes int
	// schema turns a driver's name error into the list of names that would
	// have worked. Optional: nil leaves the driver's own message untouched.
	schema SchemaProvider
	// companies supplies the tenant's PII redaction mode, which decides what
	// the empty-result probe may disclose (T-H10). Optional: nil reads as
	// strict, so a build that forgets to wire it discloses less rather than
	// more.
	companies PIIPolicyLookup
}

// PIIPolicyLookup is the one question run_sql asks of the company repository:
// this tenant's redaction policy. domain.CompanyRepository satisfies it;
// narrowed here so the tool does not acquire the whole repository, and so a
// test can answer it without a database.
type PIIPolicyLookup interface {
	GetByID(ctx context.Context, id string) (*domain.Company, error)
}

// UsageRecorder is the narrow interface tools depend on for metering. Kept
// in this file (and not in internal/app) to avoid an import cycle since
// internal/app already depends on internal/tools.
type UsageRecorder interface {
	RecordSQL(ctx context.Context, companyID, threadID string)
	RecordMetabaseCard(ctx context.Context, companyID, threadID string)
	RecordMetabaseDashboard(ctx context.Context, companyID, threadID string)
	RecordDocument(ctx context.Context, companyID, threadID, format string)
}

// nopRecorder satisfies UsageRecorder when metering is disabled.
type nopRecorder struct{}

func (nopRecorder) RecordSQL(context.Context, string, string)               {}
func (nopRecorder) RecordMetabaseCard(context.Context, string, string)      {}
func (nopRecorder) RecordMetabaseDashboard(context.Context, string, string) {}
func (nopRecorder) RecordDocument(context.Context, string, string, string)  {}

// NewRunSQLTool wires the run_sql tool. maxRows is a hard ceiling on the
// number of rows returned to the LLM; maxBytes is a secondary cap on the
// serialized JSON payload (catches wide-column results that fit under
// maxRows but still blow up context). Non-positive values disable the
// corresponding cap.
func NewRunSQLTool(pool *db.TenantConnPool, repo domain.ConnectionRepository, recorder UsageRecorder, maxRows, maxBytes int) *RunSQLTool {
	if recorder == nil {
		recorder = nopRecorder{}
	}
	return &RunSQLTool{pool: pool, repo: repo, recorder: recorder, maxRows: maxRows, maxBytes: maxBytes}
}

// WithSchema lets a failed query answer itself: on a column or table name the
// source does not have, the error carries the names it does have. Pass the same
// *GetSchemaTool the agent calls, so the lookup hits a warm cache instead of
// re-introspecting the tenant's database.
//
// Optional rather than a constructor argument because the two callers that only
// list tool names have no schema tool to give, and a query that never runs
// needs no hint.
func (t *RunSQLTool) WithSchema(p SchemaProvider) *RunSQLTool {
	t.schema = p
	return t
}

// WithPIIPolicy lets the empty-result probe honour the tenant's redaction mode
// (T-H10). A tenant on `contact_ok` has said they want customer contact details
// in answers, and a tenant on `off` has switched redaction off entirely; both
// are decisions the probe should follow rather than second-guess.
//
// Optional, and unset means strict: the probe discloses data the user's own
// query did not return, so "we could not find out what this tenant allows" has
// to answer the same way as "this tenant allows nothing".
func (t *RunSQLTool) WithPIIPolicy(p PIIPolicyLookup) *RunSQLTool {
	t.companies = p
	return t
}

// piiMode reads the tenant's policy, on the zero-row path only — the probe is
// the only thing that consults it, and it fires on a result with no rows in it.
// A lookup that fails is strict, and says so once at Warn rather than silently.
func (t *RunSQLTool) piiMode(ctx context.Context, companyID string) domain.PIIRedactionMode {
	if t.companies == nil {
		return domain.PIIRedactionStrict
	}
	c, err := t.companies.GetByID(ctx, companyID)
	if err != nil || c == nil {
		logrus.WithError(err).WithField("company_id", companyID).
			Warn("pii policy lookup failed; the empty-result probe is treated as strict")
		return domain.PIIRedactionStrict
	}
	return normalizePIIMode(c.PIIRedactionMode)
}

func (t *RunSQLTool) Name() string { return "run_sql" }

func (t *RunSQLTool) Description() string {
	return "Execute a read-only SQL query against ONE connected analytics database and return results. " +
		"Pass source_id to pick which database when the company has more than one. Only SELECT queries are allowed."
}

func (t *RunSQLTool) Parameters() map[string]interfaces.ParameterSpec {
	return map[string]interfaces.ParameterSpec{
		"sql": {
			Type:        "string",
			Description: "The SELECT query to execute. Must use the SQL dialect of the chosen source (see db_type from get_schema).",
			Required:    true,
		},
		"source_id": {
			Type:        "string",
			Description: "ID of the data source to query. Required when more than one source is registered. If omitted and only one source exists, that source is used.",
			Required:    false,
		},
	}
}

func (t *RunSQLTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

func (t *RunSQLTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		SQL      string `json:"sql"`
		SourceID string `json:"source_id"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		params.SQL = args
	}
	if params.SQL == "" {
		return "", fmt.Errorf("sql parameter is required")
	}

	companyID := tenantctx.CompanyID(ctx)
	if companyID == "" {
		return "", fmt.Errorf("no tenant in context: cannot resolve database connection")
	}

	source, err := ResolveSource(ctx, t.repo, companyID, params.SourceID)
	if err != nil {
		return "", err
	}

	// The statement's shape at Info, its values at Debug (T-H7). This line used
	// to carry `params.SQL` verbatim, so a question about one person wrote that
	// person's identifier into the operational log on the level production runs
	// at. What an operator reads a query log for — which tables, how often,
	// whether a turn is looping — is all in the normalised form.
	logrus.WithFields(logrus.Fields{
		"company_id": companyID,
		"source_id":  source.ID,
		"db_type":    source.DBType,
		"sql":        normalizeSQLForLog(params.SQL),
	}).Info("Executing SQL query")
	logrus.WithFields(logrus.Fields{
		"company_id": companyID,
		"source_id":  source.ID,
		"sql_raw":    params.SQL,
	}).Debug("Executing SQL query (literals intact)")

	// Refused after the log lines and before the dial: an operator reading the
	// query log for what a turn attempted should see the statement that was
	// refused, and a statement that is not a read has no business opening a
	// connection to the tenant's database.
	if err := guardStatement(params.SQL); err != nil {
		logrus.WithFields(logrus.Fields{
			"company_id": companyID,
			"source_id":  source.ID,
			"db_type":    source.DBType,
			"sql":        normalizeSQLForLog(params.SQL),
		}).Warn("run_sql refused a statement that is not a single read")
		return "", err
	}

	conn, err := t.pool.For(ctx, companyID, source.ID)
	if err != nil {
		return "", fmt.Errorf("resolve tenant connection: %w", err)
	}

	result, err := conn.ExecuteReadOnly(ctx, params.SQL, t.maxRows)
	if err != nil {
		return "", explainSQLError(ctx, t.schema, companyID, source.ID, params.SQL, err)
	}

	t.recorder.RecordSQL(ctx, companyID, tenantctx.ThreadID(ctx))

	// What a document source returns is subject to the tenant's redaction mode
	// (T-P12), and it is checked here — on a result that *has* rows — because
	// the probe below only ever runs on one that does not. The 2026-08-19 gate
	// found the gap the hard way: a `strict` tenant's published customer list
	// came back over MCP with three real email addresses on it.
	//
	// **Scoped to a document source on purpose.** A tenant's own warehouse is
	// theirs and its columns are theirs to query; a table this product wrote out
	// of a PDF is one nobody chose the shape of, which is the asymmetry T-P12
	// names. Widening this to every source is a bigger change than a bug fix —
	// it moves what reaches the model on ordinary warehouse turns, so it owes a
	// rule-1 re-score rather than a same-sitting patch.
	var redactedCols []string
	if source.Origin == domain.OriginDocument {
		redactedCols = RedactResultColumns(result.Columns, result.Rows, t.piiMode(ctx, companyID))
		if len(redactedCols) > 0 {
			// The columns, never their contents — the same rule the probe's own
			// log line follows, and for the sharper reason here: these are the
			// values the redaction just took out of the answer.
			logrus.WithFields(logrus.Fields{
				"company_id":       companyID,
				"source_id":        source.ID,
				"redacted_columns": redactedCols,
			}).Info("document-source result: columns withheld under the tenant's redaction policy")
		}
	}

	// A query that ran and matched nothing gets asked why (T-Q9). Only in that
	// case: the probe is a second round trip to the tenant's database, and a
	// result with rows in it has already answered the question.
	var probes []map[string]interface{}
	if matchedNothing(result) {
		probes = probeEmptyResult(ctx, conn, t.schema, companyID, source.ID, source.DBType, params.SQL, t.piiMode(ctx, companyID))
		if len(probes) > 0 {
			// Which columns, not what is in them (T-H7/T-H10). This line used to
			// log `probeJSON(probes)`, so a probe's whole disclosure — the real
			// contents of a filtered column — was written to the operational log
			// as well as handed to the model. The values are still available at
			// Debug for anyone reproducing a probe.
			logrus.WithFields(logrus.Fields{
				"company_id":     companyID,
				"source_id":      source.ID,
				"probed_columns": probedColumns(probes),
			}).Info("empty result probed: the filtered columns' actual values were returned to the agent")
			logrus.WithFields(logrus.Fields{
				"company_id": companyID,
				"source_id":  source.ID,
				"probes":     probeJSON(probes),
			}).Debug("empty result probe payload")
		}
	}

	return string(marshalSQLResult(source.ID, source.DBType, result, t.maxBytes, probes, redactedCols)), nil
}

// marshalSQLResult serialises a query result for the model, dropping rows from
// the tail until the payload fits under maxBytes. Even within maxRows, very
// wide columns can blow the context window. A non-positive maxBytes disables
// the cap.
//
// Re-marshalling per shrink is O(n²) in row count, but maxRows is small
// (default 100), so this is fine.
//
// Split out of Execute so the trimming loop is reachable without a live tenant
// connection: it is the branch that decides how much of a result the model
// ever sees.
func marshalSQLResult(sourceID, dbType string, result *db.QueryResult, maxBytes int, probes []map[string]interface{}, redacted []string) []byte {
	payload := buildSQLPayload(sourceID, dbType, result)
	// Before the shrink loop, and never shrunk away: the probe replaces the
	// zero-row note, and a payload that lost it would tell the model nothing
	// about why it got no rows. A zero-row result has no rows to drop anyway,
	// so the loop below cannot run on the one payload this affects.
	attachProbe(payload, probes)
	attachRedaction(payload, redacted)
	out, _ := json.Marshal(payload)
	if maxBytes <= 0 || len(out) <= maxBytes {
		return out
	}

	rows := result.Rows
	for len(rows) > 0 && len(out) > maxBytes {
		rows = rows[:len(rows)-1]
		result.Rows = rows
		result.Count = len(rows)
		result.Truncated = true
		payload = buildSQLPayload(sourceID, dbType, result)
		attachProbe(payload, probes)
		attachRedaction(payload, redacted)
		out, _ = json.Marshal(payload)
	}
	return out
}

// attachRedaction tells the model which columns it is not seeing, and that they
// were withheld rather than empty (T-P12).
//
// The sentence matters as much as the marker in the cells. A model handed a
// column of `[CONTACT REDACTED]` with no explanation has been known to describe
// it as missing data — and "the customer emails are not recorded" is a false
// statement about the tenant's own document, made by an instrument that was
// working correctly.
func attachRedaction(payload map[string]interface{}, redacted []string) {
	if len(redacted) == 0 {
		return
	}
	payload["redacted_columns"] = redacted
	payload["redaction_note"] = "These columns hold personal data and were withheld by this workspace's " +
		"privacy setting: " + strings.Join(redacted, ", ") + ". The values exist in the source — they were " +
		"NOT empty. Say they are withheld by policy if the user asks; do not describe them as missing, " +
		"blank or unrecorded, and do not try to recover them with another query."
}

func buildSQLPayload(sourceID, dbType string, result *db.QueryResult) map[string]interface{} {
	payload := map[string]interface{}{
		"source_id": sourceID,
		"db_type":   dbType,
		"columns":   result.Columns,
		"rows":      result.Rows,
		"row_count": result.Count,
		"truncated": result.Truncated,
	}
	if result.Truncated {
		payload["note"] = "Result was truncated to fit context. Tell the user it is partial and suggest a filter (date range, category, aggregation, etc.) to narrow the query."
	}
	// A successful query that matched nothing is the second fabrication
	// mechanism T-16 exists to close: the first eval run caught the agent
	// answering "Total Sales for December 2024: IDR 1,488,000" off a
	// zero-row result, a figure with no origin in the data. The empty set
	// alone was not enough of a signal — asked the same way about November
	// it answered honestly — so the result now says in words what it means.
	if matchedNothing(result) {
		payload["note"] = "The query succeeded but matched ZERO rows. There is no figure in this result. " +
			"Tell the user no data matched, and say what you filtered on so they can correct it. " +
			"Do NOT state a total, count or amount — there isn't one. If you suspect the filter is " +
			"wrong (a label that does not match exactly, a date range outside the data), say so and " +
			"offer to check the available values."
	}
	return payload
}

// matchedNothing reports whether a successful query found no data — in either
// of the two shapes that produces.
//
// The obvious shape is zero rows. The other one is an aggregate over an empty
// set, which Postgres, MySQL and SQL Server all answer with exactly ONE row
// whose every column is NULL — and that is the shape of the question this
// product exists to get right. `SELECT SUM(sales_amount) … WHERE month_name =
// 'December'` against the padded `'December '` labels of E-5 returns
// `[{"total": null}]`, not `[]`: row_count 1, no zero-row note, no probe. The
// agent was handed a result with a row in it and answered **IDR 1,488,000**.
//
// So the count test alone left the fabrication mechanism's own question shape
// uncovered. Found by running the T-Q9 probe against the demo warehouse on
// 2026-08-11; every test of this path had used a row-returning SELECT.
//
// COUNT(*) is deliberately safe here: over an empty set it returns 0, not
// NULL, so an honest "there are none" is never mistaken for "nothing matched".
func matchedNothing(result *db.QueryResult) bool {
	if result == nil || result.Truncated {
		return false
	}
	if result.Count == 0 {
		return true
	}
	if result.Count != 1 || len(result.Rows) != 1 || len(result.Columns) == 0 {
		return false
	}
	row := result.Rows[0]
	if len(row) == 0 {
		return false
	}
	for _, v := range row {
		if v != nil {
			return false
		}
	}
	return true
}

// guardStatement is T-H4 step 3: the structural check run_sql was always
// supposed to run and did not.
//
// `sqlguard`'s package comment has named run_sql as one of its three callers
// since the package was promoted out of `metric.ValidateTemplate` under step 1,
// and it was the one caller that never called it — so model-authored SQL went
// from `params.SQL` to the driver with nothing structural in between. The two
// guardrail rules that read like they cover this, `block_sql_mutations` and
// `block_sql_injection`, are `scope: input` (config/guardrails.yaml:190,212):
// they screen the user's message and have never seen the model's output.
//
// What was actually holding the line is the read-only transaction, and it does
// hold on Postgres and MySQL. It does not exist on SQL Server — go-mssqldb
// refuses TxOptions.ReadOnly, so adapters/db/sqlserver/conn.go:36 begins a
// plain transaction and the only barrier there is the customer's db_datareader
// grant. That is the gap this closes, and it is the reason the check runs for
// every dialect rather than only where a parser exists.
//
// **This is defence in depth, not permission to loosen the login.** The grants
// remain the guarantee; nobody should read this function as a reason to hand
// Argentum a writable account.
//
// It stays an error rather than becoming a result, for explainSQLError's
// reason: a query that did not run is not evidence, agentbudget.Observe has to
// count it as a failure, and the audit row has to record it as one. The text
// reaches the model regardless — the provider feeds a tool error back into the
// conversation — so the refusal names what would have worked, because a model
// told only "no" spends another call discovering what "yes" looks like.
//
// `nil` declared tokens is deliberate: a metric declares from and to and a
// dashboard declares its filters, but nothing binds a `{{token}}` on this path,
// so a statement carrying one would otherwise reach the driver with the braces
// still in it.
func guardStatement(sql string) error {
	if err := sqlguard.ValidateStatement(sql, nil); err != nil {
		return fmt.Errorf(
			"run_sql refused this statement: %w. run_sql executes exactly one read — "+
				"a single SELECT, or a WITH … SELECT. Rewrite it as a read and try again",
			err,
		)
	}
	return nil
}
