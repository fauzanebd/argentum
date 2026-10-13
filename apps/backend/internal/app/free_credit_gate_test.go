package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"

	"github.com/fauzanebd/argentum/internal/domain"
)

// RIZ-89: free credit waits for an operator's approval. These are the gate's
// unit arms — sign-up files, the budget check refuses and files, and nothing
// on either path writes a balance.

// stubGrants is a CreditGrantRequestRepository with one request per company,
// counting what it was asked to do.
type stubGrants struct {
	mu       sync.Mutex
	byCo     map[string]*domain.CreditGrantRequest
	inserted []*domain.CreditGrantRequest
	gets     int
	getErr   error
	insErr   error

	// Operator half.
	approval *domain.CreditGrantApproval
	rejected *domain.CreditGrantRequest
	opErr    error
	approves int
}

func newStubGrants() *stubGrants { return &stubGrants{byCo: map[string]*domain.CreditGrantRequest{}} }

func (s *stubGrants) InsertIfAbsent(_ context.Context, r *domain.CreditGrantRequest) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.insErr != nil {
		return false, s.insErr
	}
	if _, ok := s.byCo[r.CompanyID]; ok {
		return false, nil
	}
	r.ID = "req-" + r.CompanyID
	r.Status = domain.CreditGrantStatusPending
	r.CreatedAt = time.Date(2026, 10, 13, 7, 32, 0, 0, time.UTC)
	cp := *r
	s.byCo[r.CompanyID] = &cp
	s.inserted = append(s.inserted, &cp)
	return true, nil
}

func (s *stubGrants) GetForCompany(_ context.Context, companyID string) (*domain.CreditGrantRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gets++
	if s.getErr != nil {
		return nil, s.getErr
	}
	if r, ok := s.byCo[companyID]; ok {
		return r, nil
	}
	return nil, domain.ErrNotFound
}

func (s *stubGrants) ListPending(context.Context) ([]*domain.OperatorFreeCredit, error) {
	return []*domain.OperatorFreeCredit{}, s.opErr
}

func (s *stubGrants) ListReviewed(_ context.Context, limit int) ([]*domain.OperatorFreeCredit, error) {
	if limit != freeCreditReviewedLimit {
		return nil, fmt.Errorf("limit %d, want %d", limit, freeCreditReviewedLimit)
	}
	return []*domain.OperatorFreeCredit{}, s.opErr
}

func (s *stubGrants) Approve(context.Context, string, string) (*domain.CreditGrantApproval, error) {
	s.approves++
	if s.opErr != nil {
		return nil, s.opErr
	}
	return s.approval, nil
}

func (s *stubGrants) Reject(context.Context, string, string) (*domain.CreditGrantRequest, error) {
	if s.opErr != nil {
		return nil, s.opErr
	}
	return s.rejected, nil
}

func (s *stubGrants) with(companyID string, status domain.CreditGrantStatus) *stubGrants {
	s.byCo[companyID] = &domain.CreditGrantRequest{ID: "req-" + companyID, CompanyID: companyID, Status: status, AmountMicroUSD: signupGrant}
	return s
}

// recordingAlerts is a FreeCreditAlerter.
type recordingAlerts struct{ sent []FreeCreditAlert }

func (r *recordingAlerts) NotifyFreeCredit(_ context.Context, a FreeCreditAlert) {
	r.sent = append(r.sent, a)
}

// approvalPolicy is the default since RIZ-89: enforcing, $0.50, no AutoGrant.
func approvalPolicy() CreditPolicy {
	return CreditPolicy{Enforce: true, WarnPct: 20, GrantMicroUSD: signupGrant}
}

func approvalService(credits *stubCredits, grants *stubGrants, cache BudgetCache) *UsageService {
	svc := newCreditService(approvalPolicy(), credits, &stubLLMCreds{}, cache)
	if grants != nil {
		svc = svc.WithGrantRequests(grants)
	}
	return svc
}

// noCreditWrites fails when anything wrote company_credits: with approval
// required, nothing but an operator's approval may (R3).
func noCreditWrites(t *testing.T, credits *stubCredits) {
	t.Helper()
	if n := len(credits.upserts) + len(credits.inserts); n != 0 {
		t.Errorf("company_credits writes = %d (upserts %+v, inserts %+v), want 0", n, credits.upserts, credits.inserts)
	}
}

