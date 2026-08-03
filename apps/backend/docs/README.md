# Backend docs — what is where

This directory predates the monorepo and had grown a second copy of things that
live elsewhere. This page is the index (T-18), and the rule it encodes is: **one
document per fact, and this file says which one.**

## The public API (`/v1`)

| You want | Read |
| -------- | ---- |
| The contract | [`../openapi/v1.yaml`](../openapi/v1.yaml) — authored, not derived, and bound to the router by four CI checks |
| To get started | [`../../../docs/api/quickstart.md`](../../../docs/api/quickstart.md) — every code block on the page is a file CI executes |
| Served spec | `GET /v1/openapi.json`, keyless |
| Postman | [`postman/`](postman), regenerated from the spec |
| SDKs | `@argentum/sdk` (Node), `argentum` (Python), both generated from the spec |

**[`api.md`](api.md) describes the dashboard's `/api` routes and is partly
stale** — it predates the `bigref` refactor. Its `/v1` section is superseded by
the spec above, and the file says so at the top.

## The dashboard API (`/api`)

Not published, versionless on purpose: it changes with the dashboard, and `/v1`
exists so that churn never reaches an integrator. The router is the reference;
`cmd/api/policy.go` is the enumeration of which routes are admin-only, and a
test diffs it against the built router so it cannot drift.

## Streaming

The WebSocket event schema — `started`, `delta`, `thinking`, `tool_call`,
`tool_result`, `action_proposed`, `iteration`, `error`, `final` — is documented
in [`../../../docs/coverage/api-chat.md`](../../../docs/coverage/api-chat.md)
§2, beside the SSE shape it mirrors. One document, because the two transports
carry the same events and a second copy would drift the day one of them gains a
field.

## Tool contracts

The tools are their own documentation, and deliberately: each one's
`Description()` and `Parameters()` are what the model reads, so a prose copy in
this directory would be a second description that goes stale without anything
failing. `internal/tools` is the list; `internal/mcpserver` names the subset a
customer's agent may call and the scope each needs.

Two behaviours are not visible from a tool's own description and are written
down instead:

- **Every call is audited and metered.** `tools.WithAudit` writes one
  `agent_actions` row per call; the same seam records the metric and the trace
  span ([`../../../docs/coverage/agent-audit.md`](../../../docs/coverage/agent-audit.md)).
- **Every call is budget-guarded.** `agentbudget` refuses tools during a turn's
  final permitted iteration so the model answers knowing it ran out
  ([`../../../docs/coverage/eval-baseline.md`](../../../docs/coverage/eval-baseline.md)).

## Features with their own record

Each of these has a coverage document under `docs/coverage/` carrying the
design, the decisions and what was found when it was gated live:

| Feature | Record |
| ------- | ------ |
| Metric registry | `metric-registry.md` |
| Watchers | `watchers.md`, `watchers-ui.md` |
| Actions and approval | `action-framework.md` |
| Tenant MCP servers (we call theirs) | `mcp-source.md` |
| Argentum as an MCP server (they call ours) | `mcp-server.md`, and [`../../../docs/mcp/setup.md`](../../../docs/mcp/setup.md) for the client config |
| Outbound webhooks | `outbound-webhooks.md` |
| API keys and scopes | `api-keys.md` |
| Observability | `observability.md`, `api-observability.md` |
| Guardrails | `guardrail-overreach.md` |
| Reports and branding | `report-rendering.md`, `report-branding.md`, `report-deck.md` |

## Operational notes in this directory

[`db-regenerate-description`](db-regenerate-description),
[`lark-discord-integrations`](lark-discord-integrations),
[`scheduled-tasks`](scheduled-tasks) and [`usage`](usage) are runbook-style
notes written while those features were built. They are accurate about intent
and may lag the code; the coverage documents above are the maintained record.
