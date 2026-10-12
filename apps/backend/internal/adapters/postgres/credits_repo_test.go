package postgres

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Source-reading, for retention_repo_test.go's reason: what matters about the
// signup grant's statement is what it says, and a fake database answers
// whatever it was written to answer. The statement running against Postgres is
// the scratch arm in scratch_credits_test.go (RIZ-79 Q2).

func creditsStatement(t *testing.T, method string) string {
	t.Helper()
	raw, err := os.ReadFile("usage_repo.go")
	if err != nil {
		t.Fatalf("read usage_repo.go: %v", err)
	}
	code := stripComments(string(raw))
	m := regexp.MustCompile(`(?s)func \(r \*CreditsRepo\) ` + method + `\(.*?const q = ` + "`(.*?)`").FindStringSubmatch(code)
	if m == nil {
		t.Fatalf("no statement found for CreditsRepo.%s; this test is no longer reading what it thinks it is", method)
	}
	return m[1]
}

// The signup grant can never overwrite a balance (RIZ-79 R2, AC2). A DO UPDATE
// arm here would hand a company that already spent its grant a fresh one every
// time the write ran, and forgive whatever it owed.
func TestTheSignupGrantInsertNeverUpdates(t *testing.T) {
	stmt := creditsStatement(t, "InsertIfAbsent")
	if !strings.Contains(stmt, "ON CONFLICT (company_id) DO NOTHING") {
		t.Errorf("InsertIfAbsent does not end in ON CONFLICT (company_id) DO NOTHING:\n%s", strings.TrimSpace(stmt))
	}
	for _, forbidden := range []string{"DO UPDATE", "UPDATE company_credits", "SET "} {
		if strings.Contains(stmt, forbidden) {
			t.Errorf("InsertIfAbsent contains %q, so it can overwrite a balance:\n%s", forbidden, strings.TrimSpace(stmt))
		}
	}
	// Pinned against Upsert so the pair cannot drift into the same statement:
	// if both said DO NOTHING the lazy grant could never forgive pre-T-03 debt.
	if !strings.Contains(creditsStatement(t, "Upsert"), "DO UPDATE") {
		t.Error("Upsert lost its DO UPDATE arm; the lazy grant in CheckBudget depends on it")
	}
}

// RIZ-79 QA B1. `make check` has no Postgres, so this is the part of the
// decrement fix it can hold: the parameter is typed wherever it is negated or
// subtracted. An untyped `-$2` is refused by Postgres on every call; the live
// arm is TestScratchCreditsDecrementLands.
func TestTheDecrementTypesItsParameter(t *testing.T) {
	stmt := creditsStatement(t, "Decrement")
	uses, typed := strings.Count(stmt, "$2"), strings.Count(stmt, "$2::bigint")
	if uses != 2 || typed != uses {
		t.Errorf("Decrement uses $2 %d times and types it %d, want 2 and 2 — an untyped `-$2` is refused:\n%s",
			uses, typed, strings.TrimSpace(stmt))
	}
}
