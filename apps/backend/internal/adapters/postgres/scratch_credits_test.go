//go:build scratch

package postgres_test

// The live arm of RIZ-79 Q2 against an already-migrated scratch or local
// Postgres — never production. Behind the `scratch` build tag and an explicit
// DSN, so no ordinary `go test` or `make check` compiles or runs it. It needs
// no SCRATCH_MIGRATIONS: company_credits has existed since 003.
//
//	SCRATCH_PG_DSN='postgres://metabase:…@127.0.0.1:5432/argentum?sslmode=disable' \
//	go test -tags scratch -run ScratchCredits -count=1 -v ./internal/adapters/postgres/

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq"

	"github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/domain"
)

// An overdrawn company stays overdrawn when the signup grant's write runs for
// it again: InsertIfAbsent conflicts and does nothing.
func TestScratchCreditsInsertIfAbsentLeavesAnExistingRowAlone(t *testing.T) {
	dsn := os.Getenv("SCRATCH_PG_DSN")
	if dsn == "" {
		t.Skip("SCRATCH_PG_DSN is not set")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	var companyID string
	if err := db.QueryRowContext(ctx,
		// The slug is unique; a run whose cleanup never happened must not
		// make every later run fail on the seed.
		`INSERT INTO companies (name, slug) VALUES ('RIZ-79 Q2 scratch', 'riz-79-q2-' || gen_random_uuid()) RETURNING id`,
	).Scan(&companyID); err != nil {
		t.Fatalf("seed company: %v", err)
	}
	// company_credits cascades with the company.
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM companies WHERE id = $1`, companyID) })

	repo := postgres.NewCreditsRepo(db)
	fresh := &domain.CompanyCredits{CompanyID: companyID, BalanceMicroUSD: 500_000, MonthlyGrantMicroUSD: 500_000}

	// First write on a company with no row: it lands.
	if err := repo.InsertIfAbsent(ctx, fresh); err != nil {
		t.Fatalf("first InsertIfAbsent: %v", err)
	}
	got, err := repo.Get(ctx, companyID)
	if err != nil || got.BalanceMicroUSD != 500_000 || got.MonthlyGrantMicroUSD != 500_000 {
		t.Fatalf("after the first write: %+v, %v; want 500000 / 500000", got, err)
	}

	// Spend it below zero, then run the signup write again.
	if _, err := db.ExecContext(ctx,
		`UPDATE company_credits SET balance_micro_usd = -120000 WHERE company_id = $1`, companyID); err != nil {
		t.Fatalf("overdraw: %v", err)
	}
	if err := repo.InsertIfAbsent(ctx, fresh); err != nil {
		t.Fatalf("second InsertIfAbsent: %v", err)
	}
	got, err = repo.Get(ctx, companyID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.BalanceMicroUSD != -120_000 || got.MonthlyGrantMicroUSD != 500_000 {
		t.Errorf("after the second write: %d / %d, want -120000 / 500000 — the row was overwritten",
			got.BalanceMicroUSD, got.MonthlyGrantMicroUSD)
	}

	var rows int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM company_credits WHERE company_id = $1`, companyID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("company_credits rows = %d, want 1", rows)
	}
	t.Logf("company %s: -120000 / 500000 survived a second InsertIfAbsent; 1 row", companyID)
}
