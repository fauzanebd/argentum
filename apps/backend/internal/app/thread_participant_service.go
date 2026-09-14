package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/domain"
)

// ThreadParticipantService is the membership half of a conversation (T-N2):
// which of the company's agents are in this room.
//
// It is a service of its own rather than four methods on ThreadService, which
// already runs to 766 lines and owns a different question — where a message
// belongs, and when a conversation has changed topic. Membership shares none of
// that machinery.
//
// **Nothing here routes a message.** Adding an agent to a room changes who
// *may* be addressed, not who answers; T-N3 is what reads these rows at enqueue
// time. Until then this service is reachable, correct, and inert.
type ThreadParticipantService struct {
	participants domain.ThreadParticipantRepository
	threads      domain.ThreadRepository
	roster       RosterReader
	// max is the ceiling on one room. Zero takes domain.MaxThreadParticipants.
	max int
	// access refuses an agent the person adding it may not talk to (T-Z4). Nil
	// admits every agent, which is every room before roadmap 12.
	access AgentAccess
}

// WithAgentAccess makes a room refuse an agent the person adding it may not
// talk to (T-Z4). Optional: a room is the one place a person names an agent
// without the picker in between, so without this a member could add the HR
// agent to their conversation by id and then address it.
func (s *ThreadParticipantService) WithAgentAccess(a AgentAccess) *ThreadParticipantService {
	s.access = a
	return s
}

func NewThreadParticipantService(
	participants domain.ThreadParticipantRepository,
	threads domain.ThreadRepository,
	roster RosterReader,
	max int,
) *ThreadParticipantService {
	if max <= 0 {
		max = domain.MaxThreadParticipants
	}
	return &ThreadParticipantService{
		participants: participants, threads: threads, roster: roster, max: max,
	}
}

// ErrParticipantLimit is the cap being hit. A distinct sentinel rather than
// ErrInvalidInput because the caller can act on it — remove someone, or ask for
// a higher ceiling — and a 400 saying "invalid" tells them neither.
var ErrParticipantLimit = errors.New("this conversation already holds the maximum number of agents")

// ErrDefaultSpeaker is an attempt to remove the agent that answers when nobody
// is addressed. Change the default first, or remove it last.
var ErrDefaultSpeaker = errors.New("this agent answers messages that address nobody; make another agent the default first")

// ErrAgentDisabled is an attempt to add an agent an admin has taken out of
// service. AgentChannelBindingRepository.AgentForChannel applies the same rule
// to a channel binding, and for the same reason: disabling is how an admin
// stops an agent answering, and a room that could re-enlist one would make
// disabling a suggestion.
var ErrAgentDisabled = errors.New("that agent is disabled")

// ErrRoomNotOnWidget is an attempt to add an agent to a website widget's
// conversation (T-N10). A room is not enabled for widget sessions — see Add.
var ErrRoomNotOnWidget = errors.New("a website widget conversation holds one agent and cannot become a room")

// Capacity is the ceiling on one room, the default speaker's seat included.
//
// `POST /v1/threads` checks a whole room against it before the conversation is
// opened (T-N10), so a room that could never fit leaves nothing behind. Add
// still checks each insert, which is the check that holds under a race.
func (s *ThreadParticipantService) Capacity() int {
	return s.max
}

// List returns the room: the default speaker, then everyone added to it.
//
// **The default speaker is an implicit member and has no row.**
// `conversation_threads.agent_id` is written by all six thread-creating paths
// already; `thread_participants` would have been written by one. Reading the
// union here means no write path can forget, including the five that predate
// this table — see 078's comment, which is where the alternative and its
// failure mode are argued.
//
// The synthetic row carries the thread's own AddedAt and an empty AddedBy,
// because nobody added it: it is who the conversation was opened with.
func (s *ThreadParticipantService) List(ctx context.Context, companyID, threadID string) ([]*domain.ThreadParticipant, error) {
	thread, err := s.ownedThread(ctx, companyID, threadID)
	if err != nil {
		return nil, err
	}
	added, err := s.participants.ListByThread(ctx, companyID, threadID)
	if err != nil {
		return nil, err
	}
	if thread.AgentID == "" {
		// No default speaker: the thread runs as the company default, resolved
		// per turn. Naming it here would pin the conversation to whichever agent
		// happens to be default today, which is a different fact.
		return added, nil
	}

	name := ""
	if a, err := s.roster.GetByID(ctx, companyID, thread.AgentID); err == nil {
		name = a.Name
	}
	speaker := &domain.ThreadParticipant{
		ThreadID:  threadID,
		AgentID:   thread.AgentID,
		AgentName: name,
		AddedAt:   thread.CreatedAt,
	}
	out := make([]*domain.ThreadParticipant, 0, len(added)+1)
	out = append(out, speaker)
	for _, p := range added {
		// A row naming the default speaker should not exist — Add refuses it —
		// but a row written before it was made the default can, and a room
		// listing the same agent twice is worse than a skipped row.
		if p.AgentID != thread.AgentID {
			out = append(out, p)
		}
	}
	return out, nil
}

