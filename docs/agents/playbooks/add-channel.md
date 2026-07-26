# Playbook: Add a Chat Channel

Adding Slack, Telegram, or email. **Do not invent a new pattern** — Discord and
Lark were added together in `17f81f5` and established the shape. Read that commit
before starting.

**Time:** ~2d for a well-documented platform. Budget more if the platform needs a
persistent gateway connection (like Discord) rather than webhooks (like Lark).

---

## Step 0 — Decide the inbound mechanism

This is the one architectural decision, and it determines whether you need a
fourth process.

| Mechanism | Example | Consequence |
| --------- | ------- | ----------- |
| **Webhook** — platform POSTs to you | Lark, WhatsApp, Slack Events API | Handler in `cmd/api`. No new process. Preferred. |
| **Gateway** — you hold a socket open | Discord | Needs its own process (`cmd/discord`) plus Redis-based credential reload. |

Slack and Telegram both support webhooks. **Choose webhooks.** Only take the
gateway path if the platform offers no alternative.

**Third case — the widget channel (`T-20`).** The embeddable widget is also a
`Channel`, but it has *neither* inbound mechanism: the client calls
`/api/embed/chat` directly and receives the reply over its own WebSocket. So
steps 1–2 and 5–9 of this playbook apply in full (channel constant, migration,
thread keying, allowlist equivalent, usage attribution, switch-case audit), while
steps 3–4 and 6 do not — there is no provider package and no outbound send.
`ChatRunner.completeWith` gets a **deliberate no-op case with a comment saying
why**, because delivery already happened over the event bus. Do not "fix" that
empty case later.

## Step 1 — Domain: add the channel

`internal/domain/thread.go`:

```go
const (
    ChannelWhatsApp  Channel = "whatsapp"
    ChannelDashboard Channel = "dashboard"
    ChannelDiscord   Channel = "discord"
    ChannelLark      Channel = "lark"
    ChannelSlack     Channel = "slack"   // new
)
```

Then grep every switch on `Channel` and handle the new case. At minimum:
`ChatRunner.completeWith`, the usage-by-channel aggregation SQL, and the frontend
channel labels. **A missing case is a silent no-op — the agent answers and nobody
receives it.**

## Step 2 — Migrations

Three migrations, matching the Discord/Lark precedent (claim numbers per
[`add-migration.md`](add-migration.md)):

1. `NNN_company_slack_credentials` — per-tenant bot token, signing secret, workspace
   id. Secrets in `*_encrypted` columns using the DSN cipher.
2. `NNN_allowed_slack_users` — allowlist. **Every channel has one.** Without it,
   anyone who can reach the bot can query the company's data.
3. `NNN_thread_slack` — thread-keying columns on `conversation_threads` plus a
   unique index on the lookup key.

### Choosing the thread key — the important design decision

Look at how the two existing channels differ:

- **Discord:** keyed by `(company_id, discord_user_id)`. One user gets one
  continuous thread regardless of which guild or channel they message from, and the
  idle-gap + classifier fork logic applies.
- **Lark:** keyed by `(company_id, lark_thread_key)`. One Lark reply-thread is one
  agent memory **by definition**, so `ResolveForLark` skips fork classification
  entirely.

**The rule:** if the platform has native threads, key on the platform's thread id
and skip fork classification — the user already told you the boundary. If it does
not, key on user id and use the idle-gap heuristic.

Slack has native threads (`thread_ts`) **and** channel-level conversation. Support
both: key on `thread_ts` when present, fall back to `(channel_id, user_id)` when the
message is not in a thread.

## Step 3 — Provider package

`internal/slack/` mirroring `internal/lark/`:

| File | Purpose |
| ---- | ------- |
| `doc.go` | Package doc: how the integration works, which platform docs apply |
| `signature.go` | Inbound request verification. **Never skip this.** |
| `events.go` | Payload types + parsing |
| `mention.go` | Strip the bot mention from the message text |
| `provider.go` | `Provider` interface — the outbound contract |
| `client.go` | REST client with per-company token caching |

`Provider` interface, following `lark.Provider`:

```go
type Provider interface {
    Reply(ctx context.Context, companyID, messageRef, text string) error
}
```

### Signature verification is mandatory

Every existing channel verifies inbound requests: WhatsApp HMAC-SHA256, Discord
Ed25519, Lark its own signature scheme. Slack uses
`v0=HMAC-SHA256(signing_secret, "v0:{timestamp}:{body}")` and requires a timestamp
freshness check to prevent replay.

**An unverified webhook lets anyone on the internet inject messages as any user of
any tenant.** Reject before parsing.

## Step 4 — Service

`internal/app/slack_service.go`, mirroring `lark_service.go`: credential CRUD
(encrypt on save, redact on read), allowlist CRUD, inbound authorization check.

Return the same shape of "not allowed" result the other channels use — do not
invent a new error type.

