package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/queue"
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

// stubPhones is the allowlist. Every number is unknown unless allowed names it,
// which stops each case one step short of the enqueue path — reaching it needs
// a queue, and what is under test is what happens before it. RIZ-85's cases set
// allowed, and wire a queue, because "an attachment enqueues nothing" is only
// a claim about a number that would otherwise have been answered.
type stubPhones struct {
	hits int
	// allowed maps a phone number, as the handler passes it, to its company.
	allowed map[string]string
}

func (s *stubPhones) Add(context.Context, *domain.AllowedPhoneNumber) error { return nil }
func (s *stubPhones) Remove(context.Context, string, string) error          { return nil }
func (s *stubPhones) ListByCompany(context.Context, string) ([]*domain.AllowedPhoneNumber, error) {
	return nil, nil
}
func (s *stubPhones) FindCompanyByPhone(_ context.Context, phone string) (*domain.AllowedPhoneNumber, error) {
	s.hits++
	if companyID, ok := s.allowed[phone]; ok {
		return &domain.AllowedPhoneNumber{CompanyID: companyID, PhoneNumber: phone}, nil
	}
	return nil, domain.ErrNotFound
}

// stubWA is the real verifier under a recording sender, so a test can tell
// "authenticated and then dropped as an unknown number" from "refused".
type stubWA struct {
	whatsapp.Provider
	sent []string
	// sentTo is who each message in sent went to, index for index.
	sentTo []string
	// sendErr is what every send answers, for the outage case (RIZ-85, AC6).
	sendErr error
}

func (s *stubWA) SendMessage(to, message string) error {
	s.sent = append(s.sent, message)
	s.sentTo = append(s.sentTo, to)
	return s.sendErr
}
func (s *stubWA) SendResponse(string, *models.AgentResponse) error { return nil }

func newWhatsAppRig(t *testing.T, transport whatsapp.Transport) (*gin.Engine, *stubPhones, *stubWA) {
	t.Helper()
	return buildWhatsAppRig(t, transport, nil)
}

