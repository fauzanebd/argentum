# Design Tokens — `T-R1` Record

Shipped 2026-07-27. The plumbing that makes "the document and the dashboard use
the same design system" true by construction instead of by discipline.

```
packages/design-tokens/tokens.json
   ├── scripts/gen-css.mjs → apps/dashboard/src/tokens.generated.css
   └── scripts/gen-go.mjs  → apps/backend/internal/report/theme/tokens_gen.go
```

Both outputs are committed. `make tokens` regenerates them; the `tokens` CI job
regenerates and runs `git diff --exit-code`, so the build fails either when
`tokens.json` changed without regeneration or when a generated file was edited
by hand.

## Gate 1 — CI fails on a token change that was not regenerated

Changed `color.primary` from `#F25C5C` to `#0000FF` in `tokens.json`, left the
generated files as committed, then ran the two CI steps:

```
### CI step: make tokens
node packages/design-tokens/scripts/generate.mjs
wrote     apps/dashboard/src/tokens.generated.css
wrote     apps/backend/internal/report/theme/tokens_gen.go
### CI step: fail on drift
diff --git a/apps/backend/internal/report/theme/tokens_gen.go
-	ColorPrimary = Color{R: 0xF2, G: 0x5C, B: 0x5C} // #F25C5C
+	ColorPrimary = Color{R: 0x00, G: 0x00, B: 0xFF} // #0000FF
diff --git a/apps/dashboard/src/tokens.generated.css
-  --primary: 0 85.2% 65.5%; /* #F25C5C */
+  --primary: 240 100% 50%; /* #0000FF */
   (… --accent, --ring, --sidebar-primary, --sidebar-ring: same change)
EXIT CODE: 1
```

Reverted the token, ran `make tokens`, re-ran the diff:

```
GATE PASSES AFTER REGENERATION (exit 0)
```

`make tokens` is idempotent — a second run reports `unchanged` for both outputs.

## Gate 2 — a hand edit to a generated file is caught, then reverted

Edited `ColorPrimary` in `tokens_gen.go` directly (`#FF0000`):

```
--- FAIL: TestColorHex (0.00s)
    theme_test.go:71: ColorPrimary.Hex() = #FF0000, want #F25C5C
--- FAIL: TestGeneratedTokensMatchSource (0.00s)
    theme_test.go:163: ColorPrimary = #FF0000, tokens.json says #F25C5C
```

`make tokens` overwrote the edit; the tests pass again. Two independent guards
cover this on purpose: `go test` reads `tokens.json` and compares (so a hand
edit fails the backend job even if the `tokens` job did not trigger), and the
`tokens` job regenerates and diffs (so a stale output fails even when no Go test
touches that value).

## Gate 3 — the dashboard's colours did not move

The hand-written `:root` block in `index.css` was deleted and replaced by the
generated file. Its HSL triples were *rounded approximations* of the brand hex —
`--background: 60 7% 96%` renders `#F6F6F4`, not the `#F5F5F0` the comment beside
it claimed. The generated values are exact conversions of the hex in
`tokens.json`, so three colours move by a few 1/255ths and the rest are
bit-identical:

| variable | old HSL | old renders | new HSL | new renders | Δ max channel |
| -------- | ------- | ----------- | ------- | ----------- | ------------- |
| `--background` | `60 7% 96%` | `#F6F6F4` | `60 20% 95.1%` | `#F5F5F0` | 4/255 |
| `--primary` `--accent` `--ring` `--sidebar-primary` `--sidebar-ring` | `0 87% 65%` | `#F35858` | `0 85.2% 65.5%` | `#F25C5C` | 4/255 |
| `--border` `--sidebar-border` | `60 6% 88%` | `#E2E2DF` | `60 9.4% 87.5%` | `#E2E2DC` | 3/255 |
| the other 19 variables | — | — | — | — | 0 |

**27 variables compared, 8 changed, worst channel delta 4/255 (1.6%)** — and each
change moves *toward* the documented brand value rather than away from it. HSL is
emitted to one decimal place for exactly this reason: at integer precision the
round trip costs up to 1.3/255 per channel, which would make the two sides of the
design system disagree by more than the migration itself did.

The build output confirms the ordering the cascade depends on: `:root` from the
generated file lands at byte 102 of `dist/assets/index-*.css`, `.dark` at byte
44006.

**Not captured: before/after screenshots.** Headless Chrome on this machine
answers `localhost:5173` with an "Authentication Required — please access this
application through the Hamilton portal" page that exists nowhere in this repo,
with both the default and a clean profile, while `curl` gets the real dev server.
Something in the local environment intercepts Chrome's traffic. The numeric table
above is the substitute evidence, and it covers every variable rather than the
two screens a screenshot pair would have shown.

## Gate 4 — the fonts are embedded, and a missing face stops the build

Space Grotesk (Regular / Medium / Bold) is vendored under
`apps/backend/internal/report/theme/fonts/` with its OFL licence and embedded
with `go:embed`. Removing a face is a **compile** error, which is louder than the
startup check the ticket asked for:

```
$ mv internal/report/theme/fonts/SpaceGrotesk-Medium.ttf /tmp && go build ./internal/report/theme/
internal/report/theme/fonts.go:30:13: pattern fonts/SpaceGrotesk-Medium.ttf: no matching files found
```

What a compile error cannot see is a face that exists but is not a font — a
truncated download, a committed Git LFS pointer, an OTF renamed to `.ttf`. That
is `theme.VerifyFonts()`, called from `bootstrap.New` before anything else is
wired, so the worker refuses to start rather than failing a customer's document
hours later.

