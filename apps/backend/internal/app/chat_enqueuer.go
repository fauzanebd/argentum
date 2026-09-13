package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/authz"
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
	companies CompanyReader
	enqueuer  ChatRunEnqueuer
	budget    BudgetChecker
	roster    RosterReader
	bindings  ChannelBinder
	// room is T-N3's addressing. Nil disables it entirely: every message then
	// goes to the thread's agent, which is what every message did before this
	// ticket.
	room RoomReader
	// access and agents are T-Z4's: whether the person behind a dashboard turn
	// may talk to the agent it would run as, and the roster in order for when
	// the company default is one they may not. Since T-Z8 they decide the widget
	// and the chat channels too, which ask as nobody. Nil access disables the
	// check on those doors, which is every turn before roadmap 12; `/v1`'s key
	// allowlist needs neither.
	access AgentAccess
	agents AgentLister
	// conversations refuses a send into a conversation the person may not read
	// (T-Z10). Nil admits every conversation, as before.
	conversations ConversationReader
}

// ConversationReader is whether a person may read one conversation (T-Z10).
// *ConversationAccess is the production one.
type ConversationReader interface {
	MayRead(ctx context.Context, companyID, userID, threadID string) (bool, error)
}

// CompanyReader is the one read this service makes of the company: the display
// name and default currency a turn's context carries.
//
// Declared at the consumer and narrowed to one method, like ChatRunEnqueuer
// below and RosterReader above. domain.CompanyRepository can create and update
// companies; an enqueue path that can reach those is one that could be asked
// to, and it is also one no test can build without implementing five methods it
// never calls.
type CompanyReader interface {
	GetByID(ctx context.Context, id string) (*domain.Company, error)
}

// ChatRunEnqueuer is the one thing this service asks of the queue.
//
// Declared at the consumer, like RosterReader, BudgetChecker and RoomReader,
// and narrowed to one method for the reason those give — plus one specific to
// T-N3: a message that addresses three agents produces three payloads, and
// "one user message became N turns with one UserMsgID" is the ticket's central
// claim. With a concrete *queue.Enqueuer that claim was only checkable against
// a live Redis, which is why nothing checked it.
//
// *queue.Enqueuer satisfies this; nothing about the wiring changes.
type ChatRunEnqueuer interface {
	EnqueueChatRun(ctx context.Context, p queue.ChatRunPayload) (string, error)
}

// RoomReader is the membership half of a conversation the enqueue path needs
// (T-N3): who is in this room, and what the company's roster is called.
//
// Declared at the consumer, like RosterReader and BudgetChecker, and read-only
// for the same reason: an enqueue path that could write a participant is one
// that could be asked to.
//
// Two methods rather than the repository whole, and the second one is the odd
// one. Addressing resolves `@Finance` against the *room*; the roster listing
// exists only to tell apart the two ways that can fail — a name nobody has
// (which is ordinary text) from a name the company does have but has not put in
// this conversation (which is a refusal). Without it both look like text, and a
// user who addressed a real agent silently gets the default speaker instead.
type RoomReader interface {
	ListParticipants(ctx context.Context, companyID, threadID string) ([]*domain.ThreadParticipant, error)
	ListAgents(ctx context.Context, companyID string) ([]*domain.Agent, error)
}

// ErrAgentNotInRoom is an `@` naming an agent the company has and this
// conversation does not.
//
// It refuses the whole message and enqueues nothing. The two alternatives are
// both worse: answering as the default speaker is the "the answer came from the
// wrong agent" failure T-S3 refused to ship, and silently adding the agent to
// the room is a message that quietly enlarges a conversation and quietly spends
// more money.
var ErrAgentNotInRoom = errors.New("that agent is not in this conversation")

// AgentAccess is what the enqueue path asks about a person and an agent (T-Z4):
// may they talk to it, for one agent and for several at once.
// *authz.Authorizer is the production one — the decision is internal/authz's,
// all of it (roadmap 12, decision 1), and nothing here composes an answer.
//
// Declared at the consumer, like RosterReader, and read-only for its reason.
type AgentAccess interface {
	Decide(ctx context.Context, s authz.Subject, kind domain.ResourceKind, id string) (authz.Decision, error)
	Visible(ctx context.Context, s authz.Subject, kind domain.ResourceKind, ids []string) ([]string, error)
}

// AgentLister is the roster in the order the dashboard lists it, default first.
// A restricted default needs it and nothing else does: the caller's default is
// then the first enabled agent they may use.
type AgentLister interface {
	ListByCompany(ctx context.Context, companyID string) ([]*domain.Agent, error)
}

