package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/idempotency"
	"github.com/fauzanebd/argentum/internal/transport/eventbus"
	"github.com/fauzanebd/argentum/internal/transport/http/apierr"
	"github.com/fauzanebd/argentum/internal/transport/http/apiv1"
	"github.com/fauzanebd/argentum/internal/transport/http/middleware"
)

// V1ChatHandler is chat over the public API (T-A3): a question in, an answer
// out, either streamed or waited for.
//
// A report is an artefact and a question is a conversation, so this is a
// different product from `/v1/reports` even though both run the same agent.
// The part a caller cannot build around if it is wrong is the streaming
// contract, which is why the event names here are the ones the dashboard's
// WebSocket has always emitted rather than a second vocabulary invented for
// HTTP — one worker publishes both.
//
// **A client disconnect never cancels a turn.** The agent runs in the worker
// process; this handler is a reader of the same Redis pub/sub the dashboard
// reads. Hanging up ends the read, the turn finishes, the answer persists, and
// the next call collects it. Cancelling on disconnect would make a flaky
// network cost the tenant money for nothing.
type V1ChatHandler struct {
	chat     V1ChatEnqueuer
	threads  domain.ThreadRepository
	messages domain.MessageRepository
	usage    V1TurnUsageReader
	rdb      *redis.Client
	idem     idempotency.Store
	// syncTimeout caps how long the synchronous door holds a connection. It is
	// deliberately a cap on the *wait*, never on the turn.
	syncTimeout time.Duration
	// heartbeat is heartbeatEvery in production and is never configured. It is
	// a field only so a test can watch a beat arrive without sleeping for one.
	heartbeat time.Duration
	// opener opens a conversation before its first question, and members is the
	// room it holds (T-N10). Both nil is legal and is every wiring that predates
	// rooms: `POST /v1/threads` answers a typed 503, a thread read carries no
	// `participants`, and every other route behaves exactly as before.
	opener  V1ThreadOpener
	members V1Room
}

// V1ThreadOpener is the one thing `POST /v1/threads` asks of the enqueue path
// (T-N10): check every agent a new conversation would hold, then open it.
// *app.ChatEnqueuer is the production one.
//
// Separate from V1ChatEnqueuer rather than a second method on it, because the
// reports handler takes that interface too and has no conversation to open.
type V1ThreadOpener interface {
	OpenAPIThread(ctx context.Context, in app.APIThreadInput) (*domain.ConversationThread, error)
}

// V1Room is the membership half of a conversation `/v1` reads and writes
// (T-N10). *app.ThreadParticipantService is the production one, and it is the
// same service the dashboard's room routes use, so the cap, the widget refusal
// and the tenant check are one set of rules on both surfaces.
type V1Room interface {
	List(ctx context.Context, companyID, threadID string) ([]*domain.ThreadParticipant, error)
	Add(ctx context.Context, companyID, threadID, agentID, addedBy string) (*domain.ThreadParticipant, error)
	Capacity() int
}

// WithRooms enables `POST /v1/threads` and the room on a thread read (T-N10).
// Optional, in the shape ChatHandler.WithParticipants uses and for its reason.
func (h *V1ChatHandler) WithRooms(opener V1ThreadOpener, members V1Room) *V1ChatHandler {
	h.opener, h.members = opener, members
	return h
}

// V1ChatEnqueuer is the half of app.ChatEnqueuer the `/v1` handlers use:
// resolve the thread, persist the question, hand the turn to the worker.
//
// An interface rather than the concrete *app.ChatEnqueuer, because the
// concrete one needs a live asynq client to do anything at all — and a
// contract that can only be exercised against a running queue is a contract
// nothing tests until a customer does. The report handler took the concrete
// type until T-A2b, whose whole subject is *what* gets enqueued.
type V1ChatEnqueuer interface {
	Enqueue(ctx context.Context, in app.ChatInput) (*app.EnqueueResult, error)
}

// V1TurnUsageReader is the narrow half of the usage repository this handler
// needs. Declared at the consumer, like ChatEnqueuer's BudgetChecker and
// V1MeHandler's V1BudgetReader, so that reporting what a turn cost cannot turn
// into a second way of computing it.
type V1TurnUsageReader interface {
	SummaryByThread(ctx context.Context, companyID, threadID string, from, to time.Time) (*domain.UsageSummary, error)
}

func NewV1ChatHandler(
	chat V1ChatEnqueuer,
	threads domain.ThreadRepository,
	messages domain.MessageRepository,
	usage V1TurnUsageReader,
	rdb *redis.Client,
	idem idempotency.Store,
	syncTimeout time.Duration,
) *V1ChatHandler {
	if syncTimeout <= 0 {
		syncTimeout = 120 * time.Second
	}
	return &V1ChatHandler{
		chat: chat, threads: threads, messages: messages, usage: usage,
		rdb: rdb, idem: idem, syncTimeout: syncTimeout,
		heartbeat: heartbeatEvery,
	}
}

// heartbeatEvery is the SSE keepalive interval. Fifteen seconds because the
// common load balancer idle timeout is sixty and a stream that dies silently
// mid-turn is indistinguishable, from the caller's side, from a turn that
// produced nothing.
const heartbeatEvery = 15 * time.Second

// maxResumeReplay bounds how much transcript a resumed stream re-sends before
// it gives up and attaches live. A resume is meant to cover a dropped
// connection, not to be a second way of reading `GET /threads/:id/messages`.
const maxResumeReplay = 500

