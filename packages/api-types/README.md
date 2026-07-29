# @argentum/api-types

TypeScript for the Go structs the dashboard reads off `/api`. Generated —
**never hand-edited**.

```bash
make types         # regenerate src/ from apps/backend
make types-check   # verify src/ matches the Go, writing nothing
```

`src/` is committed, so a contract change shows up in the diff of the commit
that made it, and CI's `types` job regenerates and fails on any difference.

## What is in here, and what is not

| File | Generated from | Holds |
| ---- | -------------- | ----- |
| `src/domain.ts` | `internal/domain` | the entities: threads, messages, documents, usage rows, credits |
| `src/events.ts` | `internal/app/{event_bus,budget_state}.go` | the WebSocket frames and the credit position |
| `src/api.ts` | `internal/transport/http/handlers/wire.go` | `/api` responses that are not entities |
| `src/webhooks.ts` | `pkg/models` | inbound WhatsApp / Twilio envelopes |
| `src/index.ts` | — | the barrel: domain + events + api |

`src/webhooks.ts` is **not** in the barrel. Those types are what Meta and Twilio
send *us*; no browser receives one, and `models.Message` would collide with
`domain.Message`. Import them explicitly if you ever need one:

```ts
import type { WhatsAppWebhookPayload } from "@argentum/api-types/webhooks";
```

**`/v1` is not here.** The public API's types are generated from
`apps/backend/openapi/v1.yaml` into the two SDKs (`make openapi`). A published
contract is authored and the code is checked against it; an internal one is
derived from the code. Two directions on purpose.

## Adding a type

Write the Go struct in a file the generator already reads, and run `make types`.
If it belongs to none of them, the question to answer first is *which* of the
four it is — a domain entity, an event, an `/api` response body, or an inbound
webhook — because that is what decides where the Go goes.

For an `/api` response assembled inline as a `gin.H`, there is nothing to
generate: declare the struct in `handlers/wire.go` and return that instead. Its
package comment explains what belongs there.

## Two rules the generator applies after tygo

1. **Go interfaces are dropped.** `type ThreadRepository interface{…}` renders
   as `export type ThreadRepository = any`, and there are 23 of those. A
   repository has no JSON form; exporting its name bound to `any` hands a
   caller a plausible-looking type that silently switches type-checking off.
2. **`any` becomes `unknown`.** The rest come from `map[string]interface{}`,
   whose honest TypeScript is `Record<string, unknown>`: the value is real and
   the caller must narrow it.

Both run on emitted code only, never on comments — `domain/api_key.go` contains
the sentence *"there is no \"any authenticated key\" tier"*, and a blunt replace
would rewrite it.

## Enums

A Go string enum becomes a literal union when its constants are named after
their type (`ChannelWhatsApp` → `Channel`), and a `string` alias with named
constants when they are not (`UsageEventLLMCall` → `UsageEventType`). The union
is what makes this package earn its keep: adding a channel in Go widens it, and
a `switch` in the dashboard that no longer covers every case fails the build.
