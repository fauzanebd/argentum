# Access grants — who may do what, per person

The plan is
[`../plan/12-access-grants-roadmap.md`](../plan/12-access-grants-roadmap.md). The
role table this track sits on top of is [`rbac.md`](rbac.md) (`T-04`).
**One ticket of nine is built: the capability half of the mechanism. No route
asks for a capability yet, and §2 is why that is the ticket's acceptance rather
than an unfinished table.**

| Ticket | Status |
| --- | --- |
| `T-Z1` A capability is a named power, and a user is granted it | **built 2026-09-12, `make check` green, unit-gated. Migration `083`. Three gates owed — §7** |
| `T-Z2` A resource can be restricted, and a grant is what opens it | Not built. No deps — the next ticket |
| `T-Z3` A route that serves a restricted resource has to say so | Not built. Deps `T-Z2` |
| `T-Z4` Agents: who may talk to which | Not built. Deps `T-Z2` |
| `T-Z5` Dashboards: who may open which | Not built |
| `T-Z6` The "etc": data sources and documents | Not built |
| `T-Z7` Settings → Team: the access matrix | Not built. Deps `T-Z1` (met), `T-Z2` |
| `T-Z8` The other three doors, decided rather than inherited | Not built |
| `T-Z9` The negative suite, and the row that says a refusal happened | Not built |

---

## 1. What was built

A capability is a power with no object — speaking a question, approving an
action, exporting data. An admin grants one to a person; a route can require
one; an admin who was not granted it is refused like anybody else.

| Piece | Where |
| --- | --- |
| The closed vocabulary: `voice`, `approve_actions`, `export_data`, with `Valid()` and a normaliser | `internal/domain/capability.go` |
| The table: `(company_id, user_id, capability, granted_by, granted_at)`, primary key on the first three | `migrations/control/083_user_capabilities.{up,down}.sql` |
| The repository — every statement starts from the `users` row, scoped by company | `internal/adapters/postgres/capability_repo.go` |
| The service: grant, revoke, list, and `Has` behind a ten-second in-process cache | `internal/app/capability_service.go` |
| `RequireCapability`, after `RequireRole` and before the rate limiter | `internal/transport/http/middleware/capabilitypolicy.go`, `cmd/api/router.go` |
| `capabilityPolicy`, the route → capability table — **empty** | `cmd/api/policy.go` |
| Four routes under `/api/users` | `internal/transport/http/handlers/user.go` |
| `Capability` as a union type, `CapabilityGrant` | `packages/api-types/src/domain.ts` (generated) |

| Route | Role | What it does |
| --- | --- | --- |
| `GET /api/users/me/capabilities` | member | The caller's own grants. The user comes from the session, never the path |
| `GET /api/users/:id/capabilities` | admin | Anybody's in the company; `404` for a user of another company |
| `PUT /api/users/:id/capabilities/:capability` | admin | Grant. Idempotent: the second one is a `204`, and the first row's `granted_by` stands |
| `DELETE /api/users/:id/capabilities/:capability` | admin | Revoke. Idempotent: revoking what is not held is a `204` |

A refusal is a `403` whose body names the capability —
`{"error": "an admin has not granted you this", "capability": "voice"}` — so
`T-Z7` can say which grant to ask for rather than rendering a bare "forbidden"
that reads exactly like the role table's. A grant store that cannot be read is a
`503`, not a `403`: the caller should retry, not be told they lack a grant they
may hold.

## 2. The table ships empty, and the ticket could not have meant otherwise

The ticket's day-one vocabulary is `voice`, `approve_actions` and `export_data`.
Its acceptance also says **"every route that exists today behaves identically"**,
and its migration says **"no backfill — nobody has anything on day one"**.

Two of those three capabilities name things routes already do:
`export_data` is `GET /api/company/data/export` and `approve_actions` is
`POST /api/actions/:id/approve` and `/reject`. **Gating any of them on its
capability without a backfill 403s every admin who exports and every member who
approves, on their very next request.** So all three are true only if nothing is
gated yet — which is how it was built. The vocabulary exists so an admin can
prepare grants before a route asks; `voice`'s first route is roadmap 11's
`T-W7`.

This is written down in three places so it cannot be undone by accident:

- beside `capabilityPolicy`, which says why it is empty;
- `TestExistingRoutesAskForNoCapability`, which pins the three tempting routes and
  fails with *"ship the backfill that grants it to whoever does this today"* if
  one is moved behind its capability;
- the test's second half, a sweep of every classified route as an admin against
  a store that has granted nothing, asserting the store is **never read**.

The acceptance lines about a capability-gated route are proven against
synthetic routes in the middleware's own tests, and the real-router version,
`TestCapabilityGatedRoutesRefuseAnAdminWithoutAGrant`, skips with the reason
until `T-W7` adds the first gated route. From then on every new gate is covered
without writing a new test.

**Whoever gates `approve_actions` should know one more thing:** approval is
already refined per action kind by `company_actions.allowed_roles`, inside the
handler. A capability would be a third layer on that route, and the backfill has
to grant it to exactly the people `allowed_roles` admits today — which is a
per-kind set, not a role.

## 3. "A short cache" and "the next request", both true

The ticket asks for capabilities *"read per request behind a short cache"* and,
three lines later, for *"a revoke takes effect on the next request"*. A cache
with a TTL makes the second false for up to the TTL. Both hold, and here is the
exact claim:

- **A grant or revoke clears the cache entry it touched**, so on the replica that
  served the write the next request re-reads.
