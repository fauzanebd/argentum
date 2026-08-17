-- T-D22: give every agent that can build a dashboard the ability to fix one.
--
-- `agents.allowed_tools` is a frozen copy taken at creation — `draftFromTemplate`
-- copies a gallery card's `suggested_tools` verbatim and nothing re-reads the
-- file afterwards (locked decision 4). So a capability added to the registry
-- reaches no existing agent, and config/agent_templates.yaml only fixes the
-- agents nobody has created yet. 043 is the precedent and this is the same shape.
--
-- What it costs to skip this is not a missing feature, it is a worse answer.
-- An agent holding create_dashboard and not update_dashboard, asked to widen a
-- date range, does the only thing it can: it builds a SECOND dashboard. The
-- tenant collects near-duplicates, the link already sent keeps serving the wrong
-- window, and the dashboard the user is looking at never changes. That is the
-- 2026-08-17 live gate's closing sentence, one release later and automated.
--
-- `allowed_tools <> '{}'` skips the unrestricted agents: empty means EVERY tool
-- (domain.Agent.AllowsTool), so writing a name into one would NARROW it from
-- everything to one, which is the single edit this migration must never make.
--
-- The `@> '{create_dashboard}'` condition is what keeps this from being a
-- capability grant. An agent that was never given the dashboard tool is not
-- asking for dashboards and does not want an editor for something it cannot
-- produce; an agent that has one is already half-way through the feature.
UPDATE agents
   SET allowed_tools = allowed_tools || '{update_dashboard}'
 WHERE allowed_tools <> '{}'
   AND allowed_tools @> '{create_dashboard}'
   AND NOT allowed_tools @> '{update_dashboard}';
