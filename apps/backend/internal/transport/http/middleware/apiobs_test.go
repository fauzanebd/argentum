package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/transport/http/apierr"
)

type capturingSink struct{ samples []domain.APIRequestSample }

func (s *capturingSink) Record(sample domain.APIRequestSample) {
	s.samples = append(s.samples, sample)
}

// obsRouter builds the real chain order: request id, then the recorder, then
// whatever the test wants to happen underneath it.
func obsRouter(sink APIRequestSink, install func(*gin.Engine)) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestID())
	r.Use(RecordAPIRequests(sink))
	install(r)
	return r
}

func obsDo(t *testing.T, r *gin.Engine, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req, err := http.NewRequest(method, path, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestRecordsTheRoutePatternAndTheKey covers the two identities a sample needs:
// which route (the pattern, never the concrete path) and whose key.
func TestRecordsTheRoutePatternAndTheKey(t *testing.T) {
	sink := &capturingSink{}
	r := obsRouter(sink, func(r *gin.Engine) {
		r.GET("/v1/reports/:id", func(c *gin.Context) {
			// Set below the recorder, exactly as APIKeyAuth does.
			c.Set("company_id", "co-1")
			c.Set(CtxAPIKeyID, "key-9")
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})
	})

	w := obsDo(t, r, http.MethodGet, "/v1/reports/8f14e45f")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if len(sink.samples) != 1 {
		t.Fatalf("recorded %d samples, want 1", len(sink.samples))
	}
	got := sink.samples[0]
	if got.Route != "/v1/reports/:id" {
		t.Errorf("route = %q, want the pattern — a concrete path makes the label set unbounded", got.Route)
	}
	if got.CompanyID != "co-1" || got.APIKeyID != "key-9" {
		t.Errorf("attributed to %q/%q, want co-1/key-9", got.CompanyID, got.APIKeyID)
	}
	if got.Status != http.StatusOK {
		t.Errorf("status = %d, want 200", got.Status)
	}
	if got.At.IsZero() || got.Latency < 0 {
		t.Errorf("timing not recorded: at=%v latency=%v", got.At, got.Latency)
	}
}

// TestRecordedRequestIDIsTheOneTheCallerGot is the ticket's acceptance item, at
// the level where it can actually be proven: the id in the record and the id in
// the response header are the same string.
func TestRecordedRequestIDIsTheOneTheCallerGot(t *testing.T) {
	sink := &capturingSink{}
	r := obsRouter(sink, func(r *gin.Engine) {
		r.GET("/v1/me", func(c *gin.Context) {
			c.Set(CtxAPIKeyID, "key-1")
			c.Set("company_id", "co-1")
			apierr.Abort(c, apierr.TypePermission, "insufficient_scope", "no")
		})
	})

	w := obsDo(t, r, http.MethodGet, "/v1/me")
	header := w.Header().Get("X-Request-Id")
	if header == "" {
		t.Fatal("no X-Request-Id on the response")
	}
	if len(sink.samples) != 1 {
		t.Fatalf("recorded %d samples, want 1", len(sink.samples))
	}
	if sink.samples[0].RequestID != header {
		t.Errorf("recorded %q, caller received %q", sink.samples[0].RequestID, header)
	}
}

// TestRecordsTheErrorEnvelopesCode is why apierr stamps the context: by the time
// this middleware runs, the body is bytes.
func TestRecordsTheErrorEnvelopesCode(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler gin.HandlerFunc
		status  int
		code    string
		errType string
	}{
		{
			name: "abort",
			handler: func(c *gin.Context) {
				apierr.Abort(c, apierr.TypeRateLimit, "rate_limit_exceeded", "slow down")
			},
			status:  http.StatusTooManyRequests,
			code:    "rate_limit_exceeded",
			errType: "rate_limit",
		},
		{
			name: "explicit status",
			handler: func(c *gin.Context) {
				apierr.AbortStatus(c, http.StatusServiceUnavailable, apierr.TypeServer,
					"api_disabled", "off", "")
			},
			status:  http.StatusServiceUnavailable,
			code:    "api_disabled",
			errType: "server",
		},
		{
			// The idempotency middleware's 409 composes its body by hand around
			// a Detail, and it would be the one failure with no code if only
			// Abort stamped the context.
			name: "hand-composed body",
			handler: func(c *gin.Context) {
				detail := apierr.NewDetail(c, apierr.TypeInvalidRequest, "request_in_flight", "wait")
				c.JSON(http.StatusConflict, gin.H{"error": detail, "in_flight": gin.H{}})
			},
			status:  http.StatusConflict,
			code:    "request_in_flight",
			errType: "invalid_request",
		},
		{
			// A handler that writes its own body records the status and no code.
			// Recording an invented one would be worse.
			name: "no envelope",
			handler: func(c *gin.Context) {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "boom"})
			},
			status:  http.StatusInternalServerError,
			code:    "",
			errType: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &capturingSink{}
			r := obsRouter(sink, func(r *gin.Engine) { r.GET("/v1/me", tc.handler) })

			if w := obsDo(t, r, http.MethodGet, "/v1/me"); w.Code != tc.status {
				t.Fatalf("status = %d, want %d", w.Code, tc.status)
			}
			if len(sink.samples) != 1 {
				t.Fatalf("recorded %d samples, want 1", len(sink.samples))
			}
			got := sink.samples[0]
			if got.Status != tc.status {
				t.Errorf("recorded status %d, want %d", got.Status, tc.status)
			}
			if got.ErrorCode != tc.code {
				t.Errorf("recorded code %q, want %q", got.ErrorCode, tc.code)
			}
			if got.ErrorType != tc.errType {
				t.Errorf("recorded type %q, want %q", got.ErrorType, tc.errType)
			}
		})
	}
}

// TestRecordsAnUnmatchedRoute: a 404 has no pattern, and the recorder must not
// invent one from the URL somebody guessed.
func TestRecordsAnUnmatchedRoute(t *testing.T) {
	sink := &capturingSink{}
	r := obsRouter(sink, func(r *gin.Engine) { r.GET("/v1/me", func(*gin.Context) {}) })

	if w := obsDo(t, r, http.MethodGet, "/v1/nope"); w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if len(sink.samples) != 1 {
		t.Fatalf("recorded %d samples, want 1", len(sink.samples))
	}
	if got := sink.samples[0].Route; got != "" {
		t.Errorf("route = %q, want empty — the concrete path must not become a label", got)
	}
}

// TestNilSinkIsAPassThrough keeps the wiring free of a conditional install.
func TestNilSinkIsAPassThrough(t *testing.T) {
	r := obsRouter(nil, func(r *gin.Engine) {
		r.GET("/v1/me", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	})
	if w := obsDo(t, r, http.MethodGet, "/v1/me"); w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}
