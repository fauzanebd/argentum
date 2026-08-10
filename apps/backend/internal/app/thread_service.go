package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
)

// ThreadService implements the hybrid threading strategy:
//
//   - Each phone number gets its own thread chain (one number = one user
//     identity).
//   - Within that chain, an inbound message continues the latest non-archived
//     thread if the idle gap is below the configured threshold.
//   - Above the threshold, run a tiny LLM topic classifier against the
//     thread's rolling summary; continue if RELATED, start a new thread if
//     NEW.
//   - Every SummaryEveryN turns, refresh the rolling summary from the last
//     ~12 messages so future classification stays cheap and accurate.
type ThreadService struct {
	threads          domain.ThreadRepository
	messages         domain.MessageRepository
	classifier       *TopicClassifier
	llm              interfaces.LLM
	idleThreshold    time.Duration
	summaryEveryN    int
	dashboardIdleTTL time.Duration
}

// ThreadServiceConfig groups tunables.
type ThreadServiceConfig struct {
	IdleMinutes        int // default 30
	DashboardIdleHours int // default 4
	SummaryEveryNTurns int // default 8
}

func NewThreadService(
	threads domain.ThreadRepository,
	messages domain.MessageRepository,
	classifier *TopicClassifier,
	llm interfaces.LLM,
	cfg ThreadServiceConfig,
) *ThreadService {
	if cfg.IdleMinutes <= 0 {
		cfg.IdleMinutes = 30
	}
	if cfg.DashboardIdleHours <= 0 {
		cfg.DashboardIdleHours = 4
	}
	if cfg.SummaryEveryNTurns <= 0 {
		cfg.SummaryEveryNTurns = 8
	}
	return &ThreadService{
		threads:          threads,
		messages:         messages,
		classifier:       classifier,
		llm:              llm,
		idleThreshold:    time.Duration(cfg.IdleMinutes) * time.Minute,
		dashboardIdleTTL: time.Duration(cfg.DashboardIdleHours) * time.Hour,
		summaryEveryN:    cfg.SummaryEveryNTurns,
	}
}

// ResolveResult tells the caller which thread to use for the incoming message
// and whether it was just created.
type ResolveResult struct {
	Thread *domain.ConversationThread
	IsNew  bool
}

// ResolveForPhone picks the thread for an inbound WhatsApp message.
//
// agentID is what the number is bound to (T-S4), empty for "the company
// default". Like every other resolver that takes one, it is not validated here:
// whether that agent exists, belongs to this company and is enabled is answered
// before this is reached.
func (s *ThreadService) ResolveForPhone(ctx context.Context, companyID, phoneNumber, userMessage, agentID string) (*ResolveResult, error) {
	if companyID == "" || phoneNumber == "" {
		return nil, fmt.Errorf("companyID and phoneNumber required")
	}

	latest, err := s.threads.LatestForPhone(ctx, companyID, phoneNumber)
	if errors.Is(err, domain.ErrNotFound) {
		return s.createThread(ctx, companyID, domain.ChannelWhatsApp, phoneNumber, "", "", userMessage, agentID)
	}
	if err != nil {
		return nil, fmt.Errorf("lookup thread: %w", err)
	}

	return s.continueOrFork(ctx, latest, userMessage, s.idleThreshold, domain.ChannelWhatsApp, phoneNumber, "", "", agentID)
}

// ResolveForDiscordUser picks the thread for an inbound Discord message.
// Threads are keyed by (companyID, discordUserID); a single user gets one
// continuous thread regardless of which guild/channel they message from.
func (s *ThreadService) ResolveForDiscordUser(ctx context.Context, companyID, discordUserID, userMessage, agentID string) (*ResolveResult, error) {
	if companyID == "" || discordUserID == "" {
		return nil, fmt.Errorf("companyID and discordUserID required")
	}

	latest, err := s.threads.LatestForDiscordUser(ctx, companyID, discordUserID)
	if errors.Is(err, domain.ErrNotFound) {
		return s.createThread(ctx, companyID, domain.ChannelDiscord, "", "", discordUserID, userMessage, agentID)
	}
	if err != nil {
		return nil, fmt.Errorf("lookup thread: %w", err)
	}

	return s.continueOrFork(ctx, latest, userMessage, s.idleThreshold, domain.ChannelDiscord, "", "", discordUserID, agentID)
}

