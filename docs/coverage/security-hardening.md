# Security hardening Track A + `T-H15` — `T-H1`, `T-H2`, `T-H3`, `T-H15`

**Built 2026-08-11.** Roadmap:
[`../plan/03-security-hardening-roadmap.md`](../plan/03-security-hardening-roadmap.md).

`T-H1` was a live authentication bypass rather than a hardening idea, so this
record leads with what was reachable before the change and what it now costs to
reach it. Tracks B, C and D are untouched.

---

## 1. `T-H1` — WhatsApp webhook authentication

### What was reachable

`/webhook/whatsapp` is mounted outside `middleware.Auth`
(`apps/backend/cmd/api/router.go:278`), correctly: the caller is Meta or Twilio
and holds no Argentum credential, so the signature **is** the authentication.
There was not one. Three failures compounded, and a fourth that the roadmap does
not name:

1. **The verdict was ignored.** `webhook.go:63` called `VerifyWebhook`, logged
   `"invalid Twilio signature (continuing in dev mode)"`, and fell through.
2. **The verifier did not exist.** `computeTwilioSignature` was
   `return "" // Placeholder - implement if needed` (`twilio.go:194`), so every
   comparison was against the empty string.
3. **The branch was chosen by the caller.** `webhook.go:52` selected the Twilio
   path when `X-Twilio-Signature` was present *or* the content type was
   `application/x-www-form-urlencoded`. One header routed a Meta deployment into
   the verifier that did not exist.
4. **`ENABLE_WHATSAPP` is read by nothing.** It appears only in
   `apps/backend/.env.example:51`; no Go file references it. The route is mounted
   on every deployment, including ones whose operator believes WhatsApp is off.
   This is the finding that decides how widely the bypass applied, and it is not
   in the roadmap.

Both clients also failed open on an unset secret (`twilio.go:178`,
`client.go:46`), which the roadmap does name.

### What ships

| Change | Where |
| ------ | ----- |
| Real Twilio verification — HMAC-SHA1 over URL + parameters sorted by name, base64, `hmac.Equal` | `internal/whatsapp/twilio.go` |
| Both clients fail **closed** on an unset secret | `twilio.go`, `client.go` |
| Verification failure is `401` and stops | `handlers/webhook.go` |
| The provider is read from config, never from the request | `whatsapp.Transport` + `ResolveTransport`, `handlers/webhook.go`, `cmd/api/{bootstrap,deps,router}.go` |
| The signed URL carries the query string | `handlers/webhook.go` |
| The GET handshake is Meta-only, constant-time, and refuses an unset token | `handlers/webhook.go`, `client.go`, `twilio.go` |

### The algorithm, settled

The comment at `twilio.go:192` specified `Base64(HMAC-SHA256(authToken, url + body))`.
That is wrong in both the hash and the message. Twilio signs **HMAC-SHA1** over
the request URL followed by the POST parameters sorted by name, each name
written immediately before its value with no delimiter, keyed by the account's
auth token, base64-encoded.

`TestTwilioSignatureMatchesTwiliosOwnExample` pins Twilio's published worked
example rather than a value this implementation produced — an implementation
tested against itself proves its two halves agree, not that either matches the
platform we have to accept requests from. The vector is also reproducible
without any Go at all:

```
$ printf '%s' 'https://example.com/myapp.php?foo=1&bar=2CallSidCA1234567890ABCDECaller+14158675310Digits1234From+14158675310To+18005551212' \
    | openssl dgst -sha1 -hmac '12345' -binary | openssl base64
L/OH5YylLD5NRKLltdqwSvS0BnU=
```

### Two decisions worth carrying forward

**The transport is a config value, not an interface method.** The obvious shape
is `Provider.Kind()`, and it is not what shipped: adding a method to
`whatsapp.Provider` reaches `fakeWA` in `internal/app/watcher_service_test.go`,
a file another track owns. `ResolveTransport` is the switch `NewProvider` was
already making, lifted out so the handler and the client read the same one — the
drift between them is what `T-H1` was.

**The signed URL fixes `https://` rather than reading the scheme.** A TLS
terminator in front of this process leaves `c.Request.TLS` nil on a request
Twilio made over `https`, so sniffing would break verification in exactly the
deployment shape that matters. `c.Request.Host` is caller-controlled, which
costs nothing here: an attacker who cannot compute the HMAC gains nothing from
choosing which URL string we hash.

---

## 2. `T-H2` — Lark webhook, unconditional verification

`lark_webhook.go:89` verified the signature only `if sig != ""` and `:111`
checked the verification token only `if env.Header.Token != ""`. Both are fields
the caller writes: omit the header, skip the check.