- **A generation counter closes the one race that would break that on a single
  replica:** a load that read "held", then a revoke landing before that load
  stored its result. The load only stores if no write happened while it was
  out. `TestCapabilityLoadThatRacedARevokeIsNotCached` drives exactly that
  interleaving.
- **On a replica that did not serve the write, the bound is the TTL — ten
  seconds** (`TestCapabilityWriteElsewhereIsSeenWithinTheTTL`). This deployment
  runs **one** API replica (`kubectl get deploy`, 2026-09-12:
  `argentum-api 1/1`), so today the first bullet is the whole story. The day
  there are replicas, either ten seconds is acceptable for a boundary the
  roadmap calls one against accident, or the cache moves to Redis.

Why not the JWT, which the ticket also rules out: an access token lives fifteen
minutes, and the team page already tells an admin that a removed member *"loses
access within 15 minutes"*. A revoke on that clock is not a revoke.

`ListForUser` — the settings page's read — is **uncached**, so a page that just
toggled a grant is never shown the state from before the toggle.

## 4. The tenant check the ticket did not list

`T-Z2` has a cross-company acceptance line; `T-Z1` has none, and it needs one.
`user_capabilities.user_id REFERENCES users(id)` proves the user exists, **not
that they belong to `company_id`** — so a bare `INSERT` with an admin's company
and another company's user id writes a valid row naming a stranger.

Every statement therefore starts from `users WHERE id = $2 AND company_id = $1`.
Grant and revoke do it in one statement: a data-modifying CTE runs whether or
not the outer query reads it, so `SELECT count(*) FROM target` answers "is this
a user of the company" with no window between the check and the write. The list
uses a `LEFT JOIN` from `users`, which yields one all-`NULL` row for a user
holding nothing and no row for a user who is not there — `[]` against `404`,
without a second round trip. A malformed id (SQLSTATE `22P02`) is mapped to
not-found rather than surfacing as a 500 carrying the driver's message.

`TestCapabilityCrossTenantUserIsNotFound`, `TestCapabilityCacheDoesNotCrossCompanies`
and `TestCapabilityRoutesDoNotReachAnotherCompanysUser` prove the behaviour
against fakes that reproduce the join. **The SQL itself has not run** (§7).

## 5. Where the ticket was wrong, or silent

- **`RequireCapability(capabilityPolicy)` cannot be the signature.** A role is on
  the token; a grant is data, so the middleware takes a checker too:
  `RequireCapability(capabilityPolicy, d.capabilitySvc)`.
- **`Repo: BE + FE`, and no FE item in *Do*.** The frontend half of this ticket
  is the generated `Capability` union and `CapabilityGrant` interface. The
  surface an admin uses is `T-Z7`'s, and building a toggle here would have been
  building `T-Z7` early without `T-Z2`'s half of it.
- **The day-one vocabulary and "behaves identically"** — §2.
- **"A short cache" and "the next request"** — §3.
- **No cross-company acceptance** — §4.
- **Decision 12's audit rows are `T-Z9`'s, not this ticket's.** `T-Z1` records
  `granted_by` on the row and writes an `Info` line per grant and revoke with
  `company_id`, the user, the capability and who did it. A revoke leaves no row
  behind, so until `T-Z9` the log line is the only record that one happened.
- **`Migration: 083` was right**, and was still free when claimed.

## 6. Acceptance, quoted back

| Acceptance | Evidence |
| --- | --- |
| A member without the capability gets 403 on a capability-gated route | `TestRequireCapability/a_member_without_the_grant_is_refused` — against a synthetic gated route; no real one exists (§2) |
| An **admin** without the capability also gets 403 | `TestRequireCapability/an_admin_without_the_grant_is_refused_too`. The real-router arm, `TestCapabilityGatedRoutesRefuseAnAdminWithoutAGrant`, **skips** until `T-W7` |
| A revoke takes effect on the next request, with no re-login | `TestCapabilityRevokeTakesEffectOnTheNextRequest` (clock standing still, entry freshly cached) and `TestCapabilityLoadThatRacedARevokeIsNotCached`. Bounded at ten seconds across replicas — §3. **Live arm owed** |
| Granting twice is idempotent, not an error | `TestCapabilityGrantTwiceIsIdempotent`, `TestGrantingTwiceIsIdempotent` (two `204`s, one row, first granter kept). The `ON CONFLICT` has not run against Postgres |
| An unknown capability string is refused at the API boundary, not stored | `TestGrantingAnUnknownCapabilityIsRefusedAndNotStored` (`400`, store never called), `TestCapabilityUnknownIsRefusedBeforeTheStore` |
| Every route that exists today behaves identically | `TestGatedRoutesRejectMembers` and `TestMemberRoutesAdmitMembers` now run with a capability store that has granted nothing to anybody, so a route that had started asking would fail them; `TestExistingRoutesAskForNoCapability` asserts the store is never read |

Beyond the ticket: a grant cannot admit a caller the role table refused, and the
store is not even read for that request
(`TestRequireCapability/a_grant_cannot_admit_a_caller_the_role_table_refused`);
a failing store is a 503, not a pass; a missing checker or a chain without
`Auth` is a 403; `me` cannot be pointed at another user; holding nothing is
`[]`, never `null`.

## 7. Owed

In [`live-gate-backlog.md`](live-gate-backlog.md) §7c, with predictions:

1. `083` up, `down 1`, up against a control-plane Postgres.
2. The repository's three statements against a real Postgres with two
   companies — the arms in §4 that only a database can prove.
3. Two real users and a revoke. The route half can run on the stack today; the
   **middleware** half cannot run anywhere until a route asks for a capability.

No `make eval` is owed: nothing here reaches a prompt or a tool.
