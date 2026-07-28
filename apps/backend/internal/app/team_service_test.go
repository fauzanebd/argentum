package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/domain"
)

// fakeUsers and fakeInvites are in-memory stand-ins for the postgres repos.
// They reproduce the two guards the SQL relies on — the global uniqueness of
// users.email, and Activate only firing on a still-pending row — because those
// are what make an invite single-use, and a fake that ignored them would let
// this suite pass on a service that has neither.
type fakeUsers struct {
	byID  map[string]*domain.User
	seq   int
	calls []string
}

func newFakeUsers() *fakeUsers { return &fakeUsers{byID: map[string]*domain.User{}} }

func (f *fakeUsers) put(u *domain.User) {
	f.seq++
	if u.ID == "" {
		u.ID = "u" + string(rune('0'+f.seq))
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	}
	cp := *u
	f.byID[u.ID] = &cp
}

func (f *fakeUsers) Create(_ context.Context, u *domain.User) error {
	if _, err := f.byEmail(u.Email); err == nil {
		return domain.ErrAlreadyExists
	}
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	u.ActivatedAt = &now
	f.put(u)
	return nil
}

func (f *fakeUsers) CreatePending(_ context.Context, u *domain.User) error {
	if _, err := f.byEmail(u.Email); err == nil {
		return domain.ErrAlreadyExists
	}
	u.ActivatedAt = nil
	u.PasswordHash = ""
	f.put(u)
	return nil
}

