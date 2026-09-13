package app

import (
	"context"
	"errors"
	"testing"
)

// T-Z13, the owner's decision on access-grants §13d: a link to a document made in
// a conversation with a restricted agent in it is not minted, and does not open,
// while that agent is restricted — whoever minted it, whenever.

// restrictedThreads is ShareConversations over a set of conversations that hold a
// restricted agent.
type restrictedThreads struct {
	restricted map[string]bool
	err        error
	asked      []string
}

func (r *restrictedThreads) OpenToEveryone(_ context.Context, _, threadID string) (bool, error) {
	r.asked = append(r.asked, threadID)
	if r.err != nil {
		return false, r.err
	}
	return !r.restricted[threadID], nil
}

// sharedFromHR is the share fixture with its document made in HR's conversation.
func sharedFromHR(t *testing.T) (*shareFixture, *restrictedThreads) {
	t.Helper()
	f := newShareFixture(t)
	f.docs.docs[0].ThreadID = "th-hr"
	conv := &restrictedThreads{restricted: map[string]bool{}}
	f.svc.WithConversations(conv)
	return f, conv
}

func TestADocumentFromARestrictedConversationCannotBeShared(t *testing.T) {
	f, conv := sharedFromHR(t)
	conv.restricted["th-hr"] = true

	_, err := f.svc.Create(context.Background(), "co-1", "u-admin", "doc-1", 0)
	if !errors.Is(err, ErrDocumentRestricted) {
		t.Fatalf("Create = %v, want ErrDocumentRestricted", err)
	}
	if len(f.shares.rows) != 0 {
		t.Errorf("a refused mint wrote %d link(s)", len(f.shares.rows))
	}
}

// Restricting closes the door on the next open and revokes nothing; re-opening
// the agent opens it again. The closed open is not counted or audited as a view.
func TestALinkIsShutWhileItsConversationIsRestrictedAndOpensAgainAfter(t *testing.T) {
	f, conv := sharedFromHR(t)
	ctx := context.Background()
	created, err := f.svc.Create(ctx, "co-1", "u-admin", "doc-1", 0)
	if err != nil {
		t.Fatalf("Create while open: %v", err)
	}

	conv.restricted["th-hr"] = true
	if _, err := f.svc.Resolve(ctx, created.Token, "203.0.113.9", "curl"); !errors.Is(err, ErrShareGone) {
		t.Fatalf("Resolve while restricted = %v, want ErrShareGone", err)
	}
	if f.shares.viewsOf[created.Share.ID] != 0 || len(f.audit.rows) != 0 {
		t.Errorf("a shut link counted %d view(s) and wrote %d audit row(s)", f.shares.viewsOf[created.Share.ID], len(f.audit.rows))
	}
	if f.shares.rows[0].RevokedAt != nil {
		t.Error("restricting revoked the link; it should only hold it shut")
	}
	if paused, err := f.svc.Paused(ctx, "co-1", "doc-1"); err != nil || !paused {
		t.Errorf("Paused = %v, %v while restricted, want true", paused, err)
	}

	delete(conv.restricted, "th-hr")
	if _, err := f.svc.Resolve(ctx, created.Token, "203.0.113.9", "curl"); err != nil {
		t.Fatalf("Resolve after re-opening = %v, want the page", err)
	}
	if paused, err := f.svc.Paused(ctx, "co-1", "doc-1"); err != nil || paused {
		t.Errorf("Paused = %v, %v after re-opening, want false", paused, err)
	}
}

// A document with no conversation — the render door's — has nothing to inherit,
// and nothing is asked.
func TestADocumentWithNoConversationIsSharedAsBefore(t *testing.T) {
	f := newShareFixture(t)
	conv := &restrictedThreads{restricted: map[string]bool{"": true}}
	f.svc.WithConversations(conv)
	ctx := context.Background()

	created, err := f.svc.Create(ctx, "co-1", "u-admin", "doc-1", 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := f.svc.Resolve(ctx, created.Token, "", ""); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(conv.asked) != 0 {
		t.Errorf("asked about %v for a document with no conversation", conv.asked)
	}
}

// A check that cannot be made mints nothing — a retry, not a refusal — and opens
// nothing: a stranger holding a link is not somebody to tell "try again".
func TestAShareCheckThatFailsMintsNothingAndOpensNothing(t *testing.T) {
	f, conv := sharedFromHR(t)
	ctx := context.Background()
	created, err := f.svc.Create(ctx, "co-1", "u-admin", "doc-1", 0)
	if err != nil {
		t.Fatalf("Create while the check works: %v", err)
	}

	conv.err = errors.New("connection refused")
	if _, err := f.svc.Create(ctx, "co-1", "u-admin", "doc-1", 0); !errors.Is(err, ErrShareCheckFailed) || errors.Is(err, ErrDocumentRestricted) {
		t.Errorf("Create with the check down = %v, want ErrShareCheckFailed and not a refusal", err)
	}
	if len(f.shares.rows) != 1 {
		t.Errorf("%d links after a failed check, want only the first", len(f.shares.rows))
	}
	if _, err := f.svc.Resolve(ctx, created.Token, "", ""); !errors.Is(err, ErrShareGone) {
		t.Errorf("Resolve with the check down = %v, want ErrShareGone", err)
	}
	if _, err := f.svc.Paused(ctx, "co-1", "doc-1"); err == nil {
		t.Error("Paused with the check down answered, want an error")
	}
}
