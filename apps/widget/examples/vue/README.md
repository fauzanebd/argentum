# Vue 3 + Vite

`@argentum/widget` used directly — there is no Vue wrapper package, because the
loader's whole lifecycle is `init()` in `onMounted` and `destroy()` in
`onUnmounted`. The `token_expired` handler is the part worth copying: a
signature lasts 24h and a session fifteen minutes, so the widget will ask.

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

`APP_BASE` in `src/App.vue` points at the widget's `dist/app`; serve it from
anywhere on :5174 (`npx serve apps/widget/dist`) or set it to your CDN path.
This example is outside the pnpm workspace on purpose — it resolves
`@argentum/widget` from the registry, as your project will.
