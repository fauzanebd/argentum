package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/fauzanebd/argentum/internal/metadata"
)

// GetSchemaTool retrieves database schema information for the LLM.
type GetSchemaTool struct {
	schemaManager *metadata.SchemaManager
}

func NewGetSchemaTool(schemaManager *metadata.SchemaManager) *GetSchemaTool {
	return &GetSchemaTool{schemaManager: schemaManager}
}

func (t *GetSchemaTool) Name() string        { return "get_schema" }
func (t *GetSchemaTool) Description() string {
	return "Get database schema information including tables, columns, and relationships. Use this when you need to understand the database structure before writing SQL."
}

func (t *GetSchemaTool) Parameters() map[string]interfaces.ParameterSpec {
	return map[string]interfaces.ParameterSpec{
		"business_id": {
			Type:        "string",
			Description: "The business/tenant ID (use 'default' if unsure)",
			Required:    true,
			Default:     "default",
		},
	}
}

func (t *GetSchemaTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

func (t *GetSchemaTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		BusinessID string `json:"business_id"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		params.BusinessID = "default"
	}
	if params.BusinessID == "" {
		params.BusinessID = "default"
	}

	schema, err := t.schemaManager.GetSchema(ctx, params.BusinessID, false)
	if err != nil {
		return "", fmt.Errorf("failed to get schema: %w", err)
	}

	result := map[string]interface{}{
		"schema":        t.schemaManager.ToPromptFormat(schema),
		"tables":        len(schema.Tables),
		"relationships": len(schema.Relationships),
	}

	out, _ := json.Marshal(result)
	return string(out), nil
}
