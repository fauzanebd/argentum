-- Down for 066. The index by name first, for `062`'s reason: dropping the
-- column would take it with it, and a down migration that relies on cascade
-- behaviour breaks the day somebody adds a second index.
--
-- What goes is the record of which kinds of untrusted content each turn read.
-- `document_tainted` survives, so the one taint that gates an action is still
-- answerable — and that is worth stating plainly, because it means a deployment
-- that downs this keeps its approval behaviour and loses only its ability to
-- ask the wider question.

DROP INDEX IF EXISTS idx_agent_actions_input_taint;

ALTER TABLE agent_actions DROP COLUMN IF EXISTS input_taint;
