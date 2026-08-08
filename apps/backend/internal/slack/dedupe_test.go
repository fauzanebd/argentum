package slack

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newDeduper(t *testing.T) (*RedisDeduper, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	return NewRedisDeduper(rdb), mr
}

func TestFirstSightClaimsAnEventOnce(t *testing.T) {
	d, _ := newDeduper(t)
	ctx := context.Background()

	first, err := d.FirstSight(ctx, "A123", "Ev123")
	if err != nil {
		t.Fatal(err)
	}
	if !first {
		t.Fatal("the first delivery was not treated as first")
	}

	// This is Slack's retry landing while the first turn is still running.
	again, err := d.FirstSight(ctx, "A123", "Ev123")
	if err != nil {
		t.Fatal(err)
	}
	if again {
		t.Fatal("a redelivery was treated as first — the turn would run twice")
	}
}

func TestEventIDsAreNamespacedByApp(t *testing.T) {
	d, _ := newDeduper(t)
	ctx := context.Background()

	if _, err := d.FirstSight(ctx, "A111", "Ev-same"); err != nil {
		t.Fatal(err)
	}
	first, err := d.FirstSight(ctx, "A222", "Ev-same")
	if err != nil {
		t.Fatal(err)
	}
	if !first {
		t.Fatal("one tenant's event id suppressed another tenant's")
	}
}

func TestTheClaimExpires(t *testing.T) {
	d, mr := newDeduper(t)
	ctx := context.Background()

	if _, err := d.FirstSight(ctx, "A123", "Ev123"); err != nil {
		t.Fatal(err)
	}
	mr.FastForward(DedupeTTL + 1)

	first, err := d.FirstSight(ctx, "A123", "Ev123")
	if err != nil {
		t.Fatal(err)
	}
	if !first {
		t.Fatal("the claim outlived its TTL")
	}
}

// An empty event id is not a claim anyone can hold: treating it as seen would
// drop every event from a payload shape that omits the field.
func TestAnEmptyEventIDIsAlwaysFirst(t *testing.T) {
	d, _ := newDeduper(t)
	for i := range 2 {
		first, err := d.FirstSight(context.Background(), "A123", "")
		if err != nil {
			t.Fatal(err)
		}
		if !first {
			t.Fatalf("call %d: an empty event id was treated as a duplicate", i)
		}
	}
}

// A deployment without Redis still starts, and still answers questions.
func TestNoRedisMeansNoDeduplication(t *testing.T) {
	var d *RedisDeduper = NewRedisDeduper(nil)
	first, err := d.FirstSight(context.Background(), "A123", "Ev123")
	if err != nil {
		t.Fatal(err)
	}
	if !first {
		t.Fatal("a nil deduper must not swallow events")
	}
}
