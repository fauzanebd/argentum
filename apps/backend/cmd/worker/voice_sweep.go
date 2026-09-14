package main

import (
	"context"

	"github.com/hibiken/asynq"
	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/app"
)

// makeVoiceClipSweepHandler deletes voice clips past their retention, and the
// clips of deleted conversations (T-W7).
//
// Errors are swallowed to nil for makeRetentionPurgeHandler's reason. The only
// one that reaches here is "the list could not be read"; the service already
// keeps a clip whose audio would not delete and tries it next tick. The next
// tick is within the hour, and asynq's backoff retrying a deployment-wide sweep
// buys nothing a tick does not.
func makeVoiceClipSweepHandler(svc *app.VoiceClips) asynq.HandlerFunc {
	return func(ctx context.Context, _ *asynq.Task) error {
		if svc == nil {
			return nil
		}
		res, err := svc.Sweep(ctx)
		if err != nil {
			logrus.WithError(err).Error("voice clip sweep: could not list clips; the next tick retries")
			return nil
		}
		if res.Deleted > 0 || res.Kept > 0 {
			logrus.WithFields(logrus.Fields{
				"deleted": res.Deleted,
				"kept":    res.Kept,
			}).Info("voice clip sweep complete")
		}
		return nil
	}
}
