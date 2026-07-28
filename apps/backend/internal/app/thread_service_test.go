package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/domain"
)

// --- fakes -------------------------------------------------------------

// fakeThreadRepo serves one "latest" thread and records creations. Only the
// methods the resolution path touches are implemented; the rest panic so a
// future change that reaches for one is visible rather than silent.
type fakeThreadRepo struct {
	latest    *domain.ConversationThread
	latestErr error
	created   []*domain.ConversationThread
}

func (f *fakeThreadRepo) Create(_ context.Context, t *domain.ConversationThread) error {
	t.ID = "new-thread"
	f.created = append(f.created, t)
	return nil
}

func (f *fakeThreadRepo) latestOrErr() (*domain.ConversationThread, error) {
	if f.latestErr != nil {
		return nil, f.latestErr
	}
	return f.latest, nil
}

func (f *fakeThreadRepo) LatestForPhone(context.Context, string, string) (*domain.ConversationThread, error) {
	return f.latestOrErr()
}
func (f *fakeThreadRepo) LatestForUser(context.Context, string, string) (*domain.ConversationThread, error) {
	return f.latestOrErr()
}
func (f *fakeThreadRepo) LatestForDiscordUser(context.Context, string, string) (*domain.ConversationThread, error) {
	return f.latestOrErr()
}
func (f *fakeThreadRepo) LatestForLark(context.Context, string, string) (*domain.ConversationThread, error) {
	return f.latestOrErr()
}
func (f *fakeThreadRepo) GetByID(context.Context, string) (*domain.ConversationThread, error) {
	panic("unexpected GetByID")
}
func (f *fakeThreadRepo) ListByCompany(context.Context, string, int, int) ([]*domain.ConversationThread, error) {
	panic("unexpected ListByCompany")
}
func (f *fakeThreadRepo) UpdateSummary(context.Context, string, string, string) error {
	panic("unexpected UpdateSummary")
}
func (f *fakeThreadRepo) Touch(context.Context, string, time.Time) error {
	panic("unexpected Touch")
}
func (f *fakeThreadRepo) Archive(context.Context, string) error { panic("unexpected Archive") }
func (f *fakeThreadRepo) Delete(context.Context, string) error  { panic("unexpected Delete") }

// fakeClassifierLLM answers the topic classifier with a canned verdict.
type fakeClassifierLLM struct {
	reply string
	err   error
	calls int
}

func (f *fakeClassifierLLM) Generate(context.Context, string, ...interfaces.GenerateOption) (string, error) {
	f.calls++
	return f.reply, f.err
}
func (f *fakeClassifierLLM) GenerateWithTools(context.Context, string, []interfaces.Tool, ...interfaces.GenerateOption) (string, error) {
	panic("unexpected GenerateWithTools")
}
func (f *fakeClassifierLLM) GenerateDetailed(context.Context, string, ...interfaces.GenerateOption) (*interfaces.LLMResponse, error) {
	panic("unexpected GenerateDetailed")
}
func (f *fakeClassifierLLM) GenerateWithToolsDetailed(context.Context, string, []interfaces.Tool, ...interfaces.GenerateOption) (*interfaces.LLMResponse, error) {
	panic("unexpected GenerateWithToolsDetailed")
}
func (f *fakeClassifierLLM) Name() string            { return "fake-classifier" }
func (f *fakeClassifierLLM) SupportsStreaming() bool { return false }

func newThreadService(repo *fakeThreadRepo, llm *fakeClassifierLLM) *ThreadService {
	return NewThreadService(repo, nil, NewTopicClassifier(llm), llm, ThreadServiceConfig{
		IdleMinutes:        30,
		DashboardIdleHours: 4,
		SummaryEveryNTurns: 8,
	})
}

func existingThread(lastMessageAgo time.Duration, summary string) *domain.ConversationThread {
	return &domain.ConversationThread{
		ID:            "existing",
		CompanyID:     "co-1",
		Channel:       domain.ChannelWhatsApp,
		PhoneNumber:   "+628123",
		Summary:       summary,
		LastMessageAt: time.Now().Add(-lastMessageAgo),
	}
}

// --- the decision table --------------------------------------------------

