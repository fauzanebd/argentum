# Slack channel — coverage

**Status: CODE COMPLETE 2026-08-08.** The gate — a real Slack workspace, an
@mention answered in-thread, and `/api/usage/by-channel` showing `slack` with a
non-zero cost — needs the stack and a Slack app, and is outstanding.

Built from [`../agents/playbooks/add-channel.md`](../agents/playbooks/add-channel.md),
which names Slack as its worked example. The backlog entry estimated 2 days and
called the shape known; it was.

## 1. What this is

The fifth inbound chat channel, beside WhatsApp, Discord, Lark and the
dashboard. Webhook in, `chat.postMessage` out, no new process — the playbook's
Step 0 rule is "choose webhooks unless the platform offers no alternative", and
Slack's Events API does.

Nothing about the agent, the tools, or the thread model changed. A Slack turn
runs the same `ChatRunner` as every other channel.

## 2. The three decisions that were not copy-paste

Everything else mirrors `internal/lark`. These did not.

### Thread keying: two keys, not one

The playbook's rule is that a platform with native threads keys on the thread
id and skips fork classification, because the user already drew the boundary.
Slack has native threads **and** channel-level conversation, so it keys on both:

| Inbound | Key | Rule borrowed from |
| ------- | --- | ------------------ |
| message inside a thread (`thread_ts` set) | `(company, channel_id, thread_ts)` | Lark |
| top-level @mention or DM (no `thread_ts`) | `(company, channel_id, user_id)` | Discord |

Both columns include `channel_id` because Slack's `ts` is unique only *within* a
channel — `thread_ts` alone collides across rooms.

The two keys have to agree, and the place they could disagree is subtle. A
top-level mention has no thread yet; our reply creates one, hanging under the
mention's own `ts`. Every follow-up inside that new thread then arrives with
`thread_ts` set — and would find nothing under the thread key, opening a
*second* conversation for what the user is looking at as one. So a thread
opened from a top-level message **stores the ts our reply will hang under** as
its `slack_thread_ts`. That single line is what makes the two lookups resolve
to the same row.

`049`'s unique index enforces one conversation per Slack thread in the schema
rather than only in the resolver, so a concurrent pair of events cannot open
two rows.

### Deduplication is Redis, not a header

Slack retries any ack slower than three seconds, and redelivers in failover
*without* the retry header. Two deliveries of one event is two agent runs, two
charges against the tenant's credit, and two answers in one thread.

`internal/slack/dedupe.go` claims the event id with `SET NX` and a 10-minute
TTL. `SET NX` because the retry may land on a different API replica than the
delivery it repeats, and only an atomic claim is correct across replicas.

The retry header is still checked first — it is free, and it catches the
ordinary case before Redis is touched. A Redis *error* processes the event:
dropping a real question because a cache is unreachable is worse than the
duplicate it would prevent.

### The bot's own user id is learned, not configured

Lark's `bot_open_id` must be copied out of a webhook payload by hand, and until
it is set, inbound mentions are silently ignored — the integration's most
common setup failure.

Slack sends the bot's user id on every `event_callback`, in
`authorizations[].user_id` where `is_bot` is true. The webhook reads it,
persists it when it differs from what is stored, and the field stays optional
in the API and read-only in the UI.

The echo-loop guard does not depend on it either: Slack stamps `bot_id` on
every message a bot posted, so the bot's own reply is refused even on the first
event, before the id is known.

## 3. What was added

**Migrations** `047_company_slack_credentials`, `048_allowed_slack_users`,
`049_thread_slack`. Numbers claimed per `add-migration.md` — the next three
free, not the ones a plan reserved.

**`internal/slack/`** — `signature.go` (v0 HMAC + freshness), `events.go`
(payload types, `Actionable`), `mention.go`, `mrkdwn.go`, `dedupe.go`,
`client.go`, `provider.go`.

**Wiring** — `domain.ChannelSlack`; two `ThreadRepository` lookups;
`ThreadService.ResolveForSlack`; `ChatInput` + `ChatRunPayload` fields
(additive only, so in-flight tasks survive a rolling deploy);
`ChatRunner.completeWith`'s case and `WithSlack`; `SlackService`;
admin handler; webhook handler; config; the policy table; the API and worker
wiring.

