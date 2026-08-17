// Generator 1: tokens.json → apps/dashboard/src/tokens.generated.css
//
// Emits shadcn/ui's custom properties for the light theme, plus the font stack.
// It does not emit Tailwind classes or utilities: this file's job is to publish
// variables, and tailwind.config.ts decides what to do with them.
//
// The shadcn variable names live here rather than in tokens.json because they
// are a property of this consumer. tokens.json says "surface"; shadcn happens
// to want that same colour under three names (card, popover, input).

import { join } from 'node:path';
import { hexToCssHsl } from '../lib/color.mjs';
import { banner, groupEntries, groupVisible, kebab, REPO_ROOT } from '../lib/tokens.mjs';

/** The chart series the web sees, or none when the group is print-scoped. */
function chartPalette(tokens) {
  return groupVisible(tokens, 'chart', 'web') ? tokens.chart.palette : [];
}

/**
 * The same series on a dark ground, or none.
 *
 * Emitted under `.dark` rather than composed from the light ramp: four of the
 * eight are the same colour in both themes and four are lifted, because a
 * blanket lightening would have moved series that were already legible and
 * broken the identity a reader relies on when they switch.
 */
function chartPaletteDark(tokens) {
  if (!groupVisible(tokens, 'chart', 'web')) return [];
  return tokens.chart.paletteDark ?? [];
}

export const CSS_OUT = join(REPO_ROOT, 'apps/dashboard/src/tokens.generated.css');

// shadcn custom property → colour token name. Several names may share a token.
const COLOR_VARS = {
  background: 'background',
  foreground: 'foreground',
  card: 'surface',
  'card-foreground': 'foreground',
  popover: 'surface',
  'popover-foreground': 'foreground',
  primary: 'primary',
  'primary-foreground': 'primaryForeground',
  secondary: 'surfaceMuted',
  'secondary-foreground': 'foreground',
  muted: 'surfaceSubtle',
  'muted-foreground': 'muted',
  'muted-subtle': 'mutedSubtle',
  inset: 'surfaceInset',
  accent: 'primary',
  'accent-foreground': 'primaryForeground',
  'primary-tint': 'primaryTint',
  'primary-ink': 'primaryInk',
  destructive: 'destructive',
  'destructive-foreground': 'destructiveForeground',
  'destructive-tint': 'destructiveTint',
  'destructive-ink': 'destructiveInk',
  positive: 'positive',
  // Nothing legible sits on a #189A4D or #EF720C fill except white, and both
  // already have it under another name. A `positiveForeground` token would be a
  // third #FFFFFF whose only job is to drift from the other two.
  'positive-foreground': 'primaryForeground',
  'positive-tint': 'positiveTint',
  'positive-ink': 'positiveInk',
  warning: 'warning',
  'warning-foreground': 'primaryForeground',
  'warning-tint': 'warningTint',
  'warning-ink': 'warningInk',
  border: 'border',
  'border-strong': 'borderStrong',
  // --input is the *outline* of a control, never a fill: every use in the
  // dashboard is `border-input` (input.tsx, textarea.tsx, select.tsx,
  // button.tsx's outline variant). It therefore takes the interactive line
  // colour, not the field fill — `field` below is the fill, and they are two
  // tokens because a filled input still needs an edge on hover.
  input: 'borderStrong',
  field: 'field',
  ring: 'primary',
  'sidebar-background': 'sidebarSurface',
  'sidebar-foreground': 'sidebarForeground',
  'sidebar-primary': 'primary',
  'sidebar-primary-foreground': 'primaryForeground',
  'sidebar-accent': 'sidebarAccent',
  'sidebar-accent-foreground': 'sidebarAccentForeground',
  'sidebar-border': 'border',
  'sidebar-ring': 'primary',
};

// Generic families appended after the token's family name.
const FONT_FALLBACK = 'sans-serif';

export function renderCSS(tokens) {
  const colors = new Map(groupEntries(tokens, 'color', 'web'));

  // A token nobody maps would vanish silently, which is the failure mode this
  // whole package exists to remove. Adding a colour to tokens.json therefore
  // means adding it here too — or marking it scope: "print".
  const mapped = new Set(Object.values(COLOR_VARS));
  const orphans = [...colors.keys()].filter((name) => !mapped.has(name));
  if (orphans.length > 0) {
    throw new Error(
      `tokens.json: web-visible colour token(s) ${orphans.join(', ')} are not mapped to a CSS ` +
        `variable in scripts/gen-css.mjs. Map them, or set "scope": "print".`,
    );
  }

  const lines = [];
  lines.push('/*');
  lines.push(banner(' *'));
  lines.push(' */');
  lines.push('');
  lines.push('/* Light theme only. The dark palette is hand-written in index.css: documents');
  lines.push(' * are printed and forwarded, so tokens.json is light-only by design. */');
  lines.push(':root {');

  lines.push('  /* Colours as bare `H S% L%` triples — the form shadcn/ui composes into');
  lines.push('   * hsl(var(--x)). The hex in each comment is the value in tokens.json. */');
  for (const [cssName, tokenName] of Object.entries(COLOR_VARS)) {
    const tok = colors.get(tokenName);
    if (!tok) {
      throw new Error(
        `scripts/gen-css.mjs maps --${cssName} to colour token ${JSON.stringify(tokenName)}, ` +
          `which tokens.json does not define for the web.`,
      );
    }
    lines.push(`  --${cssName}: ${hexToCssHsl(tok.hex)}; /* ${tok.hex} */`);
  }

  lines.push('');
  lines.push('  /* Categorical chart series, by index (T-U11). Emitted as hex rather than');
  lines.push('   * as the `H S% L%` triples above: nothing composes these through');
  lines.push('   * hsl(), they are handed to a charting library as colour strings, and a');
  lines.push('   * triple would have to be wrapped at every call site. tokens.json states');
  lines.push('   * the CIE L* method that chose them and `make palette` verifies it. */');
  for (const [i, tok] of chartPalette(tokens).entries()) {
    lines.push(`  --chart-${i + 1}: ${tok.hex}; /* ${tok.doc} */`);
  }

  lines.push('');
  const radius = tokens.radius.base;
  lines.push(`  --radius: ${radius.rem}rem; /* ${radius.doc} */`);

  lines.push('');
  lines.push('  /* Font stacks. index.css consumes these instead of naming the family. */');
  for (const [name, tok] of groupEntries(tokens, 'font', 'web')) {
    lines.push(`  --font-${kebab(name)}: '${tok.family}', ${FONT_FALLBACK};`);
  }

  lines.push('}');

  const dark = chartPaletteDark(tokens);
  if (dark.length > 0) {
    lines.push('');
    lines.push('/* The chart series on a dark ground. This is the one part of the dark');
    lines.push(' * palette that is generated rather than hand-written in index.css: the');
    lines.push(' * series are verified against colour-vision deficiency and against the dark');
    lines.push(' * card by `make palette`, and a hand-written copy would be the drift that');
    lines.push(' * check exists to catch.');
    lines.push(' *');
    lines.push(' * Same specificity as :root and later in source, so it wins when <html> has');
    lines.push(' * the class — the rule index.css states for the rest of the dark palette. */');
    lines.push('.dark {');
    for (const [i, tok] of dark.entries()) {
      lines.push(`  --chart-${i + 1}: ${tok.hex}; /* ${tok.doc} */`);
    }
    lines.push('}');
  }

  lines.push('');
  return lines.join('\n');
}
