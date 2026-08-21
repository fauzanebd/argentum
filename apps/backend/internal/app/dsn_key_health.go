package app

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/crypto"
	"github.com/fauzanebd/argentum/internal/domain"
)

// Whether the key in this process opens the rows in that database.
//
// **Why this exists.** `db_connections.dsn_encrypted` holds one AES-GCM
// ciphertext per registered warehouse, all sealed under `ARGENTUM_DSN_KEY`.
// Nothing in the product notices when that key stops matching the rows. The
// row is read at query time, inside a turn, so the discovery path is an agent
// answering *"there appears to be a decryption problem with the database
// connection string"* — in front of whoever asked the question, on a Tuesday,
// with no other signal anywhere.
//
// That is not hypothetical here. Three distinct DSN keys existed on this
// project between 31 July and 14 August 2026; two of the twenty stored
// connections open under none of them, and the loss was found by a person
// running a gate by hand rather than by any part of the system
// (`docs/coverage/live-gate-backlog.md` §1b). The data at risk was throwaway;
// the mechanism is the one that takes a customer's warehouse with it.
//
// The check is a decrypt attempt per row and nothing else — no dial, no
// network, no plaintext kept. Twenty rows at boot is microseconds, and the
// answer is the sentence an operator needs: *N of M stored connections do not
// decrypt under the current key.*
//
// **What it does not cover, and deliberately.** The same key seals tenant LLM
// credentials, the channel credential tables, MCP server records and HTTP
// endpoint secrets. Those fail the same way and are worth the same count; this
// is the one that breaks a turn in front of a user, so it is the one that is
// built. Extending the sweep is a matter of handing DSNKeyCoverage another
// lister — see T-H14, where key rotation lives.

// DSNKeyHealth is the answer: how many stored connections there are, and how
// many of them the current key cannot open.
type DSNKeyHealth struct {
	Total int `json:"total"`
	// Undecryptable is len(Unreadable), carried explicitly so a caller that
	// only logs the numbers does not have to reach into the slice.
	Undecryptable int `json:"undecryptable"`
	// Unreadable identifies the rows, and carries no ciphertext: a row that
	// cannot be decrypted is still a secret, and the useful facts about it are
	// which tenant owns it and what it was called.
	Unreadable []UnreadableDSN `json:"unreadable"`
	// Legacy counts rows sealed in the prefix-free format that predates T-H14.
	// They open fine — the reader accepts both — and they are the rows a
	// rotation still has to re-seal, because a row that names no key cannot be
	// shown to be on the current one.
	Legacy int `json:"legacy"`
	// ByKey counts rows per key fingerprint, for the versioned rows only.
	//
	// This is the field that makes a rotation finishable. "Re-seal every row"
	// is only actionable if "which rows are still on the old key" has an
	// answer, and before the version prefix existed it did not: a row this
	// process can open is indistinguishable from a row sealed under the
	// primary. An operator finishes a rotation when this map holds one entry,
	// it is the primary, and Legacy is zero.
	ByKey     map[string]int `json:"by_key,omitempty"`
	CheckedAt time.Time      `json:"checked_at"`
}

// UnreadableDSN is one row the current key does not open.
type UnreadableDSN struct {
	ConnectionID string    `json:"connection_id"`
	CompanyID    string    `json:"company_id"`
	Label        string    `json:"label,omitempty"`
	DBType       string    `json:"db_type"`
	CreatedAt    time.Time `json:"created_at"`
}

// DSNDecryptor is the one method this check needs of the cipher.
// *crypto.DSNCipher satisfies it.
type DSNDecryptor interface {
	Decrypt(blob []byte) (string, error)
}

// AllConnectionsLister reads every stored connection across every tenant. It is
// the startup sweep's half of the repository; *postgres.ConnectionRepo
// satisfies it.
type AllConnectionsLister interface {
	ListAll(ctx context.Context) ([]*domain.DBConnection, error)
}

// EvaluateDSNKey is the pure half: given rows and a cipher, which do not open.
// Split out so the rule is testable without a database, and so the HTTP path
// and the startup sweep cannot disagree about what "unreadable" means.
//
// An empty ciphertext counts as unreadable rather than as absent. A connection
// row with no DSN is as unusable as one sealed under a lost key, and quietly
// excluding it would make the count read healthier than the deployment is.
func EvaluateDSNKey(conns []*domain.DBConnection, cipher DSNDecryptor, now time.Time) DSNKeyHealth {
	h := DSNKeyHealth{Total: len(conns), CheckedAt: now, ByKey: map[string]int{}}
	if cipher == nil {
		return h
	}
	for _, c := range conns {
		if c == nil {
			continue
		}
		// Which key seals it, whether or not it opens: a row under a key
		// nobody holds is exactly the row an operator most needs named, and
		// counting it only when it decrypts would leave it out.
		if id, versioned := crypto.SealedUnder(c.DSNEncrypted); versioned {
			h.ByKey[id]++
		} else {
			h.Legacy++
		}
		if _, err := cipher.Decrypt(c.DSNEncrypted); err != nil {
			h.Unreadable = append(h.Unreadable, UnreadableDSN{
				ConnectionID: c.ID,
				CompanyID:    c.CompanyID,
				Label:        c.Label,
				DBType:       c.DBType,
				CreatedAt:    c.CreatedAt,
			})
		}
	}
	h.Undecryptable = len(h.Unreadable)
	return h
}