// AC1 at the service: sign-up files one pending $0.50 request naming the admin
// and the IP, writes no balance, and alerts the operators once.
func TestSignupFilesAFreeCreditRequest(t *testing.T) {
	credits := &stubCredits{getErr: domain.ErrNotFound}
	grants := newStubGrants()
	alerts := &recordingAlerts{}
	svc, _, _ := authFixture(t)
	svc.companies = &signupCompanyRepo{}
	usage := approvalService(credits, grants, nil).WithFreeCreditAlerts(alerts)
	usage.syncAlerts = true

	res, err := svc.WithCredits(usage).Signup(context.Background(), "Toko Segar", "dina@tokosegar.id", "correct-horse", signupIP)
	if err != nil {
		t.Fatalf("Signup: %v", err)
	}
	noCreditWrites(t, credits)
	if len(grants.inserted) != 1 {
		t.Fatalf("requests filed = %d, want 1", len(grants.inserted))
	}
	got := grants.inserted[0]
	if got.CompanyID != res.Company.ID || got.AmountMicroUSD != signupGrant ||
		got.RequestedBy != res.User.ID || got.SignupIP != signupIP {
		t.Errorf("request = %+v, want %s for %d by %s from %s", got, res.Company.ID, signupGrant, res.User.ID, signupIP)
	}
	if len(alerts.sent) != 1 {
		t.Fatalf("operator alerts = %d, want 1", len(alerts.sent))
	}
	if a := alerts.sent[0]; a.CompanyName != "Toko Segar" || a.RequesterEmail != "dina@tokosegar.id" || a.AmountMicroUSD != signupGrant || a.SignedUpAt.IsZero() {
		t.Errorf("alert = %+v", a)
	}

	// AC2: the admin's first message is refused as pending, and still nothing
	// is written.
	st, err := usage.CheckBudget(context.Background(), res.Company.ID)
	if err != nil || !st.Blocked() || st.FreeCredit != FreeCreditStatePending {
		t.Fatalf("first check = %+v, %v; want refused as pending", st, err)
	}
	if st.RefusalMessage() != FreeCreditPendingMessage {
		t.Errorf("refusal = %q", st.RefusalMessage())
	}
	if len(grants.inserted) != 1 {
		t.Errorf("the check filed a second request: %d", len(grants.inserted))
	}
	noCreditWrites(t, credits)
}

// R2's edges: a failed filing never fails the sign-up, an address that is not
// an IP is dropped rather than failing the insert, a request that already
// existed is not alerted on again, and a grant of zero files nothing.
func TestSignupRequestEdges(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("connection reset by peer")

	t.Run("filing fails", func(t *testing.T) {
		hook := logrustest.NewGlobal()
		defer hook.Reset()
		grants := newStubGrants()
		grants.insErr = boom
		alerts := &recordingAlerts{}
		usage := approvalService(&stubCredits{getErr: domain.ErrNotFound}, grants, nil).WithFreeCreditAlerts(alerts)
		usage.syncAlerts = true
		usage.StartSignupCredit(ctx, SignupCredit{CompanyID: "co-1", UserID: "u-1", IP: signupIP})
		if len(alerts.sent) != 0 {
			t.Errorf("alerted on a request that was never filed")
		}
		warned := false
		for _, e := range hook.AllEntries() {
			warned = warned || (e.Level == logrus.WarnLevel && e.Data["company_id"] == "co-1")
		}
		if !warned {
			t.Error("no Warn naming the company for a failed filing")
		}
	})

	t.Run("not an address", func(t *testing.T) {
		grants := newStubGrants()
		usage := approvalService(&stubCredits{}, grants, nil)
		usage.StartSignupCredit(ctx, SignupCredit{CompanyID: "co-1", IP: "not-an-ip"})
		if len(grants.inserted) != 1 || grants.inserted[0].SignupIP != "" {
			t.Errorf("inserted = %+v, want one request with no IP", grants.inserted)
		}
	})

	t.Run("already filed", func(t *testing.T) {
		grants := newStubGrants().with("co-1", domain.CreditGrantStatusPending)
		alerts := &recordingAlerts{}
		usage := approvalService(&stubCredits{}, grants, nil).WithFreeCreditAlerts(alerts)
		usage.syncAlerts = true
		usage.StartSignupCredit(ctx, SignupCredit{CompanyID: "co-1", IP: signupIP})
		if len(alerts.sent) != 0 || len(grants.inserted) != 0 {
			t.Errorf("alerts %d, inserts %d; want none for a company already filed", len(alerts.sent), len(grants.inserted))
		}
	})

	t.Run("zero grant", func(t *testing.T) {
		grants := newStubGrants()
		credits := &stubCredits{}
		p := approvalPolicy()
		p.GrantMicroUSD = 0
		usage := newCreditService(p, credits, &stubLLMCreds{}, nil).WithGrantRequests(grants)
		usage.StartSignupCredit(ctx, SignupCredit{CompanyID: "co-1"})
		if len(grants.inserted) != 0 {
			t.Errorf("filed a request for $0")
		}
		noCreditWrites(t, credits)
	})

	t.Run("no repository wired", func(t *testing.T) {
		credits := &stubCredits{}
		usage := approvalService(credits, nil, nil)
		usage.StartSignupCredit(ctx, SignupCredit{CompanyID: "co-1"})
		noCreditWrites(t, credits)
	})
}

