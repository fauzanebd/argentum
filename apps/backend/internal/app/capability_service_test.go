package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// fakeCapabilityStore is user_capabilities plus the one join 083's repository
// relies on: a user belongs to exactly one company, and every method refuses a
// user id that is not a user of the company it was asked under. A fake without
// that would pass the cross-tenant test on a service that scoped nothing.
type fakeCapabilityStore struct {
	members map[string]string                 // user id → company id
	rows    map[string]domain.CapabilityGrant // "user/capability"
	lists   int
	grants  int
	err     error
	// during runs inside ListForUser after the rows are read and before they
	// are returned — the window a concurrent revoke lands in. It runs once.
	during func()
}

func newFakeCapabilityStore() *fakeCapabilityStore {
	return &fakeCapabilityStore{
		members: map[string]string{"u-1": "co-1", "u-2": "co-1", "u-9": "co-2"},
		rows:    map[string]domain.CapabilityGrant{},
	}
}

func (f *fakeCapabilityStore) ListForUser(_ context.Context, companyID, userID string) ([]domain.CapabilityGrant, error) {
	f.lists++
	if f.err != nil {
		return nil, f.err
	}
	if f.members[userID] != companyID {
		return nil, domain.ErrNotFound
	}
	out := []domain.CapabilityGrant{}
	for _, g := range f.rows {
		if g.UserID == userID {
			out = append(out, g)
		}
	}
	if during := f.during; during != nil {
		f.during = nil
		during()
	}
	return out, nil
}

func (f *fakeCapabilityStore) Grant(_ context.Context, companyID, userID string, c domain.Capability, grantedBy string) error {
	f.grants++
	if f.members[userID] != companyID {
		return domain.ErrNotFound
	}
	key := userID + "/" + string(c)
	if _, held := f.rows[key]; held {
		return nil
	}
	f.rows[key] = domain.CapabilityGrant{
		UserID:     userID,
		Capability: c,
		GrantedBy:  grantedBy,
		GrantedAt:  time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC),
	}
	return nil
}

func (f *fakeCapabilityStore) Revoke(_ context.Context, companyID, userID string, c domain.Capability) error {
	if f.members[userID] != companyID {
		return domain.ErrNotFound
	}
	delete(f.rows, userID+"/"+string(c))
	return nil
}

// newTestCapabilities is a service over a fake store, with a clock the test
// moves by writing through the returned pointer.
func newTestCapabilities() (*CapabilityService, *fakeCapabilityStore, *time.Time) {
	store := newFakeCapabilityStore()
	svc := NewCapabilityService(store)
	clock := time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return clock }
	return svc, store, &clock
}

func mustHave(t *testing.T, svc *CapabilityService, companyID, userID string, c domain.Capability) bool {
	t.Helper()
	held, err := svc.Has(context.Background(), companyID, userID, c)
	if err != nil {
		t.Fatalf("Has(%s, %s, %s): %v", companyID, userID, c, err)
	}
	return held
}

// The ticket's own acceptance: a revoke takes effect on the next request, with
// no re-login. Asserted with the clock standing still and the entry freshly
// cached, because that is the case a TTL alone would get wrong.
func TestCapabilityRevokeTakesEffectOnTheNextRequest(t *testing.T) {
	svc, store, _ := newTestCapabilities()
	ctx := context.Background()

	if err := svc.Grant(ctx, "co-1", "admin-1", "u-1", "voice"); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if !mustHave(t, svc, "co-1", "u-1", domain.CapabilityVoice) {
		t.Fatal("a granted capability is not held")
	}
	if !mustHave(t, svc, "co-1", "u-1", domain.CapabilityVoice) {
		t.Fatal("a granted capability stopped being held on the second read")
	}
	if store.lists != 1 {
		t.Fatalf("store read %d times for two checks inside the TTL, want 1 — the cache is not caching", store.lists)
	}

	if err := svc.Revoke(ctx, "co-1", "admin-1", "u-1", "voice"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if mustHave(t, svc, "co-1", "u-1", domain.CapabilityVoice) {
		t.Fatal("a revoked capability is still held on the next request")
	}
}

// What a replica that did not serve the write sees: the grant is deleted behind
// the service's back, and the cached answer stands until the TTL runs out and
// not a moment longer.
func TestCapabilityWriteElsewhereIsSeenWithinTheTTL(t *testing.T) {
	svc, store, clock := newTestCapabilities()
	if err := svc.Grant(context.Background(), "co-1", "admin-1", "u-1", "voice"); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if !mustHave(t, svc, "co-1", "u-1", domain.CapabilityVoice) {
		t.Fatal("a granted capability is not held")
	}

	delete(store.rows, "u-1/voice")
	*clock = clock.Add(capabilityCacheTTL - time.Second)
	if !mustHave(t, svc, "co-1", "u-1", domain.CapabilityVoice) {
		t.Fatal("the cache was bypassed inside its TTL")
	}
	*clock = clock.Add(2 * time.Second)
	if mustHave(t, svc, "co-1", "u-1", domain.CapabilityVoice) {
		t.Fatal("a capability deleted elsewhere is still held after the TTL")
	}
}

// The race the generation counter exists for. A load reads "held", a revoke
// lands before the load stores what it read, and the next request must not be
// answered from that stale load — otherwise the revoke waits out a whole TTL on
// the very replica that served it.
func TestCapabilityLoadThatRacedARevokeIsNotCached(t *testing.T) {
	svc, store, _ := newTestCapabilities()
	ctx := context.Background()
	if err := svc.Grant(ctx, "co-1", "admin-1", "u-1", "voice"); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	store.during = func() {
		if err := svc.Revoke(ctx, "co-1", "admin-1", "u-1", "voice"); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
	}
	// The in-flight request read the row before the revoke and may be answered
	// with it; that request was already past the gate.
	_ = mustHave(t, svc, "co-1", "u-1", domain.CapabilityVoice)

	if mustHave(t, svc, "co-1", "u-1", domain.CapabilityVoice) {
		t.Fatal("the request after a revoke was answered from a load that raced it")
	}
}

func TestCapabilityGrantTwiceIsIdempotent(t *testing.T) {
	svc, store, _ := newTestCapabilities()
	ctx := context.Background()
	if err := svc.Grant(ctx, "co-1", "admin-1", "u-1", "voice"); err != nil {
		t.Fatalf("first Grant: %v", err)
	}
	if err := svc.Grant(ctx, "co-1", "admin-2", "u-1", "voice"); err != nil {
		t.Fatalf("second Grant is an error, want idempotent success: %v", err)
	}
	if len(store.rows) != 1 {
		t.Fatalf("%d rows after granting one capability twice, want 1", len(store.rows))
	}
	if got := store.rows["u-1/voice"].GrantedBy; got != "admin-1" {
		t.Errorf("granted_by = %q after a repeat grant, want the first granter admin-1", got)
	}
}

func TestCapabilityRevokeOfWhatIsNotHeldSucceeds(t *testing.T) {
	svc, _, _ := newTestCapabilities()
	if err := svc.Revoke(context.Background(), "co-1", "admin-1", "u-1", "export_data"); err != nil {
		t.Fatalf("revoking an ungranted capability: %v, want success", err)
	}
}

// Refused at the boundary and never stored: the store is not even called.
func TestCapabilityUnknownIsRefusedBeforeTheStore(t *testing.T) {
	svc, store, _ := newTestCapabilities()
	for _, raw := range []string{"telepathy", "", "voice:read", "admin"} {
		err := svc.Grant(context.Background(), "co-1", "admin-1", "u-1", raw)
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("Grant(%q) = %v, want ErrInvalidInput", raw, err)
		}
	}
	if store.grants != 0 {
		t.Errorf("the store was asked to grant %d times for unknown capabilities, want 0", store.grants)
	}
}

