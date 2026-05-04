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
func (s *ThreadService) ResolveForPhone(ctx context.Context, companyID, phoneNumber, userMessage string) (*ResolveResult, error) {
	if companyID == "" || phoneNumber == "" {
		return nil, fmt.Errorf("companyID and phoneNumber required")
	}

	latest, err := s.threads.LatestForPhone(ctx, companyID, phoneNumber)
	if errors.Is(err, domain.ErrNotFound) {
		return s.createThread(ctx, companyID, domain.ChannelWhatsApp, phoneNumber, "", userMessage)
	}
	if err != nil {
		return nil, fmt.Errorf("lookup thread: %w", err)
	}

	return s.continueOrFork(ctx, latest, userMessage, s.idleThreshold, domain.ChannelWhatsApp, phoneNumber, "")
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
		return s.createThread(ctx, companyID, domain.ChannelDashboard, "", userID, userMessage)
	}
	if err != nil {
		return nil, fmt.Errorf("lookup thread: %w", err)
	}

	return s.continueOrFork(ctx, latest, userMessage, s.dashboardIdleTTL, domain.ChannelDashboard, "", userID)
}

// CreateDashboardThread creates a fresh dashboard thread for the given user.
// The dashboard calls this explicitly when the user clicks "New conversation".
func (s *ThreadService) CreateDashboardThread(ctx context.Context, companyID, userID, firstMessage string) (*domain.ConversationThread, error) {
	if companyID == "" || userID == "" {
		return nil, fmt.Errorf("companyID and userID required")
	}
	res, err := s.createThread(ctx, companyID, domain.ChannelDashboard, "", userID, firstMessage)
	if err != nil {
		return nil, err
	}
	return res.Thread, nil
}

func (s *ThreadService) continueOrFork(
	ctx context.Context, latest *domain.ConversationThread, userMessage string,
	threshold time.Duration, channel domain.Channel, phone, userID string,
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
	return s.createThread(ctx, latest.CompanyID, channel, phone, userID, userMessage)
}

func (s *ThreadService) createThread(
	ctx context.Context, companyID string, channel domain.Channel,
	phone, userID, firstMessage string,
) (*ResolveResult, error) {
	now := time.Now()
	t := &domain.ConversationThread{
		CompanyID:     companyID,
		Channel:       channel,
		PhoneNumber:   phone,
		UserID:        userID,
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
