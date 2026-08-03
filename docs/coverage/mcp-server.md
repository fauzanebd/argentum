# T-14 · Argentum as an MCP server — coverage

**Status: CODE COMPLETE 2026-08-03.** The gate — a transcript of an MCP client
retrieving a metric, plus the matching audit row and usage event — needs the
stack and a real client, and is outstanding.

**Not `T-M1`→`T-M4`.** Those make Argentum an MCP *client* so our agent can call
a tenant's server. This makes Argentum an MCP *server* so a tenant's agent can
call us. The names will keep colliding; the test is who holds the credential —
there it is theirs, here it is ours, issued as an API key.

## 1. What the re-scope in the ticket turned out to mean

`T-14` was re-scoped on 2026-07-28 to *"a thin adapter, not a new surface"*
after `T-A1` landed, and that held. The key auth, the scope vocabulary, the
audit attribution and the metering path all existed; what this ticket added is
the protocol binding and one deliberate decision about the surface.

**Nothing here implements a tool.** `internal/mcpserver` adapts
`internal/tools` — the same instances the agent runs, already wrapped by the
budget guard and the audit decorator — onto MCP. The ticket's hard rule was
*import `internal/tools`, do not reimplement any tool*, and the structure
enforces it: `New(tools []interfaces.Tool)` takes the registry it is given and
adds no wrapping of its own, so there is no second decorator chain in which the
audit rule could be wrong.

The consequence worth stating: an MCP call writes an `agent_actions` row with
`actor_kind = api_key` because the HTTP middleware sets three values on the
context and the existing decorator reads them. No audit code was written for
this ticket.

## 2. The surface, and what is deliberately not on it

| Tool | Scope |
| ---- | ----- |
| `list_sources`, `get_schema`, `run_sql` | `read:data` |
| `list_metrics`, `query_metric`, `list_watchers` | `read:metrics` |
| `create_visualization`, `create_dashboard` | `write:visualizations` |

**Absent: `generate_document`, `schedule_task`, `propose_action`.** Each spends
money, changes something outside Argentum, or produces an artifact somebody has
to be told about. An MCP client is an agent we did not write, reasoning without
our system prompt and without the guardrails a turn runs under — so it gets the
read surface plus the two Metabase writes, and everything that changes the world
stays behind a turn or behind `/v1`. A test asserts the exclusion by name, so
adding one of them back is a deliberate edit rather than a registry change
nobody noticed.

## 3. Two new scopes, and why they are not one

`read:data` and `write:visualizations` join the vocabulary. They gate this
surface and no `/v1` route, which is stated in the OpenAPI description of the
`Scope` enum so an integrator reading the spec does not go looking for the
routes they open.

`read:data` is separate from `read:metrics` on purpose, and the ticket's
acceptance criterion is exactly this distinction: *a `read:metrics`-only key
cannot `run_sql`*. A metric is a number an admin defined, validated and named;
`run_sql` is arbitrary SQL against every table the connection can see. A key
trusted with the first is not thereby trusted with the second.

`write:visualizations` is separate from `write:actions` because it writes to
Metabase rather than to a tenant's own system. Conflating them would mean a key
that may draw a chart may also file a ticket in the customer's helpdesk.

**Scopes are fixed at mint time and there is no edit** (T-13), so both were
added to the vocabulary in the same commit as the surface they gate — the
alternative is every key minted between the two having to be re-issued.

## 4. Auth is HTTP middleware, not an MCP check

A caller with no key is refused with `401` before an MCP session exists. Done at
the protocol level instead, the handshake would succeed, the client would list
tools, and every call would come back as a tool error — which reads to the
operator as "the server is broken" rather than "your key is missing".

Unknown, revoked and expired all answer with one sentence. A caller who learns
which of the three they hit is a caller probing key space with feedback.

A *missing scope*, by contrast, is a tool error rather than a transport error:
the client's agent reads it, and "your key is missing `read:data`" is something
it can relay to a human who can fix it. The message names the scope for that
reason.

## 5. A third process

`cmd/mcp`, on `MCP_SERVER_ADDR` (default `:8081`), for the reason `cmd/discord`
is a third process: a different protocol, a different port, a different failure
mode. An MCP session is long-lived by design — the streamable transport holds a
response open — and one that hangs must not be able to exhaust the dashboard's
connection budget. `ReadHeaderTimeout` is set and the body timeouts are not,
which is the correct shape for exactly that reason.

It builds the same `internal/bootstrap` stack the worker does, which is what
makes "the tool your agent calls is the tool our agent runs" true rather than
aspirational.

`GET /health` answers without a key: a probe is not a caller.

## 6. Not done

- **The gate.** A real client — Claude Code against a running deployment —
  listing tools, retrieving a metric, and the `agent_actions` row plus the usage
  event that followed. Everything below the protocol is already proven by
  `T-06`/`T-07`'s live gate; what is unproven is the handshake and the transport.
- **`docs/mcp/setup.md` is written but unverified.** The client config in it is
  the documented shape for a remote HTTP MCP server; nobody has pasted it into a
  client and watched it connect.
- **No Helm deployment.** `Dockerfile.mcp` exists and matches the discord
  image's shape, but the chart has no `deployment-mcp.yaml` — a deployment,
  a service and an IngressRoute, following `deployment-discord.yaml`. Left out
  deliberately rather than guessed at: the chart's ingress is where a hostname
  and a TLS certificate get decided, and both are an operator's call.
