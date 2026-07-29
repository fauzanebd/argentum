package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
	"github.com/fauzanebd/argentum/internal/tenantctx"
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
	budget    BudgetChecker
}

// BudgetChecker is the narrow contract ChatEnqueuer uses to refuse a turn a
// tenant cannot pay for. Declared here rather than taken as *UsageService so
// the refusal path is testable without a usage repository, and so nothing
// else about metering leaks into the enqueue path.
type BudgetChecker interface {
	CheckBudget(ctx context.Context, companyID string) (BudgetState, error)
}

// NewChatEnqueuer wires the dependencies. messages is unused today (kept
// for symmetry / future "user message ack" responses) but threads is
// required for ResolveForPhone / ResolveForUser + AppendUserMessage.
func NewChatEnqueuer(threads *ThreadService, messages domain.MessageRepository, companies domain.CompanyRepository, enqueuer *queue.Enqueuer) *ChatEnqueuer {
	return &ChatEnqueuer{threads: threads, messages: messages, companies: companies, enqueuer: enqueuer}
}

// WithBudget gates every enqueued turn on the tenant's credit balance. The
// kill switch lives inside the checker (CreditPolicy.Enforce) rather than
// here, so that a future caller — the /v1 API in T-A1 — cannot get a
// different answer by wiring itself differently.
func (s *ChatEnqueuer) WithBudget(b BudgetChecker) *ChatEnqueuer {
	s.budget = b
	return s
}

