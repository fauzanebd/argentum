package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
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
//  1. **The grant is provisioned here, not by a migration.** Before T-03
//     nothing had ever credited a company. `company_credits` rows were created
//     only by CreditsRepo.Decrement, which upserts a *negative* balance — so
//     on the day enforcement was switched on, every tenant that had ever run a
//     turn was at or below zero and every tenant that had not had no row at
//     all. Enforcing "balance <= 0 means refuse" against that state is a
//     global outage, not a spend ceiling. CheckBudget therefore provisions
//     CreditPolicy.GrantMicroUSD the first time it sees a company with no
//     grant, which also forgives pre-enforcement usage exactly once. Doing it
//     in Go rather than in SQL keeps one owner for the number: an operator
//     changing CREDITS_DEFAULT_GRANT_USD does not have to reconcile it with a
//     value frozen into an applied migration.
//
//     Since RIZ-79 that lazy grant is the fallback, not the main road.
//     Signup writes the same number through GrantSignupCredit, so a new
//     workspace has its free credit before its first turn rather than at it,
//     and the Usage page stops showing "$0.00" to someone who has spent
//     nothing. What the lazy path still covers is every company created
//     before that shipped, a signup served by an older pod during a rolling
//     deploy, and a signup whose grant write failed — all of which get the
//     same amount, because there is still only one setting for it.
//
//     **Since RIZ-89 neither road runs by default.** Free credit was farmable
//     — sign-up has no email verification, so a script earned $0.50 a
//     sign-up — and the owner wants every grant approved by hand. With
//     CreditPolicy.AutoGrant false (the zero value, and
//     CREDITS_GRANT_REQUIRES_APPROVAL's default) a company with no grant is
//     refused, and the check files a pending free-credit request for it if it
//     has none; an operator approving that request is the only thing that
//     writes free credit (FreeCreditService). AutoGrant true is the kill
//     switch, and restores both roads above exactly.
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

// BudgetVerdict, BudgetState and CreditsExhaustedMessage live in
// budget_state.go: they cross the wire, and that file is what
// `packages/api-types` is generated from (T-02b).

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
	// GrantMicroUSD is a new company's free credit: the amount a free-credit
	// request is filed for, or — with AutoGrant — written at signup and
	// provisioned on first check. Zero with Enforce true refuses every
	// platform-key tenant with no grant, which is a legitimate operator choice
	// and not a default.
	GrantMicroUSD int64
	// AutoGrant restores the pre-RIZ-89 behaviour: signup writes the grant
	// and CheckBudget provisions it lazily, with no operator in the loop
	// (CREDITS_GRANT_REQUIRES_APPROVAL=false). **False is the zero value on
	// purpose**: a process wired without this setting — a fourth binary, a
	// test, a forgotten call site — cannot hand out free credit, it can only
	// refuse.
	AutoGrant bool
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
// deliberately a byte store rather than a BudgetState store, so the Redis
// implementation can live next to this file without a caching package having
// to import internal/app.
//
// It used to say "without internal/cache". That package was dead — imported by
// no Go file, its InvalidateSQLCache a `return nil`, its InferQueryType
// string-matching the year "2023" — and was deleted on 2026-08-14. This is now
// the only cache in the backend.
type BudgetCache interface {
	Get(ctx context.Context, key string) ([]byte, bool)
	Set(ctx context.Context, key string, val []byte, ttl time.Duration)
	// Delete drops a cached verdict (RIZ-83). Unlike Get and Set it reports
	// its error, because its one caller has something to say when it fails:
	// a top-up that was approved and is still refused for up to one TTL.
	Delete(ctx context.Context, key string) error
}

// budgetCacheTTL bounds how stale a verdict may be. The window cuts both
// ways: a tenant can overspend by up to one TTL of turns, and a tenant whose
// balance was just topped up would stay refused for up to one TTL — which is
// why an approved top-up deletes the verdict rather than waiting it out
// (InvalidateBudget).
const budgetCacheTTL = 60 * time.Second

