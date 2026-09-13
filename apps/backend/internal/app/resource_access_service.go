package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/tenantctx"
)

var errResourceAccessUnconfigured = errors.New("resource access is not configured")

// The audit rows a change to who may reach what writes (T-Z9, roadmap 12's
// decision 12: "a grant nobody can review is a grant nobody can trust"). Pseudo
// tool names, in the shape the acknowledgement row uses: nothing ran, and the row
// records who changed what for whom.
const (
	AccessGrantAudit      = "access.grant"
	AccessRevokeAudit     = "access.revoke"
	AccessModeAudit       = "access.mode"
	CapabilityGrantAudit  = "capability.grant"
	CapabilityRevokeAudit = "capability.revoke"
)

// AccessAudit is where a change to access is written down: T-05's audit log.
// domain.AgentActionRepository satisfies it. Declared narrow, like BindingAudit,
// so neither service that writes to the log can read it.
type AccessAudit interface {
	Create(ctx context.Context, a *domain.AgentAction) error
}

// ResourceAccessService is an admin restricting a resource and saying who may
// reach it (T-Z2). It decides nothing about a request — internal/authz does,
// against the same repository — and that split is deliberate: the routes that
// change access are few, admin-only and slow-moving, while the decision runs on
// every picker render and every turn, and the two should not share a type that
// makes it easy to call one where the other was meant.
//
// Every method parses the kind and mode it was handed, so nothing reaches the
// repository with a string outside the vocabulary.
//
// **Every change is written down (T-Z9), and the direction of a change decides
// what happens when it cannot be.** A change that opens something — a grant,
// re-opening a resource — is undone if its record cannot be written, as T-Z8
// undoes a binding whose acknowledgement could not be: an opening nobody can
// review is the thing decision 12 exists to prevent. A change that closes
// something — a revoke, a restriction — stands, and the failure is logged at
// Error: holding a revoke back because the audit log is down would leave access
// open on a database blip, which is the one failure a boundary must not have. A
// change that changes nothing writes no row.
type ResourceAccessService struct {
	repo  domain.ResourceGrantRepository
	audit AccessAudit
}

// NewResourceAccessService wires the repository and the audit log. A nil audit
// writes nothing and reads nothing extra — this service exactly as it was before
// T-Z9, which is what a test that is not about the record wants.
func NewResourceAccessService(repo domain.ResourceGrantRepository, audit AccessAudit) *ResourceAccessService {
	return &ResourceAccessService{repo: repo, audit: audit}
}

// View returns a resource's mode and everyone granted on it.
func (s *ResourceAccessService) View(ctx context.Context, companyID, rawKind, id string) (*domain.ResourceAccessView, error) {
	if s == nil {
		return nil, errResourceAccessUnconfigured
	}
	kind, err := domain.ParseResourceKind(rawKind)
	if err != nil {
		return nil, err
	}
	return s.repo.View(ctx, companyID, kind, id)
}

// List returns View for every resource of a kind in the company, from one read.
// It is what Settings → Team renders both of its directions from (T-Z7): the
// people who may reach each agent, and the agents each person may reach, as one
// answer drawn twice rather than two answers that could be read a grant apart.
func (s *ResourceAccessService) List(ctx context.Context, companyID, rawKind string) ([]domain.ResourceAccessView, error) {
	if s == nil {
		return nil, errResourceAccessUnconfigured
	}
	kind, err := domain.ParseResourceKind(rawKind)
	if err != nil {
		return nil, err
	}
	return s.repo.ListViews(ctx, companyID, kind)
}

// ForUser returns every resource grant a person holds — the other direction of
// View, because an admin asks the question both ways.
func (s *ResourceAccessService) ForUser(ctx context.Context, companyID, userID string) ([]domain.ResourceGrant, error) {
	if s == nil {
		return nil, errResourceAccessUnconfigured
	}
	return s.repo.ListForUser(ctx, companyID, userID)
}

