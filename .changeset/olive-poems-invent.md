---
"@argentum/widget": minor
"@argentum/widget-react": minor
---

First published release of the embeddable chat widget.

`@argentum/widget` is the loader that was previously only available by copying
`dist/` to your own host: `init()`, `open()`, `close()`, `toggle()`,
`identify()`, `destroy()` and `on()`, as ESM, CJS and a CDN script tag, with
types. `@argentum/widget-react` wraps it as `<ArgentumWidget />`, mounting on
mount, calling `identify()` when the signed visitor changes, and destroying on
unmount.

Two notes for anyone moving off a copied `dist/`:

- `appBase` is required when the loader is bundled. The script-tag build infers
  it from its own `src`; an import has no script tag to read.
- The script-tag global is unchanged — `Argentum.init({...})` still works, and
  the CDN filename is still `argentum-widget.js`.
