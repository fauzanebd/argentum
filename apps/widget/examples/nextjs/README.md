# Next.js (App Router)

The same `<ArgentumWidget />` as the React example, with the two things Next
changes: the component carries `"use client"` because the loader is browser-only,
and the signing endpoint is a route handler (`app/api/identity/route.ts`) rather
than a second process — server-only by construction, so the secret cannot reach
the client bundle.

Env vars, in `.env.local`:

- `ARGENTUM_CLIENT_KEY` — the public key from Settings → Embed.
- `ARGENTUM_EMBED_SECRET` — the signing secret. No `NEXT_PUBLIC_` prefix, ever.
- `ARGENTUM_BASE_URL` — your Argentum API (default `http://localhost:8080`).

```bash
pnpm install
pnpm dev        # :3000
```

`appBase` is passed explicitly in `app/widget.tsx`: a bundled loader has no
script tag to infer the iframe app's URL from. Point it at your CDN copy of the
widget's `dist/app`, or serve it locally on :5174 (`npx serve apps/widget/dist`).

This example is outside the pnpm workspace on purpose — it resolves
`@argentum/widget` from the registry, as your project will.
