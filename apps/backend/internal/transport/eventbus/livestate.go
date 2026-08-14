package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/fauzanebd/argentum/internal/app"
)

// The live-turn snapshot (T-U12): what a socket that opens mid-turn is told
// before the first event it is early enough to receive. Written beside every
// publish, deleted by the event that ends the turn.
//
// Four keys rather than one JSON blob, because the two that grow are the two
// that would otherwise cost a read-modify-write per token: text is APPENDed and
// steps are RPUSHed, both O(1) and both safe to interleave with a concurrent
// reader. The hash holds only fields that are overwritten whole.
const (
	// liveTTL bounds a snapshot whose turn never ended — a worker killed
	// mid-run publishes no `final`, and nothing else would ever delete these.
	// Well past the longest turn (a video report runs minutes), short enough
	// that a dead turn cannot show a spinner to tomorrow's reader.
	liveTTL = 15 * time.Minute
	// maxLiveSteps caps the thinking trace. The dashboard shows the last few
	// and collapses the rest; a runaway loop should not be able to grow this
	// without limit.
	maxLiveSteps = 50
	// maxLiveTools caps the restored tool cards, for the same reason.
	maxLiveTools = 20
	// maxToolPayload skips one oversized tool payload rather than truncating
	// it into malformed JSON. A 200-row query result is not worth resending on
	// reconnect; the card it belongs to still arrives with the `final` turn.
	maxToolPayload = 8 * 1024
)

func liveKey(threadID string) string        { return ChannelFor(threadID) + ":live" }
func liveContentKey(threadID string) string { return liveKey(threadID) + ":content" }
func liveStepsKey(threadID string) string   { return liveKey(threadID) + ":steps" }
func liveToolsKey(threadID string) string   { return liveKey(threadID) + ":tools" }

func liveKeys(threadID string) []string {
	return []string{
		liveKey(threadID),
		liveContentKey(threadID),
		liveStepsKey(threadID),
		liveToolsKey(threadID),
	}
}

// recordLive folds one event into the thread's snapshot. It queues commands on
// pipe — the same pipeline that carries the PUBLISH — so the snapshot costs no
// extra round trip and can never be one event behind what a subscriber saw.
func recordLive(ctx context.Context, pipe redis.Pipeliner, threadID string, evt app.ChatEvent) {
	keys := liveKeys(threadID)

	switch evt.Type {
	case "final", "error":
		// The turn is over: the transcript is the record from here on, and a
		// surviving snapshot would show a spinner above a finished answer.
		pipe.Del(ctx, keys...)
		return
	case "started":
		// A retried job republishes `started`, so this clears rather than
		// merges — otherwise the second attempt's trace would open with the
		// first attempt's steps.
		pipe.Del(ctx, keys...)
	}

	ts := evt.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	stamp := ts.Format(time.RFC3339Nano)

	// job_id and started_at are set by whichever event arrives first: a turn
	// that opens with a tool call publishes no `started` before it.
	pipe.HSet(ctx, keys[0], "job_id", evt.JobID, "last_event_at", stamp)
	pipe.HSetNX(ctx, keys[0], "started_at", stamp)

	switch evt.Type {
	case "delta":
		if evt.Content != "" {
			pipe.Append(ctx, liveContentKey(threadID), evt.Content)
		}
	case "thinking":
		if evt.ThinkingStep != "" {
			pipe.RPush(ctx, liveStepsKey(threadID), evt.ThinkingStep)
			pipe.LTrim(ctx, liveStepsKey(threadID), -maxLiveSteps, -1)
		}
	case "iteration":
		if n := metaInt(evt.Metadata, "iteration"); n > 0 {
			pipe.HSet(ctx, keys[0],
				"iteration", n,
				"max_iterations", metaInt(evt.Metadata, "max_iterations"))
		}
	case "tool_call", "tool_result":
		if evt.ToolCall != nil {
			if body, err := json.Marshal(evt.ToolCall); err == nil && len(body) <= maxToolPayload {
				pipe.RPush(ctx, liveToolsKey(threadID), body)
				pipe.LTrim(ctx, liveToolsKey(threadID), -maxLiveTools, -1)
			}
		}
	}

	for _, k := range keys {
		pipe.Expire(ctx, k, liveTTL)
	}
}

// metaInt reads a numeric metadata field. The map has been through JSON on
// some paths and not on others, so the same field is a float64 here and an int
// there; both mean the iteration number.
func metaInt(meta map[string]interface{}, key string) int {
	switch v := meta[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	default:
		return 0
	}
}

// LoadLiveTurn returns the turn currently running on a thread, or nil when
// none is. Used by the WebSocket handler to greet a client that connected
// mid-turn; a partial or unreadable snapshot answers nil rather than an error,
// because a missing catch-up frame costs a few seconds of spinner and a failed
// connection costs the whole stream.
func LoadLiveTurn(ctx context.Context, rdb *redis.Client, threadID string) (*app.LiveTurn, error) {
	pipe := rdb.Pipeline()
	fieldsCmd := pipe.HGetAll(ctx, liveKey(threadID))
	contentCmd := pipe.Get(ctx, liveContentKey(threadID))
	stepsCmd := pipe.LRange(ctx, liveStepsKey(threadID), 0, -1)
	toolsCmd := pipe.LRange(ctx, liveToolsKey(threadID), 0, -1)
	// redis.Nil is the ordinary answer for a turn that has streamed no text
	// yet — the content key simply does not exist.
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}

	fields := fieldsCmd.Val()
	if fields["job_id"] == "" {
		return nil, nil
	}

	turn := &app.LiveTurn{
		JobID:         fields["job_id"],
		StartedAt:     parseStamp(fields["started_at"]),
		LastEventAt:   parseStamp(fields["last_event_at"]),
		Content:       contentCmd.Val(),
		ThinkingSteps: stepsCmd.Val(),
		Iteration:     atoiOrZero(fields["iteration"]),
		MaxIterations: atoiOrZero(fields["max_iterations"]),
	}
	for _, raw := range toolsCmd.Val() {
		var tc app.ToolCallEvent
		if err := json.Unmarshal([]byte(raw), &tc); err == nil {
			turn.ToolCalls = append(turn.ToolCalls, tc)
		}
	}
	return turn, nil
}

func parseStamp(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func atoiOrZero(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
