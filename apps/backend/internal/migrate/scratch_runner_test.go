//go:build scratch

package migrate

// Live arm for delivery-log phase 3bt against a scratch Postgres — never
// production. Behind the `scratch` build tag. It creates, uses and drops its own
// database, so the scratch stack's argentum schema is not touched.
//
//	SCRATCH_PG_ADMIN_DSN='postgres://argentum:gatepass@127.0.0.1:55453/postgres?sslmode=disable' \
//	go test -tags scratch -run ScratchRunner -count=1 -v ./internal/migrate/

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
)

func TestScratchRunnerSaysItIsMigratingAndNamesTheRepairForADirtyVersion(t *testing.T) {
	adminDSN := os.Getenv("SCRATCH_PG_ADMIN_DSN")
	if adminDSN == "" {
		t.Skip("SCRATCH_PG_ADMIN_DSN is not set")
	}
	admin, err := sql.Open("postgres", adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	name := fmt.Sprintf("migrate_gate_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)") })
	u, err := url.Parse(adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	dsn := u.String()

	dir := t.TempDir()
	for file, body := range map[string]string{
		"001_one.up.sql":   "CREATE TABLE gate_one (id int PRIMARY KEY);",
		"001_one.down.sql": "DROP TABLE gate_one;",
		"002_two.up.sql":   "CREATE TABLE gate_two (id int REFERENCES gate_one(id));",
		"002_two.down.sql": "DROP TABLE gate_two;",
	} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	hook := logrustest.NewGlobal()
	defer hook.Reset()

	// A fresh database: the start says it is migrating, from 0 to 2, before it
	// does, and how long it took after.
	if err := Up(dsn, dir); err != nil {
		t.Fatalf("up on a fresh database: %v", err)
	}
	var migrating, migrated *logrus.Entry
	for _, e := range hook.AllEntries() {
		switch {
		case e.Message == "control DB migrating; the API listens once this finishes":
			migrating = e
		case strings.HasPrefix(e.Message, "control DB migrated to version 2"):
			migrated = e
		}
	}
	if migrating == nil || migrating.Data["from_version"] != uint(0) || migrating.Data["to_version"] != uint(2) {
		t.Fatalf("no 'migrating' line from 0 to 2 before migrating: %+v", migrating)
	}
	if migrated == nil || migrated.Data["took_ms"] == nil {
		t.Fatalf("no 'migrated to version 2' line with took_ms: %+v", migrated)
	}
	t.Logf("fresh database: %q from %v to %v, then %q in %vms",
		migrating.Message, migrating.Data["from_version"], migrating.Data["to_version"], migrated.Message, migrated.Data["took_ms"])

	// What 2026-09-15 left behind: the newest version, dirty. The start refuses,
	// and says how to repair it rather than "Fix and force version."
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE schema_migrations SET dirty = true WHERE version = 2"); err != nil {
		t.Fatalf("mark dirty: %v", err)
	}
	err = Up(dsn, dir)
	if err == nil {
		t.Fatal("a dirty database started")
	}
	for _, want := range []string{"dirty at version 2", "SET dirty = false WHERE version = 2", "SET version = 1, dirty = false"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("dirty start error lacks %q: %v", want, err)
		}
	}
	t.Logf("dirty at 2: %v", err)

	// The repair the playbook names, applied: the next start serves.
	if _, err := db.Exec("UPDATE schema_migrations SET dirty = false WHERE version = 2"); err != nil {
		t.Fatalf("repair: %v", err)
	}
	hook.Reset()
	if err := Up(dsn, dir); err != nil {
		t.Fatalf("up after the repair: %v", err)
	}
	if last := hook.LastEntry(); last == nil || last.Message != "control DB schema already up to date" {
		t.Errorf("after the repair, last line = %+v, want 'already up to date'", last)
	}
	t.Logf("after clearing the flag: control DB schema already up to date")
}