// Register installs the routes on a group already carrying APIKeyAuth.
//
// The scope split is the ticket's: `write:chat` to spend, `read:threads` to
// read. A key minted for an internal reporting job must not be able to run up
// an LLM bill, and a key that drives a chat widget in the tenant's admin panel
// does not need to be able to delete conversations it did not start.
//
// `DELETE` takes `write:chat` rather than `read:threads` for that reason —
// destroying a conversation is not a read. It is not a third scope because
// scopes are fixed at a key's creation: minting `write:threads` now would
// leave every key issued since T-13 unable to do something its holder would
// reasonably expect, and the tenant is the one who has to edit their CI config.
func (h *V1ChatHandler) Register(rg *gin.RouterGroup) {
	write := middleware.RequireScope(domain.ScopeWriteChat)
	read := middleware.RequireScope(domain.ScopeReadThreads)

	// Idempotency is required here for the same reason it is on the report
	// doors: a turn spends the tenant's credits, and a caller who has not
	// thought about retries has a duplicate-billing bug waiting for its first
	// network blip. It is also what makes the 504 below safe to retry.
	rg.POST("/chat", write,
		middleware.Idempotency(h.idem,
			middleware.IdempotencyRequired(),
			middleware.IdempotencyReplayWith(h.replayChat)),
		h.send)

	rg.GET("/threads", read, h.listThreads)
	// A conversation opened before its first question, so a room can be set up
	// (T-N10). `write:chat`, because it writes. The idempotency key is honoured
	// and not required, which is the other doors' rule read the other way: they
	// require one because a retry would bill twice, and a retry here opens a
	// second empty conversation that costs nothing and can be deleted.
	rg.POST("/threads", write, middleware.Idempotency(h.idem), h.createThread)
	rg.GET("/threads/:id", read, h.getThread)
	rg.GET("/threads/:id/messages", read, h.listMessages)
	// The resume door. A 504 from the synchronous send and a `409
	// request_in_flight` from a retried one both hand back a `thread_id`, and
	// neither is collectable by re-POSTing: one turn is still running, and
	// starting a second is the thing the whole idempotency chain exists to
	// prevent. This is where a caller goes instead.
	rg.GET("/threads/:id/events", read, h.streamThread)
	rg.DELETE("/threads/:id", write, h.deleteThread)
}

// chatRequest is the send body.
type chatRequest struct {
	Message string `json:"message"`
	// ThreadID continues a conversation the caller is already tracking.
	ThreadID string `json:"thread_id,omitempty"`
	// UserRef is the tenant's own identifier for the person this turn is on
	// behalf of. It keys the thread when no ThreadID is given, and it is what
	// makes the spend attributable in `usage/by-user` — the report a tenant
	// reads to police their own integration.
	UserRef string `json:"user_ref,omitempty"`
	// AgentID names which of the company's agents answers (T-S5). Omitted is
	// the company default, which is what every call made before this field
	// existed keeps meaning.
	//
	// It applies to the conversation, not to the turn: sent with a `thread_id`
	// it must agree with what that conversation already runs as, and sent with
	// a `user_ref` whose newest conversation runs as a different agent it
	// starts a new one rather than reinterpreting a transcript produced under
	// different tools and sources.
	AgentID string `json:"agent_id,omitempty"`
}

// turnRecord is what the idempotency store remembers about a send: three
// strings, no payload.
//
// StartedAt is on it because a replay has to know *which* turn it is replaying.
// Without it, re-attaching to a thread that has since answered a later question
// would hand the caller the wrong answer and call it theirs — the same failure
// DocumentRepository.NewestForThreadSince exists to avoid.
//
// AgentID is the fourth (T-N10): the agent the turn was sent to, when there is
// exactly one, so a 504's `in_flight` can say whose answer is coming. It is not
// what scopes the stream — see own.
type turnRecord struct {
	ThreadID  string    `json:"thread_id"`
	RunID     string    `json:"run_id,omitempty"`
	AgentID   string    `json:"agent_id,omitempty"`
	StartedAt time.Time `json:"started_at"`
	// own marks a turn this caller sent, as opposed to one they attached to
	// (T-N10). In a room the thread's channel also carries a colleague's turn,
	// under the same job id and often finishing first, and a sent turn's stream
	// must end on the caller's `final`, not the colleague's.
	//
	// **Scoped by who asked, not by agent.** The first cut compared agent ids,
	// and was wrong twice over. A turn whose agent is deleted before it runs runs
	// as the default, so nothing ever matched and the stream hung. And an agent
	// asked back by the colleague it asked carries the caller's agent id, so its
	// `final` matched when it should not. A colleague's turn says so itself
	// (ChatEvent.AskedBy, `metadata.asked_by`), whatever agent it runs as.
	//
	// Not stored: only send and its replay produce a sent turn, and both set it.
	own bool
}

// scope is which answer ends this turn: the caller's own on a sent turn, the
// newest non-room answer when attaching.
func (rec turnRecord) scope() domain.AnswerScope {
	if rec.own {
		return domain.OwnAnswer
	}
	return domain.AnyAnswer
}

// send is `POST /v1/chat`.
func (h *V1ChatHandler) send(c *gin.Context) {
	if h.chat == nil {
		apierr.Abort(c, apierr.TypeServer, "chat_unavailable",
			"Chat is not available on this deployment.")
		return
	}
	var req chatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.Abort(c, apierr.TypeInvalidRequest, "invalid_request",
			"The request body is not valid JSON.")
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		apierr.AbortParam(c, apierr.TypeInvalidRequest, "message_required",
			"Send a `message` — the question you want answered.", "message")
		return
	}
	if req.UserRef == "" && req.ThreadID == "" {
		apierr.AbortParam(c, apierr.TypeInvalidRequest, "user_ref_required",
			"Send a `user_ref` identifying who is asking, or a `thread_id` to continue an existing conversation.", "user_ref")
		return
	}
	// Validated before the turn starts, so a caller who sent a broken
	// Last-Event-ID is told so instead of silently getting a stream with no
	// history in it. After the 200 an SSE response has already committed to,
	// there is no status left to say it with.
	resumeFrom, resumeID, ok := h.resumePoint(c)
	if !ok {
		return
	}

	// The instant before the turn exists. Everything that later asks "has this
	// turn answered?" asks it relative to this, so it is taken before the
	// enqueue rather than after: a clock read on the far side of a queue write
	// can land after the worker has already appended the answer.
	startedAt := time.Now()

	res, err := h.chat.Enqueue(c.Request.Context(), app.ChatInput{
		Channel:    domain.ChannelAPI,
		CompanyID:  companyID(c),
		APIUserRef: req.UserRef,
		ThreadID:   req.ThreadID,
		AgentID:    strings.TrimSpace(req.AgentID),
		Message:    req.Message,
		APIKeyID:   c.GetString(middleware.CtxAPIKeyID),
		// What this key may run as (T-Z8). Checked in the enqueue path, beside
		// the grant checks the other doors make.
		KeyAgentIDs: middleware.APIKeyAgents(c),
	})
	if err != nil {
		h.abortEnqueue(c, err)
		return
	}

	// A sent turn: its stream ends on the caller's own answer and never on a
	// colleague's (T-N10). `/v1` never fans out — only the dashboard's `@`
	// addresses more than one agent — so there is exactly one such answer.
	rec := turnRecord{ThreadID: res.Thread.ID, RunID: res.UserMsgID, StartedAt: startedAt, own: true}
	if len(res.AgentIDs) == 1 {
		// Named in a 504's `in_flight`, so a caller told to attach knows whose
		// answer is coming.
		rec.AgentID = res.AgentIDs[0]
	}
	// Written through to Redis immediately, so a retry arriving while this turn
	// is still running gets a 409 naming the thread it is already waiting on
	// rather than starting a second billed turn.
	middleware.DeclareIdempotentProgress(c, h.idem, rec)
	middleware.DeclareIdempotentResult(c, rec)

	if wantsSSE(c) {
		h.stream(c, rec, resumeFrom, resumeID)
		return
	}
	h.wait(c, rec)
}