A rendered PDF now carries the faces rather than referencing them:

| check | result |
| ----- | ------ |
| `/BaseFont` names | `utf8space-grotesk`, `…-groteskB`, `…-groteskI`, `…-groteskBI`, `utf8space-grotesk-medium`, `…-mediumB` |
| `/FontFile2` objects | 6 (all registered faces embedded) |
| `Helvetica` in the bytes | absent |
| document size | **1,567 → 34,498 bytes** for the same one-paragraph spec |

That ~33 KB is the price of a document that looks the same on a machine that has
never heard of Space Grotesk. `TestRenderPDF_embedsThemeFonts` asserts all three
conditions, because the failure mode this track exists to remove — a silent
fallback to Helvetica — is invisible unless something checks the bytes.

## Decisions taken inside the ticket

**The whole light palette moved, not just the nine listed tokens.** Splitting
`:root` across a generated file and a hand-written remainder would have left the
duplication the ticket set out to remove, one property at a time. `tokens.json`
now carries every light shadcn variable; the shadcn *names* stay in
`gen-css.mjs`, because "surface" being called `card`, `popover` and `input` is a
property of that consumer, not of the design system.

**The `.dark` block moved out of `@layer base`.** An unlayered declaration beats
a layered one regardless of specificity. With `:root` arriving unlayered from the
generated file, a `.dark` still inside `@layer base` would have lost on
`<html class="dark">` and dark mode would have silently stopped working. Both are
unlayered now, and `.dark` wins on source order. The comment in `index.css` says
so, because this is a trap the next person will otherwise step in.

**Weight 500 is a separate maroto family, not a style.** maroto's style axis is
normal/bold/italic; there is no weight slot. `theme.FontMedium`
(`space-grotesk-medium`) is registered as its own family, which is why a table
header in `T-R2` will name a family rather than a weight.

**Italic is registered pointing at the upright face.** Space Grotesk ships no
italic, and gofpdf errors on a style it has no font for. An upright glyph where
italic was asked for is a compromise the family forces; a failed render would
not be.

**`scope` decides who sees a token.** `web` (sidebar chrome) never reaches Go;
`print` (type scale in pt, spacing and page geometry in mm, chart palette) never
reaches CSS. Emitting pt-based font sizes into the dashboard's CSS would be a
unit error, not a shared token. A web-visible colour with no CSS mapping is a
generator error rather than a silent omission.

**Page geometry lives in `tokens.json` too.** A4, 18 mm margins, header/footer
bands, table row heights and the hairline width are print-only, but putting them
beside the colours means a margin change is one edit with the same drift check
behind it. `theme.Page.ContentWidth()` and `ContentHeight()` are derived in Go,
not generated.

## The chart palette, and what `T-R3` still owes

Eight categorical colours, series 1 anchored on the brand red, each on a rung of
a CIE L\* ladder from 24 to 82 with a minimum pairwise gap of **6.5 L\*** (red
59.3 vs green 65.7, a pair that only co-occurs in an 8-series chart). L\* is the
greyscale axis, so the ladder is what survives a black-and-white printer, and it
is also what carries red/green pairs through deuteranopia — hue collapses,
lightness does not.

`pnpm --filter @argentum/design-tokens palette` recomputes the ladder and prints
the tightest pair. **`T-R3` still owes the formal verification**: a colour-vision
simulation and a greyscale contact sheet. The ladder is the design; the proof is
that ticket's gate item.

**Two ramps since 2026-08-17.** The eight above were verified against paper,
deuteranopia and protanopia — and never against the surface a dashboard is drawn
on, because when they were written the dashboard had no charts. On a dark card,
series 2 (navy) measured **1.35:1** and series 7 (brown) **1.80:1**.
`tokens.json` now carries `chart.paletteDark`, emitted under `.dark`, and
`make palette` checks **both** ramps plus contrast against each ground. Four of
the eight did not move — the method was measure-first and lift only the
failures, so a reader switching theme does not re-learn which line is revenue.

Two properties of the dark ramp that are decisions rather than results: it
carries **no greyscale floor** (that floor exists for the office laser printer an
enterprise PDF comes out of, and nothing prints a dark dashboard — a
normal-vision ΔE floor of 15 replaces it), and the brand red stays put on both
grounds, which is why it is the accent in both.

**The new check also found debt in the light ramp, unfixed on purpose.** Amber
2.04:1, grey 1.61:1 and azure 2.58:1 on white are below the 3:1 line for a
non-text mark and have been since `T-R3`. Raising them re-renders the palette
every delivered PDF was made with, so it is a **warning on every run rather than
a gate** — visible instead of remembered. Full record:
[`report-charts.md`](report-charts.md) §"The dark ramp".

## What `T-R2` inherits

- `theme.MarotoConfig()` — A4, 18 mm margins, Space Grotesk as the default face,
  body text at `TypeScale.Body` in `ColorForeground`. `RenderPDF` already builds
  from it; the per-section font sizes are still literals and are that ticket's to
  replace.
- `theme.Color.Props()` for maroto and `.Hex()` for the PPTX renderer (`T-R4`),
  which wants `RRGGBB`.
- `theme.Page.ContentWidth()` (174 mm) — the width maroto's 12-column grid
  actually spans, which is what content-weighted column widths must measure
  against — and `ContentHeight()` (239 mm), which is what a table pager needs.
- `theme.SeriesColor(i)` for `T-R3`, wrapping at the palette length.