// Q5 / AC3 / AC4 / AC15 / R4 / R5: what CheckBudget answers with approval
// required, and that none of it writes a balance.
func TestCheckBudgetWithApprovalRequired(t *testing.T) {
	boom := errors.New("connection refused")
	granted := &domain.CompanyCredits{CompanyID: "c1", BalanceMicroUSD: 310_000, MonthlyGrantMicroUSD: signupGrant}
	cases := []struct {
		name        string
		credits     *stubCredits
		grants      *stubGrants // nil: not wired
		llm         *stubLLMCreds
		policy      *CreditPolicy
		want        BudgetVerdict
		wantFree    FreeCreditState
		wantFiled   int
		wantCached  bool
		wantLookups int
		wantBalance int64
		wantRefusal string
	}{
		{
			name: "no row, no request (legacy shape)", credits: &stubCredits{getErr: domain.ErrNotFound}, grants: newStubGrants(),
			want: BudgetExhausted, wantFree: FreeCreditStatePending, wantFiled: 1, wantCached: true, wantLookups: 1,
			wantRefusal: FreeCreditPendingMessage,
		},
		{
			name:    "zero-grant row with a negative balance",
			credits: &stubCredits{record: &domain.CompanyCredits{CompanyID: "c1", BalanceMicroUSD: -40_000}}, grants: newStubGrants(),
			want: BudgetExhausted, wantFree: FreeCreditStatePending, wantFiled: 1, wantCached: true, wantLookups: 1,
			wantBalance: -40_000, wantRefusal: FreeCreditPendingMessage,
		},
		{
			// The (nil, nil) contract violation is "no row" here too.
			name: "repository returned no record and no error", credits: &stubCredits{}, grants: newStubGrants(),
			want: BudgetExhausted, wantFree: FreeCreditStatePending, wantFiled: 1, wantCached: true, wantLookups: 1,
			wantRefusal: FreeCreditPendingMessage,
		},
		{
			name: "already pending", credits: &stubCredits{getErr: domain.ErrNotFound}, grants: newStubGrants().with("c1", domain.CreditGrantStatusPending),
			want: BudgetExhausted, wantFree: FreeCreditStatePending, wantCached: true, wantLookups: 1,
			wantRefusal: FreeCreditPendingMessage,
		},
		{
			name: "rejected", credits: &stubCredits{getErr: domain.ErrNotFound}, grants: newStubGrants().with("c1", domain.CreditGrantStatusRejected),
			want: BudgetExhausted, wantFree: FreeCreditStateRejected, wantCached: true, wantLookups: 1,
			wantRefusal: FreeCreditRejectedMessage,
		},
		{
			// R4: wired without the repository, refuse — never grant.
			name: "no request repository", credits: &stubCredits{getErr: domain.ErrNotFound},
			want: BudgetExhausted, wantFree: FreeCreditStatePending, wantCached: true,
			wantRefusal: FreeCreditPendingMessage,
		},
		{
			// The file's rule for a repository error: warn, allow, do not cache.
			name: "request lookup fails", credits: &stubCredits{getErr: domain.ErrNotFound},
			grants: func() *stubGrants { g := newStubGrants(); g.getErr = boom; return g }(),
			want:   BudgetOK, wantLookups: 1, wantRefusal: CreditsExhaustedMessage,
		},
		{
			// Refused as pending, but not cached, so the next check files it.
			name: "filing fails", credits: &stubCredits{getErr: domain.ErrNotFound},
			grants: func() *stubGrants { g := newStubGrants(); g.insErr = boom; return g }(),
			want:   BudgetExhausted, wantFree: FreeCreditStatePending, wantLookups: 1,
			wantRefusal: FreeCreditPendingMessage,
		},
		{
			// AC4 / R5: a company that holds a grant never reaches the
			// request table, and reads exactly as it did.
			name: "already granted", credits: &stubCredits{record: granted}, grants: newStubGrants(),
			want: BudgetOK, wantCached: true, wantBalance: 310_000, wantRefusal: CreditsExhaustedMessage,
		},
		{
			// AC15: BYO is never refused and never filed.
			name: "own LLM key", credits: &stubCredits{getErr: domain.ErrNotFound}, grants: newStubGrants(),
			llm:  &stubLLMCreds{rows: []*domain.CompanyLLMCredential{{Tier: domain.LLMTierPrimary, APIKeyEncrypted: []byte("k")}}},
			want: BudgetOK, wantCached: true, wantRefusal: CreditsExhaustedMessage,
		},
		{
			// A grant of zero: nothing to wait for, so the plain refusal.
			name: "configured grant is zero", credits: &stubCredits{getErr: domain.ErrNotFound}, grants: newStubGrants(),
			policy: &CreditPolicy{Enforce: true, WarnPct: 20},
			want:   BudgetExhausted, wantCached: true, wantRefusal: CreditsExhaustedMessage,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cache := newMapBudgetCache()
			llm := tc.llm
			if llm == nil {
				llm = &stubLLMCreds{}
			}
			policy := approvalPolicy()
			if tc.policy != nil {
				policy = *tc.policy
			}
			svc := newCreditService(policy, tc.credits, llm, cache)
			if tc.grants != nil {
				svc = svc.WithGrantRequests(tc.grants)
			}

			st, err := svc.CheckBudget(context.Background(), "c1")
			if err != nil {
				t.Fatalf("CheckBudget: %v", err)
			}
			if st.Verdict != tc.want || st.FreeCredit != tc.wantFree {
				t.Errorf("state = %+v, want %s / %q", st, tc.want, tc.wantFree)
			}
			if st.BalanceMicroUSD != tc.wantBalance && tc.want == BudgetExhausted {
				t.Errorf("balance = %d, want %d", st.BalanceMicroUSD, tc.wantBalance)
			}
			if got := st.RefusalMessage(); got != tc.wantRefusal {
				t.Errorf("refusal = %q, want %q", got, tc.wantRefusal)
			}
			if tc.grants != nil {
				if len(tc.grants.inserted) != tc.wantFiled {
					t.Errorf("filed = %d, want %d", len(tc.grants.inserted), tc.wantFiled)
				}
				if tc.grants.gets != tc.wantLookups {
					t.Errorf("request lookups = %d, want %d", tc.grants.gets, tc.wantLookups)
				}
				for _, r := range tc.grants.inserted {
					if r.SignupIP != "" || r.RequestedBy != "" || r.AmountMicroUSD != signupGrant {
						t.Errorf("lazy request = %+v, want no IP, nobody, %d", r, signupGrant)
					}
				}
			}
			if _, cached := cache.entries[budgetCacheKey("c1")]; cached != tc.wantCached {
				t.Errorf("cached = %v, want %v", cached, tc.wantCached)
			}
			noCreditWrites(t, tc.credits)
		})
	}
}

