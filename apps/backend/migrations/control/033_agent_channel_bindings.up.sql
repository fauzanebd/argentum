-- Channel bindings: which agent answers in which Discord channel, Lark chat or
-- WhatsApp number (T-S4).
--
-- The roster reaches the dashboard (T-S3) and `/v1` (T-S5) by letting the
-- caller name an agent. The chat channels have nobody to ask: an inbound
-- Discord message carries a user and a channel and no place to put a picker, so
-- until this table every channel turn ran as the company default. The ops team
-- asking in the ops channel is the case the channel integrations exist for, and
-- making them open the dashboard to reach the Ops agent removes the reason.
--
-- The binding is on the *address*, not on the person: a channel is a room an
-- admin configured, and every message that arrives in it is about that room's
-- job. Per-user bindings are a follow-on and touch no column here.
--
-- Unbound is the ordinary state and means the company default — this table
-- holds exceptions, so a company that never opens the tab keeps exactly the
-- behaviour it has today.
CREATE TABLE IF NOT EXISTS agent_channel_bindings (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id  UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    -- ON DELETE CASCADE, not SET NULL: a binding whose agent is gone is not a
    -- binding to nothing, it is the absence of a binding, and the channel
    -- correctly falls back to the company default. Keeping the row would leave
    -- a dangling id in a lookup that runs on every inbound message.
    agent_id    UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    channel     TEXT NOT NULL,   -- domain.Channel; only the inbound chat ones
    -- Discord channel id | Lark chat id | E.164 phone number, stored as the
    -- provider gives it — except the phone number, which is normalised through
    -- the same path allowed_phone_numbers uses (domain.NormalizePhone). A
    -- `whatsapp:` prefix on one side of that comparison and not the other is a
    -- lookup that silently never matches.
    external_id TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One agent per address. Two bindings on one Discord channel is not a merge
-- rule to be invented at read time, it is a mistake at write time, so the
-- database says so and the API answers 409.
CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_binding_channel_ref
    ON agent_channel_bindings(company_id, channel, external_id);

-- The FK cascade above is a `DELETE ... WHERE agent_id = $1`, which is the only
-- query in the schema that names this column. Without the index, deleting an
-- agent seq-scans every binding in the deployment.
CREATE INDEX IF NOT EXISTS idx_agent_binding_agent
    ON agent_channel_bindings(agent_id);
