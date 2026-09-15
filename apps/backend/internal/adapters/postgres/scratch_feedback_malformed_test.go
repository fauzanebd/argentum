//go:build scratch

package postgres_test

// The feedback read against a scratch Postgres — never production (live-gate
// §7l, found beside T-W8's arms on 2026-09-14). Behind the `scratch` tag, with
// scratch_rooms_test.go's helpers.
//
//	SCRATCH_PG_DSN='postgres://argentum:gatepass@127.0.0.1:55453/argentum?sslmode=disable' \
//	SCRATCH_MIGRATIONS=/tmp/tw8-gate/migrations \
//	go test -tags scratch -run ScratchFeedbackMalformed -count=1 -v ./internal/adapters/postgres/

import (
	"context"
	"errors"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"

	"github.com/fauzanebd/argentum/internal/adapters/postgres"
)

// An id that cannot be a uuid names no message, so it has no verdicts — the
// answer a well-formed id naming no message already gets. Before 2026-09-15 it
// was Postgres's cast refusal instead, which `GET /api/messages/x/feedback`
// answered as a 500 quoting the driver.
func TestScratchFeedbackMalformedMessageIDHasNoVerdicts(t *testing.T) {
	db, dsn, dir := scratchDB(t)
	m, err := migrate.New("file://"+dir, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("up: %v", err)
	}

	var co string
	if err := db.QueryRow(`INSERT INTO companies (name, slug) VALUES ('Toko Umpan', 'fb-x-' || gen_random_uuid()) RETURNING id`).Scan(&co); err != nil {
		t.Fatalf("seed company: %v", err)
	}
	repo := postgres.NewMessageFeedbackRepo(db)
	ctx := context.Background()

	unknown, err := repo.GetByMessage(ctx, co, uuid.NewString())
	if err != nil || len(unknown) != 0 {
		t.Fatalf("GetByMessage(an unknown uuid) = %d verdicts, %v; want none and no error", len(unknown), err)
	}
	malformed, err := repo.GetByMessage(ctx, co, "x")
	if err != nil || len(malformed) != 0 {
		t.Fatalf("GetByMessage(%q) = %d verdicts, %v; want none and no error, as for an unknown uuid", "x", len(malformed), err)
	}
	t.Logf("GetByMessage: an unknown uuid and 'x' both answer no verdicts and no error")
}