// ChatInput is the unified input shape across channels. It mirrors the
// shape consumed by the old ChatService.HandleInput so existing callers
// (handlers/chat.go, handlers/webhook.go) need only change the method.
type ChatInput struct {
	Channel          domain.Channel
	CompanyID        string
	UserID           string // dashboard only
	PhoneNumber      string // whatsapp only
	DiscordUserID    string // discord only
	DiscordChannelID string // discord only; reply destination
	LarkOpenID       string // lark only; initiating user's open_id
	LarkChatID       string // lark only; chat the @mention came from
	LarkThreadKey    string // lark only; thread lookup key
	LarkMessageID    string // lark only; reply target (latest inbound message id)
	APIUserRef       string // api only; the tenant's own reference for the end user
	Message          string
	// Directive is an instruction for this turn that the *caller* did not
	// write — today, `POST /v1/reports`'s ReportDirective. It is carried
	// beside Message rather than folded into it because the two are judged
	// differently: Message is what the input guardrails inspect, and an
	// instruction block sent as a user message is refused by our own
	// injection classifier (T-A2b). ChatRunner delivers this as a per-turn
	// system-prompt addendum instead.
	//
	// It is also not persisted as the user's message, so a thread reads back
	// as the conversation the caller had rather than as our scaffolding.
	Directive string
	ThreadID  string // dashboard and api; if set, bypasses resolver
	// APIReportID ties this turn to the report job `POST /v1/reports` handed
	// back (T-A2). The worker marks that row terminal when the turn ends.
	APIReportID string
	// APIKeyID attributes the turn to the credential that started it, which is
	// what makes T-05's audit rows say "an integration did this" rather than
	// naming a person who was not there.
	APIKeyID string
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
	case domain.ChannelDiscord:
		if in.DiscordUserID == "" {
			return errors.New("discord_user_id required for discord channel")
		}
		if in.DiscordChannelID == "" {
			return errors.New("discord_channel_id required for discord channel")
		}
	case domain.ChannelLark:
		if in.LarkOpenID == "" {
			return errors.New("lark_open_id required for lark channel")
		}
		if in.LarkThreadKey == "" {
			return errors.New("lark_thread_key required for lark channel")
		}
		if in.LarkMessageID == "" {
			return errors.New("lark_message_id required for lark channel")
		}
	case domain.ChannelAPI:
		// Either identity will do, and one of them must be there. A caller
		// continuing a conversation names the thread; a caller starting one
		// names their user. Neither means an unattributable turn: it would be
		// billed to the company with nothing in `usage/by-user` to say who
		// spent it, which is the report the tenant reads to police their own
		// integration.
		if in.APIUserRef == "" && in.ThreadID == "" {
			return errors.New("api_user_ref or thread_id required for api channel")
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
	// BudgetWarning is set only when the turn ran but the tenant is near the
	// end of their credit. Nil is the ordinary case, which keeps the field
	// absent from the JSON response rather than shipping a "not warning"
	// object on every send.
	BudgetWarning *BudgetState
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

	// Before the thread is resolved, not just before the task is enqueued: a
	// refused turn must not leave a new thread and an orphan user message
	// behind, which is what a company at zero credits would otherwise
	// accumulate one per attempt.
	budget, err := s.checkBudget(ctx, in.CompanyID)
	if err != nil {
		return nil, err
	}

	var resolved *ResolveResult

	switch in.Channel {
	case domain.ChannelWhatsApp:
		resolved, err = s.threads.ResolveForPhone(ctx, in.CompanyID, in.PhoneNumber, in.Message)
	case domain.ChannelDiscord:
		resolved, err = s.threads.ResolveForDiscordUser(ctx, in.CompanyID, in.DiscordUserID, in.Message)
	case domain.ChannelLark:
		resolved, err = s.threads.ResolveForLark(ctx, in.CompanyID, in.LarkChatID, in.LarkThreadKey, in.LarkOpenID, in.Message)
	case domain.ChannelAPI:
		if in.ThreadID != "" {
			thread, err := s.threads.GetByID(ctx, in.ThreadID)
			if err != nil {
				return nil, fmt.Errorf("lookup thread: %w", err)
			}
			if thread.CompanyID != in.CompanyID {
				return nil, fmt.Errorf("thread does not belong to company")
			}
			// Stricter than the dashboard's equivalent check, which only
			// tests the company. A key holder passing the thread id of a
			// dashboard conversation would otherwise append a machine turn to
			// a person's chat history and bill it under a channel it did not
			// arrive on — one company, but two surfaces that report
			// separately and are read separately.
			if thread.Channel != domain.ChannelAPI {
				return nil, fmt.Errorf("%w: thread was not started over the API", domain.ErrInvalidInput)
			}
			resolved = &ResolveResult{Thread: thread, IsNew: false}
		} else {
			resolved, err = s.threads.ResolveForAPIUser(ctx, in.CompanyID, in.APIUserRef, in.Message)
		}
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
		CompanyID:        in.CompanyID,
		ThreadID:         thread.ID,
		UserID:           in.UserID,
		PhoneNumber:      in.PhoneNumber,
		DiscordUserID:    in.DiscordUserID,
		DiscordChannelID: in.DiscordChannelID,
		LarkOpenID:       in.LarkOpenID,
		LarkChatID:       in.LarkChatID,
		LarkThreadKey:    in.LarkThreadKey,
		LarkMessageID:    in.LarkMessageID,
		Channel:          in.Channel,
		Message:          in.Message,
		Directive:        in.Directive,
		UserMsgID:        userMsg.ID,
		CompanyName:      companyName,
		DefaultCurrency:  currency,
		APIReportID:      in.APIReportID,
		APIKeyID:         in.APIKeyID,
		// Off the context rather than out of ChatInput: the request id is
		// ambient per-request identity, exactly like the company id, and a
		// field on the input would be one every caller has to remember to
		// fill. Empty for every non-HTTP caller, which is the truth.
		RequestID: tenantctx.RequestID(ctx),
	})
	if err != nil {
		return nil, fmt.Errorf("enqueue chat:run: %w", err)
	}

	out := &EnqueueResult{
		TaskID:      taskID,
		Thread:      thread,
		IsNewThread: resolved.IsNew,
		UserMsgID:   userMsg.ID,
	}
	if budget.Verdict == BudgetWarning {
		warning := budget
		out.BudgetWarning = &warning
	}
	return out, nil
}

// checkBudget returns the tenant's spend position, or ErrInsufficientCredits
// wrapped with the message the caller should show. Callers distinguish it
// with errors.Is and answer in whatever way their channel can.
func (s *ChatEnqueuer) checkBudget(ctx context.Context, companyID string) (BudgetState, error) {
	if s.budget == nil {
		return budgetOK, nil
	}
	st, err := s.budget.CheckBudget(ctx, companyID)
	if err != nil {
		return budgetOK, fmt.Errorf("check budget: %w", err)
	}
	if st.Blocked() {
		return st, fmt.Errorf("%w: %s", domain.ErrInsufficientCredits, CreditsExhaustedMessage)
	}
	return st, nil
}
