package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/domain"
)

// authFixture wires an AuthService over the same in-memory user repo the team
// tests use. The company repo is nil: Login and Refresh never touch it, and a
// nil here means a future change that does will fail loudly.
func authFixture(t *testing.T) (*AuthService, *fakeUsers, *auth.TokenSigner) {
	t.Helper()
	signer, err := auth.NewTokenSigner("0123456789abcdef0123456789abcdef", 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}
	users := newFakeUsers()
	return NewAuthService(nil, users, signer), users, signer
}

func seedUser(t *testing.T, users *fakeUsers, email, password string, role domain.Role) *domain.User {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	u := &domain.User{CompanyID: "co-1", Email: email, PasswordHash: hash, Role: role}
	if err := users.Create(context.Background(), u); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u
}

// Deactivating a member has to end their access. Without this check the only
// thing removal changed was a badge in the UI.
func TestLoginRejectsInactiveAccounts(t *testing.T) {
	ctx := context.Background()

	t.Run("deactivated", func(t *testing.T) {
		svc, users, _ := authFixture(t)
		u := seedUser(t, users, "member@acme.test", "correct horse battery", domain.RoleMember)
		if err := users.Deactivate(ctx, "co-1", u.ID, time.Now()); err != nil {
			t.Fatalf("Deactivate: %v", err)
		}
		_, err := svc.Login(ctx, "member@acme.test", "correct horse battery")
		if !errors.Is(err, domain.ErrAccountInactive) {
			t.Fatalf("err = %v, want ErrAccountInactive", err)
		}
	})

	t.Run("pending", func(t *testing.T) {
		svc, users, _ := authFixture(t)
		pending := &domain.User{CompanyID: "co-1", Email: "invitee@acme.test", Role: domain.RoleMember}
		if err := users.CreatePending(ctx, pending); err != nil {
			t.Fatalf("CreatePending: %v", err)
		}
		// The stored hash is the empty string, so this stops at the password
		// check rather than the active check — which is the intended order:
		// someone who cannot authenticate learns nothing about the account.
		_, err := svc.Login(ctx, "invitee@acme.test", "")
		if !errors.Is(err, domain.ErrCredentialsBad) {
			t.Fatalf("err = %v, want ErrCredentialsBad", err)
		}
	})

	t.Run("active", func(t *testing.T) {
		svc, users, _ := authFixture(t)
		seedUser(t, users, "member@acme.test", "correct horse battery", domain.RoleMember)
		if _, err := svc.Login(ctx, "member@acme.test", "correct horse battery"); err != nil {
			t.Fatalf("Login: %v", err)
		}
	})
}

// A refresh token lives seven days. If Refresh re-signed the claims it was
// handed, deactivating someone would leave them a week of working sessions and
// a demotion would not take effect until they logged out.
func TestRefreshRereadsTheUser(t *testing.T) {
	ctx := context.Background()

	t.Run("a deactivated user cannot refresh", func(t *testing.T) {
		svc, users, signer := authFixture(t)
		u := seedUser(t, users, "member@acme.test", "correct horse battery", domain.RoleMember)
		refresh, err := signer.IssueRefreshToken(u.ID, u.CompanyID, string(u.Role))
		if err != nil {
			t.Fatalf("IssueRefreshToken: %v", err)
		}
		if _, err := svc.Refresh(ctx, refresh); err != nil {
			t.Fatalf("Refresh before deactivation: %v", err)
		}
		if err := users.Deactivate(ctx, "co-1", u.ID, time.Now()); err != nil {
			t.Fatalf("Deactivate: %v", err)
		}
		if _, err := svc.Refresh(ctx, refresh); !errors.Is(err, domain.ErrAccountInactive) {
			t.Fatalf("err = %v, want ErrAccountInactive", err)
		}
	})

	t.Run("a demoted admin refreshes into a member token", func(t *testing.T) {
		svc, users, signer := authFixture(t)
		u := seedUser(t, users, "founder@acme.test", "correct horse battery", domain.RoleAdmin)
		refresh, err := signer.IssueRefreshToken(u.ID, u.CompanyID, string(domain.RoleAdmin))
		if err != nil {
			t.Fatalf("IssueRefreshToken: %v", err)
		}
		if err := users.UpdateRole(ctx, "co-1", u.ID, domain.RoleMember); err != nil {
			t.Fatalf("UpdateRole: %v", err)
		}
		access, err := svc.Refresh(ctx, refresh)
		if err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		claims, err := signer.Verify(access)
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if claims.Role != string(domain.RoleMember) {
			t.Errorf("role = %q, want member — the refresh carried the stale claim", claims.Role)
		}
	})

	t.Run("a deleted user cannot refresh", func(t *testing.T) {
		svc, users, signer := authFixture(t)
		u := seedUser(t, users, "gone@acme.test", "correct horse battery", domain.RoleMember)
		refresh, err := signer.IssueRefreshToken(u.ID, u.CompanyID, string(u.Role))
		if err != nil {
			t.Fatalf("IssueRefreshToken: %v", err)
		}
		if err := users.Delete(ctx, "co-1", u.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := svc.Refresh(ctx, refresh); !errors.Is(err, domain.ErrUnauthorized) {
			t.Fatalf("err = %v, want ErrUnauthorized", err)
		}
	})

	t.Run("an access token is not a refresh token", func(t *testing.T) {
		svc, users, signer := authFixture(t)
		u := seedUser(t, users, "member@acme.test", "correct horse battery", domain.RoleMember)
		access, err := signer.IssueAccessToken(u.ID, u.CompanyID, string(u.Role))
		if err != nil {
			t.Fatalf("IssueAccessToken: %v", err)
		}
		if _, err := svc.Refresh(ctx, access); !errors.Is(err, domain.ErrUnauthorized) {
			t.Fatalf("err = %v, want ErrUnauthorized", err)
		}
	})
}

// Signup and invite acceptance both set a password; the rule has to be the
// same at both doors.
func TestPasswordPolicyIsShared(t *testing.T) {
	if err := validatePassword("short"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if err := validatePassword("12345678"); err != nil {
		t.Fatalf("an eight-character password was rejected: %v", err)
	}
}
