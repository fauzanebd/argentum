# `@argentum/widget`

The Argentum chat widget: a launcher, an iframe, and a signed visitor. Drop it
in a script tag or import it — same file either way.

```bash
npm install @argentum/widget
```

```ts
import Argentum from "@argentum/widget";

// Your backend signs the visitor. It never runs in the browser — see below.
const { clientKey, apiBase, user } = await fetch("/identity").then((r) => r.json());

Argentum.init({
  clientKey,
  apiBase,
  appBase: "https://cdn.example.com/widget/v1/app/",
  user,
}).on("token_expired", async () => {
  Argentum.identify((await fetch("/identity").then((r) => r.json())).user);
});
```

Or, with no build step at all:

```html
<script src="https://cdn.example.com/widget/v1/argentum-widget.js"></script>
<script>
  Argentum.init({ clientKey, apiBase, user });
</script>
```

## `appBase` is required when you import it

The script-tag build works out where the iframe app lives from its own `src`, so
a tenant who copies `dist/` to one place gets a working widget with one URL. An
`import` has no script tag to read, and `document.currentScript` is null inside a
bundle — so the bundled path needs `appBase` spelled out, and says so if it is
missing.

## API

`init(options)` returns the same object every method below hangs off, so calls
chain.

| Method | What it does |
| --- | --- |
| `init(options)` | Mounts the launcher and the iframe. Destroys an existing instance first, so calling it twice is a replace rather than a leak. |
| `open()` / `close()` / `toggle()` | Shows or hides the panel. |
| `identify(user)` | Swaps in a freshly signed visitor without tearing down the conversation. |
| `destroy()` | Removes both elements and every listener. |
| `on(event, handler)` | `ready`, `open`, `close`, `message`, `error`, `token_expired`. |

### Options

| Option | Required | Notes |
| --- | --- | --- |
| `clientKey` | yes | The public `argw_pub_…` half. It ships in your page source; that is what it is for. |
| `user` | yes | `{ ref, name?, exp, sig }` from your backend. |
| `apiBase` | yes | Your Argentum deployment. There is deliberately no default — a wrong one sends your customers' questions to somebody else's API. |
| `appBase` | when bundled | Where the iframe app is served from. |
| `launcher` | no | `"bubble"` (default) or `"none"` if you open it from your own button. |
| `position` | no | `"bottom-right"` (default) or `"bottom-left"`. |
| `theme` | no | `{ primary?, radius?, mode? }`. |
| `locale` | no | Overrides the workspace default. |

## The part that is not optional

`sig` is `HMAC-SHA256(secret, "<user_ref>:<exp>")`, computed **on your server**
with the signing secret. Putting that secret in the browser hands anyone who
opens devtools the ability to be any of your users. The signing snippets in Go,
Node, Python and PHP are in the
[integration guide](../../apps/backend/docs/embed/README.md), each showing the
whole flow rather than the interesting line.

Tokens last 15 minutes. Listen for `token_expired` and call `identify()` with a
fresh signature; skip it and the widget stops answering a quarter of an hour in,
which reads to a visitor as the widget being broken.

## Versioning

SemVer, against the `/api/embed` contract. A major is a change to the `init()`
options or to the events. The CDN path `/widget/v1/argentum-widget.js` tracks
the latest `1.x`; `/widget/v1.2.3/argentum-widget.js` never changes once
published. See [CHANGELOG.md](CHANGELOG.md).

## React

`@argentum/widget-react` wraps this in a component. It is a wrapper, not a
second implementation — bugs get fixed here.