// ResolveForLark picks the thread for an inbound Lark message. Threads are
// keyed by (companyID, larkThreadKey) with no idle/fork classification: one
// Lark reply-thread is one persistent agent memory by definition. Missing
// row → create.
func (s *ThreadService) ResolveForLark(ctx context.Context, companyID, larkChatID, larkThreadKey, larkOpenID, userMessage, agentID string) (*ResolveResult, error) {
	if companyID == "" || larkThreadKey == "" {
		return nil, fmt.Errorf("companyID and larkThreadKey required")
	}

	latest, err := s.threads.LatestForLark(ctx, companyID, larkThreadKey)
	if errors.Is(err, domain.ErrNotFound) {
		return s.createLarkThread(ctx, companyID, larkChatID, larkThreadKey, larkOpenID, userMessage, agentID)
	}
	if err != nil {
		return nil, fmt.Errorf("lookup thread: %w", err)
	}

	return &ResolveResult{Thread: latest, IsNew: false}, nil
}

// ResolveForSlack picks the thread for an inbound Slack message.
//
// Slack is the one channel with *both* shapes the playbook describes, so it
// keys on both and skips fork classification either way:
//
//   - A message inside a thread carries thread_ts. That is the user's own
//     boundary, so it resolves by (channel, thread_ts) and continues however
//     long the gap has been — the Lark rule.
//   - A top-level mention or DM carries none. There is no thread id to look up
//     yet, so it resolves by (channel, user) — the Discord rule — and the new
//     conversation records the ts our reply will hang under.
//
// That last line is what keeps the two keys from disagreeing. Without it the
// follow-ups arriving inside the thread our own reply created would find
// nothing under (channel, thread_ts) and open a second conversation for what
// the user is looking at as one.
//
// agentID pins a new conversation to one roster agent (T-S4); empty means the
// company default.
func (s *ThreadService) ResolveForSlack(ctx context.Context, in SlackThreadKey, userMessage, agentID string) (*ResolveResult, error) {
	if in.CompanyID == "" || in.ChannelID == "" || in.UserID == "" {
		return nil, fmt.Errorf("companyID, slack channelID and userID required")
	}
	if in.MessageTS == "" {
		return nil, fmt.Errorf("slack message ts required")
	}

	// Threaded reply: the user drew the boundary, so honour it exactly.
	if in.ThreadTS != "" {
		latest, err := s.threads.LatestForSlackThread(ctx, in.CompanyID, in.ChannelID, in.ThreadTS)
		if err == nil {
			return &ResolveResult{Thread: latest, IsNew: false}, nil
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return nil, fmt.Errorf("lookup thread: %w", err)
		}
		// A thread we have never seen — the bot was invited into a conversation
		// already in progress. Open one keyed to it.
		return s.createSlackThread(ctx, in, in.ThreadTS, userMessage, agentID)
	}

	// Top-level message: continue this person's conversation in this room.
	latest, err := s.threads.LatestForSlackUser(ctx, in.CompanyID, in.ChannelID, in.UserID)
	if errors.Is(err, domain.ErrNotFound) {
		// The reply will hang under this message, so that is the thread ts.
		return s.createSlackThread(ctx, in, in.MessageTS, userMessage, agentID)
	}
	if err != nil {
		return nil, fmt.Errorf("lookup thread: %w", err)
	}
	return &ResolveResult{Thread: latest, IsNew: false}, nil
}

