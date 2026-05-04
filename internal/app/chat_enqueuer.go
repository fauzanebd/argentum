package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
)

// ChatEnqueuer is the API-side half of the chat pipeline. It resolves the
// thread, persists the user message synchronously, and hands the actual
// agent run off to asynq via queue.Enqueuer. The worker (cmd/worker) picks
// the task up and publishes events back through EventBus / Redis pub/sub.
type ChatEnqueuer struct {
	threads   *ThreadService
	messages  domain.MessageRepository
	companies domain.CompanyRepository
	enqueuer  *queue.Enqueuer
}

// NewChatEnqueuer wires the dependencies. messages is unused today (kept
// for symmetry / future "user message ack" responses) but threads is
// required for ResolveForPhone / ResolveForUser + AppendUserMessage.
func NewChatEnqueuer(threads *ThreadService, messages domain.MessageRepository, companies domain.CompanyRepository, enqueuer *queue.Enqueuer) *ChatEnqueuer {
	return &ChatEnqueuer{threads: threads, messages: messages, companies: companies, enqueuer: enqueuer}
}

// ChatInput is the unified input shape across channels. It mirrors the
// shape consumed by the old ChatService.HandleInput so existing callers
// (handlers/chat.go, handlers/webhook.go) need only change the method.
type ChatInput struct {
	Channel     domain.Channel
	CompanyID   string
	UserID      string // dashboard only
	PhoneNumber string // whatsapp only
	Message     string
	ThreadID    string // dashboard only; if set, bypasses resolver
}

func (in ChatInput) validate() error {
	if in.CompanyID == "" {
		return errors.New("company_id required")
	}
	if strings.TrimSpace(in.Message) == "" {
		return errors.New("message required")
	}
	switch in.Channel {
	case domain.ChannelWhatsApp:
		if in.PhoneNumber == "" {
			return errors.New("phone_number required for whatsapp channel")
		}
	case domain.ChannelDashboard:
		if in.UserID == "" {
			return errors.New("user_id required for dashboard channel")
		}
	default:
		return fmt.Errorf("invalid channel: %q", in.Channel)
	}
	return nil
}

// EnqueueResult is returned synchronously from Enqueue. The actual
// response streams over the WebSocket once the worker finishes.
type EnqueueResult struct {
	TaskID      string
	Thread      *domain.ConversationThread
	IsNewThread bool
	UserMsgID   string
}

// CreateDashboardThread creates a fresh empty dashboard thread for the
// authenticated user. The frontend calls this when the user clicks
// "New conversation".
func (s *ChatEnqueuer) CreateDashboardThread(ctx context.Context, companyID, userID string) (*domain.ConversationThread, error) {
	return s.threads.CreateDashboardThread(ctx, companyID, userID, "")
}

// Enqueue resolves the thread, persists the user message, and dispatches
// a chat:run task to the worker queue.
func (s *ChatEnqueuer) Enqueue(ctx context.Context, in ChatInput) (*EnqueueResult, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}

	var resolved *ResolveResult
	var err error

	switch in.Channel {
	case domain.ChannelWhatsApp:
		resolved, err = s.threads.ResolveForPhone(ctx, in.CompanyID, in.PhoneNumber, in.Message)
	case domain.ChannelDashboard:
		if in.ThreadID != "" {
			// Explicit thread selected by the user — bypass resolver.
			thread, err := s.threads.GetByID(ctx, in.ThreadID)
			if err != nil {
				return nil, fmt.Errorf("lookup thread: %w", err)
			}
			if thread.CompanyID != in.CompanyID {
				return nil, fmt.Errorf("thread does not belong to company")
			}
			resolved = &ResolveResult{Thread: thread, IsNew: false}
		} else {
			// Brand-new dashboard chat — create a fresh thread.
			thread, err := s.threads.CreateDashboardThread(ctx, in.CompanyID, in.UserID, in.Message)
			if err != nil {
				return nil, fmt.Errorf("create thread: %w", err)
			}
			resolved = &ResolveResult{Thread: thread, IsNew: true}
		}
	default:
		return nil, fmt.Errorf("%w: unknown channel %q", domain.ErrInvalidInput, in.Channel)
	}
	if err != nil {
		return nil, fmt.Errorf("resolve thread: %w", err)
	}
	thread := resolved.Thread

	userMsg, err := s.threads.AppendUserMessage(ctx, thread.ID, in.Message)
	if err != nil {
		return nil, fmt.Errorf("append user message: %w", err)
	}

	// Look up company display name and default currency for agent context.
	var companyName, currency string
	if company, err := s.companies.GetByID(ctx, in.CompanyID); err == nil {
		companyName = company.Name
		currency = company.DefaultCurrency
	}

	taskID, err := s.enqueuer.EnqueueChatRun(ctx, queue.ChatRunPayload{
		CompanyID:       in.CompanyID,
		ThreadID:        thread.ID,
		UserID:          in.UserID,
		PhoneNumber:     in.PhoneNumber,
		Channel:         in.Channel,
		Message:         in.Message,
		UserMsgID:       userMsg.ID,
		CompanyName:     companyName,
		DefaultCurrency: currency,
	})
	if err != nil {
		return nil, fmt.Errorf("enqueue chat:run: %w", err)
	}

	return &EnqueueResult{
		TaskID:      taskID,
		Thread:      thread,
		IsNewThread: resolved.IsNew,
		UserMsgID:   userMsg.ID,
	}, nil
}
