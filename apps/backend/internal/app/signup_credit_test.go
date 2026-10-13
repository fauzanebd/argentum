package app

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"

	"github.com/fauzanebd/argentum/internal/domain"
)

// RIZ-79: a new workspace starts with its free credit at signup rather than at
// its first budget check.

const signupGrant = int64(500_000) // $0.50, the CREDITS_DEFAULT_GRANT_USD default

// signupCompanyRepo is the one CompanyRepository method signup reaches. Every
// other method is the embedded nil interface, so a change that reaches further
// fails loudly here rather than passing on a zero value.
type signupCompanyRepo struct {
	domain.CompanyRepository
	created int
	err     error
}

func (r *signupCompanyRepo) Create(_ context.Context, c *domain.Company) error {
	if r.err != nil {
		return r.err
	}
	r.created++
	c.ID = "co-" + strconv.Itoa(r.created)
	return nil
}

// failingUserCreate is fakeUsers whose Create always fails, for the orphaned
// company case.
type failingUserCreate struct {
	*fakeUsers
	err error
}

func (f failingUserCreate) Create(context.Context, *domain.User) error { return f.err }

// recordingGranter remembers which companies it was asked to credit, for the
// tests about *when* signup asks rather than what the grant writes.
type recordingGranter struct{ companies []string }

func (g *recordingGranter) GrantSignupCredit(_ context.Context, companyID string) {
	g.companies = append(g.companies, companyID)
}

func signupPolicy(enforce bool, grant int64) CreditPolicy {
	return CreditPolicy{Enforce: enforce, WarnPct: 20, GrantMicroUSD: grant}
}

// signupWithCredits wires an AuthService to a real UsageService over the given
// stub, which is how cmd/api wires them.
func signupWithCredits(t *testing.T, policy CreditPolicy, credits *stubCredits) (*AuthService, *UsageService) {
	t.Helper()
	svc, _, _ := authFixture(t)
	svc.companies = &signupCompanyRepo{}
	usage := newCreditService(policy, credits, &stubLLMCreds{}, nil)
	return svc.WithCredits(usage), usage
}

// AC1 at the service: one insert of $0.50 / $0.50, which is what the Usage page
// reads before any turn — and the lazy grant then has nothing left to do.
func TestSignupWritesTheFreeCredit(t *testing.T) {
	ctx := context.Background()
	credits := &stubCredits{getErr: domain.ErrNotFound}
	auth, usage := signupWithCredits(t, signupPolicy(true, signupGrant), credits)

	res, err := auth.Signup(ctx, "Toko Segar", "dina@tokosegar.id", "correct-horse")
	if err != nil {
		t.Fatalf("Signup: %v", err)
	}
	if len(credits.inserts) != 1 {
		t.Fatalf("inserts = %d, want exactly 1", len(credits.inserts))
	}
	got := credits.inserts[0]
	if got.CompanyID != res.Company.ID || got.BalanceMicroUSD != signupGrant || got.MonthlyGrantMicroUSD != signupGrant {
		t.Errorf("grant written = %+v, want %s at %d / %d", got, res.Company.ID, signupGrant, signupGrant)
	}

	shown, err := usage.CreditsForCompany(ctx, res.Company.ID)
	if err != nil || shown.BalanceMicroUSD != signupGrant || shown.MonthlyGrantMicroUSD != signupGrant {
		t.Fatalf("CreditsForCompany = %+v, %v; want %d / %d", shown, err, signupGrant, signupGrant)
	}
	st, err := usage.CheckBudget(ctx, res.Company.ID)
	if err != nil || st.Verdict != BudgetOK || st.BalanceMicroUSD != signupGrant {
		t.Fatalf("CheckBudget = %+v, %v; want OK at the full grant", st, err)
	}
	if len(credits.upserts) != 0 {
		t.Errorf("upserts = %d, want 0 — the lazy grant ran for a company signup already credited", len(credits.upserts))
	}
}