Both are now unconditional. The signature check remains conditional on
`cred.EncryptKey != ""` — that is not the same thing, and it is correct: Lark
signs only when the app has an encrypt key, so requiring a signature from a
tenant without one would refuse every genuine callback. The condition is now on
a stored fact rather than on a supplied header.

**The residual the ticket does not name, closed here.** With `EncryptKey` and
`VerificationToken` both empty, an unconditional token check is `"" == ""` and
still admits everyone who knows the app id. That row now gets a `401` before the
body is read, and `tokenMatches` refuses an empty stored token independently.
Both comparisons moved to `hmac.Equal`.

---

## 3. `T-H3` — fail-closed configuration

All three rows, gated on `config.IsProduction()`. The refusals are
production-only by the ticket's own wording, and the request-time behaviour is
fail-closed everywhere: a development box boots without a webhook secret and
answers `401`.

| Row | Now |
| --- | --- |
| Provider webhook secret | `WHATSAPP_APP_SECRET` **warns** in production for the Meta provider; unset, every callback is `401`, so inbound WhatsApp is off rather than open. Twilio needs no separate rule — its signing key is `TWILIO_AUTH_TOKEN`, already required unconditionally by the existing triple. |
| `CORS_ORIGINS` empty | **Warns** in production. `Access-Control-Allow-Credentials` is now sent only alongside an `Allow-Origin` we actually issued. |
| Tenant DSN without TLS | `disable`/`prefer`/`allow` refused in production on the form path; the raw-DSN path checked for the same property; SQL Server floors at TLS 1.2 and encrypts by default, and verifies only when asked to by name. |

### WhatsApp no longer stops a deployment that does not use it

Not a `T-H3` row, and older than this whole track. `WHATSAPP_PROVIDER` defaults
to `whatsapp_business`, and `Validate()` required `WHATSAPP_ACCESS_TOKEN` and
`WHATSAPP_PHONE_NUMBER_ID` whenever that provider was selected — so **every
deployment had to hold WhatsApp credentials, including the ones that have never
sent a WhatsApp message.** `config.Load()` returns the error and
`cmd/api/main.go:30` is a `Fatalf`, so the process did not start.

This is the trap [`report-video.md`](report-video.md) §6 recorded as *"the API
refuses to boot without WhatsApp credentials on a deployment that uses no
WhatsApp"*, and which the 2026-08-11 agent-quality gate had to work around
again. It was filed as an environment note both times rather than as a defect.

The rule is now about intent rather than about a default:

| State | Behaviour |
| ----- | --------- |
| No `WHATSAPP_*` variable set | Channel off. Boots silently — there is nothing to say about a channel nobody asked for. |
| Some set, some missing | Boots, with a warning naming the missing variable. |
| `WHATSAPP_PROVIDER=twilio`, triple incomplete | Boots, with a warning. Selecting the provider explicitly is intent, so the warning always fires. |

**Proven, not asserted:** the API binary was run against the local stack with
`env -i` and no WhatsApp variable of any kind. It passes `config.Load()` — the
`Argentum API server starting` line at `main.go:33` is printed *after* the
`Fatalf` on `:30` that used to catch this — and goes on to fail at the control
database, whose local volume password predates the missing `.env`. The
config half is what this change owns and it is the half that is proven; a
complete boot is owed with working database credentials.

### Two rows were fatal for four hours, and are warnings — decided 2026-08-11

The repo owner reverted both boot refusals after the change was pushed, and the
reasoning is worth keeping because it is not a disagreement about the security:

**A config check that stops the process converts a security fix into an outage
on the very rollout that carries it.** `WHATSAPP_APP_SECRET` and `CORS_ORIGINS`
were tolerated by the previous release, so the first deployment to enforce them
is the one that fails to come up, and it fails at the worst moment — during a
rollout whose changelog is about webhooks. Both now log at Warn and boot.

**What this costs, stated plainly rather than softened.** The WhatsApp row costs
nothing at all: `VerifyWebhook` answers false without a secret, so the endpoint
is `401` either way and the warning only tells an operator why their inbound
messages stopped. The `CORS_ORIGINS` row **does** leave a real hole open — an
empty list reflects every `Origin` with credentials, and the dashboard
authenticates with an `at` cookie, so any site a logged-in user visits can read
their authenticated responses. That is a deployment-configuration problem now,
not a code one, and the warning names it in those words.

**The residual is a deployment check, not a ticket:** confirm `CORS_ORIGINS` is
set on every production deployment. It is the one item here a green build cannot
prove.

### The CORS claim, stated more precisely than the roadmap does

The roadmap reads `cors.go:39` and `:44` together — reflection *plus* an
unconditional `Allow-Credentials`. A browser requires the **pair**, so the
unconditional header on its own granted nothing; the exposure is specifically
the empty-list reflection at `:39`, which the production refusal closes. Pairing
the two headers is a tidiness fix, not the security fix, and is recorded as such
so nobody later reads it as the thing that mattered.