// R4: a policy nobody set AutoGrant on — what a process wired without the new
// setting builds — refuses rather than grants.
func TestTheZeroPolicyRefusesRatherThanGrants(t *testing.T) {
	credits := &stubCredits{getErr: domain.ErrNotFound}
	svc := newCreditService(CreditPolicy{Enforce: true, GrantMicroUSD: signupGrant}, credits, &stubLLMCreds{}, nil)
	st, _ := svc.CheckBudget(context.Background(), "c1")
	if !st.Blocked() {
		t.Errorf("state = %+v, want refused", st)
	}
	noCreditWrites(t, credits)
}

// After an approval the cache is dropped and the next check runs (AC5); after
// a rejection the next check says "not approved" rather than "waiting" (AC7).
func TestADecisionReachesTheNextCheck(t *testing.T) {
	ctx := context.Background()
	for _, decided := range []domain.CreditGrantStatus{domain.CreditGrantStatusApproved, domain.CreditGrantStatusRejected} {
		t.Run(string(decided), func(t *testing.T) {
			cache := newMapBudgetCache()
			credits := &stubCredits{getErr: domain.ErrNotFound}
			grants := newStubGrants()
			usage := approvalService(credits, grants, cache)
			if st, _ := usage.CheckBudget(ctx, "c1"); st.FreeCredit != FreeCreditStatePending {
				t.Fatalf("before: %+v", st)
			}

			// What the repository's approve or reject leaves behind.
			grants.byCo["c1"].Status = decided
			if decided == domain.CreditGrantStatusApproved {
				credits.getErr = nil
				credits.record = &domain.CompanyCredits{CompanyID: "c1", BalanceMicroUSD: signupGrant, MonthlyGrantMicroUSD: signupGrant}
				grants.approval = &domain.CreditGrantApproval{Request: grants.byCo["c1"], AddedMicroUSD: signupGrant, BalanceMicroUSD: signupGrant}
				if _, err := NewFreeCreditService(grants).WithBudget(usage).Approve(ctx, "00000000-0000-4000-8000-000000000001", "op-1"); err != nil {
					t.Fatal(err)
				}
			} else {
				grants.rejected = grants.byCo["c1"]
				if _, err := NewFreeCreditService(grants).WithBudget(usage).Reject(ctx, "00000000-0000-4000-8000-000000000001", "op-1"); err != nil {
					t.Fatal(err)
				}
			}

			st, _ := usage.CheckBudget(ctx, "c1")
			switch decided {
			case domain.CreditGrantStatusApproved:
				if st.Verdict != BudgetOK || st.BalanceMicroUSD != signupGrant {
					t.Errorf("after approval: %+v, want OK at $0.50", st)
				}
			default:
				if !st.Blocked() || st.FreeCredit != FreeCreditStateRejected {
					t.Errorf("after rejection: %+v, want refused as rejected", st)
				}
			}
		})
	}
}

