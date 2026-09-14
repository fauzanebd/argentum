package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

type voiceSvcFake struct {
	calls      int
	got        app.VoiceInput
	out        *app.VoiceTranscription
	err        error
	maxSeconds int
	maxBytes   int64
}

func (f *voiceSvcFake) Transcribe(_ context.Context, in app.VoiceInput) (*app.VoiceTranscription, error) {
	f.calls++
	f.got = in
	return f.out, f.err
}
func (f *voiceSvcFake) MaxClipSeconds() int { return f.maxSeconds }
func (f *voiceSvcFake) MaxClipBytes() int64 { return f.maxBytes }

func newVoiceSvcFake() *voiceSvcFake {
	return &voiceSvcFake{
		out:        &app.VoiceTranscription{Text: "berapa stok", Language: "id", Seconds: 2.5},
		maxSeconds: 60,
		maxBytes:   1 << 20,
	}
}

// voiceThreads holds one conversation, th-1, belonging to co-1.
type voiceThreads struct{}

func (voiceThreads) GetForCompany(_ context.Context, companyID, id string) (*domain.ConversationThread, error) {
	if companyID == "co-1" && id == "th-1" {
		return &domain.ConversationThread{ID: "th-1", CompanyID: "co-1"}, nil
	}
	return nil, domain.ErrNotFound
}

type voiceReader struct{ allow bool }

func (r voiceReader) Readable(_ context.Context, _, _ string, ids []string) ([]string, error) {
	if r.allow {
		return ids, nil
	}
	return nil, nil
}
func (r voiceReader) MayRead(context.Context, string, string, string) (bool, error) {
	return r.allow, nil
}

func voiceRouter(svc *voiceSvcFake, reader ConversationReader) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api")
	g.Use(func(c *gin.Context) {
		c.Set("company_id", "co-1")
		c.Set("user_id", "u-1")
		c.Set("role", "member")
	})
	h := NewVoiceHandler(svc, voiceThreads{})
	if reader != nil {
		h = h.WithConversationAccess(reader)
	}
	h.Register(g)
	return r
}

var voiceWebm = append([]byte{0x1A, 0x45, 0xDF, 0xA3}, bytes.Repeat([]byte{9}, 128)...)

type voiceForm struct {
	audio    []byte
	partType string
	fields   map[string]string
}

func okVoiceForm() voiceForm {
	return voiceForm{audio: voiceWebm, partType: "audio/webm;codecs=opus", fields: map[string]string{"duration_ms": "2500"}}
}

// voiceBodyCounter records how much of a request body the handler read.
type voiceBodyCounter struct {
	r io.Reader
	n *int
}

func (c voiceBodyCounter) Read(p []byte) (int, error) {
	k, err := c.r.Read(p)
	*c.n += k
	return k, err
}

func postVoice(r *gin.Engine, path string, f voiceForm) (*httptest.ResponseRecorder, int) {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	if f.audio != nil {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", `form-data; name="audio"; filename="clip.webm"`)
		h.Set("Content-Type", f.partType)
		part, _ := mw.CreatePart(h)
		_, _ = part.Write(f.audio)
	}
	for k, v := range f.fields {
		_ = mw.WriteField(k, v)
	}
	_ = mw.Close()
	read := 0
	req := httptest.NewRequest(http.MethodPost, path, voiceBodyCounter{r: &b, n: &read})
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w, read
}

func TestVoiceHiddenConversationIsNotFoundBeforeTheRecordingIsRead(t *testing.T) {
	svc := newVoiceSvcFake()
	w, read := postVoice(voiceRouter(svc, voiceReader{allow: false}), "/api/threads/th-1/voice", okVoiceForm())
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", w.Code, w.Body.String())
	}
	if read != 0 || svc.calls != 0 {
		t.Errorf("read %d bytes and called the service %d times for a conversation the caller may not read", read, svc.calls)
	}
}

func TestVoiceAnotherCompanysConversationIsNotFound(t *testing.T) {
	svc := newVoiceSvcFake()
	w, read := postVoice(voiceRouter(svc, nil), "/api/threads/th-9/voice", okVoiceForm())
	if w.Code != http.StatusNotFound || read != 0 || svc.calls != 0 {
		t.Errorf("status %d, %d bytes read, %d calls; want 404, 0, 0", w.Code, read, svc.calls)
	}
}

// "A clip over the cap is refused at the route and never reaches the provider"
// — both caps, and the same sentence for each.
func TestVoiceOverEitherCapNeverReachesTheService(t *testing.T) {
	long := okVoiceForm()
	long.fields["duration_ms"] = "60001"

	svc := newVoiceSvcFake()
	w, _ := postVoice(voiceRouter(svc, nil), "/api/threads/th-1/voice", long)
	if w.Code != http.StatusRequestEntityTooLarge || !strings.Contains(w.Body.String(), "60 seconds or shorter") {
		t.Errorf("declared too long: %d %s", w.Code, w.Body.String())
	}

	big := okVoiceForm()
	big.audio = append(append([]byte{}, voiceWebm...), bytes.Repeat([]byte{1}, 8192)...)
	svc2 := newVoiceSvcFake()
	svc2.maxBytes = 1024
	w2, _ := postVoice(voiceRouter(svc2, nil), "/api/threads/th-1/voice", big)
	if w2.Code != http.StatusRequestEntityTooLarge || !strings.Contains(w2.Body.String(), "60 seconds or shorter") {
		t.Errorf("too many bytes: %d %s", w2.Code, w2.Body.String())
	}
	if svc.calls != 0 || svc2.calls != 0 {
		t.Errorf("the service was called %d and %d times for clips over the cap", svc.calls, svc2.calls)
	}
}

