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
	"github.com/fauzanebd/argentum/internal/llmtenant"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/tenantctx"
	"github.com/fauzanebd/argentum/internal/whatsapp"
)

// AgentFactory builds a sdkagent.Agent for one chat turn from per-tenant
// LLM clients. The worker captures tools, memory, system prompt, and
// guardrails in the closure; callers pass the freshly resolved primary and
// light LLM clients plus the primary's interface name (so the factory can
// gate provider-specific options like Anthropic prompt caching).
type AgentFactory func(primary, light interfaces.LLM, primaryInterface string) (*sdkagent.Agent, error)

// ScheduledRunMarker is the narrow contract ChatRunner uses to close out
// a scheduled_task_runs row when the agent finishes (or errors). Defined
// as an interface so the worker can pass *ScheduledTaskService while
// API-only flows can pass nil.
type ScheduledRunMarker interface {
	MarkRunResult(ctx context.Context, runID, assistantMsgID string, runErr error)
}

// ChatRunner is the worker-side half of the chat pipeline. It runs the
// agent against a queued ChatRunPayload, persists the assistant turn,
// publishes streaming events through the EventBus, and (for WhatsApp
// channels) sends the final reply via the WA provider directly.
type ChatRunner struct {
	threads      *ThreadService
	messages     domain.MessageRepository
	threadRepo   domain.ThreadRepository
	connections  domain.ConnectionRepository
	agentFactory AgentFactory
	llmCache     *llmtenant.ClientCache
	bus          EventBus
	wa           whatsapp.Provider
	pool         *db.TenantConnPool
	scheduled    ScheduledRunMarker
	historyLimit int

	// Embedding-based table picker. Both must be non-nil to inject hints;
	// otherwise the runner silently skips and the agent falls back to the
	// regular get_schema flow.
	embRepo    domain.TableEmbeddingRepository
	embedCache *llmtenant.EmbeddingCache
	embTopK    int
}

// NewChatRunner wires the worker's dependencies. scheduled is optional;
// pass nil when no scheduled-task subsystem is configured. historyLimit
// caps how many prior thread messages are re-hydrated into agent memory
// per turn; <=0 falls back to a safe default.
func NewChatRunner(
	threads *ThreadService,
	messages domain.MessageRepository,
	threadRepo domain.ThreadRepository,
	connections domain.ConnectionRepository,
	agentFactory AgentFactory,
	llmCache *llmtenant.ClientCache,
	bus EventBus,
	wa whatsapp.Provider,
	pool *db.TenantConnPool,
	scheduled ScheduledRunMarker,
	historyLimit int,
) *ChatRunner {
	if historyLimit <= 0 {
		historyLimit = 20
	}
	return &ChatRunner{
		threads:      threads,
		messages:     messages,
		threadRepo:   threadRepo,
		connections:  connections,
		agentFactory: agentFactory,
		llmCache:     llmCache,
		bus:          bus,
		wa:           wa,
		pool:         pool,
		scheduled:    scheduled,
		historyLimit: historyLimit,
	}
}

