# apps/widget

Argentum's chat, embeddable in a tenant's own site (T-21).

Two build outputs from one source, and the split is the design:

| Output | What it is | Budget |
| ------ | ---------- | ------ |
| `dist/argentum-widget.js` | The **loader**. An IIFE with no framework that a tenant drops in a script tag. Opens an iframe, draws a launcher, bridges `postMessage`. Exposes `window.Argentum`. | ≤15 KB gzipped |
| `dist/app/` | The **app** that runs inside that iframe. Preact + `marked` + `dompurify`. | ≤80 KB gzipped |

Measured on the last build: **loader 1.8 KB**, **app 31.8 KB**. Gated in Chrome
on 2026-08-10 against a live stack — a question typed into the panel, the answer
streaming back over the WebSocket. That sitting found four defects nothing else
had: see `docs/coverage/widget.md` §5a, and the two config files here, whose
comments explain why the app is a **classic script** and why `base` is `"./"`. `pnpm size` is
the check, and it exits non-zero on a breach — the budget is the feature, since
a widget that slows the host page is one the customer's frontend team removes.

```bash
pnpm build      # both outputs
pnpm size       # the budget check
pnpm lint       # tsc --noEmit
pnpm dev        # the iframe app on its own, for styling
```

## Two build decisions that look arbitrary and are not

**The app is a classic script, never `type="module"`.** The frame is sandboxed
without `allow-same-origin`, so it has an opaque origin, and a module script is
then fetched under CORS with `Origin: null` — which no static host answers. The
panel opens blank and the error is inside a frame the host page cannot read.

**`base: "./"`.** Root-absolute asset URLs resolve only from a domain root, and
the CDN path this ships to is `/widget/v1/`.

Both are enforced by nothing but the comments in `vite.app.config.ts`. If a
future change makes the app a module again, the symptom is a blank panel with a
clean build and passing tests.

## Why an iframe

CSS isolation (the host's Tailwind cannot break the widget and the widget cannot
break their page), JS isolation, and a real origin boundary around the session
token. The cost is bridging sizing and open/close over `postMessage`, which is
most of `src/loader.ts`.

The frame is sandboxed `allow-scripts allow-forms` — deliberately without
`allow-same-origin`, so a compromised widget cannot read anything else stored on
the origin it is served from.

## Where the session token lives

In a closure in `src/app/api.ts`, and nowhere else. Not `localStorage`, not a
cookie, not the host page. It lasts fifteen minutes and is re-minted from the
tenant's own signature, so a stolen laptop, a third-party script on the host
page and an XSS in the tenant's site all fail to reach it.

The host page never sees it either: it holds the *signature*, which is useless
without an allowlisted origin.

## Deploying it

`dist/` is static. Copy it anywhere — a CDN, a Pages project, an object store —
and point the tenant's script tag at `dist/argentum-widget.js`. The loader finds
the iframe app relative to its own URL, so both halves move together and there
is no second URL to configure.

Integration guide, security model and CSP requirements:
[`apps/backend/docs/embed/`](../backend/docs/embed/README.md).

## `packages/chat-ui` was not extracted, and that is a decision

`T-21` says *extract, do not port*: move the dashboard's chat components into a
shared package so the widget and the dashboard cannot drift. This does not do
that, and the reason is a real trade rather than a shortcut.

The dashboard's chat is React 18 with the full design system, `react-markdown`,
tool-call cards and streaming state built for a full-width panel. The widget has
an 80 KB budget, runs Preact, and renders in a 400px frame. Extracting would
have meant reshaping the dashboard's components to compile for both — a
refactor of a surface that works today, with the regression landing on staff who
use it daily — to share roughly 200 lines of presentational markup.

**What that costs:** two places render a chat message, so a change to how a tool
call looks has to be made twice. That is the drift `T-21` warned about and it is
real.

**When to pay the extraction cost:** the first time a change has to be made in
both, or the first time the widget needs a component the dashboard already has
(a chart card, an approval card). Both are visible events, unlike the slow drift
of two copies nobody compares — which is why this is written here rather than
left as a silence.
