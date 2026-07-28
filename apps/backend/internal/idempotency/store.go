// Package idempotency records what a `/v1` write already did, so a retry of
// it does the same thing again instead of a second thing (T-A1).
//
// The design decision worth knowing before reading the code: **a record never
// holds the response payload.** The obvious implementation caches the bytes a
// handler wrote and replays them, and it is wrong for this API in two
// different ways. `POST /v1/reports/render` with `Accept: application/pdf`
// answers with megabytes, and keeping that per key for 24 hours turns Redis
// into a document store nobody sized for it. And a streamed answer has no
// bytes to keep at all — `POST /v1/chat` over SSE is a sequence of events
// whose replay means re-attaching to the turn, not re-sending a buffer.
//
// So a record holds the *logical* result: the ids and the status a handler
// declares (`{"report_id":"…","status":"completed"}`). Replaying re-derives
// the response from those — re-reading object storage and re-presigning a
// fresh URL, or re-attaching to a thread — which is also the only way a
// replayed download link is still valid an hour later.
package idempotency

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// TTL is how long a key is remembered. 24 hours: long enough to cover a
// client's whole retry policy including an operator manually re-running a
// failed nightly job, short enough that a key is not a permanent allocation.
const TTL = 24 * time.Hour

// MaxRecordBytes caps one stored record. It is a hard budget rather than a
// guideline — the acceptance criterion for this ticket is that no record
// exceeds 4 KiB *including* after a 10 MB render, which is only true if
// something refuses to write the oversized one.
const MaxRecordBytes = 4 * 1024

// Status is where a recorded request got to.
type Status string

const (
	// StatusInFlight — the original request is still running. A second
	// request under the same key is a client timeout plus a retry, and must
	// not start a second turn.
	StatusInFlight Status = "in_flight"
	// StatusCompleted — the original finished and its logical result is here.
	StatusCompleted Status = "completed"
)

