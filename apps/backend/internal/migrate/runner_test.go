package migrate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
)

func writeFiles(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("SELECT 1;"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestHighestUpVersionIsTheNewestUpFile(t *testing.T) {
	dir := writeFiles(t,
		"001_init.up.sql", "001_init.down.sql",
		"087_voice_clips.up.sql", "087_voice_clips.down.sql",
		// A down with no up is not a migration this binary can apply.
		"099_orphan.down.sql",
		"README.md", "notes_087.up.txt", "abc_x.up.sql",
	)
	got, ok, err := highestUpVersion(dir)
	if err != nil || !ok || got != 87 {
		t.Fatalf("highestUpVersion = %d, %v, %v; want 87, true, nil", got, ok, err)
	}

	empty := writeFiles(t, "README.md")
	if got, ok, err := highestUpVersion(empty); err != nil || ok || got != 0 {
		t.Fatalf("an empty directory: %d, %v, %v; want 0, false, nil", got, ok, err)
	}
	if _, _, err := highestUpVersion(filepath.Join(empty, "missing")); err == nil {
		t.Fatal("a missing directory answered no error")
	}
}

// The decision a booting API makes before it migrates. Only "the database holds
// a version this binary has no file for, cleanly" serves without migrating; a
// dirty one is refused, and everything else is left to golang-migrate as before.
func TestSchemaAheadServesOnlyACleanNewerSchema(t *testing.T) {
	boom := errors.New("connection refused")
	cases := []struct {
		name       string
		db         uint
		dirty      bool
		versionErr error
		highest    uint
		haveAny    bool
		wantAhead  bool
		wantErr    bool
	}{
		{name: "a rollback: the database is one migration ahead", db: 88, highest: 87, haveAny: true, wantAhead: true},
		{name: "up to date", db: 87, highest: 87, haveAny: true},
		{name: "behind: migrate as always", db: 86, highest: 87, haveAny: true},
		{name: "a fresh database", versionErr: migrate.ErrNilVersion, highest: 87, haveAny: true},
		{name: "the version could not be read", versionErr: boom, db: 0, highest: 87, haveAny: true},
		{name: "this binary holds no migrations", db: 88, highest: 0, haveAny: false},
		{name: "ahead and dirty: a newer release failed mid-migration", db: 88, dirty: true, highest: 87, haveAny: true, wantErr: true},
		{name: "not ahead and dirty: golang-migrate's own refusal", db: 87, dirty: true, highest: 87, haveAny: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ahead, err := schemaAhead(c.db, c.dirty, c.versionErr, c.highest, c.haveAny)
			if ahead != c.wantAhead || (err != nil) != c.wantErr {
				t.Fatalf("schemaAhead = %v, %v; want %v, error %v", ahead, err, c.wantAhead, c.wantErr)
			}
		})
	}
}

// A start logs that it is migrating before it does (2026-09-15: 089 was a
// silent start until a probe killed it). Only a database behind this binary, or a
// fresh one, is pending; up to date, ahead, unreadable and a binary with no files
// are not.
func TestPendingMigrationIsOnlyADatabaseBehindThisBinary(t *testing.T) {
	boom := errors.New("connection refused")
	cases := []struct {
		name        string
		db          uint
		versionErr  error
		highest     uint
		haveAny     bool
		wantFrom    uint
		wantTo      uint
		wantPending bool
	}{
		{name: "one behind", db: 88, highest: 89, haveAny: true, wantFrom: 88, wantTo: 89, wantPending: true},
		{name: "a fresh database", versionErr: migrate.ErrNilVersion, highest: 89, haveAny: true, wantTo: 89, wantPending: true},
		{name: "up to date", db: 89, highest: 89, haveAny: true},
		{name: "ahead", db: 90, highest: 89, haveAny: true},
		{name: "unreadable", versionErr: boom, highest: 89, haveAny: true},
		{name: "no files", db: 0, highest: 0, haveAny: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			from, to, pending := pendingMigration(c.db, c.versionErr, c.highest, c.haveAny)
			if from != c.wantFrom || to != c.wantTo || pending != c.wantPending {
				t.Fatalf("pendingMigration = %d, %d, %v; want %d, %d, %v", from, to, pending, c.wantFrom, c.wantTo, c.wantPending)
			}
		})
	}
}

// golang-migrate's "Fix and force version." names no fix. A dirty start names the
// check and both repairs, with this version and the one before it.
func TestDirtyErrorNamesTheCheckAndBothRepairs(t *testing.T) {
	msg := dirtyError(89).Error()
	for _, want := range []string{
		"dirty at version 89",
		"every object migration 89 creates",
		"UPDATE schema_migrations SET dirty = false WHERE version = 89",
		"UPDATE schema_migrations SET version = 88, dirty = false",
		"playbooks/add-migration.md",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("dirty error lacks %q:\n%s", want, msg)
		}
	}
	if got := dirtyError(1).Error(); !strings.Contains(got, "SET version = 0, dirty = false") {
		t.Errorf("version 1's previous version is 0: %s", got)
	}
}
