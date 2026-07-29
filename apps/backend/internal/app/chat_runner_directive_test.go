package app

import (
	"context"
	"strings"
	"sync"
	"testing"

	sdkagent "github.com/Ingenimax/agent-sdk-go/pkg/agent"
	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/llmtenant"
	"github.com/fauzanebd/argentum/internal/queue"
)

// The middle link of T-A2b: `POST /v1/reports` hands ChatRunner a prompt and a
// directive as two fields, and the runner has to keep them apart — the
// directive into the agent's system prompt, the prompt into the input the
// guardrails inspect. Both ends of that are tested elsewhere (the handler's
// enqueue in `transport/http/handlers`, the system prompt in `bootstrap`);
// this is the step between them, which is where re-folding the two together
// would be a one-line change nothing else would notice.

// directiveLLM records what the agent was asked, and answers.
type directiveLLM struct {
	mu     sync.Mutex
	inputs []string
}

func (l *directiveLLM) Generate(_ context.Context, prompt string, _ ...interfaces.GenerateOption) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.inputs = append(l.inputs, prompt)
	return "Your report is ready.", nil
}

func (l *directiveLLM) GenerateWithTools(ctx context.Context, prompt string, _ []interfaces.Tool, opts ...interfaces.GenerateOption) (string, error) {
	return l.Generate(ctx, prompt, opts...)
}
func (l *directiveLLM) GenerateDetailed(context.Context, string, ...interfaces.GenerateOption) (*interfaces.LLMResponse, error) {
	panic("unexpected GenerateDetailed")
}
func (l *directiveLLM) GenerateWithToolsDetailed(context.Context, string, []interfaces.Tool, ...interfaces.GenerateOption) (*interfaces.LLMResponse, error) {
	panic("unexpected GenerateWithToolsDetailed")
}
func (l *directiveLLM) Name() string { return "directive-stub" }

// Blocking, not streaming: the streaming path needs a provider that can
// actually stream, and the split under test happens before either is chosen.
func (l *directiveLLM) SupportsStreaming() bool { return false }

func (l *directiveLLM) seen() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.inputs...)
}

// fixedLLM hands the same client back for both tiers.
type fixedLLM struct{ llm interfaces.LLM }

func (f fixedLLM) For(context.Context, string, domain.LLMTier) (interfaces.LLM, *llmtenant.EffectiveProfile, error) {
	return f.llm, &llmtenant.EffectiveProfile{Interface: "openai"}, nil
}

// noConnections is a tenant with no data sources registered — enough for a
// turn to run, and it keeps the source catalog out of the assertions.
type noConnections struct{}

func (noConnections) ListByCompany(context.Context, string) ([]*domain.DBConnection, error) {
	return nil, nil
}
func (noConnections) Create(context.Context, *domain.DBConnection) error { panic("unexpected Create") }
func (noConnections) GetByID(context.Context, string) (*domain.DBConnection, error) {
	panic("unexpected GetByID")
}
func (noConnections) GetDefaultForCompany(context.Context, string) (*domain.DBConnection, error) {
	panic("unexpected GetDefaultForCompany")
}
func (noConnections) Update(context.Context, *domain.DBConnection) error { panic("unexpected Update") }
func (noConnections) Delete(context.Context, string) error               { panic("unexpected Delete") }
func (noConnections) SetDefault(context.Context, string, string) error {
	panic("unexpected SetDefault")
}

// runnerForTurn builds a ChatRunner that can run a whole turn against a stub
// model, and returns the spec the factory was handed.
func runnerForTurn(t *testing.T, llm interfaces.LLM) (*ChatRunner, *AgentSpec) {
	t.Helper()
	threads := quietThreadRepo{&fakeThreadRepo{latestErr: domain.ErrNotFound}}
	svc := NewThreadService(threads, stubMessages{}, nil, nil, ThreadServiceConfig{
		IdleMinutes: 30, SummaryEveryNTurns: 8,
	})

	var got AgentSpec
	factory := func(spec AgentSpec) (*sdkagent.Agent, error) {
		got = spec
		// Composed here rather than through bootstrap's factory, which this
		// package cannot import. What is under test is what ChatRunner *puts*
		// in the spec, not what bootstrap does with it.
		return sdkagent.NewAgent(
			sdkagent.WithLLM(spec.Primary),
			sdkagent.WithSystemPrompt("You are Argentum.\n\n"+spec.SystemAddendum),
			sdkagent.WithMaxIterations(2),
		)
	}

	var seq []string
	r := NewChatRunner(svc, stubMessages{}, threads, noConnections{}, factory,
		fixedLLM{llm}, orderedBus{seq: &seq}, nil, nil, nil, 20)
	return r, &got
}

func TestTheDirectiveReachesTheAgentWithoutPassingThroughTheMessage(t *testing.T) {
	llm := &directiveLLM{}
	r, spec := runnerForTurn(t, llm)

	directive := ReportDirective(ReportDirectiveInput{Format: domain.DocumentFormatPDF})
	const prompt = "Total sales by month for the last six months."

	if err := r.Run(context.Background(), queue.ChatRunPayload{
		CompanyID: "co-1", ThreadID: "th-1", Channel: domain.ChannelAPI,
		Message: prompt, Directive: directive, UserMsgID: "msg-1",
		APIReportID: "rep-1",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if spec.SystemAddendum != directive {
		t.Errorf("the factory was not given the directive:\n  got:  %q\n  want: %q",
			spec.SystemAddendum, directive)
	}

	seen := llm.seen()
	if len(seen) == 0 {
		t.Fatal("the model was never called")
	}
	for _, input := range seen {
		// The guardrails run on exactly this string. Anything of ours in it is
		// a turn the injection classifier can refuse (api-contract.md §5.2).
		for _, marker := range []string{"REPORT REQUEST", "You MUST", "generate_document"} {
			if strings.Contains(input, marker) {
				t.Errorf("the agent's input carries %q, which is what the guardrail refuses:\n%s", marker, input)
			}
		}
		if !strings.Contains(input, prompt) {
			t.Errorf("the agent's input does not carry the caller's prompt:\n%s", input)
		}
	}
}

// A turn with no directive is unchanged — every dashboard, WhatsApp, Discord
// and Lark turn is one of these.
func TestATurnWithoutADirectivePassesNothingExtra(t *testing.T) {
	llm := &directiveLLM{}
	r, spec := runnerForTurn(t, llm)

	if err := r.Run(context.Background(), queue.ChatRunPayload{
		CompanyID: "co-1", ThreadID: "th-1", Channel: domain.ChannelDashboard,
		Message: "What were our total sales last month?", UserMsgID: "msg-1", UserID: "user-1",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if spec.SystemAddendum != "" {
		t.Errorf("addendum = %q, want empty on an ordinary turn", spec.SystemAddendum)
	}
}

// The small-talk short-circuit answers without an agent, which on a report
// turn would mean a `completed` report with no document and a friendly
// sentence — the silent failure this ticket exists to remove, arriving by a
// different road.
func TestASmallTalkPromptStillRunsTheAgentOnAReportTurn(t *testing.T) {
	llm := &directiveLLM{}
	r, _ := runnerForTurn(t, llm)

	if err := r.Run(context.Background(), queue.ChatRunPayload{
		CompanyID: "co-1", ThreadID: "th-1", Channel: domain.ChannelAPI,
		Message: "hi", Directive: ReportDirective(ReportDirectiveInput{Format: domain.DocumentFormatPDF}),
		UserMsgID: "msg-1", APIReportID: "rep-1",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(llm.seen()) == 0 {
		t.Error("a report turn was short-circuited as small talk; nothing would have been generated")
	}
}
