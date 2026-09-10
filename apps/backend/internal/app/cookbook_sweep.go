package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/fauzanebd/argentum/internal/adapters/db"
	"github.com/fauzanebd/argentum/internal/domain"
	"github.com/fauzanebd/argentum/internal/sqlguard"
)

// The cookbook learns to forget (T-Q15).
//
// `T-Q8` gave the agent a memory and no way to lose one. `MarkUsed` has been
// writing `uses` and `last_used_at` on every retrieval since it shipped, and
// `TopK` reads neither — migration `055`'s own comment calls them "bookkeeping
// for whoever tunes the cookbook later" and nobody became that person. So an
// example harvested in March competes with one harvested yesterday on cosine
// distance alone, forever.
//
// **Age is the smaller half. Drift is the one that produces wrong answers.**
// `source_id` cascades, so deleting a warehouse takes its examples with it —
// but renaming a table inside a live warehouse does not. The example keeps
// ranking, and a turn is handed a worked query that would now fail, on the one
// surface whose entire purpose is "imitate this". `055`'s only answer was
// `DeleteByCompany`, which its comment describes as the escape hatch for "a
// tenant whose schema changed underneath it, where every example is now wrong":
// an all-or-nothing hammer, swung by hand, for a condition that is almost never
// all-or-nothing.
//
// Two rules, and the asymmetry between them is the design:
//
//   - **Drift is a fact and archives on it.** A table the source does not have
//     is not a judgement call.
//   - **Age is a policy and only ever archives what has never been used.**
//     `uses = 0` is read as absence of evidence, not as a low score, and it is
//     never used to *rank*. A tenant who asks one question a month must not
//     have their cookbook emptied for being quiet.
//
// Nothing here deletes. Archiving is reversible by clearing one column, and
// `ExistingOrigins` still sees archived rows, so a sweep and a harvest cannot
// take turns undoing each other.

// CookbookConnResolver is the half of the tenant pool a sweep needs: a
// connection to one source, to ask what tables it has now.
//
// Declared at the consumer like MetricConnResolver and for the same reason —
// the concrete pool resolves per-tenant credentials out of the control
// database, so a service that took it could not be exercised in a test. And
// what needs exercising here is the archive decision, which is the one thing
// in this file that destroys something a tenant paid an embedding call for.
type CookbookConnResolver interface {
	For(ctx context.Context, companyID, sourceID string) (db.Conn, error)
}

// WithSweep gives the service a warehouse to check examples against (T-Q15).
//
// Additive: a wiring without it can still harvest and still archive on age.
// Only the drift half needs a connection, and a deployment that cannot open
// one should keep the cookbook it has rather than lose it.
func (s *CookbookService) WithSweep(pool CookbookConnResolver) *CookbookService {
	s.pool = pool
	return s
}

// SweepResult is what one company's sweep did.
type SweepResult struct {
	CompanyID string `json:"company_id"`
	// Examined is live examples read. Zero for a tenant with no cookbook, which
	// is the common case and costs one query.
	Examined int `json:"examined"`
	// ArchivedDrift is examples querying a table their source no longer has.
	ArchivedDrift int `json:"archived_drift"`
	// ArchivedUnused is examples old enough to have had their chance and never
	// retrieved.
	ArchivedUnused int `json:"archived_unused"`
	// SkippedUnreadable is examples whose SQL the lexer could not attribute to
	// tables. **They are kept**, and the count is here because a number that
	// climbs means the sweep is going blind rather than finding nothing.
	SkippedUnreadable int `json:"skipped_unreadable"`
	// UnreachableSources is sources that could not be introspected this run.
	// Their examples are kept: a warehouse that is down has not dropped a
	// table, and treating "we could not ask" as "it is gone" would empty a
	// tenant's cookbook during an outage.
	UnreachableSources int `json:"unreachable_sources"`
}

// SweepUnusedAfter is how long an example gets to prove itself before never
// having been retrieved counts against it.
//
// Ninety days rather than thirty: the unit here is the tenant's question, not
// the agent's turn, and a quarterly report is a real question somebody asks
// three times a year. The cost of keeping a useless example for a season is
// that it occasionally displaces a better one from a top-3; the cost of
// dropping a seasonal one is that the agent relearns it from scratch every
// quarter, if it is lucky enough to be asked twice.
const SweepUnusedAfter = 90 * 24 * time.Hour

