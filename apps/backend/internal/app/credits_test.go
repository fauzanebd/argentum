package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// stubCredits is a CreditsRepository whose stored record and error are set
// per test. It counts Get calls so the cache test can prove the second check
// never reached the database.
type stubCredits struct {
	record  *domain.CompanyCredits
	getErr  error
	gets    int
	upserts []*domain.CompanyCredits
	upErr   error
}

func (s *stubCredits) Get(context.Context, string) (*domain.CompanyCredits, error) {
	s.gets++
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.record, nil
}

func (s *stubCredits) Upsert(_ context.Context, c *domain.CompanyCredits) error {
	s.upserts = append(s.upserts, c)
	if s.upErr != nil {
		return s.upErr
	}
	s.record = c
	return nil
}

func (s *stubCredits) Decrement(context.Context, string, int64) error { return nil }

// stubLLMCreds answers the "does this tenant pay their own provider" question.
type stubLLMCreds struct {
	rows []*domain.CompanyLLMCredential
	err  error
}

func (s *stubLLMCreds) GetByCompany(context.Context, string) ([]*domain.CompanyLLMCredential, error) {
	return s.rows, s.err
}
func (s *stubLLMCreds) Upsert(context.Context, *domain.CompanyLLMCredential) error { return nil }
func (s *stubLLMCreds) Delete(context.Context, string, domain.LLMTier) error       { return nil }

// mapBudgetCache is an in-memory BudgetCache. TTL is ignored: no test here
// asserts on expiry, and a fake clock would only test time.Now.
type mapBudgetCache struct{ entries map[string][]byte }

func newMapBudgetCache() *mapBudgetCache {
	return &mapBudgetCache{entries: map[string][]byte{}}
}
func (m *mapBudgetCache) Get(_ context.Context, key string) ([]byte, bool) {
	v, ok := m.entries[key]
	return v, ok
}
func (m *mapBudgetCache) Set(_ context.Context, key string, val []byte, _ time.Duration) {
	m.entries[key] = val
}

const testGrant = int64(25_000_000) // $25

// llm is the interface type, not *stubLLMCreds, so that passing nil produces
// a nil interface rather than a non-nil interface holding a nil pointer —
// which is what WithCredits' guard actually tests for.
func newCreditService(policy CreditPolicy, credits *stubCredits, llm domain.CompanyLLMCredentialRepository, cache BudgetCache) *UsageService {
	return NewUsageService(&fakeUsageRepo{}, credits, DefaultPricing).
		WithCredits(policy, llm, cache)
}

func enforcing() CreditPolicy {
	return CreditPolicy{Enforce: true, WarnPct: 20, GrantMicroUSD: testGrant}
}