// abortEnqueue maps a refused turn onto the envelope.
//
// A budget refusal is a typed 402 rather than a 500 for the reason T-A2 gives:
// a programmatic caller retries a 500, and retrying a turn the tenant cannot
// pay for is a loop that stops only when somebody notices.
func (h *V1ChatHandler) abortEnqueue(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrInsufficientCredits):
		apierr.Abort(c, apierr.TypeBudgetExhausted, "credits_exhausted", app.CreditsExhaustedMessage)
	// Both agent cases go above the thread cases: they wrap the same two
	// sentinels, and a caller who sent a bad `agent_id` must not be sent to
	// look at their `thread_id`.
	case errors.Is(err, app.ErrAgentNotFound):
		abortAgentNotFound(c)
	case errors.Is(err, app.ErrAgentChange):
		apierr.AbortParam(c, apierr.TypeInvalidRequest, "agent_mismatch",
			"That conversation already runs as a different agent. Start a new one by sending `user_ref` without a `thread_id`.", "agent_id")
	case errors.Is(err, app.ErrAgentNotAllowed):
		abortAgentNotAllowed(c)
	case errors.Is(err, domain.ErrInvalidInput):
		apierr.AbortParam(c, apierr.TypeInvalidRequest, "invalid_thread",
			"That `thread_id` is not an API thread for this company.", "thread_id")
	case errors.Is(err, domain.ErrNotFound):
		apierr.AbortParam(c, apierr.TypeNotFound, "thread_not_found",
			"No such thread for this company.", "thread_id")
	default:
		logrus.WithError(err).WithField("company_id", companyID(c)).Error("enqueue api chat turn")
		apierr.Abort(c, apierr.TypeServer, "turn_failed", "The turn could not be started.")
	}
}

// replayChat answers a retried `POST /v1/chat` under a key whose original
// already finished.
//
// It is the reason the idempotency middleware takes a Replayer at all: a
// streamed answer has no bytes to store and replay, so a replay here means
// re-deriving — re-reading the persisted transcript for the answer, or
// re-attaching to the turn's pubsub channel if it has not landed yet. Both
// doors go through the identical code the original used, which is the only way
// the second response can be trusted to have the shape of the first.
func (h *V1ChatHandler) replayChat(c *gin.Context, rec *idempotency.Record) bool {
	var stored turnRecord
	if err := json.Unmarshal(rec.Result, &stored); err != nil || stored.ThreadID == "" {
		return false
	}
	// Only a send is replayed, so the replay is scoped as the send was (T-N10).
	stored.own = true
	resumeFrom, resumeID, ok := h.resumePoint(c)
	if !ok {
		return true
	}
	if wantsSSE(c) {
		h.stream(c, stored, resumeFrom, resumeID)
		return true
	}
	h.wait(c, stored)
	return true
}

// resumePoint reads `Last-Event-ID`, the header an SSE client sends by itself
// when it reconnects.
//
// It decodes to the last *persisted* point the client saw, because only
// persisted frames carry an `id:`. Token deltas do not: they exist nowhere but
// in the connection that carried them, so pinning a resume point to one would
// promise a replay this system cannot perform. What a resumed stream gets back
// is the messages it missed, which is the part that was real.
func (h *V1ChatHandler) resumePoint(c *gin.Context) (time.Time, string, bool) {
	raw := strings.TrimSpace(c.GetHeader("Last-Event-ID"))
	if raw == "" {
		return time.Time{}, "", true
	}
	return decodeCursorOrAbort(c, raw, "Last-Event-ID")
}

// stream writes the SSE response for one turn.
func (h *V1ChatHandler) stream(c *gin.Context, rec turnRecord, resumeFrom time.Time, resumeID string) {
	if h.rdb == nil {
		apierr.Abort(c, apierr.TypeServer, "streaming_unavailable",
			"Event streaming is not available on this deployment.")
		return
	}
	sseStart(c)

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	if resumeID != "" && !h.replayHistory(c, ctx, rec.ThreadID, resumeFrom, resumeID) {
		return
	}

	pubsub := h.rdb.Subscribe(ctx, eventbus.ChannelFor(rec.ThreadID))
	defer func() { _ = pubsub.Close() }()
	// Wait for the subscription to be live before anything else; a subscriber
	// that has not finished subscribing receives nothing and knows it did not.
	if _, err := pubsub.Receive(ctx); err != nil {
		logrus.WithError(err).Warn("chat SSE subscribe failed")
		sseEvent(c, "", "error", gin.H{
			"message": "The event stream could not be opened. Read the thread's messages instead.",
		})
		return
	}

	// The turn can finish between the enqueue and the SUBSCRIBE above — and on
	// a replay it has usually finished long before. Redis pub/sub keeps nothing
	// for a subscriber who was not there, so without this check the caller
	// would hold a connection open waiting for a `final` that was published
	// into an empty room. The persisted transcript is the durable half of the
	// stream, and this is where the two are reconciled.
	if msg, err := h.messages.LatestAssistantSince(ctx, rec.ThreadID, rec.StartedAt, rec.scope()); err == nil {
		h.sendFinal(c, rec, msg, "")
		return
	}

	events := pubsub.Channel()
	ticker := time.NewTicker(h.heartbeat)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// The caller hung up. The turn keeps running in the worker; that is
			// the point.
			return
		case <-ticker.C:
			if !sseComment(c, "heartbeat") {
				return
			}
		case msg, ok := <-events:
			if !ok {
				return
			}
			var evt app.ChatEvent
			if err := json.Unmarshal([]byte(msg.Payload), &evt); err != nil {
				continue
			}
			if !h.forward(c, ctx, rec, &evt) {
				return
			}
		}
	}
}

