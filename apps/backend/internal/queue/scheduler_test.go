package queue

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fauzanebd/argentum/internal/domain"
)

// stubWatcherRepo satisfies domain.WatcherRepository by embedding it (nil) and
// overriding only the one method the config provider calls. Any other call would
// panic, which is the assertion that the provider touches nothing else.
type stubWatcherRepo struct {
	domain.WatcherRepository
	list []*domain.Watcher
}

func (s stubWatcherRepo) ListEnabledForScheduler(context.Context) ([]*domain.Watcher, error) {
	return s.list, nil
}

func TestWatcherConfigProviderEmitsOneConfigPerWatcher(t *testing.T) {
	repo := stubWatcherRepo{list: []*domain.Watcher{
		{ID: "w-1", CronExpression: "0 9 * * *", Timezone: "UTC"},
		{ID: "w-2", CronExpression: "*/15 * * * *", Timezone: "Asia/Jakarta"},
	}}
	p := NewWatcherConfigProvider(repo)

	cfgs, err := p.GetConfigs()
	if err != nil {
		t.Fatalf("GetConfigs: %v", err)
	}
	if len(cfgs) != 2 {
		t.Fatalf("want 2 configs, got %d", len(cfgs))
	}
	for _, c := range cfgs {
		if c.Task.Type() != TypeWatcherEval {
			t.Errorf("task type = %q, want %q", c.Task.Type(), TypeWatcherEval)
		}
	}
	// A non-UTC timezone is folded into the cron spec so asynq fires it in the
	// tenant's zone, matching the scheduled-task provider.
	if got := cfgs[1].Cronspec; got != "CRON_TZ=Asia/Jakarta */15 * * * *" {
		t.Errorf("tz cronspec = %q", got)
	}
	// The payload names the watcher and nothing else.
	var payload WatcherEvalPayload
	if err := json.Unmarshal(cfgs[0].Task.Payload(), &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload.WatcherID != "w-1" {
		t.Errorf("payload watcher id = %q, want w-1", payload.WatcherID)
	}
}
