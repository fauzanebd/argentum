package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/domain"
)

// T-Z10's route half. Which conversations a person may read is decided and
// tested in internal/app; this is that every dashboard route that lists or opens
// one asks, and answers a hidden conversation exactly as a missing one.

// hiddenConversations is a ConversationReader that hides a fixed set.
type hiddenConversations struct {
	hidden map[string]bool
	err    error
}

func (h hiddenConversations) Readable(_ context.Context, _, _ string, ids []string) ([]string, error) {
	if h.err != nil {
		return nil, h.err
	}
	var out []string
	for _, id := range ids {
		if !h.hidden[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

func (h hiddenConversations) MayRead(ctx context.Context, companyID, userID, threadID string) (bool, error) {
	out, err := h.Readable(ctx, companyID, userID, []string{threadID})
	return len(out) == 1, err
}

// twoThreads serves th-open and th-hidden, both co-1's. It embeds the interface
// so any method a route reaches that is not overridden here panics — which is
// the signal that a route did more than it should have for a hidden thread.
type twoThreads struct {
	domain.ThreadRepository
	deleted []string
}

func (s *twoThreads) GetByID(_ context.Context, id string) (*domain.ConversationThread, error) {
	if id == "th-open" || id == "th-hidden" {
		return &domain.ConversationThread{ID: id, CompanyID: "co-1", Title: "title of " + id}, nil
	}
	return nil, domain.ErrNotFound
}

func (s *twoThreads) ListByCompany(context.Context, string, int, int) ([]*domain.ConversationThread, error) {
	return []*domain.ConversationThread{
		{ID: "th-hidden", CompanyID: "co-1", Title: "payroll for March"},
		{ID: "th-open", CompanyID: "co-1", Title: "weekly sales"},
	}, nil
}

func (s *twoThreads) Delete(_ context.Context, id string) error {
	s.deleted = append(s.deleted, id)
	return nil
}

type oneTranscript struct{ domain.MessageRepository }

func (oneTranscript) ListByThread(_ context.Context, threadID string, _, _ int) ([]*domain.Message, error) {
	return []*domain.Message{{ThreadID: threadID, Content: "the answer in " + threadID}}, nil
}

func conversationRouter(t *testing.T, reader ConversationReader) (*gin.Engine, *twoThreads) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	threads := &twoThreads{}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("company_id", "co-1")
		c.Set("user_id", "u-1")
		c.Set("role", "admin") // decision 4: an admin is hidden from too
		c.Next()
	})
	h := NewChatHandler(nil, threads, oneTranscript{})
	if reader != nil {
		h = h.WithConversationAccess(reader)
	}
	h.Register(r.Group(""))
	return r, threads
}

func serveConversation(r *gin.Engine, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func TestTheThreadListOmitsAConversationThePersonMayNotRead(t *testing.T) {
	r, _ := conversationRouter(t, hiddenConversations{hidden: map[string]bool{"th-hidden": true}})
	w := serveConversation(r, http.MethodGet, "/threads")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	var body struct {
		Threads []*domain.ConversationThread `json:"threads"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var ids []string
	for _, th := range body.Threads {
		ids = append(ids, th.ID)
	}
	if !slices.Equal(ids, []string{"th-open"}) {
		t.Errorf("threads = %v, want [th-open]", ids)
	}
	if strings.Contains(w.Body.String(), "payroll") {
		t.Errorf("the hidden conversation's title reached the list: %s", w.Body)
	}
}

// Every route that names a conversation answers a hidden one as a missing one,
// and the delete destroys nothing.
func TestEveryRouteNamingAHiddenConversationAnswersNotFound(t *testing.T) {
	routes := []struct{ method, path string }{
		{http.MethodGet, "/threads/th-hidden"},
		{http.MethodGet, "/threads/th-hidden/messages"},
		{http.MethodDelete, "/threads/th-hidden"},
		{http.MethodGet, "/threads/th-hidden/participants"},
		{http.MethodPost, "/threads/th-hidden/participants"},
		{http.MethodDelete, "/threads/th-hidden/participants/ag-hr"},
	}
	r, threads := conversationRouter(t, hiddenConversations{hidden: map[string]bool{"th-hidden": true}})
	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			w := serveConversation(r, rt.method, rt.path)
			if w.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404: %s", w.Code, w.Body)
			}
			if strings.Contains(w.Body.String(), "the answer") {
				t.Errorf("the transcript reached a refusal: %s", w.Body)
			}
		})
	}
	if len(threads.deleted) != 0 {
		t.Errorf("a hidden conversation was deleted: %v", threads.deleted)
	}

	// The readable one is untouched by the check.
	if w := serveConversation(r, http.MethodGet, "/threads/th-open/messages"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "the answer in th-open") {
		t.Errorf("readable transcript: %d %s", w.Code, w.Body)
	}
}

// A read that failed refuses rather than serving the unfiltered list or the
// transcript.
func TestAConversationCheckThatFailsServesNothing(t *testing.T) {
	r, _ := conversationRouter(t, hiddenConversations{err: errors.New("control DB down")})
	for _, path := range []string{"/threads", "/threads/th-open/messages", "/threads/th-open"} {
		w := serveConversation(r, http.MethodGet, path)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status = %d, want 503", path, w.Code)
		}
		if strings.Contains(w.Body.String(), "weekly sales") || strings.Contains(w.Body.String(), "the answer") {
			t.Errorf("%s: the refusal served content: %s", path, w.Body)
		}
	}
}

// With no reader wired the routes behave as before.
func TestWithNoConversationAccessEveryThreadIsListed(t *testing.T) {
	r, _ := conversationRouter(t, nil)
	w := serveConversation(r, http.MethodGet, "/threads")
	if !strings.Contains(w.Body.String(), "th-hidden") || !strings.Contains(w.Body.String(), "th-open") {
		t.Errorf("unwired list = %s, want both conversations", w.Body)
	}
}
