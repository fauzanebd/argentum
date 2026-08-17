# Playbook: Add an Agent Tool

The most common extension in this codebase. **Ten tools exist** (2026-08-17);
follow their shape exactly. Reference implementation:
`internal/tools/run_sql.go` (simple), `internal/tools/schedule_task.go` (calls
into a service), and `internal/tools/create_dashboard.go` (a service call whose
result the model has to act on).

**Time:** 0.5–1.5d depending on whether it needs new persistence.
**Paths** are relative to `apps/backend/`.

---

## Step 1 — Decide if it should be a tool at all

A tool is right when **the agent decides** whether to invoke it mid-conversation.

Not a tool when:
- It should always run → put it in `ChatRunner` as context injection (see
  `withSourcesContext`).
- The user triggers it explicitly → an HTTP endpoint plus a UI button.
- It writes to a customer system → it belongs in the **action framework** (T-10),
  behind approval. Not a bare tool.

## Step 2 — Write the tool

`internal/tools/<name>.go`:

```go
package tools

// <Name>Tool <one line on what the agent uses it for>.
type MyThingTool struct {
    // Dependencies only. NEVER tenant state — the tool instance is shared
    // across every company and constructed once at worker boot.
    repo     domain.MyThingRepository
    recorder UsageRecorder
}

func NewMyThingTool(repo domain.MyThingRepository, recorder UsageRecorder) *MyThingTool {
    if recorder == nil {
        recorder = nopRecorder{}
    }
    return &MyThingTool{repo: repo, recorder: recorder}
}

func (t *MyThingTool) Name() string { return "my_thing" }

// Description is prompt engineering — the LLM reads this to decide whether to
// call the tool. State WHEN to use it and WHAT it returns.
func (t *MyThingTool) Description() string {
    return "Do the thing for ONE source. Pass source_id when the company has " +
        "more than one. Returns id and status."
}

func (t *MyThingTool) Parameters() map[string]interfaces.ParameterSpec {
    return map[string]interfaces.ParameterSpec{
        "thing": {
            Type:        "string",
            Description: "What to do. Be specific — this text reaches the user.",
            Required:    true,
        },
        "source_id": {
            Type:        "string",
            Description: "Data source id. Required when more than one source exists.",
            Required:    false,
        },
    }
}

func (t *MyThingTool) Run(ctx context.Context, input string) (string, error) {
    return t.Execute(ctx, input)
}

func (t *MyThingTool) Execute(ctx context.Context, args string) (string, error) {
    var params struct {
        Thing    string `json:"thing"`
        SourceID string `json:"source_id"`
    }
    if err := json.Unmarshal([]byte(args), &params); err != nil {
        // LLMs send imperfect JSON. Degrade to treating the raw input as the
        // primary argument rather than failing the turn.
        params.Thing = args
    }
    if params.Thing == "" {
        return "", fmt.Errorf("thing parameter is required")
    }

    companyID := tenantctx.CompanyID(ctx)
    if companyID == "" {
        return "", fmt.Errorf("no tenant in context: cannot resolve my_thing")
    }

    source, err := ResolveSource(ctx, t.repo, companyID, params.SourceID)
    if err != nil {
        return "", err
    }

    // ... do the work ...

    t.recorder.RecordMyThing(ctx, companyID, tenantctx.ThreadID(ctx))

    out, _ := json.Marshal(map[string]interface{}{
        "source_id": source.ID,
        "db_type":   source.DBType,   // so the agent knows the dialect next turn
        "status":    "ok",
        "id":        result.ID,
    })
    return string(out), nil
}
```

### Non-negotiables

1. **Stateless.** Tenant identity comes from `ctx` via `tenantctx`. A tool holding
   `companyID` in a field serves the wrong tenant under concurrency.
2. **Tolerate malformed JSON.** Fall back to treating raw input as the main
   argument.
3. **Reject an empty company id explicitly.** Never proceed without a tenant.
4. **Return JSON containing what the agent needs next** — `db_type`, `source_id`,
   and a `note` field when a result is partial. The tool teaches the agent in-band.
5. **Meter it** if it costs money.
6. **Use `ResolveSource`** (`internal/tools/source_resolve.go`) for source
   resolution. Do not reimplement the 0/1/many logic.
7. **A caller mistake is a *result*, not a Go error.** Measured twice: a
   `query_metric` window carrying one bound returned an error, and deepseek
   answered it by re-sending the identical call **seven times** in one turn
   before the budget stopped it — narrating the correction it never made. Return
   a normal JSON result that says what is wrong and what would be right
   (`unknownKey` and `halfWindow` are the pattern), with `row_count: 0` so the
   fabrication check cannot ground a figure on it. Reserve `error` for things
   the model cannot fix by calling differently.
8. **Return `row_count`.** `guardrails.CheckFabrication` grounds the reply on
   `TurnEvidence.DataRows`; a data tool that omits it gets every answer built on
   it suppressed.
9. **Say when a correct result is empty.** "Matched no rows" is not an error and
   is not a zero — both `query_metric` and `create_dashboard` shipped answering
   one as the other, and both times the model reported a confident number beside
   nothing. Put the covered window in the note.

## Step 3 — Extend `UsageRecorder` if the tool costs money

