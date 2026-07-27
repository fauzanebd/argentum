# @argentum/design-tokens

One source of truth for colour, type, spacing, and page geometry — shared by the
dashboard and by the documents the backend renders.

```
tokens.json ──┬── scripts/gen-css.mjs ──→ apps/dashboard/src/tokens.generated.css
              └── scripts/gen-go.mjs  ──→ apps/backend/internal/report/theme/tokens_gen.go
```

Both outputs are **committed**, and CI regenerates them and runs
`git diff --exit-code`. A hand edit to a generated file fails the build; so does
a `tokens.json` change without regeneration.

## Working on tokens

```bash
make tokens                                    # regenerate both outputs
pnpm --filter @argentum/design-tokens check     # verify only (what CI runs)
pnpm --filter @argentum/design-tokens palette   # recompute the chart palette's L* ladder
```

Adding a colour means adding it to `tokens.json` *and* mapping it to a CSS
variable in `scripts/gen-css.mjs`. An unmapped web-visible colour is an error,
not a silent omission — that omission is the failure this package exists to
prevent.

`scope` on a token (or `$scope` on its group) decides who sees it:

| scope   | dashboard CSS | Go report theme |
| ------- | ------------- | --------------- |
| `all`   | ✅            | ✅              |
| `web`   | ✅            | —               |
| `print` | —             | ✅              |

Sidebar chrome is `web`: it has no meaning on paper. The type scale, spacing,
page geometry, and chart palette are `print`: they are in points and
millimetres, and the dashboard uses Tailwind's scales for the same jobs.

## What is not here

- **The dark palette.** Documents are printed and forwarded. The dashboard's
  `.dark` block stays hand-written in `index.css`.
- **Tailwind classes or utilities.** The generators emit variables and Go
  values; `tailwind.config.ts` and the renderers decide what to do with them.
- **Per-tenant branding.** `T-R5` layers a tenant logo and accent colour over
  this theme at render time. These are the defaults, not the only possible
  values.

## Fonts

The font *files* live with the consumer that has to embed them:
`apps/backend/internal/report/theme/fonts/` (Space Grotesk + its OFL licence).
`tokens.json` records the family name for CSS and the family key the Go renderer
registers with maroto.
