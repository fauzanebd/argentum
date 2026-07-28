package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/domain"
)

// AuditHandler exposes the agent action log (T-05). Admin-only via apiPolicy:
// the log holds every SQL statement the agent ran, which is a sharper read of
// the tenant's own warehouse than any other endpoint offers.
type AuditHandler struct{ repo domain.AgentActionRepository }

func NewAuditHandler(repo domain.AgentActionRepository) *AuditHandler {
	return &AuditHandler{repo: repo}
}

// Register installs the routes. Call after Auth middleware.
func (h *AuditHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/audit/actions", h.listActions)
}

func (h *AuditHandler) listActions(c *gin.Context) {
	from, to, err := parseAuditWindow(c.Query("from"), c.Query("to"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "from/to must be RFC3339 timestamps"})
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))

	actions, err := h.repo.ListByCompany(c.Request.Context(), companyID(c), domain.AgentActionFilter{
		From:     from,
		To:       to,
		ThreadID: c.Query("thread_id"),
		Tool:     c.Query("tool"),
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if actions == nil {
		actions = []*domain.AgentAction{}
	}
	c.JSON(http.StatusOK, gin.H{"actions": actions})
}

// parseAuditWindow mirrors the usage endpoints' window: RFC3339, defaulting to
// the last 30 days. Two audit surfaces that disagree about what `from` means
// is a bug report waiting to be filed.
func parseAuditWindow(fromStr, toStr string) (time.Time, time.Time, error) {
	to := time.Now().UTC()
	from := to.AddDate(0, 0, -30)
	if fromStr != "" {
		t, err := time.Parse(time.RFC3339, fromStr)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		from = t
	}
	if toStr != "" {
		t, err := time.Parse(time.RFC3339, toStr)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		to = t
	}
	return from, to, nil
}