**Found while fixing it:** `CORS_ORIGINS` is matched **literally**, so a
deployment that sets `CORS_ORIGINS=*` expecting a wildcard gets no CORS headers
for any real origin. Pre-existing, in no ticket, not changed here — it is a
behaviour change to a public surface and belongs to whoever owns that decision.
It surfaced because `cmd/api`'s test router uses `[]string{"*"}` and its CORS
assertion had been passing only on the unconditional credentials header.

### SQL Server: the breaking change, named — and then not taken

`buildDSN` pinned `TrustServerCertificate=true` and `tlsmin=1.0`
(`company.go:172-173`) with no way to say otherwise. That is encryption against
somebody listening and nothing at all against somebody answering — anything that
can reach the address can present any certificate it likes.

SQL Server now reads the same `ssl_mode` field the other two drivers do:

| `ssl_mode` | DSN |
| ---------- | --- |
| unset, `require`, `skip-verify` | `encrypt=true&TrustServerCertificate=true` |
| `verify-ca`, `verify-full` | `encrypt=true&TrustServerCertificate=false` |
| `disable` | `encrypt=disable` (refused in production) |

`tlsmin` is `1.2` in every case, which is the half of the original change that
survived and the half nothing has to opt into.

**The default was `TrustServerCertificate=false` for four hours and is `true`
again, by the repo owner's decision on 2026-08-11.** Verification is the right
end state and the cost of getting there this way was wrong: a self-signed
certificate is what a default SQL Server installation presents, so the change
breaks most on-prem tenants — and it breaks them at the *next DSN edit* rather
than at the rollout, because `buildDSN` runs at registration and stored rows are
untouched. An admin editing a password six weeks later would have met a TLS
error with nothing connecting it to a default that moved underneath them.

So verification is opt-in by name: `verify-ca` and `verify-full` mean what they
say, and `require` means "encrypt" — the same reading Postgres gives it, which
is why all three drivers spell the two ideas as separate words.

**What is genuinely still owed** is the question the roadmap already asks:
establish whether any live tenant is on SQL Server. If none is, this default can
move with no migration and no warning; if one is, it moves with both. That is a
five-minute query against the control database and it has not been run.

Existing rows are untouched: `buildDSN` runs at registration, not at read.

### The raw-DSN floor

"Paste your own DSN" was the documented way around every transport rule the form
enforces, because the raw string is returned verbatim. In production it is now
read first, and the rule is **"does it enable TLS, in so many words"** rather
than "does it disable TLS" — none of the three drivers defaults to a mode that
cannot end in plaintext. libpq's `prefer` negotiates TLS and falls back silently;
`go-sql-driver` sends none unless `tls` is set; `go-mssqldb`'s `encrypt` default
has moved between major versions. A DSN that names nothing is refused with the
parameter it wants.

`dsnParamValues` reads one key out of any of the three grammars — they all write
`key=value` and separate on `&`, `;` or whitespace — rather than carrying three
parsers, including libpq's keyword form, for one string comparison. Every
occurrence has to pass, not the first: reading the wrong one in the permissive
direction is the only mistake here that matters.

---

## 4. `T-H15` — the callback egress window

`CheckResolvedTarget` resolved the host and refused unless **every** answer
passed `checkIP` — sound, and checking all answers rather than the first is the
right call. Then `sender.go:197` built a request from the same URL *string* and
`:208` handed it to a plain `&http.Client{Timeout: 10 * time.Second}`, which
resolved the name a second time inside `http.Transport`. Between the two
lookups the answer can change.

**The fix.** `ResolveTarget` returns the addresses it approved, and the delivery
dials through a `DialContext` that connects to one of *them*, taking the port
from the URL and discarding the host. `checkIP` runs again inside the hook, which
is redundant on every path that reaches it today and deliberately so: that hook
sees the address the stack is about to connect to.

Three shape decisions:

- **`Resolver` is an interface satisfied by `*net.Resolver` as written.**
  Production passes `net.DefaultResolver`; `WithResolver` exists for this
  ticket's gate and nothing else. A rebinding window is a resolver that answers
  differently on the second call, and no real DNS name can be asked to open one
  on cue.
- **Resolution no longer short-circuits on `allowPrivate`.** It previously
  returned before the lookup; now `allowPrivate` only relaxes *which* addresses
  are acceptable. A development deployment wants a predictable dial as much as a
  production one, and the gate needs the pin observable against a loopback
  listener it owns.
