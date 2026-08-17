# Native dashboards — what was built, and what running it found

`T-H4` step 1, `T-D3`→`T-D7`, `T-D10`, `T-D11`, and the chat embed. The backend
half of the roadmap in [`../plan/04-native-dashboards-roadmap.md`](../plan/04-native-dashboards-roadmap.md),
plus the one surface the roadmap did not carry: a chart drawn inside the chat
transcript, asked for on 2026-08-17.

---

## 1. The live gate, 2026-08-17

Run against the compose stack and `moonshotai/kimi-k2.6`, on a gate tenant
created for the sitting (`Gate Dashboards 0817`, one connection to the demo
warehouse). **Two defects, both found by running it and neither reachable by a
unit test**, because both live in what the *model* does with what the tool
returns.

### Migration 056, up and down against a populated table — pass

Version 55 → 56, then down, then up. The table came back with 12 columns, 3
indexes and all four foreign-key rules exactly as the header comment argues for:

```
  fk dashboards_company_id_fkey                 CASCADE
  fk dashboards_created_by_fkey                 SET NULL
  fk dashboards_source_id_fkey                  RESTRICT
  fk dashboards_thread_id_fkey                  SET NULL
```

`ON DELETE RESTRICT` was proven rather than read: with a dashboard stored
against a connection, deleting that connection is refused by Postgres —

```
pq: update or delete on table "db_connections" violates foreign key constraint
    "dashboards_source_id_fkey" on table "dashboards"
```

The down ran against a populated table, and the up after it left the schema
clean. `GET /api/dashboards` and the three routes beside it registered on boot,
and the DSN key-health line read `total 21, undecryptable 2` — the two rows that
have been unreadable since 10 August, unchanged.

### The turn — one call, and the link the chat embeds

> Show me a dashboard of monthly revenue as a bar chart, plus revenue by sales
> channel as a pie chart.

`list_metrics` → `get_schema` → `run_sql` ×2 → `create_dashboard`. The spec it
wrote is the shape the tool description asks for: two panels, a `period`
date_range filter, and `{{period_from}}` / `{{period_to}}` bound in each panel's
SQL rather than dates baked into it. The reply was a markdown link to
`/dashboards/<uuid>`, which is what the transcript swaps for the panels.

### Defect 1 — `create_dashboard` refused a call that omitted `source_id`

```
ERR Tool execution error error="invalid input: the dashboard needs a source" tool_name=create_dashboard
```

The model had called `run_sql` twice without a `source_id`, because on a
one-source company `ResolveSource` fills it in — that is the choke point *every*
other data tool goes through, and it is where the roster's source allowlist is
enforced (T-S2). The new tool did not call it, so a company with one source got
a refusal for a field the product does not require, and the turn spent an
iteration discovering a rule that does not exist. On a multi-source tenant it
would have cost more, because the retry has no menu to read.

Fixed: the tool resolves through `ResolveSource` like the others, which also
carries the turn's already-resolved source forward (the 2026-08-14 fix for a
retry loop) and applies the agent's allowlist.

### Defect 2 — an empty dashboard, described in confident prose

The agent queried `2020-01-01`→`2025-12-31`, found six months of data, and then
gave the dashboard the default `last_30d` window. The demo warehouse holds
2024-07-01 → 2024-12-31. In 2026 that window matches nothing, so every panel
resolved to zero rows — while the reply said:

> Revenue has trended between roughly **$3.24B** and **$3.86B** per month over
> recent months. The largest channel is **In-Store** (about **$12.73B** total)…

Both halves are true and they contradict each other: the figures came from the
`run_sql` calls, and the dashboard beside them draws nothing. Nothing warned,
because validate-on-save warned only on a panel *error*, and "matched no rows"
is not an error — it is the correct result of the window it was given.

This is the same failure the metric registry recorded from the other side
(`metric-registry.md`: an out-of-coverage window answered `Rp 0`). There the fix
was to make the tool say "this is NOT a zero". Here it is to make the save say
"this panel returned no rows", with the window in the message.

