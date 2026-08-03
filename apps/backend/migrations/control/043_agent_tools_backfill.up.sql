-- Backfill the two tools a scoped agent was never given a way to ask for.
--
-- An agent's `allowed_tools` is the whole of what `filterTools` hands the model,
-- and `draftFromTemplate` copies a gallery card's `suggested_tools` into it
-- verbatim. Four of the six cards — sales, marketing, people, support — listed
-- get_schema, run_sql and create_visualization and nothing else, so every agent
-- created from one of them could query and chart and could not produce a file.
--
-- What that looked like from the outside: a Sales agent asked for "a sales
-- overview report in PDF" answered with a markdown document in the chat, said it
-- could not export a file "from this interface", and told the user to press
-- Ctrl+P. Nothing was broken and nothing was logged. The capability had simply
-- never been ticked, and the product never said so.
--
-- The cards are fixed in config/agent_templates.yaml, but a template reaches no
-- existing agent by design (locked decision 4): a created agent is an ordinary
-- roster row that knows nothing about the file that seeded it. So the rows that
-- already exist are fixed here, once.
--
-- Two statements rather than one array append, because the two tools answer
-- different questions:

-- Every scoped agent gains generate_document. There is no job in the gallery —
-- and no job outside it — that cannot be asked for a report, and the answer to
-- that request is a file.
--
-- `allowed_tools <> '{}'` skips the unrestricted agents: empty means EVERY tool
-- (domain.Agent.AllowsTool), so writing names into one would *narrow* it from
-- everything to two, which is the one edit this migration must never make.
UPDATE agents
   SET allowed_tools = allowed_tools || '{generate_document}'
 WHERE allowed_tools <> '{}'
   AND NOT allowed_tools @> '{generate_document}';

-- create_dashboard goes only to agents that can already make a card. A card_id
-- is not something a user can open — create_dashboard is what turns one into a
-- URL — so an agent with create_visualization and without this has half a
-- feature. An agent with neither is not asking for charts and does not want a
-- dashboard tool it has no cards for.
UPDATE agents
   SET allowed_tools = allowed_tools || '{create_dashboard}'
 WHERE allowed_tools <> '{}'
   AND allowed_tools @> '{create_visualization}'
   AND NOT allowed_tools @> '{create_dashboard}';

-- On a deployment without object storage `generate_document` is not in the tool
-- registry, so the name sits in the row and reaches nothing: filterTools matches
-- by name and simply does not find it. The turn behaves exactly as it does
-- today, and the agent starts producing documents the day that deployment gets a
-- bucket. AgentService.Update tolerates a stored name it cannot register for
-- this reason — see normalizeTools — so an admin editing such an agent is not
-- shown an error about a tool they never chose.
