package app

import (
	"context"

	"github.com/fauzanebd/argentum/internal/domain"
)

// Room adapts the two reads addressing needs onto one value (T-N3).
//
// It exists because the halves live in different places and neither should grow
// the other's method: membership is ThreadParticipantService's, and the roster
// listing is the agent repository's. A struct here is cheaper than either a
// method on a service that has no business listing agents, or an interface the
// bootstrap has to satisfy with an anonymous type.
type Room struct {
	participants *ThreadParticipantService
	agents       interface {
		ListByCompany(ctx context.Context, companyID string) ([]*domain.Agent, error)
	}
}

// NewRoom joins the two reads. Either being nil makes the whole thing nil-safe
// at the call site rather than here: ChatEnqueuer.WithRoom is optional, and a
// deployment that wires neither behaves exactly as it did before this ticket.
func NewRoom(participants *ThreadParticipantService, agents interface {
	ListByCompany(ctx context.Context, companyID string) ([]*domain.Agent, error)
}) *Room {
	return &Room{participants: participants, agents: agents}
}

// ListParticipants is the room, default speaker included — see
// ThreadParticipantService.List for why that member has no row.
func (r *Room) ListParticipants(ctx context.Context, companyID, threadID string) ([]*domain.ThreadParticipant, error) {
	return r.participants.List(ctx, companyID, threadID)
}

// ListAgents is the company's roster. Read only when a message contains an `@`
// that matched no participant, which is what makes "a name the company has but
// this conversation does not" a refusal rather than silent text.
func (r *Room) ListAgents(ctx context.Context, companyID string) ([]*domain.Agent, error) {
	return r.agents.ListByCompany(ctx, companyID)
}
