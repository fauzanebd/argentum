# T-15 · Outbound webhook subscriptions — coverage

**Status: CODE COMPLETE 2026-08-03.** The gate — a local receiver, a triggered
watcher breach, and the signed payload verifying against the secret — needs the
stack and a running watcher, and is outstanding.

## 1. What this ticket is not

It is not a delivery mechanism. `T-A2` built one for report callbacks:
`internal/webhookout` signs HMAC-SHA256 over `<timestamp>.<raw body>`, delivers
through asynq with exponential backoff, refuses a target on our own network,
resolves the address again immediately before the request, and writes a row per
attempt. The ticket's own instruction is to *subscribe events to it, do not
write a second signer or a second retry loop*, and nothing here does.

So what shipped is three things: a subscription model, a fan-out, and the
failure counter that switches a subscription off.

## 2. The model

`webhook_subscriptions` (migration `046`): a URL, an events array, `enabled`,
and three health columns — `consecutive_failures`, `disabled_reason`,
`last_success_at` / `last_failure_at`.

**No secret column.** The signing secret is the company's, on
`companies.webhook_secret`, minted on first use by `EnsureWebhookSecret` and
already shared by every callback we send. A receiver verifying two
subscriptions with one secret is what every webhook integration expects;
per-subscription secrets would hand the tenant a table of them.

**An empty event list is refused** rather than meaning "everything". That is the
opposite of an agent's tool allowlist, deliberately: there an empty list widens
what an agent may do *inside* Argentum, and here it would widen what *leaves*
it.

`webhook_deliveries` gains a nullable `subscription_id`. It is `NULL` for a
`report.completed` callback — that URL arrived on the request that produced it
and belongs to no standing subscription — and `ON DELETE SET NULL`, because the
delivery log outlives the subscription and deleting one must not delete the
record of what it sent.

## 3. The three events

| Event | Fired when | Fired on failure too? |
| ----- | ---------- | --------------------- |
| `watcher.breached` | a watcher's condition is met **and** the briefing turn is enqueued | n/a |
| `action.executed` | an approved action finishes | **yes** — `status` says which |
| `scheduled_task.completed` | a scheduled run ends | **yes** |

Two of the three publish on failure, and that is the interesting decision. "We
tried to file your ticket and the far end refused" is the case an integration
most needs to hear, and a nightly report that stopped arriving is exactly what a
tenant wants told rather than left to notice. A subscriber that only wants
successes reads `status`; one that only wanted successes and got no failures
would have to poll for them.

`watcher.breached` publishes **after** the turn is enqueued, not before: the
webhook says a breach happened, and the thing that makes that true is the event
row plus the turn that will explain it.

**The bodies are structs, not maps.** The payload is marshalled exactly once, in
`webhookout.Sender`, and those bytes are what get signed and sent — a map would
marshal with a different key order on a different day, which verifies against
nothing. It is also the only JSON in this codebase we ask somebody else to write
code against, so its shape is reviewable in one file and asserted in a test.
Every body carries `event`, `occurred_at` and `company_id` in the body as well
as the headers, because a proxy can drop a header.

`watcher.breached` carries the number and the threshold rather than a rendered
sentence: a receiver deciding whether to page someone needs the value, and the
sentence the agent writes goes to the tenant's chat channels. `value` is
omitted rather than zero when the breach was `no_data` — zero is a different
claim.

## 4. Auto-disable after twenty

`Deliverer.record` is the one place a delivery reaches a terminal state, so the
health update lives there rather than at the five call sites that reach it — a
new terminal branch cannot forget the second half.

Twenty rather than three: a tenant's server being down for an hour is ordinary,
and each of those twenty is *already* five delivery attempts with backoff. A
feature that disables itself on the first blip is one people stop trusting.

The increment and the disable are one statement, so two deliveries failing at
once cannot both read nineteen and write twenty. Detecting *which* call did the
disabling is `RETURNING consecutive_failures = $3` — the counter can only land
exactly on the threshold once, where `NOT enabled` would be true for every
failure after it too.

Re-enabling clears the counter. A subscription switched back on after the
receiver was fixed must not be disabled again by the first blip landing on top
of nineteen old failures.

## 5. Surface

`GET|POST /api/webhooks`, `PUT|DELETE /api/webhooks/:id` — **admin on every
route including the reads**, the same line MCP servers drew: each row is an
egress destination we POST to unattended, and the list is a map of where a
workspace's events go.

The list response carries the event vocabulary, the disable threshold and the
signing contract beside the rows, so the settings form offers what this
deployment actually publishes and an admin can verify a delivery without opening
the API docs. A copy of any of the three in the frontend is a copy that goes
stale the day a fourth event is published.

The URL is checked at registration against the same rule the worker applies at
delivery time, so a tenant learns "that address is not one we will post to"
while looking at the form rather than through deliveries that silently fail
forever.

Settings → Webhooks renders the health: a failure count while a subscription is
still on, and the disable reason when it is off. Two different sentences on
purpose — "you turned this off" and "we did, and here is why" are not the same
thing to the admin looking at it.

## 6. Publishing never fails the thing that produced the event

`Publish` returns nothing and swallows everything: a subscription read that
failed, a delivery that could not be queued, a nil service. A watcher that
breached, breached; an action that ran, ran. A tenant's unreachable server must
not turn a completed piece of work into a failed one.

## 7. The gate, run 2026-08-04

Two receivers on loopback — one answering `200`, one answering `500` every time
— both subscribed to `watcher.breached`, against a watcher whose threshold was
already breached and whose cron fired every minute.

**The fan-out reached a real HTTP server.** First delivery arrived ~55s after
the watcher was enabled, carrying `Argentum-Event: watcher.breached`,
`Argentum-Delivery`, and a body with the value and the threshold rather than a
sentence:

```json
{"event":"watcher.breached","occurred_at":"2026-08-04T14:18:00.296442Z",
 "company_id":"…","watcher_id":"…","watcher_name":"Customers above 10",
 "metric_id":"…","event_id":"…","value":50,"comparator":"gt","threshold":10,
 "window_grain":"day","fired_at":"2026-08-04T14:18:00.255683Z"}
```

**The signature verifies.** `Argentum-Signature: t=1785853081,v1=0df4aee4…`
recomputed as HMAC-SHA256 over `t + "." + raw body` with
`companies.webhook_secret` matched exactly, and the same body with `"value":50`
changed to `"value":5` did not — which is the property the criterion names,
checked against the bytes on the wire rather than against a second reading of
§2.

**Auto-disable was watched, not reasoned about.** The `500` receiver's
subscription incremented one terminal failure at a time (each already five
attempts with backoff) and switched itself off on the twentieth, 24 minutes in:

| URL | enabled | consecutive_failures | disabled_reason | delivered | failed |
| --- | ------- | -------------------- | --------------- | --------- | ------ |
| `…:9500/hook` | t | 0 | | 25 | 0 |
| `…:9501/hook` | **f** | **20** | disabled automatically after 20 consecutive failed deliveries | 0 | 20 |

The healthy subscription beside it stayed enabled at zero the whole time — one
receiver being down does not take the other with it, and §6's "publishing never
fails the thing that produced the event" held for 25 breaches.

**One thing the gate needed that is not in this document:** a loopback receiver
is refused at registration (`callback_url must be https`) until
`API_V1_CALLBACK_ALLOW_PRIVATE=true`. That is `T-A2`'s switch, correctly reused
rather than duplicated, but §5's "the URL is checked at registration against the
same rule the worker applies" is the only hint of it, and the variable is not
named anywhere in this file.
