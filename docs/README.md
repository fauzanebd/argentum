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
dashboard, WhatsApp, Discord, or Lark. The agent introspects the schema, writes
and runs read-only SQL, builds Metabase cards and dashboards, generates
downloadable documents, schedules recurring reports, and replies in the user's
language.

## Monorepo layout

| Path                    | What it is                                | Stack                          |
| ----------------------- | ----------------------------------------- | ------------------------------ |
| `apps/backend/`         | API, worker, Discord gateway              | Go 1.26, Gin, asynq, Postgres  |
| `apps/dashboard/`       | Customer-facing web app                   | React 18, Vite, TanStack       |
| `apps/landing/`         | Marketing site                            | React 18, Vite, Tailwind       |
| `apps/widget/`          | **Planned** (T-21) — embeddable chat widget for customers' own internal sites | Preact, iframe, npm + CDN |
| `packages/api-types/`   | **Planned** (T-02b) — TS types generated from Go structs | TypeScript      |
| `packages/argentum-node/` | `@argentum/sdk` — the public API from Node, types generated from the OpenAPI spec (T-A4) | TypeScript, no runtime deps |
| `packages/argentum-python/` | `argentum` — the same three shapes from Python, sync and async (T-A4) | Python 3.9+, httpx |
| `packages/openapi-tools/` | Everything generated from `apps/backend/openapi/v1.yaml`: Postman, the Python types, the 3.1 validity check, the quickstart-example drift check (`make openapi`) | Node scripts |
| `packages/design-tokens/` | One token source generating the dashboard's CSS variables and the backend's Go report theme (`make tokens`) | JSON + codegen |
| `packages/chat-ui/`     | **Planned** (T-21) — chat components shared by dashboard + widget | Preact/React |

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
│   ├── agent-roster.md           T-S1→T-S5 the tenant's own agents, per channel and on /v1
│   ├── business-context.md       T-B1→T-B4 what business the agent works for
│   └── report-branding.md        T-R5 tenant logo, accent, contrast floor, preview
├── plan/
│   ├── 00-sprint-overview.md     8-week sprint: goal, scope, non-goals
│   ├── 01-tickets.md             Ticket-level, agent-executable units
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