// ResolveForAPIUser picks the thread for a turn started over the public API
// (T-A1). Threads are keyed by (companyID, apiUserRef).
//
// It forks on an idle gap and a topic shift, like WhatsApp and Discord and
// unlike Lark. The playbook's rule is that a platform with native threads
// keys on the platform's thread id and skips classification, because the user
// already drew the boundary — and the API has both shapes: a caller that
// tracks conversations passes an explicit thread id and never reaches here,
// while a caller that just forwards "our user asked X" has drawn no boundary
// at all and gets the heuristic.
//
// agentID pins a *new* conversation to one roster agent (T-S5) and is empty
// for "the company default". It reaches only the create path: whether a
// conversation the resolver decided to continue may run as a different agent
// is a question about that conversation rather than about where a message is
// stored, and ChatEnqueuer.forkForAgent answers it.
func (s *ThreadService) ResolveForAPIUser(ctx context.Context, companyID, apiUserRef, userMessage, agentID string) (*ResolveResult, error) {
	if companyID == "" || apiUserRef == "" {
		return nil, fmt.Errorf("companyID and apiUserRef required")
	}

	create := func() (*ResolveResult, error) {
		return s.createAPIThread(ctx, companyID, apiUserRef, userMessage, agentID)
	}

	latest, err := s.threads.LatestForAPIUser(ctx, companyID, apiUserRef)
	if errors.Is(err, domain.ErrNotFound) {
		return create()
	}
	if err != nil {
		return nil, fmt.Errorf("lookup thread: %w", err)
	}

	return s.continueOrForkWith(ctx, latest, userMessage, s.idleThreshold, create)
}

// ResolveForEmbedUser picks the thread for a turn from the widget (T-20).
// Threads are keyed by (companyID, embedUserRef).
//
// It forks on an idle gap and a topic shift, matching Discord and the API
// rather than Lark. The playbook's rule is that a platform with native threads
// keys on the platform's thread id and skips classification, because the user
// already drew the boundary — and the widget has no threads at all. A visitor
// who comes back tomorrow and asks about something else has drawn no boundary,
// so the heuristic is the only thing that can.
//
// This deliberately reuses continueOrForkWith rather than restating the rule. A
// channel that forks on different terms from the others is a channel whose
// conversations end up somewhere nobody expects.
func (s *ThreadService) ResolveForEmbedUser(ctx context.Context, companyID, embedUserRef, userMessage, agentID string) (*ResolveResult, error) {
	if companyID == "" || embedUserRef == "" {
		return nil, fmt.Errorf("companyID and embedUserRef required")
	}

	create := func() (*ResolveResult, error) {
		return s.createEmbedThread(ctx, companyID, embedUserRef, userMessage, agentID)
	}

	latest, err := s.threads.LatestForEmbedUser(ctx, companyID, embedUserRef)
	if errors.Is(err, domain.ErrNotFound) {
		return create()
	}
	if err != nil {
		return nil, fmt.Errorf("lookup thread: %w", err)
	}

	return s.continueOrForkWith(ctx, latest, userMessage, s.idleThreshold, create)
}

// CreateEmbedThread opens a fresh widget conversation for one visitor, pinned
// to one roster agent. Exported for CreateAPIThread's reason: the enqueuer
// needs a second create after a resolve when the caller's agent disagrees with
// what the resolved conversation runs as.
func (s *ThreadService) CreateEmbedThread(
	ctx context.Context, companyID, embedUserRef, firstMessage, agentID string,
) (*ResolveResult, error) {
	if companyID == "" || embedUserRef == "" {
		return nil, fmt.Errorf("companyID and embedUserRef required")
	}
	return s.createEmbedThread(ctx, companyID, embedUserRef, firstMessage, agentID)
}

func (s *ThreadService) createEmbedThread(
	ctx context.Context, companyID, embedUserRef, firstMessage, agentID string,
) (*ResolveResult, error) {
	now := time.Now()
	t := &domain.ConversationThread{
		CompanyID:     companyID,
		Channel:       domain.ChannelWidget,
		EmbedUserRef:  embedUserRef,
		AgentID:       agentID,
		Title:         deriveTitle(firstMessage),
		LastMessageAt: now,
	}
	if err := s.threads.Create(ctx, t); err != nil {
		return nil, fmt.Errorf("create thread: %w", err)
	}
	return &ResolveResult{Thread: t, IsNew: true}, nil
}

