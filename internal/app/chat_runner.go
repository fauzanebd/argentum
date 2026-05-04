package app

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	sdkagent "github.com/Ingenimax/agent-sdk-go/pkg/agent"
	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/Ingenimax/agent-sdk-go/pkg/memory"
	"github.com/Ingenimax/agent-sdk-go/pkg/multitenancy"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/tenantctx"
	"github.com/fauzanebd/argentum/internal/tools"
	"github.com/fauzanebd/argentum/internal/whatsapp"
)

// ChatRunner is the worker-side half of the chat pipeline. It runs the
// agent against a queued ChatRunPayload, persists the assistant turn,
// publishes streaming events through the EventBus, and (for WhatsApp
// channels) sends the final reply via the WA provider directly.
type ChatRunner struct {
	threads    *ThreadService
	messages   domain.MessageRepository
	threadRepo domain.ThreadRepository
	agent      *sdkagent.Agent
	bus        EventBus
	wa         whatsapp.Provider
	pool       *db.TenantConnPool
	schemaTool *tools.GetSchemaTool
}

// NewChatRunner wires the worker's dependencies.
func NewChatRunner(
	threads *ThreadService,
	messages domain.MessageRepository,
	threadRepo domain.ThreadRepository,
	agent *sdkagent.Agent,
	bus EventBus,
	wa whatsapp.Provider,
	pool *db.TenantConnPool,
	schemaTool *tools.GetSchemaTool,
) *ChatRunner {
	return &ChatRunner{
		threads:    threads,
		messages:   messages,
		threadRepo: threadRepo,
		agent:      agent,
		bus:        bus,
		wa:         wa,
		pool:       pool,
		schemaTool: schemaTool,
	}
}

// Run processes one chat task end-to-end. Errors returned trigger an
// asynq retry; user-visible failures (e.g. guardrail rejections) write a
// friendly assistant message and return nil.
func (r *ChatRunner) Run(ctx context.Context, p queue.ChatRunPayload) error {
	ctx = tenantctx.WithCompanyID(ctx, p.CompanyID)
	ctx = tenantctx.WithThreadID(ctx, p.ThreadID)
	if p.UserID != "" {
		ctx = tenantctx.WithUserID(ctx, p.UserID)
	}
	ctx = multitenancy.WithOrgID(ctx, p.CompanyID)
	ctx = memory.WithConversationID(ctx, p.ThreadID)

	if err := r.hydrateMemory(ctx, p); err != nil {
		logrus.WithError(err).Warn("memory hydration failed; continuing with empty context")
	}

	now := time.Now()
	_ = r.bus.Publish(p.ThreadID, ChatEvent{
		JobID: p.UserMsgID, ThreadID: p.ThreadID, Type: "started", Timestamp: now,
	})

	schema, err := r.schemaTool.PrefetchSchema(ctx, p.CompanyID)
	if err != nil {
		logrus.WithError(err).Warn("schema prefetch failed; agent will retry")
	}

	start := time.Now()

	// Prepend organization, currency, and DB-type context so the agent
	// knows who it is assisting, formats monetary values correctly, and
	// generates SQL compatible with the tenant DB.
	agentMsg := withCompanyNameContext(p.Message, p.CompanyName)
	agentMsg = withCurrencyContext(agentMsg, p.DefaultCurrency)
	if schema != nil {
		agentMsg = withDBTypeContext(agentMsg, schema.DBType)
	}

	// Try streaming first; fall back to blocking Run if the LLM doesn't
	// support it.
	var response string
	var streaming bool
	if r.agent.GetLLM().SupportsStreaming() {
		sp := p
		sp.Message = agentMsg
		if streamResp, err := r.runStream(ctx, sp); err == nil {
			response = streamResp
			streaming = true
		} else {
			logrus.WithError(err).Warn("streaming failed; falling back to blocking run")
		}
	}
	if !streaming {
		var err error
		response, err = r.agent.Run(ctx, agentMsg)
		if err != nil {
			return r.handleRunError(ctx, p, err)
		}
	}

	latency := time.Since(start)
	r.completeWith(ctx, p, response, 0, 0, latency)
	return nil
}

