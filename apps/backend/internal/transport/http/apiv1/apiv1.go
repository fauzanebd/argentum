// Package apiv1 holds the response shapes every `/v1` route shares (T-A1).
//
// It is a separate package from `apierr` because the two answer different
// questions and have different rules: `apierr` is the failure envelope and is
// allowed to grow codes, while what is here is the success side and is
// **additive only**. A field removed or renamed in this package is a breaking
// change to a public contract, and the answer to needing one is `/v2`.
package apiv1

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Version is the API contract version, reported by `GET /v1/me`. It is a date
// rather than a number because that is what a support conversation can act
// on: "you are on 2026-07-30" locates a deploy, where "v1" is already in the
// path and says nothing.
const Version = "2026-07-30"

// Page is the envelope for every list `/v1` returns.
//
// **Never an offset.** Rows arrive while a caller pages: with `?offset=100` a
// row inserted during the walk shifts everything down one and the caller sees
// an item twice, or misses one entirely. The cursor names the last row the
// caller actually saw, so the next page starts after that row and not after a
// position that has moved.
type Page[T any] struct {
	Data []T `json:"data"`
	// HasMore is not len(Data) == limit. A caller cannot tell a full last
	// page from a full middle one, and asking for one more page to find out
	// is a request nobody should have to make.
	HasMore    bool   `json:"has_more"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// NewPage builds the envelope. A nil slice is normalised to an empty one so
// the JSON carries `"data": []` rather than `"data": null` — a client that
// iterates the response should not need a null check to read zero rows.
func NewPage[T any](items []T, hasMore bool, nextCursor string) Page[T] {
	if items == nil {
		items = []T{}
	}
	return Page[T]{Data: items, HasMore: hasMore, NextCursor: nextCursor}
}

// ErrBadCursor is returned by DecodeCursor for anything it cannot read.
// Handlers map it to `invalid_request` / `invalid_cursor` with `param:
// "cursor"` — a caller that hand-built a cursor should be told so, not handed
// page one.
var ErrBadCursor = errors.New("malformed cursor")

// EncodeCursor renders the (created_at, id) of the last row on a page.
//
// The pair, not the id alone: rows are ordered by time and two rows can share
// a microsecond, so a keyset predicate needs both halves to be a total order.
// Microseconds because that is Postgres's own `timestamptz` resolution —
// nanoseconds would round-trip to a value the database cannot compare against.
//
// It is base64 of an internal encoding rather than a readable pair on
// purpose: a cursor is a token to hand back, not a documented structure to
// construct, and making it opaque is what keeps it changeable.
func EncodeCursor(createdAt time.Time, id string) string {
	raw := strconv.FormatInt(createdAt.UTC().UnixMicro(), 10) + ":" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeCursor reads a cursor back. The returned time is UTC.
func DecodeCursor(cursor string) (time.Time, string, error) {
	if cursor == "" {
		return time.Time{}, "", ErrBadCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", ErrBadCursor
	}
	micros, id, ok := strings.Cut(string(raw), ":")
	if !ok || id == "" {
		return time.Time{}, "", ErrBadCursor
	}
	us, err := strconv.ParseInt(micros, 10, 64)
	if err != nil {
		return time.Time{}, "", ErrBadCursor
	}
	return time.UnixMicro(us).UTC(), id, nil
}