// ResolveForUser is the old dashboard thread resolver. It is kept for
// backward compatibility but the dashboard now creates / selects threads
// explicitly via CreateDashboardThread instead.
//
// DEPRECATED: not used by the dashboard chat UI; kept for potential
// future channels that need user-scoped auto-resolution.
func (s *ThreadService) ResolveForUser(ctx context.Context, companyID, userID, userMessage string) (*ResolveResult, error) {
	if companyID == "" || userID == "" {
		return nil, fmt.Errorf("companyID and userID required")
	}

	latest, err := s.threads.LatestForUser(ctx, companyID, userID)
	if errors.Is(err, domain.ErrNotFound) {
		return s.createThread(ctx, companyID, domain.ChannelDashboard, "", userID, "", userMessage, "")
	}
	if err != nil {
		return nil, fmt.Errorf("lookup thread: %w", err)
	}

	// No agent: this path has no picker in front of it and no binding behind it
	// — the dashboard's own thread creation is CreateDashboardThread.
	return s.continueOrFork(ctx, latest, userMessage, s.dashboardIdleTTL, domain.ChannelDashboard, "", userID, "", "")
}

// CreateDashboardThread creates a fresh dashboard thread for the given user.
// The dashboard calls this explicitly when the user clicks "New conversation".
//
// agentID pins the conversation to one roster agent (T-S3) and is empty for
// "the company default", which is what every caller outside the chat UI passes.
// It is **not** validated here: whether that agent exists, belongs to this
// company and is enabled is a pick-time question, answered by ChatEnqueuer
// before this is reached. A thread service that re-checked it would need the
// roster, and the roster is not what decides where a conversation is stored.
func (s *ThreadService) CreateDashboardThread(ctx context.Context, companyID, userID, firstMessage, agentID string) (*domain.ConversationThread, error) {
	if companyID == "" || userID == "" {
		return nil, fmt.Errorf("companyID and userID required")
	}
	res, err := s.createDashboardThread(ctx, companyID, userID, firstMessage, agentID)
	if err != nil {
		return nil, err
	}
	return res.Thread, nil
}

// continueOrFork decides between the caller's latest thread and a new one.
//
// agentID is the channel's binding (T-S4) and wins when there is one; without
// one, a fork **keeps the parent's agent** rather than resolving the default
// again. A conversation that split because somebody came back an hour later is
// the same conversation, and re-resolving would silently widen its scope back
// to the default agent's — the failure direction T-S2 refused for a disabled
// agent, arriving here through the idle gap instead.
func (s *ThreadService) continueOrFork(
	ctx context.Context, latest *domain.ConversationThread, userMessage string,
	threshold time.Duration, channel domain.Channel, phone, userID, discordUserID, agentID string,
) (*ResolveResult, error) {
	if agentID == "" {
		agentID = latest.AgentID
	}
	return s.continueOrForkWith(ctx, latest, userMessage, threshold, func() (*ResolveResult, error) {
		return s.createThread(ctx, latest.CompanyID, channel, phone, userID, discordUserID, userMessage, agentID)
	})
}

// continueOrForkWith is the fork decision with the creation of the new thread
// left to the caller. The four positional identity arguments continueOrFork
// carries cannot express a fifth channel's key without growing again, and a
// second copy of the idle-gap-plus-classifier rule is the one thing that must
// not happen: a channel that forks on different terms from the others is a
// channel whose conversations end up in the wrong place.
func (s *ThreadService) continueOrForkWith(
	ctx context.Context, latest *domain.ConversationThread, userMessage string,
	threshold time.Duration, create func() (*ResolveResult, error),
) (*ResolveResult, error) {
	idle := time.Since(latest.LastMessageAt)
	if idle < threshold {
		return &ResolveResult{Thread: latest, IsNew: false}, nil
	}

	related, err := s.classifier.IsRelated(ctx, latest.Summary, userMessage)
	if err != nil {
		// Logged but not fatal — classifier returned RELATED on error
		// (fail-open) so the user keeps their thread.
		logrus.WithError(err).Warn("topic classifier failed; continuing existing thread")
	}
	if related {
		return &ResolveResult{Thread: latest, IsNew: false}, nil
	}
	return create()
}

