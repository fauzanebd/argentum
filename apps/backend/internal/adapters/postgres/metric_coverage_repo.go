package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// MetricCoverageRepo answers "are this tenant's answers standing on defined
// metrics or on SQL the model wrote for the occasion" (T-F4).
//
// **It stores nothing and needs no migration.** `agent_actions` has carried
// `tool_name`, `message_id` and `result_status` since 023, so coverage is a
// GROUP BY — which means it cannot drift from the truth, and it is retroactive
// to every turn this deployment has ever run. A counter incremented at write
// time would have been wrong for all of them.
type MetricCoverageRepo struct{ db *sql.DB }

func NewMetricCoverageRepo(db *sql.DB) *MetricCoverageRepo { return &MetricCoverageRepo{db: db} }

// coverageTurns classifies each turn once. Shared by both queries below so the
// definition of "ad hoc" cannot differ between the count and the list — which
// is the way a number and the list explaining it usually come apart.
//
// `result_status = 'ok'` throughout: a blocked or errored query_metric call did
// not put a certified number in front of anybody, and counting it would let a
// tenant whose metrics are all broken read as fully covered.
const coverageTurns = `
	SELECT message_id,
	       count(*) FILTER (WHERE tool_name = 'query_metric' AND result_status = 'ok') AS certified,
	       count(*) FILTER (WHERE tool_name = 'run_sql'      AND result_status = 'ok') AS ad_hoc
	  FROM agent_actions
	 WHERE company_id = $1
	   AND created_at >= $2
	   AND message_id IS NOT NULL
	 GROUP BY message_id`

func (r *MetricCoverageRepo) Coverage(ctx context.Context, companyID string, since time.Time) (domain.MetricCoverage, error) {
	q := `WITH turns AS (` + coverageTurns + `)
		SELECT
		  count(*) FILTER (WHERE certified > 0 AND ad_hoc = 0),
		  count(*) FILTER (WHERE certified = 0 AND ad_hoc > 0),
		  count(*) FILTER (WHERE certified > 0 AND ad_hoc > 0),
		  count(*) FILTER (WHERE certified = 0 AND ad_hoc = 0)
		FROM turns`
	out := domain.MetricCoverage{From: since}
	err := r.db.QueryRowContext(ctx, q, companyID, since).
		Scan(&out.Certified, &out.AdHoc, &out.Mixed, &out.NoData)
	if err != nil {
		return domain.MetricCoverage{}, err
	}
	return out, nil
}

// TopAdHocQuestions names what to define next.
//
// The question is resolved with the same LATERAL the feedback list uses
// (message_feedback_repo.go) and for the same reason: **nothing in this schema
// links an answer back to what was asked.** The nearest preceding user message
// in the same thread is the answer, and it is an approximation — a thread where
// two questions were asked before either was answered will attribute the wrong
// one. That is tolerable here in a way it would not be in an audit: this is a
// prompt for an admin deciding what to define, not a record of anything.
//
// Grouping is on the trimmed, lower-cased question. Not on an embedding: this
// is the panel that says "you asked this eleven times", and a grouping a human
// cannot verify by looking at the list underneath it is worse than a strict one
// that under-counts.
func (r *MetricCoverageRepo) TopAdHocQuestions(ctx context.Context, companyID string, since time.Time, limit int) ([]domain.AdHocQuestion, error) {
	if limit <= 0 {
		limit = 10
	}
	q := `WITH turns AS (` + coverageTurns + `),
		ad_hoc AS (
		    SELECT message_id FROM turns WHERE certified = 0 AND ad_hoc > 0
		),
		asked AS (
		    SELECT a.id AS message_id, q.content AS question
		      FROM ad_hoc h
		      JOIN messages a ON a.id = h.message_id
		      LEFT JOIN LATERAL (
		          SELECT u.content FROM messages u
		           WHERE u.thread_id = a.thread_id
		             AND u.role = 'user'
		             AND u.created_at <= a.created_at
		           ORDER BY u.created_at DESC, u.id DESC
		           LIMIT 1
		      ) q ON TRUE
		)
		SELECT LEFT(min(question), $4) AS question, count(*) AS turns
		  FROM asked
		 WHERE question IS NOT NULL AND btrim(question) <> ''
		 GROUP BY lower(btrim(question))
		 ORDER BY turns DESC, question
		 LIMIT $3`
	rows, err := r.db.QueryContext(ctx, q, companyID, since, limit, adHocQuestionChars)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := []domain.AdHocQuestion{}
	for rows.Next() {
		var a domain.AdHocQuestion
		if err := rows.Scan(&a.Question, &a.Turns); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// adHocQuestionChars trims a question to something a list row can hold. Cut in
// SQL rather than in Go, the rule message_feedback_repo.go set: a question can
// be long and a panel showing 200 characters of it has no reason to move the
// rest across the wire.
const adHocQuestionChars = 200