// buildWhatsAppRig is newWhatsAppRig with a chat enqueuer, which the cases that
// reach the queue need and the authentication cases above deliberately do not
// have: nil there is a panic if a refused request ever got that far.
func buildWhatsAppRig(t *testing.T, transport whatsapp.Transport, chat *app.ChatEnqueuer) (*gin.Engine, *stubPhones, *stubWA) {
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
	NewWebhookHandler(chat, companySvc, wa, transport, "verify-token").Register(r.Group("/webhook"))
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

// ---- RIZ-85: an attachment with no text gets a reply -----------------------
//
// A voice note returned 200 and nothing else: the Twilio branch labelled every
// message "text" and dropped an empty Body, and Meta dropped every type that was
// not "text". These cases run from an allowlisted number through a real
// ChatEnqueuer over a recording queue, so "no turn" is something the queue
// observed rather than something a nil enqueuer would have panicked about.

const (
	waAllowedPhone  = "+628110000000" // twilioForm's sender, without "whatsapp:"
	waAllowedMetaID = "628110000000"  // the same person, as Meta writes them
	waUnknownPhone  = "+628119999999"
	waCompanyID     = "co-wa"
	waMetaMsgID     = "wamid.RIZ85"
	// waMediaURL is a bearer link to the recording on Twilio's side. It is in
	// every Twilio media form below so the log checks have something to miss.
	waMediaURL       = "https://api.twilio.com/2010-04-01/Accounts/AC123/Messages/MM0123/Media/ME0123"
	waCaption        = "berapa stok gudang barat?"
	waAttachmentLine = "whatsapp: attachment without text; asked the sender to type"
	waAttachmentWarn = "whatsapp: attachment without text; the reply asking the sender to type was not sent"
	waDroppedLine    = "whatsapp: inbound message not handled; dropped"
)

// waTurns records what the enqueue path wrote: a thread, a user message, a
// queued turn. All three stay zero for every message that is not a question.
type waTurns struct {
	threadsCreated, messagesAppended int
	payloads                         []queue.ChatRunPayload
}

func (s *waTurns) EnqueueChatRun(_ context.Context, p queue.ChatRunPayload) (string, error) {
	s.payloads = append(s.payloads, p)
	return "task-wa", nil
}

// waThreads is the thread table: this number has never written before.
type waThreads struct {
	domain.ThreadRepository
	turns *waTurns
}

func (s waThreads) LatestForPhone(context.Context, string, string) (*domain.ConversationThread, error) {
	return nil, domain.ErrNotFound
}

func (s waThreads) Create(_ context.Context, t *domain.ConversationThread) error {
	s.turns.threadsCreated++
	t.ID = "thread-wa"
	return nil
}

func (s waThreads) Touch(context.Context, string, time.Time) error { return nil }

type waMessages struct {
	domain.MessageRepository
	turns *waTurns
}

func (s waMessages) Append(_ context.Context, m *domain.Message) error {
	s.turns.messagesAppended++
	m.ID = "msg-wa"
	return nil
}

// waCompanies is the one company read a turn makes, for the name it carries.
type waCompanies struct{}

func (waCompanies) GetByID(_ context.Context, id string) (*domain.Company, error) {
	return &domain.Company{ID: id, Name: "Toko Maju"}, nil
}

// newWhatsAppTurnRig is newWhatsAppRig with both of the sender's numbers on
// waCompanyID's allowlist and a queue behind the handler.
func newWhatsAppTurnRig(t *testing.T, transport whatsapp.Transport) (*gin.Engine, *stubPhones, *stubWA, *waTurns) {
	t.Helper()
	turns := &waTurns{}
	threads := app.NewThreadService(waThreads{turns: turns}, waMessages{turns: turns}, nil, nil, app.ThreadServiceConfig{})
	chat := app.NewChatEnqueuer(threads, nil, waCompanies{}, turns)
	r, phones, wa := buildWhatsAppRig(t, transport, chat)
	phones.allowed = map[string]string{waAllowedPhone: waCompanyID, waAllowedMetaID: waCompanyID}
	return r, phones, wa, turns
}

// twilioMediaForm is what Twilio posts for a message with one attachment: the
// count, the MIME type and the URL beside an ordinary form.
func twilioMediaForm(contentType, body string) url.Values {
	f := twilioForm()
	f.Set("Body", body)
	f.Set("NumMedia", "1")
	f.Set("MediaContentType0", contentType)
	f.Set("MediaUrl0", waMediaURL)
	return f
}

// metaMessage is one inbound Meta message of msgType from `from`. extra is the
// type's own object, already prefixed with a comma.
func metaMessage(from, msgType, extra string) string {
	return `{"object":"whatsapp_business_account","entry":[{"id":"1","changes":[{"field":"messages","value":{"messages":[` +
		`{"id":"` + waMetaMsgID + `","from":"` + from + `","timestamp":"1760000000","type":"` + msgType + `"` + extra + `}]}}]}]}`
}

func signedMetaRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, waWebhookPath, strings.NewReader(body))
	req.Host = waTestHost
	req.Header.Set("Content-Type", "application/json")
	mac := hmac.New(sha256.New, []byte(waTestAppSecret))
	mac.Write([]byte(body))
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	return req
}

func entriesNamed(hook *logrustest.Hook, message string) []*logrus.Entry {
	var out []*logrus.Entry
	for _, e := range hook.AllEntries() {
		if e.Message == message {
			out = append(out, e)
		}
	}
	return out
}

// assertFields is AC7's first half: the line carries exactly these fields.
// Exactly, because a field added later is a field nobody checked for a phone
// number.
func assertFields(t *testing.T, e *logrus.Entry, want logrus.Fields) {
	t.Helper()
	if len(e.Data) != len(want) {
		t.Errorf("%q has fields %v, want exactly %v", e.Message, e.Data, want)
	}
	for k, v := range want {
		if got, ok := e.Data[k]; !ok || got != v {
			t.Errorf("%q field %s = %v (present %v), want %v", e.Message, k, got, ok, v)
		}
	}
}

// assertNothingLeaked is AC7's second half: no line written while the request
// was handled holds the sender's number, what they wrote or where the media is.
func assertNothingLeaked(t *testing.T, hook *logrustest.Hook, secrets ...string) {
	t.Helper()
	for _, e := range hook.AllEntries() {
		line := fmt.Sprintf("%s %v", e.Message, e.Data)
		for _, s := range secrets {
			if strings.Contains(line, s) {
				t.Errorf("log line %q contains %q", line, s)
			}
		}
	}
}