// The normaliser: a URL segment typed with a capital or a stray space is the
// capability it obviously names, and is stored as the canonical form.
func TestCapabilityNameIsNormalised(t *testing.T) {
	svc, store, _ := newTestCapabilities()
	if err := svc.Grant(context.Background(), "co-1", "admin-1", "u-1", " Voice "); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if _, ok := store.rows["u-1/voice"]; !ok {
		t.Fatalf("rows = %v, want the canonical u-1/voice", store.rows)
	}
}

// The tenant-isolation arm. u-9 belongs to co-2; an admin of co-1 holding its id
// can neither grant it anything nor learn anything about it.
func TestCapabilityCrossTenantUserIsNotFound(t *testing.T) {
	svc, store, _ := newTestCapabilities()
	ctx := context.Background()

	if err := svc.Grant(ctx, "co-1", "admin-1", "u-9", "voice"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-tenant Grant = %v, want ErrNotFound", err)
	}
	if len(store.rows) != 0 {
		t.Fatalf("a cross-tenant grant wrote %d rows", len(store.rows))
	}
	if _, err := svc.ListForUser(ctx, "co-1", "u-9"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-tenant ListForUser = %v, want ErrNotFound", err)
	}
	if mustHave(t, svc, "co-1", "u-9", domain.CapabilityVoice) {
		t.Fatal("a user of another company holds a capability here")
	}
}

// The cache key carries the company. A grant held in co-2 must not be readable
// as held by asking under co-1, even once it is cached.
func TestCapabilityCacheDoesNotCrossCompanies(t *testing.T) {
	svc, _, _ := newTestCapabilities()
	if err := svc.Grant(context.Background(), "co-2", "admin-9", "u-9", "voice"); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if !mustHave(t, svc, "co-2", "u-9", domain.CapabilityVoice) {
		t.Fatal("a granted capability is not held in its own company")
	}
	if mustHave(t, svc, "co-1", "u-9", domain.CapabilityVoice) {
		t.Fatal("a capability cached for co-2 answered a question asked under co-1")
	}
}

// A store that fails is not a store that said yes — and not one that said no
// either, which is why it is an error rather than false alone: the middleware
// answers 503, so the caller retries rather than being told they lack a grant.
func TestCapabilityStoreErrorIsAnErrorNotAnAnswer(t *testing.T) {
	svc, store, _ := newTestCapabilities()
	store.err = errors.New("connection refused")
	held, err := svc.Has(context.Background(), "co-1", "u-1", domain.CapabilityVoice)
	if err == nil {
		t.Fatal("a failing store produced no error")
	}
	if held {
		t.Fatal("a failing store produced a grant")
	}
}

func TestCapabilityNilServiceRefuses(t *testing.T) {
	var svc *CapabilityService
	held, err := svc.Has(context.Background(), "co-1", "u-1", domain.CapabilityVoice)
	if err == nil || held {
		t.Fatalf("nil service Has = (%v, %v), want (false, error)", held, err)
	}
}
