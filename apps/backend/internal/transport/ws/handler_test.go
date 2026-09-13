package ws

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/domain"
)

// T-Z10 on the live stream: a conversation the person may not read is refused
// before the upgrade, as a missing thread, so its next answer cannot be watched
// arriving by somebody the thread list hides it from.

type oneThread struct{ domain.ThreadRepository }

func (oneThread) GetByID(_ context.Context, id string) (*domain.ConversationThread, error) {
	if id != "th-1" {
		return nil, domain.ErrNotFound
	}
	return &domain.ConversationThread{ID: "th-1", CompanyID: "co-1"}, nil
}

type readerFunc func(ctx context.Context, companyID, userID, threadID string) (bool, error)

func (f readerFunc) MayRead(ctx context.Context, companyID, userID, threadID string) (bool, error) {
	return f(ctx, companyID, userID, threadID)
}

func streamStatus(t *testing.T, reader ConversationReader) (int, string, []string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	var asked []string
	wrapped := readerFunc(func(ctx context.Context, companyID, userID, threadID string) (bool, error) {
		asked = append(asked, companyID+"/"+userID+"/"+threadID)
		return reader.MayRead(ctx, companyID, userID, threadID)
	})
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("company_id", "co-1")
		c.Set("user_id", "u-1")
		c.Next()
	})
	r.GET("/threads/:id/stream", NewHandler(nil, oneThread{}, nil).WithConversationAccess(wrapped).Stream)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/threads/th-1/stream", nil))
	return w.Code, w.Body.String(), asked
}

func TestTheStreamOfAHiddenConversationIsNotFound(t *testing.T) {
	code, body, asked := streamStatus(t, readerFunc(func(context.Context, string, string, string) (bool, error) {
		return false, nil
	}))
	if code != http.StatusNotFound || !strings.Contains(body, "thread not found") {
		t.Errorf("status = %d %s, want 404 thread not found", code, body)
	}
	if len(asked) != 1 || asked[0] != "co-1/u-1/th-1" {
		t.Errorf("asked %v, want once about co-1/u-1/th-1", asked)
	}
}

func TestAStreamCheckThatFailsRefusesRetryably(t *testing.T) {
	code, _, _ := streamStatus(t, readerFunc(func(context.Context, string, string, string) (bool, error) {
		return false, errors.New("control DB down")
	}))
	if code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", code)
	}
}
