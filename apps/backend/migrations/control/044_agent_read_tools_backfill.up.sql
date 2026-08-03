-- The rest of 043's class: read tools a scoped agent was never offered.
--
-- Separate from 043 because the risk is not the same. 043 adds two tools that
-- change only what an agent can *hand back* — a document, a dashboard URL — and
-- nothing about what it can read. The two below touch reading, so each carries
-- its own condition and its own reason for being safe.
--
-- The shape is the same, though, and it is the one worth remembering: an
-- agent's `allowed_tools` is a copy of a default taken on the day the row was
-- written. `list_metrics` and `query_metric` arrived in 039, nine migrations
-- after `agents` and four after the gallery — so every agent scoped before the
-- metric registry existed is scoped as though it still does not. Nothing tells
-- the tenant that. The turn simply re-derives with SQL a number the workspace
-- has already defined, which is exactly what T-07 was built to stop.

-- list_metrics, unconditionally, to every scoped agent.
--
-- It reveals nothing the turn does not already carry: ChatRunner prepends the
-- company's enabled metric catalog to the user message as "[System context:
-- Defined metrics …]" on every turn regardless of the agent's allowlist. The
-- tool is the same list, on demand, and an agent that can read the block and
-- cannot call the tool is a strictly worse version of the same access.
UPDATE agents
   SET allowed_tools = allowed_tools || '{list_metrics}'
 WHERE allowed_tools <> '{}'
   AND NOT allowed_tools @> '{list_metrics}';

-- query_metric, and ONLY to agents with no source restriction.
--
-- The gate is not caution, it is a real hole this backfill must not widen. The
-- metric tools scope by company and nothing else (internal/tools/metric_tools.go
-- resolves the metric's own source and never consults agentscope), while
-- run_sql, get_schema, create_visualization and list_sources all narrow to the
-- agent's `agent_sources` rows. So handing query_metric to a source-scoped agent
-- lets it read numbers derived from a source its administrator deliberately kept
-- it away from — a scope decision reversed by a migration nobody asked for.
--
-- An agent with no rows in agent_sources has every source already (empty means
-- unrestricted, 030), so for those there is no scope to cross. The rest keep
-- their scope and stay without the tool, which is the honest half of this:
-- source-scoped agents get the fix when the tools themselves respect the
-- allowlist, not before.
UPDATE agents a
   SET allowed_tools = a.allowed_tools || '{query_metric}'
 WHERE a.allowed_tools <> '{}'
   AND NOT a.allowed_tools @> '{query_metric}'
   AND NOT EXISTS (SELECT 1 FROM agent_sources s WHERE s.agent_id = a.id);

-- list_sources is deliberately NOT backfilled. It is agent-scoped, so it would
-- be safe — and it is also redundant: the same catalog, narrowed the same way,
-- is prepended to every turn as "[System context: Available data sources …]".
-- A checkbox that changes nothing is a checkbox nobody can reason about later.
