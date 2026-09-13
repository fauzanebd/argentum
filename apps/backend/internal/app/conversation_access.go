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
	if c == nil || c.agents == nil || c.access == nil || userID == "" || len(threadIDs) == 0 {
		return threadIDs, nil
	}
	lower := make([]string, len(threadIDs))
	for i, id := range threadIDs {
		lower[i] = strings.ToLower(strings.TrimSpace(id))
	}
	byThread, err := c.agents.AgentsByThread(ctx, companyID, lower)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAccessCheckFailed, err)
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
					return nil, err
				}
				defLoaded = true
			}
			if def != "" {
				want(def)
			}
		}
	}

	allowed, err := c.allowedAgents(ctx, authz.Subject{CompanyID: companyID, UserID: userID}, ask)
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(threadIDs))
	for i, id := range lower {
		agents, found := byThread[id]
		if !found {
			continue
		}
		if len(agents) == 0 {
			if def == "" || allowed[def] {
				out = append(out, threadIDs[i])
			}
			continue
		}
		readable := true
		for _, a := range agents {
			if !allowed[a] {
				readable = false
				break
			}
		}
		if readable {
			out = append(out, threadIDs[i])
		}
	}
	return out, nil
}

// MayRead is Readable for one conversation.
func (c *ConversationAccess) MayRead(ctx context.Context, companyID, userID, threadID string) (bool, error) {
	out, err := c.Readable(ctx, companyID, userID, []string{threadID})
	if err != nil {
		return false, err
	}
	return len(out) == 1, nil
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
