package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/lib/pq"

	"github.com/fauzanebd/argentum/internal/domain"
)

// DerivedFiguresRepo reads T-W3's measurement: what a turn's tools did, from
// `agent_actions`, beside what its reply said, from the grounding record
// `ChatRunner` stores on the assistant message.
//
// **No migration, and only half of it is retroactive.** The tool half is a
// GROUP BY over a table that has carried `tool_name`, `source_id` and
// `message_id` since 023, so it covers every turn this deployment has run. The
// reply half did not exist before this ticket: the grounding verdict went to a
// log line and a counter, neither of which joins to anything, and tool outputs
// are not stored anywhere a query could re-derive it from. So "stated a figure
// no tool returned" is answerable only for turns completed after this shipped,
// and the panel says how many answering turns it could not check rather than
// quietly reading the rest as clean.
type DerivedFiguresRepo struct{ db *sql.DB }

func NewDerivedFiguresRepo(db *sql.DB) *DerivedFiguresRepo { return &DerivedFiguresRepo{db: db} }

// TurnShapes returns how many turns had each combination of the four facts.
//
// **Grouped by message_id the way T-F4's coverage query is**, and that id is
// the *user* message — what `tenantctx.WithMessageID` puts on every audit row.
// The grounding record carries the same id as `turn`, because nothing else in
// the schema joins a reply to the tool calls behind it. A room turn that fans
// out to several agents (T-N3) is therefore one turn here, with its replies'
// records summed.
//
// `result_status` counts `truncated` as well as `ok`: a run_sql whose rows were
// trimmed to fit the context still returned rows, and the model still did
// arithmetic over them. (T-F4 counts `ok` only, which drops those turns from
// its coverage — filed, not changed here.)
//
// **Cross-source** is distinct sources among the turn's data calls:
// `search_documents` is one source, and each run_sql `source_id` another. An
// empty source_id is kept as its own key rather than dropped, because run_sql
// accepts one only when the company has a single source — so it is still a
// source, and "the warehouse plus a document" is exactly research §3d's first
// class. query_metric names no source in its arguments and contributes nothing,
// which under-counts; a metric's source is not on the audit row.
//
// The array lengths are read through jsonb_typeof so one malformed record costs
// that turn its verdict rather than raising and costing every tenant the panel.
func (r *DerivedFiguresRepo) TurnShapes(ctx context.Context, companyID string, since time.Time, dataTools []string) ([]domain.TurnShape, error) {
	const q = `
	WITH tools AS (
	    SELECT message_id::text AS turn,
	           count(*) FILTER (WHERE tool_name = 'compute') AS computes,
	           count(DISTINCT CASE
	               WHEN tool_name = 'search_documents' THEN 'documents'
	               WHEN tool_name = 'run_sql'          THEN 'source:' || source_id
	           END) AS sources
	      FROM agent_actions
	     WHERE company_id = $1
	       AND created_at >= $2
	       AND message_id IS NOT NULL
	       AND result_status IN ('ok', 'truncated')
	       AND tool_name = ANY($3)
	     GROUP BY message_id
	),
	said AS (
	    SELECT g.rec->>'turn' AS turn,
	           sum(CASE WHEN jsonb_typeof(g.rec->'ungrounded') = 'array'
	                    THEN jsonb_array_length(g.rec->'ungrounded') ELSE 0 END
	             + CASE WHEN jsonb_typeof(g.rec->'ungrounded_percents') = 'array'
	                    THEN jsonb_array_length(g.rec->'ungrounded_percents') ELSE 0 END) AS unaccounted
	      FROM messages m
	      JOIN conversation_threads t ON t.id = m.thread_id
	      CROSS JOIN LATERAL (SELECT m.metadata->'grounding' AS rec) g
	     WHERE t.company_id = $1
	       AND m.created_at >= $2
	       AND m.role = 'assistant'
	       AND jsonb_typeof(g.rec) = 'object'
	     GROUP BY 1
	)
	SELECT tl.computes > 0,
	       tl.sources >= 2,
	       s.turn IS NOT NULL,
	       COALESCE(s.unaccounted, 0) > 0,
	       count(*)
	  FROM tools tl
	  LEFT JOIN said s ON s.turn = tl.turn
	 GROUP BY 1, 2, 3, 4`

	rows, err := r.db.QueryContext(ctx, q, companyID, since, pq.Array(dataTools))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := []domain.TurnShape{}
	for rows.Next() {
		var s domain.TurnShape
		if err := rows.Scan(&s.Computed, &s.CrossSource, &s.Checked, &s.Unaccounted, &s.Turns); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