// forward writes one bus event as an SSE frame. It returns false when the
// stream is over — either the connection broke or the turn reached a terminal
// event.
//
// The names are the dashboard's, unchanged: `started`, `delta`, `thinking`,
// `tool_call`, `tool_result`, `error`, `final`. One worker publishes both
// surfaces, and a second vocabulary here would be a translation layer that has
// to be kept in step with a schema nobody wrote down.
//
// `iteration` is deliberately not forwarded. It is the SDK's own loop counter,
// it means nothing outside this codebase, and a public contract that leaks it
// is a public contract that has to keep emitting it.
//
// **In a room, only the caller's turn is forwarded (T-N10).** The thread's
// channel also carries the turn of a colleague the caller's agent asked
// mid-turn, under the same job id; forwarding it would interleave two answers'
// deltas in one stream and, whenever the colleague finished first, end the
// stream on the colleague's `final` and hand it over as the caller's answer.
// The colleague's answer is still in the transcript, attributed. A room's own
// lines (`room_event`) are not forwarded either; they are in the transcript too.
func (h *V1ChatHandler) forward(c *gin.Context, ctx context.Context, rec turnRecord, evt *app.ChatEvent) bool {
	if rec.own && evt.AskedBy != "" {
		return true
	}
	switch evt.Type {
	case "started":
		return sseEvent(c, "", "started", withAgent(gin.H{
			"thread_id": rec.ThreadID,
			"run_id":    firstNonEmpty(rec.RunID, evt.JobID),
			"at":        evt.Timestamp.UTC().Format(time.RFC3339),
		}, evt))
	case "delta":
		return sseEvent(c, "", "delta", withAgent(gin.H{"content": evt.Content}, evt))
	case "thinking":
		return sseEvent(c, "", "thinking", withAgent(gin.H{"step": evt.ThinkingStep}, evt))
	case "tool_call", "tool_result":
		// The tool's name, never its arguments or its result. Those carry the
		// SQL the agent ran, and the place for that is T-05's audit log, where
		// it is redacted on the way in and reachable only by an admin. A
		// progress stream is for what is happening, not for what was queried.
		body := gin.H{}
		if evt.ToolCall != nil {
			body["tool"] = evt.ToolCall.Name
		}
		return sseEvent(c, "", evt.Type, withAgent(body, evt))
	case "error":
		sseEvent(c, "", "error", withAgent(gin.H{"message": evt.Error}, evt))
		return false
	case "final":
		// The assistant message is persisted before `final` is published, so it
		// is already readable here. Reading it rather than echoing the event
		// gives the frame a real message id — which is what a client sends back
		// as Last-Event-ID — and a usage block the event does not carry.
		if msg, err := h.messages.LatestAssistantSince(ctx, rec.ThreadID, rec.StartedAt, rec.scope()); err == nil {
			h.sendFinal(c, rec, msg, evt.JobID)
			return false
		}
		// The answer is real even when the lookup fails, so it is sent with no
		// `id:` — the client's resume point stays where it was, and a reconnect
		// replays the message from the log rather than losing it.
		sseEvent(c, "", "final", turnBody(rec, evt.JobID, messageResponse{
			Object:    "message",
			Role:      string(domain.MessageRoleAssistant),
			Content:   evt.Content,
			AgentID:   evt.AgentID,
			AgentName: evt.AgentName,
		}, nil, ""))
		return false
	}
	return true
}

// withAgent stamps who is speaking onto a frame (T-N10), and leaves a frame from
// an unscoped turn exactly as it was. A caller that reads neither field is
// unaffected — the property T-Q10 relied on when `next_steps` rode in on
// `final`, and T-N1's when it put the agent on every ChatEvent.
func withAgent(body gin.H, evt *app.ChatEvent) gin.H {
	if evt.AgentID != "" {
		body["agent_id"] = evt.AgentID
	}
	if evt.AgentName != "" {
		body["agent_name"] = evt.AgentName
	}
	return body
}

// sendFinal writes the terminal frame, carrying the persisted message and what
// the turn cost.
func (h *V1ChatHandler) sendFinal(c *gin.Context, rec turnRecord, msg *domain.Message, runID string) {
	sseEvent(c,
		apiv1.EncodeCursor(msg.CreatedAt, msg.ID),
		"final",
		turnBody(rec, runID, messageBody(msg), h.turnUsage(c, rec), ""))
}

// replayHistory re-sends the persisted messages a reconnecting client missed.
// It returns false when the connection broke while it was doing so.
func (h *V1ChatHandler) replayHistory(c *gin.Context, ctx context.Context, threadID string, from time.Time, fromID string) bool {
	f := domain.MessageFilter{CursorTime: from, CursorID: fromID, Limit: 100}
	sent := 0
	for sent < maxResumeReplay {
		rows, hasMore, err := h.messages.ListPageByThread(ctx, threadID, f)
		if err != nil {
			logrus.WithError(err).WithField("thread_id", threadID).
				Warn("resume replay failed; attaching live instead")
			return true
		}
		for _, m := range rows {
			if !sseEvent(c, apiv1.EncodeCursor(m.CreatedAt, m.ID), "message", messageBody(m)) {
				return false
			}
			sent++
			f.CursorTime, f.CursorID = m.CreatedAt, m.ID
		}
		if !hasMore || len(rows) == 0 {
			return true
		}
	}
	return true
}