// The refusal carries its state through every error path, and reads exactly
// as the old "%w: %s" wrap did for an ordinary exhausted balance.
func TestBudgetRefusalCarriesItsSentence(t *testing.T) {
	plain := newBudgetRefusal(BudgetState{Verdict: BudgetExhausted})
	if want := fmt.Errorf("%w: %s", domain.ErrInsufficientCredits, CreditsExhaustedMessage).Error(); plain.Error() != want {
		t.Errorf("Error() = %q, want the pre-RIZ-89 %q", plain.Error(), want)
	}
	for _, tc := range []struct {
		err  error
		want string
	}{
		{plain, CreditsExhaustedMessage},
		{newBudgetRefusal(BudgetState{Verdict: BudgetExhausted, FreeCredit: FreeCreditStatePending}), FreeCreditPendingMessage},
		{fmt.Errorf("enqueue: %w", newBudgetRefusal(BudgetState{Verdict: BudgetExhausted, FreeCredit: FreeCreditStateRejected})), FreeCreditRejectedMessage},
		// A bare sentinel, as anything that predates RIZ-89 wraps it.
		{domain.ErrInsufficientCredits, CreditsExhaustedMessage},
	} {
		if !errors.Is(tc.err, domain.ErrInsufficientCredits) {
			t.Errorf("%v does not match ErrInsufficientCredits", tc.err)
		}
		if got := CreditRefusalMessage(tc.err); got != tc.want {
			t.Errorf("CreditRefusalMessage(%v) = %q, want %q", tc.err, got, tc.want)
		}
		if got, ok := SpokenRefusal(tc.err); !ok || got != tc.want {
			t.Errorf("SpokenRefusal(%v) = %q, %v; want %q", tc.err, got, ok, tc.want)
		}
	}
	// The public string is untouched (R12, AC12).
	if CreditsExhaustedMessage != "This workspace has used all of its Argentum credits, so I can't run that right now. Ask an admin to top up the balance — current usage is on the Usage page in the dashboard." {
		t.Errorf("CreditsExhaustedMessage changed: %q", CreditsExhaustedMessage)
	}
}

