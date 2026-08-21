# Argentum — Workspace Documentation

Documentation for the Argentum product (Smartsoft). Lives at the monorepo root and
covers every app in it.

> **Version control:** as of `T-00b` the workspace is a **single git repo**, so
> this directory is tracked like everything else. Before that migration it was an
> untracked local working set — if you are reading a copy that predates the
> monorepo, see [`plan/01-tickets.md`](plan/01-tickets.md) `T-00b`.

## What Argentum is

A B2B agentic analytics assistant. A customer connects their analytical
database (Postgres / MySQL / SQL Server), then talks to the agent from the web
dashboard, WhatsApp, Slack, Discord, or Lark. The agent introspects the schema, writes
and runs read-only SQL, builds Metabase cards and dashboards, generates
downloadable documents, schedules recurring reports, and replies in the user's
language.

## Monorepo layout

| Path                    | What it is                                | Stack                          |
| ----------------------- | ----------------------------------------- | ------------------------------ |
| `apps/backend/`         | API, worker, Discord gateway              | Go 1.26, Gin, asynq, Postgres  |
| `apps/dashboard/`       | Customer-facing web app                   | React 18, Vite, TanStack       |
| `apps/landing/`         | Marketing site                            | React 18, Vite, Tailwind       |
| `apps/render/`          | The video renderer (T-V2): a plan in over HTTP, an MP4 out. The one image here with a browser in it, deployed behind `egress: []` | Node 22, Remotion, ffmpeg |
| `apps/widget/`          | **Built 2026-08-09/10** (T-19→T-23) — the embeddable chat: a 1.6 KB loader and a 32 KB Preact app in a sandboxed iframe, on `/api/embed`'s five routes. Live gate outstanding; npm/CDN publishing not done. See `coverage/widget.md` | Preact, iframe |
| `packages/api-types/`   | TS types generated from the Go structs by tygo, committed and diffed by CI (T-02b) | TypeScript      |
| `packages/motion/`      | The Remotion compositions a video plan is drawn with (T-V2). Holds no palette, no type scale and no layout — everything comes from the plan | Preact/React + Remotion |
| `packages/argentum-node/` | `@argentum/sdk` — the public API from Node, types generated from the OpenAPI spec (T-A4) | TypeScript, no runtime deps |
| `packages/argentum-python/` | `argentum` — the same three shapes from Python, sync and async (T-A4) | Python 3.9+, httpx |
| `packages/openapi-tools/` | Everything generated from `apps/backend/openapi/v1.yaml`: Postman, the Python types, the 3.1 validity check, the quickstart-example drift check (`make openapi`) | Node scripts |
| `packages/design-tokens/` | One token source generating the dashboard's CSS variables and the backend's Go report theme (`make tokens`) | JSON + codegen |
| `packages/chat-ui/`     | **Not extracted, deliberately** (T-21). The widget has its own small Preact UI instead; the trade, its cost, and the two events that should trigger paying it are in `apps/widget/README.md` | — |

Consolidated from three separate repos in `T-00b`, with history preserved via
`git subtree`. One commit per feature; deploys stay independent per app.

## How to navigate

**Start here if you are a human:**

1. [`research/01-product-overview.md`](research/01-product-overview.md) — what
   the product does today, capability by capability.
2. [`coverage/feature-coverage.md`](coverage/feature-coverage.md) — the status
   matrix: what is shipped, partial, or stubbed.
3. [`plan/00-sprint-overview.md`](plan/00-sprint-overview.md) — the current
   8-week plan and why it is sequenced that way.

**Start here if you are an AI agent:**

1. [`AGENTS.md`](AGENTS.md) — the working contract. Read this first, every time.
2. [`agents/workspace-context.md`](agents/workspace-context.md) — repo map and
   invariants you must not break.
3. [`plan/01-tickets.md`](plan/01-tickets.md) — the executable task units.

## Directory map

