package tools

import (
	"context"
	"fmt"

	"github.com/fauzanebd/argentum/internal/database"
	"github.com/fauzanebd/argentum/internal/metabase"
	"github.com/fauzanebd/argentum/internal/metadata"
	"github.com/sirupsen/logrus"
)

// Registry manages available tools for the agent
type Registry struct {
	schemaManager  *metadata.SchemaManager
	db             *database.DB
	metabaseClient *metabase.Client
}

// NewRegistry creates a new tool registry
func NewRegistry(schemaManager *metadata.SchemaManager, db *database.DB, metabaseClient *metabase.Client) *Registry {
	return &Registry{
		schemaManager:  schemaManager,
		db:             db,
		metabaseClient: metabaseClient,
	}
}

// Tool represents an available tool
type Tool struct {
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Parameters  map[string]Parameter `json:"parameters"`
}

// Parameter represents a tool parameter
type Parameter struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

// GetAvailableTools returns a list of available tools
func (r *Registry) GetAvailableTools() []Tool {
	return []Tool{
		{
			Name:        "get_schema",
			Description: "Get database schema information including tables, columns, and relationships",
			Parameters: map[string]Parameter{
				"business_id": {
					Type:        "string",
					Description: "The business/tenant ID",
					Required:    true,
				},
			},
		},
		{
			Name:        "run_sql",
			Description: "Execute a read-only SQL query and return results",
			Parameters: map[string]Parameter{
				"sql": {
					Type:        "string",
					Description: "The SQL query to execute",
					Required:    true,
				},
			},
		},
		{
			Name:        "create_visualization",
			Description: "Create a visualization in Metabase and return a shareable URL",
			Parameters: map[string]Parameter{
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
					Description: "Type of chart (bar, line, pie, table, scalar)",
					Required:    false,
				},
			},
		},
	}
}

// ExecuteTool executes a tool by name with given parameters
func (r *Registry) ExecuteTool(ctx context.Context, toolName string, params map[string]interface{}) (interface{}, error) {
	switch toolName {
	case "get_schema":
		return r.executeGetSchema(ctx, params)
	case "run_sql":
		return r.executeRunSQL(ctx, params)
	case "create_visualization":
		return r.executeCreateVisualization(ctx, params)
	default:
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
}

// executeGetSchema handles the get_schema tool
func (r *Registry) executeGetSchema(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	businessID, ok := params["business_id"].(string)
	if !ok || businessID == "" {
		return nil, fmt.Errorf("business_id parameter is required")
	}

	schema, err := r.schemaManager.GetSchema(ctx, businessID, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get schema: %w", err)
	}

	// Convert to simple format for LLM
	return map[string]interface{}{
		"schema":        r.schemaManager.ToPromptFormat(schema),
		"tables":        len(schema.Tables),
		"relationships": len(schema.Relationships),
	}, nil
}

// executeRunSQL handles the run_sql tool
func (r *Registry) executeRunSQL(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	sql, ok := params["sql"].(string)
	if !ok || sql == "" {
		return nil, fmt.Errorf("sql parameter is required")
	}

	logrus.Infof("Executing SQL: %s", sql)

	result, err := r.db.ExecuteReadOnly(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("query execution failed: %w", err)
	}

	return map[string]interface{}{
		"columns":   result.Columns,
		"rows":      result.Rows,
		"row_count": result.Count,
	}, nil
}

// executeCreateVisualization handles the create_visualization tool
func (r *Registry) executeCreateVisualization(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	sql, ok := params["sql"].(string)
	if !ok || sql == "" {
		return nil, fmt.Errorf("sql parameter is required")
	}

	name, ok := params["name"].(string)
	if !ok || name == "" {
		return nil, fmt.Errorf("name parameter is required")
	}

	description, _ := params["description"].(string)
	chartType, _ := params["chart_type"].(string)
	if chartType == "" {
		chartType = "table"
	}

	// First, run the SQL to get column types for visualization detection
	result, err := r.db.ExecuteReadOnly(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}

	// Detect visualization type if not specified
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

	if chartType == "auto" || chartType == "" {
		chartType = metabase.DetectVisualizationType(len(result.Columns), result.Count, columnTypes)
	}

	// Get database ID from Metabase
	databases, err := r.metabaseClient.GetDatabases(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get databases: %w", err)
	}

	var databaseID int
	for _, db := range databases {
		if db.Engine == "postgres" {
			databaseID = db.ID
			break
		}
	}
	if databaseID == 0 {
		return nil, fmt.Errorf("no PostgreSQL database found in Metabase")
	}

	// Create the card/question
	card := &metabase.Card{
		Name:         name,
		Description:  description,
		DatasetQuery: metabase.BuildDatasetQuery(databaseID, sql),
		Display:      chartType,
		VisualizationSettings: map[string]interface{}{
			"graph.dimensions": []string{},
			"graph.metrics":    []string{},
			"table.pivot":      false,
		},
	}

	createdCard, err := r.metabaseClient.CreateCard(ctx, card)
	if err != nil {
		return nil, fmt.Errorf("failed to create card: %w", err)
	}

	// Create a dashboard
	dashboard, err := r.metabaseClient.CreateDashboard(ctx, name+" Dashboard", description)
	if err != nil {
		return nil, fmt.Errorf("failed to create dashboard: %w", err)
	}

	// Add card to dashboard
	if err := r.metabaseClient.AddCardToDashboard(ctx, dashboard.ID, createdCard.ID); err != nil {
		return nil, fmt.Errorf("failed to add card to dashboard: %w", err)
	}

	// Get public URL
	publicURL, err := r.metabaseClient.GetPublicDashboardURL(ctx, dashboard.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to generate public URL: %w", err)
	}

	return map[string]interface{}{
		"card_id":      createdCard.ID,
		"dashboard_id": dashboard.ID,
		"public_url":   publicURL,
		"chart_type":   chartType,
	}, nil
}

// BuildToolDescription builds a description of available tools for the LLM
func (r *Registry) BuildToolDescription() string {
	tools := r.GetAvailableTools()
	description := "Available Tools:\n\n"

	for _, tool := range tools {
		description += fmt.Sprintf("Tool: %s\n", tool.Name)
		description += fmt.Sprintf("Description: %s\n", tool.Description)
		description += "Parameters:\n"
		for paramName, param := range tool.Parameters {
			req := ""
			if param.Required {
				req = " (required)"
			}
			description += fmt.Sprintf("  - %s (%s)%s: %s\n", paramName, param.Type, req, param.Description)
		}
		description += "\n"
	}

	return description
}
