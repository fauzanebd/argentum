# Code Conventions

Derived from the existing codebase, not from general Go/React advice. Where this
document and the surrounding code disagree, the surrounding code wins — and this
document is wrong and should be fixed.

## Comments: the house style

This codebase has an unusually high comment quality, and it is the single biggest
reason an AI agent can work in it productively. Match it.

**The rule: comment the "why", never the "what".** Specifically, comment when a
reader would otherwise assume the code is wrong.

Real examples from the repo:

```go
// UsageRecorder is the narrow interface tools depend on for metering. Kept
// in this file (and not in internal/app) to avoid an import cycle since
// internal/app already depends on internal/tools.
```

```go
// Stream content from every iteration immediately. The SDK's default
// filtering (filterIntermediateContent) has a bug: when the agent
// finishes before maxIterations, content from the final iteration is
// captured but never replayed — resulting in empty assistant messages
// after tool calls.
```

```go
// Byte-cap: even within maxRows, very wide columns can blow context.
// Drop rows from the tail until the serialized payload fits under
// maxBytes, then mark truncated. Re-marshalling per shrink is O(n²) on
// row count but maxRows is small (default 100), so this is fine.
```

Each one answers a question a reviewer would otherwise raise. The third even
pre-empts the performance objection *and* explains why it does not matter. That is
the bar.

In YAML, the same principle applies — from `guardrails.yaml`:

```yaml
# KPI "target" / "goal" — bare words match CS prompts ("integer target",
# "the goal is to…"); keep BI phrases only.
```

**Do not write:**
```go
// GetUser gets a user            ← says nothing
// Loop through the rows          ← describes the obvious
// TODO: fix this later           ← there are zero TODOs in this repo. Keep it that way.
```

Every exported type and function has a doc comment starting with its own name.
Non-obvious struct fields are commented inline.

## Go

### Errors
- Wrap with context: `fmt.Errorf("resolve tenant connection: %w", err)`. Lowercase,
  no trailing punctuation, names the operation that failed.
- Domain errors live in `internal/domain/errors.go` and are compared with
  `errors.Is`.
- **Degrade rather than crash for optional subsystems.** The established pattern:
  ```go
  if err := r.hydrateMemory(ctx, agent, p); err != nil {
      logrus.WithError(err).Warn("memory hydration failed; continuing with empty context")
  }
  ```
  Note the message shape: what failed, semicolon, what happens instead.

### Logging
`logrus` with structured fields, JSON formatter in production.

```go
logrus.WithFields(logrus.Fields{
    "company_id": companyID,
    "source_id":  source.ID,
    "db_type":    source.DBType,
}).Info("Executing SQL query")
```

- `company_id` on anything tenant-scoped. Always. It is how production incidents
  get scoped.
- `Debug` for feature-off / skip paths, `Info` for normal flow, `Warn` for degraded
  operation, `Error` for genuine failure.
- **Never log a decrypted DSN, an API key, or a JWT.**

### Constructors and optional wiring
Required dependencies are constructor parameters. Optional features use chainable
`With*` methods returning the receiver:

```go
runner := app.NewChatRunner(threadSvc, messageRepo, /* ... */)
if cfg.LarkEnabled {
    runner = runner.WithLark(larkClient)
}
if cfg.EmbeddingEnabled {
    runner = runner.WithTablePicker(tableEmbRepo, embedCache, cfg.EmbeddingTopK)
}
```

Constructors normalize bad input rather than erroring on it:
```go
if historyLimit <= 0 { historyLimit = 20 }
if topK <= 0 { topK = 8 }
```

### Interfaces
Declared where they are **consumed**, narrow, often one or two methods:

```go
// ScheduledRunMarker is the narrow contract ChatRunner uses to close out
// a scheduled_task_runs row when the agent finishes (or errors).
type ScheduledRunMarker interface {
    MarkRunResult(ctx context.Context, runID, assistantMsgID string, runErr error)
}
```

Provide a nop implementation when the dependency is optional — see `nopRecorder`
in `internal/tools/run_sql.go`. This keeps nil-checks out of the hot path.

