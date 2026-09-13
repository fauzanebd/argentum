package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/fauzanebd/argentum/internal/authz"
	"github.com/fauzanebd/argentum/internal/domain"
)

// CreatorAccess re-checks, when a watcher or a scheduled task fires, that the
// person who created it may still reach the agent it runs as (T-Z8, roadmap 12
// decision 11).
//
// Neither job passes through ChatEnqueuer — each builds its worker payload
// itself (access-grants §10c, item 9) — so none of T-Z4's checks ever saw one,
// and a member who lost HR could keep a schedule asking HR about payroll every
// Monday. That is the roadmap's "hole with a schedule attached", closed at the
// one moment a job does anything: the fire.
//
// The answer is a reason rather than a boolean, because a refused job is
// switched off and has to say why. Empty means run.
type CreatorAccess struct {
	access  AgentAccess
	users   CreatorDirectory
	roster  DefaultAgentReader
	threads JobThreadReader
}

// CreatorDirectory is the one user read the check makes. domain.UserRepository
// satisfies it.
type CreatorDirectory interface {
	GetByID(ctx context.Context, id string) (*domain.User, error)
}

// DefaultAgentReader resolves the company default, which is what a job whose
// thread names no agent runs as — every watcher, and every schedule nobody has
// pinned. domain.AgentRepository satisfies it.
type DefaultAgentReader interface {
	GetDefault(ctx context.Context, companyID string) (*domain.Agent, error)
}

// JobThreadReader reads a job's dedicated thread for the agent it is pinned to.
// *ThreadService satisfies it.
type JobThreadReader interface {
	GetByID(ctx context.Context, id string) (*domain.ConversationThread, error)
}

// NewCreatorAccess wires the check. Every dependency is required on the worker;
// a nil *CreatorAccess checks nothing, which is the API's instance of both
// services, where nothing fires.
func NewCreatorAccess(access AgentAccess, users CreatorDirectory, roster DefaultAgentReader, threads JobThreadReader) *CreatorAccess {
	return &CreatorAccess{access: access, users: users, roster: roster, threads: threads}
}

// Check answers whether a job created by creatorID, running in threadID, may fire.
//
// **Only a restricted agent is checked against the creator.** An open agent is
// every member's (decision 3), and a job whose creator has since left keeps
// running on one exactly as it did before this ticket: roadmap 12 is about what
// a restriction closes, and a workspace with nothing restricted must not find
// its schedules switching themselves off the day it ships.
//
// On a restricted agent the creator must hold a grant **and** still be in the
// workspace. The second is its own read, because removing a member deactivates
// their row rather than deleting it and their grant rows survive — authz reads
// grants, not accounts. A creator deleted outright is removed too: a schedule's
// user_id is SET NULL, and a watcher's created_by references nothing.
//
// An error means the answer could not be read, and the caller runs nothing and
// switches nothing off: a database blip must neither fire a job nobody may run
// nor disable one somebody may.
func (c *CreatorAccess) Check(ctx context.Context, companyID, creatorID, threadID string) (domain.DisabledReason, error) {
	if c == nil || c.access == nil {
		return "", nil
	}
	agentID, err := c.jobAgent(ctx, companyID, threadID)
	if err != nil || agentID == "" {
		// No agent: a company with no roster, where the worker runs unscoped
		// and there is nothing to have restricted.
		return "", err
	}
	d, err := c.access.Decide(ctx, authz.Subject{CompanyID: companyID, UserID: creatorID}, domain.ResourceKindAgent, agentID)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrAccessCheckFailed, err)
	}
	if d.Reason == authz.ReasonOpen || d.Reason == authz.ReasonNotFound {
		// Not found is an agent deleted between the thread read and this one;
		// the worker then runs the default, and "restricted" would be the wrong
		// reason to give — turnTargets lets the same case through.
		return "", nil
	}
	member, err := c.inWorkspace(ctx, companyID, creatorID)
	if err != nil {
		return "", err
	}
	if !member {
		c.refused(ctx, companyID, creatorID, agentID, authz.ReasonCreatorRemoved)
		return domain.DisabledReasonCreatorRemoved, nil
	}
	if !d.Allowed {
		c.refused(ctx, companyID, creatorID, agentID, authz.ReasonNotGranted)
		return domain.DisabledReasonCreatorNotGranted, nil
	}
	return "", nil
}

// refused records a job refused the agent it runs as (T-Z9). Once per job, in
// practice: the caller switches the job off, and a job that is off does not fire
// to be refused again. The row names the creator whose access was checked, as
// the user the job was running as.
func (c *CreatorAccess) refused(ctx context.Context, companyID, creatorID, agentID string, reason authz.Reason) {
	authz.Record(ctx, c.access, authz.Refusal{
		Subject: authz.Subject{CompanyID: companyID, UserID: creatorID},
		Kind:    string(domain.ResourceKindAgent), ResourceID: agentID, Reason: reason,
		Door: authz.DoorJob,
	})
}

// jobAgent is the agent a job's turn will run as: its thread's, else the company
// default — the order ChatRunner.resolveAgent resolves it in. A thread that is
// gone reads as unpinned, as ScheduledTaskService.threadAgent reads it.
func (c *CreatorAccess) jobAgent(ctx context.Context, companyID, threadID string) (string, error) {
	if c.threads != nil && threadID != "" {
		t, err := c.threads.GetByID(ctx, threadID)
		switch {
		case err == nil && t != nil && t.AgentID != "":
			return t.AgentID, nil
		case err != nil && !errors.Is(err, domain.ErrNotFound):
			return "", fmt.Errorf("%w: thread: %w", ErrAccessCheckFailed, err)
		}
	}
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
	return def.ID, nil
}

// inWorkspace reports whether a creator is still an active member of the
// company. An empty id is a creator deleted outright. With no directory wired
// the grant alone decides.
func (c *CreatorAccess) inWorkspace(ctx context.Context, companyID, userID string) (bool, error) {
	if userID == "" {
		return false, nil
	}
	if c.users == nil {
		return true, nil
	}
	u, err := c.users.GetByID(ctx, userID)
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%w: creator: %w", ErrAccessCheckFailed, err)
	}
	return u != nil && u.CompanyID == companyID && u.Active(), nil
}
