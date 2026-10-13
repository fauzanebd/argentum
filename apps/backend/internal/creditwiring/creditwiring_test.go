package creditwiring

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/domain"
)

// Q7 / R4: the UsageService the api, the worker's stack and discord build from
// one config refuses a company with no grant and files a request — none of
// them provisions free credit — and all three build it here.

// loadConfig is config.Load with what it validates set, and
// CREDITS_GRANT_REQUIRES_APPROVAL as given ("" leaves it unset). The secrets
// are runs of one character for speech_defaults_test.go's reason.
func loadConfig(t *testing.T, approval string) *config.Config {
	t.Helper()
	t.Setenv("LLM_API_KEY", "test")
	t.Setenv("ARGENTUM_JWT_SECRET", strings.Repeat("j", 32))
	t.Setenv("ARGENTUM_DSN_KEY", strings.Repeat("0", 64))
	t.Setenv("DB_PASSWORD", "test")
	t.Setenv("CREDITS_ENFORCEMENT_ENABLED", "true")
	t.Setenv("CREDITS_DEFAULT_GRANT_USD", "0.5")
	t.Setenv("CREDITS_GRANT_REQUIRES_APPROVAL", approval)
	if approval == "" {
		if err := os.Unsetenv("CREDITS_GRANT_REQUIRES_APPROVAL"); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func TestPolicyFollowsTheApprovalSwitch(t *testing.T) {
	cases := []struct {
		env       string
		autoGrant bool
	}{
		{env: "", autoGrant: false},      // the default: approval
		{env: "true", autoGrant: false},  //
		{env: "TRUE", autoGrant: false},  // a typo fails closed
		{env: "fasle", autoGrant: false}, //
		{env: "false", autoGrant: true},  // the kill switch, spelled exactly
	}
	for _, tc := range cases {
		t.Run("env="+tc.env, func(t *testing.T) {
			p := Policy(loadConfig(t, tc.env))
			if p.AutoGrant != tc.autoGrant {
				t.Errorf("AutoGrant = %v, want %v", p.AutoGrant, tc.autoGrant)
			}
			if !p.Enforce || p.GrantMicroUSD != 500_000 {
				t.Errorf("policy = %+v, want enforcing at 500000", p)
			}
		})
	}
}

type noRow struct{ writes int }

func (n *noRow) Get(context.Context, string) (*domain.CompanyCredits, error) {
	return nil, domain.ErrNotFound
}
func (n *noRow) Upsert(context.Context, *domain.CompanyCredits) error         { n.writes++; return nil }
func (n *noRow) InsertIfAbsent(context.Context, *domain.CompanyCredits) error { n.writes++; return nil }
func (n *noRow) Decrement(context.Context, string, int64) error               { return nil }

type platformKeys struct{}

func (platformKeys) GetByCompany(context.Context, string) ([]*domain.CompanyLLMCredential, error) {
	return nil, nil
}
func (platformKeys) Upsert(context.Context, *domain.CompanyLLMCredential) error { return nil }
func (platformKeys) Delete(context.Context, string, domain.LLMTier) error       { return nil }

type filed struct {
	domain.CreditGrantRequestRepository
	n int
}

func (f *filed) GetForCompany(context.Context, string) (*domain.CreditGrantRequest, error) {
	return nil, domain.ErrNotFound
}
func (f *filed) InsertIfAbsent(context.Context, *domain.CreditGrantRequest) (bool, error) {
	f.n++
	return true, nil
}

func TestTheBuiltServiceRefusesAndFiles(t *testing.T) {
	cfg := loadConfig(t, "")
	credits, grants := &noRow{}, &filed{}
	svc := UsageService(cfg, nil, credits, platformKeys{}, grants, nil)
	st, err := svc.CheckBudget(context.Background(), "co-legacy")
	if err != nil || !st.Blocked() || st.FreeCredit != "pending" {
		t.Fatalf("CheckBudget = %+v, %v; want refused as pending", st, err)
	}
	if grants.n != 1 || credits.writes != 0 {
		t.Errorf("filed %d, balance writes %d; want 1 and 0", grants.n, credits.writes)
	}

	// Without the repository: refused, never granted.
	credits = &noRow{}
	st, _ = UsageService(cfg, nil, credits, platformKeys{}, nil, nil).CheckBudget(context.Background(), "co-legacy")
	if !st.Blocked() || credits.writes != 0 {
		t.Errorf("no repository: %+v, %d writes", st, credits.writes)
	}

	// The kill switch restores the lazy grant.
	credits = &noRow{}
	st, _ = UsageService(loadConfig(t, "false"), nil, credits, platformKeys{}, &filed{}, nil).CheckBudget(context.Background(), "co-legacy")
	if st.Blocked() || credits.writes != 1 {
		t.Errorf("AutoGrant: %+v, %d writes; want allowed and provisioned", st, credits.writes)
	}
}

// Every process that checks budgets builds its UsageService here, with a
// request repository, and none writes a CreditPolicy of its own. A fourth
// literal is the "missed wiring" R4 names.
func TestEveryProcessBuildsItsBudgetHere(t *testing.T) {
	for _, path := range []string{
		"../../cmd/api/bootstrap.go",
		"../bootstrap/stack.go",
		"../../cmd/discord/main.go",
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		src := string(raw)
		if !regexp.MustCompile(`creditwiring\.UsageService\([^)]*NewCreditGrantRepo|creditwiring\.UsageService\([^)]*grantRepo`).MatchString(src) {
			t.Errorf("%s does not build its UsageService with creditwiring and a request repository", path)
		}
		if strings.Contains(src, "app.CreditPolicy{") || strings.Contains(src, ".WithCredits(app.CreditPolicy") {
			t.Errorf("%s writes its own CreditPolicy", path)
		}
	}
}