// LogDSNKeyCoverage runs the sweep at startup and says what it found.
//
// It never stops the process. A deployment whose key has moved on still serves
// every tenant whose rows were re-sealed, and refusing to boot would take those
// down to protect the ones already broken — the same argument that turned
// T-H3's `CORS_ORIGINS` refusal into a warning. What it does do is make the
// state visible before a customer finds it, at Warn, with the ids.
//
// Errors reading the table are logged and swallowed for the same reason: this
// is an observation, and an observation that can stop a boot is a new way to
// fail.
func LogDSNKeyCoverage(ctx context.Context, lister AllConnectionsLister, cipher DSNDecryptor) DSNKeyHealth {
	if lister == nil || cipher == nil {
		return DSNKeyHealth{}
	}
	conns, err := lister.ListAll(ctx)
	if err != nil {
		logrus.WithError(err).Warn("could not check whether stored connections decrypt under the current ARGENTUM_DSN_KEY")
		return DSNKeyHealth{}
	}
	h := EvaluateDSNKey(conns, cipher, time.Now().UTC())
	logRotationProgress(h, cipher)
	if h.Undecryptable == 0 {
		logrus.WithField("connections", h.Total).
			Info("all stored connections decrypt under the current ARGENTUM_DSN_KEY")
		return h
	}
	ids := make([]string, 0, len(h.Unreadable))
	companies := map[string]struct{}{}
	for _, u := range h.Unreadable {
		ids = append(ids, u.ConnectionID)
		companies[u.CompanyID] = struct{}{}
	}
	logrus.WithFields(logrus.Fields{
		"undecryptable":   h.Undecryptable,
		"total":           h.Total,
		"companies":       len(companies),
		"connection_ids":  ids,
		"what_this_means": "these tenants' agent turns will fail at query time; the rows were sealed under a different ARGENTUM_DSN_KEY",
	}).Warn("stored connections do not decrypt under the current ARGENTUM_DSN_KEY")
	return h
}

// logRotationProgress says how far along a rotation is, at boot (T-H14).
//
// Only when there is something to say: a deployment that has never rotated has
// every row legacy and one key, and a line saying so on every boot is noise
// that trains an operator to skip the ones that matter. It speaks when rows are
// split across keys, or when a retired key is configured — the two states where
// somebody is mid-rotation and needs to know whether they can finish.
func logRotationProgress(h DSNKeyHealth, cipher DSNDecryptor) {
	ring, ok := cipher.(interface {
		PrimaryKeyID() string
		RetiredKeyIDs() []string
	})
	if !ok {
		return
	}
	retired := ring.RetiredKeyIDs()
	distinct := len(h.ByKey)
	if h.Legacy > 0 {
		distinct++
	}
	if len(retired) == 0 && distinct <= 1 {
		return
	}
	onPrimary := h.ByKey[ring.PrimaryKeyID()]
	fields := logrus.Fields{
		"primary_key":     ring.PrimaryKeyID(),
		"retired_keys":    retired,
		"on_primary":      onPrimary,
		"legacy_format":   h.Legacy,
		"by_key":          h.ByKey,
		"total":           h.Total,
		"what_this_means": "run `cmd/rekey` to re-seal the remaining rows; the retired key can be dropped when every row is on the primary",
	}
	if onPrimary == h.Total {
		logrus.WithFields(fields).Info("key rotation complete: every stored connection is sealed under the primary key")
		return
	}
	logrus.WithFields(fields).Warn("key rotation in progress: some stored connections are not sealed under the primary key")
}

// DSNKeyHealth answers the same question for one tenant, which is what an admin
// can act on: re-register the connection, or find the key that opens it.
func (s *CompanyService) DSNKeyHealth(ctx context.Context, companyID string) (DSNKeyHealth, error) {
	conns, err := s.connections.ListByCompany(ctx, companyID)
	if err != nil {
		return DSNKeyHealth{}, err
	}
	return EvaluateDSNKey(conns, s.dsnCipher, time.Now().UTC()), nil
}
