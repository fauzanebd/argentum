// Generator 3: tokens.json → packages/motion/src/tokens.generated.ts
//
// The video's components draw everything from `plan.brand`, which `videoplan`
// fills in Go — so this file is **not** what a scene reads. It exists for the
// two places in `packages/motion` that have no plan to read from:
//
//   * Remotion Studio's default props, opened when nobody has passed a plan.
//   * The frame drawn when a plan fails validation, where the plan is the
//     broken thing.
//
// Both had the palette pasted into them as hex literals, which is the drift
// `T-R1` deleted a hand-written `:root` block to end — and `T-V5`'s colour
// check would otherwise have had to exempt fourteen lines, which is a
// file-level allowlist wearing a comment.
//
// Colours only. Type scale, spacing and the canvas metrics are measured in Go
// per document and arrive on the plan; publishing a second copy here would
// invite a component to lay something out from it.

import { join } from 'node:path';
import { banner, groupEntries, REPO_ROOT } from '../lib/tokens.mjs';

export const MOTION_OUT = join(REPO_ROOT, 'packages/motion/src/tokens.generated.ts');

// The plan's brand fields, and which colour token each one is. Copied from
// `videoplan.builder.brand()` in Go, which is the authority — this is the
// fallback used when there is no plan, so it has to agree with what a real
// plan would have carried.
//
// `primary_on_dark` is the one that cannot be mirrored: on a real plan it is
// the tenant's accent lifted through `theme.Readable` until it clears the
// contrast floor against the dark ground. With no tenant there is no accent to
// lift, so the token itself is the honest default — the same value the Go path
// produces for a company that has set no colour.
const BRAND_FIELDS = {
  primary: 'primary',
  primary_on_dark: 'primary',
  foreground: 'foreground',
  background: 'background',
  muted: 'muted',
  border: 'border',
  dark: 'foreground',
  on_dark: 'background',
  surface: 'surface',
  surface_subtle: 'surfaceSubtle',
  positive: 'positive',
  destructive: 'destructive',
};

export function renderMotion(tokens) {
  // Both platforms, unioned. `scope` in tokens.json separates the dashboard's
  // chrome from the page's, and a video is a rendered document — it draws the
  // print-scoped colours (`positive` on a rising delta) as readily as the
  // shared ones. A frame is a page.
  const byName = new Map([
    ...groupEntries(tokens, 'color', 'print'),
    ...groupEntries(tokens, 'color', 'web'),
  ]);

  const out = [];
  out.push(banner('//'));
  out.push('');
  out.push("/** The design system's colours, for the two places with no plan to read. */");
  out.push('export const TOKEN_COLOR = {');
  for (const [name, tok] of byName) {
    out.push(`  /** ${tok.doc} */`);
    out.push(`  ${name}: "${tok.hex}",`);
  }
  out.push('} as const;');
  out.push('');
  out.push('/**');
  out.push(' * The brand block a plan carries, with no tenant to fill it.');
  out.push(' *');
  out.push(' * Mirrors videoplan.builder.brand() field for field, so Remotion Studio and');
  out.push(' * the validation-failure frame show what an unbranded tenant would get');
  out.push(' * rather than an approximation somebody typed.');
  out.push(' */');
  out.push('export const DEFAULT_BRAND_COLORS = {');
  for (const [field, token] of Object.entries(BRAND_FIELDS)) {
    const tok = byName.get(token);
    if (!tok) throw new Error(`tokens.json: no colour ${JSON.stringify(token)} for plan.brand.${field}`);
    out.push(`  ${field}: TOKEN_COLOR.${token},`);
  }
  out.push('} as const;');
  out.push('');
  return out.join('\n');
}
