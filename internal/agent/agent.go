package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fauzanebd/argentum/internal/database"
	"github.com/fauzanebd/argentum/internal/llm"
	"github.com/fauzanebd/argentum/internal/metabase"
	"github.com/fauzanebd/argentum/internal/metadata"
	"github.com/fauzanebd/argentum/internal/tools"
	"github.com/fauzanebd/argentum/pkg/models"
	"github.com/sirupsen/logrus"
)

// Agent orchestrates the analytics query workflow using the Tool Registry
type Agent struct {
	llmProvider    llm.Provider
	db             *database.DB
	toolRegistry   *tools.Registry
	schemaManager  *metadata.SchemaManager
	metabaseClient *metabase.Client
	contextMgr     *ContextManager
}

// NewAgent creates a new agent instance with Tool Registry
func NewAgent(llmProvider llm.Provider, db *database.DB, toolRegistry *tools.Registry,
	schemaManager *metadata.SchemaManager, metabaseClient *metabase.Client) *Agent {
	return &Agent{
		llmProvider:    llmProvider,
		db:             db,
		toolRegistry:   toolRegistry,
		schemaManager:  schemaManager,
		metabaseClient: metabaseClient,
		contextMgr:     NewContextManager(3),
	}
}

// ProcessQuery processes a natural language query and returns a response
func (a *Agent) ProcessQuery(ctx context.Context, sessionID string, query string) (*models.AgentResponse, error) {
	logrus.Infof("Processing query for session %s: %s", sessionID, query)

	// Load conversation context
	conversationCtx := a.contextMgr.GetContext(sessionID)

	// Build the prompt with tool descriptions (schema is auto-injected)
	prompt := a.buildAgentPrompt(ctx, query, conversationCtx)

	// Get LLM to decide which tool to use
	response, err := a.llmProvider.Generate(ctx, prompt,
		llm.WithTemperature(0.2),
		llm.WithMaxTokens(1500),
	)
	if err != nil {
		return nil, fmt.Errorf("LLM generation failed: %w", err)
	}

	// Parse the tool call from LLM response
	toolCall, err := a.parseToolCall(response.Content)
	if err != nil {
		return nil, fmt.Errorf("failed to parse tool call: %w", err)
	}

	logrus.Infof("Executing tool: %s", toolCall.Tool)

	// Execute the tool
	toolResult, err := a.toolRegistry.ExecuteTool(ctx, toolCall.Tool, toolCall.Parameters)
	if err != nil {
		return nil, fmt.Errorf("tool execution failed: %w", err)
	}

	// Generate insight from results
	insight, err := a.generateInsight(ctx, query, toolCall, toolResult)
	if err != nil {
		return nil, fmt.Errorf("failed to generate insight: %w", err)
	}

	// Update conversation context
	conversationCtx.AddTurn(query, insight)
	a.contextMgr.UpdateContext(sessionID, conversationCtx)

	// Build response
	agentResponse := &models.AgentResponse{
		MessageID:         sessionID,
		Query:             query,
		Insight:           insight,
		FollowUpQuestions: a.generateFollowUpQuestions(query, toolResult),
	}

	// If a dashboard was created, extract the URL
	if resultMap, ok := toolResult.(map[string]interface{}); ok {
		if url, ok := resultMap["public_url"].(string); ok {
			agentResponse.DashboardURL = url
		}
	}

	return agentResponse, nil
}

// ToolCall represents a tool invocation from the LLM
type ToolCall struct {
	Tool       string                 `json:"tool"`
	Parameters map[string]interface{} `json:"parameters"`
}

