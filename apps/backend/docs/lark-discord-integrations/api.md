# Lark & Discord Integration API

Per-tenant configuration and user allowlist endpoints for the Lark (Feishu) and Discord chat integrations, plus the public webhook routes those providers call.

## Conventions

- **Base URL:** instance-specific (e.g. `https://argentum.example.com`)
- **Content type:** `application/json`
- **Auth (admin endpoints):** JWT bearer token from `POST /api/auth/login`. Send as `Authorization: Bearer <access_token>`. The middleware derives `company_id` from the token — there is no tenant header or path segment.
- **Auth (webhook endpoints):** none from caller. The handler verifies provider-issued signatures / verification tokens.
- **Error envelope:** `{ "error": "<message>" }`
- **Secrets in responses:** `bot_token` (Discord) and `app_secret` (Lark) are write-only. They are encrypted at rest and never returned.

### Status codes

| Code | Meaning |
|------|---------|
| 200 | Success |
| 201 | User added to allowlist |
| 204 | Deleted / removed |
| 400 | Missing or invalid field |
| 401 | Missing or invalid JWT (admin), or bad signature / verification token (webhook) |
| 404 | Resource not found |
| 409 | User already on allowlist |
| 500 | Server-side failure |
| 503 | Integration disabled (`enabled=false`) |

---

# Discord

## Provider setup (one-time, in Discord Developer Portal)

1. Create an **Application** at <https://discord.com/developers/applications>. Note the **Application ID** and **Public Key** (Ed25519 hex).
2. Add a **Bot** to the application. Copy the **Bot Token** — Discord shows it once.
3. (Optional) Restrict the bot to a single guild and copy the **Guild ID**.
4. In **General Information → Interactions Endpoint URL**, set:
   ```
   https://<your-host>/webhook/discord/interactions
   ```
   Discord will ping this URL with a signed request when you save. Argentum responds to the PING handshake automatically once credentials are stored — so **save credentials in Argentum first**, then paste the URL in Discord.
5. Under **OAuth2 → URL Generator**, enable scopes `bot` + `applications.commands` and the permissions you need (at minimum `Send Messages`, `Read Message History`, `Use Application Commands`). Invite the bot to your guild.

## Admin endpoints

All routes below sit under `/api` and require `Authorization: Bearer <jwt>`.

### `GET /api/discord` — read current config

Returns the tenant's Discord configuration with the bot token redacted.

Response (configured):
```json
{
  "configured": true,
  "company_id": "01HV...",
  "application_id": "1234567890",
  "public_key": "ed25519-hex...",
  "guild_id": "9876543210",
  "enabled": true,
  "updated_at": "2026-05-14T08:00:00Z"
}
```

Response (not yet configured):
```json
{ "configured": false }
```

### `PUT /api/discord` — create / update config

Upsert. Use the same endpoint for first save and rotation.

Request body:
```json
{
  "application_id": "1234567890",
  "public_key": "ed25519-hex...",
  "bot_token": "MTAxNzQ...",
  "guild_id": "9876543210",
  "enabled": true
}
```

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `application_id` | string | yes | Discord application ID. |
| `public_key` | string | yes | Ed25519 hex from the developer portal. Used to verify webhook signatures. |
| `bot_token` | string | first save only | Required on first save. Omit (or empty string) on subsequent calls to keep the existing token — useful when you only want to flip `enabled` or change `guild_id`. |
| `guild_id` | string | no | Restrict bot to one guild. |
| `enabled` | bool | no | Defaults to `true`. Set `false` to keep config but stop the gateway session. |

Response: same shape as `GET /api/discord` (configured form).

Side effect: publishes a `discord:reload` signal on Redis. The `cmd/discord` worker re-opens its gateway session for this tenant without needing a restart.

### `DELETE /api/discord` — drop config

Removes the row and signals the worker to close the gateway session. `204 No Content` on success.

### `GET /api/discord/users` — list allowlist

```json
{
  "users": [
    {
      "company_id": "01HV...",
      "discord_user_id": "234567890123456789",
      "label": "alice",
      "added_at": "2026-05-14T08:00:00Z"
    }
  ]
}
```