func assertNoTurn(t *testing.T, turns *waTurns) {
	t.Helper()
	if turns.threadsCreated != 0 || turns.messagesAppended != 0 || len(turns.payloads) != 0 {
		t.Errorf("a turn was started: %d threads, %d messages, %d queued",
			turns.threadsCreated, turns.messagesAppended, len(turns.payloads))
	}
}

// AC1, AC2 and AC7 for the reply: Q1, Q2 and Q7.
func TestWhatsAppWebhook_attachmentWithNoTextAsksForText(t *testing.T) {
	type tc struct {
		name      string
		transport whatsapp.Transport
		req       func() *http.Request
		wantKind  string
		// wantContentType is Twilio's MIME type, logged on Twilio only.
		wantContentType string
	}
	twilio := func(name, contentType, body, kind string) tc {
		return tc{
			name: "twilio " + name, transport: whatsapp.TransportTwilio, wantKind: kind, wantContentType: contentType,
			req: func() *http.Request { return signedTwilioRequest(twilioMediaForm(contentType, body)) },
		}
	}
	meta := func(name, msgType, extra string) tc {
		return tc{
			name: "meta " + name, transport: whatsapp.TransportMeta, wantKind: msgType,
			req: func() *http.Request { return signedMetaRequest(metaMessage(waAllowedMetaID, msgType, extra)) },
		}
	}
	cases := []tc{
		// AC1: the voice note itself.
		twilio("voice note", "audio/ogg", "", "audio"),
		twilio("voice note with its codec", "audio/ogg; codecs=opus", "", "audio"),
		// Blank after trimming is no text.
		twilio("voice note with a blank body", "audio/ogg", " \n\t ", "audio"),
		twilio("photo", "image/jpeg", "", "image"),
		twilio("video", "video/mp4", "", "video"),
		twilio("PDF", "application/pdf", "", "document"),
		// A sticker is an image/webp on Twilio, and gets the same reply.
		twilio("sticker", "image/webp", "", "image"),
		meta("voice note", "audio", `,"audio":{"id":"MEDIA1","mime_type":"audio/ogg; codecs=opus","voice":true}`),
		meta("photo", "image", `,"image":{"id":"MEDIA2","mime_type":"image/jpeg"}`),
		meta("video", "video", `,"video":{"id":"MEDIA3","mime_type":"video/mp4"}`),
		meta("PDF", "document", `,"document":{"id":"MEDIA4","mime_type":"application/pdf","filename":"stok.pdf"}`),
		meta("sticker", "sticker", `,"sticker":{"id":"MEDIA5","mime_type":"image/webp"}`),
		// A Meta caption is not read (a non-goal): the photo is an attachment.
		meta("photo with a caption", "image", `,"image":{"id":"MEDIA6","mime_type":"image/jpeg","caption":"`+waCaption+`"}`),
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hook := logrustest.NewGlobal()
			defer hook.Reset()
			r, phones, wa, turns := newWhatsAppTurnRig(t, c.transport)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, c.req())

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", w.Code)
			}
			if len(wa.sent) != 1 || wa.sent[0] != whatsapp.AttachmentReply {
				t.Fatalf("sent = %q, want exactly the attachment reply", wa.sent)
			}
			wantTo, wantSID, wantTransport := waAllowedPhone, "SM0123456789abcdef", "twilio"
			if c.transport == whatsapp.TransportMeta {
				wantTo, wantSID, wantTransport = waAllowedMetaID, waMetaMsgID, "whatsapp_business"
			}
			if wa.sentTo[0] != wantTo {
				t.Errorf("reply went to %q, want the sender %q", wa.sentTo[0], wantTo)
			}
			// R3: the allowlist is still asked, once, after the signature.
			if phones.hits != 1 {
				t.Errorf("allowlist consulted %d times, want 1", phones.hits)
			}
			assertNoTurn(t, turns)

			lines := entriesNamed(hook, waAttachmentLine)
			if len(lines) != 1 {
				t.Fatalf("%d %q lines, want 1 (all: %d entries)", len(lines), waAttachmentLine, len(hook.AllEntries()))
			}
			if lines[0].Level != logrus.InfoLevel {
				t.Errorf("level = %s, want info", lines[0].Level)
			}
			want := logrus.Fields{
				"transport": wantTransport, "message_sid": wantSID,
				"company_id": waCompanyID, "media_kind": c.wantKind,
			}
			if c.transport == whatsapp.TransportTwilio {
				want["content_type"] = c.wantContentType
				want["num_media"] = 1
			}
			assertFields(t, lines[0], want)
			if n := len(entriesNamed(hook, waDroppedLine)); n != 0 {
				t.Errorf("%d %q lines on a message that was answered", n, waDroppedLine)
			}
			assertNothingLeaked(t, hook, waAllowedMetaID, waMediaURL, waCaption, "MEDIA")
		})
	}
}