### Context
- `ctx` is always the first parameter.
- Tenant identity travels in `ctx` via `tenantctx`, never in struct fields.
- Multi-tenancy setup happens once, at the top of the entry point:
  ```go
  ctx = tenantctx.WithCompanyID(ctx, p.CompanyID)
  ctx = tenantctx.WithThreadID(ctx, p.ThreadID)
  ctx = multitenancy.WithOrgID(ctx, p.CompanyID)
  ctx = memory.WithConversationID(ctx, p.ThreadID)
  ```

### Config
Every setting: a struct field, a `getEnv`/`getEnvAsInt` call with a default, and —
if required — a check in `Validate()`. Fallback chains get an `Effective*()`
accessor; call that, never the raw field.

## SQL and migrations

- Numbered, both directions: `NNN_snake_case.up.sql` and `.down.sql`. Sprint 1
  requires both even though 001–014 lack down files.
- Forward-compatible only — see `workspace-context.md` §6.
- Every tenant-scoped query includes `company_id` in the `WHERE` clause. **No
  exceptions.** This is the isolation boundary.
- Parameterized always. String-interpolated SQL is never acceptable, including in
  metric templates (T-06 binds `{{from}}`/`{{to}}` as parameters).
- One repository file per aggregate in `adapters/postgres/`.

## Agent tools

A tool implements `Name()`, `Description()`, `Parameters()`, `Run()`, `Execute()`
(`Run` delegates to `Execute`). Rules learned from the existing seven:

1. **Descriptions are prompt engineering.** The LLM reads them. State when to use
   the tool and what it returns:
   > "Execute a read-only SQL query against ONE connected analytics database and
   > return results. Pass source_id to pick which database when the company has
   > more than one. Only SELECT queries are allowed."
2. **Tolerate malformed arguments.** `run_sql` falls back to treating raw input as
   the SQL string when JSON parsing fails. LLMs send imperfect JSON.
3. **Resolve tenant from `ctx`, fail loudly if absent:**
   ```go
   if companyID == "" {
       return "", fmt.Errorf("no tenant in context: cannot resolve database connection")
   }
   ```
4. **Return JSON with everything the agent needs next.** Include `db_type` so the
   agent knows the dialect, `source_id` so it can stay consistent, and a `note`
   field explaining what to do when a result is truncated. The tool teaches the
   agent, in-band.
5. **Meter it** through `UsageRecorder` if it costs money.
6. **Register it** in `cmd/worker/main.go`, and describe it in
   `buildSystemPrompt()`. A tool the prompt doesn't mention gets called rarely and
   badly.

## Frontend

- Feature-first: `src/features/<feature>/` holds pages and sub-components.
  Shared primitives only in `src/components/ui/`.
- TanStack Query for all server state. No `useEffect` fetching.
- Zustand for auth only. Everything else is either Query state or local state.
- React Hook Form + Zod for every form.
- Tailwind utilities; `cn()` from `src/lib/utils.ts` for conditional classes.
- shadcn-style Radix primitives — extend the existing ones instead of adding a
  component library.
- **Types mirroring backend DTOs are generated, not written.** Import them from
  `@argentum/api-types`; if one is missing or wrong, fix the Go struct and run
  `make types` (`T-02b`). A feature-local `types.ts` is now only for shapes that
  never cross the wire — form state, view models — and a hand-written mirror of a
  Go struct is a review finding.

## Commits

Recent history uses Conventional Commits; earlier history does not. **Follow the
recent style.**

```
feat: per-thread / per-channel / per-user usage audit endpoints
fix: stop semantic injection guardrail blocking benign follow-ups
perf: cut Anthropic input tokens via prompt caching + schema filtering
refactor: enhance schema retrieval and chat runner functionality
chore: default LLM to deepseek/deepseek-v3.2 via OpenRouter
ci: remove Cleanup Old Images job
style: rework theme to red/rose palette, smooth-scroll nav, slim footer
```

Subject ≤ 72 chars, imperative mood, no trailing period. Body only when the "why"
isn't obvious from the subject.

**Do not commit or push unless explicitly asked.**

## Things this codebase deliberately does not do

Do not introduce these without discussion — their absence is a choice:

- No ORM. `database/sql` with hand-written SQL.
- No dependency-injection framework. Explicit wiring in `bootstrap.go` / `main.go`.
- No `panic` in request or task paths. Errors are returned.
- No global mutable state. Everything is constructor-injected.
- No `interface{}`/`any` in domain types. Only at JSON boundaries.
- No TODO comments. There are currently zero in the repo — file a ticket instead.
