-- Backfill `compute` onto every scoped agent (T-W1).
--
-- An agent's `allowed_tools` is the whole of what `filterTools` hands the model,
-- and `draftFromTemplate` copies a gallery card's `suggested_tools` into it
-- verbatim. Every card in config/agent_templates.yaml lists its tools
-- explicitly, so without this every agent created from one is scoped away from
-- `compute` — and the templates are the agents that need it most. Their own
-- starter questions ask for derived figures: "revenue last month compared with
-- the month before", "our conversion rate from lead to closed deal". Those are
-- exactly the answers that used to be produced by the model dividing two
-- numbers inside a sentence, which is the class T-W1 exists to close.
--
-- The cards are fixed in config/agent_templates.yaml, but a template reaches no
-- existing agent by design (locked decision 4): a created agent is an ordinary
-- roster row that knows nothing about the file that seeded it. So the rows that
-- already exist are fixed here, once — the same shape as 043.
--
-- **This grants no new reach, which is why it is unconditional where 043 had to
-- be selective.** `compute` opens no connection, reads no source and calls
-- nothing. Its only inputs are results the same turn already retrieved through
-- tools the agent was already allowed, so there is no agent for whom "may also
-- compute" widens what it can see — only what it can say correctly about it.
--
-- `allowed_tools <> '{}'` skips the unrestricted agents: empty means EVERY tool
-- (domain.Agent.AllowsTool), so writing a name into one would *narrow* it from
-- everything to one, which is the one edit this migration must never make.
UPDATE agents
   SET allowed_tools = allowed_tools || '{compute}'
 WHERE allowed_tools <> '{}'
   AND NOT allowed_tools @> '{compute}';
