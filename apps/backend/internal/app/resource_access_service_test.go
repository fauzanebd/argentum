package app

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/domain"
)

// fakeResourceGrants is 084 in memory, with the two scoping rules the SQL
// enforces: a user belongs to one company and a resource belongs to one
// company, and nothing crosses. It also satisfies authz.Loader, so a test can
// change access through the service and read the consequence through the
// decision function — the path the product will actually take.
type fakeResourceGrants struct {
	users     map[string]string                                    // user → company
	resources map[domain.ResourceKind]map[string]string            // kind → id → company
	modes     map[domain.ResourceKind]map[string]domain.AccessMode // kind → id → mode
	grants    map[string]domain.ResourceGrant                      // "user/kind/id"
	calls     map[string]int
}

func newFakeResourceGrants() *fakeResourceGrants {
	f := &fakeResourceGrants{
		users:     map[string]string{"admin-1": "co-1", "member-1": "co-1", "stranger": "co-2"},
		resources: map[domain.ResourceKind]map[string]string{},
		modes:     map[domain.ResourceKind]map[string]domain.AccessMode{},
		grants:    map[string]domain.ResourceGrant{},
		calls:     map[string]int{},
	}
	for _, kind := range domain.AllResourceKinds {
		f.resources[kind] = map[string]string{"ours": "co-1", "theirs": "co-2"}
		f.modes[kind] = map[string]domain.AccessMode{"ours": domain.AccessModeOpen, "theirs": domain.AccessModeOpen}
	}
	return f
}

func (f *fakeResourceGrants) here(companyID string, kind domain.ResourceKind, id string) bool {
	return f.resources[kind][id] == companyID
}

func (f *fakeResourceGrants) LoadAccess(_ context.Context, companyID, userID string, kind domain.ResourceKind, ids []string) (map[string]domain.ResourceAccess, error) {
	f.calls["LoadAccess"]++
	out := map[string]domain.ResourceAccess{}
	for _, id := range ids {
		if !f.here(companyID, kind, id) {
			continue
		}
		_, granted := f.grants[userID+"/"+string(kind)+"/"+id]
		out[id] = domain.ResourceAccess{Mode: f.modes[kind][id], Granted: granted}
	}
	return out, nil
}

func (f *fakeResourceGrants) View(_ context.Context, companyID string, kind domain.ResourceKind, id string) (*domain.ResourceAccessView, error) {
	if !f.here(companyID, kind, id) {
		return nil, domain.ErrNotFound
	}
	v := &domain.ResourceAccessView{Kind: kind, ResourceID: id, AccessMode: f.modes[kind][id], Grants: []domain.ResourceGrant{}}
	for _, g := range f.grants {
		if g.Kind == kind && g.ResourceID == id {
			v.Grants = append(v.Grants, g)
		}
	}
	sort.Slice(v.Grants, func(i, j int) bool { return v.Grants[i].UserID < v.Grants[j].UserID })
	return v, nil
}

