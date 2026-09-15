// Package migrate runs the control-plane SQL migrations on backend startup.
// It wraps golang-migrate so the binary can self-bootstrap without requiring
// operators to run a separate CLI.
package migrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/sirupsen/logrus"
)

// Up applies all pending control-plane migrations against the supplied
// Postgres URL. dir is the migrations folder (e.g. migrations/control); relative
// paths resolve from the process working directory, from any ancestor directory,
// or from a direct subdirectory that contains go.mod (monorepo layout).
func Up(databaseURL, dir string) error {
	absDir, err := resolveMigrationsDir(dir)
	if err != nil {
		return err
	}
	source := "file://" + filepath.ToSlash(absDir)
	target := "postgres://" + databaseURL
	if has(databaseURL, "://") {
		target = databaseURL
	}

	m, err := migrate.New(source, target)
	if err != nil {
		return fmt.Errorf("init migrator: %w", err)
	}
	defer func() {
		_, _ = m.Close()
	}()

	// A database ahead of this binary: a rollback to an earlier image, or an
	// older pod restarted during a rollout. golang-migrate's Up refuses it with
	// "no migration found for version N", which made every rollback a pod that
	// exits on boot while the newer one keeps serving — the rollback silently not
	// happening (live-gate §7k). Every migration here is forward-compatible by
	// rule (workspace-context.md §6), and that rule is precisely the promise that
	// an older binary runs on a newer schema, so this serves on it and says so.
	//
	// A dirty newer version is the exception: a newer release failed part-way
	// through, and nothing this binary holds can say what state that left.
	if highest, haveAny, herr := highestUpVersion(absDir); herr == nil {
		v, dirty, verr := m.Version()
		ahead, err := schemaAhead(v, dirty, verr, highest, haveAny)
		if err != nil {
			return err
		}
		if ahead {
			logrus.WithFields(logrus.Fields{
				"database_version": v,
				"binary_highest":   highest,
			}).Warn("control DB schema is newer than this binary; serving on it without migrating")
			return nil
		}
		if verr == nil && dirty {
			return dirtyError(v)
		}
		// Said before migrating, not only after. A migration waiting on a lock is
		// otherwise a process that logged "starting" and went silent, which is how
		// 089 looked on 2026-09-15 until a probe killed it mid-migration and left
		// the version dirty (playbooks/add-migration.md, "A start stopped
		// mid-migration").
		if from, to, pending := pendingMigration(v, verr, highest, haveAny); pending {
			logrus.WithFields(logrus.Fields{"from_version": from, "to_version": to}).
				Info("control DB migrating; the API listens once this finishes")
		}
	}

	started := time.Now()
	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			logrus.Info("control DB schema already up to date")
			return nil
		}
		return fmt.Errorf("migrate up: %w", err)
	}
	v, _, _ := m.Version()
	logrus.WithField("took_ms", time.Since(started).Milliseconds()).Infof("control DB migrated to version %d", v)
	return nil
}

// pendingMigration reports whether Up has migrations to apply, and the span it
// will log: from the database's version (0 for a fresh database) to this
// binary's highest. An unreadable version and a binary with no files are not
// pending here; golang-migrate decides those, as before.
func pendingMigration(dbVersion uint, versionErr error, highest uint, haveAny bool) (from, to uint, pending bool) {
	if !haveAny {
		return 0, 0, false
	}
	switch {
	case errors.Is(versionErr, migrate.ErrNilVersion):
		return 0, highest, true
	case versionErr != nil:
		return 0, 0, false
	case dbVersion < highest:
		return dbVersion, highest, true
	}
	return 0, 0, false
}

// dirtyError is what a start says when the control database is dirty at
// version: a migration began, and its process stopped before golang-migrate
// cleared the flag. golang-migrate's own sentence, "Fix and force version.",
// names no fix. This one names the check and both repairs, because a migration
// file runs as one statement batch and so is either whole or absent — and which
// of the two it is decides the version to force (2026-09-15: it was whole).
func dirtyError(version uint) error {
	previous := uint(0)
	if version > 0 {
		previous = version - 1
	}
	return fmt.Errorf("migrate up: the control database is dirty at version %d: a start stopped mid-migration, "+
		"and no API starts until it is repaired. Check whether every object migration %d creates exists. "+
		"If all of them do: UPDATE schema_migrations SET dirty = false WHERE version = %d. "+
		"If none does: UPDATE schema_migrations SET version = %d, dirty = false, and the next start runs it again. "+
		"See docs/agents/playbooks/add-migration.md, \"A start stopped mid-migration\"",
		version, version, version, previous)
}

// upFile is golang-migrate's file-source name for an up migration.
var upFile = regexp.MustCompile(`^([0-9]+)_.*\.up\.sql$`)

// highestUpVersion is the newest migration this binary can apply: the largest
// version with an .up.sql file in dir. ok is false when there is none.
//
// Read from the directory rather than from the migrator's source driver,
// because the driver walks forward from a version it is given and the question
// here is about the end of the list, not the next step from a known one.
func highestUpVersion(dir string) (version uint, ok bool, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, false, fmt.Errorf("read migrations dir: %w", err)
	}
	var highest uint64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		match := upFile.FindStringSubmatch(e.Name())
		if match == nil {
			continue
		}
		n, err := strconv.ParseUint(match[1], 10, 64)
		if err != nil {
			continue
		}
		if !ok || n > highest {
			highest, ok = n, true
		}
	}
	return uint(highest), ok, nil
}

// schemaAhead decides, before golang-migrate is asked, whether a booting API
// serves on a schema it cannot migrate. True only for a database cleanly past
// this binary's newest file; an error for one past it and dirty. Behind, level,
// fresh or unreadable is false, and left to m.Up exactly as before.
func schemaAhead(dbVersion uint, dirty bool, versionErr error, highest uint, haveAny bool) (bool, error) {
	if versionErr != nil || !haveAny || dbVersion <= highest {
		return false, nil
	}
	if dirty {
		return false, fmt.Errorf("migrate up: the control database is at version %d and dirty, newer than this binary's highest migration %d; a newer release failed mid-migration and must be repaired with the release that holds it", dbVersion, highest)
	}
	return true, nil
}

func has(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func resolveMigrationsDir(dir string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("migrations dir is empty")
	}
	if filepath.IsAbs(dir) {
		if err := dirIsReadable(dir); err != nil {
			return "", err
		}
		return dir, nil
	}
	// Relative to current working directory.
	if abs, err := filepath.Abs(dir); err == nil {
		if err := dirIsReadable(abs); err == nil {
			return abs, nil
		}
	}
	// Walk upward from cwd (e.g. repo root that wraps the Go module).
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for d := wd; ; {
		candidate := filepath.Join(d, dir)
		if dirIsReadable(candidate) == nil {
			return filepath.Abs(candidate)
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	// Monorepo: cwd is the workspace root with the module in a subdirectory.
	entries, err := os.ReadDir(wd)
	if err != nil {
		return "", fmt.Errorf("migrations dir %q not found (also failed to scan %s: %w)", dir, wd, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(wd, e.Name())
		if _, err := os.Stat(filepath.Join(sub, "go.mod")); err != nil {
			continue
		}
		candidate := filepath.Join(sub, dir)
		if dirIsReadable(candidate) == nil {
			return filepath.Abs(candidate)
		}
	}
	return "", fmt.Errorf("migrations dir %q not found relative to cwd or any parent path (and no nested go.mod under %s contains it)", dir, wd)
}

func dirIsReadable(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("not a directory: %s", path)
	}
	return nil
}
