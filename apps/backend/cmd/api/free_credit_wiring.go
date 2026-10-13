package main

import (
	"context"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/app"
	"github.com/fauzanebd/argentum/internal/config"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/email"
)

// buildFreeCreditService wires free credit by approval (RIZ-89).
//
// The mailer is the top-up alert's — the same SMTP, base URL and operator list,
// and the same "silent when any is unset" rule — so a deployment that already
// alerts on top-ups alerts on sign-ups with no new setting. It is handed to
// usage too, because sign-up files its request through UsageService and that
// is where the operator alert is sent from.
//
// The boot lines say what an operator would otherwise find out the hard way:
// which mode this process is in, and which allowlist entries have no account
// to guard them (R17). Since R16 nobody can create those accounts by signing
// up or by invitation, so a listed address with no account is one the owner
// must create before listing it, or drop.
func buildFreeCreditService(
	ctx context.Context, cfg *config.Config, grants domain.CreditGrantRequestRepository,
	usage *app.UsageService, mailer email.Sender, companies app.CompanyNameLookup, users app.InviterLookup,
	accounts app.OperatorAccountLookup,
) *app.FreeCreditService {
	notify := app.NewTopupMailer(mailer, cfg.PlatformOperatorEmails, cfg.AppBaseURL)
	usage.WithFreeCreditAlerts(notify)

	if cfg.CreditsGrantRequiresApproval {
		logrus.Info("credits: free credit needs an operator's approval (CREDITS_GRANT_REQUIRES_APPROVAL)")
	} else {
		logrus.Warn("credits: CREDITS_GRANT_REQUIRES_APPROVAL=false; every new workspace is granted free credit at sign-up")
	}
	app.LogOperatorAccounts(ctx, accounts, cfg.PlatformOperatorEmails)

	return app.NewFreeCreditService(grants).
		WithBudget(usage).
		WithMailer(notify, companies, users)
}
