package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/fauzanebd/argentum/internal/domain"
)

// CookbookCandidateRepo mines finished turns for queries worth learning from
// (T-Q8).
//
// It is a read over `agent_actions` joined to `messages`, which is where this
// history has been accumulating since T-05 shipped — the SQL in
// `args_redacted`, the outcome in `result_status`, the row count in
// `rows_returned`, and the question one join away through `message_id`. None
// of it was ever read back.
//
// Its own type rather than a method on AgentActionRepo, because the audit log
// is a compliance surface: a repository whose job is "what did the agent do"
// should not also grow the ability to decide what the agent learns.
type CookbookCandidateRepo struct{ db *sql.DB }

func NewCookbookCandidateRepo(db *sql.DB) *CookbookCandidateRepo {
	return &CookbookCandidateRepo{db: db}
}

// Candidates returns turns whose SQL is a plausible example, newest first.
//
// The WHERE clause is the whole design, and every line of it is a way an
// example can poison the cookbook rather than improve it:
//
//   - `tool_name = 'run_sql'` — the only tool whose argument is a query.
//   - `result_status = 'ok'` — an errored query teaches the error.
//   - `rows_returned > 0` — a query that matched nothing is the single most
//     dangerous example available. Mechanism E-5 is exactly this: a
//     well-formed query, zero rows, and an invented figure. Teaching the agent
//     to imitate one would be building a machine for reproducing it.
//   - `actor_kind = 'user'` — a schedule or a watcher runs a prompt somebody
//     wrote once and nobody has read since. A person asking a question in a
//     conversation is the evidence that the question is real.
//   - the question is a non-empty user message, and the SQL is long enough to
//     be a query rather than a probe.
//
// What it deliberately does NOT filter on is feedback. That join belongs in
// the service, because the rule is "no example a human called wrong" and that
// is a statement about message_feedback (T-Q2) which this query would have to
// duplicate.
func (r *CookbookCandidateRepo) Candidates(
	ctx context.Context, companyID string, since time.Time, limit int,
) ([]domain.CookbookCandidate, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	// The LATERAL is the verdict gate's other half. agent_actions.message_id is
	// the user's question; message_feedback is only ever keyed by an assistant
	// message, because FeedbackService.Rate refuses to rate anything else.
	// Without resolving the reply here, the service would ask "did anyone mark
	// this wrong?" about an id that table can never contain, and the answer
	// would always be no. See domain.CookbookCandidate.AnswerMessageID.
	//
	// First assistant message at or after the question, in the same thread:
	// ChatRunner writes the user row before it runs the turn and the assistant
	// row when it completes, so that is the reply. LEFT JOIN rather than inner,
	// because a turn that never produced a reply is still a candidate — it just
	// has no verdict to check.
	const q = `
		SELECT a.message_id::text,
		       COALESCE(ans.id::text, ''),
		       a.source_id,
		       COALESCE(a.rows_returned, 0),
		       COALESCE(a.args_redacted->>'query', a.args_redacted->>'sql', ''),
		       m.content,
		       a.created_at
		FROM agent_actions a
		JOIN messages m ON m.id = a.message_id
		LEFT JOIN LATERAL (
		    SELECT r.id FROM messages r
		    WHERE r.thread_id = m.thread_id
		      AND r.role = 'assistant'
		      AND r.created_at >= m.created_at
		    ORDER BY r.created_at ASC, r.id ASC
		    LIMIT 1
		) ans ON TRUE
		WHERE a.company_id = $1
		  AND a.tool_name = 'run_sql'
		  AND a.result_status = 'ok'
		  AND a.rows_returned > 0
		  AND a.actor_kind = 'user'
		  AND a.source_id <> ''
		  AND a.created_at >= $2
		  AND m.role = 'user'
		  AND length(trim(m.content)) > 0
		  AND length(COALESCE(a.args_redacted->>'query', a.args_redacted->>'sql', '')) > 20
		ORDER BY a.created_at DESC
		LIMIT $3`

	rows, err := r.db.QueryContext(ctx, q, companyID, since, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []domain.CookbookCandidate
	for rows.Next() {
		var c domain.CookbookCandidate
		if err := rows.Scan(&c.MessageID, &c.AnswerMessageID, &c.SourceID, &c.RowCount,
			&c.SQL, &c.Question, &c.RanAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CompaniesWithActivity lists the tenants worth harvesting since a time.
//
// It exists so the scheduled harvest does not walk every company on the
// deployment to find the three that asked a question yesterday.
func (r *CookbookCandidateRepo) CompaniesWithActivity(ctx context.Context, since time.Time) ([]string, error) {
	const q = `
		SELECT DISTINCT company_id::text FROM agent_actions
		WHERE tool_name = 'run_sql' AND result_status = 'ok' AND created_at >= $1`
	rows, err := r.db.QueryContext(ctx, q, since)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
