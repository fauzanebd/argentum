package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/fauzanebd/argentum/internal/database"
	"github.com/sirupsen/logrus"
)

// RunSQLTool executes read-only SQL queries against the analytics database.
type RunSQLTool struct {
	db *database.DB
}

func NewRunSQLTool(db *database.DB) *RunSQLTool {
	return &RunSQLTool{db: db}
}

func (t *RunSQLTool) Name() string { return "run_sql" }
func (t *RunSQLTool) Description() string {
	return "Execute a read-only SQL query against the PostgreSQL analytics database and return results. Only SELECT queries are allowed."
}

func (t *RunSQLTool) Parameters() map[string]interfaces.ParameterSpec {
	return map[string]interfaces.ParameterSpec{
		"sql": {
			Type:        "string",
			Description: "The PostgreSQL SELECT query to execute",
			Required:    true,
		},
	}
}

func (t *RunSQLTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

func (t *RunSQLTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		SQL string `json:"sql"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		params.SQL = args
	}
	if params.SQL == "" {
		return "", fmt.Errorf("sql parameter is required")
	}

	logrus.WithField("sql", params.SQL).Info("Executing SQL query")

	result, err := t.db.ExecuteReadOnly(ctx, params.SQL)
	if err != nil {
		return "", fmt.Errorf("query execution failed: %w", err)
	}

	out, _ := json.Marshal(map[string]interface{}{
		"columns":   result.Columns,
		"rows":      result.Rows,
		"row_count": result.Count,
	})
	return string(out), nil
}
