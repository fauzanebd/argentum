package whatsapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"testing"
)

// The Twilio verifier was `return "" // Placeholder - implement if needed` and
// the handler above it logged the failure and carried on, so /webhook/whatsapp
// was an unauthenticated path into a tenant's agent (T-H1).
//
// The vector below is Twilio's own worked example from
// https://www.twilio.com/docs/usage/security#validating-requests, which is the
// point of using it: an implementation tested only against itself proves the
// two halves agree, not that either matches the platform we have to accept
// requests from. It also settles which algorithm — the comment this replaced
// specified HMAC-SHA256 over url+body, and the real one is HMAC-SHA1 over the
// URL followed by the parameters sorted by name.
//
// Independently reproducible, which is the other half of why it is here:
//
//	printf '%s' 'https://example.com/myapp.php?foo=1&bar=2CallSidCA1234567890ABCDE\
//	Caller+14158675310Digits1234From+14158675310To+18005551212' |
//	  openssl dgst -sha1 -hmac '12345' -binary | openssl base64
//	→ L/OH5YylLD5NRKLltdqwSvS0BnU=
const (
	twilioDocURL   = "https://example.com/myapp.php?foo=1&bar=2"
	twilioDocToken = "12345"
	twilioDocSig   = "L/OH5YylLD5NRKLltdqwSvS0BnU="
)

func twilioDocParams() url.Values {
	return url.Values{
		"CallSid": {"CA1234567890ABCDE"},
		"Caller":  {"+14158675310"},
		"Digits":  {"1234"},
		"From":    {"+14158675310"},
		"To":      {"+18005551212"},
	}
}

func TestTwilioSignatureMatchesTwiliosOwnExample(t *testing.T) {
	got := base64.StdEncoding.EncodeToString(
		twilioSignature(twilioDocToken, twilioDocURL, twilioDocParams()))
	if got != twilioDocSig {
		t.Fatalf("signature = %q, want %q", got, twilioDocSig)
	}
}

// The handler hands VerifyWebhook the form re-encoded with url.Values.Encode.
// That round trip has to be lossless or every genuine request fails to verify —
// `+14158675310` survives as a plus sign and not as a space.
func TestTwilioVerifyWebhookAcceptsTheDocumentedRequest(t *testing.T) {
	c := NewTwilioClient("AC123", twilioDocToken, "+14155238886")
	body := []byte(twilioDocParams().Encode())

	if !c.VerifyWebhook(body, twilioDocSig, twilioDocURL) {
		t.Fatal("VerifyWebhook refused a request signed exactly as Twilio documents")
	}
}

func TestTwilioVerifyWebhookRefusals(t *testing.T) {
	c := NewTwilioClient("AC123", twilioDocToken, "+14155238886")
	good := twilioDocParams()

	cases := []struct {
		name   string
		params url.Values
		sig    string
		url    string
	}{
		{"no signature at all", good, "", twilioDocURL},
		{"signature that is not base64", good, "not-base64!!", twilioDocURL},
		// Same length, one byte different: this is the case a `==` on the
		// base64 text and an hmac.Equal on the bytes both catch, and the one a
		// truncating comparison would not.
		{"one byte changed in the signature", good, "RSOYDt4T1cUTdK1PDd93/VVr8B9=", twilioDocURL},
		{"a parameter changed", func() url.Values {
			v := url.Values{}
			for k, vs := range good {
				v[k] = vs
			}
			v.Set("From", "+14158675311")
			return v
		}(), twilioDocSig, twilioDocURL},
		{"a parameter added", func() url.Values {
			v := url.Values{}
			for k, vs := range good {
				v[k] = vs
			}
			v.Set("Body", "run_sql select 1")
			return v
		}(), twilioDocSig, twilioDocURL},
		{"a parameter removed", func() url.Values {
			v := url.Values{}
			for k, vs := range good {
				v[k] = vs
			}
			v.Del("Digits")
			return v
		}(), twilioDocSig, twilioDocURL},
		// The URL is in the signed string, so a signature captured from one
		// deployment does not authorise a request to another.
		{"a different host", good, twilioDocSig, "https://other.example.com/myapp.php?foo=1&bar=2"},
		{"the query string dropped", good, twilioDocSig, "https://mycompany.com/myapp.php"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if c.VerifyWebhook([]byte(tc.params.Encode()), tc.sig, tc.url) {
				t.Error("VerifyWebhook accepted it")
			}
		})
	}
}