// continueOrFork is the whole hybrid-threading strategy in one function, and
// getting it wrong is expensive in both directions: fork too eagerly and the
// user loses context mid-conversation, continue too eagerly and yesterday's
// topic contaminates today's answer.
func TestContinueOrForkDecisionTable(t *testing.T) {
	cases := []struct {
		name string
		// input
		idle    time.Duration
		summary string
		reply   string
		llmErr  error
		// expectation
		wantNew      bool
		wantLLMCalls int
		why          string
	}{
		{
			name: "under the idle threshold continues without asking",
			idle: 5 * time.Minute, summary: "sales figures", reply: "NEW",
			wantNew: false, wantLLMCalls: 0,
			why: "a reply two minutes later is the same conversation; paying for a classification would be waste",
		},
		{
			name: "over the threshold and RELATED continues",
			idle: 45 * time.Minute, summary: "sales figures", reply: "RELATED",
			wantNew: false, wantLLMCalls: 1,
		},
		{
			name: "over the threshold and NEW forks",
			idle: 45 * time.Minute, summary: "sales figures", reply: "NEW",
			wantNew: true, wantLLMCalls: 1,
		},
		{
			name: "a classifier error continues the existing thread",
			idle: 45 * time.Minute, summary: "sales figures", reply: "", llmErr: errors.New("provider down"),
			wantNew: false, wantLLMCalls: 1,
			why: "fail-open: an outage must not fragment a conversation into a thread per message",
		},
		{
			name: "no summary forks without calling the classifier",
			idle: 45 * time.Minute, summary: "", reply: "RELATED",
			wantNew: true, wantLLMCalls: 0,
			why: "there is nothing to compare the message against, so there is nothing to ask",
		},
		{
			name: "the verdict is matched case-insensitively inside the reply",
			idle: 45 * time.Minute, summary: "sales figures", reply: "  related\n",
			wantNew: false, wantLLMCalls: 1,
		},
		{
			name: "an unparseable verdict forks",
			idle: 45 * time.Minute, summary: "sales figures", reply: "I'm not sure",
			wantNew: true, wantLLMCalls: 1,
			why: "anything that is not RELATED is treated as NEW, so a chatty model cannot glue two topics together",
		},
		{
			name: "exactly at the threshold classifies",
			idle: 30 * time.Minute, summary: "sales figures", reply: "RELATED",
			wantNew: false, wantLLMCalls: 1,
			why: "the comparison is `idle < threshold`, so the boundary itself goes to the classifier",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			latest := existingThread(tc.idle, tc.summary)
			repo := &fakeThreadRepo{latest: latest}
			llm := &fakeClassifierLLM{reply: tc.reply, err: tc.llmErr}
			svc := newThreadService(repo, llm)

			res, err := svc.continueOrFork(context.Background(), latest, "how did we do?",
				svc.idleThreshold, domain.ChannelWhatsApp, "+628123", "", "")
			if err != nil {
				t.Fatalf("continueOrFork: %v", err)
			}

			if res.IsNew != tc.wantNew {
				t.Errorf("IsNew = %v, want %v. %s", res.IsNew, tc.wantNew, tc.why)
			}
			if tc.wantNew {
				if len(repo.created) != 1 {
					t.Fatalf("created %d threads, want 1", len(repo.created))
				}
				if res.Thread == latest {
					t.Error("IsNew is true but the existing thread was returned")
				}
			} else {
				if len(repo.created) != 0 {
					t.Errorf("created %d threads while continuing", len(repo.created))
				}
				if res.Thread != latest {
					t.Errorf("returned %+v, want the existing thread", res.Thread)
				}
			}
			if llm.calls != tc.wantLLMCalls {
				t.Errorf("classifier called %d times, want %d", llm.calls, tc.wantLLMCalls)
			}
		})
	}
}

// A fork has to carry the identity forward, or the new thread is unreachable
// from the channel that created it and the next message forks again.
func TestForkCarriesTheChannelIdentity(t *testing.T) {
	cases := []struct {
		name          string
		channel       domain.Channel
		phone         string
		userID        string
		discordUserID string
	}{
		{"whatsapp", domain.ChannelWhatsApp, "+628123", "", ""},
		{"dashboard", domain.ChannelDashboard, "", "user-1", ""},
		{"discord", domain.ChannelDiscord, "", "", "discord-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			latest := existingThread(45*time.Minute, "sales figures")
			latest.Channel = tc.channel
			repo := &fakeThreadRepo{latest: latest}
			svc := newThreadService(repo, &fakeClassifierLLM{reply: "NEW"})

			res, err := svc.continueOrFork(context.Background(), latest, "unrelated question",
				svc.idleThreshold, tc.channel, tc.phone, tc.userID, tc.discordUserID)
			if err != nil {
				t.Fatalf("continueOrFork: %v", err)
			}
			if !res.IsNew {
				t.Fatal("expected a fork")
			}
			got := repo.created[0]
			if got.Channel != tc.channel {
				t.Errorf("channel = %q, want %q", got.Channel, tc.channel)
			}
			if got.PhoneNumber != tc.phone || got.UserID != tc.userID || got.DiscordUserID != tc.discordUserID {
				t.Errorf("identity = (%q, %q, %q), want (%q, %q, %q)",
					got.PhoneNumber, got.UserID, got.DiscordUserID, tc.phone, tc.userID, tc.discordUserID)
			}
			if got.CompanyID != latest.CompanyID {
				t.Errorf("company = %q, want %q — a fork must stay in its tenant", got.CompanyID, latest.CompanyID)
			}
			if got.Title == "" {
				t.Error("the forked thread has no title")
			}
		})
	}
}