// AC3, Q3: an unknown number hears what it hears for text, and only that. The
// attachment reply would confirm nothing new, but sending it instead would be
// a second sentence for the same refusal.
func TestWhatsAppWebhook_attachmentFromAnUnknownNumberIsRefusedAsText(t *testing.T) {
	for _, c := range []struct {
		name      string
		transport whatsapp.Transport
		req       func() *http.Request
	}{
		{"twilio voice note", whatsapp.TransportTwilio, func() *http.Request {
			f := twilioMediaForm("audio/ogg", "")
			f.Set("From", "whatsapp:"+waUnknownPhone)
			return signedTwilioRequest(f)
		}},
		{"meta voice note", whatsapp.TransportMeta, func() *http.Request {
			return signedMetaRequest(metaMessage(strings.TrimPrefix(waUnknownPhone, "+"), "audio", `,"audio":{"id":"MEDIA1"}`))
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			hook := logrustest.NewGlobal()
			defer hook.Reset()
			r, phones, wa, turns := newWhatsAppTurnRig(t, c.transport)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, c.req())

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", w.Code)
			}
			if phones.hits != 1 {
				t.Errorf("allowlist consulted %d times, want 1", phones.hits)
			}
			if len(wa.sent) != 1 || !strings.Contains(wa.sent[0], "This phone number is not authorised") {
				t.Fatalf("sent = %q, want only the not-authorised reply", wa.sent)
			}
			assertNoTurn(t, turns)
			if n := len(entriesNamed(hook, waAttachmentLine)); n != 0 {
				t.Errorf("%d attachment lines for a number on no allowlist", n)
			}
		})
	}
}

// AC4, Q4: a photo with a caption is a question. It is enqueued with the
// caption, exactly as a text message is, and nobody is asked to type.
func TestWhatsAppWebhook_twilioCaptionIsAnsweredAsText(t *testing.T) {
	hook := logrustest.NewGlobal()
	defer hook.Reset()
	r, _, wa, turns := newWhatsAppTurnRig(t, whatsapp.TransportTwilio)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedTwilioRequest(twilioMediaForm("image/jpeg", waCaption)))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if len(turns.payloads) != 1 {
		t.Fatalf("queued %d turns, want 1", len(turns.payloads))
	}
	p := turns.payloads[0]
	if p.Message != waCaption || p.Channel != domain.ChannelWhatsApp ||
		p.PhoneNumber != waAllowedPhone || p.CompanyID != waCompanyID {
		t.Errorf("payload = %+v, want the caption from %s for %s", p, waAllowedPhone, waCompanyID)
	}
	if len(wa.sent) != 0 {
		t.Errorf("sent = %q, want nothing: the answer comes from the worker", wa.sent)
	}
	if n := len(entriesNamed(hook, waAttachmentLine)) + len(entriesNamed(hook, waDroppedLine)); n != 0 {
		t.Errorf("%d attachment or drop lines for a message that was enqueued", n)
	}
}

// AC5, Q5: what worked keeps working. Text on both transports is enqueued.
func TestWhatsAppWebhook_textIsStillEnqueued(t *testing.T) {
	for _, c := range []struct {
		name      string
		transport whatsapp.Transport
		req       func() *http.Request
		want      string
		from      string
	}{
		{"twilio", whatsapp.TransportTwilio, func() *http.Request { return signedTwilioRequest(twilioForm()) },
			"berapa penjualan bulan lalu?", waAllowedPhone},
		// NumMedia=0 is what Twilio sends with every text message.
		{"twilio with NumMedia=0", whatsapp.TransportTwilio, func() *http.Request {
			f := twilioForm()
			f.Set("NumMedia", "0")
			return signedTwilioRequest(f)
		}, "berapa penjualan bulan lalu?", waAllowedPhone},
		{"meta", whatsapp.TransportMeta, func() *http.Request {
			return signedMetaRequest(metaMessage(waAllowedMetaID, "text", `,"text":{"body":"hi"}`))
		}, "hi", waAllowedMetaID},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, _, wa, turns := newWhatsAppTurnRig(t, c.transport)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, c.req())

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", w.Code)
			}
			if len(turns.payloads) != 1 || turns.payloads[0].Message != c.want || turns.payloads[0].PhoneNumber != c.from {
				t.Fatalf("payloads = %+v, want one turn of %q from %s", turns.payloads, c.want, c.from)
			}
			if len(wa.sent) != 0 {
				t.Errorf("sent = %q, want nothing", wa.sent)
			}
		})
	}
}

