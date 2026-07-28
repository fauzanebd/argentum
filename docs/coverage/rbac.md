# RBAC and team management — T-04 record

**Landed 2026-07-28.** Ticket:
[`../plan/01-tickets.md`](../plan/01-tickets.md) `T-04`. Findings closed: `S-1`
(`AdminOnly()` applied to nothing) and `S-2` (nine credential- and
config-mutating routes reachable by any member).

---

## What was actually wrong

`middleware.AdminOnly()` had existed since the first architecture pass. It was
correct, it was tested by `T-02`, and it was wired to zero routes. Every
authenticated caller — regardless of the `role` claim in their JWT — could
rotate a database DSN, delete a data source, add a WhatsApp number to the
allowlist, or replace the Discord and Lark bot credentials.

The role column was not decorative: signup writes `admin`, and the JWT has
carried `role` since day one. Nothing read it.

There was also no way to create a second user at all. `users` had a
`company_id` and a `role`, and the only writer was signup, which creates a
company and one admin. A company with two people was not expressible.

## The shape of the fix

### Access is a table, not a decoration

The obvious implementation is `rg.PUT("/settings", middleware.AdminOnly(),
h.updateSettings)` in each handler's `Register`. It was rejected for one
concrete reason: **it cannot be verified.** `gin.RouteInfo` exposes a route's
final handler and nothing about the middleware chain in front of it, so no test
can read per-route gating back out of a built router. "Did we remember to gate
the new route?" would be answerable only by reading a dozen files carefully,
forever.

So the decision lives in `cmd/api/policy.go` as data:

```go
var apiPolicy = middleware.RolePolicy{
    "PUT /api/connections/:id/dsn": domain.RoleAdmin,
    "GET /api/connections":         domain.RoleMember,
    …
}
```

`middleware.RequireRole(apiPolicy)` sits on the authenticated group and looks
up `method + " " + c.FullPath()`. Two properties follow:

- **Unlisted is denied.** A route added without a decision fails closed on its
  first request rather than shipping open.
- **The table is diffable against the router.** `TestEveryAuthedRouteIsClassified`
  walks `r.Routes()` and fails in *both* directions — a route with no entry, and
  an entry with no route. The second half is what stops the policy rotting into
  a list of paths that no longer exist.

`AdminOnly()` is kept for one-off routes registered outside a policed group. It
is no longer how the API gates anything.

Ordering in `newRouter`: `Auth` → `RequireRole` → rate limiter. The limiter is
last on purpose — a request a member is not allowed to make should not spend
their token budget.

### Where the line was drawn

The ticket named nine routes. The policy gates more, and the reasoning is
recorded in `policy.go` next to the table rather than only here:

| Gated beyond the ticket | Why |
| --- | --- |
| `POST /api/connections` | A member who can *add* a source can point one anywhere. It is the same capability the ticket closed on `PUT …/dsn`. |
| `POST /api/connections/test`, `POST /api/connections/:id/test` | Takes a DSN and opens an outbound connection to an attacker-chosen host:port, writing no row. Leaving it open leaves the interesting half of the capability behind. |
| `PATCH /api/connections/:id`, `POST …/default` | Changing the default source changes which database every other user's questions run against. |
| `POST …/regenerate-description`, `…/reindex-embeddings`, `…/test-rag` | Each fans out LLM or embedding calls per table. Unmetered spend on demand. |

Deliberately left open to members: chat, threads, saved dashboards, usage
reads, the model catalogue, and creating or editing scheduled tasks. Gating
those would make "member" a role with nothing to do. `DELETE
/api/scheduled-tasks/:id` is admin per the ticket — a task belongs to whoever
created it, and deletion is the one operation that reaches across users.

### Invites, and the account lifecycle they force

`POST /api/users/invite` creates a **pending user** plus a single-use token.
The pending row matters: `users.email` is globally unique, so reserving the
address at invite time is what stops two companies inviting the same person and
the second accept failing on a constraint the invitee cannot act on.

- Token is 32 bytes from `crypto/rand`, base64url. Only its SHA-256 hash is
  stored, so a dump of `user_invites` cannot be replayed into account takeovers.