// SetAccessMode opens or restricts a resource. It writes no grant and removes
// none: restricting and then re-opening leaves every grant exactly where it was,
// so an admin who restricts the wrong dashboard by mistake undoes it with one
// flip rather than by re-granting forty people.
//
// Restricting a resource nobody holds a grant on makes it reachable by nobody —
// the admin doing it included. That is decision 4 and is not refused here; the
// warning before the flip is the settings page's (T-Z7), because it is the page
// that knows how many people are about to lose access.
//
// Restricting a dashboard revokes its live share links in the same transaction
// (T-Z5), and the change says how many — the one consequence of the flip that
// re-opening does not undo.
func (s *ResourceAccessService) SetAccessMode(ctx context.Context, companyID, actorID, rawKind, id, rawMode string) (domain.AccessModeChange, error) {
	if s == nil {
		return domain.AccessModeChange{}, errResourceAccessUnconfigured
	}
	kind, err := domain.ParseResourceKind(rawKind)
	if err != nil {
		return domain.AccessModeChange{}, err
	}
	mode, err := domain.ParseAccessMode(rawMode)
	if err != nil {
		return domain.AccessModeChange{}, err
	}
	// What it was, so the row says what changed and an opening that cannot be
	// recorded can be put back. Read only when there is a row to write.
	var previous domain.AccessMode
	if s.audit != nil {
		view, err := s.repo.View(ctx, companyID, kind, id)
		if err != nil {
			return domain.AccessModeChange{}, err
		}
		previous = view.AccessMode
	}
	// The flip always runs, even to the mode it already has: restricting again
	// is how a link the previous binary minted mid-deploy is revoked (§14c).
	change, err := s.repo.SetAccessMode(ctx, companyID, kind, id, mode)
	if err != nil {
		return domain.AccessModeChange{}, err
	}
	if s.audit != nil && (previous != mode || change.RevokedShares > 0) {
		err := recordAccessChange(ctx, s.audit, companyID, actorID, AccessModeAudit, kind, id, map[string]any{
			"access_mode":          mode,
			"previous_access_mode": previous,
			"revoked_shares":       change.RevokedShares,
		})
		switch {
		case err != nil && mode == domain.AccessModeOpen:
			if _, uerr := s.repo.SetAccessMode(context.WithoutCancel(ctx), companyID, kind, id, previous); uerr != nil {
				logrus.WithError(uerr).WithFields(logrus.Fields{
					"company_id": companyID, "resource_kind": kind, "resource_id": id,
				}).Error("a resource was opened, could not be audited, and could not be restricted again; restrict it by hand")
			}
			return domain.AccessModeChange{}, fmt.Errorf("record the change, so the resource stays %s: %w", previous, err)
		case err != nil:
			logrus.WithError(err).WithFields(logrus.Fields{
				"company_id": companyID, "resource_kind": kind, "resource_id": id, "changed_by": actorID,
			}).Error("a resource was restricted and its audit row could not be written; the restriction stands")
		}
	}
	logrus.WithFields(logrus.Fields{
		"company_id":     companyID,
		"resource_kind":  kind,
		"resource_id":    id,
		"access_mode":    mode,
		"changed_by":     actorID,
		"revoked_shares": change.RevokedShares,
	}).Info("Resource access mode changed")
	return change, nil
}

// Grant gives userID access to a resource. Idempotent. A grant on an open
// resource is stored and does nothing until the resource is restricted, which is
// how an admin prepares a restriction without a window in which nobody can reach
// the thing.
func (s *ResourceAccessService) Grant(ctx context.Context, companyID, actorID, userID, rawKind, id string) error {
	if s == nil {
		return errResourceAccessUnconfigured
	}
	kind, err := domain.ParseResourceKind(rawKind)
	if err != nil {
		return err
	}
	held, err := s.held(ctx, companyID, kind, id, userID)
	if err != nil {
		return err
	}
	if err := s.repo.Grant(ctx, companyID, userID, kind, id, actorID); err != nil {
		return err
	}
	if s.audit != nil && !held {
		err := recordAccessChange(ctx, s.audit, companyID, actorID, AccessGrantAudit, kind, id, map[string]any{"user_id": userID})
		if err != nil {
			// Undone. The grant was not held a moment ago, so the revoke takes back
			// exactly this one — unless another admin granted the same person in
			// the gap, which it takes back too, and that is the side to fail on.
			if rerr := s.repo.Revoke(context.WithoutCancel(ctx), companyID, userID, kind, id); rerr != nil {
				logrus.WithError(rerr).WithFields(logrus.Fields{
					"company_id": companyID, "user_id": userID, "resource_kind": kind, "resource_id": id,
				}).Error("a grant could not be audited or taken back; revoke it by hand")
			}
			return fmt.Errorf("record the grant, so nothing was granted: %w", err)
		}
	}
	logrus.WithFields(logrus.Fields{
		"company_id":    companyID,
		"user_id":       userID,
		"resource_kind": kind,
		"resource_id":   id,
		"granted_by":    actorID,
	}).Info("Resource access granted")
	return nil
}