func TestCheckBudgetVerdicts(t *testing.T) {
	cases := []struct {
		name        string
		policy      CreditPolicy
		record      *domain.CompanyCredits
		want        BudgetVerdict
		wantPct     int
		wantUpserts int
	}{
		{
			name:   "healthy balance",
			policy: enforcing(),
			record: &domain.CompanyCredits{BalanceMicroUSD: 20_000_000, MonthlyGrantMicroUSD: testGrant},
			want:   BudgetOK, wantPct: 80,
		},
		{
			// 4 of 25 dollars is 16%, under the 20% line.
			name:   "under the warning threshold",
			policy: enforcing(),
			record: &domain.CompanyCredits{BalanceMicroUSD: 4_000_000, MonthlyGrantMicroUSD: testGrant},
			want:   BudgetWarning, wantPct: 16,
		},
		{
			// Exactly at the line is not under it. Pinned because an
			// off-by-one here means every tenant sits in a permanent warning.
			name:   "exactly at the warning threshold",
			policy: enforcing(),
			record: &domain.CompanyCredits{BalanceMicroUSD: 5_000_000, MonthlyGrantMicroUSD: testGrant},
			want:   BudgetOK, wantPct: 20,
		},
		{
			name:   "zero balance",
			policy: enforcing(),
			record: &domain.CompanyCredits{BalanceMicroUSD: 0, MonthlyGrantMicroUSD: testGrant},
			want:   BudgetExhausted,
		},
		{
			name:   "overdrawn",
			policy: enforcing(),
			record: &domain.CompanyCredits{BalanceMicroUSD: -3_000_000, MonthlyGrantMicroUSD: testGrant},
			want:   BudgetExhausted,
		},
		{
			// The pre-T-03 state of every tenant that ever ran a turn: a
			// negative balance that no grant was ever set against. It must be
			// provisioned, not refused.
			name:   "never granted, negative from pre-enforcement metering",
			policy: enforcing(),
			record: &domain.CompanyCredits{BalanceMicroUSD: -8_000_000, MonthlyGrantMicroUSD: 0},
			want:   BudgetOK, wantPct: 100, wantUpserts: 1,
		},
		{
			// An operator who sets the grant to zero has asked for exactly
			// this. It is a choice, so it is pinned rather than defended.
			name:   "zero grant refuses everyone",
			policy: CreditPolicy{Enforce: true, WarnPct: 20, GrantMicroUSD: 0},
			record: nil,
			want:   BudgetExhausted, wantUpserts: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			credits := &stubCredits{record: tc.record}
			if tc.record == nil {
				credits.getErr = domain.ErrNotFound
			}
			svc := newCreditService(tc.policy, credits, &stubLLMCreds{}, nil)

			st, err := svc.CheckBudget(context.Background(), "c1")
			if err != nil {
				t.Fatalf("CheckBudget = %v, want nil", err)
			}
			if st.Verdict != tc.want {
				t.Errorf("verdict = %q, want %q", st.Verdict, tc.want)
			}
			if st.RemainingPct != tc.wantPct {
				t.Errorf("remaining_pct = %d, want %d", st.RemainingPct, tc.wantPct)
			}
			if got := len(credits.upserts); got != tc.wantUpserts {
				t.Errorf("upserts = %d, want %d", got, tc.wantUpserts)
			}
			if st.Blocked() != (tc.want == BudgetExhausted) {
				t.Errorf("Blocked() = %v for verdict %q", st.Blocked(), st.Verdict)
			}
		})
	}
}

func TestCheckBudgetProvisionsTheGrantOnce(t *testing.T) {
	credits := &stubCredits{getErr: domain.ErrNotFound}
	svc := newCreditService(enforcing(), credits, &stubLLMCreds{}, nil)

	st, err := svc.CheckBudget(context.Background(), "c1")
	if err != nil {
		t.Fatalf("first CheckBudget = %v", err)
	}
	if st.Verdict != BudgetOK || st.BalanceMicroUSD != testGrant {
		t.Fatalf("first check = %+v, want an OK verdict at the full grant", st)
	}
	if len(credits.upserts) != 1 {
		t.Fatalf("upserts after the first check = %d, want 1", len(credits.upserts))
	}
	if credits.upserts[0].MonthlyGrantMicroUSD != testGrant {
		t.Errorf("provisioned grant = %d, want %d", credits.upserts[0].MonthlyGrantMicroUSD, testGrant)
	}

	// The stub now returns what was written. A second provisioning would
	// refund a tenant every turn, which is worse than not enforcing at all.
	credits.getErr = nil
	credits.record.BalanceMicroUSD = 1_000_000
	st, err = svc.CheckBudget(context.Background(), "c1")
	if err != nil {
		t.Fatalf("second CheckBudget = %v", err)
	}
	if len(credits.upserts) != 1 {
		t.Errorf("upserts after the second check = %d, want 1 — the grant was re-provisioned", len(credits.upserts))
	}
	if st.BalanceMicroUSD != 1_000_000 {
		t.Errorf("balance = %d, want the spent-down 1000000", st.BalanceMicroUSD)
	}
}

