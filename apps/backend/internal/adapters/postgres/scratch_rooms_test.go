//go:build scratch

package postgres_test

// Live arms for roadmap 09 against a scratch Postgres — never production
// (live-gate §7f, §7g, §7h). Behind the `scratch` build tag and an explicit DSN,
// so no ordinary `go test` or `make check` compiles or runs it.
//
//	SCRATCH_PG_DSN='postgres://argentum:gatepass@127.0.0.1:55442/argentum?sslmode=disable' \
//	SCRATCH_MIGRATIONS=/tmp/tn7-gate/migrations \
//	go test -tags scratch -run Scratch -count=1 -v ./internal/adapters/postgres/
//
// SCRATCH_MIGRATIONS is a copy of migrations/control with pgvector swapped out of
// 011, 055, 061 and 072; the embedded Postgres has no vector extension.

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"

	"github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/domain"
)

func scratchDB(t *testing.T) (*sql.DB, string, string) {
	t.Helper()
	dsn, dir := os.Getenv("SCRATCH_PG_DSN"), os.Getenv("SCRATCH_MIGRATIONS")
	if dsn == "" || dir == "" {
		t.Skip("SCRATCH_PG_DSN and SCRATCH_MIGRATIONS are not set")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, dsn, dir
}

func hasColumn(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	var ok bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_name = $1 AND column_name = $2)`, table, column).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	return ok
}

func version(t *testing.T, m *migrate.Migrate) uint {
	t.Helper()
	v, dirty, err := m.Version()
	if err != nil || dirty {
		t.Fatalf("version = %d, dirty = %v, err = %v", v, dirty, err)
	}
	return v
}

// §7f: 086 up, down, up — then the flag saved, carried through an edit, and read back.
func TestScratch086RoundTripAndTheFlagSurvivesAnEdit(t *testing.T) {
	db, dsn, dir := scratchDB(t)
	m, err := migrate.New("file://"+dir, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("up: %v", err)
	}
	if v := version(t, m); v != 86 || !hasColumn(t, db, "agents", "can_nudge") {
		t.Fatalf("after up: version %d, can_nudge present = %v", v, hasColumn(t, db, "agents", "can_nudge"))
	}
	t.Logf("up: version 86, agents.can_nudge present")

	if err := m.Steps(-1); err != nil {
		t.Fatalf("086 down: %v", err)
	}
	if v := version(t, m); v != 85 || hasColumn(t, db, "agents", "can_nudge") {
		t.Fatalf("after down: version %d, can_nudge present = %v", v, hasColumn(t, db, "agents", "can_nudge"))
	}
	t.Logf("down: version 85, agents.can_nudge gone")

	if err := m.Steps(1); err != nil {
		t.Fatalf("086 up again: %v", err)
	}
	if v := version(t, m); v != 86 || !hasColumn(t, db, "agents", "can_nudge") {
		t.Fatalf("after up again: version %d", v)
	}
	t.Logf("up again: version 86, agents.can_nudge present")

	ctx := context.Background()
	var companyID string
	if err := db.QueryRow(`INSERT INTO companies (name, slug) VALUES ('Toko Maju', 'tn7-flag-' || gen_random_uuid())
		RETURNING id`).Scan(&companyID); err != nil {
		t.Fatal(err)
	}
	repo := postgres.NewAgentRepo(db)
	// AllowedTools non-nil, as AgentService.normalizeTools always hands the repo:
	// pq.Array(nil) is NULL, and the column is NOT NULL.
	a := &domain.Agent{CompanyID: companyID, Name: "Ops", Enabled: true, CanNudge: true, AllowedTools: []string{}}
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := repo.GetByID(ctx, companyID, a.ID)
	if err != nil || !got.CanNudge {
		t.Fatalf("read after create: can_nudge = %v, err = %v", got != nil && got.CanNudge, err)
	}
	// An edit that does not mention the flag: AgentService carries the stored
	// value into the row it writes (TestAnEditThatOmitsTheFlagLeavesIt); this is
	// that row, written and read back.
	got.Name, got.Description = "Operations", "renamed"
	if err := repo.Update(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	again, err := repo.GetByID(ctx, companyID, a.ID)
	if err != nil || !again.CanNudge || again.Name != "Operations" {
		t.Fatalf("read after edit: %+v, err = %v", again, err)
	}
	var fresh bool
	if err := db.QueryRow(`INSERT INTO agents (company_id, name) VALUES ($1, 'Finance') RETURNING can_nudge`,
		companyID).Scan(&fresh); err != nil || fresh {
		t.Fatalf("a new agent's can_nudge = %v, err = %v; want the default false", fresh, err)
	}
	t.Logf("saved true, edited, read back true; a new row defaults to false")
}

// §7g, and §7h's hand-off row: the one query both scoped `/v1` doors rest on.
func TestScratchLatestAssistantSinceScopes(t *testing.T) {
	db, _, _ := scratchDB(t)
	ctx := context.Background()
	var companyID, threadID, ops, fin string
	mustRow := func(q string, dest *string, args ...any) {
		t.Helper()
		if err := db.QueryRow(q, args...).Scan(dest); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustRow(`INSERT INTO companies (name, slug) VALUES ('Toko Maju', 'tn7-q-' || gen_random_uuid()) RETURNING id`, &companyID)
	mustRow(`INSERT INTO conversation_threads (company_id, channel) VALUES ($1, 'api') RETURNING id`, &threadID, companyID)
	mustRow(`INSERT INTO agents (company_id, name) VALUES ($1, 'Ops') RETURNING id`, &ops, companyID)
	mustRow(`INSERT INTO agents (company_id, name) VALUES ($1, 'Finance') RETURNING id`, &fin, companyID)

	base := time.Now().UTC().Truncate(time.Millisecond)
	at := func(ms int) time.Time { return base.Add(time.Duration(ms) * time.Millisecond) }
	insert := func(role, content, agentID, meta string, ms int) string {
		t.Helper()
		var id string
		var agent, metadata any
		if agentID != "" {
			agent = agentID
		}
		if meta != "" {
			metadata = meta
		}
		mustRow(`INSERT INTO messages (thread_id, role, content, agent_id, metadata, created_at)
			VALUES ($1, $2, $3, $4, $5::jsonb, $6) RETURNING id`, &id, threadID, role, content, agent, metadata, at(ms))
		return id
	}
	insert("user", "we're short on SKU 4471, what happened?", "", "", 0)
	insert("assistant", "→ Finance: was a goods-in posted?", ops, `{"room_event":"nudge"}`, 1)
	insert("assistant", "One goods-in on Tuesday, 200 units.", fin, `{"asked_by":"`+ops+`"}`, 2)
	insert("assistant", "Finance had nothing to add to the question from Ops.", fin, `{"room_event":"settle","asked_by":"`+ops+`"}`, 3)
	opsAnswer := insert("assistant", "Stock shows 0 since Tuesday; I asked Finance.", ops, "", 4)

	repo := postgres.NewMessageRepo(db)
	latest := func(scope domain.AnswerScope) (string, error) {
		m, err := repo.LatestAssistantSince(ctx, threadID, at(0), scope)
		if err != nil {
			return "", err
		}
		return m.Content, nil
	}
	expect := func(label string, scope domain.AnswerScope, want string) {
		t.Helper()
		got, err := latest(scope)
		switch {
		case want == "" && errors.Is(err, domain.ErrNotFound):
			t.Logf("%s: not found, as predicted", label)
		case err != nil:
			t.Errorf("%s: err = %v, want %q", label, err, want)
		case got != want:
			t.Errorf("%s = %q, want %q", label, got, want)
		default:
			t.Logf("%s = %q", label, got)
		}
	}

	expect("AnyAnswer, Ops answered", domain.AnyAnswer, "Stock shows 0 since Tuesday; I asked Finance.")
	expect("OwnAnswer, Ops answered", domain.OwnAnswer, "Stock shows 0 since Tuesday; I asked Finance.")

	if _, err := db.Exec(`DELETE FROM messages WHERE id = $1`, opsAnswer); err != nil {
		t.Fatal(err)
	}
	expect("AnyAnswer, Ops' answer deleted", domain.AnyAnswer, "One goods-in on Tuesday, 200 units.")
	expect("OwnAnswer, Ops' answer deleted", domain.OwnAnswer, "")

	// T-N7: Ops hands the question on. The hand-off is Ops' answer to the caller —
	// found by both scopes — and Finance's answer to it is a colleague's.
	insert("assistant", "Passed to Finance: Write-offs are booked in Finance's ledger.", ops, `{"handed_off_to":"`+fin+`"}`, 5)
	expect("OwnAnswer, Ops handed off", domain.OwnAnswer, "Passed to Finance: Write-offs are booked in Finance's ledger.")
	insert("assistant", "Rp 3.200.000 was written off for SKU 4471 in Q2.", fin, `{"asked_by":"`+ops+`"}`, 6)
	expect("AnyAnswer, Finance answered the hand-off", domain.AnyAnswer, "Rp 3.200.000 was written off for SKU 4471 in Q2.")
	expect("OwnAnswer, Finance answered the hand-off", domain.OwnAnswer, "Passed to Finance: Write-offs are booked in Finance's ledger.")
}