// Q20: a channel message through the enqueuer is answered with the pending
// sentence, and so is a scheduled task's run.
func TestChannelsAndSchedulesSayFreeCreditIsWaiting(t *testing.T) {
	usage := approvalService(&stubCredits{getErr: domain.ErrNotFound}, newStubGrants(), nil)

	enq := NewChatEnqueuer(nil, nil, nil, nil).WithBudget(usage)
	_, err := enq.Enqueue(context.Background(), ChatInput{
		Channel: domain.ChannelWhatsApp, CompanyID: "c1", PhoneNumber: "+6281200000000", Message: "sales last month?",
	})
	if got, ok := SpokenRefusal(err); !ok || got != FreeCreditPendingMessage {
		t.Errorf("channel reply = %q, %v (err %v); want the pending sentence", got, ok, err)
	}

	repo := &stubScheduledRepo{task: &domain.ScheduledTask{ID: "t1", CompanyID: "c1", ThreadID: "th1", Prompt: "go", Enabled: true}}
	if err := NewScheduledTaskService(repo, nil, nil, nil).WithBudget(usage).HandleFire(context.Background(), "t1"); err != nil {
		t.Fatalf("HandleFire: %v", err)
	}
	if len(repo.updated) != 1 || repo.updated[0].ErrorMessage != FreeCreditPendingMessage {
		t.Errorf("run = %+v, want failed with the pending sentence", repo.updated)
	}
}

// R3 / R14: what Usage reads with approval required, and that reading it files
// and writes nothing.
func TestCreditsForCompanyWithApprovalRequired(t *testing.T) {
	boom := errors.New("connection refused")
	topped := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name        string
		credits     *stubCredits
		grants      *stubGrants
		llm         *stubLLMCreds
		wantStatus  domain.CreditGrantStatus
		wantAmount  int64
		wantBalance int64
		wantGrant   int64
	}{
		{name: "no row, no request yet", credits: &stubCredits{getErr: domain.ErrNotFound}, grants: newStubGrants(),
			wantStatus: domain.CreditGrantStatusPending, wantAmount: signupGrant},
		{name: "pending", credits: &stubCredits{getErr: domain.ErrNotFound}, grants: newStubGrants().with("c1", domain.CreditGrantStatusPending),
			wantStatus: domain.CreditGrantStatusPending, wantAmount: signupGrant},
		{name: "rejected, negative balance", credits: &stubCredits{record: &domain.CompanyCredits{CompanyID: "c1", BalanceMicroUSD: -40_000}},
			grants:     newStubGrants().with("c1", domain.CreditGrantStatusRejected),
			wantStatus: domain.CreditGrantStatusRejected, wantAmount: signupGrant, wantBalance: -40_000},
		{name: "granted: the stored row, no free-credit fields",
			credits:     &stubCredits{record: &domain.CompanyCredits{CompanyID: "c1", BalanceMicroUSD: 5_760_000, MonthlyGrantMicroUSD: 5_760_000, LastTopupAt: &topped}},
			grants:      newStubGrants().with("c1", domain.CreditGrantStatusPending),
			wantBalance: 5_760_000, wantGrant: 5_760_000},
		{name: "own LLM key: never refused, never told it is waiting", credits: &stubCredits{getErr: domain.ErrNotFound}, grants: newStubGrants(),
			llm: &stubLLMCreds{rows: []*domain.CompanyLLMCredential{{Tier: domain.LLMTierPrimary, APIKeyEncrypted: []byte("k")}}}},
		{name: "request lookup fails: the balance, as pending", credits: &stubCredits{getErr: domain.ErrNotFound},
			grants:     func() *stubGrants { g := newStubGrants(); g.getErr = boom; return g }(),
			wantStatus: domain.CreditGrantStatusPending, wantAmount: signupGrant},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			llm := tc.llm
			if llm == nil {
				llm = &stubLLMCreds{}
			}
			svc := newCreditService(approvalPolicy(), tc.credits, llm, nil).WithGrantRequests(tc.grants)
			got, err := svc.CreditsForCompany(context.Background(), "c1")
			if err != nil {
				t.Fatalf("CreditsForCompany: %v", err)
			}
			if got.CompanyID != "c1" || got.FreeCreditStatus != tc.wantStatus || got.FreeCreditMicroUSD != tc.wantAmount ||
				got.BalanceMicroUSD != tc.wantBalance || got.MonthlyGrantMicroUSD != tc.wantGrant {
				t.Errorf("got %+v, want %q %d at %d / %d", got, tc.wantStatus, tc.wantAmount, tc.wantBalance, tc.wantGrant)
			}
			if len(tc.grants.inserted) != 0 {
				t.Errorf("opening Usage filed a request")
			}
			noCreditWrites(t, tc.credits)
		})
	}
}

// recordingReady is a FreeCreditReadyNotifier.
type recordingReady struct{ sent []FreeCreditReady }

func (r *recordingReady) NotifyFreeCreditReady(_ context.Context, m FreeCreditReady) {
	r.sent = append(r.sent, m)
}

