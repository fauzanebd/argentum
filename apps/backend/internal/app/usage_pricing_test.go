package app

import (
	"context"
	"testing"

	"github.com/fauzanebd/argentum/internal/agentscope"
	"github.com/fauzanebd/argentum/internal/domain"
)

// recordingCredits captures every decrement so a test can assert that a
// recorded cost actually reaches the balance — the half of metering that
// T-03's budget check will gate on.
type recordingCredits struct{ decrements []int64 }

func (recordingCredits) Get(context.Context, string) (*domain.CompanyCredits, error) {
	return nil, nil
}
func (recordingCredits) Upsert(context.Context, *domain.CompanyCredits) error { return nil }
func (r *recordingCredits) Decrement(_ context.Context, _ string, microUSD int64) error {
	r.decrements = append(r.decrements, microUSD)
	return nil
}

func newUsageService() (*UsageService, *fakeUsageRepo, *recordingCredits) {
	usage := &fakeUsageRepo{}
	credits := &recordingCredits{}
	return NewUsageService(usage, credits, DefaultPricing), usage, credits
}

// T-S2. "What does the Finance agent cost us" is the first question a customer
// with four agents asks, and it is answerable only if every event carries the
// agent. One assignment inside append covers all six Record* methods, so this
// checks a token event and a tool event.
func TestUsageEventsCarryTheTurnsAgent(t *testing.T) {
	svc, usage, _ := newUsageService()
	ctx := agentscope.WithScope(context.Background(), agentscope.Scope{AgentID: "ag-fin"})

	svc.RecordLLM(ctx, "co-1", "th-1", "msg-1", "gpt-4o", 100, 50, 0, 0)
	svc.RecordSQL(ctx, "co-1", "th-1")

	if len(usage.events) != 2 {
		t.Fatalf("events = %d, want 2", len(usage.events))
	}
	for _, e := range usage.events {
		if e.AgentID != "ag-fin" {
			t.Errorf("%s event agent_id = %q, want ag-fin", e.EventType, e.AgentID)
		}
	}
}

// Spend outside a turn — a schema refresh, a reindex — belongs to no agent.
func TestUsageOutsideATurnHasNoAgent(t *testing.T) {
	svc, usage, _ := newUsageService()

	svc.RecordSQL(context.Background(), "co-1", "")

	if len(usage.events) != 1 {
		t.Fatalf("events = %d, want 1", len(usage.events))
	}
	if got := usage.events[0].AgentID; got != "" {
		t.Errorf("agent_id = %q, want empty", got)
	}
}

func TestLookupModelPricing(t *testing.T) {
	cases := []struct {
		name  string
		model string
		want  ModelPricing
		found bool
	}{
		{"exact", "gpt-4o", modelPricing["gpt-4o"], true},
		{"case-insensitive", "GPT-4o", modelPricing["gpt-4o"], true},
		{"surrounding space", "  gpt-4o  ", modelPricing["gpt-4o"], true},
		// Gateways prefix the model with a vendor. OpenRouter is the default
		// provider here, so this branch is on the hot path, not a curiosity.
		{"openrouter prefix", "openai/gpt-4o", modelPricing["gpt-4o"], true},
		{"bedrock-style prefix", "anthropic.claude-3-opus-20240229", modelPricing["claude-3-opus-20240229"], true},
		{"two-level prefix", "openrouter/deepseek/deepseek-v3.2", modelPricing["deepseek-v3.2"], true},
		{"dot inside a known name still matches exactly", "gemini-1.5-pro", modelPricing["gemini-1.5-pro"], true},
		{"unknown", "llama-3-70b", ModelPricing{}, false},
		{"empty", "", ModelPricing{}, false},
		{"trailing separator", "gpt-4o/", ModelPricing{}, false},
		// Known limitation, pinned rather than fixed here: the suffix search
		// splits on the LAST separator, so a vendor prefix in front of a model
		// name that itself contains a dot does not resolve. It falls back to
		// DefaultPricing, which over- rather than under-charges, so it is a
		// reporting inaccuracy and not a billing hole.
		{"prefix in front of a dotted name", "vertex.gemini-1.5-pro", ModelPricing{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := lookupModelPricing(tc.model)
			if ok != tc.found {
				t.Fatalf("lookupModelPricing(%q) found = %v, want %v", tc.model, ok, tc.found)
			}
			if got != tc.want {
				t.Errorf("lookupModelPricing(%q) = %+v, want %+v", tc.model, got, tc.want)
			}
		})
	}
}