### `POST /api/discord/users` — add to allowlist

Request:
```json
{ "discord_user_id": "234567890123456789", "label": "alice" }
```

`label` is optional. `201 Created` on success. `409 Conflict` if the user is already on the allowlist.

### `DELETE /api/discord/users/:id` — remove from allowlist

`:id` is the Discord user snowflake. `204 No Content` on success (idempotent).

## Webhook (called by Discord)

### `POST /webhook/discord/interactions`

Public ingress for Discord's interactions API. Verifies the `X-Signature-Ed25519` + `X-Signature-Timestamp` headers against the tenant's `public_key` (resolved by `application_id` from the body — header values are not trusted for tenant lookup).

- **PING (type 1):** responds `{ "type": 1 }` (Pong). This is what Discord calls when you save the Interactions Endpoint URL.
- **Other interaction types:** responds with an ephemeral message ("Slash commands aren't enabled. DM the bot or @mention it instead."). Slash commands are not wired yet — DM and @mention flows run through the gateway worker, not this webhook.

Returns `401` if signature verification fails or the `application_id` is unknown.

## Inbound message flow (informational)

DMs to the bot and @mentions in guild channels are picked up over the **gateway** by the `cmd/discord` worker, not the HTTP webhook. There is nothing to configure beyond saving credentials and adding users to the allowlist — the worker subscribes to `discord:reload` and opens a session for every enabled tenant. Messages from users not on the allowlist are silently dropped.

---

# Lark (Feishu)

## Provider setup (one-time, in Lark Open Platform)

1. Create a **Custom App** at <https://open.larksuite.com/app> (or `open.feishu.cn` for the China region).
2. From **Credentials & Basic Info**, copy the **App ID** and **App Secret**.
3. From **Event Subscriptions → Encryption Strategy**, copy the **Verification Token**. Optionally generate an **Encrypt Key** (recommended — Lark will then encrypt event payloads with AES-256-CBC and sign requests with HMAC).
4. From **Permissions & Scopes**, grant at minimum: `im:message`, `im:message:send_as_bot`, `im:message.group_at_msg`, `im:message.p2p_msg` (and `im:resource` if you ever want to download attachments).
5. Find the bot's **open_id** (the bot's own user identifier). The easiest way: send a test message to the bot and inspect the inbound webhook payload — Argentum logs it. Save it as `bot_open_id` so the dispatcher can detect @mentions of the bot.
6. **Save credentials in Argentum first** (see `PUT /api/lark` below), then set the **Event subscription request URL** in Lark to:
   ```
   https://<your-host>/webhook/lark/events/<your-app-id>
   ```
   Lark sends a `url_verification` ping — Argentum echoes the challenge once it can resolve the app id.
7. Subscribe to the `im.message.receive_v1` event.
8. Publish the app and install it in your tenant.

## Admin endpoints

All routes below sit under `/api` and require `Authorization: Bearer <jwt>`.

### `GET /api/lark` — read current config

Response (configured):
```json
{
  "configured": true,
  "company_id": "01HV...",
  "app_id": "cli_a1b2c3d4",
  "verification_token": "v-tok-...",
  "encrypt_key": "enc-key-...",
  "bot_open_id": "ou_xxx",
  "enabled": true,
  "updated_at": "2026-05-14T08:00:00Z"
}
```

Response (not yet configured):
```json
{ "configured": false }
```

### `PUT /api/lark` — create / update config

Request body:
```json
{
  "app_id": "cli_a1b2c3d4",
  "app_secret": "secret-...",
  "verification_token": "v-tok-...",
  "encrypt_key": "enc-key-...",
  "bot_open_id": "ou_xxx",
  "enabled": true
}
```

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| `app_id` | string | yes | Lark App ID. |
| `app_secret` | string | first save only | Required on first save. Omit on subsequent calls to keep the existing secret — useful when you only want to update `bot_open_id` or flip `enabled`. |
| `verification_token` | string | yes | From the Lark event subscription page. Used to authenticate non-encrypted callbacks. |
| `encrypt_key` | string | no | If set, Lark encrypts the payload (AES-256-CBC) and signs the request. Argentum verifies the HMAC. Strongly recommended for production. |
| `bot_open_id` | string | no | Bot's own open_id. Required for @mention triggering — until this is set, inbound messages are ignored. |
| `enabled` | bool | no | Defaults to `true`. `false` makes the webhook return `503`. |