// wait is the synchronous door: block until the turn answers, or until the
// wait — not the turn — runs out.
func (h *V1ChatHandler) wait(c *gin.Context, rec turnRecord) {
	if h.rdb == nil {
		apierr.Abort(c, apierr.TypeServer, "streaming_unavailable",
			"Synchronous chat is not available on this deployment.")
		return
	}
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	pubsub := h.rdb.Subscribe(ctx, eventbus.ChannelFor(rec.ThreadID))
	defer func() { _ = pubsub.Close() }()
	if _, err := pubsub.Receive(ctx); err != nil {
		logrus.WithError(err).Warn("chat sync subscribe failed")
		h.abortPending(c, rec, "The turn was started but its result could not be waited on. Collect it from the thread.")
		return
	}
	// Same reconciliation as the stream: a turn that finished before the
	// subscription was live published into an empty room.
	if msg, err := h.messages.LatestAssistantSince(ctx, rec.ThreadID, rec.StartedAt, rec.scope()); err == nil {
		h.writeTurn(c, rec, msg)
		return
	}

	events := pubsub.Channel()
	timer := time.NewTimer(h.syncTimeout)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			// The caller hung up mid-wait. Nothing to answer, and nothing to
			// stop: the worker finishes and the answer persists.
			return
		case <-timer.C:
			h.abortPending(c, rec,
				"This turn is taking longer than the synchronous window. It is still running — stream it from `GET /v1/threads/"+
					rec.ThreadID+"/events` rather than asking again, which would pay for it twice.")
			return
		case msg, ok := <-events:
			if !ok {
				return
			}
			var evt app.ChatEvent
			if err := json.Unmarshal([]byte(msg.Payload), &evt); err != nil {
				continue
			}
			// A colleague's turn in the same room is not this caller's (T-N10):
			// its `final` is not the answer, and its `error` is not this turn
			// failing. forward's rule, for forward's reason.
			if rec.own && evt.AskedBy != "" {
				continue
			}
			switch evt.Type {
			case "error":
				apierr.Abort(c, apierr.TypeServer, "turn_failed", evt.Error)
				return
			case "final":
				persisted, err := h.messages.LatestAssistantSince(ctx, rec.ThreadID, rec.StartedAt, rec.scope())
				if err != nil {
					// Answer with what the event carried rather than failing a
					// turn that demonstrably produced a reply.
					c.JSON(http.StatusOK, turnBody(rec, evt.JobID, messageResponse{
						Object:    "message",
						Role:      string(domain.MessageRoleAssistant),
						Content:   evt.Content,
						AgentID:   evt.AgentID,
						AgentName: evt.AgentName,
					}, nil, requestIDOf(c)))
					return
				}
				h.writeTurn(c, rec, persisted)
				return
			}
		}
	}
}

// writeTurn answers the synchronous door with the completed turn.
func (h *V1ChatHandler) writeTurn(c *gin.Context, rec turnRecord, msg *domain.Message) {
	c.JSON(http.StatusOK, turnBody(rec, rec.RunID, messageBody(msg), h.turnUsage(c, rec), requestIDOf(c)))
}

// pendingBody is the 504 the synchronous door answers with.
//
// The field names match the `409 request_in_flight` the idempotency middleware
// writes, deliberately: both mean "the work is still running and here is how to
// find it", and a caller should be able to parse them with one branch.
type pendingBody struct {
	Error    apierr.Detail `json:"error"`
	InFlight turnRecord    `json:"in_flight"`
}

// abortPending answers 504 with the ids that make the turn collectable, and
// keeps the idempotency record.
//
// **The record must survive this.** A 504 is the response a client is most
// likely to retry, and the turn behind it is still running and still being
// billed. Discarding the key — which is what the middleware does for every
// other 5xx, correctly — would let that retry start a second turn on the
// tenant's money.
func (h *V1ChatHandler) abortPending(c *gin.Context, rec turnRecord, message string) {
	middleware.RetainIdempotentRecord(c)
	c.AbortWithStatusJSON(http.StatusGatewayTimeout, pendingBody{
		Error:    apierr.NewDetail(c, apierr.TypeServer, "turn_in_progress", message),
		InFlight: rec,
	})
}

// turnUsage reports what the turn cost. Best-effort: a usage read that fails
// omits the block rather than failing an answer the caller already has.
//
// The window starts at the turn and ends now, which is what makes it this
// turn's usage rather than the thread's. Read from the usage events the
// metering layer wrote, not from the message row: `tokens_in`/`tokens_out` on
// a message are zero for every streamed turn, because the provider reports
// usage per LLM call and a turn is five to seven of them after T-16.
func (h *V1ChatHandler) turnUsage(c *gin.Context, rec turnRecord) *usageBody {
	if h.usage == nil {
		return nil
	}
	sum, err := h.usage.SummaryByThread(c.Request.Context(), companyID(c), rec.ThreadID, rec.StartedAt, time.Now())
	if err != nil || sum == nil {
		return nil
	}
	// An all-zero window is omitted rather than published. It happens on one
	// path only — attaching to a thread whose turn had already finished, where
	// the window can start no earlier than the answer and every usage event
	// was written before it. Reporting `tokens_in: 0` there would state
	// something false about a turn that cost real money, which is the same
	// mistake `/v1/me` avoids by saying "not enforced" instead of "$0.00".
	if sum.TotalTokensIn == 0 && sum.TotalTokensOut == 0 && sum.TotalCostUSD == 0 {
		return nil
	}
	return &usageBody{
		TokensIn:  sum.TotalTokensIn,
		TokensOut: sum.TotalTokensOut,
		CostUSD:   sum.TotalCostUSD,
	}
}

// usageBody is what one turn cost. Dollars rather than the micro-USD the
// system counts in, for the reason `/v1/me` gives: this number is read by a
// person.
type usageBody struct {
	TokensIn  int64   `json:"tokens_in"`
	TokensOut int64   `json:"tokens_out"`
	CostUSD   float64 `json:"cost_usd"`
}

// turnResponse is the answer to one turn, shared by the synchronous door and
// the `final` SSE frame. One shape, so a caller who starts on the synchronous
// door and moves to the stream does not rewrite their parser.
type turnResponse struct {
	Object    string          `json:"object"`
	ThreadID  string          `json:"thread_id"`
	RunID     string          `json:"run_id,omitempty"`
	Message   messageResponse `json:"message"`
	Usage     *usageBody      `json:"usage,omitempty"`
	RequestID string          `json:"request_id,omitempty"`
}

