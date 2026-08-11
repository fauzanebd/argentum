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
| Provider webhook secret | `WHATSAPP_APP_SECRET` is fatal in production for the Meta provider. Twilio needs no separate rule — its signing key is `TWILIO_AUTH_TOKEN`, already required unconditionally by the existing triple. |
| `CORS_ORIGINS` empty | Fatal in production. `Access-Control-Allow-Credentials` is now sent only alongside an `Allow-Origin` we actually issued. |
| Tenant DSN without TLS | `disable`/`prefer`/`allow` refused in production on the form path; the raw-DSN path checked for the same property; SQL Server verifies its certificate by default and floors at TLS 1.2. |

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

### SQL Server: the breaking change, named

`buildDSN` pinned `TrustServerCertificate=true` and `tlsmin=1.0`
(`company.go:172-173`) with no way to say otherwise. That is encryption against
somebody listening and nothing at all against somebody answering — anything that
can reach the address can present any certificate it likes.

SQL Server now reads the same `ssl_mode` field the other two drivers do:

| `ssl_mode` | DSN |
| ---------- | --- |
| unset, `require`, `verify-ca`, `verify-full` | `encrypt=true&TrustServerCertificate=false` |
| `skip-verify` | `encrypt=true&TrustServerCertificate=true` |
| `disable` | `encrypt=disable` (refused in production) |

`tlsmin` is `1.2` in every case.

**This will break a tenant whose SQL Server presents a self-signed certificate**
— the default for an installation nobody has given a certificate to. They stay
reachable by choosing `skip-verify` explicitly, which is the whole point of
making it a choice, but they have to make it. The roadmap's own "What is owed"
already asks whether any live tenant is on SQL Server; that question is now
load-bearing for this change and not only for the `db_datareader` one.

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
| `T-H1` asks for an unset secret to be "fatal at startup". Fatal **in production**; `401` at request time everywhere | `T-H3`'s own table scopes this row to production, and an unconditional fatal would stop a development stack that has never configured WhatsApp from booting at all. The endpoint is closed in both cases; only the boot differs. |
| `T-H3` row 3 is implemented in `handlers/company.go`, outside this track's declared file ownership | The row cannot be implemented anywhere else — `buildDSN` is where the DSN is composed. The change is contained to `buildDSN`, one constructor option, and its own test file. |
| `T-H15` resolves even when `allowPrivate` is set, where the previous code returned early | The pin is the point, and it is not a property only production should have. It also makes the fix observable against a loopback listener, which is the only kind of listener a test owns. |
| `T-H15` drops `HTTPS_PROXY` support | Stated in §4. A pinned dial through a proxy pins the proxy. |

One test outside this track's files was edited: `cmd/api/v1_test.go`'s CORS
assertion, which had been passing on the unconditional `Allow-Credentials`
header. It now asserts against an origin the test router actually allowlists.

---

## 8. What is still owed

**The live half.** Everything above is unit-level. The roadmap's "What is owed"
asks for each of these against a running stack, and none of them has been run:

- `T-H1` — a forged form POST against a running API, before and after. The
  handler test is the same assertion at the same layer the bypass lived at, so
  the marginal finding is deployment-shaped: whether the reverse proxy in front
  of the API preserves the `Host` header the signature is computed over. That is
  the one thing a unit test cannot see, and it is the thing most likely to make
  a correct implementation reject genuine traffic.
- `T-H2` — a real Lark event with the header omitted.
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