Fixed in two places: `dryRun` warns on a zero-row panel, and `spec.Project` sets
the note on every viz rather than only on a KPI — so the payload says it too,
for anything that is not the browser.

### Re-run, same question, after both fixes

`create_dashboard` was called **once**, and the model told the user what the
warning told it:

> **Note:** The demo dataset contains sales from **July 2024 to December 2024**.
> The default date filter doesn't overlap with this range, so please adjust the
> **Period** filter on the dashboard to a custom range such as **2024-07-01 to
> 2024-12-31** to see the data.

That sentence is the whole point of the second fix. The dashboard is still empty
on its default window — which is honest, because the data really is not there —
and the reader is told why in the same breath rather than left to compare a
blank chart against a table of billions.

---

## 1a. The browser, opened — and the third defect (2026-08-17, later)

The sentence below used to read *"nothing here has been opened in a browser"*.
It was true for about an hour. The first screenshot of a real panel found a
defect the API gate could not have: **an axis that misstated its own scale.**

Monthly revenue on the demo warehouse is in the billions. `3,240,929,900` does
not fit a 48px axis gutter, so three different ticks rendered as the same
clipped `100,000` — a chart whose axis contradicts its own bars, with nothing to
tell the reader which one is lying. Axis ticks now carry their own compact
formatter (`3.2B`) in the viewer's locale, with 56px of gutter; the tooltip
keeps full precision, because that is where somebody goes to read a number
rather than a shape. Two formatters rather than one with a flag: an axis says
how big, a tooltip says how much. Re-read in the browser as 0 / 1B / 2B / 3B /
4B (`apps/dashboard/src/features/dashboards/panel.tsx`).

The dark ramp landed in the same sitting for the same reason — the eight chart
colours had been gated against paper and never against a dark card, where series
2 sat at 1.35:1. That record is in
[`report-charts.md`](report-charts.md) §"The dark ramp".

## 1b. The rest of the browser, run 2026-08-17 (later still)

The three items §2 listed as owed were run in one sitting, on the same tenant
and with no model spend: every panel below was drawn from a dashboard an earlier
gate had already authored. **Three passes and two defects**, and the first is
the same shape as the axis above — the data is right and the rendering is not.

| Item | Outcome |
| ---- | ------- |
| The `/dashboards` list page | **Pass.** Three dashboards, each with its title and a truncated description, each link resolving to its own page |
| The chat embed in a real transcript | **Pass on what it draws.** The `/dashboards/<uuid>` link in the authoring thread is swapped for the live panels, header actions and all — and a defect in *how* it is mounted, below |
| The dark ramp on a real dark card | **Pass.** All eight tokens drawn on `--card` in dark: `#F25C5C`, `#4981CB`, `#EAAA3E`, `#318578`, `#9C7AB4`, `#CACCD1`, `#B9672E`, `#5CA8E0`. Nothing disappears. `#318578` is the dimmest and `#4981CB`/`#5CA8E0` the closest pair, which is worth knowing before a ninth series is ever added |

### Defect 3 — a table panel ignores its own `fmt`

`Top 5 Products by Revenue` renders `20727672550.00`. The panel declares
`"fmt": "currency"`, and the same figure inside the bar chart beside it reads
`3.2B` on the axis and groups in the tooltip.

The mechanism is one line, and it is deliberate as far as it goes —
`spec/project.go:90`, *"A table draws what the query returned, in the order it
returned it"*: `out.Columns, out.Rows = res.Columns, res.Rows`. Every other viz
runs each value through `cell()` and gets a `float64`; the table alone passes the
driver's own values through, and a Postgres `numeric` decodes to a **string**.
The browser then declines to format it, because `useFormatter` returns
`String(v)` for anything that is not a number — correctly, since a table also
holds product names.

So the declared format is honoured by every panel type except the one made of
the numbers somebody reads digit by digit. ~~**Not fixed in the sitting**,
because the fix is a choice rather than an edit: coercing numeric-looking strings
in the browser turns an order id of `0012` into `12`, and coercing server-side
needs the driver's column types carried onto the payload, which no panel
currently has.~~

