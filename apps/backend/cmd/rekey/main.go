// Command rekey re-seals every stored secret under the current primary
// ARGENTUM_DSN_KEY (T-H14).
//
//	go run ./cmd/rekey -check        # report only; touches nothing
//	go run ./cmd/rekey -apply        # re-seal every row not on the primary key
//
// It is the middle step of the rotation procedure in
// docs/coverage/security-hardening.md. The whole procedure:
//
//  1. Generate a new key. Set ARGENTUM_DSN_KEY to it and
//     ARGENTUM_DSN_KEYS_RETIRED to the key it replaces. Deploy.
//     Reads accept both keys from this point; writes use the new one.
//  2. `rekey -check`. It should report rows under the retired key.
//  3. `rekey -apply`. Every row is read with whichever key opens it and
//     written back under the primary.
//  4. `rekey -check` again. It must report every row on the primary and none
//     legacy. **This is the gate for step 5** — until it passes, the retired
//     key is still load-bearing.
//  5. Remove ARGENTUM_DSN_KEYS_RETIRED. Deploy. The old key is now dead.
//
// **Every step before 5 is reversible**, which is the property the old
// single-key arrangement did not have: changing the key used to mean every
// stored ciphertext stopped opening at once, discovered by an agent telling a
// customer there was "a decryption problem with the database connection
// string" mid-turn. That happened on this project twice inside a fortnight and
// cost two of twenty stored connections.
//
// **-apply is idempotent and safe to re-run.** A row already on the primary is
// skipped rather than re-sealed, so an interrupted run continues where it
// stopped, and a run against a finished rotation is a no-op that says so.
//
// # Coverage
//
// **It covers every table that holds a blob sealed by this key** — nine columns
// across nine tables, listed in `sealedColumns` below. Until 2026-09-03 it
// covered `db_connections` alone and printed a warning saying so, which meant
// step 4's gate passed while tenant LLM keys, three channel credential tables,
// MCP server auth, action configs, embed secrets and HTTP endpoint headers were
// all still sealed under the retired key. An operator who followed the
// procedure to the letter would have removed a key that eight tables still
// needed.
//
// **It reads and writes the control database directly rather than through the
// domain repositories**, which is a deliberate inversion of this codebase's
// usual rule. Every one of these repositories is company-scoped on purpose —
// `ConnectionRepo.ListAll` carries a comment explaining why a cross-tenant list
// is kept off the domain interface — and a rotation is the one job that is
// legitimately cross-tenant. Adding eight `ListAll`s to satisfy an offline
// operator command would put a cross-tenant read on eight interfaces that the
// request path can also reach. A table-driven loop in a command that never
// serves a request does not.
//
// Adding a table: add a row to `sealedColumns`. The loop needs the table, its
// primary key column, and the column holding the blob. Nothing else.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sirupsen/logrus"

	pgctl "github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/crypto"
)

// sealedColumn is one column holding a blob sealed with ARGENTUM_DSN_KEY.
type sealedColumn struct {
	table  string
	idCol  string
	column string
	// what names it in output, so an operator reading a SKIP line knows which
	// secret is stuck without looking the table up.
	what string
}

// Every column this key seals. Kept in migration order so a reviewer can walk
// it against `grep -l _encrypted migrations/control`.
//
// `nullable` is not a field because every one of these is read with the same
// `WHERE <column> IS NOT NULL` guard: a NULL blob is a credential that was
// never set, which is not a rotation problem.
var sealedColumns = []sealedColumn{
	{"db_connections", "id", "dsn_encrypted", "warehouse DSN"},
	{"company_llm_credentials", "id", "api_key_encrypted", "tenant LLM API key"},
	// The three channel credential tables are keyed by company_id, not by an
	// id of their own — one set of channel credentials per tenant. Assuming
	// `id` here made the first run of this loop fail with
	// `column "id" does not exist`, which is the cheapest possible way to
	// learn it and the argument for running an operator tool against a real
	// schema rather than reading the migrations.
	{"company_discord_credentials", "company_id", "bot_token_encrypted", "Discord bot token"},
	{"company_lark_credentials", "company_id", "app_secret_encrypted", "Lark app secret"},
	{"company_slack_credentials", "company_id", "bot_token_encrypted", "Slack bot token"},
	{"mcp_servers", "id", "auth_encrypted", "MCP server auth"},
	{"company_actions", "id", "config_encrypted", "action config"},
	{"http_endpoints", "id", "header_encrypted", "HTTP endpoint headers"},
	{"embed_keys", "id", "secret_enc", "embed signing secret"},
}

// tally is what one table's sweep found.
type tally struct {
	total, onPrimary, retired, legacy, broken, resealed, skipped int
}

