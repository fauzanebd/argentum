package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
)

// ChatHandler exposes /threads + /chat endpoints to the dashboard.
type ChatHandler struct {
	chat       *app.ChatEnqueuer
	threads    domain.ThreadRepository
	messages   domain.MessageRepository
	dashboards *app.DashboardService
}

func NewChatHandler(chat *app.ChatEnqueuer, threads domain.ThreadRepository, messages domain.MessageRepository, dashboards *app.DashboardService) *ChatHandler {
	return &ChatHandler{chat: chat, threads: threads, messages: messages, dashboards: dashboards}
}

// Register installs the routes. Caller wraps with Auth middleware.
func (h *ChatHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/threads", h.listThreads)
	rg.POST("/threads", h.createThread)
	rg.GET("/threads/:id", h.getThread)
	rg.DELETE("/threads/:id", h.deleteThread)
	rg.GET("/threads/:id/messages", h.listMessages)
	rg.POST("/chat", h.sendMessage)
}

func (h *ChatHandler) listThreads(c *gin.Context) {
	cid := companyID(c)
	out, err := h.threads.ListByCompany(c.Request.Context(), cid, 100, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"threads": out})
}

func (h *ChatHandler) createThread(c *gin.Context) {
	thread, err := h.chat.CreateDashboardThread(c.Request.Context(), companyID(c), userID(c))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, thread)
}

func (h *ChatHandler) getThread(c *gin.Context) {
	thread, err := h.threads.GetByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if thread.CompanyID != companyID(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	c.JSON(http.StatusOK, thread)
}

func (h *ChatHandler) deleteThread(c *gin.Context) {
	thread, err := h.threads.GetByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if thread.CompanyID != companyID(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	if h.dashboards != nil {
		_ = h.dashboards.DeleteByThread(c.Request.Context(), thread.CompanyID, thread.ID)
	}
	if err := h.threads.Delete(c.Request.Context(), thread.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *ChatHandler) listMessages(c *gin.Context) {
	thread, err := h.threads.GetByID(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if thread.CompanyID != companyID(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	msgs, err := h.messages.ListByThread(c.Request.Context(), thread.ID, 200, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"messages": msgs})
}

type sendReq struct {
	Message  string `json:"message" binding:"required"`
	ThreadID string `json:"thread_id,omitempty"`
}

func (h *ChatHandler) sendMessage(c *gin.Context) {
	var req sendReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	res, err := h.chat.Enqueue(c.Request.Context(), app.ChatInput{
		Channel:   domain.ChannelDashboard,
		CompanyID: companyID(c),
		UserID:    userID(c),
		Message:   req.Message,
		ThreadID:  req.ThreadID,
	})
	if err != nil {
		// 402 rather than 400: the request was well-formed and the caller can
		// fix this, which is exactly what Payment Required means. T-A1's
		// error envelope reuses this status for the same condition.
		if errors.Is(err, domain.ErrInsufficientCredits) {
			c.JSON(http.StatusPaymentRequired, gin.H{"error": app.CreditsExhaustedMessage})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	out := gin.H{
		"task_id":       res.TaskID,
		"thread_id":     res.Thread.ID,
		"is_new_thread": res.IsNewThread,
		"user_msg_id":   res.UserMsgID,
	}
	if res.BudgetWarning != nil {
		out["budget_warning"] = res.BudgetWarning
	}
	c.JSON(http.StatusAccepted, out)
}
