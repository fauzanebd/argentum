package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/speech"
)

// answeringTranscriber is a provider that always hears "ok". calls, when set,
// counts how often anything reached it.
type answeringTranscriber struct{ calls *atomic.Int64 }

func (a answeringTranscriber) Transcribe(context.Context, io.Reader, string, string) (speech.Transcript, error) {
	if a.calls != nil {
		a.calls.Add(1)
	}
	return speech.Transcript{Text: "ok", Seconds: 1}, nil
}
func (answeringTranscriber) Enabled() bool { return true }
func (answeringTranscriber) Model() string { return "test-model" }

// grantsVoice is a capability store in which everybody holds voice.
type grantsVoice struct{}

func (grantsVoice) ListForUser(_ context.Context, _, userID string) ([]domain.CapabilityGrant, error) {
	return []domain.CapabilityGrant{{UserID: userID, Capability: domain.CapabilityVoice}}, nil
}
func (grantsVoice) Grant(context.Context, string, string, domain.Capability, string) error {
	return nil
}
func (grantsVoice) Revoke(context.Context, string, string, domain.Capability) error { return nil }

// readCounter is a request body that records how much of it anything read.
type readCounter struct {
	r io.Reader
	n *atomic.Int64
}

func (c readCounter) Read(p []byte) (int, error) {
	k, err := c.r.Read(p)
	c.n.Add(int64(k))
	return k, err
}
func (readCounter) Close() error { return nil }

func voiceRequest(t *testing.T, role string, read *atomic.Int64) *http.Request {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="audio"; filename="clip.webm"`)
	h.Set("Content-Type", "audio/webm")
	part, _ := mw.CreatePart(h)
	_, _ = part.Write(append([]byte{0x1A, 0x45, 0xDF, 0xA3}, bytes.Repeat([]byte{7}, 256)...))
	_ = mw.WriteField("duration_ms", "1500")
	_ = mw.Close()

	signer, err := auth.NewTokenSigner("0123456789abcdef0123456789abcdef", 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}
	token, err := signer.IssueAccessToken("user-1", "co-1", role)
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/threads/11111111-1111-1111-1111-111111111111/voice", nil)
	req.Body = readCounter{r: &b, n: read}
	req.ContentLength = int64(b.Len())
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

// T-W7's acceptance: "A user without the voice capability gets 403 before any
// audio is read." Asserted on the body itself — not a byte of the recording is
// read — and on the provider, which is never reached. Member and admin alike:
// a rank is not a grant.
func TestVoiceRefusesAnUngrantedPersonBeforeReadingTheRecording(t *testing.T) {
	var calls atomic.Int64
	r := routerWithDeps(t, func(d *apiDeps) {
		d.voiceSvc = app.NewVoiceService(answeringTranscriber{calls: &calls}, nil, nil, nil, 0, 0)
	})
	for _, role := range []string{"member", "admin"} {
		var read atomic.Int64
		w := httptest.NewRecorder()
		r.ServeHTTP(w, voiceRequest(t, role, &read))
		if w.Code != http.StatusForbidden {
			t.Errorf("%s without the grant: status %d, want 403: %s", role, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"capability":"voice"`) {
			t.Errorf("%s: the refusal does not name the grant to ask for: %s", role, w.Body.String())
		}
		if n := read.Load(); n != 0 {
			t.Errorf("%s: %d bytes of the recording were read before the refusal", role, n)
		}
	}
	if calls.Load() != 0 {
		t.Errorf("the provider was reached %d times by people without the grant", calls.Load())
	}
}

// And the grant is what opens it: the same request, granted, gets past both
// gates. "Not 403 and not 404" rather than 200, as in TestMemberRoutesAdmitMembers
// — there is no database behind the handler here.
func TestVoiceAdmitsAGrantedPerson(t *testing.T) {
	r := routerWithDeps(t, func(d *apiDeps) { d.capabilitySvc = app.NewCapabilityService(grantsVoice{}, nil) })
	for _, role := range []string{"member", "admin"} {
		var read atomic.Int64
		w := httptest.NewRecorder()
		r.ServeHTTP(w, voiceRequest(t, role, &read))
		if w.Code == http.StatusForbidden || w.Code == http.StatusNotFound {
			t.Errorf("%s with the grant: status %d: %s", role, w.Code, w.Body.String())
		}
	}
}

// "A deployment with SPEECH_ENABLED=false boots, logs once, and the route 404s."
// The route is not registered, so the answer is the router's own 404 — for a
// person holding the grant, and for one who does not, who is not told a grant
// would help.
func TestVoiceRouteIsAbsentWithoutAProvider(t *testing.T) {
	for name, store := range map[string]domain.CapabilityRepository{
		"granted":   grantsVoice{},
		"ungranted": &noCapabilities{},
	} {
		r := routerWithDeps(t, func(d *apiDeps) {
			d.capabilitySvc = app.NewCapabilityService(store, nil)
			d.voiceSvc = app.NewVoiceService(speech.New(speech.Config{Enabled: false}), nil, nil, nil, 0, 0)
		})
		var read atomic.Int64
		w := httptest.NewRecorder()
		r.ServeHTTP(w, voiceRequest(t, "admin", &read))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s, speech off: status %d, want 404: %s", name, w.Code, w.Body.String())
		}
	}
}

// T-W9: what `me/capabilities` tells the dashboard about voice is what the router
// did. With a provider the voice route answers and the read says transcribe, with
// its limit; without one the route is the router's 404 and the read says no. A
// microphone drawn from this read cannot offer a route that is not there, or
// hide one that is.
func TestMyCapabilitiesSayWhatTheVoiceRoutesAre(t *testing.T) {
	signer, err := auth.NewTokenSigner("0123456789abcdef0123456789abcdef", 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}
	token, err := signer.IssueAccessToken("user-1", "co-1", "member")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	for _, tc := range []struct {
		name        string
		transcriber speech.Transcriber
		want        bool
	}{
		{"a provider", answeringTranscriber{}, true},
		{"no provider", speech.New(speech.Config{Enabled: false}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := routerWithDeps(t, func(d *apiDeps) {
				d.capabilitySvc = app.NewCapabilityService(grantsVoice{}, nil)
				d.voiceSvc = app.NewVoiceService(tc.transcriber, nil, nil, nil, 45, 0)
			})
			var read atomic.Int64
			route := httptest.NewRecorder()
			r.ServeHTTP(route, voiceRequest(t, "member", &read))
			if registered := route.Code != http.StatusNotFound; registered != tc.want {
				t.Fatalf("voice route registered = %v (status %d), want %v", registered, route.Code, tc.want)
			}

			req := httptest.NewRequest(http.MethodGet, "/api/users/me/capabilities", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("me/capabilities: status %d: %s", w.Code, w.Body.String())
			}
			var body struct {
				Voice struct {
					Transcribe     bool `json:"transcribe"`
					ReadAloud      bool `json:"read_aloud"`
					MaxClipSeconds int  `json:"max_clip_seconds"`
				} `json:"voice"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Voice.Transcribe != tc.want {
				t.Errorf("transcribe = %v, want %v: %s", body.Voice.Transcribe, tc.want, w.Body.String())
			}
			wantMax := 0
			if tc.want {
				wantMax = 45
			}
			if body.Voice.MaxClipSeconds != wantMax {
				t.Errorf("max_clip_seconds = %d, want %d", body.Voice.MaxClipSeconds, wantMax)
			}
		})
	}
}
