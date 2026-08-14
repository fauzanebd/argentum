package eventbus

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/fauzanebd/argentum/internal/app"
)

// busWithRedis runs the snapshot against miniredis rather than a fake: what is
// under test is the accumulation itself — APPEND on a key that does not exist
// yet, LTRIM's negative ranges, HSetNX not overwriting — and a fake would only
// assert that this file agrees with itself.
func busWithRedis(t *testing.T) (*RedisBus, *miniredis.Miniredis) {
	t.Helper()
	srv := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewRedisBus(rdb), srv
}

func publishAll(t *testing.T, bus *RedisBus, threadID string, evts ...app.ChatEvent) {
	t.Helper()
	for _, e := range evts {
		if err := bus.Publish(threadID, e); err != nil {
			t.Fatalf("publish %s: %v", e.Type, err)
		}
	}
}

func TestLiveTurnRebuildsATurnInFlight(t *testing.T) {
	bus, srv := busWithRedis(t)
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	start := time.Now()
	publishAll(t, bus, "th-1",
		app.ChatEvent{JobID: "job-1", ThreadID: "th-1", Type: "started", Timestamp: start},
		app.ChatEvent{JobID: "job-1", ThreadID: "th-1", Type: "thinking",
			ThinkingStep: "Checking the schema", Timestamp: start.Add(time.Second)},
		app.ChatEvent{JobID: "job-1", ThreadID: "th-1", Type: "iteration",
			Metadata:  map[string]interface{}{"iteration": 2, "max_iterations": 8},
			Timestamp: start.Add(2 * time.Second)},
		app.ChatEvent{JobID: "job-1", ThreadID: "th-1", Type: "tool_call",
			ToolCall:  &app.ToolCallEvent{Name: "run_query"},
			Timestamp: start.Add(3 * time.Second)},
		app.ChatEvent{JobID: "job-1", ThreadID: "th-1", Type: "delta",
			Content: "Sales ", Timestamp: start.Add(4 * time.Second)},
		app.ChatEvent{JobID: "job-1", ThreadID: "th-1", Type: "delta",
			Content: "rose", Timestamp: start.Add(5 * time.Second)},
	)

	turn, err := LoadLiveTurn(ctx, rdb, "th-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if turn == nil {
		t.Fatal("no live turn; a running turn must be resumable")
	}
	if turn.JobID != "job-1" {
		t.Errorf("job_id = %q, want job-1", turn.JobID)
	}
	// The elapsed caption is read off this: the moment the *agent* started,
	// not the moment the browser reconnected.
	if !turn.StartedAt.Equal(start.Truncate(0)) {
		t.Errorf("started_at = %v, want %v", turn.StartedAt, start)
	}
	if turn.Content != "Sales rose" {
		t.Errorf("content = %q, want %q", turn.Content, "Sales rose")
	}
	if len(turn.ThinkingSteps) != 1 || turn.ThinkingSteps[0] != "Checking the schema" {
		t.Errorf("thinking_steps = %v", turn.ThinkingSteps)
	}
	if turn.Iteration != 2 || turn.MaxIterations != 8 {
		t.Errorf("iteration = %d/%d, want 2/8", turn.Iteration, turn.MaxIterations)
	}
	if len(turn.ToolCalls) != 1 || turn.ToolCalls[0].Name != "run_query" {
		t.Errorf("tool_calls = %v", turn.ToolCalls)
	}
	// The watermark the client dedupes the reconnect echo against.
	if !turn.LastEventAt.Equal(start.Add(5 * time.Second).Truncate(0)) {
		t.Errorf("last_event_at = %v, want the newest event's stamp", turn.LastEventAt)
	}
}

func TestLiveTurnEndsWithTheTurn(t *testing.T) {
	bus, srv := busWithRedis(t)
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	for _, ending := range []app.ChatEvent{
		{JobID: "job-1", ThreadID: "th-1", Type: "final", Content: "Sales rose 12%."},
		{JobID: "job-2", ThreadID: "th-2", Type: "error", Error: "boom"},
	} {
		threadID := ending.ThreadID
		publishAll(t, bus, threadID,
			app.ChatEvent{JobID: ending.JobID, ThreadID: threadID, Type: "started", Timestamp: time.Now()},
			app.ChatEvent{JobID: ending.JobID, ThreadID: threadID, Type: "delta",
				Content: "Sales ", Timestamp: time.Now()},
			ending,
		)

		turn, err := LoadLiveTurn(ctx, rdb, threadID)
		if err != nil {
			t.Fatalf("load after %s: %v", ending.Type, err)
		}
		if turn != nil {
			t.Errorf("after %s the turn is still live; a finished turn is the transcript's job", ending.Type)
		}
		for _, k := range liveKeys(threadID) {
			if srv.Exists(k) {
				t.Errorf("after %s key %s survives", ending.Type, k)
			}
		}
	}
}