// runStream executes the agent with streaming and fans out delta / thinking /
// tool_call / tool_result events to the EventBus. It returns the full
// assembled response text.
func (r *ChatRunner) runStream(ctx context.Context, p queue.ChatRunPayload) (string, error) {
	events, err := r.agent.RunStream(ctx, p.Message)
	if err != nil {
		return "", err
	}

	var fullResponse strings.Builder

	for evt := range events {
		switch evt.Type {
		case interfaces.AgentEventContent:
			fullResponse.WriteString(evt.Content)
			_ = r.bus.Publish(p.ThreadID, ChatEvent{
				JobID:     p.UserMsgID,
				ThreadID:  p.ThreadID,
				Type:      "delta",
				Content:   evt.Content,
				Timestamp: time.Now(),
			})

		case interfaces.AgentEventThinking:
			_ = r.bus.Publish(p.ThreadID, ChatEvent{
				JobID:        p.UserMsgID,
				ThreadID:     p.ThreadID,
				Type:         "thinking",
				ThinkingStep: evt.ThinkingStep,
				Timestamp:    time.Now(),
			})

		case interfaces.AgentEventToolCall:
			if evt.ToolCall != nil {
				args := map[string]interface{}{}
				if evt.ToolCall.Arguments != "" {
					_ = json.Unmarshal([]byte(evt.ToolCall.Arguments), &args)
				}
				_ = r.bus.Publish(p.ThreadID, ChatEvent{
					JobID:     p.UserMsgID,
					ThreadID:  p.ThreadID,
					Type:      "tool_call",
					ToolCall:  &ToolCallEvent{Name: evt.ToolCall.Name, Arguments: args},
					Timestamp: time.Now(),
				})
			}

		case interfaces.AgentEventToolResult:
			if evt.ToolCall != nil {
				res := map[string]interface{}{}
				if evt.ToolCall.Result != "" {
					_ = json.Unmarshal([]byte(evt.ToolCall.Result), &res)
				}
				_ = r.bus.Publish(p.ThreadID, ChatEvent{
					JobID:     p.UserMsgID,
					ThreadID:  p.ThreadID,
					Type:      "tool_result",
					ToolCall:  &ToolCallEvent{Name: evt.ToolCall.Name, Result: res},
					Timestamp: time.Now(),
				})
			}

		case interfaces.AgentEventError:
			if evt.Error != nil {
				errMsg := evt.Error.Error()
				// Guardrails rejections should be presented as normal
				// assistant messages, not raw errors.
				const guardrailsPrefix = "guardrails error: "
				if strings.HasPrefix(errMsg, guardrailsPrefix) {
					userMsg := strings.TrimPrefix(errMsg, guardrailsPrefix)
					fullResponse.Reset()
					fullResponse.WriteString(userMsg)
					_ = r.bus.Publish(p.ThreadID, ChatEvent{
						JobID:     p.UserMsgID,
						ThreadID:  p.ThreadID,
						Type:      "delta",
						Content:   userMsg,
						Timestamp: time.Now(),
					})
				} else {
					_ = r.bus.Publish(p.ThreadID, ChatEvent{
						JobID:     p.UserMsgID,
						ThreadID:  p.ThreadID,
						Type:      "error",
						Error:     errMsg,
						Timestamp: time.Now(),
					})
				}
			}

		case interfaces.AgentEventComplete:
			// No-op; final event is published after the loop.
		}
	}

	return fullResponse.String(), nil
}

// handleRunError deals with blocking-run failures. Guardrails errors are
// surfaced as assistant messages; everything else is retried by asynq.
func (r *ChatRunner) handleRunError(ctx context.Context, p queue.ChatRunPayload, err error) error {
	const guardrailsPrefix = "guardrails error: "
	userMsg := err.Error()
	if strings.HasPrefix(userMsg, guardrailsPrefix) {
		userMsg = strings.TrimPrefix(userMsg, guardrailsPrefix)
		r.completeWith(ctx, p, userMsg, 0, 0, 0)
		return nil
	}
	_ = r.bus.Publish(p.ThreadID, ChatEvent{
		JobID: p.UserMsgID, ThreadID: p.ThreadID, Type: "error",
		Error:     "I encountered an error processing your request. Please try rephrasing.",
		Timestamp: time.Now(),
	})
	logrus.WithError(err).WithField("company_id", p.CompanyID).Error("agent run failed")
	return err
}

