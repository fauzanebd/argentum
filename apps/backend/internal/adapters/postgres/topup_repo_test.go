package postgres

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Source-reading, for credits_repo_test.go's reason: `make check` has no
// Postgres, and what matters about these statements is what they say. The live
// arm is TestScratch090CreditTopupsRoundTripAndStatements (scratch tag).

func topupRepoSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("topup_repo.go")
	if err != nil {
		t.Fatalf("read topup_repo.go: %v", err)
	}
	return stripComments(string(raw))
}

func topupMethod(t *testing.T, name string) string {
	t.Helper()
	src := topupRepoSource(t)
	m := regexp.MustCompile(`(?s)func \(r \*TopupRepo\) ` + name + `\(.*?\n}\n`).FindString(src)
	if m == "" {
		t.Fatalf("no TopupRepo.%s; this test is no longer reading what it thinks it is", name)
	}
	return m
}

// RIZ-83 R11 / AC9: approval is credited exactly once because the status
// guard and the balance write are one transaction, and the credit parameter is
// typed wherever it is added (Decrement's RIZ-79 lesson).
func TestTheTopupApprovalIsGuardedAndTyped(t *testing.T) {
	body := topupMethod(t, "Approve")
	for _, want := range []string{
		"BeginTx",
		"WHERE t.id = $1 AND t.status = 'in_review'",
		"ON CONFLICT (company_id) DO UPDATE",
		"company_credits.balance_micro_usd + $2::bigint",
		"GREATEST(company_credits.balance_micro_usd + $2::bigint, $2::bigint)",
		"last_topup_at = now()",
		"tx.Commit()",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Approve is missing %q", want)
		}
	}
	if strings.Count(body, "$2") != strings.Count(body, "$2::bigint")+strings.Count(body, "NULLIF($2, '')::uuid") {
		t.Errorf("Approve uses $2 untyped somewhere:\n%s", body)
	}
	if !strings.Contains(topupMethod(t, "Reject"), "AND t.status = 'in_review'") {
		t.Error("Reject lost its in_review guard")
	}
}

// AC6: every tenant statement is scoped by company_id.
func TestTheTenantTopupStatementsAreScoped(t *testing.T) {
	for _, name := range []string{"GetForCompany", "ListForCompany", "AttachProof", "Cancel"} {
		if !strings.Contains(topupMethod(t, name), "company_id = $") {
			t.Errorf("TopupRepo.%s does not scope by company_id", name)
		}
	}
	// AttachProof refuses an expired unpaid request in the statement itself,
	// so a request that expired after the service read it is not reopened.
	if !strings.Contains(topupMethod(t, "AttachProof"), "t.expires_at > now()") {
		t.Error("AttachProof does not check expiry in its WHERE clause")
	}
}

// The index names Create matches on are the ones 090 creates.
func TestTheTopupIndexNamesMatchTheMigration(t *testing.T) {
	raw, err := os.ReadFile("../../../migrations/control/090_credit_topups.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{topupOneOpenIndex, topupExpectedAmountIndex} {
		if !strings.Contains(string(raw), name) {
			t.Errorf("090 does not create %s", name)
		}
	}
}
