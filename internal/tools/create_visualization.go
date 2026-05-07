package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/metabase"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// MetabaseTenantDB resolves Metabase `/api/database` id for this tenant's
// default analytical warehouse.
type MetabaseTenantDB interface {
	DefaultMetabaseDatabaseID(ctx context.Context, companyID string) (int, error)
}

// CreateVisualizationTool runs a SQL query against the tenant's analytics DB
// and registers a Metabase card for the result targeting that tenant's
// synced Metabase database connection (see CompanyService Metabase warehouse sync).
type CreateVisualizationTool struct {
	pool           *db.TenantConnPool
	metabaseClient *metabase.Client
	mbResolver     MetabaseTenantDB
	recorder       UsageRecorder
}

func NewCreateVisualizationTool(pool *db.TenantConnPool, metabaseClient *metabase.Client, mbResolver MetabaseTenantDB, recorder UsageRecorder) *CreateVisualizationTool {
	if recorder == nil {
		recorder = nopRecorder{}
	}
	return &CreateVisualizationTool{pool: pool, metabaseClient: metabaseClient, mbResolver: mbResolver, recorder: recorder}
}

func (t *CreateVisualizationTool) Name() string { return "create_visualization" }

func (t *CreateVisualizationTool) Description() string {
	return "Create a visualization card (question) in Metabase from a SQL query. Returns a card_id and chart_type. Remember the returned card_id — you MUST pass it in the 'cards' array when calling create_dashboard afterwards. Use create_dashboard to combine multiple cards into a single shareable dashboard. If the chart axes correlate with time (dates, months, weeks, etc.), the SQL MUST ORDER BY that time dimension ascending so the chart is chronological left-to-right; never rely on unspecified row order."
}

func (t *CreateVisualizationTool) Parameters() map[string]interfaces.ParameterSpec {
	return map[string]interfaces.ParameterSpec{
		"sql": {
			Type:        "string",
			Description: "The SQL query for the visualization. For time-based charts, include ORDER BY the time bucketing column ascending (earliest first).",
			Required:    true,
		},
		"name": {
			Type:        "string",
			Description: "Name for the visualization",
			Required:    true,
		},
		"description": {
			Type:        "string",
			Description: "Description of the visualization",
			Required:    false,
		},
		"chart_type": {
			Type:        "string",
			Description: "Type of chart to create",
			Required:    false,
			Default:     "auto",
			Enum:        []interface{}{"bar", "line", "pie", "table", "scalar", "auto"},
		},
	}
}

func (t *CreateVisualizationTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

func (t *CreateVisualizationTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		SQL         string `json:"sql"`
		Name        string `json:"name"`
		Description string `json:"description"`
		ChartType   string `json:"chart_type"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("failed to parse parameters: %w", err)
	}
	if params.SQL == "" {
		return "", fmt.Errorf("sql parameter is required")
	}
	if params.Name == "" {
		return "", fmt.Errorf("name parameter is required")
	}
	if params.ChartType == "" {
		params.ChartType = "auto"
	}

	companyID := tenantctx.CompanyID(ctx)
	if companyID == "" {
		return "", fmt.Errorf("no tenant in context: cannot resolve database connection")
	}

	conn, err := t.pool.For(ctx, companyID)
	if err != nil {
		return "", fmt.Errorf("resolve tenant connection: %w", err)
	}

	result, err := conn.ExecuteReadOnly(ctx, params.SQL)
	if err != nil {
		return "", fmt.Errorf("failed to execute query: %w", err)
	}

	chartType := params.ChartType
	if chartType == "auto" {
		columnTypes := make([]string, len(result.Columns))
		if len(result.Rows) > 0 {
			for i, col := range result.Columns {
				if val, ok := result.Rows[0][col]; ok {
					switch val.(type) {
					case int, int32, int64, float32, float64:
						columnTypes[i] = "number"
					default:
						columnTypes[i] = "string"
					}
				}
			}
		}
		chartType = metabase.DetectVisualizationType(len(result.Columns), result.Count, columnTypes)
	}

	if t.mbResolver == nil {
		return "", fmt.Errorf("Metabase resolver not configured")
	}
	databaseID, err := t.mbResolver.DefaultMetabaseDatabaseID(ctx, companyID)
	if err != nil {
		return "", fmt.Errorf("tenant Metabase database: %w", err)
	}

	card := &metabase.Card{
		Name:         params.Name,
		Description:  params.Description,
		DatasetQuery: metabase.BuildDatasetQuery(databaseID, params.SQL),
		Display:      chartType,
		VisualizationSettings: map[string]interface{}{
			"graph.dimensions": []string{},
			"graph.metrics":    []string{},
			"table.pivot":      false,
		},
	}

	createdCard, err := t.metabaseClient.CreateCard(ctx, card)
	if err != nil {
		return "", fmt.Errorf("failed to create card: %w", err)
	}

	t.recorder.RecordMetabaseCard(ctx, companyID, tenantctx.ThreadID(ctx))

	entry := metabase.DashCardEntry{
		CardID:    createdCard.ID,
		ChartType: chartType,
	}
	RecordThreadCard(tenantctx.ThreadID(ctx), entry)

	logrus.WithFields(logrus.Fields{
		"company_id": companyID,
		"card_id":    createdCard.ID,
		"chart_type": chartType,
	}).Info("Created Metabase card")

	out, _ := json.Marshal(map[string]interface{}{
		"card_id":         createdCard.ID,
		"card_name":       createdCard.Name,
		"chart_type":      chartType,
		"dashboard_cards": []metabase.DashCardEntry{entry},
		"note":            "To build a dashboard, call create_dashboard and pass the exact 'dashboard_cards' array above into the 'cards' parameter.",
	})
	return string(out), nil
}
