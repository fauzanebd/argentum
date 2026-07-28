package handlers

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
)

// Server-sent events plumbing, shared by the report progress stream (T-A2) and
// the chat stream (T-A3).
//
// It lives in one file because the two streams have to agree on things a
// caller can observe: that the response is unbuffered, that every frame is
// flushed, and that a heartbeat is a comment rather than an event a client has
// to learn to ignore. Two copies would agree today and drift the first time
// one of them is tuned.
//
// SSE rather than a WebSocket on `/v1`, per the ticket's locked decision: the
// consumer is a server, every HTTP library and proxy handles SSE, and a
// WebSocket client in a backend is a dependency plus a reconnect state machine
// the integrator has to write.

// sseStart writes the stream's headers and commits them.
//
// The commit matters: gin buffers a response until something forces it out, so
// a client waiting on the first byte of a two-minute turn would otherwise see
// nothing at all until the turn ended — the feature silently doing nothing,
// which is the failure mode the ticket warns about by name.
func sseStart(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	// The nginx opt-out. A proxy that buffers a response defeats the point of a
	// stream, and this header is ignored by everything that does not buffer.
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(200)
	c.Writer.Flush()
}

// sseEvent writes one frame. An empty id omits the `id:` line, which is what
// keeps a client's Last-Event-ID pinned to the last *resumable* point rather
// than to a token delta that was never persisted anywhere.
//
// The bool is whether the connection is still usable. A client that hung up
// mid-turn is the ordinary end of one of these streams, not an error worth
// logging.
func sseEvent(c *gin.Context, id, name string, payload any) bool {
	body, err := json.Marshal(payload)
	if err != nil {
		// A frame that cannot be marshalled is a bug in the caller's payload,
		// not a reason to tear down a stream that is otherwise fine.
		return true
	}
	var b strings.Builder
	if id != "" {
		b.WriteString("id: ")
		b.WriteString(id)
		b.WriteString("\n")
	}
	b.WriteString("event: ")
	b.WriteString(name)
	b.WriteString("\ndata: ")
	b.Write(body)
	b.WriteString("\n\n")
	if _, err := fmt.Fprint(c.Writer, b.String()); err != nil {
		return false
	}
	c.Writer.Flush()
	return true
}

// sseComment writes a `: text` line. Used for the heartbeat: a comment is
// invisible to every SSE client, so an idle-timeout keepalive costs the
// integrator no code, where a `heartbeat` event would be one more case in
// their switch.
func sseComment(c *gin.Context, text string) bool {
	if _, err := fmt.Fprintf(c.Writer, ": %s\n\n", text); err != nil {
		return false
	}
	c.Writer.Flush()
	return true
}

// wantsSSE reports whether the caller asked for a stream. Absent or wildcard
// Accept means no: a caller that did not say is a caller reading a response
// body, and handing them a stream they have to parse is the less useful
// default.
func wantsSSE(c *gin.Context) bool {
	return strings.Contains(strings.ToLower(c.GetHeader("Accept")), "text/event-stream")
}
