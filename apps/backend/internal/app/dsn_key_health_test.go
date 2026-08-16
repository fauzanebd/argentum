package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/crypto"
	"github.com/fauzanebd/argentum/internal/domain"
)

// A key that opens nothing is what a rotated or lost ARGENTUM_DSN_KEY looks
// like from inside the process, so the check is built against two real ciphers
// rather than a stub: sealing under one key and reading under another is the
// exact failure, and a fake Decrypt would prove only that the loop works.
func ciphers(t *testing.T) (current, retired *crypto.DSNCipher) {
	t.Helper()
	a, err := crypto.NewFromHex("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("current cipher: %v", err)
	}
	b, err := crypto.NewFromHex("fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210")
	if err != nil {
		t.Fatalf("retired cipher: %v", err)
	}
	return a, b
}

func sealed(t *testing.T, c *crypto.DSNCipher, id, company, dsn string) *domain.DBConnection {
	t.Helper()
	blob, err := c.Encrypt(dsn)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	return &domain.DBConnection{
		ID: id, CompanyID: company, Label: "warehouse " + id,
		DBType: "postgres", DSNEncrypted: blob,
	}
}

func TestEvaluateDSNKeyCountsWhatTheCurrentKeyCannotOpen(t *testing.T) {
	current, retired := ciphers(t)
	now := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)

	conns := []*domain.DBConnection{
		sealed(t, current, "c-1", "co-1", "postgres://a"),
		sealed(t, retired, "c-2", "co-1", "postgres://b"), // sealed under a key that is gone
		sealed(t, current, "c-3", "co-2", "postgres://c"),
		sealed(t, retired, "c-4", "co-2", "postgres://d"),
	}

	h := EvaluateDSNKey(conns, current, now)

	if h.Total != 4 {
		t.Errorf("Total = %d, want 4", h.Total)
	}
	if h.Undecryptable != 2 {
		t.Fatalf("Undecryptable = %d, want 2", h.Undecryptable)
	}
	if h.Undecryptable != len(h.Unreadable) {
		t.Errorf("Undecryptable = %d but Unreadable has %d entries", h.Undecryptable, len(h.Unreadable))
	}
	got := map[string]string{}
	for _, u := range h.Unreadable {
		got[u.ConnectionID] = u.CompanyID
	}
	if got["c-2"] != "co-1" || got["c-4"] != "co-2" {
		t.Errorf("unreadable rows = %v, want c-2 (co-1) and c-4 (co-2)", got)
	}
	if !h.CheckedAt.Equal(now) {
		t.Errorf("CheckedAt = %v, want %v", h.CheckedAt, now)
	}
}

func TestEvaluateDSNKeyIsSilentWhenEveryRowOpens(t *testing.T) {
	current, _ := ciphers(t)
	conns := []*domain.DBConnection{
		sealed(t, current, "c-1", "co-1", "postgres://a"),
		sealed(t, current, "c-2", "co-1", "postgres://b"),
	}

	h := EvaluateDSNKey(conns, current, time.Now())

	if h.Undecryptable != 0 || len(h.Unreadable) != 0 {
		t.Errorf("healthy deployment reported %d unreadable rows: %v", h.Undecryptable, h.Unreadable)
	}
	if h.Total != 2 {
		t.Errorf("Total = %d, want 2", h.Total)
	}
}

// An empty ciphertext is as unusable as one under a lost key, and counting it
// as fine would make the report read healthier than the deployment is.
func TestEvaluateDSNKeyCountsAnEmptyCiphertext(t *testing.T) {
	current, _ := ciphers(t)
	conns := []*domain.DBConnection{
		sealed(t, current, "c-1", "co-1", "postgres://a"),
		{ID: "c-2", CompanyID: "co-1", DBType: "mysql"}, // no DSN at all
	}

	h := EvaluateDSNKey(conns, current, time.Now())

	if h.Undecryptable != 1 || h.Unreadable[0].ConnectionID != "c-2" {
		t.Errorf("Undecryptable = %d (%v), want the row with no ciphertext", h.Undecryptable, h.Unreadable)
	}
}

// The report identifies rows; it must never carry what is in them.
func TestEvaluateDSNKeyReportsNoSecrets(t *testing.T) {
	current, retired := ciphers(t)
	const secret = "postgres://user:hunter2@warehouse.internal:5432/sales"
	conns := []*domain.DBConnection{sealed(t, retired, "c-1", "co-1", secret)}

	h := EvaluateDSNKey(conns, current, time.Now())

	if len(h.Unreadable) != 1 {
		t.Fatalf("want the row reported, got %v", h.Unreadable)
	}
	u := h.Unreadable[0]
	// The struct has no field that could carry it — this asserts that stays
	// true if somebody adds one.
	for _, field := range []string{u.ConnectionID, u.CompanyID, u.Label, u.DBType} {
		if field == secret {
			t.Errorf("the DSN reached the report: %q", field)
		}
	}
}

// A cipher-less build (there is one: the name-only registry path) reports the
// total and accuses nothing.
func TestEvaluateDSNKeyWithNoCipherAccusesNobody(t *testing.T) {
	_, retired := ciphers(t)
	conns := []*domain.DBConnection{sealed(t, retired, "c-1", "co-1", "postgres://a")}

	h := EvaluateDSNKey(conns, nil, time.Now())

	if h.Total != 1 || h.Undecryptable != 0 {
		t.Errorf("Total = %d, Undecryptable = %d, want 1 and 0", h.Total, h.Undecryptable)
	}
}

// A read that fails is logged and swallowed: this is an observation, and an
// observation that can stop a boot is a new way to fail.
type failingLister struct{}

func (failingLister) ListAll(context.Context) ([]*domain.DBConnection, error) {
	return nil, errors.New("control database is down")
}

type staticLister struct{ conns []*domain.DBConnection }

func (l staticLister) ListAll(context.Context) ([]*domain.DBConnection, error) {
	return l.conns, nil
}

func TestLogDSNKeyCoverageSurvivesAFailedRead(t *testing.T) {
	current, _ := ciphers(t)

	h := LogDSNKeyCoverage(context.Background(), failingLister{}, current)

	if h.Total != 0 || h.Undecryptable != 0 {
		t.Errorf("a failed read reported %+v, want the zero value", h)
	}
}

func TestLogDSNKeyCoverageReportsWhatTheSweepFound(t *testing.T) {
	current, retired := ciphers(t)
	lister := staticLister{conns: []*domain.DBConnection{
		sealed(t, current, "c-1", "co-1", "postgres://a"),
		sealed(t, retired, "c-2", "co-2", "postgres://b"),
	}}

	h := LogDSNKeyCoverage(context.Background(), lister, current)

	if h.Total != 2 || h.Undecryptable != 1 || h.Unreadable[0].ConnectionID != "c-2" {
		t.Errorf("sweep reported %+v, want 1 of 2 unreadable (c-2)", h)
	}
}

// Both dependencies are optional at the call sites, so neither may panic.
func TestLogDSNKeyCoverageWithNothingWiredDoesNothing(t *testing.T) {
	current, _ := ciphers(t)
	if h := LogDSNKeyCoverage(context.Background(), nil, current); h.Total != 0 {
		t.Errorf("nil lister reported %+v", h)
	}
	if h := LogDSNKeyCoverage(context.Background(), staticLister{}, nil); h.Total != 0 {
		t.Errorf("nil cipher reported %+v", h)
	}
}
