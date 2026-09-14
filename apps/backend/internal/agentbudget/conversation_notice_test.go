package agentbudget

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// T-N6's use of the ledger: a refused ask is written into the room once per
// agent per person's message, however many times that agent keeps asking.

func noticeLedger(t *testing.T) (*Conversation, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewConversation(rdb, Ceilings{}), mr
}

func TestARefusalIsAnnouncedOncePerAgentPerMessage(t *testing.T) {
	c, _ := noticeLedger(t)
	claim := func(msg, asker string) bool {
		t.Helper()
		first, err := c.ClaimNotice(context.Background(), "co-1", msg, asker)
		if err != nil {
			t.Fatalf("ClaimNotice: %v", err)
		}
		return first
	}

	if !claim("msg-1", "ag-ops") {
		t.Fatal("Ops' first refusal was not the first")
	}
	if claim("msg-1", "ag-ops") {
		t.Error("Ops' second refusal claimed the room again — a looping agent would fill it")
	}
	if !claim("msg-1", "ag-fin") {
		t.Error("Finance's first refusal was swallowed by Ops' — the person would not hear about it")
	}
	if !claim("msg-2", "ag-ops") {
		t.Error("a refusal on the next message was swallowed by the last one's")
	}
}

// The claim lives as long as the ledger it deduplicates refusals for, and is
// never created without an expiry — a depth refusal creates no ledger at all.
func TestANoticeClaimExpiresWithTheLedger(t *testing.T) {
	c, mr := noticeLedger(t)
	if _, err := c.ClaimNotice(context.Background(), "co-1", "msg-1", "ag-ops"); err != nil {
		t.Fatal(err)
	}
	if got, want := mr.TTL(noticeKey("co-1", "msg-1", "ag-ops")), 2*DefaultCeilings().Wall; got != want {
		t.Errorf("claim TTL = %s, want %s", got, want)
	}
	mr.FastForward(2*DefaultCeilings().Wall + time.Second)
	if mr.Exists(noticeKey("co-1", "msg-1", "ag-ops")) {
		t.Error("the claim outlived its ledger's window")
	}
}

func TestANoticeClaimWithoutRedisIsAnError(t *testing.T) {
	for name, c := range map[string]*Conversation{
		"nil ledger":      nil,
		"ledger no redis": NewConversation(nil, Ceilings{}),
	} {
		first, err := c.ClaimNotice(context.Background(), "co-1", "msg-1", "ag-ops")
		if first || !errors.Is(err, ErrLedgerUnavailable) {
			t.Errorf("%s: ClaimNotice = %v, %v; want false and ErrLedgerUnavailable", name, first, err)
		}
	}
}
