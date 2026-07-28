package idempotency

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// storeWithRedis runs against miniredis rather than a hand-written fake: the
// three behaviours that matter here — SET NX losing a race, KeepTTL not
// resetting the clock, and a key expiring — are Redis's semantics, and a fake
// would only assert that this file agrees with itself.
func storeWithRedis(t *testing.T) (*RedisStore, *miniredis.Miniredis) {
	t.Helper()
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewRedisStore(rdb), srv
}

func TestBeginClaimsOnceAndReturnsTheRecordAfter(t *testing.T) {
	store, _ := storeWithRedis(t)
	ctx := context.Background()
	key := Key("co-1", "k-1")

	rec, first, err := store.Begin(ctx, key, "hash-1")
	if err != nil || !first || rec != nil {
		t.Fatalf("first Begin = (%v, %v, %v), want (nil, true, nil)", rec, first, err)
	}

	rec, first, err = store.Begin(ctx, key, "hash-1")
	if err != nil {
		t.Fatalf("second Begin: %v", err)
	}
	if first {
		t.Fatal("the second caller was told it claimed the key")
	}
	if rec == nil || rec.Status != StatusInFlight || rec.BodyHash != "hash-1" {
		t.Fatalf("record = %+v, want an in-flight record carrying the body hash", rec)
	}
}

func TestCompleteMakesTheResultReplayable(t *testing.T) {
	store, _ := storeWithRedis(t)
	ctx := context.Background()
	key := Key("co-1", "k-1")

	if _, _, err := store.Begin(ctx, key, "hash-1"); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := store.Complete(ctx, key, 201, json.RawMessage(`{"report_id":"rep-1"}`)); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	rec, _, err := store.Begin(ctx, key, "hash-1")
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if rec.Status != StatusCompleted || rec.HTTPStatus != 201 {
		t.Fatalf("record = %+v, want completed with the original status", rec)
	}
	if string(rec.Result) != `{"report_id":"rep-1"}` {
		t.Errorf("result = %s", rec.Result)
	}
}

// The TTL is the caller's whole retry window. An update that reset it would
// extend a key every time the record was touched, so a chatty request could
// hold one indefinitely.
func TestUpdatingARecordDoesNotResetItsTTL(t *testing.T) {
	store, srv := storeWithRedis(t)
	ctx := context.Background()
	key := Key("co-1", "k-1")

	if _, _, err := store.Begin(ctx, key, "hash-1"); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	srv.FastForward(2 * time.Hour)

	if err := store.Complete(ctx, key, 200, json.RawMessage(`{"id":"1"}`)); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if ttl := srv.TTL(key); ttl > TTL-2*time.Hour+time.Minute {
		t.Errorf("TTL = %s after two hours, want it still counting down from the original %s", ttl, TTL)
	}
}

func TestDiscardForgetsTheKey(t *testing.T) {
	store, _ := storeWithRedis(t)
	ctx := context.Background()
	key := Key("co-1", "k-1")

	if _, _, err := store.Begin(ctx, key, "hash-1"); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := store.Discard(ctx, key); err != nil {
		t.Fatalf("Discard: %v", err)
	}

	_, first, err := store.Begin(ctx, key, "hash-1")
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if !first {
		t.Error("a discarded key still refuses a retry")
	}
}

// A record whose result does not fit is stored without it rather than not
// stored at all: the property being bought is "this did not run twice", and
// that survives losing the ids.
func TestAnOversizedResultIsDroppedAndTheRecordSurvives(t *testing.T) {
	store, _ := storeWithRedis(t)
	ctx := context.Background()
	key := Key("co-1", "k-1")

	if _, _, err := store.Begin(ctx, key, "hash-1"); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	huge, err := json.Marshal(map[string]string{"blob": strings.Repeat("x", MaxRecordBytes)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := store.Complete(ctx, key, 200, huge); err == nil {
		t.Fatal("Complete accepted an oversized result silently")
	}

	rec, _, err := store.Begin(ctx, key, "hash-1")
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if rec == nil || rec.Status != StatusCompleted {
		t.Fatalf("record = %+v, want a completed record with no result", rec)
	}
	if len(rec.Result) != 0 {
		t.Errorf("result = %s, want it dropped", rec.Result)
	}
}

// Two tenants that both send `Idempotency-Key: 1` must not collide, and one
// must not be able to reach the other's record by guessing.
func TestKeysAreNamespacedByCompany(t *testing.T) {
	store, _ := storeWithRedis(t)
	ctx := context.Background()

	if _, _, err := store.Begin(ctx, Key("co-1", "1"), "hash-1"); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	_, first, err := store.Begin(ctx, Key("co-2", "1"), "hash-2")
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if !first {
		t.Error("one tenant's idempotency key blocked another tenant's")
	}
}

func TestNilRedisYieldsNoStore(t *testing.T) {
	if got := NewRedisStore(nil); got != nil {
		t.Error("NewRedisStore(nil) returned a store that would panic on first use")
	}
}