func TestResolveForPhone(t *testing.T) {
	t.Run("creates a thread when none exists", func(t *testing.T) {
		repo := &fakeThreadRepo{latestErr: domain.ErrNotFound}
		llm := &fakeClassifierLLM{reply: "RELATED"}
		svc := newThreadService(repo, llm)

		res, err := svc.ResolveForPhone(context.Background(), "co-1", "+628123", "how were sales?")
		if err != nil {
			t.Fatalf("ResolveForPhone: %v", err)
		}
		if !res.IsNew {
			t.Error("IsNew = false for a first-ever message")
		}
		if llm.calls != 0 {
			t.Errorf("classifier called %d times with no prior thread", llm.calls)
		}
		if got := repo.created[0]; got.Channel != domain.ChannelWhatsApp || got.PhoneNumber != "+628123" {
			t.Errorf("created %+v, want a whatsapp thread for +628123", got)
		}
	})

	t.Run("requires both identifiers", func(t *testing.T) {
		svc := newThreadService(&fakeThreadRepo{}, &fakeClassifierLLM{})
		if _, err := svc.ResolveForPhone(context.Background(), "", "+628123", "hi"); err == nil {
			t.Error("ResolveForPhone = nil error with no company id")
		}
		if _, err := svc.ResolveForPhone(context.Background(), "co-1", "", "hi"); err == nil {
			t.Error("ResolveForPhone = nil error with no phone number")
		}
	})

	t.Run("propagates a lookup failure instead of forking", func(t *testing.T) {
		// A transient DB error must not look like "no thread exists" — that
		// would silently start a new conversation and lose the user's context.
		repo := &fakeThreadRepo{latestErr: errors.New("connection reset")}
		svc := newThreadService(repo, &fakeClassifierLLM{})

		if _, err := svc.ResolveForPhone(context.Background(), "co-1", "+628123", "hi"); err == nil {
			t.Fatal("ResolveForPhone = nil error when the lookup failed")
		}
		if len(repo.created) != 0 {
			t.Error("a thread was created after a failed lookup")
		}
	})
}

// Lark is deliberately outside the idle/classifier logic: one reply-thread is
// one agent memory by definition, so an old Lark thread continues however long
// the gap.
func TestResolveForLarkNeverForks(t *testing.T) {
	latest := existingThread(30*24*time.Hour, "sales figures")
	latest.Channel = domain.ChannelLark
	repo := &fakeThreadRepo{latest: latest}
	llm := &fakeClassifierLLM{reply: "NEW"}
	svc := newThreadService(repo, llm)

	res, err := svc.ResolveForLark(context.Background(), "co-1", "chat-1", "thread-key", "open-1", "anything")
	if err != nil {
		t.Fatalf("ResolveForLark: %v", err)
	}
	if res.IsNew {
		t.Error("IsNew = true; a Lark reply-thread must continue regardless of the gap")
	}
	if llm.calls != 0 {
		t.Errorf("classifier called %d times on the Lark path, want 0", llm.calls)
	}
}

func TestResolveForLarkCreatesWithItsKeys(t *testing.T) {
	repo := &fakeThreadRepo{latestErr: domain.ErrNotFound}
	svc := newThreadService(repo, &fakeClassifierLLM{})

	res, err := svc.ResolveForLark(context.Background(), "co-1", "chat-1", "thread-key", "open-1", "how were sales?")
	if err != nil {
		t.Fatalf("ResolveForLark: %v", err)
	}
	if !res.IsNew {
		t.Fatal("IsNew = false for a first-ever Lark message")
	}
	got := repo.created[0]
	if got.Channel != domain.ChannelLark {
		t.Errorf("channel = %q, want lark", got.Channel)
	}
	if got.LarkChatID != "chat-1" || got.LarkThreadKey != "thread-key" || got.LarkOpenID != "open-1" {
		t.Errorf("lark keys = (%q, %q, %q), want (chat-1, thread-key, open-1)",
			got.LarkChatID, got.LarkThreadKey, got.LarkOpenID)
	}
}

