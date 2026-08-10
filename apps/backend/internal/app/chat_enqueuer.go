package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"

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
	roster    RosterReader
	bindings  ChannelBinder
}

// ChannelBinder resolves an inbound address to the agent an admin bound it to
// (T-S4). Declared at the consumer, like RosterReader and BudgetChecker, and
// read-only for the same reason: an enqueue path that could write a binding is
// one that could be asked to.
type ChannelBinder interface {
	// AgentForChannel returns domain.ErrNotFound for an unbound address, which
	// the caller reads as "the company default".
	AgentForChannel(ctx context.Context, companyID string, channel domain.Channel, externalID string) (string, error)
}

// RosterReader is the half of the roster the enqueue path needs: which agent
// runs when the thread names none (T-S2), and whether the agent a caller just
// named is one it may have (T-S3).
//
// Declared at the consumer, like BudgetChecker. Two read methods rather than
// domain.AgentRepository whole, because this path must not be able to write to
// the roster — an enqueue path that could edit an agent is one that could be
// asked to.
type RosterReader interface {
	GetDefault(ctx context.Context, companyID string) (*domain.Agent, error)
	// GetByID returns domain.ErrNotFound for another company's id as well as
	// for one that never existed. That sameness is the point: the caller is a
	// browser holding a bare uuid, and a distinguishable error is an existence
	// oracle across tenants.
	GetByID(ctx context.Context, companyID, id string) (*domain.Agent, error)
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

// WithRoster pins each enqueued turn to an agent (T-S2). Optional: without it
// every payload leaves with an empty AgentID and the worker resolves the
// company default itself, which is the behaviour of every turn queued before
// this ticket.
func (s *ChatEnqueuer) WithRoster(r RosterReader) *ChatEnqueuer {
	s.roster = r
	return s
}

// WithChannelBindings routes WhatsApp, Discord and Lark traffic by the address
// it arrived on (T-S4). Optional: without it every channel turn resolves to the
// company default, which is the behaviour of every turn before this ticket.
func (s *ChatEnqueuer) WithChannelBindings(b ChannelBinder) *ChatEnqueuer {
	s.bindings = b
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
	DiscordChannelID string // discord only; reply destination, and T-S4's binding key
	LarkOpenID       string // lark only; initiating user's open_id
	LarkChatID       string // lark only; chat the @mention came from
	LarkThreadKey    string // lark only; thread lookup key
	LarkMessageID    string // lark only; reply target (latest inbound message id)
	SlackTeamID      string // slack only; workspace the event arrived from
	SlackChannelID   string // slack only; reply destination, and T-S4's binding key
	SlackUserID      string // slack only; the human who wrote the message
	SlackMessageTS   string // slack only; this message's own ts
	SlackThreadTS    string // slack only; set when the message is already threaded
	APIUserRef       string // api only; the tenant's own reference for the end user
	// EmbedUserRef is widget only: the visitor the tenant's backend vouched
	// for, carried on the embed session token rather than sent by the browser
	// (T-20). A field the client could set would be a field the client could
	// change, which is the whole thing T-19's HMAC exists to prevent.
	EmbedUserRef string
	// EmbedKeyID names the embed key whose session started this turn. It is
	// the audit trail's answer to "which of our sites did this come from",
	// and the object an admin revokes when the answer is wrong.
	EmbedKeyID string
	Message    string
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
	// AgentID is the roster agent the *caller* picked for a new conversation
	// (T-S3), on the dashboard and on `/v1` (T-S5).
	//
	// The chat channels never fill it: nobody picks an agent from a Discord
	// message, so T-S4 resolves theirs from the address the message arrived on
	// (boundAgent) rather than from a field a webhook would have to invent.
	//
	// It applies to thread creation and nothing else. On an existing thread it
	// must either match what the thread already runs as or be absent —
	// changing an agent mid-conversation reinterprets history produced under
	// different tools and sources, which is a decision, not a field.
	AgentID  string
	ThreadID string // dashboard and api; if set, bypasses resolver
	// APIReportID ties this turn to the report job `POST /v1/reports` handed
	// back (T-A2). The worker marks that row terminal when the turn ends.
	APIReportID string
	// APIKeyID attributes the turn to the credential that started it, which is
	// what makes T-05's audit rows say "an integration did this" rather than
	// naming a person who was not there.
	APIKeyID string
}

// slackReplyTS is the thread the worker's reply hangs under: the thread the
// message already sits in, or — for a top-level mention — the message itself,
// which is what opens the thread. Empty for every other channel.
func slackReplyTS(in ChatInput) string {
	if in.SlackThreadTS != "" {
		return in.SlackThreadTS
	}
	return in.SlackMessageTS
}

// slackKey gathers the Slack routing fields into the shape ThreadService
// resolves and creates with. One place to assemble it, so the resolve path and
// the rebind fork cannot key the same conversation differently.
func (in ChatInput) slackKey() SlackThreadKey {
	return SlackThreadKey{
		CompanyID: in.CompanyID,
		TeamID:    in.SlackTeamID,
		ChannelID: in.SlackChannelID,
		UserID:    in.SlackUserID,
		MessageTS: in.SlackMessageTS,
		ThreadTS:  in.SlackThreadTS,
	}
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
	case domain.ChannelSlack:
		if in.SlackUserID == "" {
			return errors.New("slack_user_id required for slack channel")
		}
		if in.SlackChannelID == "" {
			return errors.New("slack_channel_id required for slack channel")
		}
		// The message's own ts, not thread_ts: a top-level mention has no
		// thread yet, and this is the ts our reply hangs the thread under.
		if in.SlackMessageTS == "" {
			return errors.New("slack_message_ts required for slack channel")
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
	case domain.ChannelWidget:
		// Stricter than the api arm above, which accepts either identity. A
		// widget turn always has an embed_user_ref because the session token
		// carries one and the middleware refuses a token without it — so an
		// empty ref here is not a caller who chose to pass a thread id instead,
		// it is a wiring bug. Every widget read is scoped by this value, and a
		// turn that is stored without one is a turn its own visitor cannot
		// read back.
		if in.EmbedUserRef == "" {
			return errors.New("embed_user_ref required for widget channel")
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
//
// agentID is the agent the user picked (T-S3), empty for the company default.
// It is validated before the row is written, so a rejected pick leaves no
// thread behind — the same ordering the budget check follows above.
func (s *ChatEnqueuer) CreateDashboardThread(ctx context.Context, companyID, userID, agentID string) (*domain.ConversationThread, error) {
	pinned, err := s.pickAgent(ctx, companyID, agentID)
	if err != nil {
		return nil, err
	}
	return s.threads.CreateDashboardThread(ctx, companyID, userID, "", pinned)
}

// ErrAgentNotFound is every refusal pickAgent can produce: unknown, another
// company's, and disabled, deliberately indistinguishable.
//
// It wraps domain.ErrNotFound so callers that only care about "this is a 404"
// keep mapping it without change. `/v1` needs more than that (T-S5): a thread
// that does not exist is also a 404 there, and the two answers name different
// request fields, so the public surface has to be able to tell them apart
// without string-matching an error message.
var ErrAgentNotFound = fmt.Errorf("%w: no such agent", domain.ErrNotFound)

// ErrAgentChange is a caller naming both a conversation and an agent that
// conversation does not run as.
//
// Refused rather than ignored (T-S3): silently dropping the pick would let a
// client believe it had switched agents while every turn kept running as the
// old one, and "the answer came from the wrong agent" is not a bug anyone finds
// by reading a reply. Exported for the same reason as ErrAgentNotFound — `/v1`
// names the offending request field, and `thread_id` and `agent_id` are
// different fields to go and fix.
var ErrAgentChange = fmt.Errorf("%w: a conversation cannot change agent", domain.ErrInvalidInput)

// pickAgent validates an agent the caller named and returns what to store on
// the thread. Empty in, empty out: "the company default", resolved per turn by
// agentFor rather than frozen into the row, so a company that moves its default
// moves every unpinned conversation with it.
//
// The three refusals are one error — ErrAgentNotFound — on purpose. Unknown,
// another company's, and disabled are distinguishable states to us and must not
// be to a caller: two of them confirm a row exists that it has no business
// knowing about.
//
// Disabled is checked **here and not at turn time**. A thread already bound to
// an agent keeps its narrower scope even after an admin disables it, because
// the alternative is a conversation that silently widens its own data access
// the moment someone tidies the roster. Disabling stops new picks, not running
// ones.
func (s *ChatEnqueuer) pickAgent(ctx context.Context, companyID, agentID string) (string, error) {
	if agentID == "" {
		return "", nil
	}
	if s.roster == nil {
		// No roster wired at all: the deployment predates T-S1 or is stripped
		// down. Refusing here would break a dashboard that offers a picker
		// against a build that has no agents, so the pick is dropped and the
		// turn runs exactly as it did before the roster existed.
		return "", nil
	}
	a, err := s.roster.GetByID(ctx, companyID, agentID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return "", ErrAgentNotFound
		}
		return "", fmt.Errorf("lookup agent: %w", err)
	}
	if !a.Enabled {
		return "", ErrAgentNotFound
	}
	return a.ID, nil
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
	case domain.ChannelWhatsApp, domain.ChannelDiscord, domain.ChannelLark:
		// One lookup for all three, here rather than in each inbound handler
		// (T-S4). The ticket named three call sites — the WhatsApp webhook, the
		// Lark webhook and Discord, which exists twice — and all four reach this
		// function, so binding them here makes the count one and a channel added
		// later inherits it rather than forgetting it.
		var bound string
		bound, err = s.boundAgent(ctx, in)
		if err != nil {
			return nil, err
		}
		resolved, err = s.resolveChannelThread(ctx, in, bound)
		if err == nil {
			resolved, err = s.rebindThread(ctx, in, resolved, bound)
		}
	case domain.ChannelAPI:
		// Validated before the thread is touched, exactly as the budget is and
		// as the dashboard's own pick is: a refused agent must leave no thread
		// and no orphan user message behind.
		pinned, perr := s.pickAgent(ctx, in.CompanyID, in.AgentID)
		if perr != nil {
			return nil, perr
		}
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
			// The dashboard's rule, on the surface where it matters more
			// (T-S5): a caller that names both a thread and an agent gets a
			// refusal rather than a silently ignored pick. Compared against
			// what the thread *runs as* rather than against the stored column,
			// so naming the company default explicitly on an unpinned
			// conversation is agreement and not a change.
			if pinned != "" && pinned != s.agentFor(ctx, thread) {
				return nil, ErrAgentChange
			}
			resolved = &ResolveResult{Thread: thread, IsNew: false}
		} else {
			resolved, err = s.threads.ResolveForAPIUser(ctx, in.CompanyID, in.APIUserRef, in.Message, pinned)
			if err == nil {
				resolved, err = s.forkForAgent(ctx, in, resolved, pinned)
			}
		}
	case domain.ChannelWidget:
		// The api arm's shape, with one difference that matters: the thread id
		// is never taken on trust from the caller. A widget client runs in a
		// browser on somebody else's page, so a thread id it sends is a thread
		// id a visitor can edit — the ownership comparison below is what makes
		// "this is my conversation" a fact rather than a claim.
		pinned, perr := s.pickAgent(ctx, in.CompanyID, in.AgentID)
		if perr != nil {
			return nil, perr
		}
		if in.ThreadID != "" {
			thread, terr := s.threads.GetByID(ctx, in.ThreadID)
			if terr != nil {
				return nil, fmt.Errorf("lookup thread: %w", terr)
			}
			// Three comparisons, and none is redundant: the company is the
			// tenant boundary, the channel stops a widget turn being appended
			// to a staff conversation, and the ref is the per-visitor boundary
			// that no other channel needs because no other channel hands the
			// thread id to an untrusted client.
			if thread.CompanyID != in.CompanyID ||
				thread.Channel != domain.ChannelWidget ||
				thread.EmbedUserRef != in.EmbedUserRef {
				return nil, fmt.Errorf("%w: no such conversation", domain.ErrNotFound)
			}
			if pinned != "" && pinned != s.agentFor(ctx, thread) {
				return nil, ErrAgentChange
			}
			resolved = &ResolveResult{Thread: thread, IsNew: false}
		} else {
			resolved, err = s.threads.ResolveForEmbedUser(ctx, in.CompanyID, in.EmbedUserRef, in.Message, pinned)
			if err == nil {
				resolved, err = s.forkForAgent(ctx, in, resolved, pinned)
			}
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
			// A pick that disagrees with the thread is refused rather than
			// ignored (T-S3). Silently dropping it would let a client believe
			// it switched agents while every turn kept running as the old one,
			// and "the answer came from the wrong agent" is not a bug anyone
			// finds by reading a reply.
			if in.AgentID != "" && in.AgentID != thread.AgentID {
				return nil, ErrAgentChange
			}
			resolved = &ResolveResult{Thread: thread, IsNew: false}
		} else {
			// Brand-new dashboard chat — create a fresh thread.
			pinned, err := s.pickAgent(ctx, in.CompanyID, in.AgentID)
			if err != nil {
				return nil, err
			}
			thread, err := s.threads.CreateDashboardThread(ctx, in.CompanyID, in.UserID, in.Message, pinned)
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
		SlackTeamID:      in.SlackTeamID,
		SlackChannelID:   in.SlackChannelID,
		SlackUserID:      in.SlackUserID,
		// The thread the reply hangs under: the message's own ts when it is
		// top-level, which is the same value the resolved thread stores.
		SlackThreadTS:   slackReplyTS(in),
		Channel:         in.Channel,
		Message:         in.Message,
		Directive:       in.Directive,
		AgentID:         s.agentFor(ctx, thread),
		UserMsgID:       userMsg.ID,
		CompanyName:     companyName,
		DefaultCurrency: currency,
		APIReportID:     in.APIReportID,
		APIKeyID:        in.APIKeyID,
		EmbedUserRef:    in.EmbedUserRef,
		EmbedKeyID:      in.EmbedKeyID,
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

// forkForAgent starts a new API conversation when the one the resolver picked
// runs as a different agent (T-S5).
//
// The `user_ref` door is the one place a caller names an agent without naming
// a thread, so the resolver can hand back a conversation that predates the
// pick. Three things could happen there and only one of them is defensible:
// ignore the pick and answer as the old agent — the "the answer came from the
// wrong agent" failure T-S3 refused to ship; refuse the call — which would
// break the caller that passes `agent_id` on every request the moment their
// first thread exists; or fork, which is what the resolver already does when
// the topic shifts. An agent change is a bigger discontinuity than a topic
// change, so it forks for the same reason.
//
// Compared through agentFor rather than against thread.AgentID, so a caller
// naming the company default on a conversation that was already running as the
// default is agreement rather than a fork on every turn.
func (s *ChatEnqueuer) forkForAgent(
	ctx context.Context, in ChatInput, resolved *ResolveResult, pinned string,
) (*ResolveResult, error) {
	if pinned == "" || resolved == nil || resolved.IsNew {
		return resolved, nil
	}
	if s.agentFor(ctx, resolved.Thread) == pinned {
		return resolved, nil
	}
	// The widget shares this function because it shares the situation exactly:
	// one `user_ref`-shaped door where a caller names an agent without naming a
	// thread. What differs is only which column the new conversation is keyed
	// by, and getting that wrong would fork a widget visitor into an `api`
	// thread they could then never read back.
	if in.Channel == domain.ChannelWidget {
		return s.threads.CreateEmbedThread(ctx, in.CompanyID, in.EmbedUserRef, in.Message, pinned)
	}
	return s.threads.CreateAPIThread(ctx, in.CompanyID, in.APIUserRef, in.Message, pinned)
}

// boundAgent asks the bindings which agent answers at this address (T-S4).
// Empty means unbound, which the rest of the path reads as "the company
// default" — the state every channel was in before this ticket.
//
// A lookup failure **stops the turn** rather than falling back, and that is the
// opposite of what agentFor does two functions below. The difference is what
// each failure costs: agentFor failing leaves the payload's agent empty and the
// worker resolves the same default itself, so nothing widens. Falling back here
// would answer a question asked in the finance room with an agent that can read
// every source the company has, on the strength of one failed query — a scope
// decision made by an outage. The lookup is on the same control database the
// thread resolve two lines later needs, so a real failure costs nothing extra.
func (s *ChatEnqueuer) boundAgent(ctx context.Context, in ChatInput) (string, error) {
	if s.bindings == nil {
		return "", nil
	}
	var ref string
	switch in.Channel {
	case domain.ChannelWhatsApp:
		ref = in.PhoneNumber
	case domain.ChannelDiscord:
		// The channel the message arrived in, not the user who wrote it: a
		// binding is a room configured for a job. Per-user bindings are a
		// follow-on and would be a second lookup, not a different one.
		ref = in.DiscordChannelID
	case domain.ChannelLark:
		ref = in.LarkChatID
	case domain.ChannelSlack:
		// The Slack channel, for the same reason Discord uses its channel: a
		// binding is a room configured for a job. A DM's channel id (D…) works
		// the same way and binds that one conversation.
		ref = in.SlackChannelID
	default:
		return "", nil
	}
	// Normalised through the same function the write path uses, or a number
	// stored as `+62…` never matches inbound `whatsapp:+62…`.
	ref = domain.NormalizeChannelRef(in.Channel, ref)
	if ref == "" {
		return "", nil
	}
	agentID, err := s.bindings.AgentForChannel(ctx, in.CompanyID, in.Channel, ref)
	if errors.Is(err, domain.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("resolve channel binding: %w", err)
	}
	return agentID, nil
}

// resolveChannelThread picks the conversation for an inbound chat message,
// passing the binding through so a thread created now is pinned to it.
func (s *ChatEnqueuer) resolveChannelThread(
	ctx context.Context, in ChatInput, bound string,
) (*ResolveResult, error) {
	switch in.Channel {
	case domain.ChannelWhatsApp:
		return s.threads.ResolveForPhone(ctx, in.CompanyID, in.PhoneNumber, in.Message, bound)
	case domain.ChannelDiscord:
		return s.threads.ResolveForDiscordUser(ctx, in.CompanyID, in.DiscordUserID, in.Message, bound)
	case domain.ChannelLark:
		return s.threads.ResolveForLark(ctx, in.CompanyID, in.LarkChatID, in.LarkThreadKey,
			in.LarkOpenID, in.Message, bound)
	case domain.ChannelSlack:
		return s.threads.ResolveForSlack(ctx, in.slackKey(), in.Message, bound)
	}
	return nil, fmt.Errorf("%w: %q is not an inbound chat channel", domain.ErrInvalidInput, in.Channel)
}

// rebindThread forks the conversation when the address's binding disagrees with
// the agent the resolved thread runs as (T-S4).
//
// Discord is why this exists. A thread is keyed by (company, discord user), so
// one person asking in #ops and then in #finance is *one* thread — and without
// this, the second question would be answered by whichever agent the first one
// pinned, with the first agent's answers still in the memory the second reads.
// Continuing is the "the answer came from the wrong agent" failure T-S3 refused
// to ship, plus two scopes' history in one conversation.
//
// The comparison is against agentFor rather than against thread.AgentID, and
// against the *default* when the address is unbound: an unbound room means the
// company default, not "no opinion", so a thread pinned by a binding that has
// since been removed comes back to the default on its next message instead of
// keeping a scope nobody configured any more. Both sides resolve NULL the same
// way, so the ordinary unbound company forks nothing, ever.
func (s *ChatEnqueuer) rebindThread(
	ctx context.Context, in ChatInput, resolved *ResolveResult, bound string,
) (*ResolveResult, error) {
	if resolved == nil || resolved.IsNew {
		return resolved, nil
	}
	want := bound
	if want == "" {
		want = s.defaultAgent(ctx, resolved.Thread.CompanyID)
	}
	if s.agentFor(ctx, resolved.Thread) == want {
		return resolved, nil
	}
	logrus.WithFields(logrus.Fields{
		"company_id": in.CompanyID, "channel": in.Channel,
		"thread_id": resolved.Thread.ID, "agent_id": want,
	}).Info("channel binding changed the agent; forking the conversation")
	return s.threads.CreateChannelThread(ctx, ChannelThreadInput{
		CompanyID:     in.CompanyID,
		Channel:       in.Channel,
		PhoneNumber:   in.PhoneNumber,
		DiscordUserID: in.DiscordUserID,
		LarkChatID:    in.LarkChatID,
		LarkThreadKey: in.LarkThreadKey,
		LarkOpenID:    in.LarkOpenID,
		Slack:         in.slackKey(),
		FirstMessage:  in.Message,
		AgentID:       want,
	})
}

// agentFor decides which agent this turn runs as: the thread's own, else the
// company default (T-S2).
//
// Never an error. A roster lookup that fails leaves the field empty, the
// worker resolves the default itself, and the turn runs — the alternative is a
// tenant who cannot ask a question because a table nobody has read in six
// months is unavailable. The failure is logged, not returned.
func (s *ChatEnqueuer) agentFor(ctx context.Context, thread *domain.ConversationThread) string {
	if thread != nil && thread.AgentID != "" {
		return thread.AgentID
	}
	if thread == nil {
		return ""
	}
	return s.defaultAgent(ctx, thread.CompanyID)
}

// defaultAgent is the company's default, or empty when there is no roster or
// the lookup failed. Split out of agentFor because T-S4 asks the same question
// about a company rather than about a thread — an unbound channel address means
// this agent, and there is no thread involved in saying so.
func (s *ChatEnqueuer) defaultAgent(ctx context.Context, companyID string) string {
	if s.roster == nil || companyID == "" {
		return ""
	}
	def, err := s.roster.GetDefault(ctx, companyID)
	if err != nil {
		// ErrNotFound is ordinary: a company whose roster was never seeded.
		// Everything else is worth a line, at the level that distinguishes them.
		if !errors.Is(err, domain.ErrNotFound) {
			logrus.WithError(err).WithField("company_id", companyID).
				Warn("default agent lookup failed; the worker will resolve it")
		}
		return ""
	}
	return def.ID
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
