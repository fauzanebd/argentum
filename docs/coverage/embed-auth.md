# T-19 — Embed auth: keys, HMAC identity, session tokens

**Built 2026-08-09.** The security foundation of the widget phase: everything
`T-20`→`T-23` does assumes a session token was minted for a real visitor of a
real tenant, and this is the only place that decision is made.

Migration `051_embed_keys`. Decision that scheduled it:
[`../plan/00-sprint-overview.md`](../plan/00-sprint-overview.md) §9. What fired
the trigger: [`gelael-pilot.md`](gelael-pilot.md).

---

## 1. The shape

An embed key is **two halves that travel in opposite directions**, which is the
whole difference from `T-13`'s API key:

| | API key (`T-13`) | Embed key (`T-19`) |
| --- | --- | --- |
| Public half | none — the token is the secret | `argw_pub_<hex>`, printed in the tenant's page source |
| Secret half | held by the tenant's server, sent on every call | held by the tenant's server, **never sent** — it signs |
| Authorises by | the secret alone | an allowlisted origin **plus** an HMAC over the asserted identity |
| Reaches | every `/v1` route its scopes name | one 15-minute session, for one asserted person |
| Storage | SHA-256 of the secret | AES-256-GCM **encryption** of the secret (§2) |

There are deliberately **no scopes** on an embed key. An embed session reaches
exactly the `/api/embed` routes `T-20` registers; a per-key capability set would
be a second expression of a decision the route table already makes, and the two
would drift.

## 2. The deviation the ticket forced

The ticket asks for `secret_hash` (Argon2id) **and** for
`HMAC-SHA256(secret, "<user_ref>:<exp>")` recomputed on our side. Those are not
jointly satisfiable: an HMAC cannot be recomputed from a hash of its key.

Of the two, the HMAC **is** the security model — it is what stops a page forging
an identity — so the storage gave way. The secret is sealed with the same
AES-256-GCM cipher that protects every tenant DSN, under `ARGENTUM_DSN_KEY`.

**What that costs, stated plainly:** a database dump *plus the deployment key*
yields signing secrets. That is the same exposure a dump plus the key already
yields for every warehouse credential in `connections`, so it adds no new class
of compromise — but it is strictly weaker than `api_keys`, where a dump alone is
useless. Recorded here for the same reason `T-13`'s SHA-256-instead-of-Argon2id
deviation is recorded in [`api-keys.md`](api-keys.md): a security decision that
differs from its ticket has to be findable.

## 3. The mint, in order

`POST /api/embed/session` and `/session/refresh`. Both are outside
`middleware.Auth` by construction — the caller is a visitor of somebody else's
website and has no Argentum account.

1. **Shape.** A client key that is not `argw_pub_` + 32 hex costs a string
   comparison, not a database round trip.
2. **The key.** Unknown, revoked or disabled → `401`. One answer for all three.
3. **The origin.** Exact `scheme://host[:port]`, case-normalised, default port
   folded. Not on the list → `403`, logged **with the offending origin and the
   allowlist**, because that is the refusal a legitimate integrator hits
   constantly and the entire cost of debugging it is knowing what was compared.
4. **The signature.** `hmac.Equal`, never `==`.
5. **The deadline.** Not in the past, not more than 24h ahead — checked *after*
   the signature, so a caller who does not hold the secret cannot use the mint
   as an oracle for which timestamps are acceptable.
6. **The token.** 15 minutes by default (`EMBED_SESSION_TTL_MINUTES`).

## 4. Four decisions worth keeping

### 4.1 The session token carries no `sub`

`auth.Claims.UserID` reads the `sub` claim. The first version of `EmbedClaims`
set `Subject: "embed:" + ref` — namespaced, and still wrong: **a test caught it
holding a user id** when the token was parsed with the dashboard's own struct.
`middleware.Auth` refuses an embed token on `typ` before it ever reads a user
id, so nothing was reachable — but that left exactly one check standing between
a website visitor and an identity. `sub` is now empty and the identity lives in
`ref`, which no dashboard claim reads. Two independent reasons a stranger cannot
become a user, instead of one.

### 4.2 The origin check is not a suffix test

`strings.HasSuffix(origin, "acme.com")` admits `https://evil-acme.com`, and an
attacker who can register a domain then holds a session for somebody else's
tenant. Both sides go through one canonicaliser, and `embed_key_test.go` pins
ten refusals including `https://acme.com.evil.test` and `https://sub.acme.com` —
a subdomain is a different origin by the spec, and quietly admitting it would be
a policy nobody wrote down.