func TestVoiceBytesThatAreNotTheDeclaredAudioAreRefused(t *testing.T) {
	f := okVoiceForm()
	f.audio = []byte("%PDF-1.7\n%âãÏÓ\n1 0 obj")
	svc := newVoiceSvcFake()
	w, _ := postVoice(voiceRouter(svc, nil), "/api/threads/th-1/voice", f)
	if w.Code != http.StatusUnsupportedMediaType || svc.calls != 0 {
		t.Errorf("status %d, %d calls; want 415 and none", w.Code, svc.calls)
	}
}

func TestVoiceNeedsADeclaredLengthAndATwoLetterLanguage(t *testing.T) {
	noLength := okVoiceForm()
	delete(noLength.fields, "duration_ms")
	badLanguage := okVoiceForm()
	badLanguage.fields["language"] = "indonesian"
	noAudio := okVoiceForm()
	noAudio.audio = nil

	for name, f := range map[string]voiceForm{"no length": noLength, "bad language": badLanguage, "no audio": noAudio} {
		svc := newVoiceSvcFake()
		w, _ := postVoice(voiceRouter(svc, nil), "/api/threads/th-1/voice", f)
		if w.Code != http.StatusBadRequest || svc.calls != 0 {
			t.Errorf("%s: status %d, %d calls; want 400 and none", name, w.Code, svc.calls)
		}
	}
}

func TestVoiceReturnsTheTranscriptAndWhatWasHeardFrom(t *testing.T) {
	expires := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	svc := newVoiceSvcFake()
	svc.out.Clip = &domain.VoiceClip{ID: "clip-1", ExpiresAt: expires}
	f := okVoiceForm()
	f.fields["language"] = "EN"

	w, _ := postVoice(voiceRouter(svc, voiceReader{allow: true}), "/api/threads/th-1/voice", f)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var body VoiceTranscriptionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Transcript != "berapa stok" || body.ClipID != "clip-1" || body.ExpiresAt == nil || !body.ExpiresAt.Equal(expires) {
		t.Errorf("body = %+v", body)
	}
	got := svc.got
	if got.CompanyID != "co-1" || got.UserID != "u-1" || got.ThreadID != "th-1" {
		t.Errorf("identity came from somewhere other than the session: %+v", got)
	}
	if got.MediaType != "audio/webm" || got.Ext != "webm" || got.DeclaredSeconds != 2.5 || got.Language != "en" {
		t.Errorf("input = type %q ext %q %v s lang %q", got.MediaType, got.Ext, got.DeclaredSeconds, got.Language)
	}
	if !bytes.Equal(got.Audio, voiceWebm) {
		t.Errorf("audio = %d bytes, want the %d uploaded", len(got.Audio), len(voiceWebm))
	}
}

func TestVoiceWithNoClipOmitsTheClipFields(t *testing.T) {
	svc := newVoiceSvcFake()
	w, _ := postVoice(voiceRouter(svc, nil), "/api/threads/th-1/voice", okVoiceForm())
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "clip_id") || strings.Contains(w.Body.String(), "expires_at") {
		t.Errorf("status %d, body %s", w.Code, w.Body.String())
	}
}

// "A provider failure returns an error the UI can show": what to do, and not a
// word of what the provider said.
func TestVoiceProviderFailureSaysWhatToDoAndNotWhatTheProviderSaid(t *testing.T) {
	svc := newVoiceSvcFake()
	svc.out = nil
	svc.err = fmt.Errorf("%w (%v)", app.ErrTranscriptionFailed, errors.New("speech provider answered 429: quota exceeded for org-7f3"))
	w, _ := postVoice(voiceRouter(svc, nil), "/api/threads/th-1/voice", okVoiceForm())
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "type your question") {
		t.Errorf("status %d, body %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "quota") || strings.Contains(w.Body.String(), "org-7f3") {
		t.Errorf("the provider's words reached the response: %s", w.Body.String())
	}
}

func TestVoiceOutOfCreditsIsPaymentRequired(t *testing.T) {
	svc := newVoiceSvcFake()
	svc.out = nil
	svc.err = fmt.Errorf("%w: %s", domain.ErrInsufficientCredits, app.CreditsExhaustedMessage)
	w, _ := postVoice(voiceRouter(svc, nil), "/api/threads/th-1/voice", okVoiceForm())
	if w.Code != http.StatusPaymentRequired {
		t.Errorf("status = %d, want 402", w.Code)
	}
}
