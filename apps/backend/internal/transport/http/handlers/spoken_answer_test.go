package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/speech"
)

type spokenSvcFake struct {
	calls int
	got   *domain.Message
	err   error
}

func (f *spokenSvcFake) Speak(_ context.Context, _ string, m *domain.Message) (*app.SpokenAudio, error) {
	f.calls++
	f.got = m
	if f.err != nil {
		return nil, f.err
	}
	return &app.SpokenAudio{Body: io.NopCloser(strings.NewReader("ID3frames")), Size: 9, MediaType: "audio/mpeg"}, nil
}

// spokenMessages holds one answer, msg-1, in th-1, belonging to co-1.
type spokenMessages struct{}

func (spokenMessages) GetForCompany(_ context.Context, companyID, id string) (*domain.Message, error) {
	if companyID == "co-1" && id == "msg-1" {
		return &domain.Message{ID: "msg-1", ThreadID: "th-1", Role: domain.MessageRoleAssistant, Content: "ok"}, nil
	}
	return nil, domain.ErrNotFound
}

func spokenRouter(svc *spokenSvcFake, reader ConversationReader) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api")
	g.Use(func(c *gin.Context) {
		c.Set("company_id", "co-1")
		c.Set("user_id", "u-1")
		c.Set("role", "member")
	})
	h := NewSpokenAnswerHandler(svc, spokenMessages{})
	if reader != nil {
		h = h.WithConversationAccess(reader)
	}
	h.Register(g)
	return r
}

func getSpoken(r *gin.Engine, id string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/messages/"+id+"/audio", nil))
	return w
}

func TestSpokenAnswerServesTheAudio(t *testing.T) {
	svc := &spokenSvcFake{}
	w := getSpoken(spokenRouter(svc, voiceReader{allow: true}), "msg-1")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "audio/mpeg" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.HasPrefix(cc, "private") {
		t.Errorf("Cache-Control = %q; a tenant's answer must not be cached by anybody else", cc)
	}
	if w.Body.String() != "ID3frames" || svc.got == nil || svc.got.ID != "msg-1" {
		t.Errorf("body %q, service asked about %+v", w.Body.String(), svc.got)
	}
}

// Another company's answer, and one in a conversation hidden from the caller,
// are not found — and nothing is synthesised or billed for either.
func TestSpokenAnswerHiddenOrForeignAnswerIsNotFoundBeforeAnythingIsSpent(t *testing.T) {
	cases := map[string]struct {
		id     string
		reader ConversationReader
	}{
		"another company's answer": {"msg-2", voiceReader{allow: true}},
		"a hidden conversation":    {"msg-1", voiceReader{allow: false}},
	}
	for name, c := range cases {
		svc := &spokenSvcFake{}
		if w := getSpoken(spokenRouter(svc, c.reader), c.id); w.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", name, w.Code)
		}
		if svc.calls != 0 {
			t.Errorf("%s: the service was reached", name)
		}
	}
}

func TestSpokenAnswerFailuresSayWhatHappened(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		status   int
		contains string
		absent   string
	}{
		{"a refused reduction", &app.SpokenRefusalError{Reason: "spoken 2 juta, nearest written 1.234.567"}, http.StatusUnprocessableEntity, "spoken 2 juta", ""},
		{"a provider down", fmt.Errorf("%w (%v)", app.ErrSynthesisFailed, errors.New("speech provider answered 503: over capacity")), http.StatusBadGateway, "read it instead", "over capacity"},
		{"out of credits", fmt.Errorf("%w: x", domain.ErrInsufficientCredits), http.StatusPaymentRequired, "", ""},
		{"not an answer", app.ErrNotAnAnswer, http.StatusNotFound, "only an agent's answer", ""},
		{"voice out switched off", speech.ErrDisabled, http.StatusNotFound, "", ""},
	}
	for _, c := range cases {
		w := getSpoken(spokenRouter(&spokenSvcFake{err: c.err}, nil), "msg-1")
		if w.Code != c.status {
			t.Errorf("%s: status %d, want %d: %s", c.name, w.Code, c.status, w.Body.String())
		}
		if c.contains != "" && !strings.Contains(w.Body.String(), c.contains) {
			t.Errorf("%s: body %s does not say %q", c.name, w.Body.String(), c.contains)
		}
		if c.absent != "" && strings.Contains(w.Body.String(), c.absent) {
			t.Errorf("%s: body %s repeats the provider's words", c.name, w.Body.String())
		}
	}
}