**Fixed 2026-08-17, server-side, and the objection above is what shaped it.**
`tableRows`/`tableValue` (`spec/project.go`) coerce a table cell only when the
string is a **canonical decimal literal**: an optional minus, no leading zero
unless the value is below one, an optional fractional part, nothing else. That is
how a driver writes a number and not how anybody writes an identifier, so
`20727672550.00` becomes a number and `081234567890`, `00123`, `+6281234567`,
`1e6`, `2024-11-01` and `" 42 "` all travel exactly as they did before — the
padded-order-id case the paragraph above refuses to break, pinned by
`TestProjectTableLeavesIdentifiersAlone`. No column types were needed on the
payload: the shape of the value answers the question the type would have.

**What it does not fix, stated so it is not read as closed.** A table's `fmt` is
one setting for the whole panel and `TableBody` applies it to every numeric
column, so a year column in a currency table reads `2,024`. That is true today
for any column a driver returns as an integer and is not made worse here — it is
a per-column format, which is a spec change and a different ticket.

### Defect 4 — the embed is a block element inside a paragraph — **fixed**

`document.querySelector('section.not-prose').parentElement` is **`P`**. The
markdown renderer swaps an anchor for `<DashboardView>`, and an anchor lives
inside the paragraph react-markdown built for the prose around it, so the whole
dashboard — a `<section>` with a grid and three panels — is mounted as a child of
a `<p>`.

It renders here because React inserts into the DOM directly and the browser
never re-parses it. The cost is latent rather than visible: any path that parses
this markup instead of constructing it splits the `<p>` at the block element, so
a server-rendered transcript would hydrate against a tree that does not match.
The paragraph's own prose spacing also applies to a chart container.

**Fixed and re-read in the same sitting.** A paragraph that is going to contain
a dashboard link renders as a `<div>` carrying the same spacing; every other
paragraph in every other message is still a `<p>`. Re-read in the browser:
`parentElement` is `DIV.my-1.5`, `document.querySelector('p section')` is null,
the three panels still draw, and the four ordinary paragraphs of that same
message are still paragraphs.

**The first version of the fix changed nothing, and why is worth keeping.** It
asked the rendered children whether one of them was the embed —
`child.type === DashboardEmbed`. react-markdown hands `p` the *component* it
will call for the anchor, not what that component returns, so the check saw the
`a` override and never the embed, and the re-read came back `P` exactly as
before. The question has to be asked of the hast node, which carries the href
before anybody decides what to draw for it. **A fix re-proven in the browser is
the only reason this was caught** — it type-checked, linted and looked right.

### And what the panel grid does with a stored layout

Nothing. The spec carries `layout: {x, y, w, h}` per panel — in the dashboard
gated here, panels 1 and 2 are both `y: 0` at `x: 0` and `x: 6`, which is *side
by side* — and `dashboard-view.tsx` draws every non-KPI panel as `col-span-2` in
a three-column grid, so they stack and the third column is permanently empty.
Track F owns the grid and this is not a regression; what is worth writing down is
that the agent is authoring a layout nothing in the product reads.

## 2. What is still owed

~~**The rest of the browser.** One panel grid has now been seen. The
`/dashboards` list page, the chat embed in a real transcript, and the dark ramp
on a real dark card have not.~~ **Run 2026-08-17 — §1b above.**

**`T-D8` and `T-D9`** — the panel cache and the query log — are not built, so
today every open of a dashboard runs every panel against the tenant warehouse.
The caps hold (four concurrent, 15s each, 2 000 rows) but nothing collapses two
viewers opening the same dashboard at once.

**Rule 1 re-run.** The eval set's chart cases now assert `create_dashboard`
rather than the pair, and the chart guidelines were rewritten. Nothing has been
scored since.

---

## 3. Numbers from the sitting

| | |
| --- | --- |
| Model | `moonshotai/kimi-k2.6` |
| Turns | 2 (the same question, before and after the fixes) |
| Dashboards created | 2, both stored, both resolving over HTTP |
| Defects found | 2, both fixed and re-proven in the same sitting |
| Migration | 056 up / down / up against the real control database |