`http://` is refused except for loopback. A session token on a plain-text origin
is a session token in transit, and loopback is the one place a tenant cannot
have TLS.

### 4.3 `/api/embed` has its own CORS, and it reflects

The engine's CORS middleware skips `/api/embed`. The dashboard's list is a fixed
set of hosts we operate; this one is *every site any tenant has allowlisted*, a
set that changes when an admin edits a key and that no env var can hold.

`EmbedCORS` reflects the `Origin` and sends **no**
`Access-Control-Allow-Credentials`, so a browser carries no ambient authority
here. CORS decides who may read a response; it is not the access control. The
access control is step 3 above, and an origin nobody allowlisted reads a `403`
with no token in it.

The failure mode this avoids is worth naming: a fixed allowlist here would mean
every new tenant site needs an Argentum deploy, and that pressure is how a `*`
ends up in a CORS header on a route that mints sessions.

### 4.4 Two rate buckets, neither shared

`rl:embedmint:` is keyed on the client address, because at mint time no identity
has been verified yet — that is what the route is for. `rl:embed:` is keyed on
`(company_id, embed_user_ref)`, the **pair**: `emp_1` is a different person at
every company, so keying on the ref alone would let one tenant's traffic exhaust
another's and would leak one tenant's volume to another through refusals.

## 5. What was gated, and what was not

**Run and passing:**

- `go test -race ./...` — 61 packages, 0 failures.
- `golangci-lint run ./...` — 0 issues.
- The ticket's matrix: `{valid, tampered user_ref, tampered signature, bad
  origin, no origin, expired, exp >24h, revoked, disabled, unknown key,
  malformed key, empty user_ref} × {session, refresh}` in
  `internal/app/embedkey_service_test.go`.
- Cross-family refusal, both directions: an embed token is refused by
  `middleware.Auth` + `RequireRole`; a dashboard access token and a refresh
  token are refused by `EmbedAuth`.
- Wildcard and empty allowlists cannot be saved, through **create or update**.
- `tsc -b` clean on the dashboard.

**Run 2026-08-10, against the local stack.** The debt this section listed is
paid, and the gate found nothing:

| Gate | Result |
| ---- | ------ |
| `051` up, then **down**, then up again against a real Postgres | Pass. Down removed the table and both columns cleanly (`to_regclass` null, 0 columns); the re-apply restored the partial index and the jsonb default. Schema had been at 50 — none of the three had ever met a database |
| The mint matrix over HTTP, eight cases | Pass, and identical to the unit tests: valid → 200; suffix-lookalike origin → 403; **no `Origin` header → 403**; tampered `user_ref` → 401; forged signature → 401; expired `exp` → 401; `exp` 25h out → 401; unknown client key → 401 |
| The allowlist refusing to store a wildcard, an empty list, or plain `http` | Pass, all three 400 with the sentence that explains the rule |
| Revoke biting immediately | Pass. `DELETE` → 204, and the next mint with a valid signature → 401 |
| The token's claims | Pass. `{cid, ref, kid, typ:"embed"}` — **no `sub`, no `role`**, which is §4.1 proven rather than asserted |
| CORS on the mint | Pass. `Access-Control-Allow-Origin` reflects the caller, `Vary: Origin`, and **no `Allow-Credentials`** |
| Cross-family, both directions | Pass. Embed token on `/api/threads` and on `/api/embed-keys` → 401 `wrong token type`; dashboard token on `/api/embed/config` → 401 `invalid embed session` |

**What is still owed** is only what needs a human at a browser: the panel
rendering in Chrome, Safari and Firefox, the mobile sheet under 640px, and a
real cross-origin preflight from a page on a second origin. Everything a
`curl` can reach has now been reached.

## 6. What T-20 inherits

- `middleware.EmbedAuth` sets `company_id`, `embed_user_ref` and `embed_key_id`,
  and sets **no** user id and **no** role.
- `tenantctx` carries `actor_kind=embed` / `actor_ref=<embed_user_ref>`, so
  `T-05`'s audit rows attribute a widget turn without a line of audit code —
  `domain.ActorKindEmbed` is in the `Valid()` switch.
- `RateLimiter.EmbedMiddleware()` is written and unused until `/api/embed/chat`
  exists.
- `EMBED_ENABLED` already gates the mint; `T-20` extends it to the rest of the
  group, and `EMBED_MAX_TURNS_PER_HOUR` is read but not yet enforced anywhere —
  wiring it is `T-20`'s, and it is named here so it does not read as a setting
  that works.
