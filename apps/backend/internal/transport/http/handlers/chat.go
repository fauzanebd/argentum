package handlers

import (
	"context"
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
	// members is the room (T-N2). Nil is legal and is every wiring that has
	// not been given one: the participant routes then answer 503 and every
	// other route on this handler behaves exactly as it did before, which is
	// what keeps this ticket inert until it is wired.
	members *app.ThreadParticipantService
	// conversations hides a conversation from a person who may not talk to
	// every agent in it (T-Z10). Nil shows every conversation to every member,
	// as before.
	conversations ConversationReader
}

// ConversationReader is whether a person may read conversations (T-Z10), for
// every dashboard route that lists, opens or streams one.
// *app.ConversationAccess is the production one, and is nil-safe.
type ConversationReader interface {
	Readable(ctx context.Context, companyID, userID string, threadIDs []string) ([]string, error)
	MayRead(ctx context.Context, companyID, userID, threadID string) (bool, error)
}

func NewChatHandler(chat *app.ChatEnqueuer, threads domain.ThreadRepository, messages domain.MessageRepository) *ChatHandler {
	return &ChatHandler{chat: chat, threads: threads, messages: messages}
}

// WithParticipants enables the membership routes (T-N2). Optional, in the
// shape ChatEnqueuer.WithRoster uses and for the same reason: a wiring that
// does not supply one must keep working unchanged.
func (h *ChatHandler) WithParticipants(s *app.ThreadParticipantService) *ChatHandler {
	h.members = s
	return h
}

// WithConversationAccess hides conversations by grant (T-Z10).
//
// **Hidden, not refused.** The list omits a conversation the person may not
// read, and every route that names one by id answers it as not found — the
// same 404, body and all, as an id that never existed. That is T-Z4's picker
// rule: what the product does not offer a person it does not confirm to them
// either. It is the same for an admin, who manages grants and does not bypass
// them (decision 4).
func (h *ChatHandler) WithConversationAccess(r ConversationReader) *ChatHandler {
	h.conversations = r
	return h
}

// readable reports whether the caller may read threadID, and writes the answer
// when they may not. A 503 for a read that failed: the conversation may well be
// theirs, and "not found" would say otherwise.
func readableConversation(c *gin.Context, r ConversationReader, threadID string) bool {
	if r == nil {
		return true
	}
	ok, err := r.MayRead(c.Request.Context(), companyID(c), userID(c), threadID)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "could not check access to that conversation; try again"})
		return false
	}
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return false
	}
	return true
}

// Register installs the routes. Caller wraps with Auth middleware.
func (h *ChatHandler) Register(rg *gin.RouterGroup) {
	rg.GET("/threads", h.listThreads)
	rg.POST("/threads", h.createThread)
	rg.GET("/threads/:id", h.getThread)
	rg.DELETE("/threads/:id", h.deleteThread)
	rg.GET("/threads/:id/messages", h.listMessages)
	// The room (T-N2). `:agentID` and not `:id` on the delete: gin's tree
	// requires one name per position per method, and the position already
	// belongs to a thread id two segments up.
	rg.GET("/threads/:id/participants", h.listParticipants)
	rg.POST("/threads/:id/participants", h.addParticipant)
	rg.DELETE("/threads/:id/participants/:agentID", h.removeParticipant)
	rg.POST("/chat", h.sendMessage)
}

// participantsUnavailable answers the routes when no service was wired.
func (h *ChatHandler) participantsUnavailable(c *gin.Context) bool {
	if h.members != nil {
		return false
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "conversation participants are not configured"})
	return true
}

