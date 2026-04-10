package metabase

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

// Client handles Metabase API interactions
type Client struct {
	baseURL       string
	publicBaseURL string
	username      string
	password      string
	sessionToken  string
	httpClient    *http.Client
}

// NewClient creates a new Metabase client
func NewClient(baseURL, publicBaseURL, username, password string) *Client {
	return &Client{
		baseURL:       baseURL,
		publicBaseURL: publicBaseURL,
		username:      username,
		password:      password,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Authenticate logs in to Metabase and obtains a session token
func (c *Client) Authenticate(ctx context.Context) error {
	loginURL := fmt.Sprintf("%s/api/session", c.baseURL)

	payload := map[string]string{
		"username": c.username,
		"password": c.password,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal login payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", loginURL, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("failed to create login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("login failed (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to decode login response: %w", err)
	}

	c.sessionToken = result.ID
	logrus.Info("Successfully authenticated with Metabase")
	return nil
}

// Card represents a Metabase question/card
type Card struct {
	ID                    int                    `json:"id,omitempty"`
	Name                  string                 `json:"name"`
	Description           string                 `json:"description,omitempty"`
	DatasetQuery          map[string]interface{} `json:"dataset_query"`
	Display               string                 `json:"display"`
	VisualizationSettings map[string]interface{} `json:"visualization_settings,omitempty"`
}

// Dashboard represents a Metabase dashboard
type Dashboard struct {
	ID          int    `json:"id,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Database represents a Metabase database connection
type Database struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Engine string `json:"engine"`
}

// CreateCard creates a new question/card in Metabase
func (c *Client) CreateCard(ctx context.Context, card *Card) (*Card, error) {
	if c.sessionToken == "" {
		if err := c.Authenticate(ctx); err != nil {
			return nil, err
		}
	}

	url := fmt.Sprintf("%s/api/card", c.baseURL)

	jsonPayload, err := json.Marshal(card)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal card: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Metabase-Session", c.sessionToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to create card (status %d): %s", resp.StatusCode, string(body))
	}

	var createdCard Card
	if err := json.NewDecoder(resp.Body).Decode(&createdCard); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	logrus.Infof("Created Metabase card: %s (ID: %d)", createdCard.Name, createdCard.ID)
	return &createdCard, nil
}

// CreateDashboard creates a new dashboard in Metabase
func (c *Client) CreateDashboard(ctx context.Context, name, description string) (*Dashboard, error) {
	if c.sessionToken == "" {
		if err := c.Authenticate(ctx); err != nil {
			return nil, err
		}
	}

	url := fmt.Sprintf("%s/api/dashboard", c.baseURL)

	payload := map[string]interface{}{
		"name":        name,
		"description": description,
		"parameters":  []interface{}{},
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal dashboard: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Metabase-Session", c.sessionToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to create dashboard (status %d): %s", resp.StatusCode, string(body))
	}

	var dashboard Dashboard
	if err := json.NewDecoder(resp.Body).Decode(&dashboard); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	logrus.Infof("Created Metabase dashboard: %s (ID: %d)", dashboard.Name, dashboard.ID)
	return &dashboard, nil
}

// DashCardEntry describes a card to place on a dashboard.
// If Col/Row/SizeX/SizeY are all zero, auto-layout is applied based on ChartType.
type DashCardEntry struct {
	CardID    int
	ChartType string
	Col       int
	Row       int
	SizeX     int
	SizeY     int
}

// HasCustomLayout returns true if explicit layout positions were provided.
func (e DashCardEntry) HasCustomLayout() bool {
	return e.SizeX > 0 || e.SizeY > 0
}

// AddCardsToDashboard adds multiple cards to a dashboard.
// Cards with custom layout (non-zero SizeX/SizeY) use the provided positions.
// Cards without custom layout get smart auto-placement:
//   - Scalar/KPI: 3 per row (6 cols wide, 4 rows tall)
//   - Charts: full width (18 cols, 8 rows tall)
func (c *Client) AddCardsToDashboard(ctx context.Context, dashboardID int, cards []DashCardEntry) error {
	if c.sessionToken == "" {
		if err := c.Authenticate(ctx); err != nil {
			return err
		}
	}

	dashcards := make([]map[string]interface{}, 0, len(cards))
	tempID := -1

	// Check if any card has custom layout — if so, use custom for all that have it
	// and auto-layout the rest starting after the last custom row
	var customCards, autoScalars, autoCharts []DashCardEntry
	for _, card := range cards {
		if card.HasCustomLayout() {
			customCards = append(customCards, card)
		} else if card.ChartType == "scalar" {
			autoScalars = append(autoScalars, card)
		} else {
			autoCharts = append(autoCharts, card)
		}
	}

	// Place custom-layout cards as-is
	maxRow := 0
	for _, card := range customCards {
		dashcards = append(dashcards, map[string]interface{}{
			"id":      tempID,
			"card_id": card.CardID,
			"row":     card.Row,
			"col":     card.Col,
			"size_x":  card.SizeX,
			"size_y":  card.SizeY,
		})
		tempID--
		if endRow := card.Row + card.SizeY; endRow > maxRow {
			maxRow = endRow
		}
	}

	currentRow := maxRow

	// Auto-layout scalars: 3 per row, each 6 cols wide, 4 rows tall
	for i, s := range autoScalars {
		col := (i % 3) * 6
		row := currentRow + (i/3)*4
		dashcards = append(dashcards, map[string]interface{}{
			"id":      tempID,
			"card_id": s.CardID,
			"row":     row,
			"col":     col,
			"size_x":  6,
			"size_y":  4,
		})
		tempID--
	}
	if len(autoScalars) > 0 {
		currentRow += ((len(autoScalars)-1)/3 + 1) * 4
	}

	// Auto-layout charts: full width, stacked
	for _, ch := range autoCharts {
		dashcards = append(dashcards, map[string]interface{}{
			"id":      tempID,
			"card_id": ch.CardID,
			"row":     currentRow,
			"col":     0,
			"size_x":  18,
			"size_y":  8,
		})
		tempID--
		currentRow += 8
	}

	url := fmt.Sprintf("%s/api/dashboard/%d", c.baseURL, dashboardID)
	payload := map[string]interface{}{
		"dashcards": dashcards,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal dashcards: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Metabase-Session", c.sessionToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to add cards to dashboard (status %d): %s", resp.StatusCode, string(body))
	}

	logrus.Infof("Added %d cards to dashboard %d", len(cards), dashboardID)
	return nil
}

// GetPublicDashboardURL generates a public/shareable URL for a dashboard
func (c *Client) GetPublicDashboardURL(ctx context.Context, dashboardID int) (string, error) {
	if c.sessionToken == "" {
		if err := c.Authenticate(ctx); err != nil {
			return "", err
		}
	}

	// Enable public sharing for the dashboard
	url := fmt.Sprintf("%s/api/dashboard/%d/public_link", c.baseURL, dashboardID)

	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("X-Metabase-Session", c.sessionToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to create public link (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		UUID string `json:"uuid"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	publicURL := fmt.Sprintf("%s/metabase/public/dashboard/%s", c.publicBaseURL, result.UUID)
	logrus.Infof("Generated public dashboard URL: %s", publicURL)
	return publicURL, nil
}

// GetDatabases lists all databases configured in Metabase
func (c *Client) GetDatabases(ctx context.Context) ([]Database, error) {
	if c.sessionToken == "" {
		if err := c.Authenticate(ctx); err != nil {
			return nil, err
		}
	}

	url := fmt.Sprintf("%s/api/database", c.baseURL)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("X-Metabase-Session", c.sessionToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get databases (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []Database `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result.Data, nil
}

// BuildDatasetQuery constructs a Metabase dataset_query from SQL
func BuildDatasetQuery(databaseID int, sql string) map[string]interface{} {
	return map[string]interface{}{
		"type": "native",
		"native": map[string]interface{}{
			"query":         sql,
			"template-tags": map[string]interface{}{},
		},
		"database": databaseID,
	}
}

// DetectVisualizationType suggests a visualization type based on query results
func DetectVisualizationType(columnCount, rowCount int, columnTypes []string) string {
	if rowCount == 1 && columnCount == 1 {
		return "scalar" // Single value
	}
	if rowCount == 1 && columnCount > 1 {
		return "bar" // Single row comparison
	}
	if columnCount == 2 {
		// Check if second column is numeric
		if len(columnTypes) > 1 && (columnTypes[1] == "number" || columnTypes[1] == "integer") {
			if rowCount <= 5 {
				return "pie" // Small categorical data
			}
			return "bar" // Larger categorical data
		}
	}
	if columnCount >= 2 && rowCount > 1 {
		return "table" // Default to table for complex queries
	}
	return "table"
}
