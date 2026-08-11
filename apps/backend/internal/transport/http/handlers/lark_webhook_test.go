package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// T-H2. Both of this handler's checks were conditional on input the caller
// writes: the signature was verified only `if sig != ""` and the verification
// token only `if env.Header.Token != ""`. Omitting either header skipped its
// own check, which is the entire authentication of a public ingress that
// enqueues agent turns.
//
// A missing signature is a failed signature; a missing token is a failed token.

const (
	larkTestAppID      = "cli_a1b2c3"
	larkTestEncryptKey = "encrypt-key-abc"
	larkTestVerifyTok  = "verification-token-xyz"
)

type stubLarkCreds struct{ row *domain.CompanyLarkCredential }

func (s *stubLarkCreds) Get(context.Context, string) (*domain.CompanyLarkCredential, error) {
	if s.row == nil {
		return nil, domain.ErrNotFound
	}
	cp := *s.row
	return &cp, nil
}

func (s *stubLarkCreds) GetByAppID(_ context.Context, appID string) (*domain.CompanyLarkCredential, error) {
	if s.row == nil || s.row.AppID != appID {
		return nil, domain.ErrNotFound
	}
	cp := *s.row
	return &cp, nil
}
func (s *stubLarkCreds) Upsert(context.Context, *domain.CompanyLarkCredential) error { return nil }
func (s *stubLarkCreds) Delete(context.Context, string) error                        { return nil }
func (s *stubLarkCreds) ListEnabled(context.Context) ([]*domain.CompanyLarkCredential, error) {
	return nil, nil
}

type stubLarkUsers struct{ hits int }

func (s *stubLarkUsers) Add(context.Context, *domain.AllowedLarkUser) error { return nil }
func (s *stubLarkUsers) Remove(context.Context, string, string) error       { return nil }
func (s *stubLarkUsers) ListByCompany(context.Context, string) ([]*domain.AllowedLarkUser, error) {
	return nil, nil
}
func (s *stubLarkUsers) IsAllowed(context.Context, string, string) (bool, error) {
	s.hits++
	return false, nil
}

// newLarkRig mounts the handler over one credential row. chatEnq is nil: every
// case here returns before the enqueue path, and the allowlist counter is how a
// test tells "authenticated then dropped" from "refused".
func newLarkRig(t *testing.T, encryptKey, verifyTok string) (*gin.Engine, *stubLarkUsers) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	creds := &stubLarkCreds{row: &domain.CompanyLarkCredential{
		CompanyID:         "co-1",
		AppID:             larkTestAppID,
		VerificationToken: verifyTok,
		EncryptKey:        encryptKey,
		BotOpenID:         "ou_bot",
		Enabled:           true,
	}}
	users := &stubLarkUsers{}

	r := gin.New()
	NewLarkWebhookHandler(app.NewLarkService(creds, users, nil), nil).Register(r.Group("/webhook"))
	return r, users
}

func larkSignature(encryptKey, ts, nonce string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(ts))
	h.Write([]byte(nonce))
	h.Write([]byte(encryptKey))
	h.Write(body)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func larkMessageBody(token string) string {
	return `{"schema":"2.0","header":{"event_id":"e1","event_type":"im.message.receive_v1","token":"` + token + `"},` +
		`"event":{"sender":{"sender_id":{"open_id":"ou_user"}},"message":{"message_id":"om_1","chat_id":"oc_1",` +
		`"message_type":"text","content":"{\"text\":\"@_user_1 sales?\"}","mentions":[{"key":"@_user_1","id":{"open_id":"ou_bot"}}]}}}`
}

func larkRequest(t *testing.T, body, encryptKey string, withSig bool) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhook/lark/events/"+larkTestAppID, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if withSig {
		const ts, nonce = "1800000000", "n1"
		req.Header.Set("X-Lark-Request-Timestamp", ts)
		req.Header.Set("X-Lark-Request-Nonce", nonce)
		req.Header.Set("X-Lark-Signature", larkSignature(encryptKey, ts, nonce, []byte(body)))
	}
	return req
}

// The positive case, so the refusals below are not all passing for the same
// reason a broken handler would.
func TestLarkWebhook_signedAndTokenedRequestIsAccepted(t *testing.T) {
	r, users := newLarkRig(t, larkTestEncryptKey, larkTestVerifyTok)
	body := larkMessageBody(larkTestVerifyTok)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, larkRequest(t, body, larkTestEncryptKey, true))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if users.hits != 1 {
		t.Fatalf("allowlist consulted %d times, want 1 — the request did not get past authentication", users.hits)
	}
}

func TestLarkWebhook_missingSignatureIsAFailedSignature(t *testing.T) {
	r, users := newLarkRig(t, larkTestEncryptKey, larkTestVerifyTok)
	body := larkMessageBody(larkTestVerifyTok)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, larkRequest(t, body, larkTestEncryptKey, false))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if users.hits != 0 {
		t.Errorf("a refused request still ran the allowlist %d times", users.hits)
	}
}