// Sweep retires what a company's cookbook should no longer teach.
//
// One introspection per source, not per example: LiveRefs comes back ordered
// by source, so a tenant with three warehouses and four hundred examples costs
// three round trips to the warehouses and one UPDATE.
func (s *CookbookService) Sweep(ctx context.Context, companyID string, unusedAfter time.Duration) (SweepResult, error) {
	out := SweepResult{CompanyID: companyID}
	if s.examples == nil {
		return out, fmt.Errorf("cookbook is not configured on this deployment")
	}
	now := time.Now()

	// Age first, and on its own statement: it needs no warehouse, so a tenant
	// whose sources are all unreachable still gets the half of the sweep that
	// does not depend on them.
	if unusedAfter > 0 {
		n, err := s.examples.ArchiveUnused(ctx, companyID, now.Add(-unusedAfter), now)
		if err != nil {
			return out, fmt.Errorf("archive unused: %w", err)
		}
		out.ArchivedUnused = n
	}

	refs, err := s.examples.LiveRefs(ctx, companyID)
	if err != nil {
		return out, fmt.Errorf("read live examples: %w", err)
	}
	out.Examined = len(refs)
	if len(refs) == 0 || s.pool == nil {
		return out, nil
	}

	var (
		drifted   []int64
		sourceID  string
		live      map[string]bool
		reachable bool
	)
	for _, ref := range refs {
		if ref.SourceID != sourceID {
			sourceID = ref.SourceID
			live, reachable = s.sourceTables(ctx, companyID, sourceID)
			if !reachable {
				out.UnreachableSources++
			}
		}
		if !reachable {
			continue
		}
		missing, ok := missingTables(ref.SQL, live)
		if !ok {
			out.SkippedUnreadable++
			continue
		}
		if len(missing) == 0 {
			continue
		}
		logrus.WithFields(logrus.Fields{
			"company_id": companyID,
			"source_id":  sourceID,
			"example_id": ref.ID,
			"missing":    strings.Join(missing, ","),
		}).Info("cookbook: archiving an example whose tables the source no longer has")
		drifted = append(drifted, ref.ID)
	}

	if len(drifted) > 0 {
		n, err := s.examples.Archive(ctx, drifted, domain.ArchiveReasonSchemaDrift, now)
		if err != nil {
			return out, fmt.Errorf("archive drifted: %w", err)
		}
		out.ArchivedDrift = n
	}
	return out, nil
}

// sourceTables is the set of table names one source has right now, and whether
// the question could be answered at all.
//
// The second return is not an error because the caller does not want one: a
// warehouse that is down is a reason to do nothing, not a reason to fail the
// other two sources' sweep.
func (s *CookbookService) sourceTables(ctx context.Context, companyID, sourceID string) (map[string]bool, bool) {
	conn, err := s.pool.For(ctx, companyID, sourceID)
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": companyID, "source_id": sourceID,
		}).Warn("cookbook sweep: cannot reach the source; its examples are kept")
		return nil, false
	}
	schema, err := conn.ExtractSchema(ctx)
	if err != nil || schema == nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"company_id": companyID, "source_id": sourceID,
		}).Warn("cookbook sweep: cannot read the schema; its examples are kept")
		return nil, false
	}
	// An empty schema is treated as unreachable rather than as "every table was
	// dropped". A driver that returns no tables is far more likely to be a
	// permissions change than a warehouse somebody emptied, and the two are
	// indistinguishable from here — so the sweep declines to guess.
	if len(schema.Tables) == 0 {
		logrus.WithFields(logrus.Fields{
			"company_id": companyID, "source_id": sourceID,
		}).Warn("cookbook sweep: the source reported no tables at all; declining to archive on that")
		return nil, false
	}

	live := make(map[string]bool, len(schema.Tables)*2)
	for _, t := range schema.Tables {
		for _, k := range tableKeys(t.Name) {
			live[k] = true
		}
	}
	return live, true
}

// missingTables reports which of a statement's tables the source does not
// have. The second return is false when the SQL could not be read, which the
// caller must treat as "keep".
func missingTables(sqlText string, live map[string]bool) ([]string, bool) {
	refs := sqlguard.ReferencedTables(sqlText)
	// The lexer says so itself when it cannot attribute a name to a table, and
	// this is the one place in the sweep where guessing costs a tenant an
	// example they paid to learn. Uncertain means keep.
	if refs.Uncertain || len(refs.Tables) == 0 {
		return nil, false
	}
	var missing []string
	for _, t := range refs.Tables {
		found := false
		for _, k := range tableKeys(t) {
			if live[k] {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, t)
		}
	}
	return missing, true
}

// tableKeys is the names one table should be recognised by.
//
// A warehouse reports `public.fact_sales` and a query says `fact_sales`, or the
// other way round, and neither spelling is wrong. Both the full name and its
// last segment are keys, lowercased, so the two match in either direction. The
// failure this avoids is silent and one-directional: a schema-qualified live
// name that did not match an unqualified reference would archive every example
// against that warehouse in one sweep.
func tableKeys(name string) []string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.Trim(n, `"`)
	if n == "" {
		return nil
	}
	keys := []string{n}
	if i := strings.LastIndex(n, "."); i >= 0 && i < len(n)-1 {
		keys = append(keys, n[i+1:])
	}
	return keys
}

// SweepAll sweeps every tenant holding a cookbook.
//
// Per-tenant failures are logged and skipped rather than returned: one
// unreachable warehouse must not stop the other forty tenants' sweep, and the
// same argument the harvester makes applies twice as hard here, because the
// thing at stake is not a missed improvement but an archive decision nobody
// asked for.
func (s *CookbookService) SweepAll(ctx context.Context, unusedAfter time.Duration) []SweepResult {
	if s.examples == nil {
		return nil
	}
	companies, err := s.examples.CompaniesWithExamples(ctx)
	if err != nil {
		logrus.WithError(err).Warn("cookbook sweep: listing tenants with a cookbook failed")
		return nil
	}
	out := make([]SweepResult, 0, len(companies))
	for _, companyID := range companies {
		res, err := s.Sweep(ctx, companyID, unusedAfter)
		if err != nil {
			logrus.WithError(err).WithField("company_id", companyID).
				Warn("cookbook sweep: failed for this tenant; continuing with the rest")
			continue
		}
		out = append(out, res)
	}
	return out
}
