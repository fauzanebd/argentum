// Package ws upgrades dashboard requests to WebSockets and forwards
// per-thread chat events from Redis pub/sub directly to the client.
//
// There is no in-process hub anymore: every WS connection runs its own
// SUBSCRIBE on `argentum:thread:{id}`. This keeps the API stateless so any
// replica can serve any client.
package ws

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/transport/eventbus"
)

const (
	pingPeriod = 30 * time.Second
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
)

// Handler upgrades incoming HTTP requests to WebSockets and pipes events
// from a Redis pub/sub channel to the client.
type Handler struct {
	rdb      *redis.Client
	upgrader websocket.Upgrader
	threads  domain.ThreadRepository
}

// NewHandler builds a WebSocket handler backed by Redis pub/sub.
// allowedOrigins is consulted by the upgrader's CheckOrigin; an empty list
// disables the check (dev mode).
func NewHandler(rdb *redis.Client, threads domain.ThreadRepository, allowedOrigins []string) *Handler {
	allow := map[string]struct{}{}
	for _, o := range allowedOrigins {
		allow[o] = struct{}{}
	}
	return &Handler{
		rdb:     rdb,
		threads: threads,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 8 * 1024,
			CheckOrigin: func(r *http.Request) bool {
				if len(allow) == 0 {
					return true
				}
				_, ok := allow[r.Header.Get("Origin")]
				return ok
			},
		},
	}
}

// Stream is the GET handler for /api/threads/:id/stream. The client must
// be authenticated and authorised for the thread (enforced by middleware
// that has already populated company_id on the gin context).
func (h *Handler) Stream(c *gin.Context) {
	threadID := c.Param("id")
	companyID, _ := c.Get("company_id")

	thread, err := h.threads.GetByID(c.Request.Context(), threadID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "thread not found"})
		return
	}
	if cid, _ := companyID.(string); thread.CompanyID != cid {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	h.pump(c, threadID, h.upgrader)
}

// EmbedStream is the same socket for a widget visitor (T-20).
//
// Two differences from Stream, and both are the point:
//
//  1. **Ownership is per visitor, not per company.** A widget client runs on a
//     page we do not control, so the thread id in the path is a value the
//     visitor can edit. The thread must belong to this company, be a widget
//     thread, and carry *this session's* `embed_user_ref` — otherwise a
//     colleague's conversation is one guessed uuid away. A mismatch answers
//     404 rather than 403: a wrong id and somebody else's id are one answer.
//  2. **The origin check is off.** It cannot be anything else: the set of
//     allowed origins is per embed key and this socket has no key on it, only
//     a session. That is not a hole, because CheckOrigin defends
//     cookie-authenticated sockets and this one authenticates with a bearer
//     token in the query — a token a hostile page does not have. The origin
//     that mattered was checked when the session was minted (T-19).
func (h *Handler) EmbedStream(c *gin.Context) {
	threadID := c.Param("id")
	companyID := c.GetString("company_id")
	userRef := c.GetString("embed_user_ref")

	thread, err := h.threads.GetByID(c.Request.Context(), threadID)
	if err != nil || thread.CompanyID != companyID ||
		thread.Channel != domain.ChannelWidget || thread.EmbedUserRef == "" ||
		thread.EmbedUserRef != userRef {
		c.JSON(http.StatusNotFound, gin.H{"error": "no such conversation"})
		return
	}

	h.pump(c, threadID, h.embedUpgrader())
}

// embedUpgrader is the connection upgrader for EmbedStream: the same buffers,
// with CheckOrigin accepting anything for the reason stated above.
func (h *Handler) embedUpgrader() websocket.Upgrader {
	u := h.upgrader
	u.CheckOrigin = func(*http.Request) bool { return true }
	return u
}

// pump upgrades the connection and forwards one thread's events until either
// side goes away. Shared by both entry points so the two sockets cannot drift
// in their ping cadence, their read limit or their shutdown ordering — what
// differs between them is who is allowed to open one, which is settled above.
func (h *Handler) pump(c *gin.Context, threadID string, upgrader websocket.Upgrader) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		logrus.WithError(err).Warn("ws upgrade failed")
		return
	}
	defer conn.Close()

	conn.SetReadLimit(512)
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	// One Redis subscription per WS connection. ctx is bound to the
	// connection lifetime so closing the WebSocket aborts the SUBSCRIBE.
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	pubsub := h.rdb.Subscribe(ctx, eventbus.ChannelFor(threadID))
	defer pubsub.Close()

	// Wait for the subscription to be ready before forwarding events;
	// otherwise the first messages can be dropped.
	if _, err := pubsub.Receive(ctx); err != nil {
		logrus.WithError(err).Warn("ws redis subscribe failed")
		return
	}
	msgCh := pubsub.Channel()

	// Reader goroutine — discards client frames but keeps the pong
	// handler firing. Cancels ctx on read error so the writer loop and
	// pubsub subscription exit cleanly.
	go func() {
		defer cancel()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-msgCh:
			if !ok {
				return
			}
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			// Pass the JSON payload through unchanged: the worker
			// already serialized ChatEvent and clients parse JSON.
			if err := conn.WriteMessage(websocket.TextMessage, []byte(msg.Payload)); err != nil {
				return
			}
		case <-ticker.C:
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
