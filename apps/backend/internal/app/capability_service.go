package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
)

// capabilityCacheTTL bounds how long a capability read may be answered from
// memory without asking the database again. See CapabilityService for what it
// does and does not promise.
const capabilityCacheTTL = 10 * time.Second

// errCapabilitiesUnconfigured is what a nil service answers. A route gated on a
// capability in a wiring that never built this service has to refuse, and
// RequireCapability turns an error into a refusal rather than a pass.
var errCapabilitiesUnconfigured = errors.New("capabilities are not configured")

// CapabilityService answers "was this person granted this" (T-Z1), and is the
// only way a grant is made or taken back.
//
// Capabilities are read per request behind a short cache rather than carried
// as a JWT claim, and the reason is a number: an access token lives fifteen
// minutes (cmd/api/bootstrap.go), and a revoke that waits fifteen minutes for a
// token to expire is not a revoke.
//
// The cache is this process's memory, and a grant or revoke clears the entry it
// touched — so on the replica that served the write, the very next request sees
// it. This deployment runs one API replica, which makes that the whole story
// today. With more than one, a replica that did not serve the write may answer
// from memory for up to capabilityCacheTTL, and that bound is the claim rather
// than "immediately". Redis would close the gap at the price of a network hop
// on every gated request, for a boundary the roadmap describes as one against
// accident rather than against a determined admin (decision 4); ten seconds is
// not worth that hop yet, and the day there are replicas is the day to revisit.
type CapabilityService struct {
	repo domain.CapabilityRepository
	// audit is where a grant and a revoke are written down (T-Z9), by
	// ResourceAccessService's rule: a grant that cannot be recorded is undone,
	// a revoke that cannot be recorded stands. Nil writes nothing.
	audit AccessAudit
	ttl   time.Duration
	now   func() time.Time

	mu sync.Mutex
	// cache holds one entry per user who has reached a gated route. Nothing
	// sweeps it: an expired entry is overwritten on that user's next gated
	// request, and the map is bounded by the deployment's headcount.
	cache map[string]capabilityEntry
	// gen moves on every grant and revoke. A read notes it before going to the
	// database and stores what it loaded only if it has not moved since. That
	// is what stops a load that raced a revoke from caching the grant the
	// revoke just deleted — the one interleaving in which "the next request
	// sees it" would be false even on a single replica.
	gen uint64
}

type capabilityEntry struct {
	held    map[domain.Capability]bool
	expires time.Time
}

// NewCapabilityService wires the repository and the audit log. A nil audit writes
// nothing and reads nothing extra, as NewResourceAccessService's does.
func NewCapabilityService(repo domain.CapabilityRepository, audit AccessAudit) *CapabilityService {
	return &CapabilityService{
		repo:  repo,
		audit: audit,
		ttl:   capabilityCacheTTL,
		now:   time.Now,
		cache: map[string]capabilityEntry{},
	}
}

// Has reports whether userID holds c in companyID.
//
// Nothing here knows a role. An admin is asked exactly the question a member
// is, because a rank is not a grant (roadmap 12, decision 4) — and that is
// true here by construction, since there is no role parameter to consult.
//
// A user who is not in the company holds nothing, rather than being an error.
// The caller is deciding whether to let a request through, and "this person is
// not here" has the same answer as "this person was not granted it".
func (s *CapabilityService) Has(ctx context.Context, companyID, userID string, c domain.Capability) (bool, error) {
	if s == nil {
		return false, errCapabilitiesUnconfigured
	}
	if companyID == "" || userID == "" {
		return false, nil
	}
	key := capabilityKey(companyID, userID)

	s.mu.Lock()
	entry, cached := s.cache[key]
	gen := s.gen
	s.mu.Unlock()
	if cached && s.now().Before(entry.expires) {
		return entry.held[c], nil
	}

	grants, err := s.repo.ListForUser(ctx, companyID, userID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return false, fmt.Errorf("load capabilities: %w", err)
	}
	held := make(map[domain.Capability]bool, len(grants))
	for _, g := range grants {
		held[g.Capability] = true
	}

	s.mu.Lock()
	if s.gen == gen {
		s.cache[key] = capabilityEntry{held: held, expires: s.now().Add(s.ttl)}
	}
	s.mu.Unlock()
	return held[c], nil
}

// ListForUser returns userID's grants, or ErrNotFound when they are not a user
// of companyID. Uncached: this is the settings page asking, and a page that
// just toggled something must not be shown the state from before the toggle.
func (s *CapabilityService) ListForUser(ctx context.Context, companyID, userID string) ([]domain.CapabilityGrant, error) {
	if s == nil {
		return nil, errCapabilitiesUnconfigured
	}
	return s.repo.ListForUser(ctx, companyID, userID)
}

