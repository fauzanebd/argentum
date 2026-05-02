package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/fauzanebd/argentum/internal/metabase"
	"github.com/fauzanebd/argentum/internal/tenantctx"
	"github.com/sirupsen/logrus"
)

// CreateDashboardTool assembles multiple Metabase cards into a single dashboard.
type CreateDashboardTool struct {
	metabaseClient *metabase.Client
	recorder       UsageRecorder
}

func NewCreateDashboardTool(metabaseClient *metabase.Client, recorder UsageRecorder) *CreateDashboardTool {
	if recorder == nil {
		recorder = nopRecorder{}
	}
	return &CreateDashboardTool{metabaseClient: metabaseClient, recorder: recorder}
}

func (t *CreateDashboardTool) Name() string { return "create_dashboard" }
func (t *CreateDashboardTool) Description() string {
	return "Create a Metabase dashboard from one or more card IDs (returned by create_visualization) and return a single shareable public URL. Always call this after creating cards to give the user one unified dashboard link."
}

func (t *CreateDashboardTool) Parameters() map[string]interfaces.ParameterSpec {
	return map[string]interfaces.ParameterSpec{
		"name": {
			Type:        "string",
			Description: "Name for the dashboard",
			Required:    true,
		},
		"description": {
			Type:        "string",
			Description: "Description of the dashboard",
			Required:    false,
		},
		"cards": {
			Type:        "array",
			Description: "Array of card objects. Required fields: card_id (int), chart_type (string: 'scalar','bar','line','pie','table'). Optional layout fields: col (int, 0-17), row (int), size_x (int, 1-18), size_y (int). If layout fields are omitted, smart auto-layout is used: scalars packed 3-per-row at top, charts full-width below.",
			Required:    true,
			Items: &interfaces.ParameterSpec{
				Type:        "object",
				Description: "Card entry with card_id, chart_type, and optional layout (col, row, size_x, size_y)",
			},
		},
	}
}

func (t *CreateDashboardTool) Run(ctx context.Context, input string) (string, error) {
	return t.Execute(ctx, input)
}

func (t *CreateDashboardTool) Execute(ctx context.Context, args string) (string, error) {
	logrus.Debugf("create_dashboard raw args: %s", args)

	// Parse into a flexible map first to handle varied LLM output formats
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(args), &raw); err != nil {
		return "", fmt.Errorf("failed to parse parameters: %w", err)
	}

	// Extract name
	var name string
	if v, ok := raw["name"]; ok {
		json.Unmarshal(v, &name)
	}
	if name == "" {
		return "", fmt.Errorf("name parameter is required")
	}

	// Extract description (optional)
	var description string
	if v, ok := raw["description"]; ok {
		json.Unmarshal(v, &description)
	}

	// Extract cards — support multiple formats the LLM might use
	entries, err := parseCardEntries(raw)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("cards parameter is required and must not be empty. Provide cards as: [{\"card_id\": 1, \"chart_type\": \"bar\"}, ...]")
	}

	dashboard, err := t.metabaseClient.CreateDashboard(ctx, name, description)
	if err != nil {
		return "", fmt.Errorf("failed to create dashboard: %w", err)
	}

	if err := t.metabaseClient.AddCardsToDashboard(ctx, dashboard.ID, entries); err != nil {
		return "", fmt.Errorf("failed to add cards to dashboard: %w", err)
	}

	publicURL, err := t.metabaseClient.GetPublicDashboardURL(ctx, dashboard.ID)
	if err != nil {
		return "", fmt.Errorf("failed to generate public URL: %w", err)
	}

	t.recorder.RecordMetabaseDashboard(ctx, tenantctx.CompanyID(ctx), tenantctx.ThreadID(ctx))

	logrus.WithFields(logrus.Fields{
		"dashboard_id": dashboard.ID,
		"card_count":   len(entries),
	}).Info("Created Metabase dashboard with multiple cards")

	out, _ := json.Marshal(map[string]interface{}{
		"dashboard_id": dashboard.ID,
		"public_url":   publicURL,
		"card_count":   len(entries),
	})
	return string(out), nil
}

// parseCardEntries extracts card entries from the raw JSON, supporting multiple formats:
//   - "cards": [{"card_id": 1, "chart_type": "bar"}, ...]           (preferred)
//   - "card_ids": [{"card_id": 1, "chart_type": "bar"}, ...]        (alternate key)
//   - "cards": [1, 2, 3]  or  "card_ids": [1, 2, 3]                (flat int array)
func parseCardEntries(raw map[string]json.RawMessage) ([]metabase.DashCardEntry, error) {
	// Try both key names
	var cardsRaw json.RawMessage
	if v, ok := raw["cards"]; ok {
		cardsRaw = v
	} else if v, ok := raw["card_ids"]; ok {
		cardsRaw = v
	}
	if cardsRaw == nil {
		return nil, nil
	}

	// Try structured format: [{card_id, chart_type, ...}]
	var structured []struct {
		CardID    int    `json:"card_id"`
		ChartType string `json:"chart_type"`
		Col       int    `json:"col"`
		Row       int    `json:"row"`
		SizeX     int    `json:"size_x"`
		SizeY     int    `json:"size_y"`
	}
	if err := json.Unmarshal(cardsRaw, &structured); err == nil && len(structured) > 0 && structured[0].CardID != 0 {
		entries := make([]metabase.DashCardEntry, len(structured))
		for i, c := range structured {
			entries[i] = metabase.DashCardEntry{
				CardID:    c.CardID,
				ChartType: c.ChartType,
				Col:       c.Col,
				Row:       c.Row,
				SizeX:     c.SizeX,
				SizeY:     c.SizeY,
			}
		}
		return entries, nil
	}

	// Try flat int array: [1, 2, 3]
	var ids []int
	if err := json.Unmarshal(cardsRaw, &ids); err == nil && len(ids) > 0 {
		entries := make([]metabase.DashCardEntry, len(ids))
		for i, id := range ids {
			entries[i] = metabase.DashCardEntry{
				CardID:    id,
				ChartType: "table", // safe default when type is unknown
			}
		}
		return entries, nil
	}

	return nil, nil
}
