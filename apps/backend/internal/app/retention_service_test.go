package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// A fake repository rather than a live Postgres, because what these tests are
// about is the *decision* layer: which tenants are purged, what cutoff each one
// gets, what is recorded when a delete fails. The SQL itself — that `messages`
// is scoped through `conversation_threads`, that CASCADE spares `agent_actions`
// — is a property of the database and belongs in the live gate, where it can be
// proven against a real one instead of asserted against a mock that agrees.

type fakeRetentionRepo struct {
	targets []domain.CompanyRetention
	listErr error

	// nothingExpired makes HasExpired answer false, which is the ordinary
	// state of a tenant on any night after the first: a window is set, and
	// nothing has aged past it since the last tick.
	nothingExpired bool
	expiredErr     error
	expiredCall    []purgeCall

	purgeErr  error
	eraseErr  error
	purgeCall []purgeCall
	erased    []string

	exported []domain.ExportedMessage
}

type purgeCall struct {
	companyID string
	before    time.Time
}

func (f *fakeRetentionRepo) PurgeCompanyMessages(_ context.Context, companyID string, before time.Time) (int, int, error) {
	f.purgeCall = append(f.purgeCall, purgeCall{companyID, before})
	if f.purgeErr != nil {
		return 0, 0, f.purgeErr
	}
	return 1, 4, nil
}

func (f *fakeRetentionRepo) HasExpired(_ context.Context, companyID string, before time.Time) (bool, error) {
	f.expiredCall = append(f.expiredCall, purgeCall{companyID, before})
	if f.expiredErr != nil {
		return false, f.expiredErr
	}
	return !f.nothingExpired, nil
}

func (f *fakeRetentionRepo) EraseCompanyConversations(_ context.Context, companyID string) (int, int, error) {
	f.erased = append(f.erased, companyID)
	if f.eraseErr != nil {
		return 0, 0, f.eraseErr
	}
	return 3, 12, nil
}

func (f *fakeRetentionRepo) CompaniesWithRetention(context.Context) ([]domain.CompanyRetention, error) {
	return f.targets, f.listErr
}

func (f *fakeRetentionRepo) ExportCompanyConversations(_ context.Context, _ string, fn func(domain.ExportedMessage) error) error {
	for _, m := range f.exported {
		if err := fn(m); err != nil {
			return err
		}
	}
	return nil
}

type fakeErasureRecords struct {
	begun     []*domain.DataErasure
	completed map[string][2]int
	failed    map[string]string
	beginErr  error
	nextID    int
}

func newFakeRecords() *fakeErasureRecords {
	return &fakeErasureRecords{completed: map[string][2]int{}, failed: map[string]string{}}
}

func (f *fakeErasureRecords) Begin(_ context.Context, e *domain.DataErasure) error {
	if f.beginErr != nil {
		return f.beginErr
	}
	f.nextID++
	e.ID = string(rune('a' + f.nextID - 1))
	e.RequestedAt = time.Unix(0, 0)
	f.begun = append(f.begun, e)
	return nil
}

func (f *fakeErasureRecords) Complete(_ context.Context, id string, threads, messages int) error {
	f.completed[id] = [2]int{threads, messages}
	return nil
}

func (f *fakeErasureRecords) Fail(_ context.Context, id, reason string) error {
	f.failed[id] = reason
	return nil
}

func (f *fakeErasureRecords) ListByCompany(context.Context, string, int) ([]*domain.DataErasure, error) {
	return nil, nil
}

type fakeRetentionCompanies struct {
	company *domain.Company
	updated *domain.Company
	getErr  error
}

func (f *fakeRetentionCompanies) Create(context.Context, *domain.Company) error { return nil }
func (f *fakeRetentionCompanies) GetByID(context.Context, string) (*domain.Company, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	c := *f.company
	return &c, nil
}
func (f *fakeRetentionCompanies) GetBySlug(context.Context, string) (*domain.Company, error) {
	return f.company, nil
}
func (f *fakeRetentionCompanies) Update(_ context.Context, c *domain.Company) error {
	f.updated = c
	return nil
}

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// The cutoff is the whole retention promise expressed as arithmetic, and
// getting it wrong by a sign or a unit deletes a tenant's history. Asserted per
// tenant, because each carries its own window.
func TestPurgeExpiredUsesEachTenantsOwnWindow(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	repo := &fakeRetentionRepo{targets: []domain.CompanyRetention{
		{CompanyID: "co-30", Days: 30},
		{CompanyID: "co-365", Days: 365},
	}}
	records := newFakeRecords()
	svc := NewRetentionService(repo, records, &fakeRetentionCompanies{}).WithClock(fixedClock(now))

	res, err := svc.PurgeExpired(context.Background())
	if err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	if res.Companies != 2 || res.Threads != 2 || res.Messages != 8 {
		t.Errorf("result = %+v, want 2 companies / 2 threads / 8 messages", res)
	}
	if len(repo.purgeCall) != 2 {
		t.Fatalf("purged %d tenants, want 2", len(repo.purgeCall))
	}
	want := map[string]time.Time{
		"co-30":  now.AddDate(0, 0, -30),
		"co-365": now.AddDate(0, 0, -365),
	}
	for _, call := range repo.purgeCall {
		if !call.before.Equal(want[call.companyID]) {
			t.Errorf("%s cutoff = %s, want %s", call.companyID, call.before, want[call.companyID])
		}
	}
}

