-- T-Z8: the three doors with no person, decided rather than inherited.
--
-- A grant is a property of an Argentum user (roadmap 12, decision 7), and only
-- the dashboard carries one. Every other way into an agent needed its own rule,
-- and each rule needed somewhere to keep what an admin decided:
--
--   * `/v1` — a key is a machine, and gets an agent allowlist of its own rather
--     than borrowing its minter's grants (decision 9);
--   * a channel binding — the channel is the grant, but only once an admin has
--     acknowledged, for that address, that anyone who can post there can use a
--     restricted agent (decision 8);
--   * watchers and scheduled tasks — each runs as its creator and is re-checked
--     at fire time, and a revoked grant switches it off **and says why**
--     (decision 11), which needs a place to say it.
--
-- The widget needs no column: it never reaches a restricted agent, whatever an
-- admin configured, which is decision 10 and is enforced at turn time.
--
-- Nothing changes for anybody on the day this applies. An empty allowlist is
-- every agent, which is what every key reaches today; an unacknowledged binding
-- matters only for a restricted agent, and nothing restricted is deployed; a
-- NULL reason is a task nobody switched off. Every ADD COLUMN is a constant
-- default or none, so no table is rewritten, and no repository reads these tables
-- with SELECT * or RETURNING *, so a pod on the previous release never meets the
-- columns during the rolling deploy that ships them.

-- UUID[] with no foreign key, on purpose, and the choice is about which way a
-- deletion fails. A join table cascading from agents would shrink a key's list
-- when an agent is deleted — and a list that shrinks to nothing reads as *every
-- agent*, so deleting the one agent a reporting key was limited to would open the
-- whole roster to it. A dangling id here names nothing and admits nothing: the
-- key reaches what is left on its list, and a list of only dead ids reaches no
-- agent at all.
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS agent_ids UUID[] NOT NULL DEFAULT '{}';

-- An acknowledgement is a timestamp and a person, not a boolean: "who said anyone
-- in #hr may use HR" is the question an audit asks, and agent_actions carries the
-- same answer permanently. `restricted_ack_by` is SET NULL so deleting the admin
-- keeps the acknowledgement they made — the audit row still names them.
ALTER TABLE agent_channel_bindings
    ADD COLUMN IF NOT EXISTS restricted_ack_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS restricted_ack_by UUID REFERENCES users(id) ON DELETE SET NULL;

-- Why the product switched it off, NULL when a person did or nobody has. No
-- CHECK, unlike 084's access_mode: a reason is a sentence the code may add a
-- kind of in a commit, and a CHECK here would make the previous release's
-- binary the one that refuses the new one's writes mid-deploy.
ALTER TABLE watchers
    ADD COLUMN IF NOT EXISTS disabled_reason TEXT;
ALTER TABLE scheduled_tasks
    ADD COLUMN IF NOT EXISTS disabled_reason TEXT;