- **A client per attempt, with no proxy.** The pin belongs to this URL's
  resolution; one shared transport reading the pin off the request context buys
  connection reuse at the price of arguing about which pooled connection a later
  delivery to the same host may reuse. **`http.DefaultTransport` honoured
  `HTTPS_PROXY` and this does not** — a proxy resolves the name itself, so a
  pinned dial through one pins the proxy's address and the guard reads as present
  while doing nothing. A deployment that must egress through a proxy has to
  enforce this at the proxy; it will find out from a failed dial. This is the
  one behaviour regression in this change and it is deliberate.

---

## 5. The gate

Run 2026-08-11 from the repo root.

```
$ make vet
cd apps/backend && go vet ./...
                                          → clean

$ make lint-go
cd apps/backend && golangci-lint run ./...
0 issues.

$ make test          # go test -race -count=1 ./...
                                          → exit 0; 48 packages ok, 0 FAIL
ok  github.com/fauzanebd/argentum/cmd/api                                2.848s
ok  github.com/fauzanebd/argentum/internal/config                        2.663s
ok  github.com/fauzanebd/argentum/internal/transport/http/handlers       2.486s
ok  github.com/fauzanebd/argentum/internal/transport/http/middleware     1.965s
ok  github.com/fauzanebd/argentum/internal/webhookout                    2.209s
ok  github.com/fauzanebd/argentum/internal/whatsapp                      1.747s

$ make build         # go build ./... + pnpm -r build
                                          → exit 0
```

The tree was shared with two other tracks while this ran, so the full `make
test` output includes their packages; the six above are this change's.

### What each ticket's own Test section asked for, and where it is proven

**`T-H1`** — `internal/transport/http/handlers/webhook_test.go`,
`internal/whatsapp/signature_test.go`:

| Case | Result |
| ---- | ------ |
| A signed Twilio request passes | `200`, and the allowlist is consulted exactly once — the proof it got past authentication rather than being dropped early |
| The same body with one byte changed | `401` |
| The `From` changed after signing | `401` |
| No signature header, empty header, non-base64 header | `401` |
| A signature captured for a different host | `401` |
| A form-encoded request against a Meta deployment | `401`, including with a genuinely valid Twilio signature |
| A Meta-signed JSON callback against a Twilio deployment | `401` |
| A signed Meta request | `200` |
| The GET handshake on Twilio | `403` |
| Twilio's own published signature vector | matches |
| Either client with no secret configured | refuses |

Every refusal also asserts the allowlist was **not** consulted and no message
was sent: a `401` that had already resolved a company would have touched tenant
state on the way to refusing.

**`T-H2`** — `internal/transport/http/handlers/lark_webhook_test.go`: a signed
and tokened request passes; a missing signature is a failed signature; a wrong
key, a changed timestamp, a changed nonce, an empty header and a malformed
header are each `401`; a missing verification token is a failed token; a valid
signature with a wrong token is still `401`; `url_verification` refuses a wrong
and an omitted token; a tenant row with neither credential is refused.

**`T-H3`** — `internal/config/config_test.go`,
`internal/transport/http/middleware/cors_test.go`,
`internal/transport/http/handlers/connection_dsn_test.go`: production refuses to
validate without `WHATSAPP_APP_SECRET` or `CORS_ORIGINS` and validates with
them; development validates without either; credentials pair with an allowed
origin and are absent for a refused one; the plaintext modes are refused per
driver while the encrypting ones still build; sixteen raw-DSN cases across three
grammars; SQL Server's four modes.

**`T-H15`** — `internal/webhookout/rebind_test.go`: a resolver that answers
`127.0.0.1` once and `169.254.169.254` afterwards delivers to the listener on
`127.0.0.1`, and is asked exactly once; a resolver whose first answer set
contains `169.254.169.254` is refused before any dial, with `link-local` in the
recorded error; `pinnedDial` handed a live loopback address and a public pin
connects to neither the loopback nor anything else, and the listener accepts
zero connections; `pinnedDial` refuses a link-local pin; `ResolveTarget` returns
both approved answers, consumes no lookup for an IP literal, and fails an
unresolvable name.

---

## 6. Roadmap claims re-verified

Every `file:line` cited by `T-H1`, `T-H2`, `T-H3` and `T-H15` was checked before
being acted on. **All of them were correct** — including the six the
2026-08-11 correction log had already moved. The roadmap's citations can be
trusted for this track.

Four things it did not say:

1. `ENABLE_WHATSAPP` is read by no Go file. `/webhook/whatsapp` is mounted
   unconditionally, so the bypass applied to deployments that believed the
   channel was off.
2. `WhatsAppClient.VerifyToken` was `token == challenge`, so an unset
   `WHATSAPP_WEBHOOK_VERIFY_TOKEN` completed the GET subscription handshake for
   a caller who sent no token — a third fail-open in the same file as the two
   the ticket lists. `TwilioClient.VerifyToken` returned `true` unconditionally,
   so a Twilio deployment echoed any `hub.challenge`.