func (f *fakeResourceGrants) ListViews(ctx context.Context, companyID string, kind domain.ResourceKind) ([]domain.ResourceAccessView, error) {
	f.calls["ListViews"]++
	out := []domain.ResourceAccessView{}
	ids := make([]string, 0, len(f.resources[kind]))
	for id := range f.resources[kind] {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if !f.here(companyID, kind, id) {
			continue
		}
		v, err := f.View(ctx, companyID, kind, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, nil
}

func (f *fakeResourceGrants) ListForUser(_ context.Context, companyID, userID string) ([]domain.ResourceGrant, error) {
	if f.users[userID] != companyID {
		return nil, domain.ErrNotFound
	}
	out := []domain.ResourceGrant{}
	for _, g := range f.grants {
		if g.UserID == userID {
			out = append(out, g)
		}
	}
	return out, nil
}

func (f *fakeResourceGrants) SetAccessMode(_ context.Context, companyID string, kind domain.ResourceKind, id string, mode domain.AccessMode) (domain.AccessModeChange, error) {
	f.calls["SetAccessMode"]++
	if !f.here(companyID, kind, id) {
		return domain.AccessModeChange{}, domain.ErrNotFound
	}
	f.modes[kind][id] = mode
	return domain.AccessModeChange{AccessMode: mode}, nil
}

func (f *fakeResourceGrants) Grant(_ context.Context, companyID, userID string, kind domain.ResourceKind, id, grantedBy string) error {
	f.calls["Grant"]++
	if f.users[userID] != companyID || !f.here(companyID, kind, id) {
		return domain.ErrNotFound
	}
	key := userID + "/" + string(kind) + "/" + id
	if _, held := f.grants[key]; !held {
		f.grants[key] = domain.ResourceGrant{
			UserID: userID, Kind: kind, ResourceID: id, GrantedBy: grantedBy,
			GrantedAt: time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC),
		}
	}
	return nil
}

func (f *fakeResourceGrants) Revoke(_ context.Context, companyID, userID string, kind domain.ResourceKind, id string) error {
	if f.users[userID] != companyID || !f.here(companyID, kind, id) {
		return domain.ErrNotFound
	}
	delete(f.grants, userID+"/"+string(kind)+"/"+id)
	return nil
}

// allowed asks the decision function, the way a seam will.
func allowed(t *testing.T, store *fakeResourceGrants, userID string, role domain.Role, kind domain.ResourceKind, id string) bool {
	t.Helper()
	d, err := authz.New(store).Decide(context.Background(), authz.Subject{CompanyID: "co-1", UserID: userID, Role: role}, kind, id)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	return d.Allowed
}

// The ticket's lifecycle, end to end through the service and the decision
// function, for every kind: open for everyone; restricted with no grants for
// nobody, the admin who restricted it included; a grant opens it for one
// person; re-opening restores everyone **without touching the grant row**; and
// restricting again finds the grant still there.
func TestResourceAccessLifecycleForEveryKind(t *testing.T) {
	ctx := context.Background()
	for _, kind := range domain.AllResourceKinds {
		t.Run(string(kind), func(t *testing.T) {
			store := newFakeResourceGrants()
			svc := NewResourceAccessService(store)

			if !allowed(t, store, "member-1", domain.RoleMember, kind, "ours") {
				t.Fatal("an open resource refused a member with no grant")
			}

			if _, err := svc.SetAccessMode(ctx, "co-1", "admin-1", string(kind), "ours", "restricted"); err != nil {
				t.Fatalf("restrict: %v", err)
			}
			if allowed(t, store, "member-1", domain.RoleMember, kind, "ours") {
				t.Error("restricted with no grants, yet the member reached it")
			}
			if allowed(t, store, "admin-1", domain.RoleAdmin, kind, "ours") {
				t.Error("restricted with no grants, yet the admin who restricted it reached it — decision 4")
			}

			if err := svc.Grant(ctx, "co-1", "admin-1", "member-1", string(kind), "ours"); err != nil {
				t.Fatalf("grant: %v", err)
			}
			if !allowed(t, store, "member-1", domain.RoleMember, kind, "ours") {
				t.Error("granted, yet refused")
			}
			if allowed(t, store, "admin-1", domain.RoleAdmin, kind, "ours") {
				t.Error("the admin reached a restricted resource on the strength of granting somebody else")
			}

			grantsBefore := len(store.grants)
			if _, err := svc.SetAccessMode(ctx, "co-1", "admin-1", string(kind), "ours", "open"); err != nil {
				t.Fatalf("re-open: %v", err)
			}
			if !allowed(t, store, "admin-1", domain.RoleAdmin, kind, "ours") {
				t.Error("re-opened, yet the admin is refused")
			}
			if len(store.grants) != grantsBefore {
				t.Errorf("re-opening changed the grant rows: %d → %d", grantsBefore, len(store.grants))
			}

			if _, err := svc.SetAccessMode(ctx, "co-1", "admin-1", string(kind), "ours", "restricted"); err != nil {
				t.Fatalf("restrict again: %v", err)
			}
			if !allowed(t, store, "member-1", domain.RoleMember, kind, "ours") {
				t.Error("restricted again, and the grant that survived the re-open no longer opens it")
			}
		})
	}
}

// A grant cannot name a resource in another company, nor a person in one.
func TestResourceGrantCannotCrossCompanies(t *testing.T) {
	ctx := context.Background()
	store := newFakeResourceGrants()
	svc := NewResourceAccessService(store)

	if err := svc.Grant(ctx, "co-1", "admin-1", "member-1", "dashboard", "theirs"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("grant on another company's dashboard = %v, want ErrNotFound", err)
	}
	if err := svc.Grant(ctx, "co-1", "admin-1", "stranger", "dashboard", "ours"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("grant to another company's user = %v, want ErrNotFound", err)
	}
	if _, err := svc.SetAccessMode(ctx, "co-1", "admin-1", "dashboard", "theirs", "restricted"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("restricting another company's dashboard = %v, want ErrNotFound", err)
	}
	if _, err := svc.View(ctx, "co-1", "dashboard", "theirs"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("viewing another company's dashboard = %v, want ErrNotFound", err)
	}
	if len(store.grants) != 0 {
		t.Errorf("a cross-company request wrote %d grants", len(store.grants))
	}
	if store.modes[domain.ResourceKindDashboard]["theirs"] != domain.AccessModeOpen {
		t.Error("a cross-company request changed another company's access mode")
	}
}

func TestResourceGrantTwiceIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := newFakeResourceGrants()
	svc := NewResourceAccessService(store)
	for i := 0; i < 2; i++ {
		if err := svc.Grant(ctx, "co-1", "admin-1", "member-1", "agent", "ours"); err != nil {
			t.Fatalf("grant #%d: %v", i+1, err)
		}
	}
	if len(store.grants) != 1 {
		t.Errorf("%d grants after granting twice, want 1", len(store.grants))
	}
	if err := svc.Revoke(ctx, "co-1", "admin-1", "member-1", "agent", "ours"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := svc.Revoke(ctx, "co-1", "admin-1", "member-1", "agent", "ours"); err != nil {
		t.Fatalf("second revoke is an error, want idempotent success: %v", err)
	}
}

// An unknown kind or mode is refused before the store is asked anything.
func TestResourceAccessRefusesOutsideTheVocabulary(t *testing.T) {
	ctx := context.Background()
	store := newFakeResourceGrants()
	svc := NewResourceAccessService(store)

	if err := svc.Grant(ctx, "co-1", "admin-1", "member-1", "folder", "ours"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("unknown kind = %v, want ErrInvalidInput", err)
	}
	if _, err := svc.SetAccessMode(ctx, "co-1", "admin-1", "agent", "ours", "public"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("unknown mode = %v, want ErrInvalidInput", err)
	}
	if _, err := svc.SetAccessMode(ctx, "co-1", "admin-1", "agent", "ours", ""); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("empty mode = %v, want ErrInvalidInput", err)
	}
	if store.calls["Grant"] != 0 || store.calls["SetAccessMode"] != 0 {
		t.Errorf("the store was reached with an invalid kind or mode: %v", store.calls)
	}
}

