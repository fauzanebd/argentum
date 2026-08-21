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
// **It covers `db_connections` and nothing else yet, and it says so on every
// run rather than implying otherwise.** The same key seals tenant LLM
// credentials, the Discord/Lark/Slack credential tables, MCP server tokens,
// embed signing secrets and HTTP endpoint secrets. Each is the same three
// lines against a different repository; each also needs that repository to
// expose a cross-tenant list, which several deliberately do not (see
// ConnectionRepo.ListAll's comment on why it is absent from the domain
// interface). Extending this loop is the remaining work and it is mechanical —
// what is not mechanical, and is why the caveat is printed rather than filed,
// is that a rotation somebody believes is finished when it is not is worse
// than one they know is partial.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sirupsen/logrus"

	pgctl "github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/crypto"
)

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

	conns := pgctl.NewConnectionRepo(db)
	rows, err := conns.ListAll(ctx)
	if err != nil {
		fatalf("list connections: %v", err)
	}

	var onPrimary, legacy, retired, broken int
	for _, c := range rows {
		id, versioned := crypto.SealedUnder(c.DSNEncrypted)
		switch {
		case !versioned:
			legacy++
		case id == cipher.PrimaryKeyID():
			onPrimary++
		default:
			retired++
		}
		if _, err := cipher.Decrypt(c.DSNEncrypted); err != nil {
			broken++
			fmt.Printf("  UNREADABLE  %s (company %s, %q): %v\n", c.ID, c.CompanyID, c.Label, err)
		}
	}

	fmt.Printf("db_connections: %d total — %d on the primary key, %d on a retired key, %d in the legacy format\n",
		len(rows), onPrimary, retired, legacy)
	if broken > 0 {
		fmt.Printf("%d row(s) open under no configured key. Find the key that seals them before going further:\n"+
			"  re-sealing cannot recover a row this process cannot read.\n", broken)
	}

	fmt.Println("\nNOTE: this tool covers db_connections only. Tenant LLM credentials, the\n" +
		"Discord/Lark/Slack credential tables, MCP server tokens, embed signing secrets\n" +
		"and HTTP endpoint secrets are sealed with the same key and are NOT re-sealed\n" +
		"here. Keep the retired key configured until those are covered too.")

	if *check {
		if onPrimary == len(rows) && broken == 0 {
			fmt.Println("\nrotation complete for db_connections.")
			return
		}
		fmt.Println("\nrotation incomplete: run `rekey -apply`, then check again.")
		// Non-zero so a deploy pipeline can gate step 5 on it rather than on
		// somebody reading the output.
		os.Exit(1)
	}

	// -apply. A row that will not open is skipped loudly rather than failing
	// the run: the other tenants' rotations are not held hostage by one lost
	// key, and the row is named on every pass until somebody deals with it.
	var resealed, skipped int
	for _, c := range rows {
		if id, versioned := crypto.SealedUnder(c.DSNEncrypted); versioned && id == cipher.PrimaryKeyID() {
			continue
		}
		plain, err := cipher.Decrypt(c.DSNEncrypted)
		if err != nil {
			skipped++
			fmt.Printf("  SKIP  %s (company %s): %v\n", c.ID, c.CompanyID, err)
			continue
		}
		sealed, err := cipher.Encrypt(plain)
		if err != nil {
			fatalf("re-seal %s: %v", c.ID, err)
		}
		c.DSNEncrypted = sealed
		if err := conns.Update(ctx, c); err != nil {
			fatalf("write %s: %v", c.ID, err)
		}
		resealed++
	}
	fmt.Printf("\nre-sealed %d row(s); skipped %d unreadable.\n", resealed, skipped)
	if skipped > 0 {
		os.Exit(1)
	}
	fmt.Println("run `rekey -check` to confirm before removing ARGENTUM_DSN_KEYS_RETIRED.")
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "rekey: "+format+"\n", args...)
	os.Exit(2)
}
