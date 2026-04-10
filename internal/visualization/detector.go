package visualization

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fauzanebd/argentum/internal/llm"
)

// Detector determines the best visualization type for data
type Detector struct {
	llmProvider llm.Provider
}

// NewDetector creates a new visualization detector
func NewDetector(llmProvider llm.Provider) *Detector {
	return &Detector{llmProvider: llmProvider}
}

// VisualizationRecommendation represents a viz recommendation
type VisualizationRecommendation struct {
	Type             string                 `json:"type"`
	Display          string                 `json:"display"`
	Title            string                 `json:"title"`
	Description      string                 `json:"description"`
	XAxis            string                 `json:"x_axis,omitempty"`
	YAxis            string                 `json:"y_axis,omitempty"`
	Settings         map[string]interface{} `json:"settings,omitempty"`
	AlternativeTypes []string               `json:"alternative_types,omitempty"`
	Confidence       float64                `json:"confidence"`
}

// Detect determines the best visualization for query results
func (d *Detector) Detect(ctx context.Context, query string, columns []string, sampleData []map[string]interface{}) (*VisualizationRecommendation, error) {
	// Build data profile
	dataProfile := d.profileData(columns, sampleData)

	prompt := fmt.Sprintf(`Analyze this data and recommend the best visualization type.

Query: %s

Columns: %v

Data Profile:
%s

Recommend the best visualization type from: line, bar, pie, scatter, area, combo, table, scalar, map, funnel

Respond in JSON format:
{
  "type": "recommended_type",
  "display": "metabase_display_type",
  "title": "Chart title",
  "description": "Why this visualization",
  "x_axis": "column for x-axis",
  "y_axis": "column for y-axis",
  "settings": {"additional_settings": "values"},
  "alternative_types": ["type1", "type2"],
  "confidence": 0.95
}`, query, columns, dataProfile)

	response, err := d.llmProvider.Generate(ctx, prompt,
		llm.WithTemperature(0.1),
		llm.WithMaxTokens(500),
	)
	if err != nil {
		// Fallback to heuristic detection
		return d.heuristicDetect(query, columns, sampleData), nil
	}

	var recommendation VisualizationRecommendation
	content := response.Content
	// Clean up markdown
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	if err := json.Unmarshal([]byte(content), &recommendation); err != nil {
		return d.heuristicDetect(query, columns, sampleData), nil
	}

	return &recommendation, nil
}

// profileData creates a data profile for analysis
func (d *Detector) profileData(columns []string, sampleData []map[string]interface{}) string {
	var profile strings.Builder

	for _, col := range columns {
		if len(sampleData) == 0 {
			profile.WriteString(fmt.Sprintf("- %s: unknown type\n", col))
			continue
		}

		// Detect type from first non-null value
		for _, row := range sampleData {
			if val, ok := row[col]; ok && val != nil {
				switch val.(type) {
				case int, int32, int64, float32, float64:
					profile.WriteString(fmt.Sprintf("- %s: numeric\n", col))
				default:
					strVal := fmt.Sprintf("%v", val)
					if looksLikeDate(strVal) {
						profile.WriteString(fmt.Sprintf("- %s: datetime\n", col))
					} else {
						profile.WriteString(fmt.Sprintf("- %s: categorical\n", col))
					}
				}
				break
			}
		}
	}

	return profile.String()
}

// heuristicDetect uses rules to detect visualization type
func (d *Detector) heuristicDetect(query string, columns []string, sampleData []map[string]interface{}) *VisualizationRecommendation {
	queryLower := strings.ToLower(query)

	// Count data types
	hasDate := false
	hasNumeric := false
	hasCategorical := false

	for _, col := range columns {
		if len(sampleData) > 0 {
			if val, ok := sampleData[0][col]; ok && val != nil {
				switch val.(type) {
				case int, int32, int64, float32, float64:
					hasNumeric = true
				default:
					strVal := fmt.Sprintf("%v", val)
					if looksLikeDate(strVal) {
						hasDate = true
					} else {
						hasCategorical = true
					}
				}
			}
		}
	}

	// Detect based on query and data
	rec := &VisualizationRecommendation{
		Title:    d.generateTitle(query),
		Settings: make(map[string]interface{}),
	}

	// Time series detection
	if hasDate && (strings.Contains(queryLower, "trend") || strings.Contains(queryLower, "over time") ||
		strings.Contains(queryLower, "monthly") || strings.Contains(queryLower, "daily")) {
		rec.Type = "line"
		rec.Display = "line"
		rec.Description = "Line chart best shows trends over time"
		rec.Confidence = 0.95
		rec.AlternativeTypes = []string{"area", "bar"}
		return rec
	}

	// Part-to-whole detection
	if strings.Contains(queryLower, "percentage") || strings.Contains(queryLower, "share") ||
		strings.Contains(queryLower, "proportion") {
		rec.Type = "pie"
		rec.Display = "pie"
		rec.Description = "Pie chart shows proportions effectively"
		rec.Confidence = 0.90
		rec.AlternativeTypes = []string{"donut", "bar"}
		return rec
	}

	// Comparison detection
	if hasCategorical && hasNumeric {
		if strings.Contains(queryLower, "compare") || strings.Contains(queryLower, "top") ||
			strings.Contains(queryLower, "ranking") {
			rec.Type = "bar"
			rec.Display = "bar"
			rec.Description = "Bar chart enables easy comparison between categories"
			rec.Confidence = 0.90
			rec.AlternativeTypes = []string{"row", "pie"}
			return rec
		}
	}

	// Single value detection
	if len(columns) == 1 || len(columns) == 2 && !hasCategorical {
		rec.Type = "scalar"
		rec.Display = "scalar"
		rec.Description = "Scalar display for single metric"
		rec.Confidence = 0.95
		rec.AlternativeTypes = []string{"table"}
		return rec
	}

	// Default to table for complex data
	rec.Type = "table"
	rec.Display = "table"
	rec.Description = "Table view for detailed data exploration"
	rec.Confidence = 0.80
	rec.AlternativeTypes = []string{"bar", "line"}

	return rec
}

