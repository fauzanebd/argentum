package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/metabase"
	"github.com/fauzanebd/argentum/internal/tenantctx"
	"github.com/sirupsen/logrus"
)

// DashboardSaver persists a created dashboard to the control DB.
type DashboardSaver interface {
	Save(ctx context.Context, d *domain.SavedDashboard) error
}

// CreateDashboardTool assembles multiple Metabase cards into a single dashboard.
type CreateDashboardTool struct {
	metabaseClient *metabase.Client
	recorder       UsageRecorder
	dbSaver        DashboardSaver
}

func NewCreateDashboardTool(metabaseClient *metabase.Client, recorder UsageRecorder, dbSaver DashboardSaver) *CreateDashboardTool {
	if recorder == nil {
		recorder = nopRecorder{}
	}
	return &CreateDashboardTool{metabaseClient: metabaseClient, recorder: recorder, dbSaver: dbSaver}
}

func (t *CreateDashboardTool) Name() string { return "create_dashboard" }
func (t *CreateDashboardTool) Description() string {
	return "Create a Metabase dashboard from one or more card IDs (returned by create_visualization) and return a single shareable public URL. Always call this after creating cards to give the user one unified dashboard link. Pass cards either as 'cards' (array of objects with card_id and chart_type) or as 'card_ids' (simple array of integers). If you omit both, cards created earlier in this conversation are used automatically."
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
			Description: "Array of card objects. Fields: card_id (int), chart_type (string: 'scalar','bar','line','pie','table'). Optional layout: col, row, size_x, size_y. Prefer this when you know the chart types.",
			Required:    false,
			Items: &interfaces.ParameterSpec{
				Type:        "object",
				Description: "Card entry with card_id, chart_type, and optional layout (col, row, size_x, size_y)",
			},
		},
		"card_ids": {
			Type:        "array",
			Description: "Simple array of card IDs (integers). Use this when you only have the card IDs from create_visualization. Example: [123, 456].",
			Required:    false,
			Items: &interfaces.ParameterSpec{
				Type:        "integer",
				Description: "A card ID returned by create_visualization",
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

	// Extract name. A model that sends `{"name": {"text": "Sales"}}` used to
	// land here as an empty string and get told the parameter was missing,
	// which is advice it cannot act on — it did supply one, in the wrong
	// shape. Say what was wrong instead.
	var name string
	if v, ok := raw["name"]; ok {
		if err := json.Unmarshal(v, &name); err != nil {
			return "", fmt.Errorf("name must be a string, got %s", v)
		}
	}
	if name == "" {
		return "", fmt.Errorf("name parameter is required")
	}

	// Extract description (optional)
	var description string
	if v, ok := raw["description"]; ok {
		if err := json.Unmarshal(v, &description); err != nil {
			return "", fmt.Errorf("description must be a string, got %s", v)
		}
	}

	// Extract cards — support multiple formats the LLM might use
	entries, err := parseCardEntries(raw)
	if err != nil {
		return "", err
	}

	// Fallback: auto-resolve cards created earlier in this thread
	if len(entries) == 0 {
		threadID := tenantctx.ThreadID(ctx)
		entries = GetThreadCards(threadID)
		if len(entries) == 0 {
			return "", fmt.Errorf("cards parameter is required and must not be empty. Provide cards as: [{\"card_id\": 1, \"chart_type\": \"bar\"}, ...] or card_ids as: [1, 2, 3]")
		}
		logrus.WithField("thread_id", threadID).WithField("card_count", len(entries)).Debug("create_dashboard auto-resolved cards from thread state")
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
	ClearThreadCards(tenantctx.ThreadID(ctx))

	// Persist to control DB so the user can revisit later.
	if t.dbSaver != nil {
		_ = t.dbSaver.Save(ctx, &domain.SavedDashboard{
			CompanyID:           tenantctx.CompanyID(ctx),
			ThreadID:            tenantctx.ThreadID(ctx),
			MetabaseDashboardID: dashboard.ID,
			Name:                name,
			PublicURL:           publicURL,
		})
	}

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