## Step 5 — Webhook handler

`internal/transport/http/handlers/slack_webhook.go`, mirroring `lark_webhook.go`:

1. Verify the signature. Reject early.
2. Handle the platform's URL-verification challenge (Slack sends `type:
   url_verification` on setup).
3. **Deduplicate.** Slack retries aggressively on slow responses. Key on the
   platform event id in Redis with a short TTL. Without this, a slow turn produces
   duplicate answers and duplicate billing.
4. Ignore bot's own messages — otherwise it replies to itself in a loop.
5. Resolve company from the workspace/app id, check the allowlist.
6. Resolve the thread via `ThreadService`.
7. `ChatEnqueuer.Enqueue(...)` and return **200 immediately**. Slack requires a
   response within 3 seconds; the agent takes longer. This is exactly why the
   API/worker split exists.

Register in `cmd/api/router.go` under the `webhook` group, gated on
`cfg.SlackEnabled`:

```go
if d.slackSvc != nil {
    handlers.NewSlackWebhookHandler(d.slackSvc, d.chatEnq).Register(webhookGroup)
}
```

## Step 6 — Outbound in the worker

`ChatRunner.completeWith` — add the case:

```go
case domain.ChannelSlack:
    if r.slackProv != nil && p.SlackChannelID != "" && p.CompanyID != "" {
        if err := r.slackProv.Reply(ctx, p.CompanyID, p.SlackChannelID, response); err != nil {
            logrus.WithError(err).WithFields(logrus.Fields{
                "company_id": p.CompanyID,
                "channel_id": p.SlackChannelID,
            }).Error("slack reply failed")
        }
    }
```

Add a `WithSlack(p slack.Provider) *ChatRunner` chainable setter (following
`WithLark`) and wire it in `cmd/worker/main.go` behind `cfg.SlackEnabled`.

Extend `queue.ChatRunPayload` with the channel's routing fields. **Payload changes
are backward-compatible additions only** — tasks enqueued by the old binary must
still deserialize in the new worker during a rolling deploy.

### Message formatting

Each platform mangles markdown differently. WhatsApp gets
`stripMarkdownLinks()` — `[text](url)` becomes `text: url` — because it
auto-links raw URLs but does not render markdown. Slack uses `<url|text>` and its
own `mrkdwn` dialect. **Write a `formatForSlack()` helper; do not send raw markdown
and hope.**

## Step 7 — Config

`internal/config/config.go`:

```go
SlackEnabled     bool
SlackAPIBaseURL  string
```
```go
SlackEnabled:    getEnv("SLACK_ENABLED", "true") == "true",
SlackAPIBaseURL: getEnv("SLACK_API_BASE_URL", ""),
```

Per-tenant credentials live in the database, not in env. Env holds only the global
kill switch and the API base URL (for regional endpoints — this is why
`LARK_API_BASE_URL` exists).

## Step 8 — Management API + UI

- `internal/transport/http/handlers/slack.go` — credentials + allowlist CRUD,
  mirroring `lark.go`. **Admin-only** (see T-04; do not repeat the mistake of
  leaving these open).
- Dashboard: `src/features/settings/slack-tab.tsx` mirroring `lark-tab.tsx`, plus
  a row in `integrations-tab.tsx`.

## Step 9 — Verify

- [ ] Signature verification rejects a tampered payload
- [ ] Replayed request (stale timestamp) rejected
- [ ] Duplicate event id processed once
- [ ] Bot's own message ignored — no self-reply loop
- [ ] Non-allowlisted user refused, and the refusal is intelligible
- [ ] Thread keying: two messages in one platform thread → one Argentum thread
- [ ] Thread keying: two separate platform threads → two Argentum threads
- [ ] Full round trip: message in → agent answers → reply arrives formatted correctly
- [ ] Links render correctly on the platform
- [ ] `/api/usage/by-channel` shows the new channel with non-zero cost
- [ ] Kill switch off → webhook returns 503, no sessions opened

---

## Gate

Paste: the inbound webhook log line, the worker log for the enqueued task, a
screenshot of the reply on the actual platform, and the
`/api/usage/by-channel` response showing the new channel.

## Common mistakes

| Mistake | Consequence |
| ------- | ----------- |
| Skipping signature verification | Anyone can impersonate any user of any tenant |
| No event deduplication | Duplicate answers and duplicate billing on slow turns |
| Not ignoring the bot's own messages | Infinite self-reply loop, unbounded spend |
| Responding after the agent finishes | Platform times out and retries; see the two above |
| No allowlist | Anyone who finds the bot can read the company's data |
| Missing a `switch Channel` case | Agent answers into the void |
| Sending raw markdown | Users see literal `[text](url)` |
| Breaking `ChatRunPayload` compatibility | In-flight tasks fail across a rolling deploy |
| Management routes not admin-gated | Any member can swap the bot token |
