//go:build scratch

package postgres_test

// Live arm for T-W9's one new statement, against a scratch Postgres — never
// production. Behind the `scratch` build tag, with scratch_rooms_test.go's
// scratchDB helper, like the 087 arm beside it.
//
//	SCRATCH_PG_DSN='postgres://argentum:gatepass@127.0.0.1:55443/argentum?sslmode=disable' \
//	SCRATCH_MIGRATIONS=/tmp/tw9-gate/migrations \
//	go test -tags scratch -run ScratchVoiceClipsForMessage -count=1 -v ./internal/adapters/postgres/

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"

	"github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/domain"
)

// ForMessage on real rows: a message may name only the sender's clips from the
// conversation it is sent to, and a browser's bad id — malformed, another
// person's, another conversation's, another company's — drops out without
// failing the rest.
func TestScratchVoiceClipsForMessage(t *testing.T) {
	db, dsn, dir := scratchDB(t)
	m, err := migrate.New("file://"+dir, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("up: %v", err)
	}

	ctx := context.Background()
	mustRow := func(q string, dest any, args ...any) {
		t.Helper()
		if err := db.QueryRow(q, args...).Scan(dest); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var coA, coB, dewi, rina, budiB, threadA, threadA2, threadB string
	mustRow(`INSERT INTO companies (name, slug) VALUES ('Toko A', 'tw9-a-' || gen_random_uuid()) RETURNING id`, &coA)
	mustRow(`INSERT INTO companies (name, slug) VALUES ('Toko B', 'tw9-b-' || gen_random_uuid()) RETURNING id`, &coB)
	user := func(company, name string, dest *string) {
		mustRow(`INSERT INTO users (company_id, email, password_hash, role) VALUES ($1, $2 || gen_random_uuid() || '@tw9.test', 'x', 'member') RETURNING id`, dest, company, name)
	}
	user(coA, "dewi", &dewi)
	user(coA, "rina", &rina)
	user(coB, "budi", &budiB)
	thread := func(company string, dest *string) {
		mustRow(`INSERT INTO conversation_threads (company_id, channel) VALUES ($1, 'dashboard') RETURNING id`, dest, company)
	}
	thread(coA, &threadA)
	thread(coA, &threadA2)
	thread(coB, &threadB)

	repo := postgres.NewVoiceClipRepo(db)
	expires := time.Now().Add(7 * 24 * time.Hour)
	clip := func(company, thread, userID, transcript string) *domain.VoiceClip {
		c := &domain.VoiceClip{
			ID: uuid.NewString(), CompanyID: company, ThreadID: thread, UserID: userID,
			MimeType: "audio/webm", SizeBytes: 6004, Seconds: 2.75, Transcript: transcript,
			Language: "id", Model: "whisper-large-v3-turbo", ExpiresAt: expires,
		}
		if err := repo.Create(ctx, c); err != nil {
			t.Fatalf("create: %v", err)
		}
		return c
	}
	first := clip(coA, threadA, dewi, "berapa stok gudang barat")
	second := clip(coA, threadA, dewi, "minggu ini")
	rinas := clip(coA, threadA, rina, "gaji semua orang")
	otherThread := clip(coA, threadA2, dewi, "stok")
	otherCompany := clip(coB, threadB, budiB, "harga")

	ids := []string{second.ID, rinas.ID, otherThread.ID, otherCompany.ID, "x", uuid.NewString(), first.ID}
	got, err := repo.ForMessage(ctx, coA, dewi, threadA, ids)
	if err != nil {
		t.Fatalf("for message: %v", err)
	}
	heard := map[string]string{}
	var gotIDs []string
	for _, c := range got {
		heard[c.ID] = c.Transcript
		gotIDs = append(gotIDs, c.ID)
	}
	wantIDs := []string{first.ID, second.ID}
	sort.Strings(gotIDs)
	sort.Strings(wantIDs)
	if len(gotIDs) != 2 || gotIDs[0] != wantIDs[0] || gotIDs[1] != wantIDs[1] {
		t.Fatalf("for message = %v, want exactly Dewi's two clips in this conversation", gotIDs)
	}
	if heard[first.ID] != first.Transcript || heard[second.ID] != second.Transcript {
		t.Fatalf("transcripts read back: %v", heard)
	}
	t.Logf("for message: 2 of 7 ids — Dewi's two in this conversation; Rina's, another conversation's, another company's, 'x' and an unknown uuid dropped, and 'x' failed nothing")

	// The same ids asked as Rina get Rina's; asked under company B they get nothing.
	if got, err := repo.ForMessage(ctx, coA, rina, threadA, ids); err != nil || len(got) != 1 || got[0].ID != rinas.ID {
		t.Fatalf("as Rina: %d clips, %v; want Rina's one", len(got), err)
	}
	if got, err := repo.ForMessage(ctx, coB, dewi, threadA, ids); err != nil || len(got) != 0 {
		t.Fatalf("Dewi's conversation asked under company B: %d clips, %v; want none", len(got), err)
	}
	t.Logf("as Rina: her one clip; under the wrong company: none")

	// A malformed company, person or conversation is no clips, not an error.
	for _, bad := range [][3]string{{"x", dewi, threadA}, {coA, "x", threadA}, {coA, dewi, "x"}} {
		if got, err := repo.ForMessage(ctx, bad[0], bad[1], bad[2], ids); err != nil || len(got) != 0 {
			t.Fatalf("malformed %v: %d clips, %v; want none and no error", bad, len(got), err)
		}
	}
	if got, err := repo.ForMessage(ctx, coA, dewi, threadA, nil); err != nil || got != nil {
		t.Fatalf("no ids: %v, %v", got, err)
	}
	t.Logf("a malformed company, person or conversation: no clips and no error; no ids: no query")
}
