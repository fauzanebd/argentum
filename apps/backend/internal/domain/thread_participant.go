package domain

import (
	"context"
	"time"
)

// ThreadParticipant is one agent in one conversation (T-N2).
//
// The roster (T-S1) gave a company several agents and gave a conversation
// exactly one of them: `conversation_threads.agent_id`, resolved once per turn.
// That is the right shape for "which agent is this conversation with" and it
// cannot express "which agents are in this room", so a user who wants Ops and
// Finance on the same question opens two conversations and carries numbers
// between them by hand.
//
// **A room is a thread with more than one participant, not a new object.**
// Every thread that existed before this table is a room of one, created by
// 078's backfill, and no turn that ran before it runs differently after.
//
// Membership is not an access boundary and does not pretend to be one. T-S1's
// locked decision 1 stands: company membership is the authorization boundary,
// any member may open any of their company's agents, and an agent named "HR" is
// not a wall. A room makes that more visible rather than less — see the
// roadmap's §7.
type ThreadParticipant struct {
	ID       string `json:"id"`
	ThreadID string `json:"thread_id"`
	AgentID  string `json:"agent_id"`
	// AgentName rides along on reads so a participant list does not have to
	// join the roster in the browser. Not persisted — the same arrangement as
	// AgentChannelBinding.AgentName and Message.AgentName, and for the same
	// reason.
	AgentName string `json:"agent_name,omitempty"`
	// AddedBy is the user who put this agent in the room, or "" for a row the
	// backfill wrote and for one whose user has since been deleted. It is
	// provenance, not permission: nothing reads it to decide anything.
	AddedBy string    `json:"added_by,omitempty"`
	AddedAt time.Time `json:"added_at"`
}

// MaxThreadParticipants is the ceiling when no configuration says otherwise.
//
// Four is the number of jobs this product was built around — domain/agent.go's
// opening comment names Marketing, Ops, HR and Finance — and not a round number
// chosen to look like one. It is a default rather than a constant because the
// cost of a room has never been measured (the roadmap's §2c); Hermes Agent, the
// only shipped implementation of this feature, caps a room at six as a
// compile-time literal with an open issue asking for configurability, which is
// the mistake this default exists not to repeat.
const MaxThreadParticipants = 4

// ThreadParticipantRepository is the persistence contract for membership.
//
// Every method takes companyID, including the ones that already have a primary
// key. AgentRepository's rule, and the same reason: a repository that can be
// asked for a row by id alone is a repository whose callers have to remember
// the tenant check.
type ThreadParticipantRepository interface {
	// Add inserts one membership. The insert re-checks the agent against the
	// same company, so a participant can never name another tenant's agent even
	// if a caller reaches this layer without the service's validation —
	// AgentBindingRepo.Create's arrangement. ErrNotFound when that check fails,
	// ErrAlreadyExists when the agent is already in the room.
	Add(ctx context.Context, companyID string, p *ThreadParticipant) error
	// ListByThread returns the room, oldest member first, with names.
	ListByThread(ctx context.Context, companyID, threadID string) ([]*ThreadParticipant, error)
	// Remove deletes one membership by agent id rather than by row id. The
	// caller is a browser holding an agent, not a join row, and "remove Finance
	// from this conversation" should not require a lookup to express.
	// ErrNotFound when the agent is not in the room.
	Remove(ctx context.Context, companyID, threadID, agentID string) error
	// CountByThread is what the cap is checked against. Separate from
	// ListByThread because the check runs on a write path that does not need
	// the names.
	CountByThread(ctx context.Context, companyID, threadID string) (int, error)
}

// HasParticipant reports whether the given agent is in this conversation.
//
// One method, one place the membership rule is written down — AllowsTool's
// arrangement. It reads the Participants slice, so it answers false on a thread
// read without them; callers that need the answer must load the room, and the
// two reads that do are ThreadService.ListParticipants and the thread detail
// route.
func (t *ConversationThread) HasParticipant(agentID string) bool {
	for _, p := range t.Participants {
		if p.AgentID == agentID {
			return true
		}
	}
	return false
}
