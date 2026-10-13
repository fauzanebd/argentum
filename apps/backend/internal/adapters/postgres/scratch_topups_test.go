//go:build scratch

package postgres_test

// Live arm for RIZ-83 against a scratch Postgres — never production. Behind the
// `scratch` build tag, like scratch_spoken_answers_test.go, whose helpers from
// scratch_rooms_test.go it uses.
//
//	SCRATCH_PG_DSN='postgres://metabase:…@127.0.0.1:5432/riz83_scratch?sslmode=disable' \
//	SCRATCH_MIGRATIONS=$PWD/migrations/control \
//	go test -tags scratch -run Scratch090 -count=1 -v ./internal/adapters/postgres/

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"

	"github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/domain"
)

// 090 up, down, up — then every TopupRepo statement against real rows in
// three companies, the two partial unique indexes, and the approve race.
func TestScratch090CreditTopupsRoundTripAndStatements(t *testing.T) {
	db, dsn, dir := scratchDB(t)
	m, err := migrate.New("file://"+dir, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("up: %v", err)
	}
	if v := version(t, m); v != 90 || !hasColumn(t, db, "credit_topups", "expected_amount_idr") || !hasColumn(t, db, "company_credits", "last_topup_at") {
		t.Fatalf("after up: version %d", v)
	}
	if err := m.Steps(-1); err != nil {
		t.Fatalf("090 down: %v", err)
	}
	if v := version(t, m); v != 89 || hasColumn(t, db, "credit_topups", "id") || hasColumn(t, db, "company_credits", "last_topup_at") {
		t.Fatalf("after down: version %d", v)
	}
	if err := m.Steps(1); err != nil {
		t.Fatalf("090 up again: %v", err)
	}
	if v := version(t, m); v != 90 {
		t.Fatalf("after up again: version %d", v)
	}
	t.Logf("090: up (version 90), down (89, table and column gone), up (90)")

	ctx := context.Background()
	mustRow := func(q string, dest any, args ...any) {
		t.Helper()
		if err := db.QueryRow(q, args...).Scan(dest); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var coA, coB, coC, coD, operator string
	mustRow(`INSERT INTO companies (name, slug) VALUES ('Toko Segar', 'riz83-a-' || gen_random_uuid()) RETURNING id`, &coA)
	mustRow(`INSERT INTO companies (name, slug) VALUES ('Kopi Senja', 'riz83-b-' || gen_random_uuid()) RETURNING id`, &coB)
	mustRow(`INSERT INTO companies (name, slug) VALUES ('Apotek Sehat', 'riz83-c-' || gen_random_uuid()) RETURNING id`, &coC)
	mustRow(`INSERT INTO companies (name, slug) VALUES ('Bengkel Maju', 'riz83-d-' || gen_random_uuid()) RETURNING id`, &coD)
	mustRow(`INSERT INTO users (company_id, email, password_hash, role) VALUES ($1, 'op-' || gen_random_uuid() || '@riz83.test', 'x', 'member') RETURNING id`, &operator, coA)
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM companies WHERE id = ANY($1::uuid[])`, "{"+strings.Join([]string{coA, coB, coC, coD}, ",")+"}")
	})

	repo := postgres.NewTopupRepo(db)
	credits := postgres.NewCreditsRepo(db)
	market, spread := int64(160_000_000), 500
	newTopup := func(company string, amount int64, code int) *domain.Topup {
		return &domain.Topup{
			CompanyID: company, AmountIDR: amount, UniqueCode: code, ExpectedAmountIDR: amount + int64(code),
			CreditMicroUSD: 5_000_000,
			FX: domain.TopupFX{Source: domain.TopupFXSourceMarket, MarketIDRPerUSDE4: &market, RateDate: "2026-10-12",
				SpreadBP: &spread, EffectiveIDRPerUSDE4: 168_000_000},
			ExpiresAt: time.Now().Add(48 * time.Hour),
		}
	}

	// Create, and the two partial unique indexes (AC2, AC3).
	a := newTopup(coA, 100_000, 347)
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("create A: %v", err)
	}
	if err := repo.Create(ctx, newTopup(coA, 50_000, 12)); !errors.Is(err, domain.ErrTopupInProgress) {
		t.Errorf("second open request for A: %v, want ErrTopupInProgress", err)
	}
	if err := repo.Create(ctx, newTopup(coB, 100_000, 347)); !errors.Is(err, domain.ErrTopupCodeTaken) {
		t.Errorf("B on A's Rp 100.347: %v, want ErrTopupCodeTaken", err)
	}
	var direct string
	if _, err := db.Exec(`INSERT INTO credit_topups (company_id, amount_idr, unique_code, expected_amount_idr, fx_source,
		fx_effective_idr_per_usd_e4, credit_micro_usd, expires_at) VALUES ($1, 100000, 347, 100347, 'fallback', 180000000, 1, now() + interval '1 day')`, coC); err == nil {
		t.Error("a direct duplicate insert of an open Rp 100.347 was accepted")
	} else {
		direct = err.Error()
	}
	if err := repo.Create(ctx, newTopup(coB, 100_000, 512)); err != nil {
		t.Fatalf("create B: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO credit_topups (company_id, amount_idr, unique_code, expected_amount_idr, fx_source,
		fx_effective_idr_per_usd_e4, credit_micro_usd, expires_at) VALUES ($1, 150500, 1, 150501, 'fallback', 180000000, 1, now())`, coC); err == nil {
		t.Error("a non-thousand amount was accepted")
	}
	t.Logf("open-amount index refused a direct duplicate: %s", direct)

	// Scoped reads and writes (AC6).
	if _, err := repo.GetForCompany(ctx, coB, a.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("B reading A's top-up: %v", err)
	}
	if _, err := repo.Cancel(ctx, coB, a.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("B cancelling A's top-up: %v", err)
	}
	if list, _ := repo.ListForCompany(ctx, coB, 20); len(list) != 1 || list[0].CompanyID != coB {
		t.Errorf("B's list: %+v", list)
	}
	got, err := repo.GetForCompany(ctx, coA, a.ID)
	if err != nil || got.FX.RateDate != "2026-10-12" || *got.FX.MarketIDRPerUSDE4 != market || *got.FX.SpreadBP != 500 {
		t.Fatalf("read back: %+v, %v", got, err)
	}

	// Expiry (AC11): an unpaid request past its expiry refuses proof and frees
	// the company.
	if _, err := db.Exec(`UPDATE credit_topups SET expires_at = now() - interval '1 minute' WHERE id = $1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AttachProof(ctx, coA, a.ID, domain.TopupProof{Key: "k", ContentType: "image/png", SHA256: "x", PayeeID: "bri"}); !errors.Is(err, domain.ErrTopupClosed) {
		t.Errorf("proof on an expired request: %v", err)
	}
	if n, err := repo.ExpireStale(ctx); err != nil || n < 1 {
		t.Errorf("ExpireStale: %d, %v", n, err)
	}
	if got, _ := repo.GetForCompany(ctx, coA, a.ID); got.Status != domain.TopupStatusExpired {
		t.Errorf("status %s, want expired", got.Status)
	}

	// Proof, cancel, review queue with a duplicate image (AC4, AC12, AC17).
	a2 := newTopup(coA, 100_000, 347) // the code is free again
	if err := repo.Create(ctx, a2); err != nil {
		t.Fatalf("A's second request: %v", err)
	}
	b, _ := repo.ListForCompany(ctx, coB, 1)
	sha := strings.Repeat("ab", 32)
	if _, err := repo.AttachProof(ctx, coB, b[0].ID, domain.TopupProof{Key: "topups/b/1.png", ContentType: "image/png", SHA256: sha, PayeeID: "bri"}); err != nil {
		t.Fatalf("B's proof: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	inReview, err := repo.AttachProof(ctx, coA, a2.ID, domain.TopupProof{Key: "topups/a/1.png", ContentType: "image/png", SHA256: sha, PayeeID: "jenius"})
	if err != nil || inReview.Status != domain.TopupStatusInReview || inReview.ProofUploadedAt == nil || *inReview.PayeeID != "jenius" {
		t.Fatalf("A's proof: %+v, %v", inReview, err)
	}
	if _, err := repo.Cancel(ctx, coA, a2.ID); !errors.Is(err, domain.ErrTopupNotCancellable) {
		t.Errorf("cancel in review: %v", err)
	}
	queue, err := repo.ListForReview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var first, later *domain.OperatorTopup
	for _, o := range queue {
		switch o.ID {
		case b[0].ID:
			first = o
		case a2.ID:
			later = o
		}
	}
	if first == nil || later == nil {
		t.Fatalf("queue is missing a row: %d rows", len(queue))
	}
	if first.DuplicateOf != nil {
		t.Errorf("the original is flagged as a copy: %+v", first.DuplicateOf)
	}
	if later.DuplicateOf == nil || later.DuplicateOf.CompanyName != "Kopi Senja" || later.DuplicateOf.ExpectedAmountIDR != 100_512 {
		t.Errorf("the later one's flag: %+v", later.DuplicateOf)
	}
	if later.CompanyName != "Toko Segar" || later.FXEffectiveIDRPerUSDE4 != 168_000_000 || later.FXRateDate == nil || *later.FXRateDate != "2026-10-12" {
		t.Errorf("operator row: %+v", later)
	}

	// Approve (AC8): 120000 / 500000, cached exhausted elsewhere — the write.
	if err := credits.Upsert(ctx, &domain.CompanyCredits{CompanyID: coA, BalanceMicroUSD: 120_000, MonthlyGrantMicroUSD: 500_000}); err != nil {
		t.Fatal(err)
	}
	approved, balance, err := repo.Approve(ctx, a2.ID, operator)
	if err != nil || balance != 5_120_000 || approved.Status != domain.TopupStatusApproved || approved.ReviewedBy != operator {
		t.Fatalf("approve: %+v, %d, %v", approved, balance, err)
	}
	cc, err := credits.Get(ctx, coA)
	if err != nil || cc.BalanceMicroUSD != 5_120_000 || cc.MonthlyGrantMicroUSD != 5_120_000 || cc.LastTopupAt == nil {
		t.Errorf("credits after approval: %+v, %v", cc, err)
	}
	if _, _, err := repo.Approve(ctx, a2.ID, operator); !errors.Is(err, domain.ErrTopupAlreadyReviewed) {
		t.Errorf("second approve: %v", err)
	}
	if _, _, err := repo.Approve(ctx, "00000000-0000-4000-8000-000000000000", operator); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("approve of nothing: %v", err)
	}

	// Reject, then approve after reject (AC9, AC10).
	reason := "No transfer of Rp 100.512 reached our BRI account."
	rej, err := repo.Reject(ctx, b[0].ID, operator, reason)
	if err != nil || rej.Status != domain.TopupStatusRejected || *rej.RejectReason != reason {
		t.Fatalf("reject: %+v, %v", rej, err)
	}
	if _, _, err := repo.Approve(ctx, b[0].ID, operator); !errors.Is(err, domain.ErrTopupAlreadyReviewed) {
		t.Errorf("approve after reject: %v", err)
	}
	if _, err := credits.Get(ctx, coB); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a rejected top-up touched B's balance: %v", err)
	}
	reviewed, err := repo.ListReviewed(ctx, 50)
	if err != nil || len(reviewed) < 2 {
		t.Fatalf("reviewed: %d, %v", len(reviewed), err)
	}
	if reviewed[0].ReviewedByEmail == nil || !strings.HasSuffix(*reviewed[0].ReviewedByEmail, "@riz83.test") {
		t.Errorf("reviewed by: %v", reviewed[0].ReviewedByEmail)
	}

	// A negative balance is paid off first (AC19), and a legacy company with
	// no row gets balance = grant = credit.
	if err := credits.Upsert(ctx, &domain.CompanyCredits{CompanyID: coC, BalanceMicroUSD: -200_000, MonthlyGrantMicroUSD: 500_000}); err != nil {
		t.Fatal(err)
	}
	c := newTopup(coC, 100_000, 1)
	if err := repo.Create(ctx, c); err != nil {
		t.Fatal(err)
	}
	_, _ = repo.AttachProof(ctx, coC, c.ID, domain.TopupProof{Key: "k", ContentType: "image/png", SHA256: "c", PayeeID: "bri"})
	if _, balance, err := repo.Approve(ctx, c.ID, operator); err != nil || balance != 4_800_000 {
		t.Errorf("approve on -200000: %d, %v", balance, err)
	}
	if cc, _ := credits.Get(ctx, coC); cc.BalanceMicroUSD != 4_800_000 || cc.MonthlyGrantMicroUSD != 5_000_000 {
		t.Errorf("C: %d / %d, want 4800000 / 5000000", cc.BalanceMicroUSD, cc.MonthlyGrantMicroUSD)
	}

	// The race (AC9 / Q11): ten approvals of one top-up, one wins.
	d := newTopup(coD, 100_000, 2)
	if err := repo.Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	_, _ = repo.AttachProof(ctx, coD, d.ID, domain.TopupProof{Key: "k", ContentType: "image/png", SHA256: "d", PayeeID: "bri"})
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins, conflicts := 0, 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := repo.Approve(ctx, d.ID, operator)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, domain.ErrTopupAlreadyReviewed):
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
	if cc, _ := credits.Get(ctx, coD); cc == nil || cc.BalanceMicroUSD != 5_000_000 || cc.MonthlyGrantMicroUSD != 5_000_000 {
		t.Errorf("D after the race: %+v, want 5000000 / 5000000 (credited once, legacy row)", cc)
	}
	t.Logf("approve: 120000→5120000 grant 5120000; -200000→4800000 grant 5000000; race 1 win / 9 conflicts")
}
