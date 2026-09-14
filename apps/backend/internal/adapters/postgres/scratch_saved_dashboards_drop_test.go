//go:build scratch

package postgres_test

// Live arm for T-D16's second half against a scratch Postgres — never
// production. Behind the `scratch` build tag, like scratch_rooms_test.go, whose
// scratchDB, hasColumn and version helpers it uses.
//
//	SCRATCH_PG_DSN='postgres://argentum:gatepass@127.0.0.1:55443/argentum?sslmode=disable' \
//	SCRATCH_MIGRATIONS=/tmp/tw7-gate/migrations \
//	go test -tags scratch -run Scratch088 -count=1 -v ./internal/adapters/postgres/
//
// Run it from a database at version 87: it writes a row into the table before
// the drop, so "the data does not round trip" is observed rather than assumed.

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/golang-migrate/migrate/v4"
)

func savedDashboardIndexes(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM pg_indexes
		WHERE indexname IN ('idx_saved_dashboards_company', 'idx_saved_dashboards_thread')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestScratch088SavedDashboardsDropRoundTrip(t *testing.T) {
	db, dsn, dir := scratchDB(t)
	m, err := migrate.New("file://"+dir, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	if v := version(t, m); v != 87 {
		t.Fatalf("start at version 87, found %d", v)
	}
	if !hasColumn(t, db, "saved_dashboards", "public_url") || savedDashboardIndexes(t, db) != 2 {
		t.Fatal("at 87 the table and its two indexes should exist")
	}
	var companyID, threadID string
	if err := db.QueryRow(`INSERT INTO companies (name, slug) VALUES ('Toko Lama', 'td16-' || gen_random_uuid()) RETURNING id`).Scan(&companyID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO conversation_threads (company_id, channel) VALUES ($1, 'dashboard') RETURNING id`, companyID).Scan(&threadID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO saved_dashboards (company_id, thread_id, metabase_dashboard_id, name, public_url)
		VALUES ($1, $2, 7, 'Penjualan', 'https://metabase.example/public/dashboard/x')`, companyID, threadID); err != nil {
		t.Fatal(err)
	}
	t.Logf("87: saved_dashboards with both indexes and one row")

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("up: %v", err)
	}
	if v := version(t, m); v != 88 || hasColumn(t, db, "saved_dashboards", "public_url") || savedDashboardIndexes(t, db) != 0 {
		t.Fatalf("after up: version %d, table present %v, indexes %d", v,
			hasColumn(t, db, "saved_dashboards", "public_url"), savedDashboardIndexes(t, db))
	}
	if !hasColumn(t, db, "dashboards", "spec") {
		t.Fatal("the native dashboards table went too")
	}
	// The conversation the row pointed at is untouched, and still deletable.
	if _, err := db.Exec(`DELETE FROM conversation_threads WHERE id = $1`, threadID); err != nil {
		t.Fatalf("delete the conversation after the drop: %v", err)
	}
	t.Logf("up: version 88, table and indexes gone, native dashboards present, a conversation deletes")

	if err := m.Steps(-1); err != nil {
		t.Fatalf("088 down: %v", err)
	}
	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM saved_dashboards`).Scan(&rows); err != nil {
		t.Fatalf("after down: %v", err)
	}
	if v := version(t, m); v != 87 || savedDashboardIndexes(t, db) != 2 || rows != 0 {
		t.Fatalf("after down: version %d, indexes %d, rows %d", v, savedDashboardIndexes(t, db), rows)
	}
	t.Logf("down: version 87, table and both indexes back, empty — the schema round trips, the data does not")

	if err := m.Steps(1); err != nil {
		t.Fatalf("088 up again: %v", err)
	}
	if v := version(t, m); v != 88 || hasColumn(t, db, "saved_dashboards", "public_url") {
		t.Fatalf("after up again: version %d", v)
	}
	t.Logf("up again: version 88, table gone")
}
