# Connect your agent to Argentum over MCP

Argentum speaks MCP, so an agent you did not have to write — Claude Code, Claude
Desktop, your own — can query your warehouse, read your metrics and build a
Metabase chart, using the same tools Argentum's own agent runs.

This is the opposite direction from *Settings → MCP servers*. That screen is for
registering **your** MCP server so Argentum's agent can call it. This page is for
pointing **your** agent at Argentum.

## 1. Mint a key

Settings → **API keys** → *Create key*. Give it the scopes the tools you want
need:

| Tool | Scope | What it does |
| ---- | ----- | ------------ |
| `list_sources` | `read:data` | The databases this workspace has connected |
| `get_schema` | `read:data` | Tables and columns for one source |
| `run_sql` | `read:data` | A read-only query. Always read-only — this is enforced in the driver, not in a prompt |
| `list_metrics` | `read:metrics` | The metric registry: what is defined, and its grain |
| `query_metric` | `read:metrics` | One defined metric's value for a window |
| `create_visualization` | `write:visualizations` | A Metabase card |
| `create_dashboard` | `write:visualizations` | A Metabase dashboard from cards |

**Scopes are fixed when the key is minted.** There is no edit — adding a
capability means a new key. Mint the narrowest set that does the job; a key that
only reads metrics cannot run SQL, which is the point.

The key is shown once. Store it the way you store any other credential.

## 2. Point your client at it

The server speaks the streamable HTTP transport at the root path, authenticated
with the key as a bearer token.

**Claude Code** — `.mcp.json` in your project, or `~/.claude.json` for every
project:

```json
{
  "mcpServers": {
    "argentum": {
      "type": "http",
      "url": "https://mcp.your-argentum-host.example/",
      "headers": {
        "Authorization": "Bearer ak_live_your_key_here"
      }
    }
  }
}
```

**Anything else**: any MCP client that supports a remote server over HTTP with a
custom header will work. The server name in the handshake is `argentum`.

Check it connected by asking your agent to list its tools; the Argentum ones are
the seven in the table above, minus any this deployment does not run —
`create_visualization` and `create_dashboard` are absent where Metabase is not
configured.

Watchers are not on this surface. They are configured and read in the dashboard,
and a breach reaches you as a message on your channels or as a
`watcher.breached` webhook — Settings → Webhooks — rather than by an agent
polling for them.

## 3. What your agent can and cannot do

**Everything it calls is audited.** Each tool call writes an `agent_actions`
row with `actor_kind = api_key` and your key's id, exactly as a turn in the
dashboard writes one for a user. Settings → API keys shows the traffic;
`GET /v1/usage` shows what it cost.

**It cannot write to your systems.** The tools that change something outside
Argentum — generating and storing a document, scheduling a task, proposing an
action for approval — are not on this surface. An MCP client is an agent
Argentum did not write, reasoning without Argentum's system prompt and without
the guardrails a turn runs under, so what it gets is the read surface plus the
two Metabase writes. Those capabilities stay behind a turn or behind `/v1`.

**Queries are read-only and capped.** `run_sql` runs inside a read-only
transaction with a statement timeout, and results are capped at the same row and
byte limits the agent's own queries are. A query that hits the cap comes back
truncated with a flag, not silently short.

**A missing scope reads as a tool error, not a broken server.** The message
names the scope you need. A missing or invalid *key* is a `401` before the MCP
session starts, so a client that cannot connect at all has a credential problem
rather than a tool problem.

## 4. Running the server

`cmd/mcp` is a separate process from the API and the worker, on its own port —
an MCP session is long-lived, and one that hangs should not be able to exhaust
the dashboard's connection budget. It shares the tool registry, the tenant
connection pool, the audit sink and the metering path with the worker by
building the same stack, which is what makes "the tool your agent calls is the
tool our agent runs" true rather than aspirational.

| Setting | Default | Notes |
| ------- | ------- | ----- |
| `MCP_SERVER_ADDR` | `:8081` | Listen address |

Everything else — database, Redis, encryption key — is the same environment the
worker takes. `GET /health` answers without a key, for a probe.