func TestLarkWebhook_signatureRefusals(t *testing.T) {
	body := larkMessageBody(larkTestVerifyTok)
	cases := []struct {
		name   string
		mutate func(*http.Request)
	}{
		{"a signature over the wrong key", func(req *http.Request) {
			req.Header.Set("X-Lark-Signature", larkSignature("some-other-key", "1800000000", "n1", []byte(body)))
		}},
		// The timestamp and nonce are inside the hash, so replaying a signature
		// under a different pair does not verify.
		{"the timestamp changed after signing", func(req *http.Request) {
			req.Header.Set("X-Lark-Request-Timestamp", "1800000001")
		}},
		{"the nonce changed after signing", func(req *http.Request) {
			req.Header.Set("X-Lark-Request-Nonce", "n2")
		}},
		{"an empty signature header", func(req *http.Request) {
			req.Header.Set("X-Lark-Signature", "")
		}},
		{"a signature that is neither hex nor base64", func(req *http.Request) {
			req.Header.Set("X-Lark-Signature", "not-a-signature")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, users := newLarkRig(t, larkTestEncryptKey, larkTestVerifyTok)
			req := larkRequest(t, body, larkTestEncryptKey, true)
			tc.mutate(req)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", w.Code)
			}
			if users.hits != 0 {
				t.Errorf("a refused request still ran the allowlist %d times", users.hits)
			}
		})
	}
}

// The token half. A tenant with no encrypt key gets no signature from Lark, so
// this is the only check standing — and it was skipped whenever the field was
// absent from the body.
func TestLarkWebhook_missingTokenIsAFailedToken(t *testing.T) {
	cases := []struct {
		name  string
		token string
	}{
		{"the field left out entirely", ""},
		{"a guessed token", "verification-token-xy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// No encrypt key: the shape where the token is all there is.
			r, users := newLarkRig(t, "", larkTestVerifyTok)
			body := larkMessageBody(tc.token)
			if tc.token == "" {
				body = strings.Replace(body, `,"token":""`, "", 1)
			}

			w := httptest.NewRecorder()
			r.ServeHTTP(w, larkRequest(t, body, "", false))

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", w.Code)
			}
			if users.hits != 0 {
				t.Errorf("a refused request still ran the allowlist %d times", users.hits)
			}
		})
	}
}

// The token check also stands on a tenant that does have an encrypt key: a
// valid signature is not a licence to skip it.
func TestLarkWebhook_validSignatureWithAWrongTokenIsRefused(t *testing.T) {
	r, users := newLarkRig(t, larkTestEncryptKey, larkTestVerifyTok)
	body := larkMessageBody("wrong-token")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, larkRequest(t, body, larkTestEncryptKey, true))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if users.hits != 0 {
		t.Errorf("a refused request still ran the allowlist %d times", users.hits)
	}
}

// url_verification is the setup handshake, and it is reachable before anything
// else is configured — so it is the one an attacker who guessed the URL would
// try first.
func TestLarkWebhook_urlVerification(t *testing.T) {
	t.Run("echoes the challenge for the right token", func(t *testing.T) {
		r, _ := newLarkRig(t, "", larkTestVerifyTok)
		body := `{"type":"url_verification","token":"` + larkTestVerifyTok + `","challenge":"ajls384kdjx98XX"}`

		w := httptest.NewRecorder()
		r.ServeHTTP(w, larkRequest(t, body, "", false))

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		var out struct {
			Challenge string `json:"challenge"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out.Challenge != "ajls384kdjx98XX" {
			t.Fatalf("challenge = %q", out.Challenge)
		}
	})
	t.Run("refuses a wrong token", func(t *testing.T) {
		r, _ := newLarkRig(t, "", larkTestVerifyTok)
		body := `{"type":"url_verification","token":"guess","challenge":"ajls384kdjx98XX"}`

		w := httptest.NewRecorder()
		r.ServeHTTP(w, larkRequest(t, body, "", false))

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
	})
	t.Run("refuses an omitted token", func(t *testing.T) {
		r, _ := newLarkRig(t, "", larkTestVerifyTok)
		body := `{"type":"url_verification","challenge":"ajls384kdjx98XX"}`

		w := httptest.NewRecorder()
		r.ServeHTTP(w, larkRequest(t, body, "", false))

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
	})
}

// A tenant row with neither an encrypt key nor a verification token gives this
// handler nothing to check anyone against, and `"" == ""` would have admitted
// every caller. The route answers 401 rather than running an open ingress.
func TestLarkWebhook_tenantWithNoCredentialsIsRefused(t *testing.T) {
	for _, body := range []string{
		`{"type":"url_verification","challenge":"c"}`,
		larkMessageBody(""),
	} {
		r, users := newLarkRig(t, "", "")

		w := httptest.NewRecorder()
		r.ServeHTTP(w, larkRequest(t, body, "", false))

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 for %s", w.Code, body[:30])
		}
		if users.hits != 0 {
			t.Errorf("a refused request still ran the allowlist %d times", users.hits)
		}
	}
}