// buildAgentPrompt builds the complete prompt for the agent
func (a *Agent) buildAgentPrompt(ctx context.Context, query string, ctxInfo *ConversationContext) string {
	// Build context description
	var contextDesc string
	if ctxInfo.Summary != "" {
		contextDesc = fmt.Sprintf("\nPrevious Conversation Summary:\n%s\n", ctxInfo.Summary)
	}

	if len(ctxInfo.RecentTurns) > 0 {
		contextDesc += "\nRecent exchanges:\n"
		for _, turn := range ctxInfo.RecentTurns {
			contextDesc += fmt.Sprintf("User: %s\nAssistant: %s\n", turn.Query, turn.Response)
		}
	}

	// Get tool descriptions
	toolDescription := a.toolRegistry.BuildToolDescription()

	// AUTO-INJECT: Fetch and include actual database schema in the prompt
	schemaMetadata, err := a.schemaManager.GetSchema(ctx, "default", false)
	var schemaInfo string
	if err != nil {
		logrus.Warnf("Failed to get schema for prompt: %v", err)
		schemaInfo = "\nDatabase Schema: Unable to retrieve schema information.\n"
	} else {
		schemaInfo = fmt.Sprintf("\nDatabase Schema:\n%s\n", a.schemaManager.ToPromptFormat(schemaMetadata))
	}

	prompt := fmt.Sprintf(`You are a data analyst helping business owners understand their metrics.

%s

%s

%s

Current Question: %s

Your task is to help answer this question by using the appropriate tool.

Response Format:
Return a JSON object with the following structure:
{
  "tool": "tool_name",
  "parameters": {
    "param1": "value1",
    "param2": "value2"
  }
}

CRITICAL GUIDELINES:
1. The schema above shows ONLY the actual tables and columns in this database
2. Use ONLY the table and column names listed in the schema
3. DO NOT hallucinate or invent table names (like 'sales', 'orders', 'customers') that don't exist in the schema
4. Use "get_schema" only if the schema above is insufficient for your query
5. Use "run_sql" to answer data questions with valid PostgreSQL queries
6. Use "create_visualization" for charts and dashboards
7. Always use the business_id "default" for the get_schema tool
8. Generate valid PostgreSQL SQL using ONLY existing tables and columns
9. Include appropriate aggregations (SUM, COUNT, AVG) and filters (date ranges)
10. Limit results to reasonable numbers when appropriate

Respond ONLY with the JSON object, no additional text.`, toolDescription, schemaInfo, contextDesc, query)

	return prompt
}

// parseToolCall parses the JSON tool call from LLM response
func (a *Agent) parseToolCall(content string) (*ToolCall, error) {
	// Clean up the response - remove markdown code blocks if present
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var toolCall ToolCall
	if err := json.Unmarshal([]byte(content), &toolCall); err != nil {
		// If not valid JSON, try to extract a tool call heuristically
		return a.extractToolCallHeuristic(content)
	}

	return &toolCall, nil
}

// extractToolCallHeuristic tries to extract a tool call from non-JSON response
func (a *Agent) extractToolCallHeuristic(content string) (*ToolCall, error) {
	// Check if it looks like SQL
	if strings.Contains(strings.ToUpper(content), "SELECT") {
		return &ToolCall{
			Tool: "run_sql",
			Parameters: map[string]interface{}{
				"sql": content,
			},
		}, nil
	}

	return nil, fmt.Errorf("could not parse tool call from: %s", content)
}

// generateInsight generates natural language insight from tool results
func (a *Agent) generateInsight(ctx context.Context, originalQuery string, toolCall *ToolCall, toolResult interface{}) (string, error) {
	// Convert result to JSON for the prompt
	resultJSON, _ := json.Marshal(toolResult)

	prompt := fmt.Sprintf(`You are a helpful data analyst. Explain the following results in a clear, friendly way suitable for a business owner.

Original Question: %s
Tool Used: %s
Results:
%s

Provide a concise, natural language answer. Include specific numbers and insights. Be friendly and professional.`,
		originalQuery, toolCall.Tool, string(resultJSON))

	response, err := a.llmProvider.Generate(ctx, prompt,
		llm.WithTemperature(0.7),
		llm.WithMaxTokens(400),
	)
	if err != nil {
		return "", err
	}

	return response.Content, nil
}

// generateFollowUpQuestions suggests follow-up questions
func (a *Agent) generateFollowUpQuestions(query string, toolResult interface{}) []string {
	queryLower := strings.ToLower(query)
	suggestions := []string{}

	if strings.Contains(queryLower, "sales") || strings.Contains(queryLower, "revenue") {
		suggestions = append(suggestions, "Break down by product category")
		suggestions = append(suggestions, "Compare to last month")
		suggestions = append(suggestions, "Show me a chart of this")
	}

	if strings.Contains(queryLower, "month") || strings.Contains(queryLower, "date") {
		suggestions = append(suggestions, "Show me this month")
		suggestions = append(suggestions, "Which products sold best?")
		suggestions = append(suggestions, "Create a dashboard of this")
	}

	if strings.Contains(queryLower, "product") || strings.Contains(queryLower, "item") {
		suggestions = append(suggestions, "Which customers bought this?")
		suggestions = append(suggestions, "Show sales trends over time")
		suggestions = append(suggestions, "Compare top products")
	}

	if strings.Contains(queryLower, "customer") || strings.Contains(queryLower, "buyer") {
		suggestions = append(suggestions, "Show sales by customer segment")
		suggestions = append(suggestions, "Which cities have the most customers?")
	}

	if len(suggestions) == 0 {
		suggestions = append(suggestions, "Show me total sales")
		suggestions = append(suggestions, "What were our top products?")
		suggestions = append(suggestions, "Create a dashboard")
	}

	// Limit to 3 suggestions
	if len(suggestions) > 3 {
		suggestions = suggestions[:3]
	}

	return suggestions
}