- SHA-256 rather than Argon2id on purpose: the token is 256 bits of uniform
  randomness, so there is no dictionary to slow an attacker down against, and
  the lookup runs on every accept request.
- Single-use is enforced in SQL, not by a read-then-write.
  `UserRepo.Activate` carries `WHERE activated_at IS NULL`, so of two racing
  accepts the second updates zero rows and gets `ErrNotFound`.
  `MarkAccepted` carries the same guard; either alone would do, and having both
  means a future caller that reorders them is still single-use.
- Re-inviting a still-pending address rotates the token and retires the old one
  rather than erroring. "The email never arrived" is the common case, and the
  alternative is an admin who has to revoke before they can re-send.

**There is no mail transport in the product yet**, so the plaintext token is
returned to the inviting admin once, rendered as a link, and never readable
again. That is recorded as a limitation below, not as a feature.

### Deactivation had to reach existing sessions

Three checks, because any one alone leaves a hole:

1. `Login` refuses a non-active account — **after** verifying the password, so
   someone who cannot authenticate learns nothing about whether an address is a
   pending invite or a removed colleague.
2. `Refresh` re-reads the user instead of re-signing the claims it was handed.
   Without this, a refresh token issued before removal stays good for seven
   days. It also means a role change takes effect on the next refresh rather
   than the next login.
3. Access tokens already issued stay valid until they expire. **The window is
   15 minutes** and it is deliberate — the alternative is a token blocklist,
   which is `T-13`'s problem, not this one.

### The last-admin rule

`ErrLastAdmin` blocks demoting or removing the only admin who can currently
act. "Currently act" is the load-bearing part: a pending admin who has never
accepted, and a deactivated one, do not count. Otherwise inviting a second
admin would unlock the door before anyone walked through it.

It returns 409, not 403 — the caller has the right role; the company is in a
state that forbids the transition.

## Migration

Filed in the ticket as `027_user_invites`, landed as **`021_user_invites`**.
golang-migrate only applies versions greater than the schema's current one, so
landing 027 now would strand 021–026 permanently — and `T-05` (`021_agent_actions`)
and `T-06` (`022_metric_definitions`) are already filed against those numbers.
Renumbering was the only option that leaves them applicable.

The one line in it that matters most:

```sql
UPDATE users SET activated_at = created_at WHERE activated_at IS NULL;
```

Without that backfill, the new login check locks out every existing user the
moment the binary rolls.

## Gate

The ticket's gate: *a table-driven test over every gated route × {admin,
member} asserting {200-ish, 403}.*

**26 admin routes × {admin, member}**, plus 31 member routes checked from the
other side. Every one of these is driven through the real `newRouter`, not a
router the test built for itself:

```
$ go test ./cmd/api/ -run TestGatedRoutesRejectMembers -v
--- PASS: TestGatedRoutesRejectMembers
    --- PASS: TestGatedRoutesRejectMembers/DELETE_/api/connections/:id
    --- PASS: TestGatedRoutesRejectMembers/DELETE_/api/discord
    --- PASS: TestGatedRoutesRejectMembers/DELETE_/api/discord/users/:id
    --- PASS: TestGatedRoutesRejectMembers/DELETE_/api/lark
    --- PASS: TestGatedRoutesRejectMembers/DELETE_/api/lark/users/:id
    --- PASS: TestGatedRoutesRejectMembers/DELETE_/api/phones/:phone
    --- PASS: TestGatedRoutesRejectMembers/DELETE_/api/scheduled-tasks/:id
    --- PASS: TestGatedRoutesRejectMembers/DELETE_/api/users/:id
    --- PASS: TestGatedRoutesRejectMembers/GET_/api/users
    --- PASS: TestGatedRoutesRejectMembers/PATCH_/api/connections/:id
    --- PASS: TestGatedRoutesRejectMembers/PATCH_/api/users/:id
    --- PASS: TestGatedRoutesRejectMembers/POST_/api/connections
    --- PASS: TestGatedRoutesRejectMembers/POST_/api/connections/:id/default
    --- PASS: TestGatedRoutesRejectMembers/POST_/api/connections/:id/regenerate-description
    --- PASS: TestGatedRoutesRejectMembers/POST_/api/connections/:id/reindex-embeddings
    --- PASS: TestGatedRoutesRejectMembers/POST_/api/connections/:id/test
    --- PASS: TestGatedRoutesRejectMembers/POST_/api/connections/:id/test-rag
    --- PASS: TestGatedRoutesRejectMembers/POST_/api/connections/test
    --- PASS: TestGatedRoutesRejectMembers/POST_/api/discord/users
    --- PASS: TestGatedRoutesRejectMembers/POST_/api/lark/users
    --- PASS: TestGatedRoutesRejectMembers/POST_/api/phones
    --- PASS: TestGatedRoutesRejectMembers/POST_/api/users/invite
    --- PASS: TestGatedRoutesRejectMembers/PUT_/api/connections/:id/dsn
    --- PASS: TestGatedRoutesRejectMembers/PUT_/api/discord
    --- PASS: TestGatedRoutesRejectMembers/PUT_/api/lark
    --- PASS: TestGatedRoutesRejectMembers/PUT_/api/settings
PASS
ok  	github.com/fauzanebd/argentum/cmd/api	0.647s
```