// The list is one company's resources of one kind, and a change made through
// the service shows up in it exactly as it does in View — the matrix's two
// directions are drawn from this, so it cannot be a second opinion.
func TestResourceAccessListIsOneCompanysKind(t *testing.T) {
	ctx := context.Background()
	store := newFakeResourceGrants()
	svc := NewResourceAccessService(store)
	if err := svc.Grant(ctx, "co-1", "admin-1", "member-1", "agent", "ours"); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if _, err := svc.SetAccessMode(ctx, "co-1", "admin-1", "agent", "ours", "restricted"); err != nil {
		t.Fatalf("restrict: %v", err)
	}

	list, err := svc.List(ctx, "co-1", "agent")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ResourceID != "ours" {
		t.Fatalf("List = %+v, want only this company's agent", list)
	}
	view, err := svc.View(ctx, "co-1", "agent", "ours")
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if list[0].AccessMode != view.AccessMode || len(list[0].Grants) != 1 || list[0].Grants[0] != view.Grants[0] {
		t.Errorf("List says %+v, View says %+v", list[0], *view)
	}

	if _, err := svc.List(ctx, "co-1", "folder"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("List of an unknown kind = %v, want ErrInvalidInput", err)
	}
	if store.calls["ListViews"] != 1 {
		t.Errorf("the store was listed %d times, want once — the unknown kind must not reach it", store.calls["ListViews"])
	}
}

func TestResourceAccessNilServiceRefuses(t *testing.T) {
	var svc *ResourceAccessService
	if err := svc.Grant(context.Background(), "co-1", "a", "u", "agent", "x"); err == nil {
		t.Error("a nil service granted something")
	}
	if _, err := svc.List(context.Background(), "co-1", "agent"); err == nil {
		t.Error("a nil service listed something")
	}
}