func TestLookupModelPricingIsExportedUnchanged(t *testing.T) {
	// The dashboard reads current-model rates through the exported wrapper.
	got, ok := LookupModelPricing("openai/gpt-4o")
	want, wantOK := lookupModelPricing("openai/gpt-4o")
	if ok != wantOK || got != want {
		t.Errorf("LookupModelPricing = (%+v, %v), want (%+v, %v)", got, ok, want, wantOK)
	}
}

func TestRecordLLMPerModelRates(t *testing.T) {
	// 1000 in + 1000 out means the recorded micro-USD is exactly the per-1K
	// rate pair in micro-dollars, which keeps the arithmetic readable.
	cases := []struct {
		name  string
		model string
		want  int64
	}{
		{"gpt-4o", "gpt-4o", 2_500 + 10_000},
		{"gpt-5-mini", "gpt-5-mini", 250 + 2_000},
		{"claude sonnet 4.5", "claude-sonnet-4-5", 3_000 + 15_000},
		{"via a gateway prefix", "openai/gpt-4o", 2_500 + 10_000},
		// An unrecognised model must not be free. Falling back to the flat
		// DefaultPricing is what stops a new model string from silently
		// billing a tenant nothing.
		{"unknown model falls back to DefaultPricing", "some-new-model", 5_000 + 15_000},
		{"empty model falls back to DefaultPricing", "", 5_000 + 15_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, usage, credits := newUsageService()
			svc.RecordLLM(context.Background(), "co-1", "th-1", "msg-1", tc.model, 1000, 1000, 0, 0)

			if len(usage.events) != 1 {
				t.Fatalf("recorded %d events, want 1", len(usage.events))
			}
			e := usage.events[0]
			if e.CostMicroUSD != tc.want {
				t.Errorf("cost = %d micro-USD, want %d", e.CostMicroUSD, tc.want)
			}
			if e.EventType != domain.UsageEventLLMCall {
				t.Errorf("event type = %v, want %v", e.EventType, domain.UsageEventLLMCall)
			}
			if e.Model != tc.model || e.TokensIn != 1000 || e.TokensOut != 1000 {
				t.Errorf("event = %+v, want the model and token counts recorded verbatim", e)
			}
			if len(credits.decrements) != 1 || credits.decrements[0] != tc.want {
				t.Errorf("credit decrements = %v, want [%d]", credits.decrements, tc.want)
			}
		})
	}
}

func TestRecordLLMCacheMultipliers(t *testing.T) {
	// Anthropic prices a cache write at 1.25× the input rate and a cache read
	// at 0.10×. Both are multiples of the INPUT rate, never the output one —
	// getting that wrong is a systematic mis-bill on every cached turn, which
	// on this codebase is most of them.
	const inRate = 0.003 // claude-sonnet-4-5

	cases := []struct {
		name       string
		in, out    int
		create, rd int
		want       int64
	}{
		{"cache create only", 0, 0, 1000, 0, int64(inRate * 1.25 * 1_000_000)},
		{"cache read only", 0, 0, 0, 1000, int64(inRate * 0.10 * 1_000_000)},
		{"both caches", 0, 0, 1000, 1000, int64(inRate*1.25*1_000_000) + int64(inRate*0.10*1_000_000)},
		{"everything", 1000, 1000, 1000, 1000, 3_000 + 15_000 + 3_750 + 300},
		// A cache read is an order of magnitude cheaper than a fresh read of
		// the same tokens. If the multipliers were transposed, caching would
		// look like it cost more than not caching.
		{"a read is cheaper than a plain input token", 0, 0, 0, 1000, 300},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, usage, _ := newUsageService()
			svc.RecordLLM(context.Background(), "co-1", "th-1", "msg-1", "claude-sonnet-4-5",
				tc.in, tc.out, tc.create, tc.rd)

			e := usage.events[0]
			if e.CostMicroUSD != tc.want {
				t.Errorf("cost = %d micro-USD, want %d", e.CostMicroUSD, tc.want)
			}
			if e.CacheCreateTokensIn != tc.create || e.CacheReadTokensIn != tc.rd {
				t.Errorf("cache tokens = (%d, %d), want (%d, %d)",
					e.CacheCreateTokensIn, e.CacheReadTokensIn, tc.create, tc.rd)
			}
		})
	}
}

func TestRecordLLMZeroTokensCostsNothing(t *testing.T) {
	// A provider that returns no usage must produce a zero-cost row rather
	// than a guess — and must not decrement credits, or a failed turn would
	// bill for tokens nobody can account for.
	svc, usage, credits := newUsageService()
	svc.RecordLLM(context.Background(), "co-1", "th-1", "msg-1", "gpt-4o", 0, 0, 0, 0)

	if len(usage.events) != 1 {
		t.Fatalf("recorded %d events, want 1 — a zero-usage turn still has to be visible", len(usage.events))
	}
	if got := usage.events[0].CostMicroUSD; got != 0 {
		t.Errorf("cost = %d, want 0", got)
	}
	if len(credits.decrements) != 0 {
		t.Errorf("credits decremented %v on a zero-cost event", credits.decrements)
	}
}

