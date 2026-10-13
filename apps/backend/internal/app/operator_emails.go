package app

import (
	"context"
	"errors"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
)

// OperatorEmails is PLATFORM_OPERATOR_EMAILS as the account doors read it
// (RIZ-89 R16): addresses nobody may sign up with, be invited as, or accept an
// invitation to.
//
// **Why the doors and not the check.** OperatorOnly matches the email stored
// on the caller's account, and nothing in this product verifies an email. So
// an operator address with no account yet could be claimed by anyone — by
// signing up with it, or by inviting it into their own workspace and accepting
// the link they were handed — and that account would pass the operator check.
// Since RIZ-89 that check is the only thing between a script and free credit,
// so the hole is closed where the account is created rather than by teaching
// the middleware to doubt an email it has no way to doubt.
//
// Accounts that already exist are untouched: an operator who is already
// signed up keeps working, and R17's boot warning names any entry that has no
// active account so the owner can create it the safe way — before adding the
// address to the list.
type OperatorEmails struct{ set map[string]bool }

// NewOperatorEmails normalizes the list the way OperatorOnly compares it:
// trimmed and lowercased, empty entries dropped.
func NewOperatorEmails(emails []string) OperatorEmails {
	set := make(map[string]bool, len(emails))
	for _, e := range emails {
		if e = normalizeEmail(e); e != "" {
			set[e] = true
		}
	}
	return OperatorEmails{set: set}
}

// Has reports whether email is an operator's, compared trimmed and
// case-insensitively. The zero value has no operators.
func (o OperatorEmails) Has(email string) bool {
	return o.set[normalizeEmail(email)]
}

func normalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// OperatorAccountLookup is the one question the boot check asks.
type OperatorAccountLookup interface {
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
}

// LogOperatorAccounts warns, once at boot, about every operator entry with no
// active account (RIZ-89 R17). The line names the entry's position on the list
// and never the address: the list is who can approve money, and the log is
// read by more people than the deployment's secrets are.
//
// It is a warning and not a refusal to boot, for LogDSNKeyCoverage's reason:
// an allowlist with one stale entry still has operators who can work. What the
// owner should do about it is in the rollout notes — create the account before
// listing it, or take the entry off — and since R16 nobody else can create it.
func LogOperatorAccounts(ctx context.Context, users OperatorAccountLookup, emails []string) {
	if users == nil {
		return
	}
	for i, e := range emails {
		e = normalizeEmail(e)
		if e == "" {
			continue
		}
		u, err := users.GetByEmail(ctx, e)
		switch {
		case errors.Is(err, domain.ErrNotFound) || (err == nil && (u == nil || !u.Active())):
			logrus.Warnf("PLATFORM_OPERATOR_EMAILS entry %d has no active account", i+1)
		case err != nil:
			logrus.WithError(err).Warnf("PLATFORM_OPERATOR_EMAILS entry %d could not be checked; skipping it", i+1)
		}
	}
}
