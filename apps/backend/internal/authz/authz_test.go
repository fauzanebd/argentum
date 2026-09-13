package authz

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// TestEvaluateEveryCell is the whole rule written down: every mode this code
// stores, one it does not, granted or not, found or not. Twelve cells, each
// with its expected value typed out rather than derived — a table computed from
// the code under test would agree with the code under test.
func TestEvaluateEveryCell(t *testing.T) {
	const bogus domain.AccessMode = "public"
	cases := []struct {
		mode    domain.AccessMode
		granted bool
		found   bool
		want    Decision
	}{
		{domain.AccessModeOpen, false, true, Decision{true, ReasonOpen}},
		{domain.AccessModeOpen, true, true, Decision{true, ReasonOpen}},
		{domain.AccessModeRestricted, false, true, Decision{false, ReasonNotGranted}},
		{domain.AccessModeRestricted, true, true, Decision{true, ReasonGranted}},
		// A mode the CHECK should have refused is closed, not open.
		{bogus, false, true, Decision{false, ReasonNotGranted}},
		{bogus, true, true, Decision{false, ReasonNotGranted}},
		// Not found wins over everything, including a grant row that somehow
		// names an object this company does not have.
		{domain.AccessModeOpen, false, false, Decision{false, ReasonNotFound}},
		{domain.AccessModeOpen, true, false, Decision{false, ReasonNotFound}},
		{domain.AccessModeRestricted, false, false, Decision{false, ReasonNotFound}},
		{domain.AccessModeRestricted, true, false, Decision{false, ReasonNotFound}},
		{bogus, false, false, Decision{false, ReasonNotFound}},
		{bogus, true, false, Decision{false, ReasonNotFound}},
	}
	for _, tc := range cases {
		name := fmt.Sprintf("mode=%s granted=%v found=%v", tc.mode, tc.granted, tc.found)
		t.Run(name, func(t *testing.T) {
			got := Evaluate(domain.ResourceAccess{Mode: tc.mode, Granted: tc.granted}, tc.found)
			if got != tc.want {
				t.Errorf("Evaluate = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// fakeLoader is resource rows and grant rows for several companies, and counts
// how often it is asked — "one query for fifty ids" is an assertion about that
// count.
type fakeLoader struct {
	// resources: company → kind → id → mode
	resources map[string]map[domain.ResourceKind]map[string]domain.AccessMode
	// grants: "company/user/kind/id"
	grants map[string]bool
	calls  int
	asked  [][]string
	err    error
}

func newFakeLoader() *fakeLoader {
	return &fakeLoader{
		resources: map[string]map[domain.ResourceKind]map[string]domain.AccessMode{},
		grants:    map[string]bool{},
	}
}

func (f *fakeLoader) put(company string, kind domain.ResourceKind, id string, mode domain.AccessMode) {
	if f.resources[company] == nil {
		f.resources[company] = map[domain.ResourceKind]map[string]domain.AccessMode{}
	}
	if f.resources[company][kind] == nil {
		f.resources[company][kind] = map[string]domain.AccessMode{}
	}
	f.resources[company][kind][id] = mode
}

func (f *fakeLoader) grant(company, user string, kind domain.ResourceKind, id string) {
	f.grants[company+"/"+user+"/"+string(kind)+"/"+id] = true
}

func (f *fakeLoader) LoadAccess(_ context.Context, companyID, userID string, kind domain.ResourceKind, ids []string) (map[string]domain.ResourceAccess, error) {
	f.calls++
	f.asked = append(f.asked, slices.Clone(ids))
	if f.err != nil {
		return nil, f.err
	}
	out := map[string]domain.ResourceAccess{}
	for _, id := range ids {
		mode, ok := f.resources[companyID][kind][id]
		if !ok {
			continue
		}
		out[id] = domain.ResourceAccess{
			Mode:    mode,
			Granted: userID != "" && f.grants[companyID+"/"+userID+"/"+string(kind)+"/"+id],
		}
	}
	return out, nil
}

var (
	admin  = Subject{CompanyID: "co-1", UserID: "admin-1", Role: domain.RoleAdmin}
	member = Subject{CompanyID: "co-1", UserID: "member-1", Role: domain.RoleMember}
)

func mustDecide(t *testing.T, a *Authorizer, s Subject, kind domain.ResourceKind, id string) Decision {
	t.Helper()
	d, err := a.Decide(context.Background(), s, kind, id)
	if err != nil {
		t.Fatalf("Decide(%s, %s, %s): %v", s.UserID, kind, id, err)
	}
	return d
}

// The ticket's first two acceptance lines, for every kind, for both roles: open
// admits everybody with no grant row; restricted refuses without a grant and
// admits with one. The role column changes nothing, which is decision 4.
func TestDecideForEveryKindAndBothRoles(t *testing.T) {
	for _, kind := range domain.AllResourceKinds {
		for _, s := range []Subject{admin, member} {
			t.Run(fmt.Sprintf("%s/%s", kind, s.Role), func(t *testing.T) {
				loader := newFakeLoader()
				loader.put("co-1", kind, "open-1", domain.AccessModeOpen)
				loader.put("co-1", kind, "closed-1", domain.AccessModeRestricted)
				loader.put("co-1", kind, "closed-2", domain.AccessModeRestricted)
				loader.grant("co-1", s.UserID, kind, "closed-2")
				a := New(loader)

				if d := mustDecide(t, a, s, kind, "open-1"); d != (Decision{true, ReasonOpen}) {
					t.Errorf("open resource, no grant: %+v", d)
				}
				if d := mustDecide(t, a, s, kind, "closed-1"); d != (Decision{false, ReasonNotGranted}) {
					t.Errorf("restricted resource, no grant: %+v — an %s without a grant must be refused", d, s.Role)
				}
				if d := mustDecide(t, a, s, kind, "closed-2"); d != (Decision{true, ReasonGranted}) {
					t.Errorf("restricted resource, granted: %+v", d)
				}
			})
		}
	}
}

// Restricting with no grants reaches nobody — including the person who made
// it. Authz has no notion of a creator at all, and this is the test that says
// so out loud rather than leaving it as an absence.
func TestRestrictedWithNoGrantsReachesNobodyIncludingItsCreator(t *testing.T) {
	loader := newFakeLoader()
	loader.put("co-1", domain.ResourceKindDashboard, "payroll", domain.AccessModeRestricted)
	a := New(loader)
	creator := Subject{CompanyID: "co-1", UserID: "the-creator", Role: domain.RoleAdmin}
	for _, s := range []Subject{admin, member, creator} {
		if d := mustDecide(t, a, s, domain.ResourceKindDashboard, "payroll"); d.Allowed {
			t.Errorf("%s reached a restricted dashboard nobody was granted", s.UserID)
		}
	}
}

// Flipping back to open restores access by the mode alone: the grant row that
// was there is neither needed nor consulted, and nothing about re-opening
// requires touching it.
func TestReopeningRestoresAccessWithoutTheGrantMattering(t *testing.T) {
	loader := newFakeLoader()
	loader.put("co-1", domain.ResourceKindAgent, "hr", domain.AccessModeRestricted)
	a := New(loader)
	if mustDecide(t, a, member, domain.ResourceKindAgent, "hr").Allowed {
		t.Fatal("restricted and ungranted, yet allowed")
	}
	loader.put("co-1", domain.ResourceKindAgent, "hr", domain.AccessModeOpen)
	if d := mustDecide(t, a, member, domain.ResourceKindAgent, "hr"); d != (Decision{true, ReasonOpen}) {
		t.Fatalf("re-opened resource: %+v, want allowed as open", d)
	}
}

// The tenant-isolation arm at the decision layer: an id that exists in co-2 is,
// asked about from co-1, not found — and a grant row in co-2 for the same user
// id changes nothing.
func TestAnotherCompanysResourceIsNotFound(t *testing.T) {
	loader := newFakeLoader()
	loader.put("co-2", domain.ResourceKindAgent, "theirs", domain.AccessModeOpen)
	loader.grant("co-2", member.UserID, domain.ResourceKindAgent, "theirs")
	a := New(loader)
	if d := mustDecide(t, a, member, domain.ResourceKindAgent, "theirs"); d != (Decision{false, ReasonNotFound}) {
		t.Fatalf("another company's agent: %+v, want refused as not found", d)
	}
}

// "Visible over 50 ids issues one query." Fifty ids with duplicates and a
// capitalised copy go to the loader once, de-duplicated, and come back filtered
// in the order they were asked.
func TestVisibleOverFiftyIdsLoadsOnce(t *testing.T) {
	loader := newFakeLoader()
	var ids []string
	var want []string
	for i := range 50 {
		id := fmt.Sprintf("agent-%02d", i)
		ids = append(ids, id)
		if i%3 == 0 {
			loader.put("co-1", domain.ResourceKindAgent, id, domain.AccessModeRestricted)
		} else {
			loader.put("co-1", domain.ResourceKindAgent, id, domain.AccessModeOpen)
			want = append(want, id)
		}
	}
	ids = append(ids, "agent-01", "AGENT-02")
	want = append(want, "agent-01", "AGENT-02")

	got, err := New(loader).Visible(context.Background(), member, domain.ResourceKindAgent, ids)
	if err != nil {
		t.Fatalf("Visible: %v", err)
	}
	if loader.calls != 1 {
		t.Fatalf("loader asked %d times for %d ids, want 1", loader.calls, len(ids))
	}
	if n := len(loader.asked[0]); n != 50 {
		t.Errorf("loader was handed %d ids, want the 50 distinct ones", n)
	}
	if !slices.Equal(got, want) {
		t.Errorf("Visible = %v\nwant      %v", got, want)
	}
}

func TestVisibleWithNothingToAskLoadsNothing(t *testing.T) {
	loader := newFakeLoader()
	a := New(loader)
	got, err := a.Visible(context.Background(), member, domain.ResourceKindAgent, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("Visible(nil) = (%v, %v)", got, err)
	}
	if d := mustDecide(t, a, member, domain.ResourceKindAgent, ""); d.Allowed {
		t.Fatal("an empty id was allowed")
	}
	if d := mustDecide(t, a, Subject{UserID: "u"}, domain.ResourceKindAgent, "x"); d.Allowed {
		t.Fatal("a subject with no company was allowed")
	}
	if loader.calls != 0 {
		t.Errorf("loader asked %d times with nothing to ask, want 0", loader.calls)
	}
}

// A load that fails is an error, never an answer — and certainly never "open".
func TestLoaderFailureIsAnErrorNotAnAnswer(t *testing.T) {
	loader := newFakeLoader()
	loader.err = errors.New("connection refused")
	a := New(loader)
	if _, err := a.Decide(context.Background(), member, domain.ResourceKindAgent, "x"); err == nil {
		t.Error("Decide returned no error from a failing loader")
	}
	if got, err := a.Visible(context.Background(), member, domain.ResourceKindAgent, []string{"x"}); err == nil || got != nil {
		t.Errorf("Visible = (%v, %v), want (nil, error)", got, err)
	}
}

func TestUnknownKindAndMissingLoaderAreErrors(t *testing.T) {
	if _, err := New(newFakeLoader()).Decide(context.Background(), member, "folder", "x"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("unknown kind: %v, want ErrInvalidInput", err)
	}
	if _, err := New(nil).Decide(context.Background(), member, domain.ResourceKindAgent, "x"); err == nil {
		t.Error("an Authorizer with no loader decided something")
	}
}