func main() {
	var (
		check = flag.Bool("check", false, "report which key each stored secret is sealed under, and change nothing")
		apply = flag.Bool("apply", false, "re-seal every secret not already on the primary key")
	)
	flag.Parse()

	if *check == *apply {
		fatalf("pass exactly one of -check or -apply")
	}

	logrus.SetFormatter(&logrus.TextFormatter{})

	cfg, err := config.Load()
	if err != nil {
		fatalf("load config: %v", err)
	}
	cipher, err := crypto.NewKeyring(cfg.DSNEncryptionKeyHex, cfg.DSNRetiredKeysHex)
	if err != nil {
		fatalf("build keyring: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	db, err := pgctl.New(cfg.DatabaseURL())
	if err != nil {
		fatalf("open control database: %v", err)
	}
	defer db.Close()

	fmt.Printf("primary key: %s\n", cipher.PrimaryKeyID())
	if ids := cipher.RetiredKeyIDs(); len(ids) > 0 {
		fmt.Printf("retired keys: %v\n", ids)
	} else {
		fmt.Println("retired keys: none configured")
	}
	fmt.Println()

	var all tally
	for _, sc := range sealedColumns {
		t, err := sweep(ctx, db, cipher, sc, *apply)
		if err != nil {
			fatalf("%s.%s: %v", sc.table, sc.column, err)
		}
		report(sc, t, *apply)
		all.total += t.total
		all.onPrimary += t.onPrimary
		all.retired += t.retired
		all.legacy += t.legacy
		all.broken += t.broken
		all.resealed += t.resealed
		all.skipped += t.skipped
	}

	fmt.Printf("\nall tables: %d sealed value(s) — %d on the primary key, %d on a retired key, %d legacy\n",
		all.total, all.onPrimary, all.retired, all.legacy)

	if *check {
		if all.broken > 0 {
			fmt.Printf("\n%d value(s) open under no configured key. Find the key that seals them before going further:\n"+
				"  re-sealing cannot recover a value this process cannot read.\n", all.broken)
		}
		if all.onPrimary == all.total && all.broken == 0 {
			fmt.Println("\nrotation complete: every sealed value is on the primary key.")
			return
		}
		fmt.Println("\nrotation incomplete: run `rekey -apply`, then check again.")
		// Non-zero so a deploy pipeline can gate step 5 on it rather than on
		// somebody reading the output.
		os.Exit(1)
	}

	fmt.Printf("\nre-sealed %d value(s); skipped %d unreadable.\n", all.resealed, all.skipped)
	if all.skipped > 0 {
		os.Exit(1)
	}
	fmt.Println("run `rekey -check` to confirm before removing ARGENTUM_DSN_KEYS_RETIRED.")
}

// sweep classifies — and, when apply is set, re-seals — one column.
//
// The read is `WHERE <column> IS NOT NULL`: several of these columns are
// nullable because the credential is optional, and a NULL is not a row that
// failed to rotate.
func sweep(ctx context.Context, db *sql.DB, cipher *crypto.DSNCipher, sc sealedColumn, apply bool) (tally, error) {
	var t tally
	q := fmt.Sprintf(`SELECT %s, %s FROM %s WHERE %s IS NOT NULL`, sc.idCol, sc.column, sc.table, sc.column)
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return t, err
	}
	type row struct {
		id   string
		blob []byte
	}
	var pending []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.blob); err != nil {
			rows.Close()
			return t, err
		}
		if len(r.blob) == 0 {
			// An empty blob is an unset credential stored as zero bytes rather
			// than as NULL. Nothing to re-seal, and Decrypt would report it as
			// unreadable — which would hold a rotation open forever on a value
			// that carries no secret.
			continue
		}
		t.total++
		id, versioned := crypto.SealedUnder(r.blob)
		switch {
		case !versioned:
			t.legacy++
		case id == cipher.PrimaryKeyID():
			t.onPrimary++
			continue // already where it needs to be
		default:
			t.retired++
		}
		if _, err := cipher.Decrypt(r.blob); err != nil {
			t.broken++
			fmt.Printf("  UNREADABLE  %s.%s %s (%s): %v\n", sc.table, sc.column, r.id, sc.what, err)
			continue
		}
		pending = append(pending, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return t, err
	}
	rows.Close()

	if !apply {
		return t, nil
	}

	// A value that will not open is skipped loudly rather than failing the run:
	// the other tenants' rotations are not held hostage by one lost key, and the
	// row is named on every pass until somebody deals with it.
	for _, r := range pending {
		plain, err := cipher.Decrypt(r.blob)
		if err != nil {
			t.skipped++
			fmt.Printf("  SKIP  %s.%s %s: %v\n", sc.table, sc.column, r.id, err)
			continue
		}
		resealed, err := cipher.Encrypt(plain)
		if err != nil {
			return t, fmt.Errorf("re-seal %s: %w", r.id, err)
		}
		u := fmt.Sprintf(`UPDATE %s SET %s = $1 WHERE %s = $2`, sc.table, sc.column, sc.idCol)
		if _, err := db.ExecContext(ctx, u, resealed, r.id); err != nil {
			return t, fmt.Errorf("write %s: %w", r.id, err)
		}
		t.resealed++
	}
	t.skipped += t.broken
	return t, nil
}

func report(sc sealedColumn, t tally, apply bool) {
	if t.total == 0 {
		fmt.Printf("%-30s no sealed values\n", sc.table+"."+sc.column)
		return
	}
	line := fmt.Sprintf("%-30s %d total — %d primary, %d retired, %d legacy",
		sc.table+"."+sc.column, t.total, t.onPrimary, t.retired, t.legacy)
	if apply {
		line += fmt.Sprintf(" — re-sealed %d", t.resealed)
	}
	fmt.Println(line)
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "rekey: "+format+"\n", args...)
	os.Exit(2)
}