// ErrAgentRestricted is a person asking an agent they know about — the one their
// conversation already runs as, or one in the room with them — that an admin has
// restricted and not granted them.
//
// It **says why**, unlike ErrAgentNotFound. A pick from the picker is refused as
// not found, because the picker never offered it (T-Z4: a picker is a list of
// things you can do, so hiding beats disabling there). But an agent already on
// the screen is not a secret, and "no such agent" about the one whose answers
// sit above the composer would be a lie that sends the user hunting for a bug.
var ErrAgentRestricted = errors.New("an admin has restricted this agent and has not granted it to you")

// ErrNoAgentAvailable is a new conversation with nothing to run as: the company
// default is restricted from this person, and so is every other agent.
var ErrNoAgentAvailable = errors.New("no agent in this workspace is open to you; ask an admin for access")

// ErrAccessCheckFailed is an access read that failed. Refused, never admitted —
// and never reported as a refusal either, because the caller may well hold the
// grant and should try again rather than go and ask for one.
var ErrAccessCheckFailed = errors.New("could not check access to that agent; try again")

// ErrAgentNotClearedHere is a turn on a door with no person — a chat channel or
// the website widget — that would run as an agent an admin has restricted, and
// that nobody has cleared that door to reach (T-Z8). A channel is cleared by an
// admin acknowledging its binding (roadmap 12, decision 8); the widget never is
// (decision 10).
//
// Nobody on those doors can be granted anything, so unlike ErrAgentRestricted it
// does not tell the reader to go and ask for a grant. It says what happened.
var ErrAgentNotClearedHere = errors.New("the agent that answers here is restricted, and this conversation has not been cleared to use it")

// AgentNotClearedMessage is ErrAgentNotClearedHere as a chat channel says it
// back to the person who asked, in the shape CreditsExhaustedMessage has.
const AgentNotClearedMessage = "The agent that answers here has been restricted by an admin, and this chat has not been cleared to use it. Ask whoever runs Argentum for your team."

// ErrAgentNotAllowed is a `/v1` turn that would run as an agent outside its
// key's allowlist (T-Z8, decision 9): a conversation already running as one, or
// the company default when the call named no agent. A *pick* outside the list is
// ErrAgentNotFound instead, for pickAgent's reason — this one is about an agent
// the caller did not name and therefore already knows nothing new about.
var ErrAgentNotAllowed = errors.New("this API key may not use the agent this conversation runs as")

// SpokenRefusal is the sentence a chat channel says back when Enqueue refused a
// turn for a reason the person who asked should hear, rather than a failure the
// platform should retry. The four inbound chat handlers share it, so a refusal
// added here is spoken on all four instead of being a 500 on the ones nobody
// remembered — which is how Slack's arm went missing above.
func SpokenRefusal(err error) (string, bool) {
	switch {
	case errors.Is(err, domain.ErrInsufficientCredits):
		return CreditsExhaustedMessage, true
	case errors.Is(err, ErrAgentNotClearedHere):
		return AgentNotClearedMessage, true
	}
	return "", false
}

