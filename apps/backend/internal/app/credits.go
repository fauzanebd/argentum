package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
)

// Credit enforcement (T-03, finding B-1).
//
// UsageService.append decrements the balance and ignores the result, and
// nothing reads it back. A tenant on platform LLM keys could spend without
// limit; T-13 makes that reachable by a script in someone else's CI config.
//
// Three decisions in here are not obvious from the ticket:
//
//  1. **The grant is provisioned here, not by a migration.** Nothing has ever
//     credited a company. `company_credits` rows are created only by
//     CreditsRepo.Decrement, which upserts a *negative* balance — so on the
//     day enforcement is switched on, every tenant that has ever run a turn is
//     at or below zero and every tenant that has not has no row at all.
//     Enforcing "balance <= 0 means refuse" against that state is a global
//     outage, not a spend ceiling. CheckBudget therefore provisions
//     CreditPolicy.GrantMicroUSD the first time it sees a company with no
//     grant, which also forgives pre-enforcement usage exactly once. Doing it
//     in Go rather than in SQL keeps one owner for the number: an operator
//     changing CREDITS_DEFAULT_GRANT_USD does not have to reconcile it with a
//     value frozen into an applied migration.
//
//  2. **A grant is what makes BudgetWarning computable at all.** "<20%
//     remaining" needs a denominator, and monthly_grant_micro_usd is the only
//     column that can be one.
//
//  3. **A repository failure fails open.** A credits lookup that errors
//     returns BudgetOK with a Warn, matching the house rule for optional
//     subsystems. The alternative — refusing every turn when the control DB
//     hiccups — turns a billing check into a product outage, and the control
//     DB being down already fails the turn one step later for its own reasons.

// BudgetVerdict is what a budget check concluded. The three values are the
// whole vocabulary: everything downstream branches on these, not on the
// balance.
type BudgetVerdict string

const (
	// BudgetOK — the turn may run.
	BudgetOK BudgetVerdict = "ok"
	// BudgetWarning — the turn may run, and the tenant should be told they
	// are close to the end of their credit.
	BudgetWarning BudgetVerdict = "warning"
	// BudgetExhausted — the turn is refused before any money is spent.
	BudgetExhausted BudgetVerdict = "exhausted"
)

// BudgetState is one company's spend position at the moment it was checked.
// It carries the balance and grant as well as the verdict because the
// dashboard's warning banner has to say how much is left, and re-reading the
// balance to render it would defeat the cache this check exists behind.
type BudgetState struct {
	Verdict         BudgetVerdict `json:"verdict"`
	BalanceMicroUSD int64         `json:"balance_micro_usd"`
	GrantMicroUSD   int64         `json:"grant_micro_usd"`
	// RemainingPct is the balance as a percentage of the grant, 0–100. Zero
	// when there is no grant to measure against.
	RemainingPct int `json:"remaining_pct"`
	// BYOLLM marks a tenant running on their own LLM credentials. They pay
	// their provider directly, so no balance was consulted and none applies.
	BYOLLM bool `json:"byo_llm"`
}

// Blocked reports whether this state must refuse the turn.
func (b BudgetState) Blocked() bool { return b.Verdict == BudgetExhausted }

// CreditsExhaustedMessage is the single refusal every channel sends. It is
// one string rather than one per channel because a WhatsApp user and a
// dashboard user hitting the same wall should be told the same thing, and
// because the alternative is four wordings that drift. It names the fix and
// avoids the word "error": nothing has gone wrong.
const CreditsExhaustedMessage = "This workspace has used all of its Argentum credits, " +
	"so I can't run that right now. Ask an admin to top up the balance — " +
	"current usage is on the Usage page in the dashboard."

// budgetOK is the permissive answer returned by every path that declines to
// enforce — enforcement off, no tenant in context, a repository that errored.
var budgetOK = BudgetState{Verdict: BudgetOK}

// CreditPolicy is the operator's half of enforcement: whether to enforce at
// all, where the warning line sits, and what a new company starts with.
type CreditPolicy struct {
	// Enforce is the kill switch (CREDITS_ENFORCEMENT_ENABLED). False
	// restores the pre-T-03 behaviour exactly: balances are still recorded
	// and decremented, nothing is refused.
	Enforce bool
	// WarnPct is the remaining-percentage-of-grant threshold below which a
	// turn still runs but reports BudgetWarning.
	WarnPct int
	// GrantMicroUSD is what a company with no grant is provisioned on first
	// check. Zero with Enforce true refuses every platform-key tenant, which
	// is a legitimate operator choice and not a default.
	GrantMicroUSD int64
}

// Normalize clamps operator input rather than rejecting it, matching the
// constructors elsewhere in this package.
func (p CreditPolicy) Normalize() CreditPolicy {
	if p.WarnPct < 0 {
		p.WarnPct = 0
	}
	if p.WarnPct > 100 {
		p.WarnPct = 100
	}
	if p.GrantMicroUSD < 0 {
		p.GrantMicroUSD = 0
	}
	return p
}

// BudgetCache keeps the balance lookup off the per-turn hot path. It is
// deliberately a byte store rather than a BudgetState store so the Redis
// implementation can live next to this file without internal/cache — which
// owns SQL-result and conversation caching, with its own client and its own
// TTL policy — having to import internal/app.
type BudgetCache interface {
	Get(ctx context.Context, key string) ([]byte, bool)
	Set(ctx context.Context, key string, val []byte, ttl time.Duration)
}

// budgetCacheTTL bounds how stale a verdict may be. The window cuts both
// ways: a tenant can overspend by up to one TTL of turns, and a tenant whose
// balance was just topped up stays refused for up to one TTL.
const budgetCacheTTL = 60 * time.Second