func TestCheckBudgetNeverBlocksTenantsOnTheirOwnKey(t *testing.T) {
	// Overdrawn against a real grant: without the BYO rule this is a refusal.
	record := &domain.CompanyCredits{BalanceMicroUSD: -50_000_000, MonthlyGrantMicroUSD: testGrant}

	cases := []struct {
		name    string
		rows    []*domain.CompanyLLMCredential
		want    BudgetVerdict
		wantBYO bool
	}{
		{
			name: "primary tier with its own key",
			rows: []*domain.CompanyLLMCredential{
				{Tier: domain.LLMTierPrimary, APIKeyEncrypted: []byte("ciphertext")},
			},
			want: BudgetOK, wantBYO: true,
		},
		{
			// llmtenant.Resolver.merge only swaps the key when
			// APIKeyEncrypted is non-empty, so a model-only override is still
			// spending the platform key and is still ours to bill.
			name: "primary tier overriding only the model",
			rows: []*domain.CompanyLLMCredential{
				{Tier: domain.LLMTierPrimary, Model: "gpt-4o"},
			},
			want: BudgetExhausted,
		},
		{
			// A tenant supplying their own cheap model for classification is
			// not paying for the expensive one.
			name: "light tier with a key, primary without",
			rows: []*domain.CompanyLLMCredential{
				{Tier: domain.LLMTierLight, APIKeyEncrypted: []byte("ciphertext")},
			},
			want: BudgetExhausted,
		},
		{
			name: "no overrides at all",
			rows: nil,
			want: BudgetExhausted,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			credits := &stubCredits{record: record}
			svc := newCreditService(enforcing(), credits, &stubLLMCreds{rows: tc.rows}, nil)

			st, err := svc.CheckBudget(context.Background(), "c1")
			if err != nil {
				t.Fatalf("CheckBudget = %v", err)
			}
			if st.Verdict != tc.want {
				t.Errorf("verdict = %q, want %q", st.Verdict, tc.want)
			}
			if st.BYOLLM != tc.wantBYO {
				t.Errorf("BYOLLM = %v, want %v", st.BYOLLM, tc.wantBYO)
			}
			if tc.wantBYO && credits.gets != 0 {
				t.Errorf("credits.Get called %d times for a BYO tenant; the balance has no meaning for them", credits.gets)
			}
		})
	}
}

func TestCheckBudgetFailsOpen(t *testing.T) {
	// Overdrawn, so anything other than a deliberate fail-open would refuse.
	record := &domain.CompanyCredits{BalanceMicroUSD: -1, MonthlyGrantMicroUSD: testGrant}
	boom := errors.New("connection refused")

	cases := []struct {
		name    string
		policy  CreditPolicy
		credits *stubCredits
		llm     *stubLLMCreds
		company string
	}{
		{
			name:    "enforcement disabled",
			policy:  CreditPolicy{Enforce: false, WarnPct: 20, GrantMicroUSD: testGrant},
			credits: &stubCredits{record: record},
			llm:     &stubLLMCreds{},
			company: "c1",
		},
		{
			name:    "no tenant in context",
			policy:  enforcing(),
			credits: &stubCredits{record: record},
			llm:     &stubLLMCreds{},
			company: "",
		},
		{
			name:    "credits lookup failed",
			policy:  enforcing(),
			credits: &stubCredits{getErr: boom},
			llm:     &stubLLMCreds{},
			company: "c1",
		},
		{
			name:    "llm credential lookup failed",
			policy:  enforcing(),
			credits: &stubCredits{record: record},
			llm:     &stubLLMCreds{err: boom},
			company: "c1",
		},
		{
			// A repository that violates its contract must not panic the
			// turn; it is treated as "no row".
			name:    "credits repository returned no record and no error",
			policy:  enforcing(),
			credits: &stubCredits{},
			llm:     &stubLLMCreds{},
			company: "c1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newCreditService(tc.policy, tc.credits, tc.llm, nil)
			st, err := svc.CheckBudget(context.Background(), tc.company)
			if err != nil {
				t.Fatalf("CheckBudget = %v, want nil", err)
			}
			if st.Blocked() {
				t.Errorf("verdict = %q, want the turn allowed", st.Verdict)
			}
		})
	}
}