func (f *fakeUsers) byEmail(email string) (*domain.User, error) {
	for _, u := range f.byID {
		if strings.EqualFold(u.Email, email) {
			return u, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (f *fakeUsers) GetByID(_ context.Context, id string) (*domain.User, error) {
	u, ok := f.byID[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *u
	return &cp, nil
}

func (f *fakeUsers) GetByEmail(_ context.Context, email string) (*domain.User, error) {
	u, err := f.byEmail(email)
	if err != nil {
		return nil, err
	}
	cp := *u
	return &cp, nil
}

func (f *fakeUsers) ListByCompany(_ context.Context, companyID string) ([]*domain.User, error) {
	var out []*domain.User
	for _, u := range f.byID {
		if u.CompanyID == companyID {
			cp := *u
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (f *fakeUsers) Activate(_ context.Context, id, hash string, at time.Time) error {
	f.calls = append(f.calls, "activate:"+id)
	u, ok := f.byID[id]
	if !ok || u.ActivatedAt != nil || u.DeactivatedAt != nil {
		return domain.ErrNotFound
	}
	u.PasswordHash = hash
	u.ActivatedAt = &at
	return nil
}

func (f *fakeUsers) UpdateRole(_ context.Context, companyID, id string, role domain.Role) error {
	u, ok := f.byID[id]
	if !ok || u.CompanyID != companyID {
		return domain.ErrNotFound
	}
	u.Role = role
	return nil
}

func (f *fakeUsers) Deactivate(_ context.Context, companyID, id string, at time.Time) error {
	u, ok := f.byID[id]
	if !ok || u.CompanyID != companyID || u.DeactivatedAt != nil {
		return domain.ErrNotFound
	}
	u.DeactivatedAt = &at
	return nil
}

func (f *fakeUsers) Delete(_ context.Context, companyID, id string) error {
	u, ok := f.byID[id]
	if !ok || u.CompanyID != companyID {
		return domain.ErrNotFound
	}
	delete(f.byID, id)
	return nil
}

func (f *fakeUsers) CountActiveAdmins(_ context.Context, companyID string) (int, error) {
	n := 0
	for _, u := range f.byID {
		if u.CompanyID == companyID && u.Role == domain.RoleAdmin && u.Active() {
			n++
		}
	}
	return n, nil
}

type fakeInvites struct {
	rows map[string]*domain.UserInvite
	seq  int
}

func newFakeInvites() *fakeInvites { return &fakeInvites{rows: map[string]*domain.UserInvite{}} }

func (f *fakeInvites) Create(_ context.Context, inv *domain.UserInvite) error {
	f.seq++
	inv.ID = "i" + string(rune('0'+f.seq))
	inv.CreatedAt = time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	cp := *inv
	f.rows[inv.ID] = &cp
	return nil
}

func (f *fakeInvites) GetByTokenHash(_ context.Context, hash string) (*domain.UserInvite, error) {
	for _, inv := range f.rows {
		if inv.TokenHash == hash {
			cp := *inv
			return &cp, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (f *fakeInvites) ListOpenByCompany(_ context.Context, companyID string) ([]*domain.UserInvite, error) {
	var out []*domain.UserInvite
	for _, inv := range f.rows {
		if inv.CompanyID == companyID && inv.AcceptedAt == nil {
			cp := *inv
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (f *fakeInvites) MarkAccepted(_ context.Context, id string, at time.Time) error {
	inv, ok := f.rows[id]
	if !ok || inv.AcceptedAt != nil {
		return domain.ErrNotFound
	}
	inv.AcceptedAt = &at
	return nil
}

func (f *fakeInvites) DeleteOpenFor(_ context.Context, companyID, email string) error {
	for id, inv := range f.rows {
		if inv.CompanyID == companyID && strings.EqualFold(inv.Email, email) && inv.AcceptedAt == nil {
			delete(f.rows, id)
		}
	}
	return nil
}

// teamFixture returns a service over one company that already has one active
// admin, which is the state every real company starts in after signup.
func teamFixture(t *testing.T) (*TeamService, *fakeUsers, *fakeInvites, *domain.User) {
	t.Helper()
	users, invites := newFakeUsers(), newFakeInvites()
	svc := NewTeamService(users, invites)
	svc.now = func() time.Time { return time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC) }

	admin := &domain.User{CompanyID: "co-1", Email: "founder@acme.test", Role: domain.RoleAdmin}
	if err := users.Create(context.Background(), admin); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	return svc, users, invites, admin
}

func TestInviteAndAccept(t *testing.T) {
	ctx := context.Background()
	svc, users, _, admin := teamFixture(t)

	res, err := svc.Invite(ctx, "co-1", admin.ID, "  Analyst@Acme.test ", domain.RoleMember)
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if res.Token == "" {
		t.Fatal("no token returned")
	}
	if res.Member.Email != "analyst@acme.test" {
		t.Errorf("email = %q, want the trimmed lowercase form", res.Member.Email)
	}
	if res.Member.Status != "pending" {
		t.Errorf("status = %q, want pending", res.Member.Status)
	}

	// Pending means unusable, not merely unstamped: the password hash is empty
	// so even a caller that skipped the Active() check cannot verify against
	// it.
	pending, err := users.GetByEmail(ctx, "analyst@acme.test")
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if pending.Active() || pending.PasswordHash != "" {
		t.Fatalf("pending user is usable: active=%v hash=%q", pending.Active(), pending.PasswordHash)
	}

	user, err := svc.Accept(ctx, res.Token, "correct horse battery")
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if !user.Active() {
		t.Error("accepted user is not active")
	}
	if user.Role != domain.RoleMember {
		t.Errorf("role = %q, want member", user.Role)
	}
	ok, err := auth.VerifyPassword("correct horse battery", user.PasswordHash)
	if err != nil || !ok {
		t.Errorf("the password set at accept does not verify (ok=%v err=%v)", ok, err)
	}
}

// The whole point of hashing the token is that the stored row cannot be
// replayed. If the plaintext ever reached the database, an operator with read
// access to user_invites could activate any pending account.
func TestInviteStoresOnlyTheHash(t *testing.T) {
	ctx := context.Background()
	svc, _, invites, admin := teamFixture(t)

	res, err := svc.Invite(ctx, "co-1", admin.ID, "analyst@acme.test", domain.RoleMember)
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	for _, inv := range invites.rows {
		if inv.TokenHash == res.Token {
			t.Fatal("the plaintext token was stored")
		}
		if inv.TokenHash != auth.HashInviteToken(res.Token) {
			t.Fatal("the stored hash does not match the issued token")
		}
	}
}

func TestAcceptIsSingleUse(t *testing.T) {
	ctx := context.Background()
	svc, _, _, admin := teamFixture(t)

	res, err := svc.Invite(ctx, "co-1", admin.ID, "analyst@acme.test", domain.RoleMember)
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if _, err := svc.Accept(ctx, res.Token, "correct horse battery"); err != nil {
		t.Fatalf("first Accept: %v", err)
	}
	// A second accept must not reset the password of a live account.
	_, err = svc.Accept(ctx, res.Token, "attacker chosen password")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second Accept: err = %v, want ErrNotFound", err)
	}
}

func TestAcceptRejectsExpiredAndUnknownTokens(t *testing.T) {
	ctx := context.Background()
	svc, _, _, admin := teamFixture(t)

	res, err := svc.Invite(ctx, "co-1", admin.ID, "analyst@acme.test", domain.RoleMember)
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}

	t.Run("unknown token", func(t *testing.T) {
		if _, err := svc.Accept(ctx, "not-a-real-token", "correct horse battery"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("expired token", func(t *testing.T) {
		svc.now = func() time.Time {
			return time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC).Add(InviteTTL + time.Minute)
		}
		defer func() {
			svc.now = func() time.Time { return time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC) }
		}()
		if _, err := svc.Accept(ctx, res.Token, "correct horse battery"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("short password", func(t *testing.T) {
		if _, err := svc.Accept(ctx, res.Token, "short"); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	})
}

// Re-inviting is the "the email never arrived" path. It must issue a working
// token and retire the previous one, or an admin who re-sends twice leaves two
// live doors open.
func TestReInviteReplacesTheToken(t *testing.T) {
	ctx := context.Background()
	svc, _, _, admin := teamFixture(t)

	first, err := svc.Invite(ctx, "co-1", admin.ID, "analyst@acme.test", domain.RoleMember)
	if err != nil {
		t.Fatalf("first Invite: %v", err)
	}
	second, err := svc.Invite(ctx, "co-1", admin.ID, "analyst@acme.test", domain.RoleAdmin)
	if err != nil {
		t.Fatalf("second Invite: %v", err)
	}
	if first.Token == second.Token {
		t.Fatal("the token was not rotated")
	}
	if _, err := svc.Accept(ctx, first.Token, "correct horse battery"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("the superseded token still works: err = %v", err)
	}
	user, err := svc.Accept(ctx, second.Token, "correct horse battery")
	if err != nil {
		t.Fatalf("Accept with the current token: %v", err)
	}
	if user.Role != domain.RoleAdmin {
		t.Errorf("role = %q, want admin — the re-invite's role should win", user.Role)
	}
}

func TestInviteRejectsAnAddressThatAlreadyHasAnAccount(t *testing.T) {
	ctx := context.Background()
	svc, _, _, admin := teamFixture(t)

	if _, err := svc.Invite(ctx, "co-1", admin.ID, admin.Email, domain.RoleMember); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("err = %v, want ErrAlreadyExists", err)
	}
}

// users.email is globally unique, so a pending invite in one company blocks
// the address everywhere. The refusal must not distinguish that case from "the
// address is already an active account here", or the endpoint becomes a
// cross-tenant lookup for whether a person has an Argentum account.
func TestInviteDoesNotLeakOtherCompanies(t *testing.T) {
	ctx := context.Background()
	svc, users, _, admin := teamFixture(t)

	other := &domain.User{CompanyID: "co-2", Email: "shared@acme.test", Role: domain.RoleAdmin}
	if err := users.Create(ctx, other); err != nil {
		t.Fatalf("seed other company: %v", err)
	}

	_, errActive := svc.Invite(ctx, "co-1", admin.ID, "shared@acme.test", domain.RoleMember)
	_, errSelf := svc.Invite(ctx, "co-1", admin.ID, admin.Email, domain.RoleMember)
	if !errors.Is(errActive, domain.ErrAlreadyExists) || !errors.Is(errSelf, domain.ErrAlreadyExists) {
		t.Fatalf("errors = (%v, %v), want both ErrAlreadyExists", errActive, errSelf)
	}
}

func TestInviteValidatesInput(t *testing.T) {
	ctx := context.Background()
	svc, _, _, admin := teamFixture(t)

	cases := []struct {
		name  string
		email string
		role  domain.Role
	}{
		{"empty email", "", domain.RoleMember},
		{"not an email", "analyst", domain.RoleMember},
		{"unknown role", "analyst@acme.test", domain.Role("owner")},
		{"empty role", "analyst@acme.test", domain.Role("")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.Invite(ctx, "co-1", admin.ID, tc.email, tc.role); !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestLastAdminCannotBeDemotedOrRemoved(t *testing.T) {
	ctx := context.Background()
	svc, users, _, admin := teamFixture(t)

	if err := svc.ChangeRole(ctx, "co-1", admin.ID, domain.RoleMember); !errors.Is(err, domain.ErrLastAdmin) {
		t.Fatalf("demote: err = %v, want ErrLastAdmin", err)
	}
	if err := svc.Remove(ctx, "co-1", admin.ID); !errors.Is(err, domain.ErrLastAdmin) {
		t.Fatalf("remove: err = %v, want ErrLastAdmin", err)
	}

	// An admin who has been invited but has not accepted cannot rescue the
	// company, so they must not unlock the guard either.
	res, err := svc.Invite(ctx, "co-1", admin.ID, "second@acme.test", domain.RoleAdmin)
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if err := svc.Remove(ctx, "co-1", admin.ID); !errors.Is(err, domain.ErrLastAdmin) {
		t.Fatalf("remove with a pending admin: err = %v, want ErrLastAdmin", err)
	}

	// Once they accept, the first admin can step down.
	if _, err := svc.Accept(ctx, res.Token, "correct horse battery"); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if err := svc.ChangeRole(ctx, "co-1", admin.ID, domain.RoleMember); err != nil {
		t.Fatalf("demote with a second active admin: %v", err)
	}
	got, err := users.GetByID(ctx, admin.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Role != domain.RoleMember {
		t.Errorf("role = %q, want member", got.Role)
	}
}

// A deactivated admin does not count towards the total either — otherwise
// removing the second-to-last admin twice would empty the company.
func TestDeactivatedAdminsDoNotCountAsCover(t *testing.T) {
	ctx := context.Background()
	svc, users, _, admin := teamFixture(t)

	second := &domain.User{CompanyID: "co-1", Email: "second@acme.test", Role: domain.RoleAdmin}
	if err := users.Create(ctx, second); err != nil {
		t.Fatalf("seed second admin: %v", err)
	}
	if err := svc.Remove(ctx, "co-1", second.ID); err != nil {
		t.Fatalf("remove the second admin: %v", err)
	}
	if err := svc.Remove(ctx, "co-1", admin.ID); !errors.Is(err, domain.ErrLastAdmin) {
		t.Fatalf("remove the remaining admin: err = %v, want ErrLastAdmin", err)
	}
}

// Removing a member deactivates them — the row stays for audit and for the
// usage history attached to it. Revoking a *pending* invite deletes the row,
// because it is holding a globally unique email hostage on behalf of somebody
// who never accepted.
func TestRemoveDeactivatesMembersAndDeletesPendingOnes(t *testing.T) {
	ctx := context.Background()
	svc, users, invites, admin := teamFixture(t)

	member := &domain.User{CompanyID: "co-1", Email: "member@acme.test", Role: domain.RoleMember}
	if err := users.Create(ctx, member); err != nil {
		t.Fatalf("seed member: %v", err)
	}
	if err := svc.Remove(ctx, "co-1", member.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	got, err := users.GetByID(ctx, member.ID)
	if err != nil {
		t.Fatalf("the deactivated member's row is gone: %v", err)
	}
	if got.Active() || got.DeactivatedAt == nil {
		t.Error("the member was not deactivated")
	}

	res, err := svc.Invite(ctx, "co-1", admin.ID, "pending@acme.test", domain.RoleMember)
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if err := svc.Remove(ctx, "co-1", res.Member.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := users.GetByID(ctx, res.Member.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("the revoked pending user survives: %v", err)
	}
	if len(invites.rows) != 0 {
		t.Errorf("the revoked invite survives: %d rows", len(invites.rows))
	}
	// The address must be invitable again after a revoke.
	if _, err := svc.Invite(ctx, "co-1", admin.ID, "pending@acme.test", domain.RoleMember); err != nil {
		t.Errorf("re-invite after revoke: %v", err)
	}
}

// Every mutation takes the company from the caller's session and the id from
// the URL. A member id from another tenant must read as absent, not as
// forbidden — the caller has no business learning it exists.
func TestTeamMutationsAreCompanyScoped(t *testing.T) {
	ctx := context.Background()
	svc, users, _, _ := teamFixture(t)

	outsider := &domain.User{CompanyID: "co-2", Email: "outsider@other.test", Role: domain.RoleMember}
	if err := users.Create(ctx, outsider); err != nil {
		t.Fatalf("seed outsider: %v", err)
	}

	if err := svc.ChangeRole(ctx, "co-1", outsider.ID, domain.RoleAdmin); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ChangeRole across tenants: err = %v, want ErrNotFound", err)
	}
	if err := svc.Remove(ctx, "co-1", outsider.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Remove across tenants: err = %v, want ErrNotFound", err)
	}
	got, err := users.GetByID(ctx, outsider.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Role != domain.RoleMember || !got.Active() {
		t.Error("the outsider was modified")
	}
}

func TestListReportsInviteState(t *testing.T) {
	ctx := context.Background()
	svc, _, _, admin := teamFixture(t)

	if _, err := svc.Invite(ctx, "co-1", admin.ID, "analyst@acme.test", domain.RoleMember); err != nil {
		t.Fatalf("Invite: %v", err)
	}
	members, err := svc.List(ctx, "co-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("got %d members, want 2", len(members))
	}
	byEmail := map[string]Member{}
	for _, m := range members {
		byEmail[m.Email] = m
	}
	if got := byEmail["founder@acme.test"].Status; got != "active" {
		t.Errorf("founder status = %q, want active", got)
	}
	pending := byEmail["analyst@acme.test"]
	if pending.Status != "pending" {
		t.Errorf("invitee status = %q, want pending", pending.Status)
	}
	if pending.InviteExpiresAt == nil {
		t.Error("the pending member carries no invite expiry")
	}
}

// Preview is the accept page's first call. It must resolve a live token
// without consuming it, and refuse a spent one.
func TestPreviewDoesNotConsumeTheInvite(t *testing.T) {
	ctx := context.Background()
	svc, _, _, admin := teamFixture(t)

	res, err := svc.Invite(ctx, "co-1", admin.ID, "analyst@acme.test", domain.RoleMember)
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	preview, err := svc.Preview(ctx, res.Token)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if preview.Email != "analyst@acme.test" || preview.Role != domain.RoleMember {
		t.Errorf("preview = %+v, want the invited address and role", preview)
	}
	if _, err := svc.Accept(ctx, res.Token, "correct horse battery"); err != nil {
		t.Fatalf("Accept after Preview: %v", err)
	}
	if _, err := svc.Preview(ctx, res.Token); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Preview of a spent token: err = %v, want ErrNotFound", err)
	}
}
