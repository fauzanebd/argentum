# React + Vite

`<ArgentumWidget />` from `@argentum/widget-react`, mounted once the page knows
who the visitor is. The re-sign on `onTokenExpired` is the part worth copying:
a signature lasts 24h and a session fifteen minutes, so the widget will ask.

`server.mjs` is the signing endpoint — in your project it is a route on the
server you already have, not a second process. It reads:

- `ARGENTUM_CLIENT_KEY` — the public key from Settings → Embed.
- `ARGENTUM_EMBED_SECRET` — the signing secret. Server-side only, always.
- `ARGENTUM_BASE_URL` — your Argentum API (default `http://localhost:8080`).

```bash
pnpm install
ARGENTUM_CLIENT_KEY=… ARGENTUM_EMBED_SECRET=… pnpm identity   # :4321
pnpm dev                                                      # :5173
```

Vite proxies `/identity` to that process, so the fetch is same-origin exactly as
it would be in production.

`APP_BASE` in `src/main.jsx` points at the widget's `dist/app`; serve it from
anywhere on :5174 (`npx serve apps/widget/dist`) or set it to your CDN path.
This example is outside the pnpm workspace on purpose — it resolves
`@argentum/widget` from the registry, as your project will.
