# Hosted API docs site

**Status: SHIPPED (2026-08-03).** `docs/api/quickstart.md`, its examples, the
OpenAPI contract and the Postman collection are published as static pages under
the landing app at `/docs/`. Not a ticket — a backlog item
([`../plan/backlog.md`](../plan/backlog.md), *Platform depth*), filed 2026-08-02
because the deferral in
[`../plan/00-sprint-overview.md`](../plan/00-sprint-overview.md) §3 had no
trigger and would never have been pulled forward.

## 1. What was wrong

`T-A4` produced good docs and no way to read them. The quickstart is 491 lines
with every code block executed by CI, both SDKs carry READMEs, and
`GET /v1/openapi.json` serves the contract keyless — and all of it was reachable
only by someone holding the repository. An integrator with a key had nothing to
open.

## 2. What ships

`apps/landing/scripts/build-docs.mjs`, run by `pnpm dev` and `pnpm build`:

| Path | From |
| ---- | ---- |
| `/docs/` | `docs/api/quickstart.md`, rendered |
| `/docs/examples/` | index of `docs/api/examples/**` |
| `/docs/examples/<file>.html` | each example, syntax-labelled, with a raw link |
| `/docs/examples/raw/<file>` | the file itself, byte-for-byte |
| `/docs/v1.yaml` | `apps/backend/openapi/v1.yaml` |
| `/docs/postman/` | `apps/backend/docs/postman/` |

Entry points: a **Docs** item in the landing nav, three links in the footer, and
the dashboard's Settings → API Keys card when `VITE_DOCS_URL` is set.

## 3. The decisions worth carrying forward

- **Nothing is committed.** The output goes to `apps/landing/public/docs/`, which
  is gitignored and rebuilt on every dev and build run. A committed copy of the
  quickstart would be a second copy of a file CI executes, and the two would
  disagree the first time somebody edited one — the failure the design tokens
  (`T-R1`) and the hand-written dashboard types (`T-02b`) both had. There is one
  quickstart in this repo and the published page is derived from it.
- **A link that resolves to nothing fails the build.** Three links in the
  quickstart are written for a reader holding the repo (`examples/`,
  `../../apps/backend/openapi/v1.yaml`, `../../apps/backend/docs/postman/`), and
  each is rewritten to the copy this script emits. Every relative `href` in the
  generated HTML is then checked against the set of files actually written, so a
  renamed example or a stale rewrite is a red build rather than a 404 the author
  never sees — the author has the repository. Proven by pointing one rewrite at
  a directory that does not exist: `Published docs contain links to files that
  were never emitted: index.html: examples/nope/ → examples/nope/index.html`,
  exit 1.
- **CI's `web` filter had to grow.** It listed `apps/**` and `packages/**`, so
  editing `docs/api/quickstart.md` alone would not have rebuilt the site and the
  link check would not have run on the change most likely to break it.
  `docs/api/**`, `apps/backend/openapi/**` and `apps/backend/docs/postman/**` are
  now in it.
- **The dashboard link has no default.** `VITE_DOCS_URL` unset renders no link at
  all rather than one pointing at a domain this repo does not know. A dead link
  at the moment a tenant has a fresh key and nothing to do with it is worse than
  no link.
- **Heading ids are generated.** marked stopped emitting them in v12; a reference
  page whose sections cannot be linked is one support has to describe by
  scrolling. `#0-a-key` through `#the-five-things-worth-knowing-before-you-go-to-production`
  now exist.
- **No Redoc or Scalar.** The backlog entry prices that at 1.5d against 0.5d for
  this; the spec is served as a file and a generator can be pointed at it, which
  is what the quickstart already tells an integrator to do. Revisit when someone
  is browsing fifteen operations rather than following the quickstart.

## 4. Gate

`pnpm --filter landing build` and `pnpm --filter dashboard build` both clean.
Served from `dist/` and checked over HTTP: `/docs/` 200, `/docs/examples/` 200,
`/docs/v1.yaml` 200, `/docs/examples/raw/curl/me.sh` 200, `/docs/postman/` 200.

Photographed in headless Chrome over CDP at 1280×900 @2x — the extension is not
connected on this machine, `node --experimental-websocket` is:

- `assets/docs-quickstart.png` — the quickstart on the landing palette, its
  blockquote, and the scope table.
- `assets/docs-examples.png` — the examples index, each file with a raw link.
- `assets/docs-example-file.png` — `node/render.mjs` rendered as a page.

## 5. Known limits

- **The three application-level checks are the reader's, not CI's.** CI proves
  every link resolves to an emitted file; it does not prove the page reads well
  at 320px, or that Cloudflare Pages serves `.sh` as anything but a download.
- **Raw example files are served with whatever content type the host guesses.**
  On Pages an unknown extension is `application/octet-stream`, so `raw` links
  download rather than display. The `.html` page beside each one is the readable
  copy, which is why both exist.
- **No search, no sidebar, no version switcher.** One page, four nav items. The
  contract is additive and there is one version of it; a version switcher before
  a `/v2` exists would be furniture.
- **The landing nav's `Docs` item is not marked active** when you are on the docs
  pages — those pages carry their own header rather than the React nav.
