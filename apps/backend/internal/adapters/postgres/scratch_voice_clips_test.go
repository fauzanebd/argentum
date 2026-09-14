//go:build scratch

package postgres_test

// Live arm for T-W7 against a scratch Postgres — never production
// (live-gate §7j). Behind the `scratch` build tag, like scratch_rooms_test.go,
// whose scratchDB, hasColumn and version helpers it uses.
//
//	SCRATCH_PG_DSN='postgres://argentum:gatepass@127.0.0.1:55443/argentum?sslmode=disable' \
//	SCRATCH_MIGRATIONS=/tmp/tw7-gate/migrations \
//	go test -tags scratch -run Scratch087 -count=1 -v ./internal/adapters/postgres/

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

// §7j: 087 up, down, up — then every VoiceClipRepo statement against real rows
// in two companies.
func TestScratch087VoiceClipsRoundTripAndStatements(t *testing.T) {
	db, dsn, dir := scratchDB(t)
	m, err := migrate.New("file://"+dir, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("up: %v", err)
	}
	if v := version(t, m); v != 87 || !hasColumn(t, db, "voice_clips", "transcript") {
		t.Fatalf("after up: version %d, voice_clips present = %v", v, hasColumn(t, db, "voice_clips", "transcript"))
	}
	if err := m.Steps(-1); err != nil {
		t.Fatalf("087 down: %v", err)
	}
	if v := version(t, m); v != 86 || hasColumn(t, db, "voice_clips", "transcript") {
		t.Fatalf("after down: version %d", v)
	}
	if err := m.Steps(1); err != nil {
		t.Fatalf("087 up again: %v", err)
	}
	if v := version(t, m); v != 87 {
		t.Fatalf("after up again: version %d", v)
	}
	t.Logf("087: up (version 87), down (86, table gone), up (87)")

	ctx := context.Background()
	mustRow := func(q string, dest any, args ...any) {
		t.Helper()
		if err := db.QueryRow(q, args...).Scan(dest); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var coA, coB, threadA, threadB, threadGone string
	mustRow(`INSERT INTO companies (name, slug) VALUES ('Toko A', 'tw7-a-' || gen_random_uuid()) RETURNING id`, &coA)
	mustRow(`INSERT INTO companies (name, slug) VALUES ('Toko B', 'tw7-b-' || gen_random_uuid()) RETURNING id`, &coB)
	mustRow(`INSERT INTO conversation_threads (company_id, channel) VALUES ($1, 'dashboard') RETURNING id`, &threadA, coA)
	mustRow(`INSERT INTO conversation_threads (company_id, channel) VALUES ($1, 'dashboard') RETURNING id`, &threadB, coB)
	mustRow(`INSERT INTO conversation_threads (company_id, channel) VALUES ($1, 'dashboard') RETURNING id`, &threadGone, coB)

	repo := postgres.NewVoiceClipRepo(db)
	now := time.Now().UTC().Truncate(time.Millisecond)
	clip := func(company, thread, key string, expires time.Time) *domain.VoiceClip {
		return &domain.VoiceClip{
			ID: uuid.NewString(), CompanyID: company, ThreadID: thread, ObjectKey: key,
			MimeType: "audio/webm", SizeBytes: 23456, Seconds: 3.25,
			Transcript: "berapa stok gudang barat", Language: "id", Model: "whisper-large-v3-turbo",
			ExpiresAt: expires,
		}
	}

	// Create, and every cast in its select list.
	kept := clip(coA, threadA, "voice/"+coA+"/kept.webm", now.Add(7*24*time.Hour))
	if err := repo.Create(ctx, kept); err != nil {
		t.Fatalf("create: %v", err)
	}
	var seconds float64
	var size int64
	var userNull, lang string
	mustRow(`SELECT seconds FROM voice_clips WHERE id = $1`, &seconds, kept.ID)
	mustRow(`SELECT size_bytes FROM voice_clips WHERE id = $1`, &size, kept.ID)
	mustRow(`SELECT COALESCE(user_id::text, 'NULL') FROM voice_clips WHERE id = $1`, &userNull, kept.ID)
	mustRow(`SELECT language FROM voice_clips WHERE id = $1`, &lang, kept.ID)
	if kept.CreatedAt.IsZero() || seconds != 3.25 || size != 23456 || userNull != "NULL" || lang != "id" {
		t.Fatalf("row read back: created %v, seconds %v, size %d, user %s, lang %q", kept.CreatedAt, seconds, size, userNull, lang)
	}
	t.Logf("create: row written, created_at returned, seconds/size/NULL user/language read back")

	// Another company's conversation, and a malformed id: not found, nothing written.
	cross := clip(coA, threadB, "", now.Add(time.Hour))
	if err := repo.Create(ctx, cross); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("create under A naming B's conversation: %v, want ErrNotFound", err)
	}
	var n int
	mustRow(`SELECT count(*) FROM voice_clips WHERE id = $1`, &n, cross.ID)
	if n != 0 {
		t.Fatalf("the cross-company insert wrote %d rows", n)
	}
	if err := repo.Create(ctx, clip(coA, "x", "", now)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("create with a malformed conversation id: %v, want ErrNotFound", err)
	}
	t.Logf("create naming another company's conversation, or 'x': ErrNotFound, no row")

	// Due: expired in A, and B's clip whose conversation is deleted (SET NULL).
	expired := clip(coA, threadA, "voice/"+coA+"/expired.webm", now.Add(-time.Hour))
	live := clip(coB, threadB, "voice/"+coB+"/live.webm", now.Add(24*time.Hour))
	orphan := clip(coB, threadGone, "", now.Add(24*time.Hour))
	for _, c := range []*domain.VoiceClip{expired, live, orphan} {
		if err := repo.Create(ctx, c); err != nil {
			t.Fatalf("create %s: %v", c.ID, err)
		}
	}
	if _, err := db.Exec(`DELETE FROM conversation_threads WHERE id = $1`, threadGone); err != nil {
		t.Fatalf("delete conversation: %v", err)
	}
	var orphanThread string
	mustRow(`SELECT COALESCE(thread_id::text, 'NULL') FROM voice_clips WHERE id = $1`, &orphanThread, orphan.ID)
	if orphanThread != "NULL" {
		t.Fatalf("a deleted conversation's clip has thread_id %s; SET NULL did not fire (or it cascaded)", orphanThread)
	}
	due, err := repo.Due(ctx, now, 10)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	got := map[string]*domain.VoiceClip{}
	for _, c := range due {
		got[c.ID] = c
	}
	if len(got) != 2 || got[expired.ID] == nil || got[orphan.ID] == nil {
		t.Fatalf("due = %d clips, want exactly the expired one and the orphan", len(due))
	}
	if got[expired.ID].ObjectKey != expired.ObjectKey || got[expired.ID].CompanyID != coA || got[orphan.ID].CompanyID != coB {
		t.Fatalf("due rows = %+v / %+v", got[expired.ID], got[orphan.ID])
	}
	if got[expired.ID].Transcript != "" {
		t.Fatalf("the cross-company read returned a transcript")
	}
	t.Logf("delete conversation: clip kept with thread_id NULL; due: the expired and the orphan, across companies, no transcript")

	// Delete is scoped: B cannot delete A's clip; A can; a malformed id is not an error.
	if err := repo.Delete(ctx, coB, expired.ID); err != nil {
		t.Fatalf("delete under the wrong company: %v", err)
	}
	mustRow(`SELECT count(*) FROM voice_clips WHERE id = $1`, &n, expired.ID)
	if n != 1 {
		t.Fatalf("company B deleted company A's clip")
	}
	if err := repo.Delete(ctx, coA, expired.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	mustRow(`SELECT count(*) FROM voice_clips WHERE id = $1`, &n, expired.ID)
	if n != 0 {
		t.Fatalf("delete left the row")
	}
	if err := repo.Delete(ctx, coA, "x"); err != nil {
		t.Fatalf("delete of a malformed id: %v", err)
	}
	t.Logf("delete: refused across companies (row stays), done in its own, 'x' is not an error")

	// DeleteForCompany: A's remaining clip, none of B's.
	erased, err := repo.DeleteForCompany(ctx, coA)
	if err != nil || erased != 1 {
		t.Fatalf("delete for A = %d, %v; want 1", erased, err)
	}
	mustRow(`SELECT count(*) FROM voice_clips WHERE company_id = $1`, &n, coB)
	if n != 2 {
		t.Fatalf("erasing A left B with %d clips, want 2", n)
	}
	t.Logf("delete for company A: 1 row; B's 2 untouched")
}