// completeWith persists the assistant message, publishes the final event,
// and (for WA channels) sends the reply through the WhatsApp provider.
func (r *ChatRunner) completeWith(
	ctx context.Context, p queue.ChatRunPayload, response string,
	tokensIn, tokensOut int, latency time.Duration,
) {
	now := time.Now()
	if _, err := r.threads.AppendAssistantMessage(
		ctx, p.ThreadID, response, tokensIn, tokensOut, latency.Milliseconds(),
	); err != nil {
		logrus.WithError(err).Warn("append assistant message")
	}
	if err := r.bus.Publish(p.ThreadID, ChatEvent{
		JobID: p.UserMsgID, ThreadID: p.ThreadID, Type: "final",
		Content:   response,
		Metadata:  map[string]interface{}{"latency_ms": latency.Milliseconds()},
		Timestamp: now,
	}); err != nil {
		logrus.WithError(err).Warn("publish final event")
	}

	if p.Channel == domain.ChannelWhatsApp && p.PhoneNumber != "" && r.wa != nil {
		waText := stripMarkdownLinks(response)
		if err := r.wa.SendMessage(p.PhoneNumber, waText); err != nil {
			logrus.WithError(err).WithField("phone", p.PhoneNumber).Error("whatsapp send failed")
		}
	}
}

// stripMarkdownLinks converts [text](url) to "text: url" so WhatsApp can
// auto-detect and hyperlink the raw URL.
func stripMarkdownLinks(s string) string {
	re := regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	return re.ReplaceAllString(s, "$1: $2")
}

// hydrateMemory loads prior turns from Postgres into the agent's memory so
// the agent has full context even if Redis was empty or reset.
func (r *ChatRunner) hydrateMemory(ctx context.Context, p queue.ChatRunPayload) error {
	msgs, err := r.messages.ListByThread(ctx, p.ThreadID, 200, 0)
	if err != nil {
		return err
	}
	if len(msgs) == 0 {
		return nil
	}

	mem := r.agent.GetMemory()

	// If the memory already holds messages for this conversation, skip
	// hydration to avoid duplicates.
	if convMem, ok := mem.(interfaces.ConversationMemory); ok {
		existing, err := convMem.GetConversationMessages(ctx, p.ThreadID)
		if err == nil && len(existing) > 0 {
			return nil
		}
	}

	for _, m := range msgs {
		// Skip the current user message; the agent will add it itself
		// during Run/RunStream.
		if m.Role == domain.MessageRoleUser && m.Content == p.Message && m.ID == p.UserMsgID {
			continue
		}
		sdkMsg := interfaces.Message{
			Role:    interfaces.MessageRole(m.Role),
			Content: m.Content,
		}
		if err := mem.AddMessage(ctx, sdkMsg); err != nil {
			logrus.WithError(err).Warn("hydrate memory: add message")
		}
	}
	return nil
}

// withCompanyNameContext prepends the tenant organization name so the
// agent can personalize references. If name is empty, msg is unchanged.
func withCompanyNameContext(msg, companyName string) string {
	if companyName == "" {
		return msg
	}
	return fmt.Sprintf(
		"[System context: The user's organization is named %s.]\n\n%s",
		companyName, msg,
	)
}

// withCurrencyContext prepends a short currency instruction to the user
// message so the agent knows which currency to use for formatting. If
// currency is empty, the message is returned unchanged.
func withCurrencyContext(msg, currency string) string {
	if currency == "" {
		return msg
	}
	return fmt.Sprintf(
		"[System context: The default currency is %s. Format money accordingly.]\n\n%s",
		currency, msg,
	)
}

// withDBTypeContext prepends the connected database type so the agent
// generates dialect-compatible SQL. If dbType is empty, the message is
// returned unchanged.
func withDBTypeContext(msg, dbType string) string {
	if dbType == "" {
		return msg
	}
	hints := ""
	switch dbType {
	case "mysql":
		hints = " Use MySQL-compatible syntax. For date truncation use DATE_FORMAT, not DATE_TRUNC. For string aggregation use GROUP_CONCAT, not STRING_AGG. For current timestamp use NOW(), not CURRENT_TIMESTAMP. For date arithmetic use DATE_ADD / DATE_SUB, not INTERVAL expressions."
	case "postgres":
		hints = " Use PostgreSQL-compatible syntax. For date truncation use DATE_TRUNC. For string aggregation use STRING_AGG. For current timestamp use CURRENT_TIMESTAMP or NOW()."
	}
	return fmt.Sprintf(
		"[System context: The connected database is %s.%s]\n\n%s",
		dbType, hints, msg,
	)
}
