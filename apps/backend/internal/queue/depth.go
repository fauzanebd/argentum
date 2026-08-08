package queue

import (
	"context"
	"time"

	"github.com/hibiken/asynq"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/metrics"
)

// DefaultDepthInterval is how often the poller asks Redis. Queue depth is read
// to answer "is work piling up", which is a question about minutes rather than
// seconds, and each sample is one round trip per queue.
const DefaultDepthInterval = 15 * time.Second

// DepthPoller samples asynq's queue statistics into the metrics collector.
//
// It exists because depth is the one number in the exposition that this
// process cannot count: every other metric is something Argentum did, while a
// backlog is a fact about Redis that is equally true when this process has
// done nothing at all. asynq keeps it, so the honest way to export it is to
// ask on a ticker rather than to maintain a parallel counter that drifts the
// first time a task is enqueued by a process that is not this one.
type DepthPoller struct {
	inspector *asynq.Inspector
	sink      *metrics.Collector
	interval  time.Duration
}

// NewDepthPoller builds a poller against the same Redis asynq uses. interval
// <= 0 falls back to DefaultDepthInterval.
func NewDepthPoller(opt asynq.RedisConnOpt, sink *metrics.Collector, interval time.Duration) *DepthPoller {
	if interval <= 0 {
		interval = DefaultDepthInterval
	}
	if sink == nil {
		sink = metrics.Default()
	}
	return &DepthPoller{inspector: asynq.NewInspector(opt), sink: sink, interval: interval}
}

// Run samples once immediately, then on the ticker, until ctx is done. It is
// meant to be started in a goroutine and never returns an error: a scrape that
// is missing its queue series is a worse outcome than a log line, but it is
// nowhere near bad enough to take the process down.
func (p *DepthPoller) Run(ctx context.Context) {
	p.sample()
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := p.inspector.Close(); err != nil {
				logrus.WithError(err).Debug("queue depth: inspector close")
			}
			return
		case <-t.C:
			p.sample()
		}
	}
}

// sample reads every queue asynq knows about. Queues are discovered rather
// than configured: WORKER_QUEUES lives on the worker, and an API process that
// only knew about the queues it was told about would stop reporting the moment
// somebody added one.
func (p *DepthPoller) sample() {
	names, err := p.inspector.Queues()
	if err != nil {
		logrus.WithError(err).Warn("queue depth: could not list queues; gauges will go stale")
		return
	}
	out := make(map[string]metrics.QueueDepth, len(names))
	for _, name := range names {
		info, err := p.inspector.GetQueueInfo(name)
		if err != nil {
			// One unreadable queue must not blank the others: the map is
			// replaced wholesale, so returning early here would drop every
			// gauge over a single bad queue.
			logrus.WithError(err).WithField("queue", name).Warn("queue depth: queue info failed")
			continue
		}
		out[name] = metrics.QueueDepth{
			Pending:   int64(info.Pending),
			Active:    int64(info.Active),
			Scheduled: int64(info.Scheduled),
			Retry:     int64(info.Retry),
			Archived:  int64(info.Archived),
		}
	}
	p.sink.SetQueueDepths(out)
}