// countingBudget is a BudgetInvalidator.
type countingBudget struct{ companies []string }

func (c *countingBudget) InvalidateBudget(_ context.Context, companyID string) {
	c.companies = append(c.companies, companyID)
}

type oneCompany struct{}

func (oneCompany) GetByID(context.Context, string) (*domain.Company, error) {
	return &domain.Company{ID: "c1", Name: "Toko Segar"}, nil
}

type oneUser struct{}

func (oneUser) GetByID(context.Context, string) (*domain.User, error) {
	return &domain.User{ID: "u-1", Email: "dina@tokosegar.id"}, nil
}

// R8 / R9 / R19 / AC14 at the service.
func TestFreeCreditServiceDecisions(t *testing.T) {
	ctx := context.Background()
	const id = "00000000-0000-4000-8000-000000000001"
	req := &domain.CreditGrantRequest{ID: id, CompanyID: "c1", RequestedBy: "u-1", AmountMicroUSD: signupGrant}

	t.Run("approve adds, invalidates and emails the admin", func(t *testing.T) {
		grants := newStubGrants()
		grants.approval = &domain.CreditGrantApproval{Request: req, AddedMicroUSD: signupGrant, BalanceMicroUSD: signupGrant}
		budget, mail := &countingBudget{}, &recordingReady{}
		svc := NewFreeCreditService(grants).WithBudget(budget).WithMailer(mail, oneCompany{}, oneUser{}).withSyncMail()
		res, err := svc.Approve(ctx, id, "op-1")
		if err != nil || res.AddedMicroUSD != signupGrant {
			t.Fatalf("Approve = %+v, %v", res, err)
		}
		if len(budget.companies) != 1 || budget.companies[0] != "c1" {
			t.Errorf("invalidated %v, want [c1]", budget.companies)
		}
		if len(mail.sent) != 1 || mail.sent[0].To != "dina@tokosegar.id" || mail.sent[0].CompanyName != "Toko Segar" || mail.sent[0].AddedMicroUSD != signupGrant {
			t.Errorf("approval email = %+v", mail.sent)
		}
	})

	t.Run("approve that adds nothing sends nothing", func(t *testing.T) {
		grants := newStubGrants()
		grants.approval = &domain.CreditGrantApproval{Request: req, AddedMicroUSD: 0, BalanceMicroUSD: 310_000}
		mail := &recordingReady{}
		svc := NewFreeCreditService(grants).WithMailer(mail, oneCompany{}, oneUser{}).withSyncMail()
		if _, err := svc.Approve(ctx, id, "op-1"); err != nil {
			t.Fatal(err)
		}
		if len(mail.sent) != 0 {
			t.Errorf("emailed about an approval that added nothing: %+v", mail.sent)
		}
	})

	t.Run("already reviewed and missing pass through", func(t *testing.T) {
		for _, want := range []error{domain.ErrCreditGrantAlreadyReviewed, domain.ErrNotFound} {
			grants := newStubGrants()
			grants.opErr = want
			budget := &countingBudget{}
			svc := NewFreeCreditService(grants).WithBudget(budget)
			if _, err := svc.Approve(ctx, id, "op-1"); !errors.Is(err, want) {
				t.Errorf("Approve = %v, want %v", err, want)
			}
			if _, err := svc.Reject(ctx, id, "op-1"); !errors.Is(err, want) {
				t.Errorf("Reject = %v, want %v", err, want)
			}
			if len(budget.companies) != 0 {
				t.Errorf("invalidated on a refused decision")
			}
		}
	})

	t.Run("an id that is not a uuid is not found, without a query", func(t *testing.T) {
		grants := newStubGrants()
		svc := NewFreeCreditService(grants)
		if _, err := svc.Approve(ctx, "../etc", "op-1"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("Approve = %v", err)
		}
		if _, err := svc.Reject(ctx, "1", "op-1"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("Reject = %v", err)
		}
		if grants.approves != 0 {
			t.Errorf("reached the repository")
		}
	})

	t.Run("reject invalidates", func(t *testing.T) {
		grants := newStubGrants()
		grants.rejected = &domain.CreditGrantRequest{ID: id, CompanyID: "c1", Status: domain.CreditGrantStatusRejected}
		budget := &countingBudget{}
		if _, err := NewFreeCreditService(grants).WithBudget(budget).Reject(ctx, id, "op-1"); err != nil {
			t.Fatal(err)
		}
		if len(budget.companies) != 1 {
			t.Errorf("invalidated %v", budget.companies)
		}
	})

	t.Run("lists", func(t *testing.T) {
		svc := NewFreeCreditService(newStubGrants())
		if _, err := svc.OperatorList(ctx, false); err != nil {
			t.Error(err)
		}
		if _, err := svc.OperatorList(ctx, true); err != nil {
			t.Error(err)
		}
	})
}