// ChannelBinder resolves an inbound address to the agent an admin bound it to
// (T-S4). Declared at the consumer, like RosterReader and BudgetChecker, and
// read-only for the same reason: an enqueue path that could write a binding is
// one that could be asked to.
type ChannelBinder interface {
	// AgentForChannel returns domain.ErrNotFound for an unbound address, which
	// the caller reads as "the company default". The route carries whether an
	// admin acknowledged the address for a restricted agent (T-Z8).
	AgentForChannel(ctx context.Context, companyID string, channel domain.Channel, externalID string) (domain.ChannelRoute, error)
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
func NewChatEnqueuer(threads *ThreadService, messages domain.MessageRepository, companies CompanyReader, enqueuer ChatRunEnqueuer) *ChatEnqueuer {
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

// WithRoom enables `@agent` addressing (T-N3). Optional, in the shape
// WithRoster and WithChannelBindings use: without it every message resolves to
// the thread's agent exactly as it did before this ticket, and the `@` is text.
func (s *ChatEnqueuer) WithRoom(r RoomReader) *ChatEnqueuer {
	s.room = r
	return s
}

// WithAgentAccess makes grants decide which agents a turn may run as (T-Z4,
// T-Z8). Optional in the shape of the three above: without it every door but
// `/v1`'s reaches every agent, which is what every turn did before roadmap 12.
//
// **A grant is a property of an Argentum user** (roadmap 12, decision 7), and
// only the dashboard carries one: `/v1` has a key, the widget has a visitor, and
// a channel has a platform identity with no user row behind it. So the other
// doors get a rule of their own rather than an inherited one (door, below): the
// widget and the channels ask as nobody, who holds no grant, and reach an open
// agent — plus, on a channel an admin acknowledged, the one agent its binding
// names. `/v1` asks nothing here; its key carries an allowlist of its own.
func (s *ChatEnqueuer) WithAgentAccess(access AgentAccess, agents AgentLister) *ChatEnqueuer {
	s.access = access
	s.agents = agents
	return s
}

// WithConversationAccess refuses a dashboard send into a conversation the person
// may not read (T-Z10).
//
// T-Z4's turnTargets already refuses a turn that would *run* as a restricted
// agent. This covers the case it cannot see: a conversation that runs as an open
// agent but holds a restricted one's answers — in the room, or said before it
// left. A turn there replays those answers into the model's memory, and "what
// did HR say above?" would read them back to somebody the thread list hides
// them from.
func (s *ChatEnqueuer) WithConversationAccess(r ConversationReader) *ChatEnqueuer {
	s.conversations = r
	return s
}

// personOf is the Argentum user behind a turn, or "" when there is none. Read
// off the channel rather than trusted from UserID alone: the field is commented
// "dashboard only", and a door that one day fills it for attribution must not
// thereby start enforcing grants it has not decided about.
func personOf(in ChatInput) string {
	if in.Channel != domain.ChannelDashboard {
		return ""
	}
	return in.UserID
}

// door is who a turn's agent is checked against (T-Z8).
//
// Roadmap 12's decision 7 gives a grant to an Argentum user and nobody else, and
// decisions 8–10 are what each other way in does instead. This is where those
// decisions meet the enqueue path: one value, built by the arm of Enqueue that
// knows which door it is, and read by the three checks — pickAgent, openingAgent
// and turnTargets — that until T-Z8 read a user id and asked nothing without one.
// "A door nobody decided about is a door that is open" (§2), and a user id alone
// could only say "decided" for one door of four.
type door struct {
	// grants says internal/authz decides, for userID. False on `/v1`, whose key
	// carries an allowlist instead (decision 9), and on every door when no
	// access is wired.
	grants bool
	// userID is the person on the dashboard, and "" on the widget and the chat
	// channels: nobody, who holds no grant, so an open agent is reachable and a
	// restricted one is not.
	userID string
	// cleared is the restricted agent a channel may reach anyway: the one its
	// binding names, once an admin acknowledged that anyone who can post on the
	// address can use it (decision 8). Empty everywhere else.
	cleared string
	// keyAgents is `/v1`'s allowlist, empty for every agent.
	keyAgents []string
}

// doorFor builds the door for a turn. route is the channel binding the address
// resolved to, and empty for every channel that has none.
//
// With no access wired, the dashboard, the widget and the channels ask nothing,
// exactly as before roadmap 12. `/v1`'s allowlist needs no wiring: it arrived on
// the key.
func (s *ChatEnqueuer) doorFor(in ChatInput, route domain.ChannelRoute) door {
	switch in.Channel {
	case domain.ChannelAPI:
		return door{keyAgents: in.KeyAgentIDs}
	case domain.ChannelDashboard:
		return s.personDoor(in.UserID)
	case domain.ChannelWidget:
		// Decision 10, and the one door where refusing is right: the person on
		// the other end is the tenant's customer, and no admin acknowledgement
		// changes that. Nothing is ever cleared here.
		return door{grants: s.access != nil}
	default:
		d := door{grants: s.access != nil}
		if route.Acknowledged {
			d.cleared = route.AgentID
		}
		return d
	}
}

// personDoor is the dashboard's door for one person. An empty user id asks
// nothing, as T-Z4 did: the dashboard validates that one is present, so the case
// is a caller outside Enqueue that has no person to name.
func (s *ChatEnqueuer) personDoor(userID string) door {
	return door{grants: s.access != nil && userID != "", userID: userID}
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
	// KeyAgentIDs is that key's agent allowlist (T-Z8), empty for every agent.
	// Api only. Carried rather than looked up, because APIKeyAuth read it off
	// the key on the query that authenticated the request.
	KeyAgentIDs []string
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
	// TaskID is the first task of the turn. It stays a single value because
	// every caller reads it as "the turn started"; the full list is TaskIDs.
	TaskID string
	// TaskIDs is one id per addressed agent (T-N3). Length one for every
	// message that addresses nobody, which is every message on every channel
	// but the dashboard and most of those.
	TaskIDs []string
	// AddressedAgentIDs is who this message was routed to, or nil when it
	// addressed nobody and the thread's own agent answered.
	AddressedAgentIDs []string
	Thread            *domain.ConversationThread
	IsNewThread       bool
	UserMsgID         string
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
	pinned, err := s.dashboardAgent(ctx, companyID, s.personDoor(userID), agentID)
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
//
// **Restricted is a fourth refusal with the same face (T-Z4)**, and unlike
// disabled it is checked at turn time too (turnTargets): a grant is about a
// person, and a person who lost one must not keep talking to the agent because
// their conversation predates the revoke.
//
// d says who is asking (T-Z8): a person on the dashboard; nobody on the widget,
// who holds no grant, so a restricted agent is refused there too; and on `/v1` a
// key, whose allowlist is checked instead of any grant. A pick outside that list
// is the same not-found, because a caller that may not use an agent has no
// business learning which ids are agents.
func (s *ChatEnqueuer) pickAgent(ctx context.Context, companyID string, d door, agentID string) (string, error) {
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
	if d.grants {
		dec, err := s.access.Decide(ctx, authz.Subject{CompanyID: companyID, UserID: d.userID}, domain.ResourceKindAgent, a.ID)
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrAccessCheckFailed, err)
		}
		if !dec.Allowed {
			return "", ErrAgentNotFound
		}
	}
	if !domain.KeyAllowsAgent(d.keyAgents, a.ID) {
		return "", ErrAgentNotFound
	}
	return a.ID, nil
}

// dashboardAgent is what a new dashboard conversation is opened on: the agent
// the person picked, or — when they picked none — the agent their default
// resolves to. Both halves are checked before the thread is written, so a
// refused conversation leaves no thread behind.
func (s *ChatEnqueuer) dashboardAgent(ctx context.Context, companyID string, d door, agentID string) (string, error) {
	pinned, err := s.pickAgent(ctx, companyID, d, agentID)
	if err != nil || pinned != "" {
		return pinned, err
	}
	return s.openingAgent(ctx, companyID, d)
}

// openingAgent decides what a new conversation with no pick runs as, for one
// person (T-Z4): "" — the company default, resolved per turn, today's behaviour
// — when they may talk to the default; otherwise the first enabled agent they
// may talk to, **pinned** onto the thread.
//
// "A restricted agent's default is not a default", and the fall-through is pinned
// rather than re-resolved because turnTargets refuses an unpinned conversation
// whose default the person may not reach — which is what a conversation that
// already ran as that default must do. Leaving this one unpinned would have it
// refused on its own second message.
//
// Every read that fails refuses: a default that could not be looked up is a
// default that could not be checked, and before this ticket the worker resolved
// it unchecked.
//
// The widget opens by the same rule for nobody (T-Z8): a restricted default is
// not a website visitor's default, and the first open agent is.
func (s *ChatEnqueuer) openingAgent(ctx context.Context, companyID string, d door) (string, error) {
	if !d.grants || s.roster == nil {
		return "", nil
	}
	userID := d.userID
	def, err := s.roster.GetDefault(ctx, companyID)
	if errors.Is(err, domain.ErrNotFound) {
		// No roster at all: nothing exists to be restricted.
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("%w: default agent: %w", ErrAccessCheckFailed, err)
	}
	subject := authz.Subject{CompanyID: companyID, UserID: userID}
	dec, err := s.access.Decide(ctx, subject, domain.ResourceKindAgent, def.ID)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrAccessCheckFailed, err)
	}
	if dec.Allowed {
		return "", nil
	}
	if s.agents == nil {
		return "", ErrNoAgentAvailable
	}
	roster, err := s.agents.ListByCompany(ctx, companyID)
	if err != nil {
		return "", fmt.Errorf("%w: roster: %w", ErrAccessCheckFailed, err)
	}
	ids := make([]string, 0, len(roster))
	for _, a := range roster {
		if a != nil && a.Enabled {
			ids = append(ids, a.ID)
		}
	}
	// One load for the whole roster, in roster order — Visible keeps the order
	// it was given, which is what makes "first" mean the dashboard's first.
	open, err := s.access.Visible(ctx, subject, domain.ResourceKindAgent, ids)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrAccessCheckFailed, err)
	}
	if len(open) == 0 {
		return "", ErrNoAgentAvailable
	}
	return open[0], nil
}

