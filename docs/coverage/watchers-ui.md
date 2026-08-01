# Watchers UI — T-09 record

Track: [`../plan/01-tickets.md`](../plan/01-tickets.md), *Week 3 — It tells you
first*. The dashboard half of **the wedge**: the surface where an admin defines a
watcher, checks it against recent history with a dry-run, enables it, and reads
back what it has done. The backend it drives is [`watchers.md`](watchers.md)
(T-08); this record is only the frontend.

| Ticket | What | Size | State |
| ------ | ---- | ---- | ----- |
| `T-09` | Watchers UI: list, create/edit form, dry-run-gated enable, event history sheet, sidebar nav | 2d | **code complete — `pnpm build` + `pnpm lint` clean; live gate outstanding** |

---

## 1. What ships

| Layer | File |
| ----- | ---- |
| Feature | `src/features/watchers/watchers-page.tsx` — list page, `New watcher`, mounts the form and the events sheet |
| Form | `src/features/watchers/watcher-form.tsx` — create/edit: metric picker, window, comparator, threshold, compare-to, cron (+cronstrue preview), timezone, cooldown, channel multi-select with per-channel ref |
| Row | `src/features/watchers/watcher-row.tsx` — condition summary, schedule, dry-run button + inline result, enable/pause toggle, edit, events, thread, delete |
| Events | `src/features/watchers/watcher-events-sheet.tsx` — last 50 evaluations: breached / ok / suppressed, value·comparison·delta, per-channel delivery badges, thread link |
| Model | `src/features/watchers/watcher-model.ts` — `WatcherDraft`/`DryRunResult` (hand-written; see §3), labels, `conditionSummary`, `hasFreshDryRun`, `watcherToDraft` |
| Nav | `src/components/layout/watchers-nav.tsx` — sidebar entry beside Scheduled Tasks |
| Wiring | `src/components/layout/app-shell.tsx` (nav mount), `src/routes/index.tsx` (`/watchers` protected route) |

## 2. The properties the ticket turns on

- **Enable cannot be reached without a fresh dry-run.** The row's Enable button
  is disabled unless `hasFreshDryRun(watcher.last_dry_run_at)` — the 24h window
  the backend enforces (`enableRequiresDryRunWithin`). Running the Dry-run button
  invalidates the `watchers` query, so `last_dry_run_at` refreshes and the gate
  unlocks in the same interaction. The client check is a UX mirror, not the
  guarantee: the PUT still carries `enabled:true` to a backend that re-checks and
  answers `400` if the dry-run is stale, which surfaces as a toast.
- **The dry-run result is shown before enabling.** A successful dry-run renders
  an inline panel — "Would have fired N times in the last M periods" — straight
  from `DryRunResult.would_have_fired`/`periods_evaluated`, so the admin sees the
  alert's historical noise level before turning it on.
- **Suppressed and delivered events read differently.** The events sheet badges a
  row `breached` (destructive), `suppressed` (outline, with the reason —
  `cooldown` or `credits_exhausted`), or `ok` (secondary), and renders one
  delivery badge per channel coloured by outcome (`delivered`/`failed`/`skipped`).
  A silent non-breach and a suppressed real breach are never conflated.
- **The form never hard-codes the vocabulary.** Grains, comparators, channels and
  compare-to options come from `WatchersResponse`, exactly as the metrics tab
  reads its grains/units off `MetricsResponse`. A comparator added to the backend
  appears in the dropdown without a frontend change; the labels map degrades to
  the raw value for anything it does not yet name.
- **A watcher needs a metric first.** `New watcher` is disabled and a pointer to
  Settings → Metrics is shown when the tenant has no metric — the same guard the
  metrics tab applies against having no database connection.

## 3. Deviations from the ticket, and why

- **`WatcherDraft` and `DryRunResult` are hand-written, not generated.** Both
  mirror types in the Go `app` package (`app.WatcherInput`, `app.DryRunResult`),
  which tygo does not scan — only `domain` structs cross into
  `@argentum/api-types`. They are commented as mirrors and kept field-for-field in
  step with the JSON tags, the same treatment `MetricDraft` gets in the metrics
  tab. The generated `WatchersResponse` types `watchers`/`grains`/`comparators`
  as `any`/`unknown` (cross-package references tygo cannot resolve), so the page
  casts them to the domain types at the query boundary.
- **Dry-run and enable live on the row, not inside the create form.** The backend
  requires a persisted watcher before it can dry-run (the id is in the route), and
  `Create` always returns a disabled watcher regardless of input. So the flow is
  create → dry-run → enable, and the last two are row actions on the saved
  watcher. The ticket's "dry-run is a required step in the form … before the
  Enable toggle unlocks" is honoured by that gate, just located where the id
  exists.
- **Enable/pause re-sends the whole condition.** `PUT /watchers/:id` binds a full
  `WatcherInput`, so `watcherToDraft(watcher, next)` maps the stored row back to
  the body with only `enabled` changed. There is no PATCH for a single field, as
  there is for scheduled tasks.
- **No `?watcherId` deep-link search param.** Scheduled tasks auto-open their runs
  sheet from a query param; nothing links into a specific watcher's events yet, so
  the route takes no search. Additive later if a notification needs to deep-link.

## 4. Verified, and what is not

**Verified locally:**

- `pnpm build` (`tsc -b && vite build`) is clean.
- `pnpm lint` (`tsc -b --noEmit && eslint .`) reports 0 errors; the 6 warnings are
  pre-existing and in unrelated files (`ui/*`, `chat-page`, `onboarding`, `main`).
  No new file adds a warning.

**Outstanding — the live gate.** Needs the dashboard against a running API with
the demo tenant — unavailable here (no Docker), the same reason T-06/T-07/T-08
left their gates open. When it runs, per the ticket:

1. Create a watcher → run a dry-run → confirm Enable is locked until the dry-run
   returns and unlocks, and that the "would have fired N times" panel shows.
2. Screenshot create → dry-run → enable → a fired event with its thread link.
3. Confirm the events sheet renders a suppressed event (cooldown) and a delivered
   event distinguishably.
4. Confirm a non-admin sees Enable/Delete disabled with the explaining tooltip.

Given this project's record — the live half of the gate has found something the
build could not on every ticket so far — expect it to surface at least one thing,
and run it before T-09 is called landed.
