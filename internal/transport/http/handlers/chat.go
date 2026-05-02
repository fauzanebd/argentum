package handlers

import (
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
	rg.GET("/threads/:id", h.getThread)
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
	Message string `json:"message" binding:"required"`
}

func (h *ChatHandler) sendMessage(c *gin.Context) {
	var req sendReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	uid, _ := c.Get("user_id")
	res, err := h.chat.Enqueue(c.Request.Context(), app.ChatInput{
		Channel:   domain.ChannelDashboard,
		CompanyID: companyID(c),
		UserID:    uid.(string),
		Message:   req.Message,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{
		"task_id":       res.TaskID,
		"thread_id":     res.Thread.ID,
		"is_new_thread": res.IsNewThread,
		"user_msg_id":   res.UserMsgID,
	})
}