// InvalidateBudget drops companyID's cached verdict, so the next budget check
// reads the balance a top-up just changed (RIZ-83 R13). Every process that
// enforces — api, worker, discord — reads the same Redis key, so one delete
// here reaches all of them.
//
// Best-effort and silent to the caller: the balance is already written, and a
// failed delete costs at most one budget-cache TTL of a refusal the next check
// corrects by itself. Undoing the approval over it would be the wrong trade.
func (s *UsageService) InvalidateBudget(ctx context.Context, companyID string) {
	if s == nil || s.budgetCache == nil || companyID == "" {
		return
	}
	if err := s.budgetCache.Delete(ctx, budgetCacheKey(companyID)); err != nil {
		logrus.WithError(err).WithField("company_id", companyID).
			Warn("budget cache delete failed; the old verdict expires within a minute")
	}
}

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

// WithGrantRequests gives the budget check somewhere to file a free-credit
// request (RIZ-89). Without it a company with no grant is still refused — as
// pending — rather than granted: R4's "a process wired without the request
// repository refuses rather than grants".
func (s *UsageService) WithGrantRequests(repo domain.CreditGrantRequestRepository) *UsageService {
	s.grants = repo
	return s
}

// FreeCreditAlerter tells the operators a sign-up is waiting for its free
// credit (RIZ-89 R18). TopupMailer satisfies it.
type FreeCreditAlerter interface {
	NotifyFreeCredit(ctx context.Context, a FreeCreditAlert)
}

// WithFreeCreditAlerts makes a sign-up's request email the operators. Only a
// request sign-up filed is alerted on: a lazy one is a legacy workspace being
// checked, not somebody waiting at the door.
func (s *UsageService) WithFreeCreditAlerts(a FreeCreditAlerter) *UsageService {
	s.alerts = a
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
		st := BudgetState{Verdict: BudgetOK, BYOLLM: true, Enforced: true}
		s.cacheBudget(ctx, companyID, st)
		return st, nil
	}

	credits, err := s.credits.Get(ctx, companyID)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		credits = nil
	case err != nil:
		logrus.WithError(err).WithField("company_id", companyID).
			Warn("credit balance lookup failed; allowing the turn")
		return budgetOK, nil
	}
	if credits == nil || credits.MonthlyGrantMicroUSD == 0 {
		// Never granted anything. The balance is whatever pre-enforcement
		// metering happened to subtract from zero, which is not a debt the
		// tenant agreed to — see (1) at the top of this file. The nil check
		// is not redundant: a repository returning (nil, nil) is a contract
		// violation, and dereferencing it here would take down every turn.
		if !s.credit.AutoGrant {
			return s.refuseWithoutGrant(ctx, companyID, credits)
		}
		credits = s.provisionGrant(ctx, companyID)
	}

	st := BudgetState{
		BalanceMicroUSD: credits.BalanceMicroUSD,
		GrantMicroUSD:   credits.MonthlyGrantMicroUSD,
		RemainingPct:    remainingPct(credits.BalanceMicroUSD, credits.MonthlyGrantMicroUSD),
		Enforced:        true,
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

// refuseWithoutGrant is CheckBudget's answer, with approval required, for a
// company that holds no grant (RIZ-89 R3): it files the company's free-credit
// request if there is none, and refuses the turn saying why.
//
// Every path out of it refuses but one, and that one is the house rule for a
// repository that errors (decision 3 above): a request lookup that fails
// allows the turn, with a Warn, and is not cached, so the next check asks
// again. Refusing there would make a control-DB hiccup refuse every legacy
// workspace's turn for a minute, and the window it opens — turns on a
// balance that only goes negative — is the one decision 3 already accepts.
//
// What it never does is write a balance. That is the point of the ticket.
func (s *UsageService) refuseWithoutGrant(ctx context.Context, companyID string, credits *domain.CompanyCredits) (BudgetState, error) {
	st := BudgetState{Verdict: BudgetExhausted, Enforced: true}
	if credits != nil {
		st.BalanceMicroUSD = credits.BalanceMicroUSD
	}
	// A grant of zero is an operator who decided nobody gets free credit.
	// There is nothing to wait for, so nothing is filed and the refusal is
	// the plain one — which is what this configuration answered before too.
	if s.credit.GrantMicroUSD <= 0 {
		s.cacheBudget(ctx, companyID, st)
		return st, nil
	}
	st.FreeCredit = FreeCreditStatePending
	if s.grants == nil {
		// Wired without the repository: nowhere to file, and nothing that
		// may grant. Refuse as pending; the deployment is wrong, not the
		// tenant, and a grant here is exactly what R4 forbids.
		logrus.WithField("company_id", companyID).
			Warn("free credit needs approval but no request repository is wired; refusing the turn")
		s.cacheBudget(ctx, companyID, st)
		return st, nil
	}

	req, err := s.grants.GetForCompany(ctx, companyID)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		inserted, ferr := s.grants.InsertIfAbsent(ctx, &domain.CreditGrantRequest{
			CompanyID:      companyID,
			AmountMicroUSD: s.credit.GrantMicroUSD,
		})
		if ferr != nil {
			// Still pending — the company has no grant either way — but not
			// cached, so the next check files it.
			logrus.WithError(ferr).WithField("company_id", companyID).
				Warn("free-credit request could not be filed; refusing the turn and retrying on the next check")
			return st, nil
		}
		if inserted {
			logrus.WithFields(logrus.Fields{
				"company_id":  companyID,
				"grant_micro": s.credit.GrantMicroUSD,
			}).Info("filed a free-credit request at a budget check")
		}
	case err != nil:
		logrus.WithError(err).WithField("company_id", companyID).
			Warn("free-credit request lookup failed; allowing the turn")
		return budgetOK, nil
	case req.Status == domain.CreditGrantStatusRejected:
		st.FreeCredit = FreeCreditStateRejected
	case req.Status == domain.CreditGrantStatusApproved:
		// Approved and still no grant: approval writes the grant in the same
		// transaction, so only a hand edit of company_credits gets here.
		// Nothing is waiting, so the plain refusal is the true one.
		st.FreeCredit = ""
	}
	s.cacheBudget(ctx, companyID, st)
	return st, nil
}

