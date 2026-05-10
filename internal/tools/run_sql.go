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

func NewRunSQLTool(pool *db.TenantConnPool, repo domain.ConnectionRepository, recorder UsageRecorder) *RunSQLTool {
	if recorder == nil {
		recorder = nopRecorder{}
	}
	return &RunSQLTool{pool: pool, repo: repo, recorder: recorder}
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

	result, err := conn.ExecuteReadOnly(ctx, params.SQL)
	if err != nil {
		return "", fmt.Errorf("query execution failed: %w", err)
	}

	t.recorder.RecordSQL(ctx, companyID, tenantctx.ThreadID(ctx))

	out, _ := json.Marshal(map[string]interface{}{
		"source_id": source.ID,
		"db_type":   source.DBType,
		"columns":   result.Columns,
		"rows":      result.Rows,
		"row_count": result.Count,
	})
	return string(out), nil
}
