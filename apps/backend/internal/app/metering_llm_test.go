package app

import (
	"context"
	"testing"
	"time"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"
	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/llmusage"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// --- fakes -------------------------------------------------------------

type fakeUsageRepo struct{ events []*domain.UsageEvent }

func (f *fakeUsageRepo) Append(_ context.Context, e *domain.UsageEvent) error {
	f.events = append(f.events, e)
	return nil
}
func (f *fakeUsageRepo) SummaryByCompany(context.Context, string, time.Time, time.Time) (*domain.UsageSummary, error) {
	return nil, nil
}
func (f *fakeUsageRepo) RecentByCompany(context.Context, string, int) ([]*domain.UsageEvent, error) {
	return nil, nil
}
func (f *fakeUsageRepo) SummaryByThread(context.Context, string, string, time.Time, time.Time) (*domain.UsageSummary, error) {
	return nil, nil
}
func (f *fakeUsageRepo) ListThreadUsage(context.Context, string, time.Time, time.Time, int, int) ([]*domain.ThreadUsageRow, error) {
	return nil, nil
}
func (f *fakeUsageRepo) EventsByThread(context.Context, string, string, int, int) ([]*domain.UsageEvent, error) {
	return nil, nil
}
func (f *fakeUsageRepo) UsageByChannel(context.Context, string, time.Time, time.Time) ([]*domain.ChannelUsageRow, error) {
	return nil, nil
}
func (f *fakeUsageRepo) UsageByUser(context.Context, string, time.Time, time.Time) ([]*domain.UserUsageRow, error) {
	return nil, nil
}

type fakeCreditsRepo struct{}

func (fakeCreditsRepo) Get(context.Context, string) (*domain.CompanyCredits, error) { return nil, nil }
func (fakeCreditsRepo) Upsert(context.Context, *domain.CompanyCredits) error        { return nil }
func (fakeCreditsRepo) Decrement(context.Context, string, int64) error              { return nil }

// fakeStreamingLLM emits a fixed event sequence. emitUsage mirrors
// agent-sdk-go's Anthropic client (usage in stream metadata); tapUsage mirrors
// its OpenAI client, which reports usage only on the wire — the HTTP tap picks
// it up out of band, exactly as internal/llmusage does in production.
type fakeStreamingLLM struct {
	emitUsage *interfaces.TokenUsage
	tapUsage  *llmusage.Usage
	sawOpts   []interfaces.GenerateOption
}

func (f *fakeStreamingLLM) Generate(context.Context, string, ...interfaces.GenerateOption) (string, error) {
	return "", nil
}
func (f *fakeStreamingLLM) GenerateWithTools(context.Context, string, []interfaces.Tool, ...interfaces.GenerateOption) (string, error) {
	return "", nil
}
func (f *fakeStreamingLLM) GenerateDetailed(context.Context, string, ...interfaces.GenerateOption) (*interfaces.LLMResponse, error) {
	return &interfaces.LLMResponse{}, nil
}
func (f *fakeStreamingLLM) GenerateWithToolsDetailed(context.Context, string, []interfaces.Tool, ...interfaces.GenerateOption) (*interfaces.LLMResponse, error) {
	return &interfaces.LLMResponse{}, nil
}
func (f *fakeStreamingLLM) Name() string            { return "fake-provider" }
func (f *fakeStreamingLLM) SupportsStreaming() bool { return true }

func (f *fakeStreamingLLM) GenerateStream(ctx context.Context, _ string, opts ...interfaces.GenerateOption) (<-chan interfaces.StreamEvent, error) {
	return f.stream(ctx, opts), nil
}

func (f *fakeStreamingLLM) GenerateWithToolsStream(ctx context.Context, _ string, _ []interfaces.Tool, opts ...interfaces.GenerateOption) (<-chan interfaces.StreamEvent, error) {
	return f.stream(ctx, opts), nil
}

func (f *fakeStreamingLLM) stream(ctx context.Context, opts []interfaces.GenerateOption) <-chan interfaces.StreamEvent {
	f.sawOpts = opts
	if f.tapUsage != nil {
		llmusage.CollectorFrom(ctx).Add(*f.tapUsage)
	}
	ch := make(chan interfaces.StreamEvent, 4)
	go func() {
		defer close(ch)
		ch <- interfaces.StreamEvent{Type: interfaces.StreamEventContentDelta, Content: "answer"}
		if f.emitUsage != nil {
			ch <- interfaces.StreamEvent{
				Type: interfaces.StreamEventContentDelta,
				Metadata: map[string]interface{}{"usage": map[string]interface{}{
					"input_tokens":                f.emitUsage.InputTokens,
					"output_tokens":               f.emitUsage.OutputTokens,
					"cache_creation_input_tokens": f.emitUsage.CacheCreationInputTokens,
					"cache_read_input_tokens":     f.emitUsage.CacheReadInputTokens,
				}},
			}
		}
		ch <- interfaces.StreamEvent{Type: interfaces.StreamEventMessageStop}
	}()
	return ch
}

// --- helpers -----------------------------------------------------------

func drainStream(t *testing.T, ch <-chan interfaces.StreamEvent) {
	t.Helper()
	for range ch { //nolint:revive // draining is the point
	}
}

func meteredFixture(t *testing.T, inner interfaces.LLM) (*MeteredLLM, *fakeUsageRepo, context.Context) {
	t.Helper()
	repo := &fakeUsageRepo{}
	svc := NewUsageService(repo, fakeCreditsRepo{}, DefaultPricing)
	ctx := tenantctx.WithThreadID(tenantctx.WithCompanyID(context.Background(), "co-1"), "th-1")
	return NewMeteredLLM(inner, "anthropic/claude-haiku-4.5", svc), repo, ctx
}

// --- tests -------------------------------------------------------------

// The Anthropic path: usage arrives in stream event metadata, including cache
// tokens. Regression guard for commit 74f5419.
func TestStreamRecordsUsageFromStreamMetadata(t *testing.T) {
	llm := &fakeStreamingLLM{emitUsage: &interfaces.TokenUsage{
		InputTokens: 100, OutputTokens: 40,
		CacheCreationInputTokens: 900, CacheReadInputTokens: 300,
	}}
	m, repo, ctx := meteredFixture(t, llm)

	ch, err := m.GenerateWithToolsStream(ctx, "q", nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	drainStream(t, ch)

	if len(repo.events) != 1 {
		t.Fatalf("recorded %d usage events, want 1", len(repo.events))
	}
	e := repo.events[0]
	if e.TokensIn != 100 || e.TokensOut != 40 || e.CacheCreateTokensIn != 900 || e.CacheReadTokensIn != 300 {
		t.Fatalf("event = %+v, want in=100 out=40 create=900 read=300", e)
	}
	if e.CostMicroUSD == 0 {
		t.Fatal("cost is zero for a metered turn")
	}
}

// Finding C-2: agent-sdk-go's OpenAI client never forwards the usage chunk from
// GenerateWithToolsStream, so a full agent turn recorded nothing. The HTTP tap
// supplies it instead.
func TestStreamRecordsUsageFromHTTPTapWhenMetadataIsSilent(t *testing.T) {
	llm := &fakeStreamingLLM{tapUsage: &llmusage.Usage{InputTokens: 4200, OutputTokens: 610}}
	m, repo, ctx := meteredFixture(t, llm)

	ch, err := m.GenerateWithToolsStream(ctx, "q", nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	drainStream(t, ch)

	if len(repo.events) != 1 {
		t.Fatalf("recorded %d usage events, want 1", len(repo.events))
	}
	e := repo.events[0]
	if e.TokensIn != 4200 || e.TokensOut != 610 {
		t.Fatalf("event = %+v, want in=4200 out=610", e)
	}
	if e.CompanyID != "co-1" || e.ThreadID != "th-1" || e.Model != "anthropic/claude-haiku-4.5" {
		t.Fatalf("event attribution = %+v", e)
	}
}

// Metadata wins when both sources report, so the Anthropic path can never be
// double-billed by a tap that also saw the response.
func TestStreamPrefersMetadataOverTapAndNeverDoubleCounts(t *testing.T) {
	llm := &fakeStreamingLLM{
		emitUsage: &interfaces.TokenUsage{InputTokens: 100, OutputTokens: 40},
		tapUsage:  &llmusage.Usage{InputTokens: 100, OutputTokens: 40},
	}
	m, repo, ctx := meteredFixture(t, llm)

	ch, err := m.GenerateWithToolsStream(ctx, "q", nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	drainStream(t, ch)

	if len(repo.events) != 1 {
		t.Fatalf("recorded %d usage events, want exactly 1", len(repo.events))
	}
	if repo.events[0].TokensIn != 100 {
		t.Fatalf("tokens in = %d, want 100 (not summed)", repo.events[0].TokensIn)
	}
}

// Silence is what let C-2 survive for nine weeks. A turn nobody could bill must
// be loud.
func TestStreamWithoutAnyUsageWarnsAndRecordsNothing(t *testing.T) {
	hook := logrustest.NewGlobal()
	defer hook.Reset()

	llm := &fakeStreamingLLM{}
	m, repo, ctx := meteredFixture(t, llm)

	ch, err := m.GenerateWithToolsStream(ctx, "q", nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	drainStream(t, ch)

	if len(repo.events) != 0 {
		t.Fatalf("recorded %d usage events, want 0", len(repo.events))
	}
	var warned *logrus.Entry
	for _, e := range hook.AllEntries() {
		if e.Level == logrus.WarnLevel {
			warned = e
			break
		}
	}
	if warned == nil {
		t.Fatal("no Warn logged for a turn with no usage")
	}
	for _, field := range []string{"company_id", "model", "provider"} {
		if _, ok := warned.Data[field]; !ok {
			t.Fatalf("warning is missing %q: %+v", field, warned.Data)
		}
	}
}

// withForcedUsage is what makes the provider send usage at all on
// OpenAI-interface routes; without EnableReasoning the SDK omits
// stream_options.include_usage for non-reasoning models.
func TestStreamForcesIncludeUsageOption(t *testing.T) {
	llm := &fakeStreamingLLM{emitUsage: &interfaces.TokenUsage{InputTokens: 1, OutputTokens: 1}}
	m, _, ctx := meteredFixture(t, llm)

	ch, err := m.GenerateWithToolsStream(ctx, "q", nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	drainStream(t, ch)

	applied := &interfaces.GenerateOptions{}
	for _, opt := range llm.sawOpts {
		opt(applied)
	}
	if applied.LLMConfig == nil || !applied.LLMConfig.EnableReasoning {
		t.Fatalf("EnableReasoning not set on forwarded options: %+v", applied.LLMConfig)
	}
}

// The tap sums across every HTTP request in the tool-calling loop; a turn that
// took three iterations must bill all three.
func TestStreamRecordsTapUsageAcrossToolIterations(t *testing.T) {
	repo := &fakeUsageRepo{}
	svc := NewUsageService(repo, fakeCreditsRepo{}, DefaultPricing)
	ctx := tenantctx.WithCompanyID(context.Background(), "co-1")

	inner := &multiIterationLLM{iterations: 3, per: llmusage.Usage{InputTokens: 1000, OutputTokens: 100}}
	m := NewMeteredLLM(inner, "gpt-5-mini", svc)

	ch, err := m.GenerateWithToolsStream(ctx, "q", nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	drainStream(t, ch)

	if len(repo.events) != 1 {
		t.Fatalf("recorded %d usage events, want 1 aggregate", len(repo.events))
	}
	if repo.events[0].TokensIn != 3000 || repo.events[0].TokensOut != 300 {
		t.Fatalf("event = %+v, want in=3000 out=300", repo.events[0])
	}
}

// multiIterationLLM stands in for the SDK's tool-calling loop: several HTTP
// requests, each reporting its own usage to the collector, one stream.
type multiIterationLLM struct {
	fakeStreamingLLM
	iterations int
	per        llmusage.Usage
}

func (f *multiIterationLLM) GenerateWithToolsStream(ctx context.Context, _ string, _ []interfaces.Tool, _ ...interfaces.GenerateOption) (<-chan interfaces.StreamEvent, error) {
	col := llmusage.CollectorFrom(ctx)
	ch := make(chan interfaces.StreamEvent, f.iterations+1)
	go func() {
		defer close(ch)
		for i := 0; i < f.iterations; i++ {
			col.Add(f.per)
			ch <- interfaces.StreamEvent{Type: interfaces.StreamEventContentDelta, Content: "step"}
		}
	}()
	return ch, nil
}
