package postgres

import (
	"context"
	"fmt"

	"github.com/lib/pq"
)

// AgentsByThread returns, for every conversation of the company among ids,
// every agent that is part of it or ever was (T-Z10): the agent the thread runs
// as, the agents in its room, and every agent that wrote a message in it.
//
// **All three, because each misses a case the others catch.** The thread's own
// agent misses a room. The room misses an agent that answered and was then
// removed — its answers are still in the transcript, and removing it from the
// room is not removing what it said. The authors miss a conversation opened on
// an agent that has not answered yet, and every message written before 077
// gave messages an agent_id. A thread that has none of the three — unpinned and
// unattributed — maps to an empty list, which the caller reads as "the company
// default", the agent T-S2 ran it as.
//
// A conversation that is not the company's, or not an id, is absent from the
// result rather than an error, the resource grants repository's rule
// (LoadAccess): a malformed id arrives alone from a URL, and not-found is the
// answer for it.
//
// One statement for the whole page. The messages arm walks each thread's rows
// by (thread_id, created_at) — 002's index — and 077 deliberately gave agent_id
// no index of its own; a page of a hundred busy conversations is where that
// would first show, and it is owed a measurement rather than an index nobody has
// shown is needed.
func (r *ThreadRepo) AgentsByThread(ctx context.Context, companyID string, ids []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(ids) == 0 {
		return out, nil
	}
	const q = `
		SELECT t.id::text, COALESCE(a.agent_id::text, '')
		  FROM conversation_threads t
		  LEFT JOIN LATERAL (
		        SELECT t.agent_id AS agent_id WHERE t.agent_id IS NOT NULL
		        UNION
		        SELECT p.agent_id FROM thread_participants p WHERE p.thread_id = t.id
		        UNION
		        SELECT m.agent_id FROM messages m
		         WHERE m.thread_id = t.id AND m.agent_id IS NOT NULL
		  ) a ON true
		 WHERE t.company_id = $1 AND t.id = ANY($2::uuid[])
	`
	rows, err := r.db.QueryContext(ctx, q, companyID, pq.Array(ids))
	if err != nil {
		if malformedID(err) {
			return out, nil
		}
		return nil, fmt.Errorf("load conversation agents: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, agent string
		if err := rows.Scan(&id, &agent); err != nil {
			return nil, fmt.Errorf("scan conversation agents: %w", err)
		}
		if _, ok := out[id]; !ok {
			out[id] = []string{}
		}
		if agent != "" {
			out[id] = append(out[id], agent)
		}
	}
	if err := rows.Err(); err != nil {
		if malformedID(err) {
			return map[string][]string{}, nil
		}
		return nil, fmt.Errorf("load conversation agents: %w", err)
	}
	return out, nil
}
