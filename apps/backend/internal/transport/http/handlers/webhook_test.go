package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/whatsapp"
	"github.com/fauzanebd/argentum/pkg/models"
)

// T-H1. /webhook/whatsapp is mounted outside middleware.Auth because the caller
// is Meta or Twilio and holds no Argentum credential, so the signature is the
// authentication. It verified the Twilio one, logged "continuing in dev mode"
// and carried on — against a verifier that was a stub returning "" — and it
// chose which of the two providers to check from a request header.
//
// The cases below are the ticket's matrix: a signed request passes, the same
// body with one byte changed is 401, no signature header is 401, and a
// form-encoded request against a Meta-configured deployment is 401.

const (
	waTestAuthToken = "twilio-auth-token"
	waTestAppSecret = "meta-app-secret"
	waTestHost      = "argentum.example.com"
	waWebhookPath   = "/webhook/whatsapp"
)

// stubPhones is the allowlist. Every number is unknown, which stops each case
// one step short of the enqueue path — reaching it needs a queue, and what is
// under test is what happens before it.
type stubPhones struct{ hits int }

func (s *stubPhones) Add(context.Context, *domain.AllowedPhoneNumber) error { return nil }
func (s *stubPhones) Remove(context.Context, string, string) error          { return nil }
func (s *stubPhones) ListByCompany(context.Context, string) ([]*domain.AllowedPhoneNumber, error) {
	return nil, nil
}
func (s *stubPhones) FindCompanyByPhone(context.Context, string) (*domain.AllowedPhoneNumber, error) {
	s.hits++
	return nil, domain.ErrNotFound
}

// stubWA is the real verifier under a recording sender, so a test can tell
// "authenticated and then dropped as an unknown number" from "refused".
type stubWA struct {
	whatsapp.Provider
	sent []string
}

func (s *stubWA) SendMessage(_, message string) error {
	s.sent = append(s.sent, message)
	return nil
}
func (s *stubWA) SendResponse(string, *models.AgentResponse) error { return nil }

func newWhatsAppRig(t *testing.T, transport whatsapp.Transport) (*gin.Engine, *stubPhones, *stubWA) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	var inner whatsapp.Provider
	switch transport {
	case whatsapp.TransportTwilio:
		inner = whatsapp.NewTwilioClient("AC123", waTestAuthToken, "+14155238886")
	default:
		inner = whatsapp.NewWhatsAppClient("v18.0", "123", "access", waTestAppSecret)
	}
	wa := &stubWA{Provider: inner}
	phones := &stubPhones{}
	// Only the phone repository is reached: ResolveCompanyByPhone is the whole
	// of what this handler asks the service for.
	companySvc := app.NewCompanyService(nil, nil, phones, nil, nil, nil, nil)

	r := gin.New()
	NewWebhookHandler(nil, companySvc, wa, transport, "verify-token").Register(r.Group("/webhook"))
	return r, phones, wa
}

// signedTwilioRequest builds what Twilio would send: the form, and HMAC-SHA1
// over the URL followed by the parameters sorted by name.
func signedTwilioRequest(form url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, waWebhookPath, strings.NewReader(form.Encode()))
	req.Host = waTestHost
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	keys := make([]string, 0, len(form))
	for k := range form {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	mac := hmac.New(sha1.New, []byte(waTestAuthToken))
	mac.Write([]byte("https://" + waTestHost + waWebhookPath))
	for _, k := range keys {
		for _, v := range form[k] {
			mac.Write([]byte(k))
			mac.Write([]byte(v))
		}
	}
	req.Header.Set("X-Twilio-Signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	return req
}

func twilioForm() url.Values {
	return url.Values{
		"MessageSid": {"SM0123456789abcdef"},
		"From":       {"whatsapp:+628110000000"},
		"To":         {"whatsapp:+14155238886"},
		"Body":       {"berapa penjualan bulan lalu?"},
	}
}

// The positive case. It stops at the allowlist rather than the queue, and the
// spoken refusal is the proof it got that far: nothing before authentication
// sends a message.
func TestWhatsAppWebhook_signedTwilioRequestIsAccepted(t *testing.T) {
	r, phones, wa := newWhatsAppRig(t, whatsapp.TransportTwilio)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedTwilioRequest(twilioForm()))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if phones.hits != 1 {
		t.Fatalf("allowlist consulted %d times, want 1 — the request did not get past authentication", phones.hits)
	}
	if len(wa.sent) != 1 || !strings.Contains(wa.sent[0], "not authorised") {
		t.Errorf("sent = %v, want the unknown-number refusal", wa.sent)
	}
}

