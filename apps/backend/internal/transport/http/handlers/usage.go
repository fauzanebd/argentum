package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// UsageHandler exposes the usage / credits endpoints to the dashboard.
type UsageHandler struct {
	svc *app.UsageService
	// conversations hides the per-conversation usage of a conversation the
	// person may not read (T-Z10). Not only for the cost: each row of the list
	// carries the conversation's title, which is its first question — "payroll
	// for March" is the transcript's first line, served from another route.
	conversations ConversationReader
}

func NewUsageHandler(svc *app.UsageService) *UsageHandler { return &UsageHandler{svc: svc} }

// WithConversationAccess applies ChatHandler's rule to the per-conversation
// usage routes (T-Z10). The company and per-channel totals are unaffected: they
// name no conversation.
func (h *UsageHandler) WithConversationAccess(r ConversationReader) *UsageHandler {
	h.conversations = r
	return h
}

// keepUsageRows is rows narrowed to the conversations in keep, in their order.
func keepUsageRows(rows []*domain.ThreadUsageRow, keep []string) []*domain.ThreadUsageRow {
	allowed := make(map[string]bool, len(keep))
	for _, id := range keep {
		allowed[id] = true
	}
	out := make([]*domain.ThreadUsageRow, 0, len(keep))
	for _, r := range rows {
		if r != nil && allowed[r.ThreadID] {
			out = append(out, r)
		}
	}
	return out
}

// Register installs the routes. Call after Auth middleware.
func (h *UsageHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/usage/summary", h.summary)
	rg.GET("/usage/credits", h.credits)
	rg.GET("/usage/threads", h.listThreads)
	rg.GET("/usage/threads/:id", h.threadSummary)
	rg.GET("/usage/threads/:id/events", h.threadEvents)
	rg.GET("/usage/by-channel", h.byChannel)
	rg.GET("/usage/by-user", h.byUser)
}

func (h *UsageHandler) summary(c *gin.Context) {
	out, err := h.svc.SummaryForCompany(c.Request.Context(), companyID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *UsageHandler) listThreads(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	out, err := h.svc.ListThreadsUsage(c.Request.Context(), companyID(c),
		c.Query("from"), c.Query("to"), limit, offset)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if h.conversations != nil {
		ids := make([]string, 0, len(out))
		for _, r := range out {
			if r != nil {
				ids = append(ids, r.ThreadID)
			}
		}
		keep, err := h.conversations.Readable(c.Request.Context(), companyID(c), userID(c), ids)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "could not check access to conversations; try again"})
			return
		}
		out = keepUsageRows(out, keep)
	}
	c.JSON(http.StatusOK, gin.H{"threads": out})
}

func (h *UsageHandler) threadSummary(c *gin.Context) {
	if !readableConversation(c, h.conversations, c.Param("id")) {
		return
	}
	out, err := h.svc.SummaryForThread(c.Request.Context(), companyID(c),
		c.Param("id"), c.Query("from"), c.Query("to"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *UsageHandler) threadEvents(c *gin.Context) {
	if !readableConversation(c, h.conversations, c.Param("id")) {
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	out, err := h.svc.EventsForThread(c.Request.Context(), companyID(c),
		c.Param("id"), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": out})
}

func (h *UsageHandler) byChannel(c *gin.Context) {
	out, err := h.svc.CostByChannel(c.Request.Context(), companyID(c),
		c.Query("from"), c.Query("to"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"channels": out})
}

func (h *UsageHandler) byUser(c *gin.Context) {
	out, err := h.svc.CostByUser(c.Request.Context(), companyID(c),
		c.Query("from"), c.Query("to"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": out})
}

func (h *UsageHandler) credits(c *gin.Context) {
	out, err := h.svc.CreditsForCompany(c.Request.Context(), companyID(c))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			c.JSON(http.StatusOK, gin.H{"company_id": companyID(c), "balance_micro_usd": 0})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, out)
}
