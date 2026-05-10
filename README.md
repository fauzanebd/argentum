# Argentum Dashboard

Vite + React + TypeScript frontend for the Argentum agentic analytics platform.

## Stack

- React 18, TypeScript, Vite 5
- TanStack Router + TanStack Query
- Tailwind CSS + shadcn-style primitives (Radix UI)
- React Hook Form + Zod for forms
- Zustand for auth state

## Local development

```bash
npm install
cp .env.example .env.local   # then edit values
npm run dev
```

The dev server starts on `:5173` and proxies `/api`, `/webhook`, `/metabase`
to the backend host in `VITE_API_BASE_URL` (defaults to `http://localhost:8080`).

The backend must be running with at least:

- `ARGENTUM_JWT_SECRET=<32+ chars>`
- `ARGENTUM_DSN_KEY=<64 hex chars>` (32 bytes for AES-256-GCM)
- A Postgres URL pointed at the control DB (auto-migrates on boot)

## Environment variables

Client (`.env.local`, baked into bundle at build time):

| Var | Where | Purpose |
|-----|-------|---------|
| `VITE_API_BASE_URL` | `vite.config.ts` (dev only) | Target host for the dev server proxy. Ignored in prod build. |
| `VITE_WS_BASE_URL`  | `src/features/chat/use-thread-stream.ts` | Full origin for the chat WebSocket (e.g. `ws://localhost:8080`, `wss://argentum-api.gaia.smartsoft.co.id`). Browser connects direct — Cloudflare Pages Functions don't proxy WS. |

Server (Cloudflare Pages → Settings → Environment variables, Production + Preview):

| Var | Where | Purpose |
|-----|-------|---------|
| `UPSTREAM_URL` | `functions/api/[[path]].ts` | Backend host the Pages Function forwards `/api/*` requests to. |

Keep `VITE_API_BASE_URL`, `VITE_WS_BASE_URL`, and `UPSTREAM_URL` pointed at the same backend host (different protocols).

## Pages

- `/login`, `/signup` — public
- `/onboarding` — first-run DB connection wizard
- `/chat`, `/chat/:threadId` — main conversation UI with WS streaming
- `/threads` — list of threads grouped by phone number
- `/settings` — connections + phone allowlist
- `/usage` — soft metering summary

## Build

```bash
npm run build
```

Outputs static assets into `dist/`.
