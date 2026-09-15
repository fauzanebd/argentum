package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/domain"
)

type fakeSpokenReader struct {
	calls int
	args  []string
	ids   []string
	out   *domain.SpokenQuestion
	err   error
}

func (f *fakeSpokenReader) SpokenQuestion(_ context.Context, companyID, userID, threadID string, ids []string, sent string) (*domain.SpokenQuestion, error) {
	f.calls++
	f.args = []string{companyID, userID, threadID, sent}
	f.ids = ids
	return f.out, f.err
}

// sessionContext is a request the Auth middleware has already let through as u-1
// of co-1.
func sessionContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/chat", nil)
	c.Set("company_id", "co-1")
	c.Set("user_id", "u-1")
	return c
}

// T-W9: the send asks about the clips it names as the person in the session. A
// body cannot say whose clips they are, so it cannot name somebody else's.
func TestSendAsksAboutTheClipsItNamesAsTheSessionsPerson(t *testing.T) {
	want := &domain.SpokenQuestion{ClipIDs: []string{"c-1"}, Verbatim: true}
	reader := &fakeSpokenReader{out: want}
	h := (&ChatHandler{}).WithVoiceClips(reader)

	got := h.spokenQuestion(sessionContext(), sendReq{Message: "berapa stok", ThreadID: "th-1", VoiceClipIDs: []string{"c-1"}})
	if got != want {
		t.Errorf("record = %+v, want the reader's %+v", got, want)
	}
	if wantArgs := []string{"co-1", "u-1", "th-1", "berapa stok"}; !reflect.DeepEqual(reader.args, wantArgs) {
		t.Errorf("asked with %v, want %v", reader.args, wantArgs)
	}
	if !reflect.DeepEqual(reader.ids, []string{"c-1"}) {
		t.Errorf("asked about %v", reader.ids)
	}
}

// A send that opens a conversation cannot name one of its clips, a send naming no
// clip is typed, and a wiring without a reader records nothing: none of the three
// looks anything up.
func TestSendThatCannotNameAClipDoesNotLook(t *testing.T) {
	reader := &fakeSpokenReader{out: &domain.SpokenQuestion{ClipIDs: []string{"c-1"}}}
	h := (&ChatHandler{}).WithVoiceClips(reader)
	for name, req := range map[string]sendReq{
		"a new conversation": {Message: "x", VoiceClipIDs: []string{"c-1"}},
		"no clips":           {Message: "x", ThreadID: "th-1"},
	} {
		if got := h.spokenQuestion(sessionContext(), req); got != nil {
			t.Errorf("%s: record %+v, want none", name, got)
		}
	}
	if reader.calls != 0 {
		t.Errorf("looked up %d times, want 0", reader.calls)
	}
	if got := (&ChatHandler{}).spokenQuestion(sessionContext(), sendReq{Message: "x", ThreadID: "th-1", VoiceClipIDs: []string{"c-1"}}); got != nil {
		t.Errorf("no reader: record %+v, want none", got)
	}
}

// A lookup that fails costs the record and not the message (decision 15): the
// send goes ahead as if the message had been typed.
func TestSendWhoseClipLookupFailsGoesAheadWithoutTheRecord(t *testing.T) {
	h := (&ChatHandler{}).WithVoiceClips(&fakeSpokenReader{err: errors.New("connection refused")})
	if got := h.spokenQuestion(sessionContext(), sendReq{Message: "x", ThreadID: "th-1", VoiceClipIDs: []string{"c-1"}}); got != nil {
		t.Errorf("record %+v, want none", got)
	}
}