func TestLiveTurnIsAbsentBetweenTurns(t *testing.T) {
	_, srv := busWithRedis(t)
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	turn, err := LoadLiveTurn(context.Background(), rdb, "th-quiet")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if turn != nil {
		t.Errorf("live turn on an idle thread = %+v, want nil", turn)
	}
}

func TestRetriedTurnDoesNotInheritTheFirstAttempt(t *testing.T) {
	bus, srv := busWithRedis(t)
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	now := time.Now()
	publishAll(t, bus, "th-1",
		app.ChatEvent{JobID: "job-1", ThreadID: "th-1", Type: "started", Timestamp: now},
		app.ChatEvent{JobID: "job-1", ThreadID: "th-1", Type: "thinking",
			ThinkingStep: "first attempt", Timestamp: now.Add(time.Second)},
		app.ChatEvent{JobID: "job-1", ThreadID: "th-1", Type: "delta",
			Content: "half an ", Timestamp: now.Add(2 * time.Second)},
		// asynq retries the job: the handler runs from the top and republishes.
		app.ChatEvent{JobID: "job-1", ThreadID: "th-1", Type: "started", Timestamp: now.Add(time.Minute)},
		app.ChatEvent{JobID: "job-1", ThreadID: "th-1", Type: "delta",
			Content: "Sales rose", Timestamp: now.Add(time.Minute + time.Second)},
	)

	turn, err := LoadLiveTurn(ctx, rdb, "th-1")
	if err != nil || turn == nil {
		t.Fatalf("load = (%v, %v)", turn, err)
	}
	if turn.Content != "Sales rose" {
		t.Errorf("content = %q; the retry must not resume the abandoned attempt", turn.Content)
	}
	if len(turn.ThinkingSteps) != 0 {
		t.Errorf("thinking_steps = %v, want the first attempt's trace dropped", turn.ThinkingSteps)
	}
	if !turn.StartedAt.Equal(now.Add(time.Minute).Truncate(0)) {
		t.Errorf("started_at = %v, want the retry's start", turn.StartedAt)
	}
}

func TestLiveTurnCapsWhatItKeeps(t *testing.T) {
	bus, srv := busWithRedis(t)
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	now := time.Now()
	publishAll(t, bus, "th-1",
		app.ChatEvent{JobID: "job-1", ThreadID: "th-1", Type: "started", Timestamp: now})
	for i := range maxLiveSteps + 10 {
		publishAll(t, bus, "th-1", app.ChatEvent{
			JobID: "job-1", ThreadID: "th-1", Type: "thinking",
			ThinkingStep: string(rune('a' + i%26)), Timestamp: now.Add(time.Duration(i) * time.Second),
		})
	}
	// One payload past the cap: dropped whole rather than truncated into
	// malformed JSON.
	big := map[string]interface{}{"rows": make([]interface{}, 0, 1024)}
	for range 1024 {
		big["rows"] = append(big["rows"].([]interface{}), "0123456789")
	}
	publishAll(t, bus, "th-1", app.ChatEvent{
		JobID: "job-1", ThreadID: "th-1", Type: "tool_result",
		ToolCall: &app.ToolCallEvent{Name: "run_query", Result: big}, Timestamp: now,
	})

	turn, err := LoadLiveTurn(ctx, rdb, "th-1")
	if err != nil || turn == nil {
		t.Fatalf("load = (%v, %v)", turn, err)
	}
	if len(turn.ThinkingSteps) != maxLiveSteps {
		t.Errorf("thinking_steps = %d, want the last %d", len(turn.ThinkingSteps), maxLiveSteps)
	}
	if len(turn.ToolCalls) != 0 {
		t.Errorf("tool_calls = %d, want an oversized payload skipped", len(turn.ToolCalls))
	}
}

func TestLiveTurnExpiresWithoutAnEnding(t *testing.T) {
	bus, srv := busWithRedis(t)
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	publishAll(t, bus, "th-1", app.ChatEvent{
		JobID: "job-1", ThreadID: "th-1", Type: "started", Timestamp: time.Now(),
	})
	// A worker killed mid-run publishes no `final`; nothing else would ever
	// delete this, and a spinner is not a thing to leave on tomorrow's screen.
	srv.FastForward(liveTTL + time.Minute)

	turn, err := LoadLiveTurn(ctx, rdb, "th-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if turn != nil {
		t.Errorf("live turn survived its TTL: %+v", turn)
	}
}
