# Watchers UI — T-09 record

Track: [`../plan/01-tickets.md`](../plan/01-tickets.md), *Week 3 — It tells you
first*. The dashboard half of **the wedge**: the surface where an admin defines a
watcher, checks it against recent history with a dry-run, enables it, and reads
back what it has done. The backend it drives is [`watchers.md`](watchers.md)
(T-08); this record is only the frontend.

| Ticket | What | Size | State |
| ------ | ---- | ---- | ----- |
| `T-09` | Watchers UI: list, create/edit form, dry-run-gated enable, event history sheet, sidebar nav | 2d | **gated live 2026-08-02** — the non-admin disabled-with-tooltip view is the one item not photographed |

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

## 5. The live gate — run 2026-08-02

Dashboard dev server on :5173 against the gate API on :8080, driven through
headless Chrome over CDP (the recipe in `../agents/` — a synthetic `click()` does
not open a Radix select, so every click is a real `Input.dispatchMouseEvent`).
Screenshots in the session scratchpad; the sequence below is what they show.

**Create.** `New watcher` opens the form with the metric picker populated from
`/api/metrics` (`Average order value`, `Order count`, `Revenue`,
`Revenue (Dec 2024 fixture)`), comparators reading as sentences
(`is greater than`, `rises by more than (%)`, `returns no data`), a cron preset
that writes `0 9 * * *` into the expression field and explains it underneath
(`At 09:00`), and `Deliver to` with **Dashboard thread checked by default**.
Saving produced a watcher badged **off** with the toast *"Watcher created — Run a
dry-run to check it against recent history, then enable it."*

**The dry-run gate holds, in the UI as well as the API.** On the new row the
`Enable` button is rendered **disabled**. Clicking `Dry-run` returned, in a panel
above the row and *before* the toggle unlocked:

> Would have fired 14 times in the last 14 periods.
> You can enable this watcher now.

`Enable` then became clickable; clicking it flipped the badge to `enabled` and
the button to `Pause`. Read as booleans: `enableDisabled: true` → dry-run →
`enableDisabled: false`.

**The events sheet distinguishes the three outcomes.** Opened on the watcher that
fired during `T-08`'s gate, the top entry is a red **breached** badge with
`value 3,863,405,700` and **`Dashboard thread: delivered`**; every entry below it
is a grey **suppressed / cooldown** with the same value; a second watcher's sheet
shows silent evaluations. `Open thread` links to the conversation the briefing
landed in.

#### What the gate found

- **A one-line-per-evaluation history is mostly noise, and the cap hides the
  event that matters.** The sheet shows "the last 50 evaluations". A watcher on a
  per-minute cron inside a 720-minute cooldown produces 50 identical
  `suppressed / cooldown` rows in under an hour, and the delivery that started
  the cooldown scrolls out of the window — so the screen that exists to show what
  a watcher *did* showed nothing but what it declined to do. Reproduced exactly:
  the fire at 08:30 was invisible by 09:20, and the gate had to drop the cooldown
  to zero and let it deliver again to photograph a delivered row. Collapsing
  consecutive suppressions into one line ("suppressed 47× since 08:31"), or a
  filter, would put the deliveries back on screen. The 50-row cap is not the
  problem; a row per silent evaluation is.

  **Fixed 2026-08-03, and it needed both halves.** Consecutive suppressed rows
  collapse into one expandable line — *"47 suppressed · cooldown · 2 hours ago –
  5 minutes ago"* — which is the display half. Collapsing alone would not have
  brought the delivery back, though: the window is the last 50 *rows*, so past an
  hour of per-minute suppression the fire is not off screen, it is not in the
  response. So `GET /watchers/:id/events` takes `?fired=true` (breached and not
  suppressed — the same condition the evaluator writes a delivery for), and the
  sheet has an **All evaluations / Fired only** toggle that changes the query
  rather than filtering what came back.

  The default stays **all evaluations**, deliberately: *"why did it not message
  me?"* is answered by the suppressed rows, and that question is at least as
  common as *"what did it send?"*. A run of one is not collapsed, because putting
  a single row behind a toggle hides more than it collapses.

  **Not closed:** the photograph, again through the browser this sheet was first
  read in.
- **The default channel is a trap for a scripted click.** `Dashboard thread`
  arrives checked, so a driver that "checks the box" un-checks the only channel
  and `Create watcher` stays disabled with no message explaining why. Noted for
  whoever writes the next browser gate rather than as a product defect — though
  the disabled button never says which requirement is unmet, which is worth a
  tooltip.

**Not covered:** the non-admin view (Enable/Delete disabled with a tooltip).
The member role's refusal is proven at the API (`403` on every watcher write),
but the disabled-with-tooltip rendering was not photographed.

## 4a. Verified after the gate

`pnpm build` clean; `pnpm lint` 0 errors, 6 pre-existing warnings, none in the
watcher files.
