package tenantctx

import (
	"context"
	"testing"
)

// An unset key must read as "" and never as a value belonging to something
// else. Every tool guards on `CompanyID(ctx) == ""`; a getter that returned a
// stale or wrong value instead of "" would turn that guard into a
// cross-tenant read.
func TestGettersReturnEmptyWhenUnset(t *testing.T) {
	ctx := context.Background()

	if got := CompanyID(ctx); got != "" {
		t.Errorf("CompanyID on a bare context = %q, want \"\"", got)
	}
	if got := UserID(ctx); got != "" {
		t.Errorf("UserID on a bare context = %q, want \"\"", got)
	}
	if got := ThreadID(ctx); got != "" {
		t.Errorf("ThreadID on a bare context = %q, want \"\"", got)
	}
}

func TestGettersIgnoreAForeignValueOfTheWrongType(t *testing.T) {
	// The keys are unexported empty structs, so nothing outside this package
	// can set them. The type assertion in each getter is the second line of
	// defence: a value of another type must read as "", not panic.
	type otherKey struct{}
	ctx := context.WithValue(context.Background(), otherKey{}, "acme")

	if got := CompanyID(ctx); got != "" {
		t.Errorf("CompanyID = %q, want \"\" — a foreign key must not be read", got)
	}
}

func TestEachSetterTouchesOnlyItsOwnKey(t *testing.T) {
	// Three keys, three distinct struct types. If two shared a key, setting
	// one would silently overwrite the other — a user id read as a company id
	// is a cross-tenant lookup.
	cases := []struct {
		name string
		with func(context.Context) context.Context
		want [3]string // company, user, thread
	}{
		{"company only", func(c context.Context) context.Context { return WithCompanyID(c, "co-1") }, [3]string{"co-1", "", ""}},
		{"user only", func(c context.Context) context.Context { return WithUserID(c, "u-1") }, [3]string{"", "u-1", ""}},
		{"thread only", func(c context.Context) context.Context { return WithThreadID(c, "th-1") }, [3]string{"", "", "th-1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := tc.with(context.Background())
			got := [3]string{CompanyID(ctx), UserID(ctx), ThreadID(ctx)}
			if got != tc.want {
				t.Errorf("(company, user, thread) = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAllThreeCoexist(t *testing.T) {
	ctx := WithThreadID(WithUserID(WithCompanyID(context.Background(), "co-1"), "u-1"), "th-1")

	if got := CompanyID(ctx); got != "co-1" {
		t.Errorf("CompanyID = %q, want co-1", got)
	}
	if got := UserID(ctx); got != "u-1" {
		t.Errorf("UserID = %q, want u-1", got)
	}
	if got := ThreadID(ctx); got != "th-1" {
		t.Errorf("ThreadID = %q, want th-1", got)
	}
}

// The queue consumer derives one context per task from a long-lived parent.
// A derived context must not be able to write back into its parent, or one
// tenant's task would leak its company id into the next one to run.
func TestDerivingDoesNotMutateTheParent(t *testing.T) {
	parent := WithCompanyID(context.Background(), "co-parent")
	child := WithCompanyID(parent, "co-child")

	if got := CompanyID(child); got != "co-child" {
		t.Errorf("child CompanyID = %q, want co-child", got)
	}
	if got := CompanyID(parent); got != "co-parent" {
		t.Errorf("parent CompanyID = %q after deriving a child, want co-parent", got)
	}

	// Two siblings off one parent are independent of each other.
	siblingA := WithCompanyID(parent, "co-a")
	siblingB := WithCompanyID(parent, "co-b")
	if CompanyID(siblingA) != "co-a" || CompanyID(siblingB) != "co-b" {
		t.Errorf("siblings = (%q, %q), want (co-a, co-b)", CompanyID(siblingA), CompanyID(siblingB))
	}
}

// Cancelling or timing out a context — what every tool call does — must not
// drop the tenant identity, because the deferred cleanup paths still read it.
func TestValuesSurviveCancellation(t *testing.T) {
	ctx := WithThreadID(WithCompanyID(context.Background(), "co-1"), "th-1")
	ctx, cancel := context.WithCancel(ctx)
	cancel()

	if got := CompanyID(ctx); got != "co-1" {
		t.Errorf("CompanyID after cancel = %q, want co-1", got)
	}
	if got := ThreadID(ctx); got != "th-1" {
		t.Errorf("ThreadID after cancel = %q, want th-1", got)
	}
}

// An empty string set explicitly reads back as empty, which is what the
// callers' `== ""` rejection depends on: a WhatsApp turn has no user id and
// must not be mistaken for a dashboard one.
func TestExplicitEmptyIsIndistinguishableFromUnset(t *testing.T) {
	ctx := WithUserID(context.Background(), "")
	if got := UserID(ctx); got != "" {
		t.Errorf("UserID = %q, want \"\"", got)
	}
}
