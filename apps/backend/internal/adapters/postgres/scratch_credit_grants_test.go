//go:build scratch

package postgres_test

// Live arm for RIZ-89 against a scratch Postgres — never production. Behind the
// `scratch` build tag, like scratch_topups_test.go, whose helpers from
// scratch_rooms_test.go it uses.
//
//	SCRATCH_PG_DSN='postgres://metabase:…@127.0.0.1:5432/riz89_scratch?sslmode=disable' \
//	SCRATCH_MIGRATIONS=$PWD/migrations/control \
//	go test -tags scratch -run Scratch091 -count=1 -v ./internal/adapters/postgres/

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/golang-migrate/migrate/v4"

	"github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/domain"
)

// 091 up, down, up — then every CreditGrantRepo statement against real rows:
// filing once, the operator's signals, R8's four branches (Q17) and the approve
// race (Q10).
func TestScratch091FreeCreditRoundTripAndStatements(t *testing.T) {
	db, dsn, dir := scratchDB(t)
	m, err := migrate.New("file://"+dir, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	if err := m.Migrate(91); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("up: %v", err)
	}
	if v := version(t, m); v != 91 || !hasColumn(t, db, "credit_grant_requests", "signup_ip") {
		t.Fatalf("after up: version %d", v)
	}
	if err := m.Steps(-1); err != nil {
		t.Fatalf("091 down: %v", err)
	}
	if v := version(t, m); v != 90 || hasColumn(t, db, "credit_grant_requests", "id") {
		t.Fatalf("after down: version %d", v)
	}
	if err := m.Steps(1); err != nil {
		t.Fatalf("091 up again: %v", err)
	}
	if v := version(t, m); v != 91 {
		t.Fatalf("after up again: version %d", v)
	}
	t.Logf("091: up (version 91), down (90, table gone), up (91)")

	ctx := context.Background()
	mustRow := func(q string, dest any, args ...any) {
		t.Helper()
		if err := db.QueryRow(q, args...).Scan(dest); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	newCompany := func(name string) string {
		var id string
		mustRow(`INSERT INTO companies (name, slug) VALUES ($1, 'riz89-' || gen_random_uuid()) RETURNING id`, &id, name)
		return id
	}
	coA, coB, coC, coD, coE, coF := newCompany("Toko Segar"), newCompany("Apotek Sehat"),
		newCompany("Kopi Senja"), newCompany("Bengkel Maju"), newCompany("abc store"), newCompany("Warung Race")
	var admin, operator string
	mustRow(`INSERT INTO users (company_id, email, password_hash, role) VALUES ($1, 'dina-' || gen_random_uuid() || '@riz89.test', 'x', 'admin') RETURNING id`, &admin, coA)
	mustRow(`INSERT INTO users (company_id, email, password_hash, role) VALUES ($1, 'op-' || gen_random_uuid() || '@riz89.test', 'x', 'member') RETURNING id`, &operator, coB)
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM companies WHERE id = ANY($1::uuid[])`,
			"{"+strings.Join([]string{coA, coB, coC, coD, coE, coF}, ",")+"}")
	})

	repo := postgres.NewCreditGrantRepo(db)
	credits := postgres.NewCreditsRepo(db)
	const amount = int64(500_000)
	file := func(company, by, ip string) bool {
		t.Helper()
		ok, err := repo.InsertIfAbsent(ctx, &domain.CreditGrantRequest{
			CompanyID: company, AmountMicroUSD: amount, RequestedBy: by, SignupIP: ip,
		})
		if err != nil {
			t.Fatalf("file %s: %v", company, err)
		}
		return ok
	}

	// Filing (AC1, AC3): once per company, with and without an IP.
	if !file(coA, admin, "203.0.113.7") {
		t.Fatal("A's first request was not inserted")
	}
	if file(coA, admin, "203.0.113.7") {
		t.Error("A was filed twice")
	}
	got, err := repo.GetForCompany(ctx, coA)
	if err != nil || got.Status != domain.CreditGrantStatusPending || got.AmountMicroUSD != amount ||
		got.RequestedBy != admin || got.SignupIP != "203.0.113.7" {
		t.Fatalf("A read back: %+v, %v", got, err)
	}
	if _, err := repo.GetForCompany(ctx, coF); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("F with no request: %v", err)
	}
	if !file(coB, "", "") {
		t.Fatal("B's lazy request was not inserted")
	}
	if b, _ := repo.GetForCompany(ctx, coB); b.SignupIP != "" || b.RequestedBy != "" {
		t.Errorf("lazy request: %+v", b)
	}

	// Signals (R11): a data source on A, and E sharing its IP with two others.
	if _, err := db.Exec(`INSERT INTO db_connections (company_id, db_type, label, dsn_encrypted) VALUES ($1, 'postgres', 'shop', '\x00')`, coA); err != nil {
		t.Fatalf("add a data source to A: %v", err)
	}
	file(coC, "", "198.51.100.9")
	file(coD, "", "198.51.100.9")
	file(coE, "", "198.51.100.9")
	queue, err := repo.ListPending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byCompany := map[string]*domain.OperatorFreeCredit{}
	for _, o := range queue {
		byCompany[o.CompanyID] = o
	}
	if a := byCompany[coA]; a == nil || a.CompanyName != "Toko Segar" || !strings.HasSuffix(a.RequestedByEmail, "@riz89.test") ||
		a.SameIPSignups7d != 0 || !a.HasDataSource {
		t.Errorf("A in the queue: %+v", a)
	}
	if e := byCompany[coE]; e == nil || e.SameIPSignups7d != 2 || e.HasDataSource {
		t.Errorf("E in the queue: %+v, want 2 other sign-ups from its IP", e)
	}
	if b := byCompany[coB]; b == nil || b.SameIPSignups7d != 0 || b.RequestedByEmail != "" {
		t.Errorf("B (no IP) in the queue: %+v", b)
	}

	// R8 (Q17): (a) no row.
	approved, err := repo.Approve(ctx, got.ID, operator)
	if err != nil || approved.AddedMicroUSD != amount || approved.BalanceMicroUSD != amount ||
		approved.Request.Status != domain.CreditGrantStatusApproved || approved.Request.ReviewedBy != operator {
		t.Fatalf("approve A (no row): %+v, %v", approved, err)
	}
	if cc, _ := credits.Get(ctx, coA); cc == nil || cc.BalanceMicroUSD != amount || cc.MonthlyGrantMicroUSD != amount || cc.LastTopupAt != nil {
		t.Errorf("A: %+v, want 500000 / 500000 and no top-up date", cc)
	}
	if _, err := repo.Approve(ctx, got.ID, operator); !errors.Is(err, domain.ErrCreditGrantAlreadyReviewed) {
		t.Errorf("second approve: %v", err)
	}
	if _, err := repo.Reject(ctx, got.ID, operator); !errors.Is(err, domain.ErrCreditGrantAlreadyReviewed) {
		t.Errorf("reject after approve: %v", err)
	}
	if _, err := repo.Approve(ctx, "00000000-0000-4000-8000-000000000000", operator); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("approve of nothing: %v", err)
	}

	// (b) a zero-grant row with a negative balance (Decrement's shape).
	if err := credits.Decrement(ctx, coB, 20_000); err != nil {
		t.Fatal(err)
	}
	b, _ := repo.GetForCompany(ctx, coB)
	if res, err := repo.Approve(ctx, b.ID, operator); err != nil || res.AddedMicroUSD != amount || res.BalanceMicroUSD != amount {
		t.Errorf("approve B (-20000 / 0): %+v, %v", res, err)
	}
	if cc, _ := credits.Get(ctx, coB); cc.BalanceMicroUSD != amount || cc.MonthlyGrantMicroUSD != amount {
		t.Errorf("B: %d / %d, want 500000 / 500000", cc.BalanceMicroUSD, cc.MonthlyGrantMicroUSD)
	}

	// (c) topped up while waiting (AC11): 5760000 paid, then approved.
	if _, err := db.Exec(`INSERT INTO company_credits (company_id, balance_micro_usd, monthly_grant_micro_usd, last_topup_at, updated_at)
		VALUES ($1, 5760000, 5760000, now(), now())`, coC); err != nil {
		t.Fatal(err)
	}
	c, _ := repo.GetForCompany(ctx, coC)
	if res, err := repo.Approve(ctx, c.ID, operator); err != nil || res.AddedMicroUSD != amount || res.BalanceMicroUSD != 6_260_000 {
		t.Errorf("approve C (topped up): %+v, %v", res, err)
	}
	if cc, _ := credits.Get(ctx, coC); cc.BalanceMicroUSD != 6_260_000 || cc.MonthlyGrantMicroUSD != 6_260_000 {
		t.Errorf("C: %d / %d, want 6260000 / 6260000", cc.BalanceMicroUSD, cc.MonthlyGrantMicroUSD)
	}

	// (d) already holds free credit, never topped up: nothing added.
	if err := credits.Upsert(ctx, &domain.CompanyCredits{CompanyID: coD, BalanceMicroUSD: 310_000, MonthlyGrantMicroUSD: amount}); err != nil {
		t.Fatal(err)
	}
	d, _ := repo.GetForCompany(ctx, coD)
	if res, err := repo.Approve(ctx, d.ID, operator); err != nil || res.AddedMicroUSD != 0 || res.BalanceMicroUSD != 310_000 ||
		res.Request.Status != domain.CreditGrantStatusApproved {
		t.Errorf("approve D (already granted): %+v, %v", res, err)
	}
	if cc, _ := credits.Get(ctx, coD); cc.BalanceMicroUSD != 310_000 || cc.MonthlyGrantMicroUSD != amount {
		t.Errorf("D: %d / %d, want 310000 / 500000 untouched", cc.BalanceMicroUSD, cc.MonthlyGrantMicroUSD)
	}

	// Reject (AC7): final, no balance.
	e, _ := repo.GetForCompany(ctx, coE)
	rej, err := repo.Reject(ctx, e.ID, operator)
	if err != nil || rej.Status != domain.CreditGrantStatusRejected || rej.ReviewedAt == nil {
		t.Fatalf("reject E: %+v, %v", rej, err)
	}
	if _, err := repo.Approve(ctx, e.ID, operator); !errors.Is(err, domain.ErrCreditGrantAlreadyReviewed) {
		t.Errorf("approve after reject: %v", err)
	}
	if _, err := credits.Get(ctx, coE); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a rejection touched E's balance: %v", err)
	}
	reviewed, err := repo.ListReviewed(ctx, 50)
	if err != nil || len(reviewed) < 5 {
		t.Fatalf("reviewed: %d, %v", len(reviewed), err)
	}
	if reviewed[0].ReviewedByEmail == "" || reviewed[0].ReviewedAt == nil {
		t.Errorf("newest decision: %+v", reviewed[0])
	}

	// The race (AC6 / Q10): ten approvals of one request, one wins, one $0.50.
	file(coF, "", "")
	f, _ := repo.GetForCompany(ctx, coF)
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins, conflicts := 0, 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := repo.Approve(ctx, f.ID, operator)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, domain.ErrCreditGrantAlreadyReviewed):
				conflicts++
			default:
				t.Errorf("approve: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins != 1 || conflicts != 9 {
		t.Errorf("%d wins, %d conflicts; want 1 and 9", wins, conflicts)
	}
	if cc, _ := credits.Get(ctx, coF); cc == nil || cc.BalanceMicroUSD != amount || cc.MonthlyGrantMicroUSD != amount {
		t.Errorf("F after the race: %+v, want 500000 / 500000 (credited once)", cc)
	}
	t.Logf("approve: no row → 500000/500000; -20000/0 → 500000/500000; topped-up 5760000 → 6260000 (+500000); granted 310000/500000 → +0; race 1 win / 9 conflicts")
}