**Bindings (T-S4)** — Slack joins `BindableChannels`, keyed on the Slack
channel id, for the reason Discord uses its channel: a binding is a room
configured for a job. `agent_binding_service_test.go` had been using `"slack"`
as its stand-in for a channel that does not exist; that case now says
`"telegram"`.

## 4. Formatting

Slack renders *mrkdwn*, not Markdown. Untranslated, the agent's output reaches
users as literal punctuation — the playbook's named mistake.

| Agent writes | Posted |
| ------------ | ------ |
| `[Sales](https://…)` | `<https://…\|Sales>` |
| `**bold**` | `*bold*` |
| `## Heading` | `*Heading*` |

Links convert before bold, so `**[text](url)**` collapses correctly. Lists,
inline code and fenced blocks already match and pass through.

## 5. Tests

`internal/slack` is at 90%+ statements, plus the webhook's security boundary in
`internal/transport/http/handlers`.

| Property | Test |
| -------- | ---- |
| Signature matches Slack's own published vector | `TestVerifySignature_knownVector` |
| Tampered body rejected | `TestVerifySignature_tamperedBody` |
| Replay outside the 5-minute window rejected, both directions | `TestVerifySignature_replayOutsideWindow` |
| A duplicate event id is claimed once | `TestFirstSightClaimsAnEventOnce` |
| One tenant's event id cannot suppress another's | `TestEventIDsAreNamespacedByApp` |
| No Redis degrades to processing, not to dropping | `TestNoRedisMeansNoDeduplication` |
| Bot echo refused even when `bot_user_id` is unknown | `TestActionable_unknownBotUserID` |
| Edits, joins and bot posts are not questions | `TestActionable` |
| A top-level message and a threaded reply key differently | `TestThreadKey` |
| Markdown becomes mrkdwn | `TestToMrkdwn` |
| Auth failure evicts the token and retries once; other errors do not retry | `TestClient_Reply_retriesOnceAfterAuthError`, `TestClient_Reply_nonAuthErrorNotRetried` |
| Unknown app id, disabled tenant, forged and missing signature | `TestSlackWebhook_*` |
| Delivery retry and non-allowlisted sender never reach the enqueuer | `TestSlackWebhook_deliveryRetryDropped`, `TestSlackWebhook_nonAllowlistedUserDropped` |

## 6. Known limitations

**Watchers cannot deliver to Slack.** `WatcherService.validateChannels` accepts
dashboard, WhatsApp, Discord and Lark; a Slack target is refused at creation
with `"slack" is not a channel a watcher can deliver to`. That is a clean
refusal rather than a silent drop, but it means the first tenant to ask for
"alert my Slack channel" is blocked. Adding it is a `Send` method on
`slack.Provider` (post without `thread_ts`) plus the two switches in
`watcher_service.go` — deliberately out of scope here, because the playbook's
checklist covers chat and this is a second feature.

**Text messages only.** Files, attachments and Block Kit inputs are ignored.
Same limitation Lark ships with.

**Replies are plain text.** `chat.postMessage` sends `text`; no Block Kit
formatting, no threading of long answers into multiple messages.

**Socket Mode is not supported**, and should not be — it is the gateway shape
the playbook says to avoid when webhooks exist.

## 7. Gate — outstanding

Needs a Slack workspace, which no CI job has. To run it:

1. Create an app, add the `app_mentions:read`, `chat:write`, `im:history`,
   `channels:history` bot scopes, install, copy the `xoxb-` token.
2. `PUT /api/slack` with app id, signing secret and token.
3. Point Event Subscriptions at `https://<host>/webhook/slack/events/<app_id>`;
   Slack verifies immediately.
4. Subscribe `app_mention` and `message.im`.
5. `POST /api/slack/users` for yourself, `/invite` the bot, ask it something.

Paste, per the playbook: the inbound webhook log line, the worker log for the
enqueued task, a screenshot of the reply in Slack, and the
`/api/usage/by-channel` response showing `slack` with a non-zero cost.

The one thing worth watching on a first run is §2's threading claim — ask a
top-level question, then a follow-up **inside** the thread the bot opens, and
confirm both land in one `conversation_threads` row.
