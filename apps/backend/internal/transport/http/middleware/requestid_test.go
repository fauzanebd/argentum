package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// requestIDRouter echoes what the middleware left behind, so a test can
// assert on the context value rather than only on the header.
func requestIDRouter() *gin.Engine {
	r := gin.New()
	r.Use(RequestID())
	r.GET("/probe", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"gin": c.GetString(CtxRequestID),
			"ctx": tenantctx.RequestID(c.Request.Context()),
		})
	})
	return r
}

func requestIDOf(t *testing.T, sent string) (header string, body map[string]string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	if sent != "" {
		req.Header.Set("X-Request-Id", sent)
	}
	w := httptest.NewRecorder()
	requestIDRouter().ServeHTTP(w, req)

	out := map[string]string{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode body %q: %v", w.Body.String(), err)
	}
	return w.Header().Get("X-Request-Id"), out
}

func TestRequestIDIsMintedWhenTheCallerSendsNone(t *testing.T) {
	header, body := requestIDOf(t, "")

	if !strings.HasPrefix(header, "req_") {
		t.Errorf("X-Request-Id = %q, want a req_-prefixed id", header)
	}
	// The three have to agree or the id in a support ticket resolves to
	// nothing: the caller reads the header, the log line reads the Gin
	// context, and the audit row reads the request context.
	if body["gin"] != header || body["ctx"] != header {
		t.Errorf("header %q, gin %q, ctx %q — all three must carry the same id", header, body["gin"], body["ctx"])
	}
}

func TestRequestIDIsUniquePerRequest(t *testing.T) {
	first, _ := requestIDOf(t, "")
	second, _ := requestIDOf(t, "")
	if first == second {
		t.Errorf("two requests got the same id %q", first)
	}
}

func TestRequestIDAcceptsACallersOwnID(t *testing.T) {
	// The point of accepting one: an integrator correlating their logs with
	// ours sends the id they already have.
	header, body := requestIDOf(t, "trace-abc.123:xyz_9")

	if header != "trace-abc.123:xyz_9" {
		t.Errorf("X-Request-Id = %q, want the caller's own id echoed", header)
	}
	if body["ctx"] != header {
		t.Errorf("context id = %q, want %q", body["ctx"], header)
	}
}

func TestRequestIDReplacesAnythingUnsafeToLog(t *testing.T) {
	// This value ends up in a log line and in agent_actions.request_id. A
	// newline in it is a forged second log event; a space or a quote is a
	// field boundary somebody's log parser will believe.
	cases := map[string]string{
		"newline":     "abc\ndef",
		"carriage":    "abc\rdef",
		"space":       "abc def",
		"quote":       `abc"def`,
		"ansi escape": "abc\x1b[31mdef",
		"too long":    strings.Repeat("a", maxCallerRequestIDLen+1),
		"null byte":   "abc\x00def",
	}
	for name, sent := range cases {
		t.Run(name, func(t *testing.T) {
			header, _ := requestIDOf(t, sent)
			if header == sent {
				t.Errorf("X-Request-Id = %q — an unsafe caller id was echoed verbatim", header)
			}
			if !strings.HasPrefix(header, "req_") {
				t.Errorf("X-Request-Id = %q, want a minted req_ id instead", header)
			}
		})
	}
}
