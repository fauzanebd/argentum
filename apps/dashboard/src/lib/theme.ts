/**
 * Which of the two palettes is showing.
 *
 * This file used to also export `LIGHT_COLORS` and `DARK_COLORS` — two objects
 * of hex literals under a header calling itself "the single source of truth for
 * the color palette". It was not one. `packages/design-tokens/tokens.json` is,
 * and has been since `T-R1`: it generates the dashboard's CSS variables, the Go
 * report theme and the Remotion fallback, with `make tokens-check` diffing all
 * three in CI. Nothing imported either object, so nothing broke when the light
 * palette here drifted from the generated one — which it had.
 *
 * `T-U1` deleted them. A colour belongs in `tokens.json`; the dark palette,
 * which is light-only there by design, belongs in `index.css`. Neither belongs
 * in a `.ts` file that no component reads.
 */
export type Theme = "light" | "dark";