// WithTablePicker enables the embedding-based table-hint injection. Pass
// a nil repo or cache to leave the feature disabled. topK <= 0 falls back
// to 8.
func (r *ChatRunner) WithTablePicker(repo domain.TableEmbeddingRepository, embCache *llmtenant.EmbeddingCache, topK int) *ChatRunner {
	if topK <= 0 {
		topK = 8
	}
	r.embRepo = repo
	r.embedCache = embCache
	r.embTopK = topK
	return r
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

	// Cheap small-talk short-circuit: skip the agent (and the light-LLM
	// guardrail/classifier pipeline behind it) when the message is a
	// greeting or one-word ack. Saves multiple LLM calls per turn.
	if reply, ok := trivialReply(p.Message); ok {
		now := time.Now()
		_ = r.bus.Publish(p.ThreadID, ChatEvent{
			JobID: p.UserMsgID, ThreadID: p.ThreadID, Type: "started", Timestamp: now,
		})
		r.completeWith(ctx, p, reply, 0, 0, 0)
		return nil
	}

	// Resolve the per-tenant LLMs for this turn. Primary is required; light
	// falls back to primary if resolution fails (preserves today's behavior
	// where missing LIGHT_LLM_API_KEY means "use primary").
	primaryLLM, primaryProfile, err := r.llmCache.For(ctx, p.CompanyID, domain.LLMTierPrimary)
	if err != nil {
		return r.handleRunError(ctx, p, fmt.Errorf("resolve primary LLM: %w", err))
	}
	lightLLM, _, err := r.llmCache.For(ctx, p.CompanyID, domain.LLMTierLight)
	if err != nil {
		logrus.WithError(err).Warn("resolve light LLM failed; using primary for guardrails")
		lightLLM = primaryLLM
	}
	agent, err := r.agentFactory(primaryLLM, lightLLM, primaryProfile.Interface)
	if err != nil {
		return r.handleRunError(ctx, p, fmt.Errorf("build agent: %w", err))
	}

	if err := r.hydrateMemory(ctx, agent, p); err != nil {
		logrus.WithError(err).Warn("memory hydration failed; continuing with empty context")
	}

	now := time.Now()
	_ = r.bus.Publish(p.ThreadID, ChatEvent{
		JobID: p.UserMsgID, ThreadID: p.ThreadID, Type: "started", Timestamp: now,
	})

	sources, err := r.connections.ListByCompany(ctx, p.CompanyID)
	if err != nil {
		logrus.WithError(err).Warn("source catalog prefetch failed; agent will discover via list_sources")
	}

	start := time.Now()

	// Prepend organization, currency, source-catalog, and (optionally) the
	// embedding-based table-picker hint. Order matters: the table hint sits
	// closest to the top so the agent reads it before the source catalog.
	agentMsg := withCompanyNameContext(p.Message, p.CompanyName)
	agentMsg = withCurrencyContext(agentMsg, p.DefaultCurrency)
	agentMsg = withSourcesContext(agentMsg, sources)
	agentMsg = r.withRelevantTablesContext(ctx, agentMsg, p.Message, sources)

	// Try streaming first; fall back to blocking Run if the LLM doesn't
	// support it.
	var response string
	var streaming bool
	if agent.GetLLM().SupportsStreaming() {
		sp := p
		sp.Message = agentMsg
		if streamResp, err := r.runStream(ctx, agent, sp); err == nil {
			response = streamResp
			streaming = true
		} else {
			logrus.WithError(err).Warn("streaming failed; falling back to blocking run")
		}
	}
	if !streaming {
		var err error
		response, err = agent.Run(ctx, agentMsg)
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
func (r *ChatRunner) runStream(ctx context.Context, agent *sdkagent.Agent, p queue.ChatRunPayload) (string, error) {
	events, err := agent.RunStream(ctx, p.Message)
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
	if p.ScheduledRunID != "" && r.scheduled != nil {
		r.scheduled.MarkRunResult(ctx, p.ScheduledRunID, "", err)
	}
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
	assistantMsg, err := r.threads.AppendAssistantMessage(
		ctx, p.ThreadID, response, tokensIn, tokensOut, latency.Milliseconds(),
	)
	if err != nil {
		logrus.WithError(err).Warn("append assistant message")
	}
	if p.ScheduledRunID != "" && r.scheduled != nil {
		var msgID string
		if assistantMsg != nil {
			msgID = assistantMsg.ID
		}
		r.scheduled.MarkRunResult(ctx, p.ScheduledRunID, msgID, nil)
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
func (r *ChatRunner) hydrateMemory(ctx context.Context, agent *sdkagent.Agent, p queue.ChatRunPayload) error {
	msgs, err := r.messages.ListByThread(ctx, p.ThreadID, r.historyLimit, 0)
	if err != nil {
		return err
	}
	if len(msgs) == 0 {
		return nil
	}

	mem := agent.GetMemory()

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

// withRelevantTablesContext queries the per-source embedding index for the
// top-K tables semantically closest to the user's message, then prepends a
// hint listing them so the agent calls get_schema with a pre-filtered
// `tables` argument instead of dumping the full catalog. Silent skip when
// the feature is off, the source has no embeddings, or the embedding API
// fails — the agent's regular get_schema flow still works.
func (r *ChatRunner) withRelevantTablesContext(ctx context.Context, msg, userMsg string, sources []*domain.DBConnection) string {
	companyID := tenantctx.CompanyID(ctx)
	if r.embRepo == nil || r.embedCache == nil || len(sources) == 0 {
		logrus.WithField("company_id", companyID).Debug("table picker: feature off (nil repo/cache or no sources)")
		return msg
	}
	eligible := make([]*domain.DBConnection, 0, len(sources))
	for _, s := range sources {
		if s.EnableTableEmbedding {
			eligible = append(eligible, s)
		}
	}
	if len(eligible) == 0 {
		logrus.WithField("company_id", companyID).Debug("table picker: no eligible sources (enable_table_embedding=false on all)")
		return msg
	}

	embClient, err := r.embedCache.For(ctx, companyID)
	if err != nil {
		logrus.WithError(err).WithField("company_id", companyID).Warn("table picker: resolve embedding client failed; skipping hint")
		return msg
	}
	if embClient == nil {
		logrus.WithField("company_id", companyID).Info("table picker: no embedding client for tenant (no key); skipping hint")
		return msg
	}

	embedStart := time.Now()
	vecs, err := embClient.Embed(ctx, []string{userMsg})
	if err != nil || len(vecs) == 0 {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": companyID,
			"model":      embClient.Model(),
		}).Warn("table picker: embed user message failed; skipping hint")
		return msg
	}
	logrus.WithFields(logrus.Fields{
		"company_id":  companyID,
		"model":       embClient.Model(),
		"duration_ms": time.Since(embedStart).Milliseconds(),
	}).Debug("table picker: user message embedded")
	qv := vecs[0]

	var b strings.Builder
	fmt.Fprintf(&b, "[System context: Likely-relevant tables (top-%d semantic match per source):\n", r.embTopK)
	any := false
	hintedTotal := 0
	for _, s := range eligible {
		n, err := r.embRepo.CountBySource(ctx, s.ID)
		if err != nil {
			logrus.WithError(err).WithField("source_id", s.ID).Warn("table picker: CountBySource failed")
			continue
		}
		if n == 0 {
			logrus.WithField("source_id", s.ID).Warn("table picker: source has no embeddings — run reindex")
			continue
		}
		hits, err := r.embRepo.TopK(ctx, s.ID, qv, r.embTopK)
		if err != nil {
			logrus.WithError(err).WithField("source_id", s.ID).Warn("table picker: TopK query failed")
			continue
		}
		if len(hits) == 0 {
			logrus.WithField("source_id", s.ID).Info("table picker: TopK returned 0 rows")
			continue
		}
		names := make([]string, 0, len(hits))
		for _, h := range hits {
			names = append(names, h.TableName)
		}
		any = true
		hintedTotal += len(names)
		logrus.WithFields(logrus.Fields{
			"source_id": s.ID,
			"k":         r.embTopK,
			"hits":      len(hits),
			"names":     names,
		}).Info("table picker: top-k resolved")
		fmt.Fprintf(&b, " - %s: %s\n", s.ID, strings.Join(names, ", "))
	}
	if !any {
		logrus.WithField("company_id", companyID).Info("table picker: no source produced hits; hint skipped")
		return msg
	}
	b.WriteString("Pass these as the `tables` argument to get_schema for the matching source; only call get_schema unfiltered if the hint clearly misses what the user asked about.]\n\n")
	b.WriteString(msg)
	logrus.WithFields(logrus.Fields{
		"company_id":            companyID,
		"sources_with_hits":     len(eligible),
		"total_tables_hinted":   hintedTotal,
	}).Info("table picker: hint injected")
	return b.String()
}

// withSourcesContext prepends the catalog of available data sources so the
// agent can pick a source_id per tool call without spending a list_sources /
// get_schema round-trip. Per-source dialect hints are returned in each
// run_sql / get_schema / create_visualization result (db_type field) so we
// don't repeat them here.
func withSourcesContext(msg string, sources []*domain.DBConnection) string {
	if len(sources) == 0 {
		return msg
	}
	var b strings.Builder
	b.WriteString("[System context: Available data sources for this organization:\n")
	for _, s := range sources {
		label := s.Label
		if label == "" {
			label = "(unlabelled)"
		}
		marker := ""
		if s.IsDefault {
			marker = ", default"
		}
		desc := s.Description
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(&b, " - %s | %s (%s%s) — %s\n", s.ID, label, s.DBType, marker, desc)
	}
	b.WriteString("Pick the appropriate source_id when calling get_schema, run_sql, or create_visualization. ")
	if len(sources) > 1 {
		b.WriteString("If unsure which source the user means, ASK before querying.")
	} else {
		b.WriteString("Only one source exists, so source_id is optional.")
	}
	b.WriteString("]\n\n")
	b.WriteString(msg)
	return b.String()
}

// trivialMessagePattern matches greetings and one-word acks in English and
// Indonesian. Anchored, case-insensitive, optional punctuation/emoji-free
// trailing chars. Question marks short-circuit so "hi?" still goes to the
// agent — though in practice questions are longer.
var trivialMessagePattern = regexp.MustCompile(
	`(?i)^\s*(hi|hello|hey|yo|hai|halo|haloo+|p|pagi|selamat pagi|siang|selamat siang|sore|selamat sore|malam|selamat malam|` +
		`thanks|thank you|thx|ok|okay|okey|noted|sip|oke|baik|terima kasih|makasih|mksh|` +
		`test|tes|ping|ok thanks|thank you so much)[\s.!,]*$`,
)

var indonesianTrivialPattern = regexp.MustCompile(
	`(?i)^\s*(hai|halo|haloo+|p|pagi|selamat pagi|siang|selamat siang|sore|selamat sore|malam|selamat malam|` +
		`sip|oke|baik|terima kasih|makasih|mksh|tes)[\s.!,]*$`,
)

// trivialReply returns a canned response when the user message is small-talk
// (greeting, one-word ack, ping). Skipping the agent on these saves the
// primary-LLM call plus all light-LLM guardrail/classifier work.
func trivialReply(msg string) (string, bool) {
	if !trivialMessagePattern.MatchString(msg) {
		return "", false
	}
	if indonesianTrivialPattern.MatchString(msg) {
		return "Halo! Ada pertanyaan tentang data atau metrik bisnis yang bisa saya bantu?", true
	}
	return "Hi! Ask me a question about your business data or metrics and I'll dig in.", true
}