func (s *ThreadService) createThread(
	ctx context.Context, companyID string, channel domain.Channel,
	phone, userID, discordUserID, firstMessage, agentID string,
) (*ResolveResult, error) {
	now := time.Now()
	t := &domain.ConversationThread{
		CompanyID:     companyID,
		Channel:       channel,
		PhoneNumber:   phone,
		UserID:        userID,
		DiscordUserID: discordUserID,
		AgentID:       agentID,
		Title:         deriveTitle(firstMessage),
		LastMessageAt: now,
	}
	if err := s.threads.Create(ctx, t); err != nil {
		return nil, fmt.Errorf("create thread: %w", err)
	}
	return &ResolveResult{Thread: t, IsNew: true}, nil
}

func (s *ThreadService) createLarkThread(
	ctx context.Context, companyID, larkChatID, larkThreadKey, larkOpenID, firstMessage, agentID string,
) (*ResolveResult, error) {
	now := time.Now()
	t := &domain.ConversationThread{
		CompanyID:     companyID,
		Channel:       domain.ChannelLark,
		LarkChatID:    larkChatID,
		LarkThreadKey: larkThreadKey,
		LarkOpenID:    larkOpenID,
		AgentID:       agentID,
		Title:         deriveTitle(firstMessage),
		LastMessageAt: now,
	}
	if err := s.threads.Create(ctx, t); err != nil {
		return nil, fmt.Errorf("create thread: %w", err)
	}
	return &ResolveResult{Thread: t, IsNew: true}, nil
}

// SlackThreadKey is everything needed to find or open a Slack conversation.
// A struct rather than six positional strings, for the reason
// ChannelThreadInput states — and because MessageTS and ThreadTS differ by one
// character at the call site while meaning opposite things.
type SlackThreadKey struct {
	CompanyID string
	TeamID    string
	ChannelID string
	// UserID is the human who wrote the message, and half of the fallback key.
	UserID string
	// MessageTS is this message's own ts. It becomes the conversation's
	// thread_ts when the message is top-level, because that is where our reply
	// will hang.
	MessageTS string
	// ThreadTS is set only when the message already sits inside a thread.
	ThreadTS string
}

func (s *ThreadService) createSlackThread(
	ctx context.Context, in SlackThreadKey, threadTS, firstMessage, agentID string,
) (*ResolveResult, error) {
	now := time.Now()
	t := &domain.ConversationThread{
		CompanyID:      in.CompanyID,
		Channel:        domain.ChannelSlack,
		SlackTeamID:    in.TeamID,
		SlackChannelID: in.ChannelID,
		SlackThreadTS:  threadTS,
		SlackUserID:    in.UserID,
		AgentID:        agentID,
		Title:          deriveTitle(firstMessage),
		LastMessageAt:  now,
	}
	if err := s.threads.Create(ctx, t); err != nil {
		return nil, fmt.Errorf("create thread: %w", err)
	}
	return &ResolveResult{Thread: t, IsNew: true}, nil
}

// createDashboardThread is the dashboard's own constructor, beside Lark's and
// the API's, rather than an eighth positional argument on createThread. The
// same reasoning continueOrForkWith records: a channel that carries a key the
// others do not gets its own function, because the alternative is a signature
// every channel pays for and one channel reads.
func (s *ThreadService) createDashboardThread(
	ctx context.Context, companyID, userID, firstMessage, agentID string,
) (*ResolveResult, error) {
	now := time.Now()
	t := &domain.ConversationThread{
		CompanyID:     companyID,
		Channel:       domain.ChannelDashboard,
		UserID:        userID,
		AgentID:       agentID,
		Title:         deriveTitle(firstMessage),
		LastMessageAt: now,
	}
	if err := s.threads.Create(ctx, t); err != nil {
		return nil, fmt.Errorf("create thread: %w", err)
	}
	return &ResolveResult{Thread: t, IsNew: true}, nil
}