// The dashboard gets a longer leash than WhatsApp — someone leaves a tab open
// over lunch — so the two thresholds must stay distinct.
func TestDashboardUsesItsOwnIdleThreshold(t *testing.T) {
	latest := existingThread(2*time.Hour, "sales figures")
	latest.Channel = domain.ChannelDashboard
	repo := &fakeThreadRepo{latest: latest}
	llm := &fakeClassifierLLM{reply: "NEW"}
	svc := newThreadService(repo, llm)

	res, err := svc.ResolveForUser(context.Background(), "co-1", "user-1", "and by region?")
	if err != nil {
		t.Fatalf("ResolveForUser: %v", err)
	}
	if res.IsNew {
		t.Error("a 2-hour gap forked a dashboard thread; the dashboard TTL is 4 hours")
	}
	if llm.calls != 0 {
		t.Errorf("classifier called %d times inside the dashboard TTL", llm.calls)
	}

	// Past the dashboard TTL it does classify, and this time the fork lands.
	latest.LastMessageAt = time.Now().Add(-5 * time.Hour)
	res, err = svc.ResolveForUser(context.Background(), "co-1", "user-1", "something else entirely")
	if err != nil {
		t.Fatalf("ResolveForUser: %v", err)
	}
	if !res.IsNew {
		t.Error("a 5-hour gap with a NEW verdict did not fork")
	}
}

func TestNewThreadServiceDefaultsNonPositiveTunables(t *testing.T) {
	svc := NewThreadService(&fakeThreadRepo{}, nil, NewTopicClassifier(&fakeClassifierLLM{}),
		&fakeClassifierLLM{}, ThreadServiceConfig{})

	if svc.idleThreshold != 30*time.Minute {
		t.Errorf("idleThreshold = %v, want 30m", svc.idleThreshold)
	}
	if svc.dashboardIdleTTL != 4*time.Hour {
		t.Errorf("dashboardIdleTTL = %v, want 4h", svc.dashboardIdleTTL)
	}
	// A zero here would be a modulo by zero in AppendAssistantMessage.
	if svc.summaryEveryN != 8 {
		t.Errorf("summaryEveryN = %d, want 8", svc.summaryEveryN)
	}
}

func TestTopicClassifierIsRelated(t *testing.T) {
	cases := []struct {
		name    string
		summary string
		reply   string
		err     error
		want    bool
		wantErr bool
	}{
		{"related", "sales", "RELATED", nil, true, false},
		{"new", "sales", "NEW", nil, false, false},
		{"lowercase", "sales", "related", nil, true, false},
		{"padded", "sales", "  RELATED  ", nil, true, false},
		{"empty summary short-circuits to new", "", "RELATED", nil, false, false},
		{"whitespace summary short-circuits to new", "   ", "RELATED", nil, false, false},
		// Fail-open, and the error is still returned so the caller can log it.
		{"error fails open to related", "sales", "", errors.New("boom"), true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			llm := &fakeClassifierLLM{reply: tc.reply, err: tc.err}
			got, err := NewTopicClassifier(llm).IsRelated(context.Background(), tc.summary, "message")
			if got != tc.want {
				t.Errorf("IsRelated = %v, want %v", got, tc.want)
			}
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestDeriveTitle(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "New conversation"},
		{"whitespace only", "   \n\t ", "New conversation"},
		{"short", "how were sales", "how were sales"},
		{"trimmed", "  how were sales  ", "how were sales"},
		{"capped at six words", "one two three four five six seven eight", "one two three four five six"},
		{"whitespace collapsed", "how   were\n\tsales", "how were sales"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deriveTitle(tc.in); got != tc.want {
				t.Errorf("deriveTitle(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	t.Run("long single word is ellipsised at 60", func(t *testing.T) {
		// Titles land in a fixed-width sidebar, so the cap is a layout
		// contract, not a nicety.
		got := deriveTitle(strings.Repeat("x", 200))
		if len(got) != 60 {
			t.Errorf("len = %d, want 60: %q", len(got), got)
		}
		if !strings.HasSuffix(got, "...") {
			t.Errorf("got %q, want it to end in an ellipsis", got)
		}
	})
}
