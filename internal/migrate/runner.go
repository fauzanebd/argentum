// Package migrate runs the control-plane SQL migrations on backend startup.
// It wraps golang-migrate so the binary can self-bootstrap without requiring
// operators to run a separate CLI.
package migrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

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

	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			logrus.Info("control DB schema already up to date")
			return nil
		}
		return fmt.Errorf("migrate up: %w", err)
	}
	v, _, _ := m.Version()
	logrus.Infof("control DB migrated to version %d", v)
	return nil
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