3. The URL the Twilio check would have signed omitted the query string
   (`c.Request.URL.Path`), which would have failed verification for any tenant
   whose console URL carries one. Invisible until the verifier existed.
4. `T-H2`'s "make both checks unconditional" leaves the both-credentials-empty
   row open, because an unconditional `"" == ""` is still true.

---

## 7. Departures from the tickets

| Departure | Argument |
| --------- | -------- |
| `T-H1` asks to "select the provider from config" — done via a `Transport` value passed to the handler, not a method on `whatsapp.Provider` | The interface has a fake in `internal/app/watcher_service_test.go`, owned by another track. `ResolveTransport` keeps one switch, which is the property the ticket actually wants. |
| `T-H1` asks for an unset secret to be "fatal at startup". It is a **Warn** in every environment; `401` at request time everywhere | Two decisions stacked. Fatal-in-production was the first (an unconditional fatal stops a dev stack that never configured WhatsApp from booting). The repo owner then reverted the production fatal on 2026-08-11: a check that refuses to start converts a security fix into an outage on the rollout carrying it. The endpoint is closed in every case; only the boot differs. |
| `T-H3` asks for `CORS_ORIGINS` to be fatal in production. It is a **Warn** | Same decision, same date, and unlike the row above this one leaves a real hole open — see §"Two rows were fatal for four hours". It is now a deployment check rather than a code guarantee. |
| `T-H3` asks for the SQL Server certificate to be verified. It encrypts by default and verifies on `verify-ca`/`verify-full` | Same decision, same date. Verifying by default breaks every tenant on a self-signed certificate, at their next DSN edit rather than at the rollout. `tlsmin=1.2` survives unconditionally. |
| `T-H3` row 3 is implemented in `handlers/company.go`, outside this track's declared file ownership | The row cannot be implemented anywhere else — `buildDSN` is where the DSN is composed. The change is contained to `buildDSN`, one constructor option, and its own test file. |
| `T-H15` resolves even when `allowPrivate` is set, where the previous code returned early | The pin is the point, and it is not a property only production should have. It also makes the fix observable against a loopback listener, which is the only kind of listener a test owns. |
| `T-H15` drops `HTTPS_PROXY` support | Stated in §4. A pinned dial through a proxy pins the proxy. |

One test outside this track's files was edited: `cmd/api/v1_test.go`'s CORS
assertion, which had been passing on the unconditional `Allow-Credentials`
header. It now asserts against an origin the test router actually allowlists.

---

## 8. What is still owed

**The live half.** Everything above is unit-level. The roadmap's "What is owed"
asks for each of these against a running stack. **`T-H1`'s was run on 2026-08-14
— see §9**; the rest have not been:

- ~~`T-H1` — a forged form POST against a running API~~ **run 2026-08-14, §9.**
  What it proved is the branch selection and the fail-closed path, over HTTP,
  on a deployment holding no secret. What it did **not** prove is the part this
  bullet said was the marginal finding: whether the reverse proxy in front of
  the API preserves the `Host` header the signature is computed over. The run
  had no proxy in front of it, so that question is untouched and is the reason
  this row is not struck out whole.
- `T-H2` — a real Lark event with the header omitted. **Attempted 2026-08-14 and
  it needs a seeded tenant** — see §9.
- `T-H3` — a boot with each setting empty in production mode, and a raw-DSN
  registration with no TLS parameters through the dashboard. `Validate()` is
  tested directly; that it is called on the path `cmd/api` actually takes is not.
- `T-H15` — the controlled resolver is exercised, but only inside the process.
  A real short-TTL record against a real worker would test the same property
  through one more layer.

**Not attempted, and why.** `T-H3`'s table has no row for the Metabase copy of
the tenant DSN or for the SQL Server read-only story — those are `T-H5` and
`T-H4`, and both are Track B.

**A decision, not code.** The SQL Server certificate change (§3) needs the
answer to "is any live tenant on SQL Server" before it reaches production. If
one is, they need telling which of `require` and `skip-verify` describes their
server before their next connection edit, not after.

---

## 9. The live gate — `T-H1` run 2026-08-14

`cmd/api` on `:8080` against the rebuilt `.env` and the compose stack. This
deployment is the shape that matters for the bypass: **Meta transport, and no
`WHATSAPP_APP_SECRET` set** — exactly the configuration in which
`VerifyWebhook` used to `return true` and log a line.

```
1. forged Twilio POST, no signature        → HTTP 401
   -d "From=whatsapp:+62…&Body=what were our total sales last month&MessageSid=SMforged1"
2. forged Twilio POST, bogus signature     → HTTP 401
   -H "X-Twilio-Signature: aGVsbG93b3JsZGhlbGxvd29ybGQxMjM0NTY3OA=="
3. Meta JSON POST, no X-Hub-Signature-256  → HTTP 401
4. Meta GET handshake, wrong verify token  → HTTP 403
```