func TestCheckBudgetWithoutLLMRepositoryDoesNotEnforce(t *testing.T) {
	// Enforcing while blind to BYO tenants would bill-block the companies
	// costing us nothing, so a nil repository disables enforcement instead.
	credits := &stubCredits{record: &domain.CompanyCredits{BalanceMicroUSD: -1, MonthlyGrantMicroUSD: testGrant}}
	svc := newCreditService(enforcing(), credits, nil, nil)

	st, err := svc.CheckBudget(context.Background(), "c1")
	if err != nil {
		t.Fatalf("CheckBudget = %v", err)
	}
	if st.Blocked() {
		t.Errorf("verdict = %q, want the turn allowed", st.Verdict)
	}
}

func TestCheckBudgetUsesTheCache(t *testing.T) {
	credits := &stubCredits{record: &domain.CompanyCredits{BalanceMicroUSD: 20_000_000, MonthlyGrantMicroUSD: testGrant}}
	cache := newMapBudgetCache()
	svc := newCreditService(enforcing(), credits, &stubLLMCreds{}, cache)

	first, err := svc.CheckBudget(context.Background(), "c1")
	if err != nil {
		t.Fatalf("first CheckBudget = %v", err)
	}
	if credits.gets != 1 {
		t.Fatalf("credits.Get calls after the first check = %d, want 1", credits.gets)
	}

	// A balance change the cache has not seen must not be observed inside the
	// TTL — that staleness is the deal the cache makes.
	credits.record.BalanceMicroUSD = -1
	second, err := svc.CheckBudget(context.Background(), "c1")
	if err != nil {
		t.Fatalf("second CheckBudget = %v", err)
	}
	if credits.gets != 1 {
		t.Errorf("credits.Get calls after the second check = %d, want 1 — the cache was bypassed", credits.gets)
	}
	if second != first {
		t.Errorf("cached state = %+v, want the first check's %+v", second, first)
	}
}

func TestCreditPolicyNormalize(t *testing.T) {
	cases := []struct {
		name      string
		in        CreditPolicy
		wantPct   int
		wantGrant int64
	}{
		{name: "in range", in: CreditPolicy{WarnPct: 20, GrantMicroUSD: 5}, wantPct: 20, wantGrant: 5},
		{name: "negative percent", in: CreditPolicy{WarnPct: -5}, wantPct: 0},
		{name: "percent over 100", in: CreditPolicy{WarnPct: 400}, wantPct: 100},
		{name: "negative grant", in: CreditPolicy{GrantMicroUSD: -1}, wantGrant: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.in.Normalize()
			if got.WarnPct != tc.wantPct {
				t.Errorf("WarnPct = %d, want %d", got.WarnPct, tc.wantPct)
			}
			if got.GrantMicroUSD != tc.wantGrant {
				t.Errorf("GrantMicroUSD = %d, want %d", got.GrantMicroUSD, tc.wantGrant)
			}
		})
	}
}

func TestRemainingPct(t *testing.T) {
	cases := []struct {
		name    string
		balance int64
		grant   int64
		want    int
	}{
		{name: "half", balance: 50, grant: 100, want: 50},
		{name: "full", balance: 100, grant: 100, want: 100},
		{name: "topped up above the grant clamps", balance: 340, grant: 100, want: 100},
		{name: "overdrawn floors", balance: -20, grant: 100, want: 0},
		{name: "no grant", balance: 50, grant: 0, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := remainingPct(tc.balance, tc.grant); got != tc.want {
				t.Errorf("remainingPct(%d, %d) = %d, want %d", tc.balance, tc.grant, got, tc.want)
			}
		})
	}
}
