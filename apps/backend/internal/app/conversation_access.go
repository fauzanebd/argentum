package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/domain"
)

// ErrConversationNotFound is a conversation the person may not read, answered
// exactly as one that does not exist. Wraps domain.ErrNotFound.
var ErrConversationNotFound = fmt.Errorf("%w: no such conversation", domain.ErrNotFound)

// ConversationAgentLoader is the one read a conversation's readability needs:
// which agents each conversation holds or has held. postgres.ThreadRepo is the
// production one.
type ConversationAgentLoader interface {
	AgentsByThread(ctx context.Context, companyID string, threadIDs []string) (map[string][]string, error)
}

// ConversationAccess answers whether a person may read a conversation (T-Z10).
//
// **A conversation is as restricted as the agents in it.** T-Z4 stopped a person
// talking to an agent they were not granted; it did not stop them reading what
// that agent told somebody who was, because every conversation in a company was
// listable and readable by every member. Restricting HR while its answers about
// payroll sat in the thread list would be the boundary on paper that roadmap 12
// exists to avoid, one level up. The owner chose this rule over making
// conversations private to their author on 2026-09-12 (access-grants.md §10d):
// it changes nothing for a tenant with nothing restricted, and it keeps the
// shared thread list everybody already uses.
//
// The rule, all of it: a person may read a conversation when they may talk to
// **every** agent that is or was in it — its own, its room's, and every agent
// that wrote in it (ThreadRepo.AgentsByThread says why all three). A
// conversation with none of them runs as the company default and is judged by
// it. An agent the grant store cannot find — deleted, its messages outliving it
// by 077's design — restricts nothing, for RequireResource's reason.
//
// The decision is still internal/authz's. This composes the question — which
// agents — and asks it once per page.
//
// A nil *ConversationAccess, or no person, reads everything, which is every
// conversation before this ticket and every door that has no Argentum user. The
// methods are nil-safe on purpose, so a wiring that never built one degrades to
// that rather than to a panic.
type ConversationAccess struct {
	agents ConversationAgentLoader
	access AgentAccess
	roster RosterReader
}

// NewConversationAccess wires the three reads: conversations' agents, the grant
// decision, and the company default for a conversation that names no agent.
func NewConversationAccess(agents ConversationAgentLoader, access AgentAccess, roster RosterReader) *ConversationAccess {
	return &ConversationAccess{agents: agents, access: access, roster: roster}
}

// Readable returns the conversations among threadIDs the person may read, in the
// order given. A conversation that is not the company's is not returned.
//
// Two loads for a page however long — the agents, then one grant read over the
// union of every agent on the page — plus the default when some conversation
// needs it, and one decision per agent the grant read did not admit, to tell a
// restricted agent from a deleted one.
func (c *ConversationAccess) Readable(ctx context.Context, companyID, userID string, threadIDs []string) ([]string, error) {
	readable, _, err := c.sift(ctx, companyID, userID, threadIDs)
	return readable, err
}

// MayRead is Readable for one conversation — the question every route that
// opens, streams or writes into a conversation by id asks.
//
// A conversation of this company the person may not read is a refusal, and is
// recorded (T-Z9). Readable records nothing: a thread list that leaves one out
// is not somebody being turned away, and counting it would count page loads.
func (c *ConversationAccess) MayRead(ctx context.Context, companyID, userID, threadID string) (bool, error) {
	readable, hidden, err := c.sift(ctx, companyID, userID, []string{threadID})
	if err != nil {
		return false, err
	}
	if len(hidden) > 0 {
		authz.Record(ctx, c.access, authz.Refusal{
			Subject: authz.Subject{CompanyID: companyID, UserID: userID},
			Kind:    authz.KindConversation, ResourceID: hidden[0], Reason: authz.ReasonNotGranted,
			Door: authz.DoorDashboard, Channel: domain.ChannelDashboard,
		})
	}
	return len(readable) == 1, nil
}

// sift splits threadIDs into the conversations the person may read, in the
// order given, and those of this company they may not. A conversation that is not
// the company's is in neither.
func (c *ConversationAccess) sift(ctx context.Context, companyID, userID string, threadIDs []string) (readable, hidden []string, err error) {
	if c == nil || c.agents == nil || c.access == nil || userID == "" || len(threadIDs) == 0 {
		return threadIDs, nil, nil
	}
	return c.siftFor(ctx, authz.Subject{CompanyID: companyID, UserID: userID}, threadIDs)
}

