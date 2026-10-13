package postgres

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Source-reading, for topup_repo_test.go's reason: `make check` has no
// Postgres. The live arm — the four R8 branches against real rows and the
// approve race — is TestScratch091FreeCreditRoundTripAndStatements (scratch
// tag).

func creditGrantSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("credit_grant_repo.go")
	if err != nil {
		t.Fatalf("read credit_grant_repo.go: %v", err)
	}
	return stripComments(string(raw))
}

func creditGrantFunc(t *testing.T, signature string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)func ` + regexp.QuoteMeta(signature) + `\(.*?\n}\n`).FindString(creditGrantSource(t))
	if m == "" {
		t.Fatalf("no %s; this test is no longer reading what it thinks it is", signature)
	}
	return m
}

// R8 / AC6: the free credit is written exactly once — the pending guard and the
// balance write are one transaction — and every parameter added to a balance
// is typed (Decrement's RIZ-79 lesson).
func TestTheFreeCreditApprovalIsGuardedAndTyped(t *testing.T) {
	approve := creditGrantFunc(t, "(r *CreditGrantRepo) Approve")
	for _, want := range []string{
		"BeginTx",
		"WHERE r.id = $1 AND r.status = 'pending'",
		"writeFreeCredit(ctx, tx,",
		"tx.Commit()",
	} {
		if !strings.Contains(approve, want) {
			t.Errorf("Approve is missing %q", want)
		}
	}

	write := creditGrantFunc(t, "writeFreeCredit")
	for _, want := range []string{
		// The row is locked before it is read, so the branch cannot be decided
		// on a balance that changes before the write.
		"FOR UPDATE",
		// A row that appeared since the read is never overwritten by the
		// insert's arm; the branches decide it.
		"ON CONFLICT (company_id) DO NOTHING",
		"company_credits.balance_micro_usd + $2::bigint",
		"GREATEST(company_credits.balance_micro_usd + $2::bigint, $2::bigint)",
		"case grant == 0:",
		"case toppedUp:",
	} {
		if !strings.Contains(write, want) {
			t.Errorf("writeFreeCredit is missing %q", want)
		}
	}
	if n := strings.Count(write, "$2"); n != strings.Count(write, "$2::bigint") {
		t.Errorf("writeFreeCredit uses $2 untyped somewhere:\n%s", write)
	}
	if strings.Contains(write, "last_topup_at = now()") {
		t.Error("free credit sets last_topup_at; it is not a top-up, and the Usage card would caption it as one")
	}

	if !strings.Contains(creditGrantFunc(t, "(r *CreditGrantRepo) Reject"), "AND r.status = 'pending'") {
		t.Error("Reject lost its pending guard")
	}
}

// The tenant's two statements are scoped by company_id, and filing is
// insert-only: a decided request is never reopened by a later check.
func TestTheTenantFreeCreditStatementsAreScoped(t *testing.T) {
	for _, sig := range []string{"(r *CreditGrantRepo) InsertIfAbsent", "(r *CreditGrantRepo) GetForCompany"} {
		body := creditGrantFunc(t, sig)
		if !strings.Contains(body, "company_id") {
			t.Errorf("%s does not name company_id", sig)
		}
	}
	insert := creditGrantFunc(t, "(r *CreditGrantRepo) InsertIfAbsent")
	if !strings.Contains(insert, "ON CONFLICT (company_id) DO NOTHING") || strings.Contains(insert, "DO UPDATE") {
		t.Errorf("InsertIfAbsent must never update an existing request:\n%s", insert)
	}
}

// The unique key InsertIfAbsent relies on, and the two indexes the operator's
// reads need, are the ones 091 creates.
func TestTheFreeCreditMigrationMatchesTheRepository(t *testing.T) {
	raw, err := os.ReadFile("../../../migrations/control/091_credit_grant_requests.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := string(raw)
	for _, want := range []string{
		"company_id       UUID NOT NULL UNIQUE",
		"CHECK (status IN ('pending', 'approved', 'rejected'))",
		"CHECK (amount_micro_usd > 0)",
		"signup_ip        INET",
		"ON credit_grant_requests (status, created_at)",
		"ON credit_grant_requests (signup_ip, created_at) WHERE signup_ip IS NOT NULL",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("091 up is missing %q", want)
		}
	}
	down, err := os.ReadFile("../../../migrations/control/091_credit_grant_requests.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(down), "DROP TABLE IF EXISTS credit_grant_requests") {
		t.Error("091 down does not drop the table")
	}
}