// AC5 and AC7, Q5 and Q7: a message with neither text nor an attachment is
// still not answered — nobody sending a thumbs-up is waiting for one — but it
// is no longer invisible. It is dropped before the allowlist, as it always was.
func TestWhatsAppWebhook_messageWithNothingToAnswerIsLoggedAndDropped(t *testing.T) {
	for _, c := range []struct {
		name      string
		transport whatsapp.Transport
		req       func() *http.Request
		wantSID   string
		wantType  string
	}{
		{"twilio empty body, no media", whatsapp.TransportTwilio, func() *http.Request {
			f := twilioForm()
			f.Set("Body", "")
			return signedTwilioRequest(f)
		}, "SM0123456789abcdef", "text"},
		// A status callback: no Body at all.
		{"twilio status callback", whatsapp.TransportTwilio, func() *http.Request {
			f := twilioForm()
			f.Del("Body")
			f.Set("MessageStatus", "read")
			return signedTwilioRequest(f)
		}, "SM0123456789abcdef", "text"},
		// A shared location: Latitude and Longitude, no Body, NumMedia=0.
		{"twilio location", whatsapp.TransportTwilio, func() *http.Request {
			f := twilioForm()
			f.Set("Body", " ")
			f.Set("NumMedia", "0")
			f.Set("Latitude", "-6.2")
			f.Set("Longitude", "106.8")
			return signedTwilioRequest(f)
		}, "SM0123456789abcdef", "text"},
		// A NumMedia that does not parse counts no attachment.
		{"twilio malformed NumMedia", whatsapp.TransportTwilio, func() *http.Request {
			f := twilioMediaForm("audio/ogg", "")
			f.Set("NumMedia", "one")
			return signedTwilioRequest(f)
		}, "SM0123456789abcdef", "text"},
		{"meta reaction", whatsapp.TransportMeta, func() *http.Request {
			return signedMetaRequest(metaMessage(waAllowedMetaID, "reaction", `,"reaction":{"message_id":"wamid.X","emoji":"👍"}`))
		}, waMetaMsgID, "reaction"},
		{"meta location", whatsapp.TransportMeta, func() *http.Request {
			return signedMetaRequest(metaMessage(waAllowedMetaID, "location", `,"location":{"latitude":-6.2,"longitude":106.8}`))
		}, waMetaMsgID, "location"},
	} {
		t.Run(c.name, func(t *testing.T) {
			hook := logrustest.NewGlobal()
			defer hook.Reset()
			r, phones, wa, turns := newWhatsAppTurnRig(t, c.transport)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, c.req())

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", w.Code)
			}
			if len(wa.sent) != 0 {
				t.Errorf("sent = %q, want nothing", wa.sent)
			}
			if phones.hits != 0 {
				t.Errorf("allowlist consulted %d times for a message that was dropped", phones.hits)
			}
			assertNoTurn(t, turns)
			lines := entriesNamed(hook, waDroppedLine)
			if len(lines) != 1 {
				t.Fatalf("%d %q lines, want 1", len(lines), waDroppedLine)
			}
			if lines[0].Level != logrus.InfoLevel {
				t.Errorf("level = %s, want info", lines[0].Level)
			}
			transport := "twilio"
			if c.transport == whatsapp.TransportMeta {
				transport = "whatsapp_business"
			}
			assertFields(t, lines[0], logrus.Fields{
				"transport": transport, "message_sid": c.wantSID, "message_type": c.wantType,
			})
			assertNothingLeaked(t, hook, waAllowedMetaID, waMediaURL)
		})
	}
}