Response: same shape as `GET /api/lark` (configured form).

### `DELETE /api/lark` — drop config

`204 No Content`. No reload signal — the worker's `lark.Client` refreshes credentials lazily on the next outbound call (or on a `401` retry).

### `GET /api/lark/users` — list allowlist

```json
{
  "users": [
    {
      "company_id": "01HV...",
      "lark_open_id": "ou_abc123",
      "label": "bob",
      "added_at": "2026-05-14T08:00:00Z"
    }
  ]
}
```

### `POST /api/lark/users` — add to allowlist

Request:
```json
{ "lark_open_id": "ou_abc123", "label": "bob" }
```

`label` is optional. `201 Created` on success. `409 Conflict` if already present.

### `DELETE /api/lark/users/:id` — remove from allowlist

`:id` is the Lark open_id. `204 No Content` (idempotent).

## Webhook (called by Lark)

### `POST /webhook/lark/events/:app_id`

Public ingress for Lark event callbacks. The `app_id` in the path is used to resolve the tenant and load `verification_token` / `encrypt_key` / `bot_open_id` — header and body values are not trusted for tenant lookup.

Flow per request:
1. Look up credentials by `app_id`. `404` if unknown, `503` if `enabled=false`.
2. Parse envelope (decrypt with `encrypt_key` if encrypted).
3. If `encrypt_key` is set and Lark sent `X-Lark-Signature`, verify HMAC (`ts`, `nonce`, body). `401` on mismatch.
4. Branch on event type:
   - **`url_verification`** — verify `token` matches `verification_token`, then echo `{ "challenge": "..." }`. This is what the Open Platform calls when you save the request URL.
   - **`im.message.receive_v1`** — see below.
   - **Anything else** — silent `200` so Lark stops retrying.

For `im.message.receive_v1`:
- The bot must be @mentioned (matched against `bot_open_id`). Otherwise silent `200`.
- Only `text` messages are processed today; other types are silently acked.
- The sender's `open_id` must be on the allowlist. Otherwise silent `200` (no error returned to the sender).
- The handler enqueues a `chat:run` task with the message and a `thread_key` derived from `thread_id`, `root_id`, or `message_id` (first non-empty). The reply is delivered asynchronously by the worker.

---

# End-to-end checklist

## Discord

1. `POST /api/auth/login` → get JWT.
2. Create app + bot in Discord Developer Portal. Collect `application_id`, `public_key`, `bot_token`, (optional) `guild_id`.
3. `PUT /api/discord` with those fields.
4. Set the Interactions Endpoint URL in Discord to `https://<host>/webhook/discord/interactions`. Save — Discord pings, Argentum pongs.
5. Invite the bot to your guild via OAuth2.
6. `POST /api/discord/users` for each user that should be allowed to chat.
7. DM the bot or @mention it in a channel. The `cmd/discord` worker handles the reply over the gateway.

## Lark

1. `POST /api/auth/login` → get JWT.
2. Create app in Lark Open Platform. Collect `app_id`, `app_secret`, `verification_token`, optional `encrypt_key`.
3. `PUT /api/lark` with those fields (leave `bot_open_id` empty if you don't know it yet).
4. Set the event subscription request URL to `https://<host>/webhook/lark/events/<app_id>` and subscribe to `im.message.receive_v1`.
5. Send a test message to the bot. Look up the bot's `open_id` from the inbound payload in the logs.
6. `PUT /api/lark` again with `bot_open_id` set (and `app_secret` omitted — the existing value is kept).
7. `POST /api/lark/users` for each user that should be allowed to chat.
8. @mention the bot in a chat where it has been added. Reply is delivered asynchronously.
