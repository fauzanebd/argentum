package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

// The T-P1 gate found this: an upload over the cap answered 400 "expected a PDF
// in the file field", because MaxBytesReader cuts the body mid-part and the
// multipart reader reports a parse failure rather than a size. The client is
// told its request was malformed when the request was fine and the file was too
// big.
//
// Both arms matter and only one of them is the typed error. `mime/multipart`
// reads through its own buffered reader and hands back a plain errors.New, so
// the string arm is what actually fires on a real oversized upload — which is
// exactly the case this exists for.
func TestIsBodyTooLarge(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"the typed error", &http.MaxBytesError{Limit: 1 << 20}, true},
		{"wrapped typed error", fmt.Errorf("parse form: %w", &http.MaxBytesError{Limit: 1 << 20}), true},
		{"multipart's flattened string", errors.New("multipart: NextPart: http: request body too large"), true},
		{"a genuinely malformed request", errors.New("request Content-Type isn't multipart/form-data"), false},
		{"a missing field", http.ErrMissingFile, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBodyTooLarge(tc.err); got != tc.want {
				t.Errorf("isBodyTooLarge(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