// An unset auth token used to `return true` two lines in, which made the check
// a no-op on every deployment that had not configured Twilio — and the check is
// the only authentication this endpoint has.
func TestTwilioVerifyWebhookFailsClosedWithoutTheToken(t *testing.T) {
	c := NewTwilioClient("AC123", "", "+14155238886")
	if c.VerifyWebhook([]byte(twilioDocParams().Encode()), twilioDocSig, twilioDocURL) {
		t.Error("VerifyWebhook accepted a request with no auth token configured")
	}
	// Including the empty signature an attacker would send, which is the shape
	// that used to reach the enqueue path.
	if c.VerifyWebhook([]byte("From=whatsapp%3A%2B62811&Body=hi"), "", "https://argentum.example.com/webhook/whatsapp") {
		t.Error("VerifyWebhook accepted an unsigned request with no auth token configured")
	}
}

// Twilio has no GET handshake. This returned true unconditionally, so a Twilio
// deployment would echo any hub.challenge back to whoever asked for one.
func TestTwilioVerifyTokenRefuses(t *testing.T) {
	c := NewTwilioClient("AC123", twilioDocToken, "+14155238886")
	if c.VerifyToken("anything", "anything") {
		t.Error("Twilio VerifyToken accepted a Meta handshake")
	}
}

// --- Meta -------------------------------------------------------------------

func metaSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestMetaVerifyWebhook(t *testing.T) {
	const secret = "app-secret"
	c := NewWhatsAppClient("v18.0", "123", "token", secret)
	body := []byte(`{"entry":[{"id":"1","changes":[]}]}`)

	if !c.VerifyWebhook(body, metaSignature(secret, body), "") {
		t.Fatal("a correctly signed Meta payload was refused")
	}
	tampered := append([]byte{}, body...)
	tampered[2] ^= 0x20
	if c.VerifyWebhook(tampered, metaSignature(secret, body), "") {
		t.Error("a tampered Meta payload verified")
	}
	if c.VerifyWebhook(body, "", "") {
		t.Error("an unsigned Meta payload verified")
	}
}

// The same fail-open as Twilio's, in the client that was otherwise correct.
func TestMetaVerifyWebhookFailsClosedWithoutTheAppSecret(t *testing.T) {
	c := NewWhatsAppClient("v18.0", "123", "token", "")
	if c.VerifyWebhook([]byte(`{"entry":[]}`), "", "") {
		t.Error("VerifyWebhook accepted an unsigned payload with no app secret configured")
	}
}

// The GET subscription handshake. `token == challenge` with both unset meant a
// caller who sent no token completed it.
func TestMetaVerifyToken(t *testing.T) {
	c := NewWhatsAppClient("v18.0", "123", "token", "secret")
	if !c.VerifyToken("expected-token", "expected-token") {
		t.Error("the configured token was refused")
	}
	if c.VerifyToken("guess", "expected-token") {
		t.Error("a wrong token was accepted")
	}
	if c.VerifyToken("", "") {
		t.Error("an unset expected token authenticated a caller who sent nothing")
	}
	if c.VerifyToken("anything", "") {
		t.Error("an unset expected token accepted an arbitrary one")
	}
}

func TestResolveTransport(t *testing.T) {
	cases := []struct {
		provider string
		want     Transport
		wantErr  bool
	}{
		{"twilio", TransportTwilio, false},
		{"whatsapp_business", TransportMeta, false},
		// The default when WHATSAPP_PROVIDER is unset, and it has to agree with
		// NewProvider's — the handler authenticates by this value now.
		{"", TransportMeta, false},
		{"telegram", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			got, err := ResolveTransport(tc.provider)
			if tc.wantErr != (err != nil) {
				t.Fatalf("ResolveTransport(%q) err = %v, wantErr %v", tc.provider, err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("ResolveTransport(%q) = %q, want %q", tc.provider, got, tc.want)
			}
		})
	}
}