// WithCredits turns on enforcement. llmCreds is required, not optional: the
// "never block a tenant using their own key" rule cannot be honoured without
// it, and enforcing while blind to it would bill-block the tenants who are
// not costing us anything. A nil repository therefore disables enforcement
// rather than silently changing what it means. cache may be nil.
func (s *UsageService) WithCredits(policy CreditPolicy, llmCreds domain.CompanyLLMCredentialRepository, cache BudgetCache) *UsageService {
	if policy.Enforce && llmCreds == nil {
		logrus.Warn("credit enforcement requested without an LLM credential repository; enforcement disabled")
		policy.Enforce = false
	}
	s.credit = policy.Normalize()
	s.llmCreds = llmCreds
	s.budgetCache = cache
	return s
}

// CheckBudget resolves whether companyID may spend. Callers refuse the turn
// on BudgetState.Blocked and surface BudgetWarning to the user; nothing else
// should read the balance directly.
func (s *UsageService) CheckBudget(ctx context.Context, companyID string) (BudgetState, error) {
	if !s.credit.Enforce || companyID == "" {
		return budgetOK, nil
	}
	if st, ok := s.cachedBudget(ctx, companyID); ok {
		return st, nil
	}

	// BYO first. A tenant on their own key never touches the balance — we
	// never decrement it for them either — so consulting one would refuse a
	// turn on a number that has no meaning for that company.
	byo, err := s.hasOwnPrimaryKey(ctx, companyID)
	if err != nil {
		logrus.WithError(err).WithField("company_id", companyID).
			Warn("tenant LLM credential lookup failed; allowing the turn")
		return budgetOK, nil
	}
	if byo {
		st := BudgetState{Verdict: BudgetOK, BYOLLM: true}
		s.cacheBudget(ctx, companyID, st)
		return st, nil
	}

	credits, err := s.credits.Get(ctx, companyID)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		credits = s.provisionGrant(ctx, companyID)
	case err != nil:
		logrus.WithError(err).WithField("company_id", companyID).
			Warn("credit balance lookup failed; allowing the turn")
		return budgetOK, nil
	case credits == nil || credits.MonthlyGrantMicroUSD == 0:
		// Never granted anything. The balance is whatever pre-enforcement
		// metering happened to subtract from zero, which is not a debt the
		// tenant agreed to — see (1) at the top of this file. The nil check
		// is not redundant: a repository returning (nil, nil) is a contract
		// violation, and dereferencing it here would take down every turn.
		credits = s.provisionGrant(ctx, companyID)
	}

	st := BudgetState{
		BalanceMicroUSD: credits.BalanceMicroUSD,
		GrantMicroUSD:   credits.MonthlyGrantMicroUSD,
		RemainingPct:    remainingPct(credits.BalanceMicroUSD, credits.MonthlyGrantMicroUSD),
	}
	switch {
	case credits.BalanceMicroUSD <= 0:
		st.Verdict = BudgetExhausted
	case credits.MonthlyGrantMicroUSD > 0 && st.RemainingPct < s.credit.WarnPct:
		st.Verdict = BudgetWarning
	default:
		st.Verdict = BudgetOK
	}
	s.cacheBudget(ctx, companyID, st)
	return st, nil
}

// provisionGrant writes the configured grant for a company that has none and
// returns the record as if it had been read. A write failure is not fatal:
// the returned value still reflects the grant, so the turn proceeds and the
// next check retries the write.
func (s *UsageService) provisionGrant(ctx context.Context, companyID string) *domain.CompanyCredits {
	c := &domain.CompanyCredits{
		CompanyID:            companyID,
		BalanceMicroUSD:      s.credit.GrantMicroUSD,
		MonthlyGrantMicroUSD: s.credit.GrantMicroUSD,
	}
	if err := s.credits.Upsert(ctx, c); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id":  companyID,
			"grant_micro": s.credit.GrantMicroUSD,
		}).Warn("credit grant provisioning failed; retrying on the next check")
		return c
	}
	logrus.WithFields(logrus.Fields{
		"company_id":  companyID,
		"grant_micro": s.credit.GrantMicroUSD,
	}).Info("provisioned initial credit grant")
	return c
}

// hasOwnPrimaryKey reports whether the tenant supplies their own primary-tier
// API key. A primary row that only overrides the model or base URL does not
// count — llmtenant.Resolver.merge swaps the key only when APIKeyEncrypted is
// non-empty, so such a tenant is still spending ours.
func (s *UsageService) hasOwnPrimaryKey(ctx context.Context, companyID string) (bool, error) {
	rows, err := s.llmCreds.GetByCompany(ctx, companyID)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if row.Tier == domain.LLMTierPrimary && len(row.APIKeyEncrypted) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// remainingPct floors at 0 and caps at 100 so a top-up above the grant does
// not report 340% remaining to a progress bar.
func remainingPct(balance, grant int64) int {
	if grant <= 0 || balance <= 0 {
		return 0
	}
	pct := balance * 100 / grant
	if pct > 100 {
		return 100
	}
	return int(pct)
}

func budgetCacheKey(companyID string) string { return "credits:budget:" + companyID }

func (s *UsageService) cachedBudget(ctx context.Context, companyID string) (BudgetState, bool) {
	if s.budgetCache == nil {
		return BudgetState{}, false
	}
	raw, ok := s.budgetCache.Get(ctx, budgetCacheKey(companyID))
	if !ok {
		return BudgetState{}, false
	}
	var st BudgetState
	if err := json.Unmarshal(raw, &st); err != nil {
		return BudgetState{}, false
	}
	return st, true
}

func (s *UsageService) cacheBudget(ctx context.Context, companyID string, st BudgetState) {
	if s.budgetCache == nil {
		return
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return
	}
	s.budgetCache.Set(ctx, budgetCacheKey(companyID), raw, budgetCacheTTL)
}
