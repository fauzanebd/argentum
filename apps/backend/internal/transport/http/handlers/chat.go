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
	chat     *app.ChatEnqueuer
	threads  domain.ThreadRepository
	messages domain.MessageRepository
}

func NewChatHandler(chat *app.ChatEnqueuer, threads domain.ThreadRepository, messages domain.MessageRepository) *ChatHandler {
	return &ChatHandler{chat: chat, threads: threads, messages: messages}
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

// createThreadReq is the body of POST /threads. Every field is optional: the
// dashboard's "New conversation" button still posts nothing at all.
type createThreadReq struct {
	// AgentID pins the conversation to one of the company's agents (T-S3).
	// Absent means the company default, resolved per turn.
	AgentID string `json:"agent_id,omitempty"`
}

func (h *ChatHandler) createThread(c *gin.Context) {
	var req createThreadReq
	// An empty body is the ordinary case — the button that opens a chat sends
	// none — so a decode failure is only an error when there was something to
	// decode. ShouldBindJSON reports EOF for the empty body, which is not one.
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	thread, err := h.chat.CreateDashboardThread(c.Request.Context(), companyID(c), userID(c), req.AgentID)
	if err != nil {
		chatFail(c, err)
		return
	}
	c.JSON(http.StatusCreated, thread)
}

// chatFail maps the enqueue path's sentinel errors onto status codes.
//
// ErrNotFound is 404 for an agent belonging to another company as much as for
// one that never existed, matching what agentFail does on the roster's own
// routes — a 403 would confirm the row is real to a caller holding a bare uuid.
func chatFail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "no such agent"})
	case errors.Is(err, domain.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	}
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
	// AgentID applies to the send that *opens* a conversation — the dashboard
	// creates the thread and sends the first message in one call (T-S3). On a
	// send that names an existing thread it must match what the thread already
	// runs as, or the enqueuer refuses it.
	AgentID string `json:"agent_id,omitempty"`
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
		AgentID:   req.AgentID,
	})
	if err != nil {
		// 402 rather than 400: the request was well-formed and the caller can
		// fix this, which is exactly what Payment Required means. T-A1's
		// error envelope reuses this status for the same condition.
		if errors.Is(err, domain.ErrInsufficientCredits) {
			c.JSON(http.StatusPaymentRequired, gin.H{"error": app.CreditsExhaustedMessage})
			return
		}
		chatFail(c, err)
		return
	}
	// A declared struct rather than a gin.H, so the dashboard's TypeScript for
	// this shape is generated rather than hand-mirrored (T-02b). The JSON is
	// identical: `budget_warning` is a nil pointer with `omitempty` in the
	// ordinary case, exactly as the map omitted the key.
	c.JSON(http.StatusAccepted, SendMessageResponse{
		TaskID:        res.TaskID,
		ThreadID:      res.Thread.ID,
		IsNewThread:   res.IsNewThread,
		UserMsgID:     res.UserMsgID,
		BudgetWarning: res.BudgetWarning,
	})
}
