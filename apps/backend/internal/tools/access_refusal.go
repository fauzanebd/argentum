package tools

import (
	"context"

	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

// recordRefusal records a person on a turn refused one object by a tool (T-Z9),
// through the authorizer the tool decided with.
//
// A tool knows its turn only through the context, so the door is the turn's
// channel and the actor is the turn's own when it set one — a scheduled task's,
// whose turn carries its creator as the person (access-grants §15c). Otherwise
// the refusal names the person, as every dashboard turn's audit rows do.
func recordRefusal(ctx context.Context, access any, s authz.Subject, kind domain.ResourceKind, id string, reason authz.Reason) {
	channel := domain.Channel(tenantctx.Channel(ctx))
	r := authz.Refusal{
		Subject: s, Kind: string(kind), ResourceID: id, Reason: reason,
		Door: authz.DoorOf(channel), Channel: channel,
	}
	if actorKind, actorRef := tenantctx.Actor(ctx); actorKind != "" {
		r.ActorKind, r.ActorRef = domain.ActorKind(actorKind), actorRef
	}
	authz.Record(ctx, access, r)
}
