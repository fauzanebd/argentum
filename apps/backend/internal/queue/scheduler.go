package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
)

// DBConfigProvider is the bridge between scheduled_tasks rows in Postgres
// and asynq's PeriodicTaskManager. The manager calls GetConfigs() every
// SyncInterval (configured at NewPeriodicTaskManager) and diffs against
// the previous snapshot, so creating, toggling, or editing a task in the
// database becomes effective on the next sync without a worker restart.
type DBConfigProvider struct {
	repo domain.ScheduledTaskRepository
}

func NewDBConfigProvider(repo domain.ScheduledTaskRepository) *DBConfigProvider {
	return &DBConfigProvider{repo: repo}
}

// GetConfigs returns one PeriodicTaskConfig per enabled scheduled_tasks
// row. Each config emits a `scheduled:run` task carrying just the task
// ID; the worker handler reloads the latest task definition on each fire.
func (p *DBConfigProvider) GetConfigs() ([]*asynq.PeriodicTaskConfig, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tasks, err := p.repo.ListEnabledForScheduler(ctx)
	if err != nil {
		return nil, fmt.Errorf("scheduler list enabled: %w", err)
	}

	cfgs := make([]*asynq.PeriodicTaskConfig, 0, len(tasks))
	for _, t := range tasks {
		spec := t.CronExpression
		if t.Timezone != "" && t.Timezone != "UTC" {
			spec = "CRON_TZ=" + t.Timezone + " " + spec
		}
		body, err := json.Marshal(ScheduledRunPayload{TaskID: t.ID})
		if err != nil {
			logrus.WithError(err).WithField("task_id", t.ID).Warn("scheduler: marshal payload")
			continue
		}
		cfgs = append(cfgs, &asynq.PeriodicTaskConfig{
			Cronspec: spec,
			Task:     asynq.NewTask(TypeScheduledTaskRun, body),
			Opts: []asynq.Option{
				asynq.MaxRetry(2),
				asynq.Timeout(15 * time.Minute),
				asynq.Retention(7 * 24 * time.Hour),
			},
		})
	}
	return cfgs, nil
}

// WatcherConfigProvider is the watcher half of the same bridge (T-08): it turns
// enabled `watchers` rows into `watcher:eval` periodic configs. A second provider
// rather than a second scheduler — the ticket's exact instruction. It runs under
// its own PeriodicTaskManager because the two tables are read by two repositories
// and diffed against two independent snapshots; the entries never collide because
// they carry different task types.
type WatcherConfigProvider struct {
	repo domain.WatcherRepository
}

func NewWatcherConfigProvider(repo domain.WatcherRepository) *WatcherConfigProvider {
	return &WatcherConfigProvider{repo: repo}
}

// GetConfigs returns one PeriodicTaskConfig per enabled watcher. Each emits a
// `watcher:eval` carrying just the watcher id; the handler reloads the row so a
// firing always uses the latest condition.
func (p *WatcherConfigProvider) GetConfigs() ([]*asynq.PeriodicTaskConfig, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	watchers, err := p.repo.ListEnabledForScheduler(ctx)
	if err != nil {
		return nil, fmt.Errorf("scheduler list enabled watchers: %w", err)
	}

	cfgs := make([]*asynq.PeriodicTaskConfig, 0, len(watchers))
	for _, w := range watchers {
		spec := w.CronExpression
		if w.Timezone != "" && w.Timezone != "UTC" {
			spec = "CRON_TZ=" + w.Timezone + " " + spec
		}
		body, err := json.Marshal(WatcherEvalPayload{WatcherID: w.ID})
		if err != nil {
			logrus.WithError(err).WithField("watcher_id", w.ID).Warn("scheduler: marshal watcher payload")
			continue
		}
		cfgs = append(cfgs, &asynq.PeriodicTaskConfig{
			Cronspec: spec,
			Task:     asynq.NewTask(TypeWatcherEval, body),
			Opts: []asynq.Option{
				// One retry: a breach evaluation that failed on a transient DB blip
				// is worth one more attempt, but a watcher fires again on its next
				// cron tick regardless, so a long retry chain only delays the next
				// clean evaluation.
				asynq.MaxRetry(1),
				asynq.Timeout(5 * time.Minute),
				asynq.Retention(7 * 24 * time.Hour),
			},
		})
	}
	return cfgs, nil
}
