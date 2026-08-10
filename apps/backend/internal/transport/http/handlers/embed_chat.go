package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
)

// EmbedChatHandler is the widget's whole API surface (T-20).
//
// **Five routes, and nothing else.** No connections, no settings, no usage, no
// metrics, no audit, no documents. The list is short because every route on it
// is reachable from a page we do not control, by a person who has no account
// with us — so the question for each one is not "would this be useful?" but
// "is a visitor of a tenant's website entitled to it?". Adding a sixth is a
// decision, not a convenience.
//
// Every read is scoped to the session's own `embed_user_ref`. That is the
// check the Gelael pilot had to write by hand against `/v1` and would have
// leaked a colleague's conversation without — see
// docs/coverage/gelael-pilot.md §3.1.
type EmbedChatHandler struct {
	chat     *app.ChatEnqueuer
	threads  domain.ThreadRepository
	messages domain.MessageRepository
	// agents is the roster, so the widget can offer a picker. Reused from
	// `GET /v1/agents` rather than declared again: one narrow interface, two
	// consumers, and nothing here can reach the rest of AgentService.
	agents V1RosterLister
	// config is the tenant's own greeting, prompts and theme (T-23). Nil is a
	// supported wiring: the route then serves Argentum's defaults, which is
	// also what a tenant who has configured nothing gets.
	config domain.WidgetConfigStore
}

// WithConfig gives the widget the tenant's own appearance and content (T-23).
// Additive, like APIKeysHandler.WithTraffic: a wiring without it serves exactly
// what T-20 served.
func (h *EmbedChatHandler) WithConfig(store domain.WidgetConfigStore) *EmbedChatHandler {
	h.config = store
	return h
}

func NewEmbedChatHandler(
	chat *app.ChatEnqueuer, threads domain.ThreadRepository,
	messages domain.MessageRepository, agents V1RosterLister,
) *EmbedChatHandler {
	return &EmbedChatHandler{chat: chat, threads: threads, messages: messages, agents: agents}
}

// Register installs the routes. The caller wraps the group with
// middleware.EmbedAuth and the per-visitor rate limiter; the stream is
// registered by the router because it needs the WebSocket handler.
func (h *EmbedChatHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/config", h.getConfig)
	rg.POST("/chat", h.send)
	rg.GET("/threads/current", h.currentThread)
	rg.GET("/threads/:id/messages", h.listMessages)
}

// embedThreadResponse is the thread as the widget sees it. Deliberately not
// domain.ConversationThread: that struct carries every channel's routing keys,
// and a widget has no business learning that this workspace also answers in a
// Slack channel with a particular id.
type embedThreadResponse struct {
	ID       string            `json:"id"`
	Title    string            `json:"title,omitempty"`
	AgentID  string            `json:"agent_id,omitempty"`
	IsNew    bool              `json:"is_new"`
	Messages []*domain.Message `json:"messages,omitempty"`
}

type embedSendReq struct {
	Message  string `json:"message" binding:"required"`
	ThreadID string `json:"thread_id,omitempty"`
	AgentID  string `json:"agent_id,omitempty"`
}

// send starts one turn. The identity comes off the session token, never off
// the body — a browser that could name its own `user_ref` could name anybody's.
func (h *EmbedChatHandler) send(c *gin.Context) {
	if h.chat == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "chat is not configured on this deployment"})
		return
	}
	var req embedSendReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message is required"})
		return
	}

	res, err := h.chat.Enqueue(c.Request.Context(), app.ChatInput{
		Channel:      domain.ChannelWidget,
		CompanyID:    companyID(c),
		EmbedUserRef: middleware.EmbedUserRef(c),
		EmbedKeyID:   c.GetString(middleware.CtxEmbedKeyID),
		Message:      req.Message,
		ThreadID:     req.ThreadID,
		AgentID:      req.AgentID,
	})
	if err != nil {
		embedChatFail(c, err)
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"task_id":       res.TaskID,
		"thread_id":     res.Thread.ID,
		"is_new_thread": res.IsNewThread,
		"user_msg_id":   res.UserMsgID,
	})
}

// embedChatFail maps the enqueue path's failures onto statuses a widget can
// act on.
//
// The credit refusal is the one worth reading twice. It is a 402 carrying the
// same plain sentence every other channel uses, because the person who sees it
// is a visitor of somebody else's website: they cannot top up an account they
// do not have, and a stack trace or a raw `budget_exhausted` code tells them
// nothing they can do. The tenant learns about it from their own usage page.
func embedChatFail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrInsufficientCredits):
		c.JSON(http.StatusPaymentRequired, gin.H{"error": app.CreditsExhaustedMessage})
	case errors.Is(err, domain.ErrNotFound):
		// Covers both an agent this workspace cannot use and a thread that is
		// not this visitor's. One answer for both, deliberately.
		c.JSON(http.StatusNotFound, gin.H{"error": "no such conversation"})
	case errors.Is(err, app.ErrAgentChange):
		c.JSON(http.StatusConflict, gin.H{"error": "that conversation runs as a different agent"})
	case errors.Is(err, domain.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not start that turn"})
	}
}

