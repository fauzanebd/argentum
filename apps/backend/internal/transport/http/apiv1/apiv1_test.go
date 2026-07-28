package apiv1

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCursorRoundTrips(t *testing.T) {
	// A microsecond-precision timestamp, because that is what Postgres
	// stores: a cursor that round-trips through this package but not through
	// the database is a cursor that skips or repeats a row.
	when := time.Date(2026, 7, 28, 11, 4, 5, 123456000, time.UTC)
	cursor := EncodeCursor(when, "01912f0e-6a7b-7c8d-9e0f-112233445566")

	got, id, err := DecodeCursor(cursor)
	if err != nil {
		t.Fatalf("DecodeCursor: %v", err)
	}
	if !got.Equal(when) {
		t.Errorf("time = %s, want %s", got, when)
	}
	if id != "01912f0e-6a7b-7c8d-9e0f-112233445566" {
		t.Errorf("id = %q", id)
	}
}

func TestCursorNormalisesToUTC(t *testing.T) {
	jakarta := time.FixedZone("WIB", 7*3600)
	when := time.Date(2026, 7, 28, 18, 0, 0, 0, jakarta)

	got, _, err := DecodeCursor(EncodeCursor(when, "id-1"))
	if err != nil {
		t.Fatalf("DecodeCursor: %v", err)
	}
	if !got.Equal(when) {
		t.Errorf("time = %s, want the same instant as %s", got, when)
	}
	if got.Location() != time.UTC {
		t.Errorf("location = %s, want UTC", got.Location())
	}
}

func TestMalformedCursorsAreRejected(t *testing.T) {
	// A caller hand-building a cursor should be told so, not handed page one:
	// silently restarting a walk is how a paging loop never terminates.
	cases := map[string]string{
		"empty":          "",
		"not base64":     "!!!!",
		"no separator":   encode("1753700000000000"),
		"empty id":       encode("1753700000000000:"),
		"time not a int": encode("yesterday:id-1"),
	}
	for name, cursor := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := DecodeCursor(cursor); !errors.Is(err, ErrBadCursor) {
				t.Errorf("err = %v, want ErrBadCursor", err)
			}
		})
	}
}

// An id containing the separator must survive: ids are opaque, and a cursor
// that truncated one would page from the wrong row.
func TestCursorKeepsAnIDContainingTheSeparator(t *testing.T) {
	_, id, err := DecodeCursor(EncodeCursor(time.Unix(0, 0), "a:b:c"))
	if err != nil {
		t.Fatalf("DecodeCursor: %v", err)
	}
	if id != "a:b:c" {
		t.Errorf("id = %q, want a:b:c", id)
	}
}

// A cursor is a token to hand back, not a structure to construct. Base64 is
// what makes that obvious, and what keeps the encoding changeable.
func TestCursorIsOpaque(t *testing.T) {
	cursor := EncodeCursor(time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC), "id-1")
	if strings.Contains(cursor, ":") || strings.Contains(cursor, "id-1") {
		t.Errorf("cursor %q exposes its internals", cursor)
	}
}

func TestEmptyPageSerialisesAsAnEmptyArray(t *testing.T) {
	raw, err := json.Marshal(NewPage[string](nil, false, ""))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// `"data": null` makes every client add a null check before it can
	// iterate zero rows.
	if got := string(raw); got != `{"data":[],"has_more":false}` {
		t.Errorf("page = %s", got)
	}
}

func TestPageCarriesTheCursorOnlyWhenThereIsMore(t *testing.T) {
	raw, err := json.Marshal(NewPage([]string{"a"}, true, "abc"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(raw); got != `{"data":["a"],"has_more":true,"next_cursor":"abc"}` {
		t.Errorf("page = %s", got)
	}
}

// encode builds a cursor body directly, so a test can plant one this package
// would never have produced.
func encode(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}
