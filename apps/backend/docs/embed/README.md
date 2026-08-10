# Argentum in your own site

Put Argentum's chat inside a page you run, answering as one of your own people,
without giving the browser anything that could ask questions as anybody else.

Ten minutes, three pieces: a key, an endpoint, a script tag.

---

## 1. A key

Dashboard → **Settings → Embed → Create embed key**.

You give it a name and the list of sites it may be used from. The list is
mandatory, exact, and cannot be `*`:

```
https://intranet.acme.com
http://localhost:3000
```

Exact means scheme, host and port. `https://acme.com` does not cover
`https://staff.acme.com` — a subdomain is a different origin, and Argentum
compares them the way the browser does rather than by suffix. `http://` is
refused except for localhost, because a session token on a plain-text origin is
a session token in transit.

You get two values and the second is shown **once**:

| Value | Where it lives |
| ----- | -------------- |
| `argw_pub_…` — the client key | Your page source. It is public and identifies without authorising. |
| The signing secret | An environment variable on **your server**. Never in a browser, a repo, or a build artifact. |

## 2. An endpoint that says who the visitor is

This is the piece you write, and it is the whole of the trust relationship.
Argentum will answer as whoever `user_ref` names, so this endpoint must name the
person whose session it just read — never a value from the request.

```js
// Node. Runs on YOUR server.
import { createHmac } from "node:crypto";

app.get("/argentum-identity", requireLogin, (req, res) => {
  const userRef = String(req.user.id);                 // YOUR id for this person
  const exp = Math.floor(Date.now() / 1000) + 900;     // 15 minutes; 24h is the max

  const sig = createHmac("sha256", process.env.ARGENTUM_EMBED_SECRET)
    .update(`${userRef}:${exp}`)
    .digest("hex");

  res.json({
    clientKey: "argw_pub_…",
    apiBase: "https://argentum.example.com",
    user: { ref: userRef, name: req.user.name, exp, sig },
  });
});
```

The signed string is `<user_ref>:<exp>` — that exact shape, hex-encoded. A space,
a different separator or base64 all produce a `401` with no further explanation,
which is deliberate: a mint that explained *why* a signature was wrong would be
an oracle for the secret.

The dashboard's Embed tab generates this snippet in Go, Node, Python and PHP,
pre-filled with your own client key.

**Three rules for this endpoint**, and each one is a real incident somewhere:

1. **Never take `user_ref` from the request.** An endpoint that signs whatever it
   is asked to sign lets any visitor become any employee.
2. **Never return the secret**, not even to your own frontend. Everything the
   browser needs is in the response above.
3. **Require your own session.** If it answers to an anonymous request, so does
   Argentum.

## 3. A script tag

```html
<script src="https://cdn.example.com/widget/v1/argentum-widget.js"></script>
<script>
  async function identify() {
    return (await fetch("/argentum-identity", { credentials: "same-origin" })).json();
  }

  identify().then((id) => {
    Argentum.init({
      clientKey: id.clientKey,
      apiBase: id.apiBase,
      user: id.user,
    });

    // The one event you must handle. Sessions last minutes; when one ends the
    // widget asks the page to re-sign rather than retrying a refusal.
    Argentum.on("token_expired", async () => Argentum.identify((await identify()).user));
  });
</script>
```

That is the integration. A working copy is in
[`../../../apps/widget/examples/vanilla/`](../../../apps/widget/examples/vanilla/),
signing server included, in about thirty lines.

---

## Options

```js
Argentum.init({
  clientKey: "argw_pub_…",     // required
  apiBase: "https://…",        // required — your Argentum deployment
  user: { ref, name, exp, sig }, // required — from your endpoint
  appBase: "https://…/app",    // optional; defaults to ./app/ beside the loader
  launcher: "bubble" | "none", // 'none' = you render your own trigger
  position: "bottom-right" | "bottom-left",
  theme: { primary: "#e11d48", radius: 12, mode: "light" | "dark" | "auto" },
  locale: "en" | "id",
});

Argentum.open();  Argentum.close();  Argentum.toggle();  Argentum.destroy();
Argentum.identify(user);                       // re-sign
Argentum.on("ready" | "open" | "close" | "message" | "error" | "token_expired", cb);
```

Greeting, suggested prompts and theme can also be set once in **Settings →
Embed** and left out of the code entirely; the widget reads them on open, so
changing them needs no deploy of your site. Values passed to `init()` win.

## The security model, in five sentences

The client key is public and authorises nothing. A session is minted only when
the request comes from an origin you allowlisted **and** carries an HMAC only
your backend can compute, over an identity and a deadline together. The session
that comes back lasts fifteen minutes, carries no role, and can reach exactly
five routes — chat, this visitor's own conversation, its transcript, its event
stream, and the widget's own configuration. Every read is scoped to the
`user_ref` the session was minted for, so one of your people cannot read
another's conversation by guessing an id. Revoking a key in the dashboard stops
new sessions immediately; the ones already issued expire within the quarter hour.

## Content Security Policy

If your site sends CSP headers — and it should — the widget needs three entries.
This is the single most common embed support ticket in every product that ships
one:

```
frame-src   https://cdn.example.com;              # where the widget app is served
connect-src https://argentum.example.com          # the API
            wss://argentum.example.com;           # and its event stream
script-src  https://cdn.example.com;              # the loader
```

`wss:` is easy to miss. Without it the chat sends fine and no answer ever
arrives, which reads as "the agent is slow" rather than as a policy error.

## Troubleshooting

| What you see | What it is |
| ------------ | ---------- |
| `403` from `/api/embed/session` | The `Origin` is not on the key's allowlist. Compare exactly — scheme, host **and** port. The server log names the offending origin. |
| `401` from `/api/embed/session` | The signature, the deadline, or the key. Check the signed string is `<ref>:<exp>` with no spaces; check your server's clock; check the key is not revoked or paused. |
| `401` seconds after it worked | Normal — the session expired. Handle `token_expired` and re-sign. |
| Blank iframe | CSP `frame-src`, or `appBase` pointing at somewhere the app is not served from. |
| The chat sends but nothing comes back | CSP `connect-src` is missing `wss:`, or a proxy is closing idle WebSocket connections. |
| The answer arrives all at once, seconds late | A reverse proxy is buffering the stream. Nginx buffers by default: `proxy_buffering off` for the API, or `X-Accel-Buffering: no`. |
| The transcript differs from what the user watched appear | The client kept the streamed deltas instead of the `final` message. `final` carries the answer of record. |
| `402` in the panel | The workspace is out of credit. The visitor sees a plain sentence; you see it in Settings → Usage. |
| `429` | The per-visitor turn budget (`EMBED_MAX_TURNS_PER_HOUR`, default 60). |

## What the widget deliberately does not do

- **Serve anonymous visitors.** Every session names somebody your backend
  vouched for. Putting this on a public marketing page is a different product
  with a different threat model — ask us first; usually what is wanted is a
  support bot.
- **Render dashboards.** Chat only in v1.
- **Hold a credential you can rotate silently.** Rotating a signing secret means
  minting a second key, moving your site to it, and revoking the first.
