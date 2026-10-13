// Package creditwiring builds the budget-checking UsageService that every
// process enforcing credit uses — cmd/api, the worker's stack in
// internal/bootstrap, and cmd/discord — from one function (RIZ-89 R4).
//
// **Why a package for one function.** Before RIZ-89 each of the three wrote its
// own `app.CreditPolicy{…}` literal from the same three config fields, and that
// was fine while all three meant the same thing. Free credit by approval made a
// fourth field decide whether a process may hand out money, and the risk the
// ticket names first is "a process still provisions free credit (a missed
// wiring or call site)". Three literals are three places to miss it; one
// function is one, and creditwiring_test.go reads the three call sites to hold
// them to it. It is not in internal/bootstrap because cmd/api and cmd/discord
// do not import the worker's stack, and not in internal/app because that
// package does not read config.
package creditwiring

import (
	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/domain"
)

// Policy is the credit policy config describes. AutoGrant is true only when
// CREDITS_GRANT_REQUIRES_APPROVAL is explicitly false: approval is the default,
// and app.CreditPolicy's zero value is approval too.
func Policy(cfg *config.Config) app.CreditPolicy {
	return app.CreditPolicy{
		Enforce:       cfg.CreditsEnforcementEnabled,
		WarnPct:       cfg.CreditsWarningThresholdPct,
		GrantMicroUSD: cfg.CreditsDefaultGrantMicroUSD(),
		AutoGrant:     !cfg.CreditsGrantRequiresApproval,
	}
}

// UsageService is the metering-and-budget service of a process that checks
// budgets: the policy above, the request repository a check files free-credit
// requests into, and the shared verdict cache. grants may be nil — the check
// then refuses a company with no grant rather than granting it.
func UsageService(
	cfg *config.Config,
	usage domain.UsageRepository,
	credits domain.CreditsRepository,
	llmCreds domain.CompanyLLMCredentialRepository,
	grants domain.CreditGrantRequestRepository,
	cache app.BudgetCache,
) *app.UsageService {
	svc := app.NewUsageService(usage, credits, app.DefaultPricing).
		WithCredits(Policy(cfg), llmCreds, cache)
	if grants != nil {
		svc = svc.WithGrantRequests(grants)
	}
	return svc
}
