# Argentum — Architecture Map

Verified against `argentum` @ `3891579`. This document is the mental model an
agent needs before changing anything.

## 1. Process topology

Four deployable processes, all stateless, all horizontally scalable:

```
┌──────────────────┐   ┌──────────────────┐   ┌──────────────────┐
│  cmd/api         │   │  cmd/worker      │   │  cmd/discord     │
│  HTTP + WS       │   │  asynq consumer  │   │  gateway session │
│  asynq producer  │   │  + periodic mgr  │   │  per tenant bot  │
└────────┬─────────┘   └────────┬─────────┘   └────────┬─────────┘
         │                      │                      │
         └──────────┬───────────┴──────────┬───────────┘
                    ▼                      ▼
            ┌───────────────┐      ┌───────────────────┐
            │ Redis 7       │      │ Postgres 16       │
            │ asynq queue   │      │ control plane     │
            │ pub/sub bus   │      │ + pgvector        │
            │ agent memory  │      └───────────────────┘
            │ schema cache  │
            │ rate limiter  │      ┌───────────────────┐
            └───────────────┘      │ Metabase 0.60     │
                                   └───────────────────┘
                    │
                    ▼
          Tenant analytical databases (customer-owned, never migrated)
```

Plus: MinIO / S3 for generated documents (optional).

**Why the split matters:** the API never calls an LLM for chat. It validates,
resolves the thread, persists the user message, and enqueues. All agent work is
in the worker. This means API latency is unaffected by LLM latency, and the
worker can be scaled or restarted independently — in-flight tasks survive in
Redis and retry with backoff.

## 2. The chat request lifecycle

The single most important flow in the system. Trace it once and the rest follows.

```
1. POST /api/chat  (or WhatsApp / Discord / Lark webhook)
      │
2. ChatEnqueuer
      ├─ ThreadService.ResolveFor{User,Phone,DiscordUser,Lark}
      │     └─ idle-gap check → LLM topic classifier → continue or fork thread
      ├─ MessageRepository.Append (role=user)
      └─ asynq.Enqueue("chat:run", ChatRunPayload)
      │
3. cmd/worker  asynq.Server picks up chat:run
      │
4. ChatRunner.Run
      ├─ tenantctx: company / thread / user into ctx
      ├─ trivialReply() short-circuit?  → publish + persist + return
      ├─ llmCache.For(company, primary)  → MeteredLLM-wrapped client
      ├─ llmCache.For(company, light)    → falls back to primary
      ├─ agentFactory(primary, light, interface) → fresh sdkagent.Agent
      │     ├─ tools, memory, system prompt captured in closure
      │     ├─ guardrails template rebound to this tenant's light LLM
      │     └─ Anthropic-only: prompt-cache config
      ├─ hydrateMemory: last N messages from Postgres → agent memory
      ├─ context injection: table hint → sources → currency → company
      ├─ agent.RunStream  (falls back to agent.Run if unsupported)
      │     └─ per event: publish delta / thinking / tool_call / tool_result
      └─ completeWith
            ├─ MessageRepository.Append (role=assistant)
            ├─ ScheduledRunMarker.MarkRunResult (if scheduled run)
            ├─ bus.Publish(final)
            └─ channel-specific outbound: WhatsApp send / Discord publish / Lark reply
      │
5. Redis PUBLISH argentum:thread:{id}
      │
6. cmd/api  WS handler (SUBSCRIBEd) → browser
```

**Key consequence:** any API replica can serve the WebSocket for any thread,
because events travel over Redis pub/sub, not in-process channels.

## 3. Layering

Clean hexagonal-ish architecture, consistently applied:

```
internal/
  domain/        Pure entities + repository interfaces. No imports outside stdlib
                 + sibling domain types. This is the contract layer.
  app/           Use-case services. Depend on domain interfaces, never on
                 concrete adapters. ChatEnqueuer (API side) and ChatRunner
                 (worker side) are the two halves of the chat pipeline.
  adapters/
    postgres/    Control-plane repository implementations (one file per aggregate)
    db/          Tenant DB driver abstraction + per-dialect drivers
    storage/     MinIO / S3
  transport/
    http/        Gin handlers + middleware
    ws/          WebSocket, Redis-subscribed
    eventbus/    Redis pub/sub EventBus implementation
  tools/         agent-sdk Tool implementations (the agent's action surface)
  <infra>/       auth, crypto, cache, queue, metabase, guardrails, whatsapp,
                 discord, lark, llmclient, llmtenant, embedding, metrics,
                 migrate, tenantctx, config
```

**Dependency direction is enforced by convention, not tooling.** `internal/app`
imports `internal/tools` (for the `CreateScheduledTaskInput` type), which is why
`UsageRecorder` is declared in `internal/tools/run_sql.go` rather than in
`internal/app` — an import cycle would otherwise form. That comment is in the
source; preserve the arrangement.

## 4. Multi-tenancy model

Three independent mechanisms, all of which must hold for isolation:

1. **Request scoping.** `tenantctx` carries company / thread / user through the
   context. Every repository method that touches tenant data takes `companyID`
   and includes it in the WHERE clause. There is no ambient "current tenant".
2. **Connection isolation.** `TenantConnPool` keys pools by
   `(companyID, sourceID)`. DSNs are decrypted only inside
   `ConnectionResolver`, never returned upward.
3. **LLM isolation.** `llmtenant.ClientCache` keys clients by
   `(companyID, tier)`. A tenant with its own key never shares a client with the
   env-default pool.