// Record is what one key remembers.
type Record struct {
	// BodyHash is the sha256 of the request body the key was first used
	// with. A second request under the same key with different content is a
	// broken retry loop, and catching it is the difference between billing
	// once and billing twice for two different things.
	BodyHash string `json:"body_hash"`
	Status   Status `json:"status"`
	// Result is the handler's declared logical result — ids and status, never
	// a payload. Present while in flight only for what the handler could
	// declare early, which is what lets a 409 name the thread the caller is
	// already waiting on.
	Result json.RawMessage `json:"result,omitempty"`
	// HTTPStatus is the status the original response carried, so a replay
	// answers 200 where the original said 200 and 202 where it said 202.
	HTTPStatus int       `json:"http_status,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// ErrTooLarge is returned when a declared result would push a record past
// MaxRecordBytes. Callers log it and carry on: the request itself succeeded,
// and refusing it because its bookkeeping did not fit would be the wrong
// trade.
var ErrTooLarge = errors.New("idempotency record exceeds the size cap")

// Store is the contract the middleware needs. Declared here rather than in
// the middleware package because the Redis implementation is the only one
// that will ever exist and a test double is simpler than a second package.
type Store interface {
	// Begin claims key for a request whose body hashes to bodyHash. It
	// returns the existing record when one is already there, and (nil, true)
	// when this caller is the first.
	Begin(ctx context.Context, key, bodyHash string) (*Record, bool, error)
	// Progress attaches what the handler knows so far to a still-in-flight
	// record.
	Progress(ctx context.Context, key string, result json.RawMessage) error
	// Complete marks the record done with the logical result to replay.
	Complete(ctx context.Context, key string, httpStatus int, result json.RawMessage) error
	// Discard forgets the key. Used when the original request failed: a 500
	// that kept its key would make the retry — the exact thing a client is
	// supposed to do next — impossible for 24 hours.
	Discard(ctx context.Context, key string) error
}

// RedisStore is the Store backed by the same Redis the rate limiter and the
// event bus use.
type RedisStore struct{ rdb *redis.Client }

// NewRedisStore returns a store, or nil when there is no Redis. Nil is a
// legitimate configuration — the middleware degrades to no idempotency rather
// than refusing every write — and callers test for it the way they already
// test the rate limiter.
func NewRedisStore(rdb *redis.Client) *RedisStore {
	if rdb == nil {
		return nil
	}
	return &RedisStore{rdb: rdb}
}

// Key builds the namespaced Redis key. The company is in it because an
// idempotency key is chosen by the caller: two tenants both sending
// `Idempotency-Key: 1` must not collide, and a tenant must not be able to
// probe another's keys by guessing.
func Key(companyID, key string) string { return "idem:" + companyID + ":" + key }

func (s *RedisStore) Begin(ctx context.Context, key, bodyHash string) (*Record, bool, error) {
	rec := &Record{BodyHash: bodyHash, Status: StatusInFlight, CreatedAt: time.Now().UTC()}
	raw, err := json.Marshal(rec)
	if err != nil {
		return nil, false, err
	}
	// SetArgs with Mode "NX" rather than SetNX: the latter is deprecated in
	// go-redis v9, and the linter this repo runs in CI (T-02) fails on it.
	// A key that already exists comes back as redis.Nil, not as an error.
	err = s.rdb.SetArgs(ctx, key, raw, redis.SetArgs{Mode: "NX", TTL: TTL}).Err()
	switch {
	case err == nil:
		return nil, true, nil
	case !errors.Is(err, redis.Nil):
		return nil, false, err
	}

	existing, err := s.get(ctx, key)
	if err != nil {
		return nil, false, err
	}
	if existing == nil {
		// The record expired between the SetNX and the read. Treating this as
		// "first caller" is the safe reading: the original is at least 24
		// hours old, so nothing is in flight to duplicate.
		return nil, true, nil
	}
	return existing, false, nil
}

func (s *RedisStore) Progress(ctx context.Context, key string, result json.RawMessage) error {
	return s.update(ctx, key, func(rec *Record) { rec.Result = result })
}

func (s *RedisStore) Complete(ctx context.Context, key string, httpStatus int, result json.RawMessage) error {
	return s.update(ctx, key, func(rec *Record) {
		rec.Status = StatusCompleted
		rec.HTTPStatus = httpStatus
		rec.Result = result
	})
}

func (s *RedisStore) Discard(ctx context.Context, key string) error {
	return s.rdb.Del(ctx, key).Err()
}

// update reads, mutates and writes back, preserving the key's remaining TTL.
//
// It is deliberately not a transaction. The only concurrent writer is a
// second request under the same key, and that one is refused with a 409
// before it reaches here — so the lost-update race this would otherwise have
// requires a client to defeat the check that exists to stop it.
func (s *RedisStore) update(ctx context.Context, key string, mutate func(*Record)) error {
	rec, err := s.get(ctx, key)
	if err != nil {
		return err
	}
	if rec == nil {
		return nil
	}
	mutate(rec)

	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if len(raw) > MaxRecordBytes {
		// Keep the record, drop what did not fit. A record whose result is
		// missing still stops a duplicate turn, which is the property the
		// caller is actually paying for; a record that failed to write does
		// not.
		rec.Result = nil
		if raw, err = json.Marshal(rec); err != nil {
			return err
		}
		if err := s.rdb.Set(ctx, key, raw, redis.KeepTTL).Err(); err != nil {
			return err
		}
		return ErrTooLarge
	}
	return s.rdb.Set(ctx, key, raw, redis.KeepTTL).Err()
}

func (s *RedisStore) get(ctx context.Context, key string) (*Record, error) {
	raw, err := s.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rec Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		// A record we cannot read is a record we cannot honour. Reporting it
		// as absent lets the request proceed; the alternative is a key that
		// is permanently poisoned for 24 hours.
		return nil, nil
	}
	return &rec, nil
}
