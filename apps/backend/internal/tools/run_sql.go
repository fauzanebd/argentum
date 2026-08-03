// Package tools implements the agent's tool surface: read-only SQL, schema
// retrieval, source resolution, Metabase cards and dashboards, document
// generation and scheduled tasks. Every tool resolves its tenant from
// tenantctx rather than from its arguments.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/domain"
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

	logrus.WithFields(logrus.Fields{
		"company_id": companyID,
		"source_id":  source.ID,
		"db_type":    source.DBType,
		"sql":        params.SQL,
	}).Info("Executing SQL query")

	conn, err := t.pool.For(ctx, companyID, source.ID)
	if err != nil {
		return "", fmt.Errorf("resolve tenant connection: %w", err)
	}

	result, err := conn.ExecuteReadOnly(ctx, params.SQL, t.maxRows)
	if err != nil {
		return "", explainSQLError(ctx, t.schema, companyID, source.ID, params.SQL, err)
	}

	t.recorder.RecordSQL(ctx, companyID, tenantctx.ThreadID(ctx))

	return string(marshalSQLResult(source.ID, source.DBType, result, t.maxBytes)), nil
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
func marshalSQLResult(sourceID, dbType string, result *db.QueryResult, maxBytes int) []byte {
	payload := buildSQLPayload(sourceID, dbType, result)
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
		out, _ = json.Marshal(payload)
	}
	return out
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
	if result.Count == 0 && !result.Truncated {
		payload["note"] = "The query succeeded but matched ZERO rows. There is no figure in this result. " +
			"Tell the user no data matched, and say what you filtered on so they can correct it. " +
			"Do NOT state a total, count or amount — there isn't one. If you suspect the filter is " +
			"wrong (a label that does not match exactly, a date range outside the data), say so and " +
			"offer to check the available values."
	}
	return payload
}