// AC5, Q5: the order of checks did not move. A forged request with media is a
// 401 before anything reads what it carries.
func TestWhatsAppWebhook_forgedAttachmentIsRefused(t *testing.T) {
	hook := logrustest.NewGlobal()
	defer hook.Reset()
	r, phones, wa, turns := newWhatsAppTurnRig(t, whatsapp.TransportTwilio)

	req := signedTwilioRequest(twilioMediaForm("audio/ogg", ""))
	req.Header.Set("X-Twilio-Signature", base64.StdEncoding.EncodeToString([]byte("not the signature")))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if phones.hits != 0 || len(wa.sent) != 0 {
		t.Errorf("a refused request reached the allowlist %d times and sent %q", phones.hits, wa.sent)
	}
	assertNoTurn(t, turns)
	if n := len(entriesNamed(hook, waAttachmentLine)) + len(entriesNamed(hook, waDroppedLine)); n != 0 {
		t.Errorf("%d attachment or drop lines for a forged request", n)
	}
}

// Meta's delivery receipts carry no message at all. They were silent and stay
// silent: one Info line per sent, delivered and read would bury the lines above.
func TestWhatsAppWebhook_metaStatusCallbackIsSilent(t *testing.T) {
	hook := logrustest.NewGlobal()
	defer hook.Reset()
	r, phones, wa, turns := newWhatsAppTurnRig(t, whatsapp.TransportMeta)

	body := `{"object":"whatsapp_business_account","entry":[{"id":"1","changes":[{"field":"messages","value":{"statuses":[{"id":"wamid.S","status":"delivered","recipient_id":"` + waAllowedMetaID + `"}]}}]}]}`
	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedMetaRequest(body))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if phones.hits != 0 || len(wa.sent) != 0 {
		t.Errorf("a status callback reached the allowlist %d times and sent %q", phones.hits, wa.sent)
	}
	assertNoTurn(t, turns)
	if n := len(entriesNamed(hook, waDroppedLine)); n != 0 {
		t.Errorf("%d drop lines for a status callback", n)
	}
}

// AC6, Q6: the provider is down. Still 200 — a non-2xx is retried, and the
// retry is a second copy of the reply once the outage ends — and a Warn that
// carries the error in place of the Info line.
func TestWhatsAppWebhook_attachmentReplyThatFailsToSendIsStill200(t *testing.T) {
	sendErr := errors.New("twilio error: service unavailable")
	for _, c := range []struct {
		name      string
		transport whatsapp.Transport
		req       func() *http.Request
	}{
		{"twilio", whatsapp.TransportTwilio, func() *http.Request {
			return signedTwilioRequest(twilioMediaForm("audio/ogg", ""))
		}},
		{"meta", whatsapp.TransportMeta, func() *http.Request {
			return signedMetaRequest(metaMessage(waAllowedMetaID, "audio", `,"audio":{"id":"MEDIA1"}`))
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			hook := logrustest.NewGlobal()
			defer hook.Reset()
			r, _, wa, turns := newWhatsAppTurnRig(t, c.transport)
			wa.sendErr = sendErr

			w := httptest.NewRecorder()
			r.ServeHTTP(w, c.req())

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", w.Code)
			}
			if len(wa.sent) != 1 {
				t.Errorf("tried to send %d times, want 1", len(wa.sent))
			}
			assertNoTurn(t, turns)
			warns := entriesNamed(hook, waAttachmentWarn)
			if len(warns) != 1 {
				t.Fatalf("%d %q lines, want 1", len(warns), waAttachmentWarn)
			}
			if warns[0].Level != logrus.WarnLevel {
				t.Errorf("level = %s, want warning", warns[0].Level)
			}
			if got, _ := warns[0].Data[logrus.ErrorKey].(error); !errors.Is(got, sendErr) {
				t.Errorf("error field = %v, want the send error", warns[0].Data[logrus.ErrorKey])
			}
			if warns[0].Data["media_kind"] != "audio" || warns[0].Data["company_id"] != waCompanyID {
				t.Errorf("fields = %v, want the attachment line's", warns[0].Data)
			}
			if n := len(entriesNamed(hook, waAttachmentLine)); n != 0 {
				t.Errorf("%d %q lines for a reply that was not sent", n, waAttachmentLine)
			}
			assertNothingLeaked(t, hook, waAllowedMetaID, waMediaURL)
		})
	}
}
