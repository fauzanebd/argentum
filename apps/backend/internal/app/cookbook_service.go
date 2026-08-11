package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/embedding"
)

// CookbookService turns a tenant's own answered questions into examples the
// agent is shown before it writes a query (T-Q8).
//
// **What makes this cheap.** It collects no new data. `agent_actions` has
// recorded the SQL of every `run_sql` call since T-05, `messages` holds the
// question, and `message_feedback` (T-Q2) holds whether anyone thought the
// answer was right. This service reads those three, embeds the questions, and
// writes what survives.
//
// **What makes it dangerous, and how that is handled.** An example is a thing
// the agent will imitate, so a wrong one is not neutral — it is a wrong answer
// with a precedent behind it. Four gates stand between a turn and the
// cookbook, and they are in two places on purpose:
//
//   - The SQL-level ones are in the query (CookbookCandidateRepo.Candidates):
//     the query ran, it returned rows, a person asked for it. The zero-rows
//     filter is the important one — mechanism E-5 is a well-formed query that
//     matched nothing followed by an invented figure, and an example of that
//     shape would be a machine for reproducing it.
//   - The verdict gate is here, because it is a statement about a different
//     table: an answer somebody marked wrong never becomes an example, even if
//     the query behind it ran perfectly. `result_status = ok` means the query
//     executed, never that it answered.
type CookbookService struct {
	examples   domain.QueryExampleRepository
	candidates domain.CookbookCandidateSource
	feedback   domain.MessageFeedbackRepository
	embed      EmbeddingResolver
}

// EmbeddingResolver is the half of llmtenant.EmbeddingCache this service
// needs: the tenant's embedding client, or nil when they have no credentials.
//
// Declared at the consumer, like ChatRunner's LLMResolver and for the same
// stated reason — the concrete cache resolves per-tenant credentials out of
// the control database, so a service that took it could not be exercised in a
// test at all. And what needs exercising here is precisely the gate order: the
// four filters that stand between a turn and the cookbook are the whole
// design, and a gate nobody can test is a gate that stops working quietly.
type EmbeddingResolver interface {
	For(ctx context.Context, companyID string) (embedding.Client, error)
}

func NewCookbookService(
	examples domain.QueryExampleRepository,
	candidates domain.CookbookCandidateSource,
	feedback domain.MessageFeedbackRepository,
	embed EmbeddingResolver,
) *CookbookService {
	return &CookbookService{examples: examples, candidates: candidates, feedback: feedback, embed: embed}
}

// configured reports whether this deployment can harvest at all.
//
// The embedder is checked here rather than lazily inside the loop because a
// harvest that cannot embed cannot learn anything, and reading candidates
// first would spend a query to discover it.
func (s *CookbookService) configured() error {
	if s.examples == nil || s.candidates == nil || s.feedback == nil || s.embed == nil {
		return fmt.Errorf("cookbook is not configured on this deployment")
	}
	return nil
}

// HarvestResult is what one run did, for the log and the admin route.
type HarvestResult struct {
	CompanyID string `json:"company_id"`
	Examined  int    `json:"examined"`
	Learned   int    `json:"learned"`
	// SkippedKnown is turns already in the cookbook. Expected to be most of
	// them on any run after the first, and the reason a re-run is cheap.
	SkippedKnown int `json:"skipped_known"`
	// SkippedNegative is turns a human marked wrong. The number worth watching:
	// if it climbs, the agent is getting worse and this is the first place it
	// shows.
	SkippedNegative int `json:"skipped_negative"`
	SkippedTooLong  int `json:"skipped_too_long"`
	Failed          int `json:"failed"`
}

// HarvestWindow is how far back a run looks when the caller names no window.
// Seven days rather than everything, because a re-harvest is meant to be a
// cheap incremental job — `ExistingOrigins` already stops re-learning, so the
// window is about how much is read, not about what is written.
const HarvestWindow = 7 * 24 * time.Hour

