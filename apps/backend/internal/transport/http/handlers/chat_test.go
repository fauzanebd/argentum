package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/domain"
)

// The status codes the dashboard chat answers a rejected agent pick with
// (T-S3). Which picks are rejected is decided in internal/app and tested
// there; this is the other half — that a refusal reaches the browser as 404
// and not as the 400 every enqueue error used to become.

func TestChatFailStatusCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name string
		err  error
		want int
		body string
	}{
		{
			// The acceptance item: another company's agent id, an id that
			// never existed, and a disabled agent all arrive here as one
			// error, and all three have to read the same to the caller.
			name: "a refused agent pick",
			err:  fmt.Errorf("%w: no such agent", domain.ErrNotFound),
			want: http.StatusNotFound,
			body: "no such agent",
		},
		{
			name: "changing a conversation's agent",
			err:  fmt.Errorf("%w: a conversation cannot change agent", domain.ErrInvalidInput),
			want: http.StatusBadRequest,
			body: "cannot change agent",
		},
		{
			// Everything the enqueue path could already fail with keeps the
			// status it had before this ticket. A 500 leaking out of here as a
			// 404 would send an admin looking for a row rather than a log.
			name: "any other enqueue failure",
			err:  errors.New("thread does not belong to company"),
			want: http.StatusBadRequest,
			body: "does not belong",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/chat", nil)

			chatFail(c, tc.err)

			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), tc.body) {
				t.Errorf("body = %s, want it to mention %q", rec.Body, tc.body)
			}
		})
	}
}

// A 404 must not name what it refused. "no such agent" is the whole message
// for all three refusals, so a caller cannot tell an id that is real and
// somebody else's from one that is neither.
func TestARefusedPickRevealsNothing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/chat", nil)

	chatFail(c, fmt.Errorf("%w: no such agent", domain.ErrNotFound))

	for _, leak := range []string{"disabled", "company", "enabled"} {
		if strings.Contains(strings.ToLower(rec.Body.String()), leak) {
			t.Errorf("the refusal mentions %q, which distinguishes it from the others: %s", leak, rec.Body)
		}
	}
}
