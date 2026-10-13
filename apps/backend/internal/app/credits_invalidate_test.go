package app

import (
	"context"
	"errors"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// RIZ-83 R13, AC8: an approved top-up drops the cached "exhausted" verdict, so
// the next check reads the new balance instead of refusing for up to a minute.
func TestInvalidateBudgetLetsTheNextCheckSeeATopUp(t *testing.T) {
	credits := &stubCredits{record: &domain.CompanyCredits{CompanyID: "co-a", BalanceMicroUSD: -1, MonthlyGrantMicroUSD: 500_000}}
	cache := newMapBudgetCache()
	svc := newCreditService(enforcing(), credits, &stubLLMCreds{}, cache)
	ctx := context.Background()

	st, _ := svc.CheckBudget(ctx, "co-a")
	if st.Verdict != BudgetExhausted {
		t.Fatalf("before: %s", st.Verdict)
	}
	// The approval's balance write, which the cached verdict does not know of.
	credits.record = &domain.CompanyCredits{CompanyID: "co-a", BalanceMicroUSD: 5_120_000, MonthlyGrantMicroUSD: 5_120_000}
	if st, _ := svc.CheckBudget(ctx, "co-a"); st.Verdict != BudgetExhausted {
		t.Fatalf("the cache should still be answering: %s", st.Verdict)
	}

	svc.InvalidateBudget(ctx, "co-a")
	if _, ok := cache.entries[budgetCacheKey("co-a")]; ok {
		t.Fatal("the verdict is still cached")
	}
	if st, _ := svc.CheckBudget(ctx, "co-a"); st.Verdict != BudgetOK || st.BalanceMicroUSD != 5_120_000 {
		t.Errorf("after: %+v, want OK at 5120000", st)
	}
}

// A delete that fails is logged and nothing more: the approval stands.
type failingDeleteCache struct{ *mapBudgetCache }

func (failingDeleteCache) Delete(context.Context, string) error {
	return errors.New("redis: connection refused")
}

func TestInvalidateBudgetSurvivesAFailedDelete(t *testing.T) {
	svc := newCreditService(enforcing(), &stubCredits{}, &stubLLMCreds{}, failingDeleteCache{newMapBudgetCache()})
	svc.InvalidateBudget(context.Background(), "co-a") // must not panic or block
	var nilSvc *UsageService
	nilSvc.InvalidateBudget(context.Background(), "co-a")
	(&UsageService{}).InvalidateBudget(context.Background(), "co-a")
}