// provisionGrant writes the configured grant for a company that has none and
// returns the record as if it had been read. A write failure is not fatal:
// the returned value still reflects the grant, so the turn proceeds and the
// next check retries the write.
//
// Reached only with CreditPolicy.AutoGrant (RIZ-89's kill switch). With
// approval required nothing in this file writes free credit.
func (s *UsageService) provisionGrant(ctx context.Context, companyID string) *domain.CompanyCredits {
	c := s.projectedGrant(companyID)
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

// SignupCredit is what sign-up knows about the workspace it just created,
// which is everything a free-credit request and its alert need.
type SignupCredit struct {
	CompanyID   string
	CompanyName string
	// UserID and Email are the new admin's: requested_by on the request, and
	// who the operator alert and the approval email name.
	UserID string
	Email  string
	// IP is the client address sign-up came from, for the operator's same-IP
	// signal. Anything that does not parse as an address is dropped rather
	// than failing the insert over a column that is only a hint.
	IP string
}

// StartSignupCredit starts a company created by signup on its free credit.
//
// With AutoGrant it is RIZ-79's GrantSignupCredit unchanged: balance and grant
// both CreditPolicy.GrantMicroUSD, insert-only. With approval required (RIZ-89)
// it files a pending free-credit request for that amount instead, writes no
// balance, and emails the operators when it filed one.
//
// Things about it that are deliberate:
//
//   - It ignores Enforce. The kill switch decides whether a balance is
//     *refused on*, not whether one is kept — decrements carry on with
//     enforcement off too — so a company that signed up while it was off
//     still has its credit, or its request, on the day it is switched back on.
//   - It is insert-only. CreditsRepository.InsertIfAbsent never touches a row
//     that exists, and CreditGrantRequestRepository.InsertIfAbsent never files
//     a second request, so this cannot top a company up however often it runs.
//   - It returns nothing. A signup that fails over its free credit is a
//     visitor with no account, which is worse than an account whose credit (or
//     request) arrives one budget check late: CheckBudget writes the grant
//     (AutoGrant) or files the request (approval) the first time this company
//     is checked.
//
// A grant of zero writes and files nothing. CheckBudget treats a zero-grant row
// exactly as it treats no row, and a request for $0 is a request for nothing.
func (s *UsageService) StartSignupCredit(ctx context.Context, sc SignupCredit) {
	grant := s.credit.GrantMicroUSD
	if grant <= 0 || sc.CompanyID == "" {
		return
	}
	if !s.credit.AutoGrant {
		s.fileSignupRequest(ctx, sc, grant)
		return
	}
	c := &domain.CompanyCredits{
		CompanyID:            sc.CompanyID,
		BalanceMicroUSD:      grant,
		MonthlyGrantMicroUSD: grant,
	}
	if err := s.credits.InsertIfAbsent(ctx, c); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id":  sc.CompanyID,
			"grant_micro": grant,
		}).Warn("signup credit grant failed; the first budget check will provision it")
		return
	}
	// "if absent": DO NOTHING does not say whether it did anything, and a
	// line claiming a grant for a company that already had a row would be the
	// one an incident review trusts.
	logrus.WithFields(logrus.Fields{
		"company_id":  sc.CompanyID,
		"grant_micro": grant,
	}).Info("signup credit written if absent")
}

