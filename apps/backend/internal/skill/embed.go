package skill

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
)

// The vectors T-K5 ranks with, and the two paths that produce them.
//
// **Write time is the primary path and the background one is the repair.** An
// admin saving a procedure is already waiting on a round trip, so embedding it
// there costs nothing anybody notices and means a skill is rankable the moment
// it exists. The backfill exists for the two states that path cannot reach:
// every skill written before migration `072`, and every skill a tenant wrote
// while their embedding credentials were missing or broken.
//
// **Neither path may fail a save or a turn.** A vector decides which of a
// tenant's procedures survive truncation; it is not what makes a procedure
// exist. So every error here is a log line and a nil vector, and the ranker
// degrades to `lower(name)` — which is T-K3's shipped behaviour and is exactly
// what a tenant under the bound gets anyway.

// Client is the two methods this package needs from an embedding client.
//
// **Declared here rather than imported from `internal/embedding`, and the
// reason is a compile error rather than taste.** `internal/config` imports this
// package for T-K3's two default bounds, and `internal/embedding` imports
// `internal/config`; taking the concrete client type would close that loop. A
// narrow local interface is the right shape anyway — this package uses two of
// its three methods — and it is what lets a test hand the embedder a stub with
// no provider behind it.
type Client interface {
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
	Model() string
}

// ClientFor resolves a tenant's embedding client. `llmtenant.EmbeddingCache.For`
// is what satisfies it in the wiring, through a two-line adapter that exists to
// keep the cycle above open.
//
// **A nil client with a nil error is the ordinary "this tenant has no embedding
// setup" answer**, which the cache already returns and every existing caller
// already branches on — so the adapter has to return a genuinely nil Client for
// it rather than an interface value wrapping a nil one.
type ClientFor func(ctx context.Context, companyID string) (Client, error)

// backfillCooldown is how long a process waits before working the same
// company's unembedded skills again.
//
// It exists because the trigger is a turn. A tenant over the index bound whose
// embedding credentials are wrong would otherwise fire one failing API call per
// question asked, forever, and the log would say the same thing every time. Ten
// minutes is long enough that a broken tenant costs six attempts an hour and
// short enough that a tenant who has just fixed their key does not have to
// wonder whether anything is happening.
const backfillCooldown = 10 * time.Minute

// Embedder writes and repairs the vectors on `skills`.
type Embedder struct {
	repo      domain.SkillRepository
	clientFor ClientFor

	mu     sync.Mutex
	recent map[string]time.Time
}

// NewEmbedder wires the embedder. A nil repo or a nil resolver returns nil, and
// every method on a nil Embedder is a no-op — so a deployment with no embedding
// credentials wires nothing and calls it unconditionally.
func NewEmbedder(repo domain.SkillRepository, clientFor ClientFor) *Embedder {
	if repo == nil || clientFor == nil {
		return nil
	}
	return &Embedder{repo: repo, clientFor: clientFor, recent: map[string]time.Time{}}
}

// EmbedOne stores the vector for one freshly written skill, best-effort.
//
// Called from the save. It returns no error on purpose: the caller has already
// written the row the tenant cares about, and the only thing this can add is a
// ranking that matters to companies over the index bound. Reporting a failed
// embedding call as a failed save would be answering a form with the wrong
// question.
func (e *Embedder) EmbedOne(ctx context.Context, companyID string, s *domain.Skill) {
	if e == nil || s == nil || s.IsBuiltin() {
		return
	}
	client, err := e.client(ctx, companyID)
	if client == nil || err != nil {
		return
	}
	vecs, err := client.Embed(ctx, []string{s.EmbedText()})
	if err != nil || len(vecs) == 0 {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": companyID, "skill_id": s.ID,
		}).Warn("skill embedding: failed; this skill ranks after the embedded ones until a backfill picks it up")
		return
	}
	if err := e.repo.SetEmbedding(ctx, companyID, s, vecs[0], client.Model()); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": companyID, "skill_id": s.ID,
		}).Warn("skill embedding: storing it failed")
		return
	}
	s.Embedding, s.EmbeddingModel = vecs[0], client.Model()
}

// Backfill embeds every enabled skill of one company that has no vector, and
// returns how many it stored.
//
// **One batch call, not one per skill.** What is embedded is an index line, so
// two hundred of them — T-K1's per-company cap — is a few thousand tokens; the
// client chunks internally if its provider wants smaller requests.
//
// A row whose text moved between the read and the write is skipped rather than
// retried, because `SetEmbedding`'s conditional write is what makes a stale
// vector unrepresentable and the next backfill will find it again.
func (e *Embedder) Backfill(ctx context.Context, companyID string) (int, error) {
	if e == nil {
		return 0, nil
	}
	pending, err := e.repo.ListUnembedded(ctx, companyID)
	if err != nil || len(pending) == 0 {
		return 0, err
	}
	client, err := e.client(ctx, companyID)
	if client == nil || err != nil {
		return 0, err
	}

	texts := make([]string, 0, len(pending))
	for _, s := range pending {
		texts = append(texts, s.EmbedText())
	}
	vecs, err := client.Embed(ctx, texts)
	if err != nil {
		return 0, err
	}
	if len(vecs) != len(pending) {
		// A provider that returns a different number of vectors than it was
		// given inputs has broken the only thing that pairs them — position —
		// so nothing here is safe to store. Refused rather than truncated, for
		// domain.Skill.Validate's reason.
		logrus.WithFields(logrus.Fields{
			"company_id": companyID, "sent": len(pending), "returned": len(vecs),
		}).Warn("skill backfill: the provider returned a different number of vectors than it was sent; storing none")
		return 0, nil
	}

	stored := 0
	for i, s := range pending {
		if err := e.repo.SetEmbedding(ctx, companyID, s, vecs[i], client.Model()); err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"company_id": companyID, "skill_id": s.ID,
			}).Debug("skill backfill: skipped one row")
			continue
		}
		stored++
	}
	logrus.WithFields(logrus.Fields{
		"company_id": companyID, "stored": stored, "pending": len(pending), "model": client.Model(),
	}).Info("skill backfill: vectors stored; the index ranks by relevance from the next turn")
	return stored, nil
}

// BackfillSoon runs Backfill detached from the caller, at most once per
// cooldown per company.
//
// **Detached because the caller is a turn.** The trigger is an index that had
// to drop something, which is discovered while composing a system prompt; an
// embedding round trip there would put a network call on the critical path of
// every question a tenant over the bound asks, to improve a ranking that
// applies from the *next* turn either way.
func (e *Embedder) BackfillSoon(ctx context.Context, companyID string) {
	if e == nil || !e.claim(companyID) {
		return
	}
	go func() {
		bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
		defer cancel()
		if _, err := e.Backfill(bgCtx, companyID); err != nil {
			logrus.WithError(err).WithField("company_id", companyID).
				Warn("skill backfill: failed; the index keeps ranking alphabetically")
		}
	}()
}

// claim reports whether this company is due, and marks it attempted if so.
func (e *Embedder) claim(companyID string) bool {
	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	if last, ok := e.recent[companyID]; ok && now.Sub(last) < backfillCooldown {
		return false
	}
	e.recent[companyID] = now
	return true
}

func (e *Embedder) client(ctx context.Context, companyID string) (Client, error) {
	client, err := e.clientFor(ctx, companyID)
	if err != nil {
		logrus.WithError(err).WithField("company_id", companyID).
			Warn("skill embedding: resolving the tenant's embedding client failed")
		return nil, err
	}
	return client, nil
}
