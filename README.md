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
npm run dev
```

The dev server starts on `:5173` and proxies `/api`, `/webhook`, `/metabase`
to the Go backend on `:8080` (configurable in `vite.config.ts`).

The backend must be running with at least:

- `ARGENTUM_JWT_SECRET=<32+ chars>`
- `ARGENTUM_DSN_KEY=<64 hex chars>` (32 bytes for AES-256-GCM)
- A Postgres URL pointed at the control DB (auto-migrates on boot)

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