// turnTargets is who runs this turn, and the check that the person asking may
// talk to every one of them (T-Z4). Called after addressing and before the user
// message is written, so a refused turn leaves no orphan row.
//
// This is the seam that covers three of the ticket's five at once — the thread's
// own agent (rehydration), the company default an unpinned thread resolves to,
// and every agent an `@` addressed — because all three end up here as the list
// that is about to be enqueued. Checking the list rather than each path that
// produced it is what keeps a sixth path, added later, from being the one that
// forgot.
//
// An agent the grant store does not find is let through, exactly as
// RequireResource lets a missing object through to its handler: a thread pinned
// to a deleted agent behaved some way before this ticket, and "restricted" would
// be the wrong sentence for it.
//
// d decides what "may" means for this door (T-Z8): a person's grants; nobody's
// on the widget and the channels, less the one agent a channel's acknowledged
// binding clears; or, on `/v1`, the key's allowlist and no grant at all.
func (s *ChatEnqueuer) turnTargets(
	ctx context.Context, in ChatInput, thread *domain.ConversationThread, addressed []string, d door,
) ([]string, error) {
	if !d.grants && len(d.keyAgents) == 0 {
		if len(addressed) > 0 {
			return addressed, nil
		}
		return []string{s.agentFor(ctx, thread)}, nil
	}

	targets := addressed
	if len(targets) == 0 {
		agent, err := s.threadAgent(ctx, thread)
		if err != nil {
			return nil, err
		}
		if agent == "" {
			if len(d.keyAgents) > 0 {
				// A key limited to named agents, in a company whose default could
				// not be named: the worker would run unscoped, and unscoped is no
				// agent on the key's list.
				return nil, ErrAgentNotAllowed
			}
			// A company with no roster: the worker runs unscoped, and there is
			// no agent to have restricted.
			return []string{""}, nil
		}
		targets = []string{agent}
	}

	if len(d.keyAgents) > 0 {
		// Decision 9: a key's own list, and no grant. No read — the list rode in
		// on the key.
		for _, id := range targets {
			if !domain.KeyAllowsAgent(d.keyAgents, id) {
				return nil, ErrAgentNotAllowed
			}
		}
		return targets, nil
	}

	// The agent a channel's acknowledgement clears is not asked about: whatever
	// its mode, this address may reach it (decision 8). Asking anyway would cost
	// a read to learn nothing the answer could change.
	asked := make([]string, 0, len(targets))
	for _, id := range targets {
		if d.cleared == "" || !strings.EqualFold(id, d.cleared) {
			asked = append(asked, id)
		}
	}
	if len(asked) == 0 {
		return targets, nil
	}

	subject := authz.Subject{CompanyID: in.CompanyID, UserID: d.userID}
	open, err := s.access.Visible(ctx, subject, domain.ResourceKindAgent, asked)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAccessCheckFailed, err)
	}
	allowed := make(map[string]bool, len(open))
	for _, id := range open {
		allowed[id] = true
	}
	for _, id := range asked {
		if allowed[id] {
			continue
		}
		// Only on the refusal path, so the ordinary turn stays one load.
		dec, err := s.access.Decide(ctx, subject, domain.ResourceKindAgent, id)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrAccessCheckFailed, err)
		}
		if dec.Reason == authz.ReasonNotFound {
			continue
		}
		return nil, s.closedTo(ctx, d, in.CompanyID, id)
	}
	return targets, nil
}