```
$ go test ./cmd/api/ ./internal/transport/http/middleware/ ./internal/app/
ok  	github.com/fauzanebd/argentum/cmd/api
ok  	github.com/fauzanebd/argentum/internal/transport/http/middleware
ok  	github.com/fauzanebd/argentum/internal/app
```

Whole tree, race detector on:

```
$ go test -race ./...
# 0 FAIL
```

### Against a live server and a real Postgres

The unit tests run with nil services. This ran `cmd/api` against the local
`argentum_postgres` container, which self-applied `021` on boot
(`control DB migrated to version 21`, `dirty = f`).

Invite → preview → accept → replay → login:

```
=== 2. admin invites a member ===
{"user": {"email": "member…@t04.test", "role": "member", "status": "pending",
          "invite_expires_at": "2026-08-04T14:00:33+07:00"},
 "token": "CtiBZTuhcLG0jnR5DwvHs-SNBG-SXhC_fHnWaLuLr2M"}
=== 3. preview the invite (does not consume) ===
{"invite": {"email": "member…@t04.test", "role": "member", …}}
=== 4. accept -> logged in as member ===
role: member | email: member…@t04.test
=== 5. the same token again ===
  replay -> HTTP 404
=== 6. member logs in normally ===
  login -> HTTP 200
```

Route gate, member token vs admin token, same server:

```
ROUTE                                    MEMBER   ADMIN
GET /users                               403      200
POST /users/invite                       403      201
PUT /settings                            403      204
POST /phones                             403      201
PUT /connections/…/dsn                   403      500
DELETE /connections/…                    403      500
POST /connections/test                   403      200
PUT /discord                             403      200
POST /lark/users                         403      201
DELETE /scheduled-tasks/…                403      404

GET /connections                         200      200
GET /threads                             200      200
GET /usage/summary                       200      200
GET /users/me                            200      200
GET /settings                            200      200
```

The two 500s are the admin arm hitting a connection id that does not exist:
`updateConnectionDSN` and `deleteConnection` return the raw repository error
rather than mapping `ErrNotFound` to a 404. Pre-existing, unrelated to this
ticket, and left alone — but noted, because it is the kind of thing this table
makes visible.

Last-admin guard and the account lifecycle:

```
  demote self -> HTTP 409  {"error":"this is the last admin; promote someone else first"}
  remove self -> HTTP 409  {"error":"this is the last admin; promote someone else first"}
  DELETE /users/$member -> HTTP 204
  member login after removal -> HTTP 403
    {"error":"this account is not active — accept your invitation, or ask an admin to restore access"}

  admin…@t04.test    admin   active
  member…@t04.test   member  deactivated
  x@t04.test         member  pending
```

Stored token shape — 64 hex characters, i.e. SHA-256, not the 43-character
base64url plaintext the admin was shown:

```
    token_hash     | len | accepted
-------------------+-----+----------
 f4550406851192b5… |  64 | t
 1711a953cf5a0ac4… |  64 | f
```