// AC7 / Q7: a failed grant write is a warning, not a failed signup, and the
// first budget check provisions the same $0.50.
func TestSignupSurvivesAFailedGrant(t *testing.T) {
	hook := logrustest.NewGlobal()
	defer hook.Reset()

	ctx := context.Background()
	credits := &stubCredits{getErr: domain.ErrNotFound, insErr: errors.New("connection reset by peer")}
	auth, usage := signupWithCredits(t, signupPolicy(true, signupGrant), credits)

	res, err := auth.Signup(ctx, "Toko Segar", "dina@tokosegar.id", "correct-horse")
	if err != nil {
		t.Fatalf("Signup = %v, want success — the grant is best-effort", err)
	}
	if res.AccessToken == "" || res.RefreshToken == "" {
		t.Fatalf("Signup returned no session: %+v", res)
	}

	var warned *logrus.Entry
	for _, e := range hook.AllEntries() {
		if e.Level == logrus.WarnLevel {
			warned = e
			break
		}
	}
	if warned == nil {
		t.Fatal("no Warn logged for a failed signup grant")
	}
	if warned.Data["company_id"] != res.Company.ID {
		t.Errorf("warning company_id = %v, want %s", warned.Data["company_id"], res.Company.ID)
	}

	st, err := usage.CheckBudget(ctx, res.Company.ID)
	if err != nil {
		t.Fatalf("CheckBudget: %v", err)
	}
	if len(credits.upserts) != 1 || credits.upserts[0].BalanceMicroUSD != signupGrant {
		t.Fatalf("lazy grant upserts = %+v, want one at %d", credits.upserts, signupGrant)
	}
	if st.Verdict != BudgetOK || st.BalanceMicroUSD != signupGrant {
		t.Errorf("first check = %+v, want OK at %d", st, signupGrant)
	}
}

// R1: the grant is written whatever the kill switch says, and not at all when
// the configured grant is zero.
func TestSignupGrantPolicy(t *testing.T) {
	cases := []struct {
		name        string
		policy      CreditPolicy
		wantInserts int
	}{
		{name: "enforcement on", policy: signupPolicy(true, signupGrant), wantInserts: 1},
		{
			// The switch governs refusal, not bookkeeping: a company that signs
			// up while it is off still has its credit the day it comes back on.
			name: "enforcement off", policy: signupPolicy(false, signupGrant), wantInserts: 1,
		},
		{name: "zero grant", policy: signupPolicy(true, 0), wantInserts: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			credits := &stubCredits{getErr: domain.ErrNotFound}
			auth, _ := signupWithCredits(t, tc.policy, credits)
			if _, err := auth.Signup(context.Background(), "Toko Segar", "dina@tokosegar.id", "correct-horse"); err != nil {
				t.Fatalf("Signup: %v", err)
			}
			if got := len(credits.inserts); got != tc.wantInserts {
				t.Errorf("inserts = %d, want %d", got, tc.wantInserts)
			}
		})
	}
}

// The grant is written once the admin exists, and only by signup. A signup that
// fails creates no credit for the company it left behind, and an invite joins a
// company that already has its grant (R5, AC3).
func TestOnlyACompletedSignupAsksForCredit(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("pq: connection refused")

	t.Run("a completed signup asks once, for the new company", func(t *testing.T) {
		svc, _, _ := authFixture(t)
		svc.companies = &signupCompanyRepo{}
		g := &recordingGranter{}
		res, err := svc.WithCredits(g).Signup(ctx, "Toko Segar", "dina@tokosegar.id", "correct-horse")
		if err != nil {
			t.Fatalf("Signup: %v", err)
		}
		if len(g.companies) != 1 || g.companies[0] != res.Company.ID {
			t.Errorf("granted %v, want [%s]", g.companies, res.Company.ID)
		}
	})

	t.Run("the company insert failed", func(t *testing.T) {
		svc, _, _ := authFixture(t)
		svc.companies = &signupCompanyRepo{err: boom}
		g := &recordingGranter{}
		if _, err := svc.WithCredits(g).Signup(ctx, "Toko Segar", "dina@tokosegar.id", "correct-horse"); err == nil {
			t.Fatal("Signup succeeded with a failing company insert")
		}
		if len(g.companies) != 0 {
			t.Errorf("granted %v for a company that was never created", g.companies)
		}
	})

	t.Run("the admin insert failed", func(t *testing.T) {
		svc, users, _ := authFixture(t)
		svc.companies = &signupCompanyRepo{}
		svc.users = failingUserCreate{fakeUsers: users, err: boom}
		g := &recordingGranter{}
		if _, err := svc.WithCredits(g).Signup(ctx, "Toko Segar", "dina@tokosegar.id", "correct-horse"); err == nil {
			t.Fatal("Signup succeeded with a failing user insert")
		}
		if len(g.companies) != 0 {
			t.Errorf("granted %v to a company nobody can log into", g.companies)
		}
	})

	t.Run("an email already in use", func(t *testing.T) {
		svc, users, _ := authFixture(t)
		svc.companies = &signupCompanyRepo{}
		seedUser(t, users, "dina@tokosegar.id", "correct-horse", domain.RoleAdmin)
		g := &recordingGranter{}
		if _, err := svc.WithCredits(g).Signup(ctx, "Toko Segar", "dina@tokosegar.id", "correct-horse"); !errors.Is(err, domain.ErrAlreadyExists) {
			t.Fatalf("Signup = %v, want ErrAlreadyExists", err)
		}
		if len(g.companies) != 0 {
			t.Errorf("granted %v on a refused signup", g.companies)
		}
	})

	t.Run("an accepted invite", func(t *testing.T) {
		// Accept is TeamService, which holds no granter; the session it is
		// handed comes from IssueSession, which must not grant either.
		svc, users, _ := authFixture(t)
		g := &recordingGranter{}
		member := seedUser(t, users, "budi@tokosegar.id", "correct-horse", domain.RoleMember)
		if _, err := svc.WithCredits(g).IssueSession(member); err != nil {
			t.Fatalf("IssueSession: %v", err)
		}
		if len(g.companies) != 0 {
			t.Errorf("granted %v when a teammate joined", g.companies)
		}
	})
}