// fileSignupRequest is StartSignupCredit with approval required.
func (s *UsageService) fileSignupRequest(ctx context.Context, sc SignupCredit, grant int64) {
	if s.grants == nil {
		logrus.WithField("company_id", sc.CompanyID).
			Warn("free credit needs approval but no request repository is wired; the workspace stays refused")
		return
	}
	ip := ""
	if addr := net.ParseIP(strings.TrimSpace(sc.IP)); addr != nil {
		ip = addr.String()
	}
	req := &domain.CreditGrantRequest{
		CompanyID:      sc.CompanyID,
		AmountMicroUSD: grant,
		RequestedBy:    sc.UserID,
		SignupIP:       ip,
	}
	inserted, err := s.grants.InsertIfAbsent(ctx, req)
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id":  sc.CompanyID,
			"grant_micro": grant,
		}).Warn("free-credit request could not be filed at signup; the first budget check will file it")
		return
	}
	if !inserted {
		return
	}
	logrus.WithFields(logrus.Fields{
		"company_id":  sc.CompanyID,
		"grant_micro": grant,
	}).Info("filed a free-credit request at signup")
	if s.alerts == nil {
		return
	}
	alert := FreeCreditAlert{
		CompanyName:    sc.CompanyName,
		RequesterEmail: sc.Email,
		SignedUpAt:     req.CreatedAt,
		AmountMicroUSD: grant,
	}
	// Off the request path, for TopupService.UploadProof's reason: SMTP can
	// take its timeout, and the visitor is waiting on their new account, not
	// on our inbox.
	actx := context.WithoutCancel(ctx)
	s.alertAsync(func() { s.alerts.NotifyFreeCredit(actx, alert) })
}

// projectedGrant is what CheckBudget would provision for a company that has
// no grant yet, as a record nobody wrote. CreditsForCompany answers with it so
// a company that has not been checked yet reads the balance it will have,
// rather than a zero it never owed. AutoGrant only: with approval required
// nothing will be provisioned, and projecting a grant would promise one.
func (s *UsageService) projectedGrant(companyID string) *domain.CompanyCredits {
	return &domain.CompanyCredits{
		CompanyID:            companyID,
		BalanceMicroUSD:      s.credit.GrantMicroUSD,
		MonthlyGrantMicroUSD: s.credit.GrantMicroUSD,
	}
}

// alertAsync runs an operator alert off the caller's path. syncAlerts is the
// tests' switch, for TopupService.withSyncAlerts' reason: an alert that has
// been sent by the time the call returns.
func (s *UsageService) alertAsync(f func()) {
	if s.syncAlerts {
		f()
		return
	}
	go f()
}

// BudgetRefusal is ErrInsufficientCredits carrying the state that refused the
// turn (RIZ-89), so the handler that answers it can say *why*: out of credit,
// or free credit waiting for approval, or not approved. errors.Is still
// matches ErrInsufficientCredits, so every caller that only knows the
// sentinel — `/v1`, the widget — answers exactly as before, and its Error()
// reads exactly as the "%w: %s" wrap it replaces did for an ordinary
// exhausted balance.
type BudgetRefusal struct{ State BudgetState }

func (e *BudgetRefusal) Error() string {
	return domain.ErrInsufficientCredits.Error() + ": " + e.State.RefusalMessage()
}

func (e *BudgetRefusal) Unwrap() error { return domain.ErrInsufficientCredits }

// newBudgetRefusal is the error every budget-checked entry point returns for
// a refused state.
func newBudgetRefusal(st BudgetState) error { return &BudgetRefusal{State: st} }

// CreditRefusalMessage is the sentence the dashboard and the team's channels
// answer a refused turn with: the refusing state's own when err carries one,
// and CreditsExhaustedMessage otherwise — which is every refusal that
// predates RIZ-89 and every caller that wraps the bare sentinel.
func CreditRefusalMessage(err error) string {
	var r *BudgetRefusal
	if errors.As(err, &r) {
		return r.State.RefusalMessage()
	}
	return CreditsExhaustedMessage
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