// currentThread resolves this visitor's live conversation and its transcript,
// which is what the widget asks for when it opens.
//
// It does **not** create one. A page that mounts the widget on every route
// would otherwise write an empty thread per page view, and the first message is
// where a conversation actually begins — `send` with no `thread_id` opens it.
func (h *EmbedChatHandler) currentThread(c *gin.Context) {
	ref := middleware.EmbedUserRef(c)
	if ref == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid embed session"})
		return
	}
	thread, err := h.threads.LatestForEmbedUser(c.Request.Context(), companyID(c), ref)
	if errors.Is(err, domain.ErrNotFound) {
		// Not a 404: "you have no conversation yet" is the ordinary state of a
		// visitor who has never typed anything, and an error would make the
		// widget's empty state look like a failure.
		c.JSON(http.StatusOK, gin.H{"thread": nil})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read that conversation"})
		return
	}

	out := embedThreadResponse{ID: thread.ID, Title: thread.Title, AgentID: thread.AgentID}
	if h.messages != nil {
		if msgs, err := h.messages.ListByThread(c.Request.Context(), thread.ID, embedHistoryLimit, 0); err == nil {
			out.Messages = msgs
		}
	}
	c.JSON(http.StatusOK, gin.H{"thread": out})
}

// embedHistoryLimit bounds a transcript read. Generous enough that a returning
// visitor sees their conversation and bounded so one page load cannot ask for
// a year of it.
const embedHistoryLimit = 100

// listMessages serves one conversation's transcript, scoped three ways.
func (h *EmbedChatHandler) listMessages(c *gin.Context) {
	thread, ok := h.ownThread(c)
	if !ok {
		return
	}
	msgs, err := h.messages.ListByThread(c.Request.Context(), thread.ID, embedHistoryLimit, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not read that conversation"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"messages": msgs})
}

// ownThread loads the thread named in the path and proves it belongs to this
// session. Company, channel and ref, all three: the company is the tenant
// boundary, the channel stops a widget reading a staff conversation, and the
// ref is the per-visitor boundary that only this surface needs — because only
// this surface hands the thread id to a browser on somebody else's page.
//
// Every failure is one 404. A visitor must not be able to tell "no such
// thread" from "not yours", or the route enumerates the workspace.
func (h *EmbedChatHandler) ownThread(c *gin.Context) (*domain.ConversationThread, bool) {
	ref := middleware.EmbedUserRef(c)
	thread, err := h.threads.GetForCompany(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil || thread == nil ||
		thread.Channel != domain.ChannelWidget ||
		ref == "" || thread.EmbedUserRef != ref {
		c.JSON(http.StatusNotFound, gin.H{"error": "no such conversation"})
		return nil, false
	}
	return thread, true
}

// getConfig is what the widget renders itself with before anyone has typed:
// the greeting, the suggested prompts, the theme, the agents it may offer.
//
// It carries no tenant data and no credit position — a page source is a public
// place, and "how much credit does Acme have left" is not something a visitor
// of their website should be able to read out of a network tab. domain.
// WidgetConfig's own comment states the test for adding a field.
func (h *EmbedChatHandler) getConfig(c *gin.Context) {
	cfg := domain.WidgetConfig{}
	if h.config != nil {
		if stored, err := h.config.GetWidgetConfig(c.Request.Context(), companyID(c)); err == nil && stored != nil {
			cfg = *stored
		}
		// A read failure is not fatal and not reported: the widget's job is to
		// render, and a tenant whose settings row is briefly unreadable should
		// get Argentum's defaults rather than an empty panel.
	}
	out := gin.H{"config": cfg.WithDefaults()}

	if h.agents != nil {
		if list, err := h.agents.List(c.Request.Context(), companyID(c)); err == nil {
			agents := make([]gin.H, 0, len(list))
			for _, a := range list {
				if !a.Enabled {
					continue
				}
				// Name and id only. A persona is the tenant's prompt
				// engineering and their tool allowlist is a map of what the
				// agent can reach; neither belongs in a browser.
				agents = append(agents, gin.H{"id": a.ID, "name": a.Name, "is_default": a.IsDefault})
			}
			out["agents"] = agents
		}
	}
	c.JSON(http.StatusOK, out)
}