// A nonsense window must not be read as "delete everything". This is the one
// failure in the file where guessing has no safe direction, so the tenant is
// skipped and said so.
func TestPurgeExpiredSkipsAnOutOfRangeWindow(t *testing.T) {
	repo := &fakeRetentionRepo{targets: []domain.CompanyRetention{
		{CompanyID: "co-bad", Days: -5},
		{CompanyID: "co-huge", Days: domain.MaxMessageRetentionDays + 1},
		{CompanyID: "co-ok", Days: 30},
	}}
	svc := NewRetentionService(repo, newFakeRecords(), &fakeRetentionCompanies{})

	res, err := svc.PurgeExpired(context.Background())
	if err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	if res.Companies != 1 {
		t.Errorf("purged %d tenants, want only the valid one", res.Companies)
	}
	if len(repo.purgeCall) != 1 || repo.purgeCall[0].companyID != "co-ok" {
		t.Errorf("purge calls = %+v, want only co-ok", repo.purgeCall)
	}
}

// One tenant's failure is a reason to try again tonight, not a reason for every
// other tenant's retention promise to go unkept.
func TestPurgeExpiredIsolatesOneTenantsFailure(t *testing.T) {
	repo := &fakeRetentionRepo{
		targets:  []domain.CompanyRetention{{CompanyID: "co-1", Days: 30}},
		purgeErr: errors.New("deadlock detected"),
	}
	records := newFakeRecords()
	svc := NewRetentionService(repo, records, &fakeRetentionCompanies{})

	res, err := svc.PurgeExpired(context.Background())
	if err != nil {
		t.Fatalf("PurgeExpired returned an error for a per-tenant failure: %v", err)
	}
	if res.Companies != 0 {
		t.Errorf("counted %d successful tenants, want 0", res.Companies)
	}
	// The failure is recorded against the tenant, so a window that has silently
	// stopped being enforced is visible in their own history.
	if len(records.failed) != 1 {
		t.Errorf("recorded %d failures, want 1 — a purge that fails silently is a promise nobody knows is broken", len(records.failed))
	}
}

// The record is the evidence. Deleting without one would leave a tenant with
// fewer messages than yesterday and nothing to explain why.
func TestPurgeDoesNotDeleteWhenTheRecordCannotBeOpened(t *testing.T) {
	repo := &fakeRetentionRepo{targets: []domain.CompanyRetention{{CompanyID: "co-1", Days: 30}}}
	records := newFakeRecords()
	records.beginErr = errors.New("control database unavailable")
	svc := NewRetentionService(repo, records, &fakeRetentionCompanies{})

	if _, err := svc.PurgeExpired(context.Background()); err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	if len(repo.purgeCall) != 0 {
		t.Error("purged rows with no record open; the delete must not outrun its evidence")
	}
}

func TestEraseCompanyDataWritesTheCompletionRecord(t *testing.T) {
	repo := &fakeRetentionRepo{}
	records := newFakeRecords()
	svc := NewRetentionService(repo, records, &fakeRetentionCompanies{})

	rec, err := svc.EraseCompanyData(context.Background(), "co-1", "user-9")
	if err != nil {
		t.Fatalf("EraseCompanyData: %v", err)
	}
	if rec.Status != domain.ErasureStatusCompleted {
		t.Errorf("status = %q, want %q", rec.Status, domain.ErasureStatusCompleted)
	}
	if rec.ThreadsDeleted != 3 || rec.MessagesDeleted != 12 {
		t.Errorf("counts = %d threads / %d messages, want 3 / 12", rec.ThreadsDeleted, rec.MessagesDeleted)
	}
	if rec.CompletedAt == nil {
		t.Error("completed_at is nil on a completed erasure")
	}
	// Who authorised it is the question a regulator asks, and it has to be in
	// the row rather than only in a log line.
	if len(records.begun) != 1 || records.begun[0].RequestedBy != "user-9" {
		t.Errorf("record does not name the requester: %+v", records.begun)
	}
	if records.begun[0].Scope != domain.ErasureScopeAll {
		t.Errorf("scope = %q, want %q", records.begun[0].Scope, domain.ErasureScopeAll)
	}
}

func TestEraseCompanyDataRecordsAFailure(t *testing.T) {
	repo := &fakeRetentionRepo{eraseErr: errors.New("statement timeout")}
	records := newFakeRecords()
	svc := NewRetentionService(repo, records, &fakeRetentionCompanies{})

	if _, err := svc.EraseCompanyData(context.Background(), "co-1", "user-9"); err == nil {
		t.Fatal("EraseCompanyData returned nil on a failed delete")
	}
	if len(records.failed) != 1 {
		t.Errorf("recorded %d failures, want 1", len(records.failed))
	}
}

