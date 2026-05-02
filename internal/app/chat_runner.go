package app

import (
	"context"
	"strings"
	"time"

	sdkagent "github.com/Ingenimax/agent-sdk-go/pkg/agent"
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

	now := time.Now()
	_ = r.bus.Publish(p.ThreadID, ChatEvent{
		JobID: p.UserMsgID, ThreadID: p.ThreadID, Type: "started", Timestamp: now,
	})

	if _, err := r.schemaTool.PrefetchSchema(ctx, p.CompanyID); err != nil {
		logrus.WithError(err).Warn("schema prefetch failed; agent will retry")
	}

	start := time.Now()
	response, err := r.agent.Run(ctx, p.Message)
	latency := time.Since(start)

	if err != nil {
		const guardrailsPrefix = "guardrails error: "
		userMsg := err.Error()
		if strings.HasPrefix(userMsg, guardrailsPrefix) {
			userMsg = strings.TrimPrefix(userMsg, guardrailsPrefix)
			r.completeWith(ctx, p, userMsg, latency)
			return nil
		}

		// Permanent vs. transient: surface the original error to asynq
		// so it can apply its retry/backoff schedule. The user sees a
		// best-effort error event in the meantime.
		_ = r.bus.Publish(p.ThreadID, ChatEvent{
			JobID: p.UserMsgID, ThreadID: p.ThreadID, Type: "error",
			Error:     "I encountered an error processing your request. Please try rephrasing.",
			Timestamp: time.Now(),
		})
		logrus.WithError(err).WithField("company_id", p.CompanyID).Error("agent run failed")
		return err
	}

	r.completeWith(ctx, p, response, latency)
	return nil
}

// completeWith persists the assistant message, publishes the final event,
// and (for WA channels) sends the reply through the WhatsApp provider.
func (r *ChatRunner) completeWith(
	ctx context.Context, p queue.ChatRunPayload, response string, latency time.Duration,
) {
	now := time.Now()
	if _, err := r.threads.AppendAssistantMessage(
		ctx, p.ThreadID, response, 0, 0, latency.Milliseconds(),
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
		if err := r.wa.SendMessage(p.PhoneNumber, response); err != nil {
			logrus.WithError(err).WithField("phone", p.PhoneNumber).Error("whatsapp send failed")
		}
	}
}
