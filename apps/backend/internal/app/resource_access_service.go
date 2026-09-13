package app

import (
	"context"
	"errors"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
)

var errResourceAccessUnconfigured = errors.New("resource access is not configured")

// ResourceAccessService is an admin restricting a resource and saying who may
// reach it (T-Z2). It decides nothing about a request — internal/authz does,
// against the same repository — and that split is deliberate: the routes that
// change access are few, admin-only and slow-moving, while the decision runs on
// every picker render and every turn, and the two should not share a type that
// makes it easy to call one where the other was meant.
//
// Every method parses the kind and mode it was handed, so nothing reaches the
// repository with a string outside the vocabulary.
type ResourceAccessService struct {
	repo domain.ResourceGrantRepository
}

// NewResourceAccessService wires the repository.
func NewResourceAccessService(repo domain.ResourceGrantRepository) *ResourceAccessService {
	return &ResourceAccessService{repo: repo}
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
	change, err := s.repo.SetAccessMode(ctx, companyID, kind, id, mode)
	if err != nil {
		return domain.AccessModeChange{}, err
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
	if err := s.repo.Grant(ctx, companyID, userID, kind, id, actorID); err != nil {
		return err
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
	if err := s.repo.Revoke(ctx, companyID, userID, kind, id); err != nil {
		return err
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