func turnBody(rec turnRecord, runID string, msg messageResponse, usage *usageBody, requestID string) turnResponse {
	return turnResponse{
		Object:    "turn",
		ThreadID:  rec.ThreadID,
		RunID:     firstNonEmpty(rec.RunID, runID),
		Message:   msg,
		Usage:     usage,
		RequestID: requestID,
	}
}

// threadResponse is the public shape of a conversation. Additive only.
//
// It is not domain.ConversationThread: that struct carries a phone number, a
// Discord user id and two Lark keys, none of which an `api` thread has and all
// of which would become part of a published contract the moment one was
// serialized.
type threadResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	UserRef string `json:"user_ref,omitempty"`
	Title   string `json:"title,omitempty"`
	// Summary is the rolling topic summary the thread resolver keeps. It is
	// what the fork classifier reads, and it is useful to an integrator
	// rendering a conversation list.
	Summary       string    `json:"summary,omitempty"`
	LastMessageAt time.Time `json:"last_message_at"`
	CreatedAt     time.Time `json:"created_at"`
	// Participants is the room (T-N10): the conversation's own agent first, then
	// everyone added to it. On the single-thread read and on create only — the
	// dashboard's rule, and its reason: a list needs titles, and a second query
	// per row to fill a field a list does not show is paid on every page.
	//
	// Absent, not empty, for a conversation that runs as the workspace default
	// with nobody added: that one names no agent, because naming today's default
	// would pin it to an agent it is not pinned to.
	Participants []participantResponse `json:"participants,omitempty"`
}

func threadBody(t *domain.ConversationThread) threadResponse {
	return threadResponse{
		ID:            t.ID,
		Object:        "thread",
		UserRef:       t.APIUserRef,
		Title:         t.Title,
		Summary:       t.Summary,
		LastMessageAt: t.LastMessageAt,
		CreatedAt:     t.CreatedAt,
	}
}

// participantResponse is one agent in a conversation (T-N10).
//
// Not domain.ThreadParticipant: its row id does not exist for the default
// speaker, which has no row, and `added_by` is a dashboard user's id, which a
// machine credential has no use for and should not be handed.
type participantResponse struct {
	Object    string `json:"object"`
	AgentID   string `json:"agent_id"`
	AgentName string `json:"agent_name,omitempty"`
	// Default marks the agent that answers a message naming nobody — the
	// conversation's own. Every other participant answers only when named in
	// `agent_id` on `POST /v1/chat`.
	Default bool      `json:"default"`
	AddedAt time.Time `json:"added_at"`
}

func participantBodies(t *domain.ConversationThread, room []*domain.ThreadParticipant) []participantResponse {
	out := make([]participantResponse, 0, len(room))
	for _, p := range room {
		if p == nil {
			continue
		}
		out = append(out, participantResponse{
			Object:    "participant",
			AgentID:   p.AgentID,
			AgentName: p.AgentName,
			Default:   t.AgentID != "" && p.AgentID == t.AgentID,
			AddedAt:   p.AddedAt,
		})
	}
	return out
}

// createThreadRequest is the body of `POST /v1/threads` (T-N10).
type createThreadRequest struct {
	// UserRef is required, for the reason `POST /v1/chat` gives: it is what
	// makes a conversation's spend attributable, and a conversation opened
	// without one is one `usage/by-user` can never name.
	UserRef string `json:"user_ref"`
	// AgentID is the conversation's own agent. Omitted is the workspace default,
	// resolved per turn, as on `POST /v1/chat`.
	AgentID string `json:"agent_id,omitempty"`
	// ParticipantIDs are the other agents in the room. A repeat, or the
	// conversation's own agent, is dropped rather than refused: it names someone
	// who is already there.
	ParticipantIDs []string `json:"participant_ids,omitempty"`
}