// generateTitle creates a chart title from query
func (d *Detector) generateTitle(query string) string {
	// Remove common prefixes
	title := query
	prefixes := []string{"show me ", "display ", "what is ", "how many ", "what are "}
	for _, prefix := range prefixes {
		if strings.HasPrefix(strings.ToLower(title), prefix) {
			title = title[len(prefix):]
		}
	}

	// Capitalize first letter
	if len(title) > 0 {
		title = strings.ToUpper(title[:1]) + title[1:]
	}

	return title
}

// looksLikeDate checks if string looks like a date
func looksLikeDate(s string) bool {
	dateIndicators := []string{"-", "/", ":", "date", "time", "202", "201"}
	sLower := strings.ToLower(s)

	for _, indicator := range dateIndicators {
		if strings.Contains(s, indicator) || strings.Contains(sLower, indicator) {
			return true
		}
	}
	return false
}

// ChartType represents supported chart types
var ChartTypes = map[string]ChartTypeInfo{
	"line": {
		Name:        "Line",
		Description: "Best for trends over time",
		BestFor:     []string{"time series", "trends", "progression"},
	},
	"bar": {
		Name:        "Bar",
		Description: "Best for comparing categories",
		BestFor:     []string{"comparisons", "rankings", "categories"},
	},
	"pie": {
		Name:        "Pie",
		Description: "Best for showing proportions",
		BestFor:     []string{"percentages", "shares", "composition"},
	},
	"scatter": {
		Name:        "Scatter",
		Description: "Best for correlations",
		BestFor:     []string{"relationships", "correlations", "distributions"},
	},
	"table": {
		Name:        "Table",
		Description: "Best for detailed data",
		BestFor:     []string{"details", "multiple metrics", "exact values"},
	},
	"scalar": {
		Name:        "Scalar",
		Description: "Best for single metrics",
		BestFor:     []string{"single value", "KPI", "metric"},
	},
	"area": {
		Name:        "Area",
		Description: "Best for cumulative trends",
		BestFor:     []string{"volume over time", "cumulative data"},
	},
	"combo": {
		Name:        "Combo",
		Description: "Best for mixed data types",
		BestFor:     []string{"multiple metrics", "mixed scales"},
	},
}

// ChartTypeInfo holds information about a chart type
type ChartTypeInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	BestFor     []string `json:"best_for"`
}

// MultiChartDashboard creates a dashboard with multiple visualizations
type MultiChartDashboard struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Charts      []DashboardChart  `json:"charts"`
	Filters     []DashboardFilter `json:"filters,omitempty"`
}

// DashboardChart represents a chart in a dashboard
type DashboardChart struct {
	ID       int                    `json:"id"`
	Title    string                 `json:"title"`
	Type     string                 `json:"type"`
	SQL      string                 `json:"sql"`
	Position ChartPosition          `json:"position"`
	Settings map[string]interface{} `json:"settings"`
}

// ChartPosition defines chart layout position
type ChartPosition struct {
	Row   int `json:"row"`
	Col   int `json:"col"`
	SizeX int `json:"size_x"`
	SizeY int `json:"size_y"`
}

// DashboardFilter represents a dashboard filter
type DashboardFilter struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Field   string   `json:"field"`
	Options []string `json:"options,omitempty"`
	Default string   `json:"default,omitempty"`
}

// BuildMultiChartDashboard creates a dashboard with multiple charts
func BuildMultiChartDashboard(query string, recommendations []*VisualizationRecommendation) *MultiChartDashboard {
	dashboard := &MultiChartDashboard{
		Name:        d.generateTitle(query) + " Dashboard",
		Description: "Multi-view analysis",
		Charts:      make([]DashboardChart, 0),
	}

	col := 0
	row := 0

	for i, rec := range recommendations {
		if i > 0 && i%2 == 0 {
			row += 4
			col = 0
		}

		chart := DashboardChart{
			ID:    i + 1,
			Title: rec.Title,
			Type:  rec.Display,
			Position: ChartPosition{
				Row:   row,
				Col:   col * 6,
				SizeX: 6,
				SizeY: 4,
			},
			Settings: rec.Settings,
		}

		dashboard.Charts = append(dashboard.Charts, chart)
		col++
	}

	return dashboard
}

// var d *Detector - helper for BuildMultiChartDashboard
var d *Detector