Three refusals, three log lines, and **no `inbound from unknown phone number`
line in the whole run** — the refusal happens before `ResolveCompanyByPhone`, so
nothing touched tenant state and nothing was enqueued.

**The finding worth having run it for is which branch the forged requests took.**
All three logged `whatsapp webhook: Meta signature verification failed` —
including the two that carried `X-Twilio-Signature` and a form-encoded body.
That is the actual `T-H1` vulnerability, proven dead over HTTP rather than in a
handler test: the transport is the deployment's, and a caller cannot select the
Twilio path by sending a header. Each 401 was preceded by
`WhatsApp app secret is not configured; the webhook signature cannot be
verified`, which is the fail-closed branch running in the deployment that used
to fail open.

**What this does not cover.** No reverse proxy sat in front of the process, so
the `Host`-header question §8 names is exactly as open as it was. It needs a
deployment, not a stack.

### `T-H2` needs a seeded tenant, and that is the whole finding

The Lark route is `/webhook/lark/events/:app_id` and mounts only when
`LARK_ENABLED=true`; the working `.env` sets it false, so on the default local
deployment the route does not exist (`404`). Booted a second process with
`LARK_ENABLED=true` and the route appears — and then refuses an unknown
`app_id` with **`404` before any signature work happens**
(`lark_webhook.go:57-65`, `ResolveCompanyByAppID` ahead of the header checks).

So the 401 this gate is after is only reachable for an app id that exists in
`company_lark_credentials` with an encrypt key. That is a seeding step through
the product's own configuration path — not model spend, not a workspace, not a
handset — and it is why this item stayed open rather than passing today.

**One accident worth a line, because it cost ten minutes.** The second process
was aimed at `:8081`, which another project on this machine was already
listening on; it answered `/health` with `200` and the webhook path with a
*typed JSON* `404` envelope, which reads exactly like Argentum with the route
missing. The real Argentum 404 is gin's plain `404 page not found`. Bind to a
port checked free with `lsof`, and read the 404's body before believing it.

## 10. `T-H3` — the boot matrix, run 2026-08-14

The claim this gate exists for is not that `Validate()` is correct — a unit test
already says that — but that `cmd/api` reaches it on the path a deployment takes.
It does: `config.Load()` calls `Validate()` at `config.go:508` and `main.go:28`
is `Load`'s only caller before anything else happens.

Run against a built `cmd/api` binary in a directory holding a copy of the
working `.env`, with one variable blanked per boot and `ENV=production`. An
empty value is not the same as an unset one to `godotenv`, which is why the
override works: the variable is present in the environment, so the `.env` value
is not substituted.

```
LLM_API_KEY=          → exit 1  "Failed to load config: LLM_API_KEY is required"
ARGENTUM_JWT_SECRET=  → exit 1  "ARGENTUM_JWT_SECRET is required"
ARGENTUM_DSN_KEY=     → exit 1  "ARGENTUM_DSN_KEY is required (64 hex chars)"
DB_PASSWORD=          → exit 1  "DB_PASSWORD is required"
```

**The warn-rather-than-refuse rows behave as the 2026-08-11 decision says.**
Production boot with `WHATSAPP_ACCESS_TOKEN` set and nothing else WhatsApp-shaped
logged both warnings — the missing `WHATSAPP_PHONE_NUMBER_ID`, and the unset
`WHATSAPP_APP_SECRET` — and then booted: `/health` `200 {"status":"ok"}`,
`/ready` `200 {"ready":true}`.

### The raw-DSN half, over HTTP

Three registrations against the running production process, all with
`skip_test` so the refusal being measured is the transport rule and not
reachability:

```
POST /api/connections  dsn=postgres://…/demo_analytics            → 400
  "this DSN does not set sslmode; production requires it, because the
   driver's default permits an unencrypted connection"
POST /api/connections  dsn=postgres://…?sslmode=disable           → 400
  "this DSN sets sslmode=disable, which sends credentials and rows in plaintext"
POST /api/connections  host/port form, ssl_mode=disable           → 400
  "ssl_mode \"disable\" sends this connection's credentials and rows in
   plaintext; use require, skip-verify or verify-full"
```

`requireTLS` is `cfg.IsProduction()` (`cmd/api/router.go:63`), so all three are
development-permitted by design — the laptop case — and closed in production.

### And the gate found two things about CORS

**1. The production warning could not fire for the input a deployment produces.**
`CORS_ORIGINS` is read with `getEnv("CORS_ORIGINS", "http://localhost:5173")`,
and `getEnv` treats unset and empty identically, so `len(CORSOrigins) == 0` —
the condition `Validate()` warns on — is reachable only from a value that parses
to nothing, such as `" , "`. Booting production with `CORS_ORIGINS=` unset or
empty produced **no warning at all** and left the process allowing exactly one
origin: the development default. The dashboard's own requests would be refused
by the browser, with nothing in the log to say why. A second warning now names
that case, and it is what an operator actually sees:

```
CORS_ORIGINS is unset in production: the only allowed origin is the development
default ["http://localhost:5173"], so the dashboard's own requests will be
refused by the browser — set it to the dashboard host
```

**2. The hole itself is real, and now photographed.** With
`CORS_ORIGINS=" , "` in production the middleware reflected an unlisted origin:

```
curl -H "Origin: https://evil.example" http://localhost:8098/health
  Access-Control-Allow-Origin: https://evil.example
  Access-Control-Allow-Credentials: true
```

That is the documented consequence of the owner's warn-rather-than-refuse
decision, so it is not a regression — but `middleware/cors.go` claimed in its own
comment that `Validate()` *refuses to start* a production process in this state.
It warns. The comment is corrected, in the file, with the date this run
disproved it.

## 11. `T-H15` — the resolver that changes its answer, run 2026-08-14

The one claim in this track that cannot be shown with a static read: that the
address checked is the address dialled. Driven through the real
`webhookout.Deliverer` — the same constructor `cmd/worker/main.go:107` uses —
with a `Resolver` that answers `192.0.2.1` (TEST-NET-1, routable and
unreachable) the first time and `127.0.0.1` every time after, and a raw TCP
listener on `127.0.0.1:9099` counting connections rather than requests, because
the callback path is https and a wrong dial would fail the handshake *after*
making the connection it was not supposed to make.

```
0. control  dial 127.0.0.1:9099 directly            → listener accepted 1
1. Deliver() with the rebinding resolver            → "dial tcp 192.0.2.1:9099:
     connect: network is unreachable"; resolver answered ONCE; listener still 1
2. Deliver() with a resolver that is loopback at check time
                                                    → "callback_url resolves to
     a loopback address"; listener still 1
3. the same rebound answer, dialled without the pin → listener accepted 2
```

Line 1 is the property: the name resolved to loopback by dial time and the
connection went to the checked address anyway, because `http.Transport` never
got to resolve — `pinnedDial` took the port from the address it was handed and
threw the host away. Line 3 is the control that makes line 1 mean something: the
same answer, dialled the ordinary way, reaches the listener.

**What this does not cover.** It ran in a gate binary, not inside `cmd/worker`,
because making the worker's own resolver rebind needs DNS this machine cannot
supply: the public rebinder answered with the public half of the pair on 14 of
14 lookups, which is an upstream resolver filtering rebinding, and `/etc/hosts`
cannot flip an answer between two lookups milliseconds apart. What the worker
adds over what was run is one line of wiring — `NewDeliverer(...)` at
`cmd/worker/main.go:107`, the same call the gate makes — and that is a read, not
a measurement.

---

# Track B and D, first three tickets — `T-H7`, `T-H10`, `T-H13`, built 2026-08-14

Three tickets that had nothing to do with each other and one thing in common:
each is a disclosure or a blind spot that no test in this repo could have
failed on, because nothing was looking. `go build`, `go vet`, `go test -race`,
`golangci-lint` and `gofmt` clean over the backend.

## 12. `T-H7` — query text out of the logs

`run_sql.go` logged the executed statement at **Info** with its literals
intact, so a turn asking about one person wrote that person's identifier into
the operational log, at the level production runs at, on the path logs are
shipped from.

**What shipped.** `normalizeSQLForLog` (`internal/tools/sql_log.go`), a
single-pass scanner: single-quoted strings become `'?'` (doubled-quote escapes
included), standalone numbers become `?`, `--` and `/* */` comments are dropped
whole, and double-quoted or backticked identifiers are copied through because
they are names. Info carries the normalised form under the same `sql` key it
always had; the raw statement moved to **Debug** under `sql_raw`.

**Numbers, not just strings.** A NIK is sixteen digits, an Indonesian mobile is
eleven or twelve, and an account id is whatever the tenant made it — a
normaliser that only handled quoted literals would have missed the class that
matters most. Digits that follow an identifier character are left alone, so
`fact_sales_2024`, `t2.col1` and `x_1` survive while `= 42` does not, and a
value carrying a decimal point stays a number rather than being read as an
identifier.

**And the probe's own log line was the same bug one file over.** T-Q9's
empty-result probe logged `probeJSON(probes)` at Info — which is the probe's
entire disclosure, the real contents of a filtered column, written to the log
as well as handed to the model. Info now names the columns
(`customers.city,orders.channel`) and the payload moved to Debug.