// Grant gives userID the capability named by raw, on grantedBy's authority.
// Idempotent. raw is parsed here rather than in the handler, so no caller of
// this service can reach the repository with a string outside the vocabulary.
//
// An admin may grant themselves. That is decision 4 working as written rather
// than a gap in it: this is a boundary against accident and casual browsing,
// and what makes that honest is that somebody can see it happened — granted_by
// now, and T-Z9's audit row.
func (s *CapabilityService) Grant(ctx context.Context, companyID, grantedBy, userID, raw string) error {
	if s == nil {
		return errCapabilitiesUnconfigured
	}
	c, err := domain.ParseCapability(raw)
	if err != nil {
		return err
	}
	held, err := s.holds(ctx, companyID, userID, c)
	if err != nil {
		return err
	}
	err = s.repo.Grant(ctx, companyID, userID, c, grantedBy)
	// Forgotten whether or not the write reported success. An error after the
	// commit — a connection that dropped on the way back — leaves the database
	// changed and the cache not, and the safe side of that is a re-read.
	s.forget(companyID, userID)
	if err != nil {
		return err
	}
	if s.audit != nil && !held {
		err := recordAccessChange(ctx, s.audit, companyID, grantedBy, CapabilityGrantAudit, "", "",
			map[string]any{"user_id": userID, "capability": c})
		if err != nil {
			// Undone, by ResourceAccessService.Grant's rule and its reason.
			rerr := s.repo.Revoke(context.WithoutCancel(ctx), companyID, userID, c)
			s.forget(companyID, userID)
			if rerr != nil {
				logrus.WithError(rerr).WithFields(logrus.Fields{
					"company_id": companyID, "user_id": userID, "capability": c,
				}).Error("a capability could not be audited or taken back; revoke it by hand")
			}
			return fmt.Errorf("record the grant, so nothing was granted: %w", err)
		}
	}
	logrus.WithFields(logrus.Fields{
		"company_id": companyID,
		"user_id":    userID,
		"capability": c,
		"granted_by": grantedBy,
	}).Info("Capability granted")
	return nil
}

// Revoke takes the capability named by raw away from userID. Idempotent, and
// effective on this replica's next request — see CapabilityService.
func (s *CapabilityService) Revoke(ctx context.Context, companyID, revokedBy, userID, raw string) error {
	if s == nil {
		return errCapabilitiesUnconfigured
	}
	c, err := domain.ParseCapability(raw)
	if err != nil {
		return err
	}
	held, err := s.holds(ctx, companyID, userID, c)
	if err != nil {
		return err
	}
	err = s.repo.Revoke(ctx, companyID, userID, c)
	s.forget(companyID, userID)
	if err != nil {
		return err
	}
	if s.audit != nil && held {
		err := recordAccessChange(ctx, s.audit, companyID, revokedBy, CapabilityRevokeAudit, "", "",
			map[string]any{"user_id": userID, "capability": c})
		if err != nil {
			// Stands, by ResourceAccessService.Revoke's rule.
			logrus.WithError(err).WithFields(logrus.Fields{
				"company_id": companyID, "user_id": userID, "capability": c, "revoked_by": revokedBy,
			}).Error("a capability was revoked and its audit row could not be written; the revoke stands")
		}
	}
	logrus.WithFields(logrus.Fields{
		"company_id": companyID,
		"user_id":    userID,
		"capability": c,
		"revoked_by": revokedBy,
	}).Info("Capability revoked")
	return nil
}

// holds reports whether userID held c before a change — uncached, because the
// cache may be ten seconds behind the row a change is about. Read only when there
// is an audit log, so without one Grant and Revoke make exactly the calls they
// made before T-Z9. A user who is not the company's is not found here, which is
// what the write after it would have answered.
func (s *CapabilityService) holds(ctx context.Context, companyID, userID string, c domain.Capability) (bool, error) {
	if s.audit == nil {
		return false, nil
	}
	grants, err := s.repo.ListForUser(ctx, companyID, userID)
	if err != nil {
		return false, err
	}
	for _, g := range grants {
		if g.Capability == c {
			return true, nil
		}
	}
	return false, nil
}

func (s *CapabilityService) forget(companyID, userID string) {
	s.mu.Lock()
	delete(s.cache, capabilityKey(companyID, userID))
	s.gen++
	s.mu.Unlock()
}

// capabilityKey carries the company as well as the user. A user id is unique
// on its own, so this is not about collisions: it is so that nothing reading
// this map can answer a question for one company out of an entry another
// company's request put there.
func capabilityKey(companyID, userID string) string { return companyID + "/" + userID }