// ChannelThreadInput identifies a conversation on one of the inbound chat
// channels. It exists because those channels key on different things — a phone
// number, a Discord user, a Lark reply-thread — and continueOrForkWith already
// records why a fifth positional argument was the wrong direction.
type ChannelThreadInput struct {
	CompanyID     string
	Channel       domain.Channel
	PhoneNumber   string
	DiscordUserID string
	LarkChatID    string
	LarkThreadKey string
	LarkOpenID    string
	Slack         SlackThreadKey
	FirstMessage  string
	// AgentID pins the new conversation. Not validated here, for the reason
	// CreateDashboardThread states.
	AgentID string
}

// CreateChannelThread opens a fresh WhatsApp, Discord or Lark conversation
// pinned to one roster agent (T-S4).
//
// Exported beside the resolvers for the same reason CreateAPIThread is: the
// enqueuer needs a second create *after* a resolve, when the address's binding
// disagrees with the agent the resolved thread runs as. Continuing that thread
// would answer as the wrong agent and mix two scopes' history in one memory;
// forking is what T-S5 already does when a caller names an agent the resolved
// conversation does not run as.
func (s *ThreadService) CreateChannelThread(ctx context.Context, in ChannelThreadInput) (*ResolveResult, error) {
	if in.CompanyID == "" {
		return nil, fmt.Errorf("companyID required")
	}
	if in.Channel == domain.ChannelLark {
		return s.createLarkThread(ctx, in.CompanyID, in.LarkChatID, in.LarkThreadKey,
			in.LarkOpenID, in.FirstMessage, in.AgentID)
	}
	if in.Channel == domain.ChannelSlack {
		// The fork keeps the trigger's own thread: a rebind opens a new
		// conversation, not a new place to speak.
		threadTS := in.Slack.ThreadTS
		if threadTS == "" {
			threadTS = in.Slack.MessageTS
		}
		return s.createSlackThread(ctx, in.Slack, threadTS, in.FirstMessage, in.AgentID)
	}
	return s.createThread(ctx, in.CompanyID, in.Channel,
		in.PhoneNumber, "", in.DiscordUserID, in.FirstMessage, in.AgentID)
}

// CreateAPIThread opens a fresh API conversation for one `user_ref`, pinned to
// one roster agent (T-S5).
//
// Exported beside the resolver because ChatEnqueuer needs a *second* create
// after a resolve has already happened: a caller who named an agent the
// resolved thread does not run as gets a new conversation rather than an answer
// from the wrong agent. Like CreateDashboardThread, it does not validate
// agentID — that is a pick-time question, answered before this is reached.
func (s *ThreadService) CreateAPIThread(
	ctx context.Context, companyID, apiUserRef, firstMessage, agentID string,
) (*ResolveResult, error) {
	if companyID == "" || apiUserRef == "" {
		return nil, fmt.Errorf("companyID and apiUserRef required")
	}
	return s.createAPIThread(ctx, companyID, apiUserRef, firstMessage, agentID)
}

func (s *ThreadService) createAPIThread(
	ctx context.Context, companyID, apiUserRef, firstMessage, agentID string,
) (*ResolveResult, error) {
	now := time.Now()
	t := &domain.ConversationThread{
		CompanyID:     companyID,
		Channel:       domain.ChannelAPI,
		APIUserRef:    apiUserRef,
		AgentID:       agentID,
		Title:         deriveTitle(firstMessage),
		LastMessageAt: now,
	}
	if err := s.threads.Create(ctx, t); err != nil {
		return nil, fmt.Errorf("create thread: %w", err)
	}
	return &ResolveResult{Thread: t, IsNew: true}, nil
}

// GetByID looks up a thread by ID.
func (s *ThreadService) GetByID(ctx context.Context, id string) (*domain.ConversationThread, error) {
	return s.threads.GetByID(ctx, id)
}

