package main

import (
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// RIZ-97 R3 / AC7: choosing the workspace's chat model is admin, reading it is
// member. TestGatedRoutesRejectMembers sends the member's 403 through the real
// router for every admin route; this pins the classification itself, so a
// later loosening is a diff to this file rather than a quiet table edit.
func TestChatModelPolicy(t *testing.T) {
	for key, want := range map[string]domain.Role{
		"PUT /api/config/chat-model": domain.RoleAdmin,
		"GET /api/config/models":     domain.RoleMember,
	} {
		if got := apiPolicy[key]; got != want {
			t.Errorf("%s is %q, want %q", key, got, want)
		}
	}
}
