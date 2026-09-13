package app

import (
	"context"
	"testing"
)

// T-Z13: a public link asks whether a conversation is open to everyone — every
// agent in it open — as nobody. A grant somebody holds changes nothing: a link
// has no person to hold one.
func TestAConversationIsOpenToEveryoneOnlyWhenEveryAgentInItIs(t *testing.T) {
	ca, _, grants, _ := conversationsFixture("ag-fin")
	grants.restricted["ag-hr"] = true
	grants.granted["u-1/ag-hr"] = true
	ctx := context.Background()

	cases := map[string]bool{
		"th-fin":      true,
		"th-hr":       false,
		"th-room":     false, // HR in the room
		"th-said":     false, // HR answered, then left
		"th-unpinned": true,  // judged by the default, Finance
		"th-ghost":    true,  // an agent that no longer exists restricts nothing
		"th-missing":  true,  // not this company's conversation: nothing to inherit
		"":            true,
	}
	for thread, want := range cases {
		got, err := ca.OpenToEveryone(ctx, "co-1", thread)
		if err != nil {
			t.Fatalf("OpenToEveryone(%q): %v", thread, err)
		}
		if got != want {
			t.Errorf("OpenToEveryone(%q) = %v, want %v", thread, got, want)
		}
	}
	// The person granted HR may read th-hr; the link still may not.
	if ok, err := ca.MayRead(ctx, "co-1", "u-1", "th-hr"); err != nil || !ok {
		t.Fatalf("MayRead as the granted person = %v, %v; the fixture is wrong", ok, err)
	}

	restrictedDefault, _, g2, _ := conversationsFixture("ag-hr")
	g2.restricted["ag-hr"] = true
	if open, err := restrictedDefault.OpenToEveryone(ctx, "co-1", "th-unpinned"); err != nil || open {
		t.Errorf("an unattributed conversation under a restricted default: %v, %v, want shut", open, err)
	}

	var unwired *ConversationAccess
	if open, err := unwired.OpenToEveryone(ctx, "co-1", "th-hr"); err != nil || !open {
		t.Errorf("an unwired check = %v, %v, want open, as before roadmap 12", open, err)
	}
}