// Harvest mines one company's recent turns and learns what survives the gates.
//
// Errors on individual candidates are counted and skipped rather than
// returned: one question whose embedding call failed must not cost the other
// forty their place in the cookbook.
func (s *CookbookService) Harvest(ctx context.Context, companyID string, since time.Time, limit int) (HarvestResult, error) {
	out := HarvestResult{CompanyID: companyID}
	if err := s.configured(); err != nil {
		return out, err
	}
	if since.IsZero() {
		since = time.Now().Add(-HarvestWindow)
	}

	cands, err := s.candidates.Candidates(ctx, companyID, since, limit)
	if err != nil {
		return out, fmt.Errorf("read candidates: %w", err)
	}
	out.Examined = len(cands)
	if len(cands) == 0 {
		return out, nil
	}

	// Both gates as batch reads before the loop, so a harvest of forty
	// candidates costs two queries rather than eighty.
	ids := make([]string, 0, len(cands))
	// The verdict gate reads a DIFFERENT set of ids from the origin gate, and
	// conflating the two is a defect this ticket shipped with: `ids` below is
	// the questions, because that is what query_examples.origin_message_id
	// holds; `verdictIDs` is the questions AND the answers, because a verdict is
	// only ever filed against an assistant message. Asking message_feedback
	// about a question id is asking a table of answers about something it
	// cannot contain, and it returns "nobody complained" every time.
	verdictIDs := make([]string, 0, len(cands)*2)
	for _, c := range cands {
		ids = append(ids, c.MessageID)
		verdictIDs = append(verdictIDs, c.VerdictKeys()...)
	}
	known, err := s.examples.ExistingOrigins(ctx, companyID, ids)
	if err != nil {
		return out, fmt.Errorf("read existing origins: %w", err)
	}
	// A failure here is fatal to the run rather than skipped. Proceeding would
	// mean learning from turns whose verdicts we could not read, and "we could
	// not check" must never resolve to "assume it was fine" for the one gate
	// that exists to keep wrong answers out.
	negative, err := s.feedback.NegativeMessageIDs(ctx, companyID, verdictIDs)
	if err != nil {
		return out, fmt.Errorf("read feedback verdicts: %w", err)
	}

	client, err := s.embed.For(ctx, companyID)
	if err != nil {
		return out, fmt.Errorf("resolve embedding client: %w", err)
	}
	if client == nil {
		return out, fmt.Errorf("no embedding credentials for this tenant")
	}

	for _, c := range cands {
		switch {
		case known[c.MessageID]:
			out.SkippedKnown++
			continue
		case isNegative(negative, c):
			out.SkippedNegative++
			logrus.WithFields(logrus.Fields{
				"company_id": companyID,
				"message_id": c.MessageID,
			}).Info("cookbook: not learning from a turn somebody marked wrong")
			continue
		case len(c.SQL) > domain.QueryExampleMaxSQLChars:
			// Not truncated. Half a query is a syntax error to imitate, and an
			// example the agent cannot run is worse than no example.
			out.SkippedTooLong++
			continue
		}

		vecs, err := client.Embed(ctx, []string{c.Question})
		if err != nil || len(vecs) == 0 {
			out.Failed++
			logrus.WithError(err).WithField("message_id", c.MessageID).
				Warn("cookbook: embedding the question failed; skipping this example")
			continue
		}

		e := &domain.QueryExample{
			CompanyID:       companyID,
			SourceID:        c.SourceID,
			Question:        strings.TrimSpace(c.Question),
			SQL:             strings.TrimSpace(c.SQL),
			RowCount:        c.RowCount,
			OriginMessageID: c.MessageID,
			Embedding:       vecs[0],
			Model:           client.Model(),
		}
		if err := s.examples.Upsert(ctx, e); err != nil {
			out.Failed++
			logrus.WithError(err).WithField("message_id", c.MessageID).
				Warn("cookbook: writing the example failed")
			continue
		}
		out.Learned++
	}

	logrus.WithFields(logrus.Fields{
		"company_id":       companyID,
		"examined":         out.Examined,
		"learned":          out.Learned,
		"skipped_known":    out.SkippedKnown,
		"skipped_negative": out.SkippedNegative,
		"skipped_too_long": out.SkippedTooLong,
		"failed":           out.Failed,
	}).Info("cookbook harvest complete")
	return out, nil
}

// isNegative reports whether anyone marked this turn wrong, on either of the
// ids a verdict could name.
//
// A free function rather than a method on the candidate because the map is the
// harvester's, not the domain's — and it is a function rather than an inline
// loop so the gate has one name to grep for.
func isNegative(negative map[string]bool, c domain.CookbookCandidate) bool {
	for _, id := range c.VerdictKeys() {
		if negative[id] {
			return true
		}
	}
	return false
}

// HarvestAll runs the harvest for every company with recent activity. This is
// what the scheduled job calls.
//
// One company's failure does not stop the others: a tenant with no embedding
// credentials is a permanent, expected error, and it must not stop the
// deployment's other tenants from learning.
func (s *CookbookService) HarvestAll(ctx context.Context, since time.Time, limit int) []HarvestResult {
	if s.candidates == nil {
		return nil
	}
	if since.IsZero() {
		since = time.Now().Add(-HarvestWindow)
	}
	companies, err := s.candidates.CompaniesWithActivity(ctx, since)
	if err != nil {
		logrus.WithError(err).Warn("cookbook: listing active companies failed")
		return nil
	}
	out := make([]HarvestResult, 0, len(companies))
	for _, companyID := range companies {
		res, err := s.Harvest(ctx, companyID, since, limit)
		if err != nil {
			logrus.WithError(err).WithField("company_id", companyID).
				Warn("cookbook: harvest failed for this tenant; continuing with the rest")
			continue
		}
		out = append(out, res)
	}
	return out
}

// Forget empties a tenant's cookbook. The escape hatch for a schema that
// changed underneath it, where every example is now wrong.
func (s *CookbookService) Forget(ctx context.Context, companyID string) (int, error) {
	if s.examples == nil {
		return 0, fmt.Errorf("cookbook is not configured on this deployment")
	}
	n, err := s.examples.DeleteByCompany(ctx, companyID)
	if err == nil {
		logrus.WithFields(logrus.Fields{"company_id": companyID, "deleted": n}).
			Info("cookbook emptied")
	}
	return n, err
}

// Count is how many examples a tenant has.
func (s *CookbookService) Count(ctx context.Context, companyID string) (int, error) {
	if s.examples == nil {
		return 0, nil
	}
	return s.examples.CountByCompany(ctx, companyID)
}
