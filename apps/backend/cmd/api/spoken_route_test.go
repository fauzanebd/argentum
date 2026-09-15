package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ingenimax/agent-sdk-go/pkg/interfaces"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/auth"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/speech"
)

// speakingSynth is a synthesiser that always answers. calls, when set, counts
// how often anything reached it.
type speakingSynth struct{ calls *atomic.Int64 }

func (s speakingSynth) Speak(context.Context, string, string) (speech.Audio, error) {
	if s.calls != nil {
		s.calls.Add(1)
	}
	return speech.Audio{Data: []byte("ID3"), MediaType: "audio/mpeg", Ext: "mp3"}, nil
}
func (speakingSynth) Enabled() bool { return true }
func (speakingSynth) Model() string { return "tts-1" }
func (speakingSynth) Voice() string { return "alloy" }

type answeringLLM struct{}

func (answeringLLM) Generate(context.Context, string, ...interfaces.GenerateOption) (string, error) {
	return "ok", nil
}

type noSpokenAnswers struct{}

func (noSpokenAnswers) ForMessage(context.Context, string, string) (*domain.SpokenAnswer, error) {
	return nil, domain.ErrNotFound
}
func (noSpokenAnswers) Save(context.Context, *domain.SpokenAnswer) error { return nil }
func (noSpokenAnswers) Due(context.Context, time.Time, int) ([]*domain.SpokenAnswer, error) {
	return nil, nil
}
func (noSpokenAnswers) Delete(context.Context, string, string) error          { return nil }
func (noSpokenAnswers) DeleteForCompany(context.Context, string) (int, error) { return 0, nil }

type emptyAudioStore struct{}

func (emptyAudioStore) UploadKey(_ context.Context, key string, _ io.Reader, _ string) (string, error) {
	return key, nil
}
func (emptyAudioStore) StreamKey(context.Context, string) (io.ReadCloser, int64, error) {
	return nil, 0, errors.New("no such key")
}
func (emptyAudioStore) RemoveKey(context.Context, string) error { return nil }

func spokenService(synth speech.Synthesizer) *app.SpokenAnswerService {
	return app.NewSpokenAnswerService(synth, answeringLLM{}, noSpokenAnswers{}, emptyAudioStore{}, nil, 0)
}

func audioRequest(t *testing.T, role string) *http.Request {
	t.Helper()
	signer, err := auth.NewTokenSigner("0123456789abcdef0123456789abcdef", 15*time.Minute, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewTokenSigner: %v", err)
	}
	token, err := signer.IssueAccessToken("user-1", "co-1", role)
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/messages/11111111-1111-1111-1111-111111111111/audio", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

// T-W8's acceptance: "A user without the capability gets 403." Member and admin
// alike, and before anything is synthesised.
func TestSpokenAnswerRefusesAnUngrantedPersonBeforeSynthesis(t *testing.T) {
	var calls atomic.Int64
	r := routerWithDeps(t, func(d *apiDeps) { d.spokenSvc = spokenService(speakingSynth{calls: &calls}) })
	for _, role := range []string{"member", "admin"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, audioRequest(t, role))
		if w.Code != http.StatusForbidden {
			t.Errorf("%s without the grant: status %d, want 403: %s", role, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"capability":"voice"`) {
			t.Errorf("%s: the refusal does not name the grant to ask for: %s", role, w.Body.String())
		}
	}
	if calls.Load() != 0 {
		t.Errorf("the synthesiser was reached %d times by people without the grant", calls.Load())
	}
}

// And the grant opens it: past both gates. "Not 403 and not 404", as in
// TestVoiceAdmitsAGrantedPerson — there is no database behind the handler.
func TestSpokenAnswerAdmitsAGrantedPerson(t *testing.T) {
	r := routerWithDeps(t, func(d *apiDeps) { d.capabilitySvc = app.NewCapabilityService(grantsVoice{}, nil) })
	for _, role := range []string{"member", "admin"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, audioRequest(t, role))
		if w.Code == http.StatusForbidden || w.Code == http.StatusNotFound {
			t.Errorf("%s with the grant: status %d: %s", role, w.Code, w.Body.String())
		}
	}
}

// Without a usable synthesiser the route does not exist: the router's 404, for
// a person holding the grant too.
func TestSpokenAnswerRouteIsAbsentWithoutASynthesizer(t *testing.T) {
	r := routerWithDeps(t, func(d *apiDeps) {
		d.capabilitySvc = app.NewCapabilityService(grantsVoice{}, nil)
		d.spokenSvc = spokenService(speech.NewSynthesizer(speech.SynthConfig{Enabled: false}))
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, audioRequest(t, "admin"))
	if w.Code != http.StatusNotFound {
		t.Errorf("voice out off: status %d, want 404: %s", w.Code, w.Body.String())
	}
}
