# Playbook: Add an Agent Tool

The most common extension in this codebase. Seven tools exist; follow their shape
exactly. Reference implementation: `internal/tools/run_sql.go` (simple) and
`internal/tools/schedule_task.go` (calls into a service).

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

`cmd/worker/main.go`, in the `agentTools` slice (~line 156):

```go
agentTools := []interfaces.Tool{
    tools.NewListSourcesTool(connRepo),
    getSchemaTool,
    // ...
    tools.NewMyThingTool(myThingRepo, usageSvc),
}
```

Conditional registration when the tool needs optional infrastructure — copy the
`generate_document` pattern:

```go
if storageSvc != nil {
    agentTools = append(agentTools, tools.NewGenerateDocumentTool(...))
}
```

Once T-05 lands, wrap with the audit decorator.

## Step 5 — Tell the agent it exists

`buildSystemPrompt()` in `cmd/worker/main.go`. Add to the tool list:

```
- my_thing: Do the thing for one source. Pass source_id when more than one
  source is registered. Returns id and status.
```

Add a numbered guideline if there are ordering or preference rules — e.g. "prefer
`query_metric` over `run_sql` when a metric covers the question".

**A tool the prompt does not mention gets called rarely and badly.** This step is
not optional.

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