In `internal/tools/run_sql.go` (where the interface lives — see
`../conventions.md` on the import cycle):

```go
type UsageRecorder interface {
    // ... existing
    RecordMyThing(ctx context.Context, companyID, threadID string)
}
```

Then implement on `*app.UsageService`, add a `nopRecorder` method, add a
`UsageEventType` constant in `internal/domain/usage.go`, add a price to
`Pricing`/`DefaultPricing`, and — if the dashboard should show it — handle the new
event type in the usage aggregation SQL.

**Adding the interface method without the `nopRecorder` method breaks the build.
Do both.**

## Step 4 — Register it

**`internal/tools/registry.go`, in `Registry()`** — one construction site, not
`cmd/worker/main.go` (which is where this playbook pointed until 2026-08-17).
Add the dependency to `RegistryDeps` and the constructor to the `ts` slice:

```go
ts := []interfaces.Tool{
    NewListSourcesTool(d.Connections),
    schema,
    // ...
    NewMyThingTool(d.MyThing, d.Usage),
}
```

**Register unconditionally, and make a nil dependency legal.** `Metrics`,
`Actions` and `Dashboards` all do this: the tool registers with a nil service and
reports *"not configured"* if executed. The reason is that the API builds this
same registry **name-only** to serve the agent allowlist checkboxes and the
template vocabulary — a tool that registers only when its service is wired is a
capability no admin can ever tick.

The one real exception is `generate_document` with `Docs == nil`: object storage
absent means the tool would be registered and broken, so it is left out.
`Renders` shows the third case — a dependency that changes a tool's *vocabulary*
(the `mp4` enum value) rather than its behaviour, which must not be offered when
nothing can produce it.

The worker wraps what comes back in the budget guard and the audit recorder
(`T-16`, `T-05`); `cmd/mcp` wraps the same instance. **Do not wrap inside the
tool** — a second decorator chain is a second place for the audit rule to be
wrong.

**If the tool reads tenant data, go through `ResolveSource`.** That is the choke
point that fills in `source_id` on a one-source company and enforces the agent's
source allowlist (`T-S2`). `create_dashboard` skipped it, and the 2026-08-17
live gate found it refusing a call that omitted a field the product does not
require.

## Step 5 — Tell the agent it exists

**`internal/bootstrap/system_prompt.go`** — `SystemPromptForTurn()` composes the
catalog **per turn** from the tools that turn actually holds. There is no
constant listing every tool: one existed, it named all nine while `filterTools`
handed the model a subset, and a Sales agent was told it could write PDFs it had
no tool for.

Add the tool's line to the catalog source, and a numbered guideline if there are
ordering or preference rules — e.g. "prefer `query_metric` over `run_sql` when a
metric covers the question". Both are composed from the tool list, so a
guideline that mentions a tool the turn does not hold must not appear.

```
- my_thing: Do the thing for one source. Pass source_id when more than one
  source is registered. Returns id and status.
```

**A tool the prompt does not mention gets called rarely and badly.** This step is
not optional.

## Step 5b — Give existing agents access

A new tool reaches **no existing agent**: `agents.allowed_tools` is an explicit
list, written when the agent was created. Two edits, both required:

- `config/agent_templates.yaml` — add it to `suggested_tools` on the cards where
  it belongs, so new agents get it pre-ticked. A name no registry knows fails
  the golden test at build time, which is the point.
- A backfill migration for the agents that already exist — copy `043`/`044`,
  and narrow it the way those do rather than granting to everything.

## Step 6 — Test

Unit (`internal/tools/my_thing_test.go`):
- Valid JSON args → expected result
- Malformed JSON → graceful fallback
- Missing required param → clear error
- Empty company id → rejected
- `source_id` resolution: 0 sources, 1 source, many sources, explicit id
- Usage event recorded

Live:
```bash
go run ./cmd/worker
# ask a question in the dashboard that should trigger it
# tail the worker log for the tool name
```

Then: `make eval` — confirm no regression. A new tool changes the agent's decision
surface for **every** question, not just the ones it is meant for. This is the step
people skip and regret.

## Step 7 — Document

- `apps/backend/docs/` — tool contract: params, return shape, errors
- `docs/coverage/feature-coverage.md` — new row under Agent capability
- `docs/coverage/api-surface.md` — new row in the agent tools table

---

## Gate

- [ ] `go test ./internal/tools/... -race -v` green, output pasted
- [ ] `go build ./...` and `go vet ./...` clean
- [ ] Live: tool called when it should be — worker log pasted
- [ ] Live: **not** called on an unrelated question
- [ ] `make eval` pass rate ≥ baseline, both numbers stated
- [ ] Usage event visible in `/api/usage/summary`
- [ ] Docs updated

## Common mistakes

| Mistake | Consequence |
| ------- | ----------- |
| Storing tenant state on the tool struct | Cross-tenant data leak under concurrency |
| Vague `Description()` | Agent never calls it, or calls it constantly |
| Registering but not mentioning it in the prompt | Tool is effectively invisible |
| Returning bare text instead of JSON | Agent cannot chain to the next step |
| Omitting `db_type` from the result | Agent writes SQL in the wrong dialect on the next turn |
| Skipping the eval run | Silent regression on unrelated questions |
| Erroring on malformed JSON | Turn fails on an LLM formatting slip |