Test data was removed afterwards (`delete from companies where name like 'T04
Test %'`, which cascades).

Four tests carry the gate between them:

- `TestGatedRoutesRejectMembers` — every admin route in the policy, member and
  admin. Member must be 403; admin must not be. "Not 403" rather than "200"
  because the routers in these tests have no database behind them, so *reaching*
  the handler is the signal.
- `TestMemberRoutesAdmitMembers` — the other half. A gate that denied everything
  would pass the test above.
- `TestEveryAuthedRouteIsClassified` — the router's route list against the
  policy, both directions.
- `TestTicketGatedRoutesAreAdmin` — T-04 step 1's own enumeration, pinned
  literally, so a later loosening cannot quietly drop one of the nine findings
  `S-1` and `S-2` named.

## One thing went wrong during verification

The first attempt to run `cmd/api` locally sourced `apps/backend/.env`, which
sets **`DB_HOST=103.76.120.171`** — not localhost, despite the
`argentum_postgres` container being up. `cmd/api` applies migrations on boot,
so `021_user_invites` landed on that remote host before anyone intended it to.

State afterwards: `schema_migrations = 21`, not dirty; 4 users, all 4 with
`activated_at` set by the backfill; `user_invites` empty. The migration is
additive and forward-compatible, so the binary deployed there — which reads
none of the new columns — was unaffected. The repo owner chose to leave it
applied rather than run the down migration.

Recorded here rather than quietly fixed because the lesson is reusable:
**`.env` in this repo does not point at the local container**, and anything
that boots `cmd/api` migrates whatever `DB_HOST` resolves to. Verify locally by
overriding the host explicitly (`DB_HOST=127.0.0.1 DB_PORT=5432`), and read the
`control DB migrated to version N` line back before believing which database
was touched.

## Acceptance

- [x] Member JWT gets 403 on every route listed in step 1 — `TestGatedRoutesRejectMembers`
- [x] Admin JWT succeeds on all of them — same test, admin arm
- [x] Invite → accept → login works end to end — `TestInviteAndAccept`,
      `TestAcceptIsSingleUse`, and the `/accept-invite` page
- [x] Last admin cannot be removed or demoted — `TestLastAdminCannotBeDemotedOrRemoved`,
      `TestDeactivatedAdminsDoNotCountAsCover`

## What this does not do

- **No email.** The invite link is handed to the admin to pass on. A transport
  belongs with the `send_message` action (`T-12a`), not here.
- **No token revocation.** A removed user keeps their access token for up to 15
  minutes. Refresh is closed; the access window is not.
- **Two roles only.** `admin` and `member`. Per-source or per-tool scoping is a
  different feature, and `T-13`'s API keys are where scoped access actually
  gets designed.
- **The ticket mentions "every LLM-credential route".** There are none — the
  `company_llm_credentials` table has a repo and a resolver but no HTTP
  surface. When one lands it needs a policy entry, and
  `TestEveryAuthedRouteIsClassified` will insist on it.
- **Self-removal is not specially blocked.** An admin can deactivate their own
  account as long as they are not the last one. The last-admin rule is what
  keeps a company recoverable; anything beyond that is a UX preference.

## Dashboard

- **Settings → Team** — invite form with a role select, member list with role
  badges and pending/removed status, inline role change, revoke. Visible to
  admins only: every route behind it is admin-gated, so a member would see an
  empty panel and a 403.
- **The other Settings panels render read-only for members** via `AdminGate`,
  which wraps the panel in a disabled `<fieldset>`. That disables every button,
  input and select beneath it natively — fewer edits than threading a prop
  through a dozen components, and harder to forget when a new control is added.
  Data stays visible because every GET in those panels is member-accessible.
- **`/accept-invite?token=…`** — public route. It resolves the token before
  showing the form, so an expired link says so up front rather than after
  somebody has chosen a password twice. Accepting logs the invitee straight in;
  they hold no other credential at that point and the token they arrived with is
  now spent.

None of the above enforces anything. `useIsAdmin` reads a zustand store
persisted to localStorage, which a user can edit freely. It exists to stop
showing members buttons that would answer 403.