func TestRecordLLMSubMicroCostsTruncateToZero(t *testing.T) {
	// Cost is stored as whole micro-USD, so a handful of tokens on a cheap
	// model rounds down to nothing. Pinned because it is the mechanism by
	// which a very chatty, very cheap model could accumulate free usage — the
	// per-event loss is under 1 micro-USD, which is the accepted trade for an
	// integer column.
	svc, usage, _ := newUsageService()
	svc.RecordLLM(context.Background(), "co-1", "th-1", "msg-1", "gpt-5-nano", 1, 0, 0, 0)

	if got := usage.events[0].CostMicroUSD; got != 0 {
		t.Errorf("cost = %d, want 0 (a single nano token is worth 0.05 micro-USD)", got)
	}
}

func TestAppendDropsEventsWithNoCompany(t *testing.T) {
	// An event with no company id cannot be attributed or billed, and writing
	// it would put an orphan row in usage_events. Every Record* path goes
	// through this guard.
	svc, usage, credits := newUsageService()
	ctx := context.Background()

	svc.RecordLLM(ctx, "", "th-1", "msg-1", "gpt-4o", 1000, 1000, 0, 0)
	svc.RecordSQL(ctx, "", "th-1")
	svc.RecordDashboard(ctx, "", "th-1")
	svc.RecordDocument(ctx, "", "th-1", "pdf")

	if len(usage.events) != 0 {
		t.Errorf("recorded %d events with no company id, want 0", len(usage.events))
	}
	if len(credits.decrements) != 0 {
		t.Errorf("credits decremented %v with no company id", credits.decrements)
	}
}

func TestFlatRateEventsUseTheConfiguredPricing(t *testing.T) {
	svc, usage, credits := newUsageService()
	ctx := context.Background()

	svc.RecordSQL(ctx, "co-1", "th-1")
	svc.RecordDashboard(ctx, "co-1", "th-1")
	svc.RecordDocument(ctx, "co-1", "th-1", "pdf")

	want := []struct {
		eventType domain.UsageEventType
		cost      int64
	}{
		{domain.UsageEventSQLQuery, int64(DefaultPricing.SQLQueryCost * 1_000_000)},
		// The event type keeps its historical name so the usage series does not
		// split at the decommission date (T-D15).
		{domain.UsageEventMetabaseDashboard, int64(DefaultPricing.DashboardCost * 1_000_000)},
		{domain.UsageEventDocumentGenerated, int64(DefaultPricing.DocumentCost * 1_000_000)},
	}
	if len(usage.events) != len(want) {
		t.Fatalf("recorded %d events, want %d", len(usage.events), len(want))
	}
	for i, w := range want {
		e := usage.events[i]
		if e.EventType != w.eventType {
			t.Errorf("event %d type = %v, want %v", i, e.EventType, w.eventType)
		}
		if e.CostMicroUSD != w.cost {
			t.Errorf("event %d cost = %d, want %d", i, e.CostMicroUSD, w.cost)
		}
	}
	if len(credits.decrements) != len(want) {
		t.Errorf("%d credit decrements, want %d", len(credits.decrements), len(want))
	}

	// The format rides along as metadata so pricing can be split per format
	// later without a migration.
	// events[2], not [3]: the Metabase card recorder was deleted with the rest
	// of that surface (T-D15) and this list is one shorter.
	doc := usage.events[2]
	if got := doc.Metadata["format"]; got != "pdf" {
		t.Errorf("document metadata format = %v, want pdf", got)
	}
}

func TestRecordLLMUsesTheServicePricingNotTheGlobalDefault(t *testing.T) {
	// NewUsageService takes a Pricing value; an unknown model must fall back
	// to THAT, not to the package-level DefaultPricing, or a deployment with
	// custom rates would quietly bill at ours.
	custom := Pricing{LLMInputCostPer1K: 1.0, LLMOutputCostPer1K: 2.0}
	usage := &fakeUsageRepo{}
	svc := NewUsageService(usage, &recordingCredits{}, custom)

	svc.RecordLLM(context.Background(), "co-1", "th-1", "msg-1", "unknown-model", 1000, 1000, 0, 0)

	if got, want := usage.events[0].CostMicroUSD, int64(1_000_000+2_000_000); got != want {
		t.Errorf("cost = %d, want %d", got, want)
	}
}