`multitenancy.WithOrgID(ctx, companyID)` and
`memory.WithConversationID(ctx, threadID)` additionally scope the agent SDK's own
memory namespace — so Redis-backed agent memory cannot bleed between tenants or
threads.

## 5. Control-plane schema evolution

**56 forward migrations as of 2026-08-17, all 56 with a `.down.sql`.** The table
below is the first twenty, kept as written because it is the history this
document was built to explain; `021`→`056` are the Sprint 1–3 tracks —
report branding, RBAC, the audit log, metrics and watchers, actions, API keys,
the agent roster, business context, embed keys and widget config, message
feedback, the query cookbook, and `056_dashboards`. Read
`migrations/control/` for the current list rather than this table.

The first twenty tell the product's early history precisely:

| #   | Migration                          | What it unlocked                      |
| --- | ---------------------------------- | ------------------------------------- |
| 001 | init                               | companies, users, connections, phones |
| 002 | threading                          | conversation_threads, messages        |
| 003 | metering                           | usage_events, company_credits         |
| 004 | metabase_tenant_connections        | Metabase warehouse sync per tenant    |
| 005 | company_currency                   | per-tenant money formatting           |
| 006 | saved_dashboards                   | persisted dashboard artifacts         |
| 007 | documents                          | generate_document tracking            |
| 008 | db_connection_description          | LLM-written source descriptions       |
| 009 | scheduled_tasks                    | cron automation                       |
| 010 | usage_event_model                  | per-model cost attribution            |
| 011 | db_connection_embedding            | pgvector table embeddings             |
| 012 | company_llm_credentials            | BYO-LLM per tenant                    |
| 013 | drop_table_embeddings_ivfflat      | index strategy change                 |
| 014 | usage_cache_tokens                 | Anthropic prompt-cache billing        |
| 015 | company_discord_credentials        | Discord channel                       |
| 016 | allowed_discord_users              | Discord allowlist                     |
| 017 | thread_discord                     | Discord thread keying                 |
| 018 | company_lark_credentials           | Lark channel                          |
| 019 | allowed_lark_users                 | Lark allowlist                        |
| 020 | thread_lark                        | Lark thread keying                    |

**Corrected 2026-08-17: every migration has a `.down.sql`, all 56 of them.**
This section said *"only 015–020 have down files; 001–014 are irreversible"*,
and the delivery log's own patterns list carried "no down migrations after 014"
as a standing weakness. Both were true when written and neither is now —
`001_init.down.sql` through `014` exist on disk. The `add-migration` playbook's
"both files, always" rule is the one that closed it. See gap analysis for the
finding's original form.

## 6. Configuration surface

`internal/config/config.go` — **970 lines and ~120 `getEnv*` reads as of
2026-08-17** (was 494 lines / ~75 variables on 2026-07-26), all with defaults,
validated in `Validate()`.

Required (hard failure on boot): `LLM_API_KEY`, `ARGENTUM_JWT_SECRET`,
`ARGENTUM_DSN_KEY`, `DB_PASSWORD`, and provider-conditional WhatsApp credentials.

Notable derived accessors — use these, never the raw field:
`EffectiveLLMInterface()`, `EffectiveLightLLMModel()`,
`EffectiveClassifierModel()`, `EffectiveEmbeddingAPIKey()`,
`ResolvedAsynqRedisURL()`, `RedisDialAddr()`, `WorkerQueueMap()`.

Feature kill switches: `EMBEDDING_ENABLED`, `DISCORD_ENABLED`, `LARK_ENABLED`,
and the implicit `MINIO_ENDPOINT` (empty = no `generate_document` tool).

## 7. LLM tiering

Three roles, each independently configurable and separately priced:

| Tier       | Default                        | Used for                                          |
| ---------- | ------------------------------ | ------------------------------------------------- |
| Primary    | `anthropic/claude-haiku-4.5`   | The agent itself                                  |
| Light      | `gpt-5-mini`                   | Guardrail LLM patterns, rolling summaries, source descriptions |
| Classifier | `gpt-5-nano`                   | Thread continue-vs-fork topic classification      |
| Embedding  | `text-embedding-3-small`       | Table picker                                      |

Per-tenant overrides live in `company_llm_credentials`; env values are the
fallback. The light tier falls back to primary if resolution fails, so a
misconfigured tenant degrades rather than breaks.

## 8. Invariants an agent must preserve

1. `cmd/api` must never import `internal/tools` in a way that requires an LLM at
   boot. The API's `GetSchemaTool` instance exists **only** for cache
   invalidation on DSN rotation.
2. Each process has its **own** schema cache and LLM cache. Invalidation in one
   process does not propagate. If you add cross-process invalidation, do it over
   the Redis event bus (the pattern already exists for Discord credential reload).
3. Guardrails are parsed **once** at worker boot and rebound per turn with
   `.WithLLM(light)`. Do not re-parse YAML per request.
4. The agent is constructed **fresh per turn** by `AgentFactory` but tools,
   memory, and system prompt are captured in the closure. Tools must therefore be
   stateless and resolve tenant identity from `ctx`, never from struct fields.
5. `asynq` retries on returned error. `ChatRunner` deliberately returns `nil`
   after writing a user-visible guardrail message, because retrying a blocked
   message is pointless. Preserve that distinction.
6. Malformed task payloads return `asynq.SkipRetry` so bad tasks archive instead
   of looping.