// messageResponse is the public shape of one turn in a transcript.
//
// Token counts and latency are not on it. They are per-message zeros for every
// streamed turn (see turnUsage), and publishing a field that is honestly zero
// most of the time is publishing a field integrators will report as a bug.
type messageResponse struct {
	ID      string `json:"id,omitempty"`
	Object  string `json:"object"`
	Role    string `json:"role"`
	Content string `json:"content"`
	// AgentID and AgentName are who wrote an assistant message (T-N1, published
	// by T-N10). Empty on a user message, and on an answer from a turn that ran
	// unscoped. The name is read from the roster at read time, so a renamed agent
	// renames its past messages — the dashboard's arrangement.
	AgentID   string `json:"agent_id,omitempty"`
	AgentName string `json:"agent_name,omitempty"`
	// RoomEvent marks a room's own line (T-N6): a question one agent asked
	// another, a question the conversation budget refused, a colleague with
	// nothing to add, a question whose recipient left. They are assistant rows,
	// and without this a caller cannot tell "Finance had nothing to add" from
	// Finance's answer — the reason the dashboard draws them as lines.
	RoomEvent string    `json:"room_event,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

func messageBody(m *domain.Message) messageResponse {
	out := messageResponse{
		ID:        m.ID,
		Object:    "message",
		Role:      string(m.Role),
		Content:   m.Content,
		AgentID:   m.AgentID,
		AgentName: m.AgentName,
		CreatedAt: m.CreatedAt,
	}
	if ev, ok := m.Metadata[app.RoomEventKey].(string); ok {
		out.RoomEvent = ev
	}
	return out
}

// listThreads is `GET /v1/threads` — the conversations this integration
// started, newest first.
func (h *V1ChatHandler) listThreads(c *gin.Context) {
	if h.threads == nil {
		apierr.Abort(c, apierr.TypeServer, "chat_unavailable", "Chat is not available on this deployment.")
		return
	}
	// Never anything but `api`. A machine credential that could list the
	// dashboard's threads would let a leaked key read the staff's own chat
	// history, and the tenant's audit surface for those is the dashboard, which
	// is role-gated. T-A1 drew the same line on the write side.
	f := domain.ThreadFilter{Channel: domain.ChannelAPI}
	f.APIUserRef = strings.TrimSpace(c.Query("user_ref"))

	var ok bool
	if f.Limit, ok = parseLimitParam(c); !ok {
		return
	}
	if f.CursorTime, f.CursorID, ok = parseCursorParam(c); !ok {
		return
	}

	rows, hasMore, err := h.threads.ListPage(c.Request.Context(), companyID(c), f)
	if err != nil {
		logrus.WithError(err).WithField("company_id", companyID(c)).Error("list api threads")
		apierr.Abort(c, apierr.TypeServer, "list_failed", "The thread list could not be read.")
		return
	}
	items := make([]threadResponse, 0, len(rows))
	for _, t := range rows {
		items = append(items, threadBody(t))
	}
	next := ""
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		next = apiv1.EncodeCursor(last.CreatedAt, last.ID)
	}
	c.JSON(http.StatusOK, apiv1.NewPage(items, hasMore, next))
}

// createThread is `POST /v1/threads` (T-N10): a conversation before its first
// question, holding the agents the caller names.
func (h *V1ChatHandler) createThread(c *gin.Context) {
	if h.opener == nil {
		apierr.Abort(c, apierr.TypeServer, "rooms_unavailable",
			"Opening a conversation ahead of its first question is not available on this deployment. `POST /v1/chat` with a `user_ref` opens one.")
		return
	}
	var req createThreadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.Abort(c, apierr.TypeInvalidRequest, "invalid_request",
			"The request body is not valid JSON.")
		return
	}
	req.UserRef = strings.TrimSpace(req.UserRef)
	if req.UserRef == "" {
		abortUserRefRequired(c)
		return
	}
	own := strings.TrimSpace(req.AgentID)
	others := distinctParticipants(own, req.ParticipantIDs)
	if len(others) > 0 && h.members == nil {
		apierr.Abort(c, apierr.TypeServer, "rooms_unavailable",
			"A conversation holding more than one agent is not available on this deployment.")
		return
	}
	// The whole room against the ceiling, before anything is written. The
	// conversation's own agent takes a seat whether it is named or is the
	// workspace default: either way it answers here, and a ceiling that counted
	// it only when named would let an unnamed default make a room one larger.
	if len(others) > 0 && len(others)+1 > h.members.Capacity() {
		apierr.AbortParam(c, apierr.TypeInvalidRequest, "too_many_participants",
			fmt.Sprintf("A conversation holds at most %d agents, its own agent included.", h.members.Capacity()),
			"participant_ids")
		return
	}

	ctx := c.Request.Context()
	thread, err := h.opener.OpenAPIThread(ctx, app.APIThreadInput{
		CompanyID:      companyID(c),
		APIUserRef:     req.UserRef,
		AgentID:        own,
		ParticipantIDs: others,
		APIKeyID:       c.GetString(middleware.CtxAPIKeyID),
		KeyAgentIDs:    middleware.APIKeyAgents(c),
	})
	if err != nil {
		abortOpenThread(c, err)
		return
	}
	for _, id := range others {
		_, err := h.members.Add(ctx, companyID(c), thread.ID, id, "")
		if err == nil || errors.Is(err, domain.ErrAlreadyExists) {
			continue
		}
		// Every agent was checked a moment ago, so this is a race — one disabled
		// or deleted in between — or the database. The conversation is left, not
		// deleted, for the reason the dashboard's POST /api/threads gives: it is a
		// valid conversation with its own agent, and a compensating delete is a
		// second write that can also fail. The message names it, so the caller
		// can delete it or add to it.
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, app.ErrAgentDisabled) {
			apierr.AbortParam(c, apierr.TypeNotFound, "participant_not_found",
				"An agent in `participant_ids` stopped being available while the conversation was opened. Conversation "+
					thread.ID+" was opened without it.", "participant_ids")
			return
		}
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": companyID(c), "thread_id": thread.ID,
		}).Error("add a participant to an api thread")
		apierr.Abort(c, apierr.TypeServer, "open_failed",
			"Conversation "+thread.ID+" was opened, but not every agent could be added to it.")
		return
	}

	body := threadBody(thread)
	body.Participants = h.room(c, thread)
	middleware.DeclareIdempotentResult(c, body)
	c.JSON(http.StatusCreated, body)
}

// abortOpenThread maps OpenAPIThread's refusals onto the envelope. The
// participant case goes first: it wraps ErrAgentNotFound, and a caller whose
// `participant_ids` were wrong must not be sent to look at `agent_id`.
func abortOpenThread(c *gin.Context, err error) {
	switch {
	case errors.Is(err, app.ErrParticipantNotFound):
		apierr.AbortParam(c, apierr.TypeNotFound, "participant_not_found",
			"An agent in `participant_ids` is not one this key can use in this workspace. List them with `GET /v1/agents`.",
			"participant_ids")
	case errors.Is(err, app.ErrAgentNotFound):
		abortAgentNotFound(c)
	case errors.Is(err, app.ErrAgentNotAllowed):
		abortAgentNotAllowed(c)
	case errors.Is(err, domain.ErrInvalidInput):
		abortUserRefRequired(c)
	default:
		logrus.WithError(err).WithField("company_id", companyID(c)).Error("open api thread")
		apierr.Abort(c, apierr.TypeServer, "open_failed", "The conversation could not be opened.")
	}
}

func abortUserRefRequired(c *gin.Context) {
	apierr.AbortParam(c, apierr.TypeInvalidRequest, "user_ref_required",
		"Send a `user_ref` identifying who the conversation is for.", "user_ref")
}

// distinctParticipants is `participant_ids` trimmed, in the order given, without
// blanks, repeats or the conversation's own agent. Each of those names someone
// already in the room, and refusing a list for saying so twice would make every
// integrator deduplicate a list the server can.
func distinctParticipants(own string, ids []string) []string {
	seen := map[string]bool{}
	if own != "" {
		seen[own] = true
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// room reads a conversation's participants for a response (T-N10).
//
// A failed read drops the field rather than failing the response — the
// dashboard's thread detail makes the same trade, and for its reason:
// participants make a conversation richer and are never what makes it
// readable. Logged, because an absent field is otherwise indistinguishable
// from a conversation nobody was added to.
func (h *V1ChatHandler) room(c *gin.Context, t *domain.ConversationThread) []participantResponse {
	if h.members == nil {
		return nil
	}
	ps, err := h.members.List(c.Request.Context(), companyID(c), t.ID)
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": companyID(c), "thread_id": t.ID,
		}).Warn("thread participants not read; answering without them")
		return nil
	}
	return participantBodies(t, ps)
}

// getThread is `GET /v1/threads/:id`.
func (h *V1ChatHandler) getThread(c *gin.Context) {
	t, ok := h.loadThread(c)
	if !ok {
		return
	}
	body := threadBody(t)
	body.Participants = h.room(c, t)
	c.JSON(http.StatusOK, body)
}

// listMessages is `GET /v1/threads/:id/messages` — the transcript,
// oldest-first, cursor-paginated.
func (h *V1ChatHandler) listMessages(c *gin.Context) {
	t, ok := h.loadThread(c)
	if !ok {
		return
	}
	f := domain.MessageFilter{}
	if f.Limit, ok = parseLimitParam(c); !ok {
		return
	}
	if f.CursorTime, f.CursorID, ok = parseCursorParam(c); !ok {
		return
	}

	rows, hasMore, err := h.messages.ListPageByThread(c.Request.Context(), t.ID, f)
	if err != nil {
		logrus.WithError(err).WithField("thread_id", t.ID).Error("list thread messages")
		apierr.Abort(c, apierr.TypeServer, "list_failed", "The messages could not be read.")
		return
	}
	items := make([]messageResponse, 0, len(rows))
	for _, m := range rows {
		items = append(items, messageBody(m))
	}
	next := ""
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		next = apiv1.EncodeCursor(last.CreatedAt, last.ID)
	}
	c.JSON(http.StatusOK, apiv1.NewPage(items, hasMore, next))
}

// streamThread is `GET /v1/threads/:id/events`.
//
// It attaches to the thread's newest turn. If that turn has already answered,
// the answer is delivered and the stream closes — the caller asked to watch a
// turn, and holding the connection open for a question nobody has asked yet
// would be a long-poll for the future rather than a resume of the past.
func (h *V1ChatHandler) streamThread(c *gin.Context) {
	t, ok := h.loadThread(c)
	if !ok {
		return
	}
	resumeFrom, resumeID, ok := h.resumePoint(c)
	if !ok {
		return
	}
	h.stream(c, h.newestTurn(c, t), resumeFrom, resumeID)
}

// newestTurn locates the turn this thread is on.
//
// **Not from `last_message_at`.** The obvious implementation reads the thread
// row's own timestamp, and the live gate proved it does not work: that column
// is written by `Touch` from the API's clock while `messages.created_at` comes
// from Postgres's, and the two land 130µs apart in the wrong direction. So a
// settled thread's answer was never `>= last_message_at`, and attaching to one
// held the connection open for a `final` that had already happened. Comparing
// two rows written by the same clock has no such failure mode.
//
// The two cases collapse into one expression. If the newest message is the
// assistant's, the thread is settled and that message *is* the turn's answer,
// so the terminal check matches it immediately. If it is the user's, a turn is
// running and its answer is whatever assistant message lands after the
// question — which also excludes the previous turn's answer, sitting older.
func (h *V1ChatHandler) newestTurn(c *gin.Context, t *domain.ConversationThread) turnRecord {
	rec := turnRecord{ThreadID: t.ID, StartedAt: t.LastMessageAt}
	latest, err := h.messages.LatestByThread(c.Request.Context(), t.ID)
	if err != nil {
		// An empty thread has no turn to attach to; the fallback keeps the
		// stream live rather than failing it.
		return rec
	}
	rec.StartedAt = latest.CreatedAt
	if latest.Role == domain.MessageRoleUser {
		// The question's id is the run id: it is what ChatEvent carries as
		// JobID, so a caller who attached before any event arrived still gets
		// the same string the events will use.
		rec.RunID = latest.ID
	}
	return rec
}

// deleteThread is `DELETE /v1/threads/:id`.
func (h *V1ChatHandler) deleteThread(c *gin.Context) {
	t, ok := h.loadThread(c)
	if !ok {
		return
	}
	if err := h.threads.Delete(c.Request.Context(), t.ID); err != nil {
		logrus.WithError(err).WithField("thread_id", t.ID).Error("delete api thread")
		apierr.Abort(c, apierr.TypeServer, "delete_failed", "The thread could not be deleted.")
		return
	}
	c.Status(http.StatusNoContent)
}

// loadThread resolves `:id` inside every boundary that applies: the tenant, the
// channel, and — when the caller names one — the end user.
//
// All three failures answer the same 404. A 403 on the second would tell a key
// holder that a dashboard thread with that id exists, and a 403 on the third
// would let one of a tenant's users enumerate another's conversations by id.
// An existence oracle is the whole vulnerability; the status code is the
// oracle.
func (h *V1ChatHandler) loadThread(c *gin.Context) (*domain.ConversationThread, bool) {
	notFound := func() (*domain.ConversationThread, bool) {
		apierr.Abort(c, apierr.TypeNotFound, "thread_not_found", "No such thread for this company.")
		return nil, false
	}
	if h.threads == nil {
		apierr.Abort(c, apierr.TypeServer, "chat_unavailable", "Chat is not available on this deployment.")
		return nil, false
	}
	t, err := h.threads.GetForCompany(c.Request.Context(), companyID(c), c.Param("id"))
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			logrus.WithError(err).WithField("company_id", companyID(c)).Warn("thread lookup failed")
		}
		return notFound()
	}
	if t.Channel != domain.ChannelAPI {
		return notFound()
	}
	// `user_ref` is the tenant's own identifier and we cannot authenticate it —
	// the key belongs to the company, not to one of its users. What we can do
	// is hold the caller to the one they named, which is what turns "our
	// backend passes the logged-in user's id through" into an isolation
	// boundary rather than a convention.
	if ref := strings.TrimSpace(c.Query("user_ref")); ref != "" && t.APIUserRef != ref {
		return notFound()
	}
	return t, true
}