// GrantSignupCredit is insert-only: a company that already has a row keeps it,
// whatever it says (R2).
func TestGrantSignupCreditNeverOverwrites(t *testing.T) {
	existing := &domain.CompanyCredits{CompanyID: "co-1", BalanceMicroUSD: -120_000, MonthlyGrantMicroUSD: signupGrant}
	credits := &stubCredits{record: existing}
	usage := newCreditService(signupPolicy(true, signupGrant), credits, &stubLLMCreds{}, nil)

	usage.GrantSignupCredit(context.Background(), "co-1")

	if credits.record != existing || existing.BalanceMicroUSD != -120_000 || existing.MonthlyGrantMicroUSD != signupGrant {
		t.Errorf("record = %+v, want the -120000 / 500000 row untouched", credits.record)
	}
	if len(credits.upserts) != 0 {
		t.Errorf("upserts = %d, want 0 — the signup grant must never use Upsert", len(credits.upserts))
	}
}

// R6 / AC9 / Q9: what the Usage page reads for a company that has not been
// checked yet, and that reading it writes nothing.
func TestCreditsForCompanyProjectsTheGrant(t *testing.T) {
	boom := errors.New("connection refused")
	legacy := &domain.CompanyCredits{CompanyID: "co-1", BalanceMicroUSD: 18_000_000, MonthlyGrantMicroUSD: 25_000_000}
	cases := []struct {
		name        string
		enforce     bool
		record      *domain.CompanyCredits
		getErr      error
		wantErr     error
		wantBalance int64
		wantGrant   int64
	}{
		{
			name: "no row, enforcement on", enforce: true, getErr: domain.ErrNotFound,
			wantBalance: signupGrant, wantGrant: signupGrant,
		},
		{
			// Pre-T-03 metering left a negative balance with no grant; the
			// first check forgives it, so that is the balance to show.
			name: "a row with no grant, enforcement on", enforce: true,
			record:      &domain.CompanyCredits{CompanyID: "co-1", BalanceMicroUSD: -40_000},
			wantBalance: signupGrant, wantGrant: signupGrant,
		},
		{
			// AC8: a legacy $25 grant is what it was.
			name: "a granted row, enforcement on", enforce: true, record: legacy,
			wantBalance: 18_000_000, wantGrant: 25_000_000,
		},
		{
			name: "no row, enforcement off", enforce: false, getErr: domain.ErrNotFound,
			wantErr: domain.ErrNotFound,
		},
		{
			name: "a row with no grant, enforcement off", enforce: false,
			record:      &domain.CompanyCredits{CompanyID: "co-1", BalanceMicroUSD: -40_000},
			wantBalance: -40_000, wantGrant: 0,
		},
		{
			name: "a lookup error, enforcement on", enforce: true, getErr: boom,
			wantErr: boom,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			credits := &stubCredits{record: tc.record, getErr: tc.getErr}
			usage := newCreditService(signupPolicy(tc.enforce, signupGrant), credits, &stubLLMCreds{}, nil)

			got, err := usage.CreditsForCompany(context.Background(), "co-1")
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("CreditsForCompany: %v", err)
			}
			if got.CompanyID != "co-1" || got.BalanceMicroUSD != tc.wantBalance || got.MonthlyGrantMicroUSD != tc.wantGrant {
				t.Errorf("got %+v, want co-1 at %d / %d", got, tc.wantBalance, tc.wantGrant)
			}
			if len(credits.upserts)+len(credits.inserts) != 0 {
				t.Errorf("reading the balance wrote %d upserts and %d inserts, want none",
					len(credits.upserts), len(credits.inserts))
			}
		})
	}
}
