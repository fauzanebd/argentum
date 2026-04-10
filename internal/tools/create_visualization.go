package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/fauzanebd/argentum/internal/database"
	"github.com/fauzanebd/argentum/internal/metabase"
	"github.com/sirupsen/logrus"
)

// CreateVisualizationTool creates Metabase dashboards from SQL queries.
type CreateVisualizationTool struct {
	db             *database.DB
	metabaseClient *metabase.Client
}

func NewCreateVisualizationTool(db *database.DB, metabaseClient *metabase.Client) *CreateVisualizationTool {
	return &CreateVisualizationTool{db: db, metabaseClient: metabaseClient}
}

func (t *CreateVisualizationTool) Name() string { return "create_visualization" }
func (t *CreateVisualizationTool) Description() string {
	return "Create a visualization card (question) in Metabase from a SQL query. Returns a card_id and chart_type. Use create_dashboard afterwards to combine multiple cards into a single shareable dashboard."
}

func (t *CreateVisualizationTool) Parameters() map[string]interfaces.ParameterSpec {
	return map[string]interfaces.ParameterSpec{
		"sql": {
			Type:        "string",
			Description: "The SQL query for the visualization",
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

	result, err := t.db.ExecuteReadOnly(ctx, params.SQL)
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

	databases, err := t.metabaseClient.GetDatabases(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get Metabase databases: %w", err)
	}

	var databaseID int
	for _, db := range databases {
		if db.Engine == "postgres" {
			databaseID = db.ID
			break
		}
	}
	if databaseID == 0 {
		return "", fmt.Errorf("no PostgreSQL database found in Metabase")
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

	logrus.WithFields(logrus.Fields{
		"card_id":    createdCard.ID,
		"chart_type": chartType,
	}).Info("Created Metabase card")

	out, _ := json.Marshal(map[string]interface{}{
		"card_id":    createdCard.ID,
		"card_name":  createdCard.Name,
		"chart_type": chartType,
	})
	return string(out), nil
}