// closedTo is the refusal for an agent a door may not reach, worded for the one
// who reads it (T-Z8). A person on the dashboard is told the agent's name and to
// ask an admin; nobody on a channel or the widget is told the agent is
// restricted here, because nobody there can be granted anything.
func (s *ChatEnqueuer) closedTo(ctx context.Context, d door, companyID, agentID string) error {
	if d.userID != "" {
		return s.restrictedAgent(ctx, companyID, agentID)
	}
	return ErrAgentNotClearedHere
}

// threadAgent is agentFor with its failure surfaced rather than swallowed.
// agentFor may leave the agent empty on a failed lookup because the worker then
// resolves the same default itself; with a grant to check, that default would
// run unchecked, so here the failure refuses the turn.
func (s *ChatEnqueuer) threadAgent(ctx context.Context, thread *domain.ConversationThread) (string, error) {
	if thread.AgentID != "" {
		return thread.AgentID, nil
	}
	if s.roster == nil {
		return "", nil
	}
	def, err := s.roster.GetDefault(ctx, thread.CompanyID)
	if errors.Is(err, domain.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("%w: default agent: %w", ErrAccessCheckFailed, err)
	}
	return def.ID, nil
}

// restrictedAgent is ErrAgentRestricted with the agent's name on it, because
// "this agent" is not actionable when a room holds four.
func (s *ChatEnqueuer) restrictedAgent(ctx context.Context, companyID, agentID string) error {
	if s.roster != nil {
		if a, err := s.roster.GetByID(ctx, companyID, agentID); err == nil && a.Name != "" {
			return fmt.Errorf("%w: %s", ErrAgentRestricted, a.Name)
		}
	}
	return ErrAgentRestricted
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
	// gate is who this turn's agent is checked against (T-Z8). Each arm sets it,
	// because each door answers that question differently.
	var gate door

	switch in.Channel {
	case domain.ChannelWhatsApp, domain.ChannelDiscord, domain.ChannelLark, domain.ChannelSlack:
		// One lookup for all four, here rather than in each inbound handler
		// (T-S4). The ticket named three call sites — the WhatsApp webhook, the
		// Lark webhook and Discord, which exists twice — and all of them reach
		// this function, so binding them here makes the count one and a channel
		// added later inherits it rather than forgetting it. **Slack forgot it
		// anyway:** it arrived after this case line and every other Slack arm on
		// the path was written but this one, so until T-Z8 each Slack message
		// fell to the default below as an unknown channel.
		var route domain.ChannelRoute
		route, err = s.boundAgent(ctx, in)
		if err != nil {
			return nil, err
		}
		// Nobody is asking, so a restricted agent answers here only when it is
		// the one this address is bound to and an admin acknowledged the address
		// for it (decision 8). An unbound address runs as the company default,
		// and a restricted default is refused rather than swapped for another
		// agent: a binding is how an admin says who answers in a room.
		gate = s.doorFor(in, route)
		resolved, err = s.resolveChannelThread(ctx, in, route.AgentID)
		if err == nil {
			resolved, err = s.rebindThread(ctx, in, resolved, route.AgentID)
		}
	case domain.ChannelAPI:
		// Validated before the thread is touched, exactly as the budget is and
		// as the dashboard's own pick is: a refused agent must leave no thread
		// and no orphan user message behind.
		//
		// No person: a key is a machine, and what it may reach is its own agent
		// allowlist, not a user's grants (decision 9, T-Z8). A pick outside it is
		// refused here; a conversation already running as an agent outside it —
		// or a call naming none whose default is outside it — by turnTargets.
		gate = s.doorFor(in, domain.ChannelRoute{})
		pinned, perr := s.pickAgent(ctx, in.CompanyID, gate, in.AgentID)
		if perr != nil {
			return nil, perr
		}
		if pinned == "" && in.ThreadID == "" && len(gate.keyAgents) > 0 &&
			!domain.KeyAllowsAgent(gate.keyAgents, s.defaultAgent(ctx, in.CompanyID)) {
			// A key limited to named agents, a call naming none, and a default
			// that is not on the list: refused here, before a conversation is
			// opened for it, so a refused call leaves no thread behind. Judged by
			// the default rather than by whichever conversation `user_ref` would
			// continue, so the answer does not depend on history the caller cannot
			// see — a key like this names its agent. A default that could not be
			// read is not on any list.
			return nil, ErrAgentNotAllowed
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
		//
		// No person either: a visitor has no account, and the widget never
		// reaches a restricted agent (decision 10, T-Z8). The roadmap said an
		// embed key would refuse one at save time, but an embed key names no
		// agent — the visitor's browser picks — so the refusal is here: a pick of
		// a restricted agent is not found, and a conversation that would run as
		// one is refused by turnTargets.
		gate = s.doorFor(in, domain.ChannelRoute{})
		pinned, perr := s.pickAgent(ctx, in.CompanyID, gate, in.AgentID)
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
			// No pick: the dashboard's opening rule (T-Z4), for nobody. A
			// restricted company default is not a visitor's default and the first
			// open agent is, pinned — but only a conversation that would run as
			// that default moves onto it. One pinned to an open agent stays put.
			fallback := ""
			if pinned == "" {
				fallback, perr = s.openingAgent(ctx, in.CompanyID, gate)
				if perr != nil {
					return nil, perr
				}
			}
			opening := pinned
			if opening == "" {
				opening = fallback
			}
			resolved, err = s.threads.ResolveForEmbedUser(ctx, in.CompanyID, in.EmbedUserRef, in.Message, opening)
			if err == nil {
				forkTo := pinned
				if fallback != "" && resolved != nil && !resolved.IsNew && resolved.Thread.AgentID == "" {
					forkTo = fallback
				}
				resolved, err = s.forkForAgent(ctx, in, resolved, forkTo)
			}
		}
	case domain.ChannelDashboard:
		gate = s.doorFor(in, domain.ChannelRoute{})
		if in.ThreadID != "" {
			// Explicit thread selected by the user — bypass resolver.
			thread, err := s.threads.GetByID(ctx, in.ThreadID)
			if err != nil {
				return nil, fmt.Errorf("lookup thread: %w", err)
			}
			if thread.CompanyID != in.CompanyID {
				return nil, fmt.Errorf("thread does not belong to company")
			}
			// A conversation this person may not read is one they may not
			// write into either (T-Z10), answered as a missing one.
			if s.conversations != nil {
				readable, rerr := s.conversations.MayRead(ctx, in.CompanyID, in.UserID, thread.ID)
				if rerr != nil {
					return nil, rerr
				}
				if !readable {
					return nil, ErrConversationNotFound
				}
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
			// Brand-new dashboard chat — create a fresh thread, on the agent
			// this person picked or the one their default resolves to (T-Z4).
			pinned, err := s.dashboardAgent(ctx, in.CompanyID, gate, in.AgentID)
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

	// Who this message is for (T-N3). Before the user message is written, so a
	// refused `@` leaves no orphan row behind — the ordering the budget check
	// and the agent pick both already follow.
	addr, err := s.resolveAddressing(ctx, in, thread)
	if err != nil {
		return nil, err
	}
	// Who runs this turn, and whether the person asking may talk to each of
	// them (T-Z4) — before the user message for the same reason.
	targets, err := s.turnTargets(ctx, in, thread, addr.AgentIDs, gate)
	if err != nil {
		return nil, err
	}

	// **The original text, not the cleaned one.** A transcript should read as
	// what the person typed; the `@` tokens are stripped only from what the
	// model sees, which is lark.StripMentions' arrangement and its reason.
	userMsg, err := s.threads.AppendUserMessage(ctx, thread.ID, in.Message)
	if err != nil {
		return nil, fmt.Errorf("append user message: %w", err)
	}

	// Look up company display name and default currency for agent context.
	var companyName, currency string
	// And the convention that currency is written under (T-W2) — off the same
	// row, so the worker needs no second read to know whether a rupiah figure
	// has decimal places.
	var money domain.Currency
	if company, err := s.companies.GetByID(ctx, in.CompanyID); err == nil {
		companyName = company.Name
		currency = company.DefaultCurrency
		money = company.Currency()
	}

	// One user message, N turns (T-N3). Every payload carries the same
	// UserMsgID, which is what ChatEvent.JobID is — so N concurrent turns
	// publish under one job id and are told apart by the agent id T-N1 put on
	// every event.
	//
	// Order is the order the agents were addressed. asynq gives no ordering
	// guarantee across workers and this does not add one: the transcript is
	// ordered by messages.created_at, which is Postgres's clock, and that is
	// the order a reader sees. Do not build a sequencer for this.
	addressed := addr.AgentIDs

	var taskIDs []string
	var taskID string
	for _, target := range targets {
		id, ferr := s.enqueuer.EnqueueChatRun(ctx, queue.ChatRunPayload{
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
			SlackThreadTS: slackReplyTS(in),
			Channel:       in.Channel,
			// The cleaned text: the `@` tokens are routing metadata, and leaving
			// them in the prompt teaches the model that `@` is something it should
			// produce (lark/mention.go:28-33).
			Message:         addr.Cleaned,
			Directive:       in.Directive,
			AgentID:         target,
			UserMsgID:       userMsg.ID,
			CompanyName:     companyName,
			DefaultCurrency: currency,
			Money:           money,
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
		if ferr != nil {
			// The first failure ends the fan-out. Turns already queued keep
			// running and their answers still arrive — they are real turns the
			// user asked for, and cancelling them is neither possible nor
			// desirable. What the caller is told is that the message did not
			// reach everybody, which is the honest report and is why the
			// partial ids travel with the error rather than being discarded.
			if len(taskIDs) > 0 {
				return nil, fmt.Errorf("enqueue chat:run for %d of %d agents: %w",
					len(taskIDs), len(targets), ferr)
			}
			return nil, fmt.Errorf("enqueue chat:run: %w", ferr)
		}
		if taskID == "" {
			taskID = id
		}
		taskIDs = append(taskIDs, id)
	}

	out := &EnqueueResult{
		TaskID:            taskID,
		TaskIDs:           taskIDs,
		Thread:            thread,
		IsNewThread:       resolved.IsNew,
		UserMsgID:         userMsg.ID,
		AddressedAgentIDs: addressed,
	}
	if budget.Verdict == BudgetWarning {
		warning := budget
		out.BudgetWarning = &warning
	}
	return out, nil
}

// resolveAddressing decides who this message is for (T-N3).
//
// **Dashboard only.** The `@` is a human affordance: `/v1` and the widget carry
// an explicit agent_id field (T-N10), and the channels resolve theirs from the
// address the message arrived on (T-S4) or from a group room's bindings (T-N9).
// A machine caller's message text is not a place to look for routing.
//
// Returns zero agents for every message that names nobody, which is what every
// message did before this ticket and what the caller turns into a single turn
// for the thread's own agent.
func (s *ChatEnqueuer) resolveAddressing(
	ctx context.Context, in ChatInput, thread *domain.ConversationThread,
) (Addressing, error) {
	plain := Addressing{Cleaned: in.Message}
	if s.room == nil || in.Channel != domain.ChannelDashboard || !strings.Contains(in.Message, "@") {
		return plain, nil
	}

	participants, err := s.room.ListParticipants(ctx, in.CompanyID, thread.ID)
	if err != nil {
		// A membership lookup that failed must not refuse the turn. The answer
		// then comes from the thread's own agent, which is the answer it would
		// have come from before this ticket — ChatRunner.companyContext's
		// argument, that context makes an answer better and is never what makes
		// one possible.
		logrus.WithError(err).WithField("thread_id", thread.ID).
			Warn("participant lookup failed; addressing this turn to the thread's agent")
		return plain, nil
	}
	if len(participants) < 2 {
		// A room of one cannot be addressed: there is nobody else to pick, and
		// "@Finance" in a conversation that only has Finance is text.
		return plain, nil
	}

	addr := ParseAddressing(in.Message, participants)
	if len(addr.AgentIDs) > 0 {
		return addr, nil
	}

	// Nothing matched. Two ways that can be true, and they need different
	// answers: a name nobody has is ordinary text, and a name the *company* has
	// but this conversation does not is a refusal. Without the second the user
	// addresses a real agent, is silently answered by the default speaker, and
	// finds out by reading a reply in the wrong voice.
	//
	// Only reached when a message contains an `@` that resolved to no
	// participant, so the roster listing is off the hot path for every ordinary
	// message.
	if name := s.namesARosterAgent(ctx, in, participants); name != "" {
		return plain, fmt.Errorf("%w: %s", ErrAgentNotInRoom, name)
	}
	return addr, nil
}

// namesARosterAgent returns the name of a company agent that was addressed and
// is not in the room, or "".
func (s *ChatEnqueuer) namesARosterAgent(
	ctx context.Context, in ChatInput, participants []*domain.ThreadParticipant,
) string {
	agents, err := s.room.ListAgents(ctx, in.CompanyID)
	if err != nil {
		// Same rule as the participant lookup: a failed read degrades to
		// today's behaviour rather than refusing the turn.
		logrus.WithError(err).WithField("company_id", in.CompanyID).
			Warn("roster lookup failed; an unmatched @ is being treated as text")
		return ""
	}
	inRoom := make(map[string]bool, len(participants))
	for _, p := range participants {
		inRoom[p.AgentID] = true
	}
	// Reuse the parser rather than re-implementing the match: the whole roster
	// stands in for the room, so "would this have matched if the agent were
	// here" is the same question ParseAddressing already answers.
	roster := make([]*domain.ThreadParticipant, 0, len(agents))
	for _, a := range agents {
		if !inRoom[a.ID] && a.Enabled {
			roster = append(roster, &domain.ThreadParticipant{AgentID: a.ID, AgentName: a.Name})
		}
	}
	roster = s.reachableOnly(ctx, in, roster)
	hit := ParseAddressing(in.Message, roster)
	for _, id := range hit.AgentIDs {
		for _, a := range agents {
			if a.ID == id {
				return a.Name
			}
		}
	}
	return ""
}

// reachableOnly narrows a roster to the agents the person asking may talk to
// (T-Z4), for namesARosterAgent.
//
// An agent the person was never offered is one whose name is **text** to them,
// not "not in this conversation": the refusal names the agent, and naming one
// the picker hid would tell a member it exists — the thing hiding it was for.
// The cost is that `@HR` from somebody not granted HR is answered by the room's
// default speaker, which is exactly what `@` followed by any other unknown name
// gets. A failed read narrows to nothing for the same reason; the roster read
// failing already degrades to text one function up.
func (s *ChatEnqueuer) reachableOnly(
	ctx context.Context, in ChatInput, roster []*domain.ThreadParticipant,
) []*domain.ThreadParticipant {
	userID := personOf(in)
	if s.access == nil || userID == "" || len(roster) == 0 {
		return roster
	}
	ids := make([]string, len(roster))
	for i, p := range roster {
		ids[i] = p.AgentID
	}
	open, err := s.access.Visible(ctx, authz.Subject{CompanyID: in.CompanyID, UserID: userID}, domain.ResourceKindAgent, ids)
	if err != nil {
		logrus.WithError(err).WithField("company_id", in.CompanyID).
			Warn("agent access lookup failed; an unmatched @ is being treated as text")
		return nil
	}
	allowed := make(map[string]bool, len(open))
	for _, id := range open {
		allowed[id] = true
	}
	out := roster[:0:0]
	for _, p := range roster {
		if allowed[p.AgentID] {
			out = append(out, p)
		}
	}
	return out
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
func (s *ChatEnqueuer) boundAgent(ctx context.Context, in ChatInput) (domain.ChannelRoute, error) {
	if s.bindings == nil {
		return domain.ChannelRoute{}, nil
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
		return domain.ChannelRoute{}, nil
	}
	// Normalised through the same function the write path uses, or a number
	// stored as `+62…` never matches inbound `whatsapp:+62…`.
	ref = domain.NormalizeChannelRef(in.Channel, ref)
	if ref == "" {
		return domain.ChannelRoute{}, nil
	}
	route, err := s.bindings.AgentForChannel(ctx, in.CompanyID, in.Channel, ref)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.ChannelRoute{}, nil
	}
	if err != nil {
		return domain.ChannelRoute{}, fmt.Errorf("resolve channel binding: %w", err)
	}
	return route, nil
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
