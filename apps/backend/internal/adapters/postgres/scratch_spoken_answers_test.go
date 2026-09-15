//go:build scratch

package postgres_test

// Live arm for T-W8 against a scratch Postgres — never production
// (live-gate §7l). Behind the `scratch` build tag, like scratch_voice_clips_test.go,
// whose helpers from scratch_rooms_test.go it uses.
//
//	SCRATCH_PG_DSN='postgres://argentum:gatepass@127.0.0.1:55443/argentum?sslmode=disable' \
//	SCRATCH_MIGRATIONS=/tmp/tw8-gate/migrations \
//	go test -tags scratch -run Scratch089 -count=1 -v ./internal/adapters/postgres/

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"

	"github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/domain"
)

// §7l: 089 up, down, up — then every SpokenAnswerRepo statement against real
// rows in two companies, and the FK chain a deleted conversation sets off.
func TestScratch089SpokenAnswersRoundTripAndStatements(t *testing.T) {
	db, dsn, dir := scratchDB(t)
	m, err := migrate.New("file://"+dir, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("up: %v", err)
	}
	if v := version(t, m); v != 89 || !hasColumn(t, db, "spoken_answers", "refusal") {
		t.Fatalf("after up: version %d, spoken_answers present = %v", v, hasColumn(t, db, "spoken_answers", "refusal"))
	}
	if err := m.Steps(-1); err != nil {
		t.Fatalf("089 down: %v", err)
	}
	if v := version(t, m); v != 88 || hasColumn(t, db, "spoken_answers", "refusal") {
		t.Fatalf("after down: version %d", v)
	}
	if err := m.Steps(1); err != nil {
		t.Fatalf("089 up again: %v", err)
	}
	if v := version(t, m); v != 89 {
		t.Fatalf("after up again: version %d", v)
	}
	t.Logf("089: up (version 89), down (88, table gone), up (89)")

	ctx := context.Background()
	mustRow := func(q string, dest any, args ...any) {
		t.Helper()
		if err := db.QueryRow(q, args...).Scan(dest); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var coA, coB, threadA, threadB, threadGone, msgA, msgB, msgGone string
	mustRow(`INSERT INTO companies (name, slug) VALUES ('Toko A', 'tw8-a-' || gen_random_uuid()) RETURNING id`, &coA)
	mustRow(`INSERT INTO companies (name, slug) VALUES ('Toko B', 'tw8-b-' || gen_random_uuid()) RETURNING id`, &coB)
	mustRow(`INSERT INTO conversation_threads (company_id, channel) VALUES ($1, 'dashboard') RETURNING id`, &threadA, coA)
	mustRow(`INSERT INTO conversation_threads (company_id, channel) VALUES ($1, 'dashboard') RETURNING id`, &threadB, coB)
	mustRow(`INSERT INTO conversation_threads (company_id, channel) VALUES ($1, 'dashboard') RETURNING id`, &threadGone, coB)
	mustRow(`INSERT INTO messages (thread_id, role, content) VALUES ($1, 'assistant', 'Penjualan Rp 1.234.567.') RETURNING id`, &msgA, threadA)
	mustRow(`INSERT INTO messages (thread_id, role, content) VALUES ($1, 'assistant', 'Stok 1.480 unit.') RETURNING id`, &msgB, threadB)
	mustRow(`INSERT INTO messages (thread_id, role, content) VALUES ($1, 'assistant', 'Margin 18,42%.') RETURNING id`, &msgGone, threadGone)

	repo := postgres.NewSpokenAnswerRepo(db)
	now := time.Now().UTC().Truncate(time.Millisecond)
	answer := func(company, msg, key, refusal string, expires time.Time) *domain.SpokenAnswer {
		return &domain.SpokenAnswer{
			ID: uuid.NewString(), CompanyID: company, MessageID: msg, ObjectKey: key,
			MimeType: "audio/mpeg", SizeBytes: 48123, SpokenText: "Penjualan sekitar 1,2 juta rupiah.",
			Refusal: refusal, Voice: "alloy", Model: "tts-1", Chars: 34, ExpiresAt: expires,
		}
	}

	// Save, and every cast in its select list read back through ForMessage.
	kept := answer(coA, msgA, "voice/"+coA+"/answers/"+msgA+".mp3", "", now.Add(7*24*time.Hour))
	if err := repo.Save(ctx, kept); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := repo.ForMessage(ctx, coA, msgA)
	if err != nil {
		t.Fatalf("for message: %v", err)
	}
	if kept.CreatedAt.IsZero() || got.ID != kept.ID || got.CompanyID != coA || got.MessageID != msgA ||
		got.ObjectKey != kept.ObjectKey || got.SizeBytes != 48123 || got.Chars != 34 || got.Voice != "alloy" ||
		got.Model != "tts-1" || got.SpokenText != kept.SpokenText || got.Refusal != "" || !got.ExpiresAt.Equal(kept.ExpiresAt) {
		t.Fatalf("read back = %+v, saved %+v", got, kept)
	}
	t.Logf("save: row written, created_at returned; for message: every column read back")

	// A second save for the same message replaces the first: one row, refused now.
	refused := answer(coA, msgA, "", "spoken 2 juta, nearest written 1.234.567: not the written figure at the precision it was spoken", now.Add(7*24*time.Hour))
	if err := repo.Save(ctx, refused); err != nil {
		t.Fatalf("second save: %v", err)
	}
	var n int
	mustRow(`SELECT count(*) FROM spoken_answers WHERE message_id = $1`, &n, msgA)
	got, err = repo.ForMessage(ctx, coA, msgA)
	if err != nil || n != 1 || got.Refusal == "" || got.ObjectKey != "" || got.ID != kept.ID {
		t.Fatalf("after a second save: %d rows, %+v, %v; want one row, same id, refused, no key", n, got, err)
	}
	t.Logf("second save for the message: replaced in place (1 row, id kept, refusal set, key cleared)")

	// Another company: B cannot save against A's message nor read A's row; 'x' is not found.
	if err := repo.Save(ctx, answer(coB, msgA, "", "", now.Add(time.Hour))); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("save under B naming A's message: %v, want ErrNotFound", err)
	}
	if _, err := repo.ForMessage(ctx, coB, msgA); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("B reading A's spoken answer: %v, want ErrNotFound", err)
	}
	if err := repo.Save(ctx, answer(coA, "x", "", "", now)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("save with a malformed message id: %v, want ErrNotFound", err)
	}
	if _, err := repo.ForMessage(ctx, coA, "x"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("for a malformed message id: %v, want ErrNotFound", err)
	}
	t.Logf("save and read across companies, or with 'x': ErrNotFound, nothing written")

	// Due: B's expired row, and the row of a message whose conversation was deleted.
	expired := answer(coB, msgB, "voice/"+coB+"/answers/"+msgB+".mp3", "", now.Add(-time.Hour))
	orphan := answer(coB, msgGone, "voice/"+coB+"/answers/"+msgGone+".mp3", "", now.Add(24*time.Hour))
	for _, a := range []*domain.SpokenAnswer{expired, orphan} {
		if err := repo.Save(ctx, a); err != nil {
			t.Fatalf("save %s: %v", a.ID, err)
		}
	}
	if _, err := db.Exec(`DELETE FROM conversation_threads WHERE id = $1`, threadGone); err != nil {
		t.Fatalf("delete conversation: %v", err)
	}
	var orphanMsg string
	mustRow(`SELECT COALESCE(message_id::text, 'NULL') FROM spoken_answers WHERE id = $1`, &orphanMsg, orphan.ID)
	if orphanMsg != "NULL" {
		t.Fatalf("a deleted conversation's spoken answer has message_id %s; the cascade-then-SET NULL chain did not fire", orphanMsg)
	}
	due, err := repo.Due(ctx, now, 10)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	byID := map[string]*domain.SpokenAnswer{}
	for _, a := range due {
		byID[a.ID] = a
	}
	if len(byID) != 2 || byID[expired.ID] == nil || byID[orphan.ID] == nil {
		t.Fatalf("due = %d rows, want exactly the expired one and the orphan", len(due))
	}
	if byID[orphan.ID].ObjectKey != orphan.ObjectKey || byID[orphan.ID].SpokenText != "" || byID[expired.ID].CompanyID != coB {
		t.Fatalf("due rows = %+v / %+v", byID[expired.ID], byID[orphan.ID])
	}
	t.Logf("delete conversation: message cascaded, spoken answer kept with message_id NULL; due: the expired and the orphan, key and company only")

	// Delete is scoped; DeleteForCompany takes one company's rows only.
	if err := repo.Delete(ctx, coA, expired.ID); err != nil {
		t.Fatalf("delete under the wrong company: %v", err)
	}
	mustRow(`SELECT count(*) FROM spoken_answers WHERE id = $1`, &n, expired.ID)
	if n != 1 {
		t.Fatalf("company A deleted company B's spoken answer")
	}
	if err := repo.Delete(ctx, coB, expired.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := repo.Delete(ctx, coB, "x"); err != nil {
		t.Fatalf("delete of a malformed id: %v", err)
	}
	erased, err := repo.DeleteForCompany(ctx, coB)
	if err != nil || erased != 1 {
		t.Fatalf("delete for B = %d, %v; want the orphan", erased, err)
	}
	mustRow(`SELECT count(*) FROM spoken_answers WHERE company_id = $1`, &n, coA)
	if n != 1 {
		t.Fatalf("erasing B left A with %d rows, want 1", n)
	}
	t.Logf("delete: refused across companies, done in its own, 'x' not an error; delete for B: 1 row, A's untouched")
}
