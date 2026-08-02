-- T-12b: registered HTTP endpoints — the targets an http_action may call.
--
-- The action framework (041) ships two action kinds. send_message (T-12a) reaches
-- an already-allowlisted recipient and needs no per-target configuration. This is
-- the other one: http_action lets an agent call one of a company's own systems —
-- a ticket queue, an ERP, an internal service. The safety property is that the
-- agent never types a URL. It names a *registered* endpoint, and everything about
-- the request — the method, the host, the credentials — was set by an admin here,
-- not by the model at turn time.
--
-- One table. Each row is one callable endpoint: a stable name the agent picks, a
-- method, a URL template whose authority (scheme://host) is literal and whose path
-- and query may carry {{.placeholders}} the agent fills, a header template sealed
-- at rest because it carries the credential, and an optional body template. The
-- host is fixed by construction — a placeholder in the authority is refused at
-- registration — so an agent-supplied value can never redirect the call to another
-- host. The SSRF egress guard (shared with the MCP client) is the second line: it
-- pins the resolved address and refuses our own network regardless of what a name
-- resolves to.
--
-- Numbered 042 from schema_migrations at implementation time; the tree is at 041
-- (company_actions). Reserved migration numbers in the ticket table have been
-- wrong eight times running — take the next free number.
CREATE TABLE IF NOT EXISTS http_endpoints (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id        UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    -- The name the agent proposes against, e.g. 'create_ticket'. Stable and
    -- company-scoped: an admin renames a target by deleting and re-registering,
    -- because a rename would change what an in-flight proposal points at.
    name              TEXT NOT NULL,
    -- GET | POST | PUT | PATCH | DELETE. Validated at registration; TEXT rather
    -- than an enum for the reason 041 gives — ALTER TYPE cannot run inside the
    -- transaction golang-migrate wraps a migration in.
    method            TEXT NOT NULL,
    -- The URL, with a literal authority and optional {{.placeholders}} in the path
    -- and query. Registration refuses a placeholder before the first '/' after the
    -- scheme, so the host an admin registered is the host the call reaches.
    url_template      TEXT NOT NULL,
    -- The request headers as a sealed JSON template (crypto.DSNCipher, AES-256-GCM),
    -- because this is where the credential lives: {"Authorization":"Bearer …"}.
    -- Encrypted at rest for the same reason a DSN is, and never returned to a list
    -- view — a browser sees only whether one is set. NULL for an endpoint that
    -- needs no headers.
    header_encrypted  BYTEA,
    -- An optional request-body template, {{.placeholders}} filled from the same
    -- values as the URL. Empty for a GET or a call with no body. Not a secret, so
    -- not sealed; a body carrying a credential belongs in the header instead.
    body_template     TEXT NOT NULL DEFAULT '',
    -- Who registered it. Nullable and unreferenced, like company_actions.created_by:
    -- the endpoint outlives the admin who set it up.
    created_by        UUID,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- One endpoint per name per company. Registering 'create_ticket' twice is a
    -- contradiction, not two rows; the case-insensitive collision is the service's
    -- to catch with a clearer message before it reaches this constraint.
    UNIQUE (company_id, name)
);

CREATE INDEX IF NOT EXISTS idx_http_endpoints_company ON http_endpoints(company_id);
