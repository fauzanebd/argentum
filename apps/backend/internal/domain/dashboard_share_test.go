package domain

import (
	"testing"
	"time"
)

// Pinned parameters are locked, never merged. Merging is the obvious
// implementation and it is the one that turns every declared filter into a
// dimension a stranger may enumerate: a link to one region becomes a link to
// all of them by editing the query string.
func TestLockedParamsCannotBeOverriddenByTheVisitor(t *testing.T) {
	sh := &DashboardShare{
		LockedParams: map[string]string{"region": "Jakarta"},
		AllowFilters: true, // even at the permissive end
	}
	got := sh.EffectiveParams(map[string]string{"region": "Surabaya"})
	if got["region"] != "Jakarta" {
		t.Errorf("region = %q, want Jakarta — a pinned filter must survive the query string", got["region"])
	}
}

func TestFiltersAreIgnoredEntirelyWhenNotAllowed(t *testing.T) {
	sh := &DashboardShare{
		LockedParams: map[string]string{"region": "Jakarta"},
		AllowFilters: false,
	}
	got := sh.EffectiveParams(map[string]string{"region": "Surabaya", "quarter": "Q1"})
	if len(got) != 1 || got["region"] != "Jakarta" {
		t.Errorf("got %v, want only the pinned region — a share that does not allow filters ignores the request", got)
	}
}

// An unpinned filter may move when the share says so; that is the feature.
func TestAnUnpinnedFilterMovesWhenFiltersAreAllowed(t *testing.T) {
	sh := &DashboardShare{
		LockedParams: map[string]string{"region": "Jakarta"},
		AllowFilters: true,
	}
	got := sh.EffectiveParams(map[string]string{"quarter": "Q1"})
	if got["quarter"] != "Q1" {
		t.Errorf("quarter = %q, want Q1", got["quarter"])
	}
	if got["region"] != "Jakarta" {
		t.Errorf("region = %q, want the pin to hold alongside", got["region"])
	}
}

func TestEffectiveParamsOnAShareWithNoPinsAndNoFilters(t *testing.T) {
	sh := &DashboardShare{}
	if got := sh.EffectiveParams(map[string]string{"region": "Surabaya"}); len(got) != 0 {
		t.Errorf("got %v, want nothing — the dashboard's own defaults apply", got)
	}
}

// Expiry and revocation are separate decisions and a share needs both: one
// bounds the link nobody remembers, the other is the button pressed at 11pm.
func TestLiveRequiresBothUnexpiredAndUnrevoked(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	revoked := now.Add(-time.Hour)

	cases := []struct {
		name string
		sh   *DashboardShare
		want bool
	}{
		{"live", &DashboardShare{ExpiresAt: now.Add(time.Hour)}, true},
		{"expired", &DashboardShare{ExpiresAt: now.Add(-time.Hour)}, false},
		{"revoked", &DashboardShare{ExpiresAt: now.Add(time.Hour), RevokedAt: &revoked}, false},
		{"expired and revoked", &DashboardShare{ExpiresAt: now.Add(-time.Hour), RevokedAt: &revoked}, false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.sh.Live(now); got != tc.want {
				t.Errorf("Live = %v, want %v", got, tc.want)
			}
		})
	}
}

// Expiry is exclusive at the boundary: a share is not live at the instant it
// expires. The alternative reads as "live for one more nanosecond", which is
// not a property anybody wants to defend in a review.
func TestLiveIsFalseAtTheExpiryInstant(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	if (&DashboardShare{ExpiresAt: now}).Live(now) {
		t.Error("a share must not be live at the exact moment it expires")
	}
}

func TestRequiresPassword(t *testing.T) {
	if (&DashboardShare{}).RequiresPassword() {
		t.Error("a share with no password hash must not demand one")
	}
	if !(&DashboardShare{PasswordHash: "$argon2id$..."}).RequiresPassword() {
		t.Error("a share with a password hash must demand one")
	}
}