// OpenToEveryone reports whether every agent that is or was in a conversation is
// open — whether anybody at all could be refused it (T-Z13).
//
// It is the question a public link asks. A link has no person behind it, so it
// cannot ask whether *a* person may read the conversation a document came from;
// it asks as nobody, who holds no grant, and nobody reads exactly the
// conversations whose agents are all open. The rule is T-Z10's in every other
// respect: an unattributed conversation is judged by the company default, and an
// agent that no longer exists restricts nothing.
//
// Nothing is recorded: a refusal needs somebody to have been refused (T-Z9,
// access-grants §18c). A conversation this company does not have restricts
// nothing, and a nil *ConversationAccess answers open, as before roadmap 12.
func (c *ConversationAccess) OpenToEveryone(ctx context.Context, companyID, threadID string) (bool, error) {
	if c == nil || c.agents == nil || c.access == nil || strings.TrimSpace(threadID) == "" {
		return true, nil
	}
	_, hidden, err := c.siftFor(ctx, authz.Subject{CompanyID: companyID}, []string{threadID})
	if err != nil {
		return false, err
	}
	return len(hidden) == 0, nil
}

// siftFor is sift for any subject — a person, or nobody.
func (c *ConversationAccess) siftFor(ctx context.Context, s authz.Subject, threadIDs []string) (readable, hidden []string, err error) {
	companyID := s.CompanyID
	lower := make([]string, len(threadIDs))
	for i, id := range threadIDs {
		lower[i] = strings.ToLower(strings.TrimSpace(id))
	}
	byThread, err := c.agents.AgentsByThread(ctx, companyID, lower)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrAccessCheckFailed, err)
	}

	def, defLoaded := "", false
	var ask []string
	seen := map[string]bool{}
	want := func(agentID string) {
		if !seen[agentID] {
			seen[agentID] = true
			ask = append(ask, agentID)
		}
	}
	for _, id := range lower {
		agents, found := byThread[id]
		switch {
		case !found:
		case len(agents) > 0:
			for _, a := range agents {
				want(a)
			}
		default:
			if !defLoaded {
				if def, err = c.defaultAgent(ctx, companyID); err != nil {
					return nil, nil, err
				}
				defLoaded = true
			}
			if def != "" {
				want(def)
			}
		}
	}

	allowed, err := c.allowedAgents(ctx, s, ask)
	if err != nil {
		return nil, nil, err
	}

	out := make([]string, 0, len(threadIDs))
	for i, id := range lower {
		agents, found := byThread[id]
		if !found {
			continue
		}
		open := def == "" || allowed[def]
		if len(agents) > 0 {
			open = true
			for _, a := range agents {
				if !allowed[a] {
					open = false
					break
				}
			}
		}
		if open {
			out = append(out, threadIDs[i])
		} else {
			hidden = append(hidden, id)
		}
	}
	return out, hidden, nil
}

// allowedAgents is the set of agents among ids the subject may talk to — or that
// no longer exist, which restrict nothing. Visible cannot tell those two apart,
// so an agent it did not admit is asked about once more, and only then.
func (c *ConversationAccess) allowedAgents(ctx context.Context, s authz.Subject, ids []string) (map[string]bool, error) {
	allowed := make(map[string]bool, len(ids))
	if len(ids) == 0 {
		return allowed, nil
	}
	open, err := c.access.Visible(ctx, s, domain.ResourceKindAgent, ids)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAccessCheckFailed, err)
	}
	for _, id := range open {
		allowed[id] = true
	}
	for _, id := range ids {
		if allowed[id] {
			continue
		}
		d, err := c.access.Decide(ctx, s, domain.ResourceKindAgent, id)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrAccessCheckFailed, err)
		}
		if d.Reason == authz.ReasonNotFound {
			allowed[id] = true
		}
	}
	return allowed, nil
}

// defaultAgent is the company default, "" when the company has none, and an
// access failure when it could not be read — an unattributed conversation
// judged by a default nobody could look up would be judged by nothing.
func (c *ConversationAccess) defaultAgent(ctx context.Context, companyID string) (string, error) {
	if c.roster == nil {
		return "", nil
	}
	def, err := c.roster.GetDefault(ctx, companyID)
	if errors.Is(err, domain.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("%w: default agent: %w", ErrAccessCheckFailed, err)
	}
	return strings.ToLower(def.ID), nil
}