// participantFail maps the membership sentinels onto status codes.
//
// The three service-specific ones are 409 rather than 400: each says the
// request was well-formed and the conversation's current state refuses it,
// and each names the action that would make it succeed.
func participantFail(c *gin.Context, err error) {
	switch {
	case errors.Is(err, app.ErrParticipantLimit),
		errors.Is(err, app.ErrDefaultSpeaker),
		errors.Is(err, app.ErrAgentDisabled),
		errors.Is(err, app.ErrRoomNotOnWidget),
		errors.Is(err, domain.ErrAlreadyExists):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, app.ErrAccessCheckFailed):
		// The sentinel's own sentence, never the wrapped database error.
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": app.ErrAccessCheckFailed.Error()})
	case errors.Is(err, domain.ErrNotFound):
		// 404 and not 403 for another company's thread or agent, which is what
		// chatFail does one function up and for the same reason — and for an
		// agent the person may not talk to (T-Z4), which the add menu never
		// offered them.
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	case errors.Is(err, domain.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

func (h *ChatHandler) listParticipants(c *gin.Context) {
	// Readability first: a hidden conversation is not found whatever this
	// deployment has wired, and its room is part of it.
	if !readableConversation(c, h.conversations, c.Param("id")) || h.participantsUnavailable(c) {
		return
	}
	out, err := h.members.List(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil {
		participantFail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"participants": out})
}

// addParticipantReq is the body of POST /threads/:id/participants.
type addParticipantReq struct {
	AgentID string `json:"agent_id"`
}

func (h *ChatHandler) addParticipant(c *gin.Context) {
	if !readableConversation(c, h.conversations, c.Param("id")) || h.participantsUnavailable(c) {
		return
	}
	var req addParticipantReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	p, err := h.members.Add(c.Request.Context(), companyID(c), c.Param("id"), req.AgentID, userID(c))
	if err != nil {
		participantFail(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"participant": p})
}

func (h *ChatHandler) removeParticipant(c *gin.Context) {
	if !readableConversation(c, h.conversations, c.Param("id")) || h.participantsUnavailable(c) {
		return
	}
	err := h.members.Remove(c.Request.Context(), companyID(c), c.Param("id"), c.Param("agentID"))
	if err != nil {
		participantFail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *ChatHandler) listThreads(c *gin.Context) {
	cid := companyID(c)
	out, err := h.threads.ListByCompany(c.Request.Context(), cid, 100, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if h.conversations != nil {
		// Filtered after the page is read, so a page can come back shorter
		// than its limit. The list has never paged — it is the latest hundred —
		// and a sidebar a few conversations short is the honest cost of not
		// pushing grants into the listing query.
		ids := make([]string, 0, len(out))
		for _, t := range out {
			ids = append(ids, t.ID)
		}
		keep, err := h.conversations.Readable(c.Request.Context(), cid, userID(c), ids)
		if err != nil {
			// Refused, not served unfiltered: the unfiltered list is the
			// thing this person was not supposed to see.
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "could not check access to conversations; try again"})
			return
		}
		out = keepThreads(out, keep)
	}
	c.JSON(http.StatusOK, gin.H{"threads": out})
}

// keepThreads is threads narrowed to the ids in keep, in their own order.
func keepThreads(threads []*domain.ConversationThread, keep []string) []*domain.ConversationThread {
	allowed := make(map[string]bool, len(keep))
	for _, id := range keep {
		allowed[id] = true
	}
	out := make([]*domain.ConversationThread, 0, len(keep))
	for _, t := range threads {
		if t != nil && allowed[t.ID] {
			out = append(out, t)
		}
	}
	return out
}

// createThreadReq is the body of POST /threads. Every field is optional: the
// dashboard's "New conversation" button still posts nothing at all.
type createThreadReq struct {
	// AgentID pins the conversation to one of the company's agents (T-S3).
	// Absent means the company default, resolved per turn. It is also the
	// conversation's **default speaker** (T-N2) — who answers a message that
	// addresses nobody.
	AgentID string `json:"agent_id,omitempty"`
	// ParticipantIDs are the other agents in the room (T-N2). Absent is the
	// ordinary case and makes a room of one, which is every conversation this
	// product has ever created.
	ParticipantIDs []string `json:"participant_ids,omitempty"`
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
	// The rest of the room (T-N2). After the thread exists, because a
	// participant needs something to belong to, and **failing the whole
	// creation if one of them is refused** — the alternative is a conversation
	// that silently opened with fewer agents than the user picked, which they
	// would discover by addressing one and being told it is not here.
	//
	// The thread is left behind rather than rolled back: it is a valid empty
	// conversation with a default speaker, it costs nothing, and the user's
	// next action is to try again. A delete here would be a second write that
	// can also fail.
	for _, id := range req.ParticipantIDs {
		if h.members == nil {
			break
		}
		if _, err := h.members.Add(c.Request.Context(), companyID(c), thread.ID, id, userID(c)); err != nil {
			participantFail(c, err)
			return
		}
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
	case errors.Is(err, app.ErrAgentNotInRoom):
		// 409 and not 404: the agent exists and the caller may have it, and the
		// action that would make this succeed — add it to the conversation — is
		// one they can take. The message names the agent, because "not in this
		// conversation" without saying which one is not actionable.
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, app.ErrConversationNotFound):
		// Before ErrNotFound, which it wraps, so the body names the right
		// thing: a hidden conversation reads as a missing one (T-Z10), not as
		// a missing agent.
		c.JSON(http.StatusNotFound, gin.H{"error": "no such conversation"})
	case errors.Is(err, app.ErrAgentRestricted), errors.Is(err, app.ErrNoAgentAvailable):
		// 403 with the sentence (T-Z4). These are refusals about an agent the
		// person already knows — the one their conversation runs as, or one in
		// the room — so the message can name it, and a 404 would be a lie about
		// an agent whose answers are on the screen.
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	case errors.Is(err, app.ErrAccessCheckFailed):
		// 503, and the sentinel's sentence rather than the wrapped database
		// error: retry, do not go and ask an admin for a grant you may hold.
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": app.ErrAccessCheckFailed.Error()})
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
	if !readableConversation(c, h.conversations, thread.ID) {
		return
	}
	// The room, on the detail read only (T-N2). The listing route deliberately
	// does not do this: a sidebar needs a title, and a second query per row to
	// render one is a cost paid on every page load.
	//
	// A failure here is logged by the repository and dropped rather than
	// failing the read. Participants make a conversation richer; they are never
	// what makes it openable, and refusing to show a thread because a join
	// missed would trade a slightly poorer screen for no screen at all — the
	// argument ChatRunner.companyContext already makes for the company profile.
	if h.members != nil {
		if ps, err := h.members.List(c.Request.Context(), thread.CompanyID, thread.ID); err == nil {
			thread.Participants = make([]domain.ThreadParticipant, 0, len(ps))
			for _, p := range ps {
				thread.Participants = append(thread.Participants, *p)
			}
		}
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
	// Deleting what you may not read would destroy the conversation of
	// somebody who may.
	if !readableConversation(c, h.conversations, thread.ID) {
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
	if !readableConversation(c, h.conversations, thread.ID) {
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