```
docs/
├── README.md                     ← you are here
├── AGENTS.md                     ← agent working contract (read first)
├── research/
│   ├── 01-product-overview.md    Product + capability inventory
│   ├── 02-architecture.md        System map, data flow, invariants
│   └── 03-gap-analysis.md        Gaps, risks, and the agent-native thesis
├── coverage/
│   ├── feature-coverage.md       Feature status matrix
│   ├── test-coverage.md          Measured test state + CI gaps
│   ├── api-surface.md            Endpoint + tool inventory
│   ├── delivery-log.md           What has been shipped, chronologically
│   ├── eval-baseline.md          Agent answer quality: the number to beat
│   ├── environment-notes.md      T-00 smoke test + local environment findings
│   ├── migration-notes.md        T-00b migration record + remaining steps
│   ├── design-tokens.md          T-R1 one token source → dashboard CSS + Go theme
│   ├── report-rendering.md       T-R2 PDF renderer v2 record
│   ├── report-charts.md          T-R3 chart images, palette and CVD gate
│   ├── report-deck.md            T-R4 PPTX deck renderer record
│   ├── rbac.md                   T-04 route policy, team invites, account lifecycle
│   ├── agent-audit.md            T-05 agent_actions log, redaction, attribution
│   ├── credit-enforcement.md     T-03 budget check, the starting grant, BYO-key exemption
│   ├── api-keys.md               T-13 scoped machine credentials, /v1 auth, scope gate
│   ├── api-foundation.md         T-A1 /v1 envelope, request ids, idempotency, limits
│   ├── api-reports.md            T-A2 both report doors, documents, signed callbacks
│   ├── api-chat.md               T-A3 SSE + sync chat, the event schema, thread reads
│   ├── api-contract.md           T-A4 OpenAPI 3.1, the SDKs, the quickstart
│   ├── api-observability.md      T-A5 per-key request record, /v1/usage, /metrics auth
│   ├── generated-types.md        T-02b Go structs → TS types, and the job that diffs them
│   ├── agent-roster.md           T-S1→T-S5 the tenant's own agents, per channel and on /v1
│   ├── business-context.md       T-B1→T-B4 what business the agent works for
│   ├── report-branding.md        T-R5 tenant logo, accent, contrast floor, preview
│   ├── metric-registry.md        T-06/T-07 defined metrics, list_metrics, query_metric
│   ├── watchers.md               T-08 the eval loop, breach → turn → delivery
│   ├── watchers-ui.md            T-09 the watcher surfaces, and who may press what
│   ├── action-framework.md       T-10→T-12b propose → approve → execute once
│   ├── mcp-server.md             T-14 Argentum's tools over MCP, for someone else's agent
│   ├── mcp-source.md             T-M1→T-M4 the tenant's MCP server as a tool source
│   ├── outbound-webhooks.md      T-15 subscriptions, signing, auto-disable
│   ├── observability.md          T-17 exposition, queue depth, spans, the waterfall
│   ├── guardrail-overreach.md    T-07b PII policy, the leak guard, false positives
│   ├── launch-hygiene.md         T-18 what shipped, what the landing page may claim
│   ├── slack-channel.md          The Slack channel, threading, dedupe, watcher delivery
│   ├── report-video.md           T-V1→T-V3 the spec as a video, and the service that draws it
│   ├── docs-site.md              The published quickstart, spec and collection
│   ├── gelael-pilot.md           The first integration outside this repo, and what it found
│   ├── embed-auth.md             T-19 embed keys, HMAC identity, session tokens
│   ├── widget.md                 T-20→T-23 the widget channel, client, docs and config
│   ├── pdf-knowledge.md          T-P1→T-P13 a PDF that is a source, and its number
│   └── live-gate-backlog.md      Every acceptance item owed that code cannot close
├── plan/
│   ├── 00-sprint-overview.md     8-week sprint: goal, scope, non-goals
│   ├── 01-tickets.md             Ticket-level, agent-executable units
│   ├── 02-agent-quality-roadmap.md      T-Q1→T-Q9 smarter and more reliable
│   ├── 03-security-hardening-roadmap.md T-H1→T-H15 what a review finds first
│   ├── 04-native-dashboards-roadmap.md  T-D1→T-D21 replacing Metabase
│   ├── 05-next-steps-and-dashboard-revision.md
│   │                             T-Q10/T-U13 suggested next steps; T-D22/T-D23 revising a dashboard
│   ├── 06-pdf-knowledge-roadmap.md        T-P1→T-P13 a PDF the agent can query, not quote
│   ├── 07-agentic-skills-roadmap.md       T-K1→T-K10 a procedure the tenant writes down (planned, unscheduled)
│   └── backlog.md                Deferred work with rationale
└── agents/
    ├── workspace-context.md      Repo map, invariants, danger zones
    ├── conventions.md            Code, comment, commit conventions
    ├── verification.md           How to prove a change works
    ├── task-template.md          The unit-of-work format
    └── playbooks/
        ├── add-agent-tool.md
        ├── add-migration.md
        └── add-channel.md
```

## Freshness

Written 2026-07-26 against these states, **captured before the `T-00b` monorepo
consolidation**. These are the pre-migration repos, retained because every finding
in `research/` and `coverage/` was verified against exactly these commits:

| Source repo (pre-monorepo) | Now at            | Branch | HEAD      | Subject                                          |
| -------------------------- | ----------------- | ------ | --------- | ------------------------------------------------ |
| `argentum`                 | `apps/backend/`   | main   | `3891579` | fix: stop semantic injection guardrail blocking follow-ups |
| `argentum-dashboard`       | `apps/dashboard/` | main   | `0e51718` | Add tabbed usage analytics UI                     |
| `argentum-landing`         | `apps/landing/`   | main   | `a1ee7c0` | style: rework theme to red/rose palette           |

After `T-00b`, all three histories live in the single monorepo — `git log --follow`
across the subtree boundary still reaches them.

When these move substantially, re-verify `coverage/` before trusting it.