func TestSetRetentionRejectsAnUnsettableWindow(t *testing.T) {
	companies := &fakeRetentionCompanies{company: &domain.Company{ID: "co-1", MessageRetentionDays: 0}}
	svc := NewRetentionService(&fakeRetentionRepo{}, newFakeRecords(), companies)

	for _, days := range []int{-1, domain.MaxMessageRetentionDays + 1} {
		err := svc.SetRetention(context.Background(), "co-1", days)
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("SetRetention(%d) = %v, want ErrInvalidInput", days, err)
		}
		if companies.updated != nil {
			t.Fatalf("SetRetention(%d) wrote the company anyway", days)
		}
	}
}

// Zero is settable and means forever, which is the value most likely to be
// mistaken for "unset" by a caller and for "delete now" by a reader.
func TestSetRetentionAcceptsForever(t *testing.T) {
	companies := &fakeRetentionCompanies{company: &domain.Company{ID: "co-1", MessageRetentionDays: 90}}
	svc := NewRetentionService(&fakeRetentionRepo{}, newFakeRecords(), companies)

	if err := svc.SetRetention(context.Background(), "co-1", domain.RetentionForever); err != nil {
		t.Fatalf("SetRetention(forever): %v", err)
	}
	if companies.updated == nil || companies.updated.MessageRetentionDays != domain.RetentionForever {
		t.Errorf("company was not updated to forever: %+v", companies.updated)
	}
}

func TestExportStreamsEveryMessage(t *testing.T) {
	repo := &fakeRetentionRepo{exported: []domain.ExportedMessage{
		{MessageID: "m1", Role: "user", Content: "hello"},
		{MessageID: "m2", Role: "assistant", Content: "hi"},
	}}
	svc := NewRetentionService(repo, newFakeRecords(), &fakeRetentionCompanies{})

	var got []string
	err := svc.ExportCompanyData(context.Background(), "co-1", func(m domain.ExportedMessage) error {
		got = append(got, m.MessageID)
		return nil
	})
	if err != nil {
		t.Fatalf("ExportCompanyData: %v", err)
	}
	if len(got) != 2 || got[0] != "m1" || got[1] != "m2" {
		t.Errorf("exported %v, want [m1 m2]", got)
	}
}

// A tick that deleted nothing must write no record — the property purgeOne's
// own comment claims and, until 2026-08-22, did not have. Found by the §1q
// live gate: two nightly ticks against a tenant with nothing left to purge
// left two `completed` rows reading 0 threads / 0 messages, and the tenant's
// real erasure was already outnumbered by them in `GET /company/data/erasures`.
//
// The cost of the defect is not storage. `data_erasures` is the evidence table
// a tenant hands a regulator, and one 0/0 row per tenant per night buries the
// four rows somebody is looking for.
func TestPurgeWritesNoRecordWhenNothingIsExpired(t *testing.T) {
	repo := &fakeRetentionRepo{
		targets:        []domain.CompanyRetention{{CompanyID: "co-1", Days: 30}},
		nothingExpired: true,
	}
	records := newFakeRecords()
	svc := NewRetentionService(repo, records, &fakeRetentionCompanies{})

	res, err := svc.PurgeExpired(context.Background())
	if err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	if len(records.begun) != 0 {
		t.Errorf("opened %d record(s) for a tick with nothing to delete; the evidence table must not grow by a row per tenant per night", len(records.begun))
	}
	if len(repo.purgeCall) != 0 {
		t.Errorf("ran %d delete(s) against a tenant with nothing expired", len(repo.purgeCall))
	}
	if res.Companies != 0 || res.Threads != 0 || res.Messages != 0 {
		t.Errorf("result = %+v, want an empty tick", res)
	}
	// The check itself must still have used the tenant's own cutoff, or a
	// skipped tick is only skipped by luck.
	if len(repo.expiredCall) != 1 {
		t.Fatalf("checked %d tenants, want 1", len(repo.expiredCall))
	}
}

// The check failing is not "nothing expired". A tenant whose EXISTS probe
// errors must be skipped and logged, not read as an empty night.
func TestPurgeSkipsTheTenantWhenTheExpiryCheckFails(t *testing.T) {
	repo := &fakeRetentionRepo{
		targets:    []domain.CompanyRetention{{CompanyID: "co-1", Days: 30}},
		expiredErr: errors.New("control database unavailable"),
	}
	records := newFakeRecords()
	svc := NewRetentionService(repo, records, &fakeRetentionCompanies{})

	if _, err := svc.PurgeExpired(context.Background()); err != nil {
		t.Fatalf("PurgeExpired returned an error for a per-tenant failure: %v", err)
	}
	if len(repo.purgeCall) != 0 {
		t.Error("deleted rows after the expiry check failed")
	}
	if len(records.begun) != 0 {
		t.Error("opened a record after the expiry check failed")
	}
}