// Revoke takes a person's grant away. Idempotent.
func (s *ResourceAccessService) Revoke(ctx context.Context, companyID, actorID, userID, rawKind, id string) error {
	if s == nil {
		return errResourceAccessUnconfigured
	}
	kind, err := domain.ParseResourceKind(rawKind)
	if err != nil {
		return err
	}
	held, err := s.held(ctx, companyID, kind, id, userID)
	if err != nil {
		return err
	}
	if err := s.repo.Revoke(ctx, companyID, userID, kind, id); err != nil {
		return err
	}
	if s.audit != nil && held {
		err := recordAccessChange(ctx, s.audit, companyID, actorID, AccessRevokeAudit, kind, id, map[string]any{"user_id": userID})
		if err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"company_id": companyID, "user_id": userID, "resource_kind": kind, "resource_id": id, "revoked_by": actorID,
			}).Error("a grant was revoked and its audit row could not be written; the revoke stands")
		}
	}
	logrus.WithFields(logrus.Fields{
		"company_id":    companyID,
		"user_id":       userID,
		"resource_kind": kind,
		"resource_id":   id,
		"revoked_by":    actorID,
	}).Info("Resource access revoked")
	return nil
}

// held reports whether userID held a grant on the resource before a change, so
// a change that changes nothing writes no row and an undo takes back only what
// was given. Read only when there is an audit log: without one this service
// makes exactly the calls it made before T-Z9.
//
// A resource that is not the company's is not found here, which is what the
// write after it would have answered.
func (s *ResourceAccessService) held(ctx context.Context, companyID string, kind domain.ResourceKind, id, userID string) (bool, error) {
	if s.audit == nil {
		return false, nil
	}
	view, err := s.repo.View(ctx, companyID, kind, id)
	if err != nil {
		return false, err
	}
	for _, g := range view.Grants {
		if strings.EqualFold(g.UserID, strings.TrimSpace(userID)) {
			return true, nil
		}
	}
	return false, nil
}

// recordAccessChange writes one change to who may reach what (decision 12). The
// admin who made it is the actor and the person it was made for is in the
// arguments, so the row names both. kind is empty for a capability, which has no
// object.
func recordAccessChange(
	ctx context.Context, audit AccessAudit, companyID, actorID, tool string,
	kind domain.ResourceKind, resourceID string, fields map[string]any,
) error {
	if kind != "" {
		fields["resource_kind"] = kind
		fields["resource_id"] = resourceID
	}
	args, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	row := &domain.AgentAction{
		CompanyID:    companyID,
		ActorKind:    domain.ActorKindUser,
		ActorRef:     actorID,
		Channel:      domain.ChannelDashboard,
		ToolName:     tool,
		ArgsRedacted: args,
		ArgsHash:     sha256Hex(tool + "|" + string(args)),
		ResultStatus: domain.ActionStatusOK,
		RequestID:    tenantctx.RequestID(ctx),
	}
	if kind == domain.ResourceKindAgent {
		row.AgentID = resourceID
	}
	// Detached, with its own deadline, for auditDecision's reason: an admin who
	// closes the tab after pressing Grant must not be how the record is lost —
	// and here, how the grant is undone.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return audit.Create(writeCtx, row)
}