// R16 / AC9: an operator's address cannot sign up, be invited or accept an
// invitation, and each door answers as it does for an address in use.
func TestOperatorAddressesCannotBeClaimed(t *testing.T) {
	ctx := context.Background()
	ops := NewOperatorEmails([]string{"owner@example.com", " Ops@Argentum.id "})

	t.Run("signup", func(t *testing.T) {
		svc, users, _ := authFixture(t)
		companies := &signupCompanyRepo{}
		svc.companies = companies
		g := &recordingGranter{}
		_, err := svc.WithCredits(g).WithOperatorEmails(ops).Signup(ctx, "Ops", " OPS@argentum.id ", "correct-horse", signupIP)
		if !errors.Is(err, domain.ErrAlreadyExists) {
			t.Fatalf("Signup = %v, want ErrAlreadyExists", err)
		}
		if companies.created != 0 || len(g.companies) != 0 {
			t.Errorf("created %d companies, granted %v", companies.created, g.companies)
		}
		if _, err := users.GetByEmail(ctx, "ops@argentum.id"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("a user was created: %v", err)
		}
		// Everyone else still signs up.
		if _, err := svc.Signup(ctx, "Toko Segar", "dina@tokosegar.id", "correct-horse", signupIP); err != nil {
			t.Errorf("an ordinary signup: %v", err)
		}
	})

	t.Run("invite", func(t *testing.T) {
		svc, users, invites, admin := teamFixture(t)
		_, err := svc.WithOperatorEmails(ops).Invite(ctx, "co-1", admin.ID, "Owner@Example.com", domain.RoleMember)
		if !errors.Is(err, domain.ErrAlreadyExists) {
			t.Fatalf("Invite = %v, want ErrAlreadyExists", err)
		}
		if _, err := users.GetByEmail(ctx, "owner@example.com"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("a pending user was created: %v", err)
		}
		if n := len(invites.rows); n != 0 {
			t.Errorf("invites = %d, want 0", n)
		}
	})

	t.Run("accept an invitation made before the address was listed", func(t *testing.T) {
		svc, users, _, admin := teamFixture(t)
		res, err := svc.Invite(ctx, "co-1", admin.ID, "ops@argentum.id", domain.RoleMember)
		if err != nil {
			t.Fatalf("Invite before listing: %v", err)
		}
		if _, err := svc.WithOperatorEmails(ops).Accept(ctx, res.Token, "correct-horse"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("Accept = %v, want ErrNotFound", err)
		}
		u, err := users.GetByEmail(ctx, "ops@argentum.id")
		if err != nil || u.Active() {
			t.Errorf("user = %+v, %v; want still pending", u, err)
		}
	})
}

// R17: one warning per entry with no active account, by position, never by
// address.
func TestOperatorAccountsAreReportedByPosition(t *testing.T) {
	hook := logrustest.NewGlobal()
	defer hook.Reset()
	users := newFakeUsers()
	ctx := context.Background()
	active := &domain.User{CompanyID: "co-1", Email: "owner@example.com", Role: domain.RoleMember}
	if err := users.Create(ctx, active); err != nil {
		t.Fatal(err)
	}
	pending := &domain.User{CompanyID: "co-1", Email: "pending@example.com", Role: domain.RoleMember}
	if err := users.CreatePending(ctx, pending); err != nil {
		t.Fatal(err)
	}

	LogOperatorAccounts(ctx, users, []string{"owner@example.com", "ops@argentum.id", "pending@example.com"})

	var lines []string
	for _, e := range hook.AllEntries() {
		if e.Level == logrus.WarnLevel {
			lines = append(lines, e.Message)
		}
		for _, field := range []string{e.Message, fmt.Sprint(e.Data)} {
			if strings.Contains(field, "@") {
				t.Errorf("a log line names an address: %q", field)
			}
		}
	}
	want := []string{
		"PLATFORM_OPERATOR_EMAILS entry 2 has no active account",
		"PLATFORM_OPERATOR_EMAILS entry 3 has no active account",
	}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Errorf("warnings = %q, want %q", lines, want)
	}
}
