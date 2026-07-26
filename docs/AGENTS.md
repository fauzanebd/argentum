# Agent Working Contract — Argentum Workspace

Read this before touching any file in this workspace. It is the shortest path
from "task assigned" to "change merged without breaking a tenant".

## 1. Orientation (always, before editing)

1. Read [`agents/workspace-context.md`](agents/workspace-context.md) — repo map,
   invariants, danger zones.
2. Identify **which app** the change belongs to under `apps/`. This is one
   monorepo, so a change spanning backend and frontend is **one commit** — make it
   atomic. Deploys are still per-app, so a frontend change must not assume a
   backend endpoint that has not been released yet.
3. Find the ticket in [`plan/01-tickets.md`](plan/01-tickets.md). If there is no
   ticket, write one using [`agents/task-template.md`](agents/task-template.md)
   before writing code.
4. Check [`agents/conventions.md`](agents/conventions.md) for the local idiom.
   This codebase has a strong, consistent style — match it rather than importing
   your defaults.

## 2. Hard rules

These are not preferences. Violating them breaks tenant isolation, billing, or
security.

| Rule                                                                                                  | Why                                                                            |
| ----------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------ |
| **Never** run a query against a tenant database outside `Conn.ExecuteReadOnly`.                       | Read-only transaction + statement timeout is the only thing protecting tenants. |
| **Never** log, return, or persist a decrypted tenant DSN.                                             | DSNs are AES-256-GCM encrypted at rest; plaintext must not escape the resolver. |
| **Never** resolve a tenant resource without `tenantctx.CompanyID(ctx)` scoping the lookup.            | Cross-tenant data leak is the single worst failure mode.                        |
| **Never** write a migration that is not forward-compatible.                                           | `cmd/api` self-applies migrations on boot during rolling deploys.               |
| **Never** edit `apps/backend/migrations/control/*.sql` that already exists. Add a new numbered pair.   | Applied migrations are immutable; editing them desyncs environments.            |
| **Never** run `migrations/demo_tenant/` against anything but the local demo container.                 | It creates tables. Tenant databases are never migrated by Argentum.             |
| **Never** hand-edit `packages/api-types/`.                                                            | It is generated from Go structs. Edit the Go struct and run `make types`.        |
| **Never** change `module github.com/fauzanebd/argentum` in `apps/backend/go.mod`.                     | Kept deliberately mismatched with the directory so the monorepo migration needed zero import rewrites. |
| **Never** commit or push unless explicitly asked.                                                     | Repo owner controls history.                                                    |
| **Never** add a secret to a committed file. `.env` is gitignored; `helm/` uses Bitwarden secrets.      | Leaked LLM keys are billable by strangers.                                      |

## 3. Definition of done

A task is done when **all** of these hold. Not when the code compiles.

- [ ] Code matches the conventions in `agents/conventions.md`.
- [ ] The verification gate for this change type passed —
      see [`agents/verification.md`](agents/verification.md). Paste the actual
      command output; do not assert success from inspection.
- [ ] `go build ./...` and `go vet ./...` clean (backend), or
      `pnpm build` clean (frontend).
- [ ] New behaviour has a test, or the ticket states explicitly why it cannot.
- [ ] Docs updated when the change alters the public surface:
      `apps/backend/docs/` for API changes, `docs/coverage/feature-coverage.md`
      for capability changes.
- [ ] The ticket's own acceptance criteria, quoted back with evidence.

## 4. Reporting back

Report in this shape. Terse. No narration of what you tried.

```
DONE  T-07 watchers domain + migration
FILES apps/backend/internal/domain/watcher.go,
      apps/backend/internal/adapters/postgres/watcher_repo.go,
      apps/backend/migrations/control/023_watchers.{up,down}.sql
GATE  go test ./internal/... → ok (3 new tests in internal/domain)
      migrate up/down round-trip against local postgres → clean
NOTES CountByCompany intentionally unimplemented; not needed until T-09 UI.
RISK  none — new tables only, no existing table touched.
```

If blocked, report the blocker and what you *did* finish. Do not invent a
workaround that changes the ticket's scope.

## 5. When you disagree with the ticket

Say so in one or two sentences, then build the ticket as written under the
stated assumption. Scope decisions belong to the repo owner. Exception: if
following the ticket would violate a hard rule in section 2, stop and report.

## 6. Parallelism

Multiple agents can work this workspace at once, but:

- **One agent per app per task.** Two agents editing
  `apps/backend/internal/app/` simultaneously will conflict. A monorepo removes
  the repo boundary, not the file boundary.
- **Migrations are serialized.** Only one agent may claim the next migration
  number. Check `ls apps/backend/migrations/control/ | tail -3` before claiming.
- **`packages/` changes fan out.** Editing `packages/chat-ui` affects both the
  dashboard and the widget. Build both before reporting done.
- **Ticket dependencies in `plan/01-tickets.md` are real.** `T-11` cannot start
  before `T-10` lands, because it needs the action registry to exist.