// Add puts one of the company's agents into a conversation.
//
// The order of the checks is the order they can be answered cheaply, and the
// tenant check is first: every refusal below this line has already established
// that the caller owns the thread, so none of them leaks whether an id exists.
func (s *ThreadParticipantService) Add(ctx context.Context, companyID, threadID, agentID, addedBy string) (*domain.ThreadParticipant, error) {
	if agentID == "" {
		return nil, fmt.Errorf("%w: agent_id is required", domain.ErrInvalidInput)
	}
	thread, err := s.ownedThread(ctx, companyID, threadID)
	if err != nil {
		return nil, err
	}
	// A website widget's conversation holds one agent (T-N10). The person on the
	// other end is a visitor to somebody else's site rather than staff, and a room
	// there is the roadmap's §7 — a tenant's customer addressing the HR agent —
	// arriving through the one door where nobody on it belongs to the company.
	// The widget has no route that adds a participant; this is what stops a
	// member adding one from the dashboard, which the tenant check alone allows.
	// Before the roster read, because the answer does not depend on the agent.
	if thread.Channel == domain.ChannelWidget {
		return nil, ErrRoomNotOnWidget
	}

	// ErrNotFound for another company's agent as much as for one that never
	// existed — RosterReader.GetByID's rule, and the reason is that the caller
	// is a browser holding a bare uuid and a distinguishable error is an
	// existence oracle across tenants.
	agent, err := s.roster.GetByID(ctx, companyID, agentID)
	if err != nil {
		return nil, err
	}
	// An agent this person may not talk to cannot join their room (T-Z4), and
	// it is refused as not found — the picker's rule, because the add menu
	// never offered it. **Before the disabled check**, so a restricted agent
	// that is also disabled does not confirm it exists by saying so.
	if s.access != nil && addedBy != "" {
		d, err := s.access.Decide(ctx, authz.Subject{CompanyID: companyID, UserID: addedBy}, domain.ResourceKindAgent, agentID)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrAccessCheckFailed, err)
		}
		if !d.Allowed {
			// Answered as not found, and recorded as what it was (T-Z9): the
			// person asked for an agent and was refused it. An agent the grant
			// store could not find is a race with a deletion, and refuses nobody.
			if d.Reason != authz.ReasonNotFound {
				authz.Record(ctx, s.access, authz.Refusal{
					Subject: authz.Subject{CompanyID: companyID, UserID: addedBy},
					Kind:    string(domain.ResourceKindAgent), ResourceID: agent.ID, Reason: d.Reason,
					Door: authz.DoorDashboard, Channel: domain.ChannelDashboard,
				})
			}
			return nil, domain.ErrNotFound
		}
	}
	if !agent.Enabled {
		return nil, ErrAgentDisabled
	}
	// The default speaker is already in the room without a row (see List), so
	// adding it is a duplicate and answers as one. Without this the unique
	// index would not fire — there is no row to collide with — and the room
	// would list the same agent twice.
	if thread.AgentID != "" && thread.AgentID == agentID {
		return nil, domain.ErrAlreadyExists
	}

	// Checked before the insert rather than enforced by the database, because
	// there is no constraint that can express "at most N rows per thread" and a
	// trigger that could would put the ceiling somewhere nobody reading this
	// service would find it. The race — two concurrent adds against a full
	// room — overshoots by one and is not worth a lock: the ceiling is a cost
	// control, not a security boundary.
	n, err := s.participants.CountByThread(ctx, companyID, threadID)
	if err != nil {
		return nil, err
	}
	if thread.AgentID != "" {
		// The default speaker occupies a seat and has no row. A cap that
		// counted only rows would let a four-agent room hold five.
		n++
	}
	if n >= s.max {
		return nil, fmt.Errorf("%w (%d)", ErrParticipantLimit, s.max)
	}

	p := &domain.ThreadParticipant{ThreadID: threadID, AgentID: agentID, AddedBy: addedBy}
	if err := s.participants.Add(ctx, companyID, p); err != nil {
		return nil, err
	}
	p.AgentName = agent.Name
	return p, nil
}

// Remove takes an agent out of a conversation.
//
// The default speaker cannot be removed while it is the default speaker. A room
// whose unaddressed messages have nowhere to go is not a state this service
// will produce — and the fix is one the caller can carry out, which is why the
// error says what it is.
func (s *ThreadParticipantService) Remove(ctx context.Context, companyID, threadID, agentID string) error {
	thread, err := s.ownedThread(ctx, companyID, threadID)
	if err != nil {
		return err
	}
	if thread.AgentID != "" && thread.AgentID == agentID {
		return ErrDefaultSpeaker
	}
	return s.participants.Remove(ctx, companyID, threadID, agentID)
}

// ownedThread is the tenant boundary, in one place so that no method above can
// be the one that forgot it.
func (s *ThreadParticipantService) ownedThread(ctx context.Context, companyID, threadID string) (*domain.ConversationThread, error) {
	if threadID == "" {
		return nil, fmt.Errorf("%w: thread id is required", domain.ErrInvalidInput)
	}
	return s.threads.GetForCompany(ctx, companyID, threadID)
}
