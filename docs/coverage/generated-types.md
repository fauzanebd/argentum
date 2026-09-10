# Generated API types — `T-02b` Record

**Shipped 2026-07-29.** `apps/dashboard/src/features/*/types.ts` hand-mirrored
Go JSON tags and nothing checked that they agreed. They are gone. The
dashboard now compiles against `@argentum/api-types`, generated from the Go
structs by [tygo](https://github.com/gzuidhof/tygo) and committed, and a Go
struct change without a regeneration is a red build.

This closes the last open half of phase 1b's exit criterion — *"a Go struct
rename without `make types` is a red build"*.

---

## Gate 1 — the drift gate, proven by breaking it

`ConversationThread.Title`'s tag renamed `json:"title"` → `json:"headline"`,
nothing else touched.

```
$ make types-check
node packages/api-types/scripts/generate.mjs --check
api-types: 1 file(s) differ from the Go structs: domain.ts
Run 'make types' and commit the result.
make: *** [types-check] Error 1
```

Then what CI actually runs — regenerate, then diff:

```
$ make types && git --no-pager diff --exit-code -- packages/api-types
diff --git a/packages/api-types/src/domain.ts b/packages/api-types/src/domain.ts
@@ -728,7 +728,7 @@ export interface ConversationThread {
   api_user_ref?: string;
-  title: string;
+  headline: string;
   summary?: string;
git diff exit=1
```

And the half that is the actual point — the dashboard stops compiling:

```
$ pnpm exec tsc -b --noEmit
src/components/layout/recent-chats.tsx(91,30): error TS2339: Property 'title' does not exist on type 'ConversationThread'.
src/features/chat/chat-page.tsx(460,19):      error TS2339: Property 'title' does not exist on type 'ConversationThread'.
src/features/chat/threads-page.tsx(66,72):    error TS2339: Property 'title' does not exist on type 'ConversationThread'.
```

Reverted, and green again:

```
$ git checkout apps/backend/internal/domain/thread.go && make types && make types-check
api-types: wrote 5 files to src/ (1 changed)
api-types: 5 generated files are current
$ git --no-pager diff --stat -- packages/api-types   # clean
```

## Gate 2 — the dashboard, with every hand-written type deleted

```
$ pnpm exec tsc -b --noEmit        # dashboard
DASHBOARD_TSC_OK
$ pnpm build
✓ built in 3.78s
$ pnpm lint
✖ 6 problems (0 errors, 6 warnings)   # all pre-existing react-hooks/react-refresh warnings
```

Four files deleted: `features/{chat,dashboard,scheduled-tasks,usage}/types.ts`.
`features/usage/labels.ts` is what survives of the last one — the label maps and
`microToUsd`, which are the dashboard's own business and not a contract.

## Gate 3 — the backend is unchanged in behaviour

```
$ go build ./... && go vet ./... && go test -count=1 ./internal/...
BACKEND_OK
```

---

## What the migration found

Every one of these was live. The first is the one worth the ticket on its own.

### 1. `Thread.channel` claimed two channels; there are five

```ts
channel: "whatsapp" | "dashboard";       // hand-written
channel: Channel;                         // generated — whatsapp|dashboard|discord|lark|api
```

Discord landed in phase 5, Lark after it, `api` with `T-A1`. The dashboard has
been rendering threads whose `channel` its own types said could not exist. Any
`switch` over it was exhaustive against a two-value world and silently fell
through for the other three.

The same type also had no `discord_user_id`, `lark_chat_id`, `lark_thread_key`,
`lark_open_id` or `api_user_ref` — five fields the API has been sending to a
client that declared they were not there.

### 2. Optional fields typed as `| null` are absent, not null

`ScheduledTask.LastRunAt` is a `*time.Time` with `omitempty`: when a task has
never run, the key is **not in the JSON**. The hand-written type said
`string | null`, so `relative(ts: string | null)` was checking for a value the
API has never once sent. Same shape in `TaskRun.finished_at`,
`assistant_msg_id`, and `UsageEvent.message_id` / `model` / `metadata`.

Caught by the compiler the moment the generated types landed:

```
src/features/scheduled-tasks/task-row.tsx(102,30): error TS2345:
  Argument of type 'string | undefined' is not assignable to parameter of type 'string | null'.
```

### 3. Counters declared required are `omitempty`

`UsageEvent.tokens_in`, `tokens_out` and `cache_read_tokens_in` omit at zero.
The usage sheet compared `e.tokens_in > 0` — `undefined > 0`, which is `false`
rather than a crash, so the row simply never rendered its token counts for the
events it was written to describe. Now `(e.tokens_in ?? 0) > 0`.

### 4. `BudgetWarning` was missing `enforced`

`T-A1` added it to `app.BudgetState` to distinguish "the balance is zero" from
"no balance was consulted". The dashboard's copy never grew the field.

### 5. `POST /api/chat` returned a shape no Go type described

The response was assembled as a `gin.H` literal, so the dashboard's
`SendMessageResponse` mirrored nothing — there was no Go declaration for a
generator or a reviewer to compare it against. It is now
`handlers.SendMessageResponse` in `handlers/wire.go`, and the JSON is
byte-identical: `budget_warning` is a nil pointer with `omitempty` exactly where
the map omitted the key.

### 6. `ScheduledTaskRun.Status` was weaker in Go than in TypeScript

Go had three untyped string constants and a `Status string` field; the
dashboard had a proper `"running" | "succeeded" | "failed"` union. The
hand-written type was **more correct than the backend**, so the fix went the
other way: `type ScheduledRunStatus string`, the constants typed, the field
typed. The generated union now matches what the dashboard always believed.

### 7. Four names had forked

`ThreadRow` / `ChannelRow` / `UserRow` / `CreditBalance` in TypeScript were
`ThreadUsageRow` / `ChannelUsageRow` / `UserUsageRow` / `CompanyCredits` in Go,
and `Thread` was `ConversationThread`, `TaskRun` was `ScheduledTaskRun`. The
call sites now use the Go names. A re-export could have hidden the fork behind
an alias; that would have left two vocabularies for one thing, which is how the
drift started.

---

## Decisions taken inside the ticket

### The generator runs against source, and the output is committed

Same shape as the design tokens and the OpenAPI artifacts, for the same reason:
a contract change is legible in the diff of the commit that made it, and CI
regenerates and diffs so a stale copy cannot ship. Three generators now follow
this pattern in this repo; a fourth reviewer will recognise it.

### A file is what decides whether a type crosses the wire

tygo emits every exported type in a package, so "what does the browser see" had
to become a decision someone makes on purpose rather than a consequence of
where a struct happened to be written:

- `handlers/wire.go` — `/api` bodies that are not entities. New file.
- `app/budget_state.go` — the credit position, split out of `credits.go`, which
  keeps `CreditPolicy`, the cache and the provisioning logic *out* of the
  generated TypeScript. Operator configuration is not a wire type.

`internal/domain` is included whole, which is the one place the rule is loose:
the four `*Filter` structs in it are repository arguments, not responses, and
they generate with Go-cased keys because they carry no `json` tags. That is the
tell, and it is the reason they are harmless — a shape with `CursorTime` in it
is visibly not something the API returns.

### Go interfaces are dropped rather than emitted as `any`

23 repository interfaces in `internal/domain` render as
`export type XRepository = any`. `any` is not a description of a value, it is
the absence of type-checking, and exporting one under a familiar name is worse
than exporting nothing. The remaining `any`s — from `map[string]interface{}` —
become `unknown`, which is what the hand-written types already used.

The rewrite runs on emitted code only. `domain/api_key.go` contains the
sentence *"there is no \"any authenticated key\" tier"*, and a blunt replace
would have edited a comment.

### Enums become literal unions where Go's naming allows it

tygo forms a union when every constant is named after its type
(`ChannelWhatsApp` → `Channel`) and falls back to `= string` when they are not
(`UsageEventLLMCall`, `APIReportQueued`, `BudgetOK`). The result is mixed in
strength and uniform in behaviour — a union is assignable everywhere the alias
was — and the alternative was renaming domain constants to suit a TypeScript
generator.

The union is what makes finding #1 impossible to repeat: adding a channel in Go
widens the type, and a dashboard `switch` that does not cover it fails the
build.

### tygo is pinned, and not in `go.mod`

`go run github.com/gzuidhof/tygo@v0.2.21`, pinned in the generator script.
`go get -tool` was tried first and pulled `golang.org/x/crypto`,
`golang.org/x/net` and `golang.org/x/sys` up a version and added 744 lines to
`go.sum` — a production dependency change to install a code generator. Not
worth it for a tool that runs twice a week.

### The `types` CI job carries both toolchains, and the web job carries none

Regeneration needs Go, and the `web` job has no Go installed. So
`packages/api-types` deliberately has **no `build` or `lint` script**: `pnpm -r
build` would otherwise fail there. The dedicated `types` job has Node and Go and
owns the gate; the dashboard's own build is what checks the committed output is
usable.

---

## Modules added since (2026-08-17)

`T-D11`'s frontend half added two packages to `tygo.yaml`, both unbarrelled for
the reason `videoplan` is: `internal/dashboard/spec` → `src/dashboardspec.ts`
and `internal/dashboard` → `src/dashboard.ts`. `Dashboard`, `Panel`, `Filter`
and `Series` are names a shared namespace should not be handed, so they are
imported explicitly by the two components that draw them.

One generator limit surfaced doing it: **tygo renders `15 * time.Second` as
`15 * any`**, which does not compile. `Result` moved to its own file
(`internal/dashboard/result.go`) to keep the constant out of the emitted set —
worth knowing before adding a package whose types sit beside a duration
constant.

The current emitted set is `domain.ts`, `api.ts`, `events.ts`, `embed.ts`,
`dashboard.ts`, `dashboardspec.ts`, `doctable.ts`, `docparse.ts`, `videoplan.ts`
and `webhooks.ts`.

## The embed contract (2026-09-10), and what the last hand-written types cost

`apps/widget` was never made a consumer of this package. It kept two
hand-written interfaces for `/api/embed`, and the ticket's own thesis held
exactly: **both were wrong, and neither failed anywhere.**

- `WidgetConfig` described the inner object of a `{config, agents}` envelope, so
  the widget read the tenant's greeting and starter prompts off the envelope and
  got `undefined` for both — for a month, in front of every tenant who had
  configured either.
- `Message` described four fields of a row the route was serving whole, so
  nobody had to look at the other six. Two of them were the agent's tool calls
  and the tool-role rows carrying the truncated SQL, the `source_id` and the
  table names of every turn.

Both are in [`widget.md`](widget.md) §6 with the fix. What belongs *here* is
what it says about this ticket:

**"Extend it when X lands" is not a mechanism.** The backlog row asking for
these types was written when `T-02b` shipped and named its own trigger —
`T-19`/`T-20` adding embed types. They landed 2026-08-09. The row was still
open on 2026-09-10, and nothing in between could have said so, because the cost
of not doing it is a hand-written type that compiles.

**The surface got a package, and that closed the last bullet under Limits for
one route group.** `internal/transport/http/embedwire` is generated to
`src/embed.ts`, and the handlers now return its structs rather than `gin.H` —
so on this surface the chain really is *route → Go struct → TypeScript*, and a
handler that returns something else does not compile. The other forty `/api`
envelopes are unchanged.

**And a mechanical finding worth writing down: tygo keys `packages:` by import
path.** Two entries naming the same Go package silently produce one file — no
warning, no error, exit 0, and the second `output_path` simply never appears.
Found by writing that exact config and getting no file. It means *"generate a
second view of one package"* is not a thing this generator can do, and a surface
that wants its own output file needs its own Go package.

## Go struct embedding does not survive tygo (2026-09-10)

`encoding/json` **inlines** an embedded struct's fields; tygo renders embedding
as a **named field**. So a Go type like

```go
type FeedbackWithContext struct {
    MessageFeedback           // flat on the wire
    Question string `json:"question,omitempty"`
}
```

generates `{ MessageFeedback: MessageFeedback; question?: string }` for a body
that is actually `{ id, thread_id, rating, …, question }`. **The generated type
misdescribes its own wire** — the exact defect this ticket exists to prevent,
arriving from the generator instead of from a hand-written file.

Found by the TypeScript compiler the first time a browser tried to read one
(`T-Q16`), which is the system working: eight `TS2339`s naming eight fields that
the type said were one level down. Fixed by not embedding — `FeedbackWithContext`
spells its ten fields out, with a comment on the type saying why.

**Two latent cases remain and are deliberately left alone:** `QueryExampleHit`
(embeds `QueryExample`) and the document-chunk hit (embeds `DocumentChunk`).
Both generate the same wrong shape and neither is read by any browser — they are
turn-time types that happen to live in `internal/domain`. Un-embedding them would
ripple through every Go caller relying on field promotion, for no consumer.
What they get instead is a warning comment where the next person will hit it.

**The rule:** a type that crosses the wire does not embed. Inside the process,
embed freely.

## The generator copies a comment it cannot tell is misfiled (2026-09-11)

tygo carries Go doc comments into the TypeScript. That is the feature — the
reason a dashboard developer reads the same argument the backend author wrote
— and it means a comment attached to the wrong declaration is republished,
faithfully, to the other side of the wire.

`embedwire.go` had its `package` clause **below** `ConfigResponse`'s doc
comment. Go attaches a package comment to whatever comment group immediately
precedes the clause, so:

- `go doc` opened the package with two paragraphs about why `agents` is nested,
- `ConfigResponse` was undocumented,
- the forty lines above — the argument for the package existing at all,
  including *"a field added here reaches the browser and a field not here
  cannot"* — became an orphaned block attached to nothing, and
- `packages/api-types/src/embed.ts` shipped all of that, in exactly that wrong
  arrangement, to the file the widget imports.

**`make types --check` was green throughout, and correctly.** It compares the
committed output against what the current Go generates. The Go was wrong, the
output matched it, and there is no drift. A drift gate cannot have an opinion
about its input — which is the same boundary the section above records from the
other direction, where the compiler caught what the generator could not.

**Nothing else caught it either** — but only because it was the *package*
clause. Written here first as "no linter can see this", and corrected within the
day: staticcheck's `ST1021`/`ST1022` catch a misfiled doc comment on an exported
**type or var** precisely, and were already reporting two other instances of it
in this tree (`videoplan.PromoBrand`, `app.ErrSharePassword`) into a lint step
that had been red since August and unread since
([`delivery-log.md`](delivery-log.md) Phase 3y).

What is genuinely uncovered is the package clause and nothing else: the file
compiles, `gofmt` has no opinion about where a comment sits, and `ST1000` asks
only whether a package comment *exists* — one did. The check that found it was `go doc`, run
once against a package that was one day old.

**The rule, in two halves:** a misfiled doc comment on an exported type or var
is a lint finding — run `make check`, which includes it, rather than the three
commands by hand. On a **package clause** it is not, and there `go doc` is the
only view that shows what the generator is about to copy. Read it once on a
package the first time you create one; it takes five seconds.

## Limits

- **`/api` envelopes are still untyped.** Forty responses are `gin.H`
  wrappers — `{"threads": […]}` — around generated element types. The elements
  are the contract that drifts; the wrappers are one word each and the
  dashboard unwraps them inline. Typing them is a wire-shape change per route,
  which is a ticket rather than a line in this one.
- **`pkg/models` is generated but unused.** The ticket named it. Its types are
  inbound webhook envelopes from Meta and Twilio; no browser will ever receive
  one, so they are generated to `src/webhooks.ts` and kept out of the barrel
  rather than pretended into the dashboard's contract.
- **Embedded structs generate a shape the JSON does not have.** See above.
  `QueryExampleHit` and the document-chunk hit are wrong in the committed output
  today; neither has a consumer, and both carry a comment saying so.
- **The four `*Filter` structs are noise.** See above — visible noise, not
  misleading noise.
- **Nothing checks that a *handler* returns what its type says.** The chain is
  Go struct → TypeScript, not route → TypeScript. A handler that returns a
  different struct than the dashboard expects is still a runtime surprise;
  `/v1` has that covered by `T-A4`'s schema-parity test, and `/api` does not.
  **This limit cost a month of every tenant's widget configuration** before
  2026-09-10 — see above. It is now closed for `/api/embed`, whose handlers
  return the generated structs, and open for the other forty routes.