// AppendUserMessage records the user's turn and bumps last_message_at.
func (s *ThreadService) AppendUserMessage(ctx context.Context, threadID, content string) (*domain.Message, error) {
	now := time.Now()
	m := &domain.Message{
		ThreadID:  threadID,
		Role:      domain.MessageRoleUser,
		Content:   content,
		CreatedAt: now,
	}
	if err := s.messages.Append(ctx, m); err != nil {
		return nil, err
	}
	_ = s.threads.Touch(ctx, threadID, now)
	return m, nil
}

// AppendAssistantMessage records the agent's turn, bumps last_message_at,
// and may trigger a summary refresh.
func (s *ThreadService) AppendAssistantMessage(
	ctx context.Context, threadID, content string,
	tokensIn, tokensOut int, latencyMs int64,
) (*domain.Message, error) {
	now := time.Now()
	m := &domain.Message{
		ThreadID:  threadID,
		Role:      domain.MessageRoleAssistant,
		Content:   content,
		TokensIn:  tokensIn,
		TokensOut: tokensOut,
		LatencyMs: latencyMs,
		CreatedAt: now,
	}
	if err := s.messages.Append(ctx, m); err != nil {
		return nil, err
	}
	_ = s.threads.Touch(ctx, threadID, now)

	count, err := s.messages.CountByThread(ctx, threadID)
	if err == nil && (count == 2 || count%s.summaryEveryN == 0) {
		go s.refreshSummary(context.Background(), threadID)
	}
	return m, nil
}

// refreshSummary regenerates the rolling thread summary from the last 12
// messages. Runs asynchronously so it doesn't block the user response path.
func (s *ThreadService) refreshSummary(ctx context.Context, threadID string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	msgs, err := s.messages.ListByThread(ctx, threadID, 12, 0)
	if err != nil {
		logrus.WithError(err).Warn("summary refresh: list messages")
		return
	}
	if len(msgs) == 0 {
		return
	}

	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(string(m.Role))
		b.WriteString(": ")
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	prompt := "Summarize this analytics conversation in 1–2 sentences (≤200 chars). Focus on the topic, not on individual numbers.\n\n" + b.String()
	resp, err := s.llm.Generate(ctx, prompt,
		interfaces.WithSystemMessage("You produce ultra-concise topic summaries."),
		interfaces.WithTemperature(0),
	)
	if err != nil {
		logrus.WithError(err).Warn("summary refresh: llm generate")
		return
	}
	summary := strings.TrimSpace(resp)
	if len(summary) > 400 {
		summary = summary[:400]
	}
	thread, err := s.threads.GetByID(ctx, threadID)
	if err != nil {
		return
	}
	title := thread.Title
	if title == "" || strings.HasPrefix(title, "Thread") || title == "New conversation" || thread.Summary == "" {
		titlePrompt := "Generate a short, concise title (max 4-5 words) for this conversation. Respond ONLY with the title. Do not use quotes.\n\n" + b.String()
		titleResp, titleErr := s.llm.Generate(ctx, titlePrompt,
			interfaces.WithSystemMessage("You generate short, punchy conversation titles."),
			interfaces.WithTemperature(0),
		)
		if titleErr == nil && strings.TrimSpace(titleResp) != "" {
			title = strings.TrimSpace(titleResp)
			// Remove surrounding quotes if the LLM added them anyway
			title = strings.Trim(title, `"'`)
			if len(title) > 60 {
				title = title[:57] + "..."
			}
		} else {
			title = deriveTitle(summary)
		}
	}
	if err := s.threads.UpdateSummary(ctx, threadID, title, summary); err != nil {
		logrus.WithError(err).Warn("summary refresh: update summary")
	}
}

// deriveTitle returns a short title from a piece of free text — the first ~6
// words, capped at 60 chars.
func deriveTitle(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "New conversation"
	}
	fields := strings.Fields(text)
	if len(fields) > 6 {
		fields = fields[:6]
	}
	t := strings.Join(fields, " ")
	if len(t) > 60 {
		t = t[:57] + "..."
	}
	return t
}
