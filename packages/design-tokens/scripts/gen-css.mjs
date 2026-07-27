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
import { banner, groupEntries, kebab, REPO_ROOT } from '../lib/tokens.mjs';

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
  accent: 'primary',
  'accent-foreground': 'primaryForeground',
  destructive: 'destructive',
  'destructive-foreground': 'destructiveForeground',
  border: 'border',
  input: 'surface',
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
  const radius = tokens.radius.base;
  lines.push(`  --radius: ${radius.rem}rem; /* ${radius.doc} */`);

  lines.push('');
  lines.push('  /* Font stacks. index.css consumes these instead of naming the family. */');
  for (const [name, tok] of groupEntries(tokens, 'font', 'web')) {
    lines.push(`  --font-${kebab(name)}: '${tok.family}', ${FONT_FALLBACK};`);
  }

  lines.push('}');
  lines.push('');
  return lines.join('\n');
}
