package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/crypto"
	"github.com/fauzanebd/argentum/internal/domain"
)

const (
	whTestKeyHex = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	whTestSecret = "8f742231b10e8888abcd99yyyzzz85a5"
	whTestAppID  = "A123"
)

// stubSlackCreds serves one credential row keyed by app id.
type stubSlackCreds struct {
	row *domain.CompanySlackCredential
}

func (s *stubSlackCreds) Get(context.Context, string) (*domain.CompanySlackCredential, error) {
	if s.row == nil {
		return nil, domain.ErrNotFound
	}
	cp := *s.row
	return &cp, nil
}

func (s *stubSlackCreds) GetByAppID(_ context.Context, appID string) (*domain.CompanySlackCredential, error) {
	if s.row == nil || s.row.AppID != appID {
		return nil, domain.ErrNotFound
	}
	cp := *s.row
	return &cp, nil
}
func (s *stubSlackCreds) Upsert(context.Context, *domain.CompanySlackCredential) error { return nil }
func (s *stubSlackCreds) Delete(context.Context, string) error                         { return nil }
func (s *stubSlackCreds) ListEnabled(context.Context) ([]*domain.CompanySlackCredential, error) {
	return nil, nil
}

type stubSlackUsers struct{}

func (stubSlackUsers) Add(context.Context, *domain.AllowedSlackUser) error { return nil }
func (stubSlackUsers) Remove(context.Context, string, string) error        { return nil }
func (stubSlackUsers) ListByCompany(context.Context, string) ([]*domain.AllowedSlackUser, error) {
	return nil, nil
}
func (stubSlackUsers) IsAllowed(context.Context, string, string) (bool, error) { return false, nil }

// newWebhookRig builds a router with the Slack webhook mounted. chatEnq is
// nil: every case here returns before the enqueue path is reached.
func newWebhookRig(t *testing.T, enabled bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	cipher, err := crypto.NewFromHex(whTestKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	creds := &stubSlackCreds{row: &domain.CompanySlackCredential{
		CompanyID:     "co-1",
		AppID:         whTestAppID,
		SigningSecret: whTestSecret,
		Enabled:       enabled,
	}}
	svc := app.NewSlackService(creds, stubSlackUsers{}, cipher)

	r := gin.New()
	NewSlackWebhookHandler(svc, nil).Register(r.Group("/webhook"))
	return r
}

func signedRequest(t *testing.T, appID, body string, hdr map[string]string) *http.Request {
	t.Helper()
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(whTestSecret))
	mac.Write([]byte("v0:" + ts + ":" + body))

	req := httptest.NewRequest(http.MethodPost, "/webhook/slack/events/"+appID, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-Slack-Signature", "v0="+hex.EncodeToString(mac.Sum(nil)))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return req
}

func TestSlackWebhook_unknownAppID(t *testing.T) {
	w := httptest.NewRecorder()
	newWebhookRig(t, true).ServeHTTP(w, signedRequest(t, "A-unknown", `{"type":"url_verification"}`, nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestSlackWebhook_disabledTenant(t *testing.T) {
	w := httptest.NewRecorder()
	newWebhookRig(t, false).ServeHTTP(w, signedRequest(t, whTestAppID, `{"type":"url_verification"}`, nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestSlackWebhook_badSignatureRejected(t *testing.T) {
	body := `{"type":"url_verification","challenge":"abc"}`
	req := signedRequest(t, whTestAppID, body, nil)
	req.Header.Set("X-Slack-Signature", "v0=deadbeef")

	w := httptest.NewRecorder()
	newWebhookRig(t, true).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

// A valid signature over a *different* body must not authorize this one.
func TestSlackWebhook_signatureBoundToBody(t *testing.T) {
	req := signedRequest(t, whTestAppID, `{"type":"url_verification","challenge":"abc"}`, nil)
	tampered := `{"type":"url_verification","challenge":"evil"}`
	req.Body = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tampered)).Body
	req.ContentLength = int64(len(tampered))

	w := httptest.NewRecorder()
	newWebhookRig(t, true).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestSlackWebhook_missingSignatureRejected(t *testing.T) {
	req := signedRequest(t, whTestAppID, `{"type":"url_verification"}`, nil)
	req.Header.Del("X-Slack-Signature")

	w := httptest.NewRecorder()
	newWebhookRig(t, true).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestSlackWebhook_urlVerificationEchoesChallenge(t *testing.T) {
	w := httptest.NewRecorder()
	newWebhookRig(t, true).ServeHTTP(w,
		signedRequest(t, whTestAppID, `{"type":"url_verification","challenge":"3eZbrw1a"}`, nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var out struct {
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Challenge != "3eZbrw1a" {
		t.Fatalf("challenge = %q", out.Challenge)
	}
}

// Slack redelivers on a slow ack. Re-running would charge a second agent
// turn and post a duplicate answer, so retries are acked and dropped —
// reaching the (nil) enqueuer here would panic.
func TestSlackWebhook_deliveryRetryDropped(t *testing.T) {
	body := `{"type":"event_callback","team_id":"T1","event_id":"Ev1",
		"authorizations":[{"team_id":"T1","user_id":"U0BOT","is_bot":true}],
		"event":{"type":"app_mention","user":"U999","text":"<@U0BOT> hi","ts":"1700000000.000100","channel":"C1"}}`

	w := httptest.NewRecorder()
	newWebhookRig(t, true).ServeHTTP(w, signedRequest(t, whTestAppID, body, map[string]string{
		"X-Slack-Retry-Num":    "1",
		"X-Slack-Retry-Reason": "http_timeout",
	}))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

// Non-actionable events (here: the bot's own post) are acked and dropped
// before the enqueue path.
func TestSlackWebhook_botEchoDropped(t *testing.T) {
	body := `{"type":"event_callback","team_id":"T1",
		"authorizations":[{"team_id":"T1","user_id":"U0BOT","is_bot":true}],
		"event":{"type":"message","subtype":"bot_message","bot_id":"B1","user":"U0BOT",
		         "text":"the answer","ts":"1700000000.000100","channel":"D1","channel_type":"im"}}`

	w := httptest.NewRecorder()
	newWebhookRig(t, true).ServeHTTP(w, signedRequest(t, whTestAppID, body, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

// A user who is not on the allowlist is dropped silently — the stub
// allowlist returns false for everyone, so this must not reach the enqueuer.
func TestSlackWebhook_nonAllowlistedUserDropped(t *testing.T) {
	body := `{"type":"event_callback","team_id":"T1",
		"authorizations":[{"team_id":"T1","user_id":"U0BOT","is_bot":true}],
		"event":{"type":"app_mention","user":"U999","text":"<@U0BOT> sales?","ts":"1700000000.000100","channel":"C1"}}`

	w := httptest.NewRecorder()
	newWebhookRig(t, true).ServeHTTP(w, signedRequest(t, whTestAppID, body, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}