func TestWhatsAppWebhook_forgedTwilioRequestsAreRefused(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*http.Request)
		wantErr string
	}{
		{
			// One byte of the body changed after signing. This is the case the
			// whole ticket is about: the forged `Body` is what the agent runs.
			name: "one byte changed in the body",
			mutate: func(req *http.Request) {
				f := twilioForm()
				f.Set("Body", "berapa penjualan bulan lalu!")
				body := f.Encode()
				req.Body = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)).Body
				req.ContentLength = int64(len(body))
			},
		},
		{
			// A different `From` is a different tenant. Same signature.
			name: "the sender changed",
			mutate: func(req *http.Request) {
				f := twilioForm()
				f.Set("From", "whatsapp:+628119999999")
				body := f.Encode()
				req.Body = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)).Body
				req.ContentLength = int64(len(body))
			},
		},
		{
			name:   "no signature header",
			mutate: func(req *http.Request) { req.Header.Del("X-Twilio-Signature") },
		},
		{
			name:   "empty signature header",
			mutate: func(req *http.Request) { req.Header.Set("X-Twilio-Signature", "") },
		},
		{
			name:   "a signature that is not base64",
			mutate: func(req *http.Request) { req.Header.Set("X-Twilio-Signature", "deadbeef") },
		},
		{
			// A signature captured from another deployment. The URL is inside
			// the signed string, so it does not travel.
			name:   "signed for a different host",
			mutate: func(req *http.Request) { req.Host = "other.example.com" },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, phones, wa := newWhatsAppRig(t, whatsapp.TransportTwilio)
			req := signedTwilioRequest(twilioForm())
			tc.mutate(req)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", w.Code)
			}
			// 401 is necessary but not sufficient: a refusal that still resolved
			// the company would have already touched tenant state.
			if phones.hits != 0 {
				t.Errorf("a refused request still resolved a company %d times", phones.hits)
			}
			if len(wa.sent) != 0 {
				t.Errorf("a refused request still sent %d messages", len(wa.sent))
			}
		})
	}
}

// The provider was chosen by the caller: an X-Twilio-Signature header or a form
// content type routed a Meta deployment into the Twilio branch, where the
// verifier was a stub. Now the deployment decides, so this request meets the
// Meta check, has no X-Hub-Signature-256, and is refused.
func TestWhatsAppWebhook_formEncodedRequestOnAMetaDeploymentIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*http.Request)
	}{
		{"with a Twilio signature header", func(*http.Request) {}},
		{"with no signature header at all", func(req *http.Request) { req.Header.Del("X-Twilio-Signature") }},
		{"with a Twilio signature that is genuinely valid", func(*http.Request) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, phones, _ := newWhatsAppRig(t, whatsapp.TransportMeta)
			req := signedTwilioRequest(twilioForm())
			tc.mutate(req)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", w.Code)
			}
			if phones.hits != 0 {
				t.Errorf("a refused request still resolved a company %d times", phones.hits)
			}
		})
	}
}

// The mirror image: a Meta-shaped JSON callback against a Twilio deployment
// carries no X-Twilio-Signature, so it fails the check the deployment runs.
func TestWhatsAppWebhook_metaPayloadOnATwilioDeploymentIsRefused(t *testing.T) {
	r, phones, _ := newWhatsAppRig(t, whatsapp.TransportTwilio)

	body := `{"entry":[{"id":"1","changes":[{"value":{"messages":[{"id":"wamid.1","from":"628110000000","type":"text","text":{"body":"hi"}}]}}]}]}`
	req := httptest.NewRequest(http.MethodPost, waWebhookPath, strings.NewReader(body))
	req.Host = waTestHost
	req.Header.Set("Content-Type", "application/json")
	mac := hmac.New(sha256.New, []byte(waTestAppSecret))
	mac.Write([]byte(body))
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if phones.hits != 0 {
		t.Errorf("a refused request still resolved a company %d times", phones.hits)
	}
}

func TestWhatsAppWebhook_signedMetaRequestIsAccepted(t *testing.T) {
	r, phones, wa := newWhatsAppRig(t, whatsapp.TransportMeta)

	body := `{"entry":[{"id":"1","changes":[{"value":{"messages":[{"id":"wamid.1","from":"628110000000","type":"text","text":{"body":"hi"}}]}}]}]}`
	req := httptest.NewRequest(http.MethodPost, waWebhookPath, strings.NewReader(body))
	req.Host = waTestHost
	req.Header.Set("Content-Type", "application/json")
	mac := hmac.New(sha256.New, []byte(waTestAppSecret))
	mac.Write([]byte(body))
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if phones.hits != 1 {
		t.Fatalf("allowlist consulted %d times, want 1", phones.hits)
	}
	if len(wa.sent) != 1 {
		t.Errorf("sent = %v, want the unknown-number refusal", wa.sent)
	}
}

// The GET handshake belongs to Meta. Answering it on a Twilio deployment would
// echo an attacker's own hub.challenge back to them.
func TestWhatsAppWebhook_verifyHandshake(t *testing.T) {
	t.Run("meta echoes the challenge for the configured token", func(t *testing.T) {
		r, _, _ := newWhatsAppRig(t, whatsapp.TransportMeta)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
			waWebhookPath+"?hub.mode=subscribe&hub.verify_token=verify-token&hub.challenge=1158201444", nil))
		if w.Code != http.StatusOK || w.Body.String() != "1158201444" {
			t.Fatalf("status = %d, body = %q", w.Code, w.Body.String())
		}
	})
	t.Run("meta refuses a wrong token", func(t *testing.T) {
		r, _, _ := newWhatsAppRig(t, whatsapp.TransportMeta)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
			waWebhookPath+"?hub.mode=subscribe&hub.verify_token=guess&hub.challenge=1158201444", nil))
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", w.Code)
		}
	})
	t.Run("twilio has no handshake", func(t *testing.T) {
		r, _, _ := newWhatsAppRig(t, whatsapp.TransportTwilio)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
			waWebhookPath+"?hub.mode=subscribe&hub.verify_token=verify-token&hub.challenge=1158201444", nil))
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", w.Code)
		}
	})
}