Two tests: a table of shapes, and a leak test that asserts the *absence* of a
NIK and an email as substrings of the output, in a query, in an IN list, in a
comment and unquoted. The second is the one that fails if somebody adds a
branch that copies a literal through.

## 13. `T-H10` — the empty-result probe discloses a column's real contents

`distinctValues` answers a zero-row query by returning the filtered column's
actual values. That is the right answer for `month_name = 'December'` against a
column padded to `'December '` — the case T-Q9 exists for — and the wrong one
for `email = 'budi@examle.co.id'` against a column of real customer addresses:
a typo'd domain returns twenty real emails the user's own query did not fetch,
on a path no output guardrail sees, because guardrails run on the reply and
this is a tool result.

**Two filters, because either alone is wrong** (`internal/tools/probe_pii.go`):

| Filter | Catches | Misses |
| ------ | ------- | ------ |
| Column name (`email`, `no_hp`, `nik`, `npwp`, `card_number`, …) | The column that announces itself — and it is refused **before** the query runs, so the disclosure never crosses the network | A column called `keterangan` that happens to hold addresses |
| Value shape (email, phone, 13–19 digit runs, SSN) | That column, after one probe query | Nothing this repo has seen |

A column that fails either check is dropped **whole** rather than value by
value: one email among twenty rows means the column holds emails, and returning
the other nineteen would disclose the same class of thing while looking
careful.

**The tenant's policy decides.** `strict` (and unset, and unknown, and a
company row that could not be read) discloses neither class; `contact_ok`
allows contact-class columns, because that mode *is* a tenant saying they want
customer contact details in answers; `off` allows both. The lookup is on
`RunSQLTool` behind `WithPIIPolicy`, consulted only on the zero-row path, and a
build that forgets to wire it gets strict.

**What it deliberately does not do.** It never widens what the query returned —
the probe only runs on a result with zero rows in it, so everything it
discloses is data the user's own query did not fetch. That asymmetry is why the
default is the protective one even though `PIIRedactionMode` defaults to strict
for a different reason.

Four tests, including the two that matter: a refused column must not reach the
tenant's database at all (asserted on a recording connection, because a probe
that runs and then discards its answer has already read the data), and the
ordinary label column must still answer under strict — the T-Q9 case is not
worth trading for this one.

## 14. `T-H13` — security scanning in CI, and what the first run found

Three scanners in a new `security` job in `.github/workflows/ci.yaml`:
**gitleaks** over the full history (secret scanning), **govulncheck** and
**gosec** over the backend, plus `actions/dependency-review-action` on pull
requests. All four block. `fetch-depth: 0`, because a shallow clone would pass
a repository whose key is one commit back.

**Every one was run locally first and made green, which is the point.** A
scanner introduced red is a scanner somebody learns to route around. Making
them green was not free:

- **govulncheck found 25 called vulnerabilities** — reachable symbols, not
  merely affected module versions — in a dependency set
  `03-security-hardening-roadmap.md` §`T-H13` had described as *"current
  today"*. It was right about the versions and the versions were not the
  question.
- **Seven modules were bumped**: grpc 1.79.3 → 1.82.1, x/net 0.53 → 0.56,
  x/text 0.38 → 0.39, excelize 2.10.1 → 2.11.0, quic-go 0.59.0 → 0.59.1, the
  aws-sdk eventstream + bedrockruntime pair, and otel 1.40 → 1.44 across the
  five modules that version together.
- **The other eighteen were the standard library**, at go1.26.2, with the last
  of their fixes in **1.26.6** — so `go.mod`'s directive moved to `go 1.26.6`.
  CI and all four Dockerfiles ask for `1.26`, which resolves to the newest
  patch, so the directive is what stops a developer's older toolchain building
  what CI would refuse. After both: **0 called vulnerabilities.**
- **gosec is gated at high severity + high confidence**, where this tree is
  clean. At medium/medium it reports 15, every one examined and none a defect:
  eight G202 SQL concatenations building filter clauses out of fixed fragments
  in `internal/adapters/postgres`, three G304 reads of our own config paths,
  one G306 on the eval CLI's report file, one G505 for `crypto/sha1` — which is
  Twilio's signature algorithm and therefore not ours to choose (`T-H1`) — and
  two worth a look of their own: an int→uint32 conversion in
  `internal/auth/password.go:80` and a goroutine on `context.Background` in
  `internal/app/thread_service.go:614`. The bar is a line in the workflow and
  the list is written down here, so raising it is a decision rather than a
  discovery.

**What is owed on all three.** `T-H7` and `T-H10` are unit-gated only. The live
half is one turn each against a running stack: a query with a literal in it,
read back out of the API log; and a zero-row query filtered on an email column
under each of the three redaction modes. `T-H13` cannot be gated locally at all
— the assertion is that the job runs and blocks on GitHub, which is the first
pull request after this lands.
