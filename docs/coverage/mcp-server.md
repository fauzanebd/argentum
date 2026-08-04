# T-14 · Argentum as an MCP server — coverage

**Status: GATED LIVE 2026-08-04**, with one defect found by the gate (§7) and
the Helm deployment still an operator's call (§6). The handshake, the transport,
the scope split, the audit row and the usage event were all watched against a
real client.

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

## 6. The gate, run 2026-08-04

The client was the official MCP TypeScript SDK over the streamable HTTP
transport with the key as a bearer token — the transport `docs/mcp/setup.md`
documents for Claude Code, exercised by a client we did not write the server for.

| Step | Result |
| ---- | ------ |
| Handshake | `{"name":"argentum","version":"1"}` |
| `POST /` with no key | `401`, `WWW-Authenticate: Bearer realm="argentum-mcp"`, before any session — §4 holds |
| `tools/list` | **7 tools**, not the 8 this document claims — see §7 |
| `list_metrics` | the registry, `isError: false` |
| `query_metric` | `{"metric_key":"active_customers","value":50,"window":{…},"row_count":1}` |
| `run_sql` (key with `read:data`) | `{"row_count":1,"rows":[{"customers":50}],"truncated":false}` |
| `run_sql` (key with only `read:metrics`) | tool error: *"this API key does not carry the read:data scope, which run_sql requires"* — the ticket's own acceptance criterion |

**The audit and the meter both wrote.** Each call left an `agent_actions` row
with `actor_kind = api_key` and the key's id in `actor_ref`, and the two calls
that ran SQL left `usage_events` rows of type `sql_query` at the same instants.
No audit code exists in this package, which is the point of §1.

**Two observations that are not defects but are not written down anywhere
either.** `tools/list` is not filtered by scope — a `read:metrics`-only key is
shown `run_sql` and the two Metabase writes and learns on refusal, which is what
§4 chose deliberately for the *error*, but the *listing* consequence is
undocumented. And a scope refusal writes no `agent_actions` row, because it is
checked before `tool.Execute`: a key probing tools outside its scope leaves the
audit silent.

## 7. Found by the gate: `list_watchers` is advertised and does not exist

`exposed` in `internal/mcpserver/server.go` maps `list_watchers` →
`read:metrics`, this document's §2 lists it, and `docs/mcp/setup.md`'s table
sells it. **No tool in `internal/tools` is named `list_watchers`**, so `New`
skips it and the wire surface is seven tools. A tenant who reads the setup page
and asks their agent for their watchers gets "no such tool".

The reason it survived: `ExposedTools()` returns the map's keys, so the doc and
any test reading from it agree with the map rather than with the registry —
eight either way. The startup log prints both lists side by side (`tools` from
the map, `of` from the registry) and the disagreement is visible in it.

Two ways out, and they are not equivalent: write the tool (it would join the
*agent's* registry too, which changes every turn's prompt), or drop the entry
and the two doc rows. Left for the owner; the wire is the honest surface either
way.

## 8. Still not done

- **No Helm deployment.** `Dockerfile.mcp` exists and matches the discord
  image's shape, but the chart has no `deployment-mcp.yaml` — a deployment,
  a service and an IngressRoute, following `deployment-discord.yaml`. Left out
  deliberately rather than guessed at: the chart's ingress is where a hostname
  and a TLS certificate get decided, and both are an operator's call.
- **`docs/mcp/setup.md`'s client config has still not been pasted into Claude
  Code.** The gate proved the transport and the auth the config describes, with
  a client speaking the same protocol; what remains unproven is the config file
  itself, and §7's table is one row wrong in it.
