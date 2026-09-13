package handlers

import (
	"net/http"
	"slices"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/domain"
)

// T-Z10 on the usage routes: the per-conversation list carries each
// conversation's title, which is its first question, so it is narrowed like the
// thread list, and the per-conversation reads refuse a hidden one before the
// service is reached.

func TestTheUsageListIsNarrowedToReadableConversations(t *testing.T) {
	rows := []*domain.ThreadUsageRow{
		{ThreadID: "th-hidden", Title: "payroll for March"},
		{ThreadID: "th-open", Title: "weekly sales"},
		nil,
	}
	got := keepUsageRows(rows, []string{"th-open"})
	if len(got) != 1 || got[0].ThreadID != "th-open" {
		t.Errorf("keepUsageRows = %+v, want only th-open", got)
	}
}

func TestPerConversationUsageOfAHiddenConversationIsNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("company_id", "co-1")
		c.Set("user_id", "u-1")
		c.Next()
	})
	// No service at all: a refusal must happen before one is needed.
	NewUsageHandler(nil).
		WithConversationAccess(hiddenConversations{hidden: map[string]bool{"th-hidden": true}}).
		Register(r.Group(""))

	var codes []int
	for _, path := range []string{"/usage/threads/th-hidden", "/usage/threads/th-hidden/events"} {
		codes = append(codes, serveConversation(r, http.MethodGet, path).Code)
	}
	if !slices.Equal(codes, []int{http.StatusNotFound, http.StatusNotFound}) {
		t.Errorf("statuses = %v, want 404 for both", codes)
	}
}
