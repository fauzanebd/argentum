package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	pgctl "github.com/fauzanebd/argentum/internal/adapters/postgres"
	"github.com/fauzanebd/argentum/internal/adapters/storage"
	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/email"
)

// buildTopupService wires the manual credit top-up (RIZ-83).
//
// A configuration that will not parse turns top-up off with an error line and
// boots anyway (R2): a payment page misconfigured is not a reason to take chat
// down with it. Off means no request can be made; the operator queue still
// works, so a payee removed mid-review does not strand a paid customer.
//
// The boot line says how many payees and never which: the account numbers are
// the owner's, and they stay out of logs as they stay out of git.
func buildTopupService(
	cfg *config.Config, controlDB *sql.DB, rdb *redis.Client, bucket *storage.StorageService,
	usage *app.UsageService, mailer email.Sender, companies app.CompanyNameLookup, users app.InviterLookup,
) *app.TopupService {
	tc, err := cfg.TopupSettings()
	switch {
	case errors.Is(err, config.ErrTopupPayeesUnset):
		logrus.Info("topup: TOPUP_PAYEES is not set; credit top-up is off")
	case err != nil:
		logrus.WithError(err).Error("topup: configuration is invalid; credit top-up is off")
		tc.Payees = nil
	default:
		logrus.Infof("topup: %d payees, spread %s%%, fallback %d, range %d–%d",
			len(tc.Payees), spreadPct(tc.SpreadBP), tc.FallbackIDRPerUSD, tc.MinIDR, tc.MaxIDR)
	}

	rates := app.NewTopupRates(
		app.NewFrankfurterSource(tc.FXURL, tc.FXTimeout),
		app.NewRedisFXCache(rdb, tc.FXMaxAge),
		app.TopupRatePolicy{
			SpreadBP:          tc.SpreadBP,
			FallbackIDRPerUSD: tc.FallbackIDRPerUSD,
			Timeout:           tc.FXTimeout,
			CacheFor:          tc.FXCacheFor,
			MaxAge:            tc.FXMaxAge,
		},
	)
	svc := app.NewTopupService(pgctl.NewTopupRepo(controlDB), rates, app.TopupSettings{
		MinIDR:     tc.MinIDR,
		MaxIDR:     tc.MaxIDR,
		PresetsIDR: tc.PresetsIDR,
		Payees:     tc.Payees,
	}).
		WithBudget(usage).
		WithMailer(app.NewTopupMailer(mailer, cfg.PlatformOperatorEmails, cfg.AppBaseURL), companies, users)
	// A nil *StorageService in the interface would not be a nil interface —
	// brandingStore's care in bootstrap.go, for the same reason.
	if bucket != nil {
		svc = svc.WithProofStore(bucket)
	}
	if len(cfg.PlatformOperatorEmails) == 0 {
		logrus.Info("topup: PLATFORM_OPERATOR_EMAILS is empty; nobody can review top-ups")
	}
	return svc
}

// spreadPct renders basis points as the percentage an operator typed: 500 is
// "5", 250 is "2.5".
func spreadPct(bp int) string {
	s := fmt.Sprintf("%d.%02d", bp/100, bp%100)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	return s
}
