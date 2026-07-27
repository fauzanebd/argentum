// Generator 2: tokens.json → apps/backend/internal/report/theme/tokens_gen.go
//
// Emits values only. The types (Color, TypeScaleTokens, …) and everything
// derived from them are hand-written in theme.go, so this generator never has
// an opinion about how a token is used — the same rule the CSS side follows.
//
// Output must be byte-identical to gofmt's, because `make tokens` runs
// `gofmt -l` over it and CI diffs the result. Every value therefore sits on a
// line of its own preceded by a doc comment: gofmt aligns adjacent assignments
// and adjacent trailing comments, and a comment line between entries removes
// the adjacency, which removes the alignment question entirely. The one place
// with adjacent lines is ChartPalette, where every code segment is the same
// width, so a single space before each trailing comment is already canonical.

import { join } from 'node:path';
import { hexToRgb } from '../lib/color.mjs';
import { banner, groupEntries, groupVisible, pascal, REPO_ROOT } from '../lib/tokens.mjs';

export const GO_OUT = join(REPO_ROOT, 'apps/backend/internal/report/theme/tokens_gen.go');

// Token names whose Go spelling is not a straight capitalisation.
const GO_NAMES = { xs: 'XS', sm: 'SM', md: 'MD', lg: 'LG', xl: 'XL', h1: 'H1', h2: 'H2' };

// Every group in tokens.json must be handled below. An unhandled group is a
// token someone added expecting the report renderer to see it.
const HANDLED = new Set(['color', 'font', 'radius', 'typeScale', 'spacing', 'print', 'chart']);

// Doc comments wrap here. Matches what the rest of the backend is written to.
const WRAP = 88;

export function renderGo(tokens) {
  const unhandled = Object.keys(tokens).filter((k) => !k.startsWith('$') && !HANDLED.has(k));
  if (unhandled.length > 0) {
    throw new Error(
      `tokens.json: group(s) ${unhandled.join(', ')} are not handled by scripts/gen-go.mjs`,
    );
  }

  const out = [];
  out.push(banner('//'));
  out.push('');
  out.push('package theme');
  out.push('');

  colors(tokens, out);
  fonts(tokens, out);
  radius(tokens, out);
  scale(tokens, out, {
    group: 'typeScale',
    varName: 'TypeScale',
    typeName: 'TypeScaleTokens',
    unit: 'pt',
    doc: 'TypeScale is the print type scale, in points.',
  });
  scale(tokens, out, {
    group: 'spacing',
    varName: 'Spacing',
    typeName: 'SpacingTokens',
    unit: 'mm',
    doc: 'Spacing is the vertical rhythm, in millimetres.',
  });
  scale(tokens, out, {
    group: 'print',
    varName: 'Page',
    typeName: 'PageTokens',
    unit: 'mm',
    doc: 'Page is the page geometry, in millimetres.',
  });
  palette(tokens, out);

  return out.join('\n');
}

function colors(tokens, out) {
  out.push('// Brand colours. Light palette only — a document has no dark mode.');
  out.push('var (');
  let first = true;
  for (const [name, tok] of groupEntries(tokens, 'color', 'print')) {
    if (!first) out.push('');
    first = false;
    const id = `Color${goName(name)}`;
    out.push(...wrap(`${id} — ${tok.doc}`, '\t// '));
    out.push(`\t${id} = Color${goColor(tok.hex)} // ${tok.hex}`);
  }
  out.push(')');
  out.push('');
}

function fonts(tokens, out) {
  out.push('// Font families as registered with maroto. See fonts.go for the faces.');
  out.push('const (');
  let first = true;
  for (const [name, tok] of groupEntries(tokens, 'font', 'print')) {
    if (!first) out.push('');
    first = false;
    const id = `Font${goName(name)}`;
    out.push(...wrap(`${id} (${tok.family}) — ${tok.doc}`, '\t// '));
    out.push(`\t${id} = ${JSON.stringify(tok.key)}`);
  }
  out.push(')');
  out.push('');
}

function radius(tokens, out) {
  const base = tokens.radius.base;
  out.push(
    ...wrap(
      `RadiusBase is the corner radius in millimetres — the print equivalent of the ` +
        `dashboard's ${base.rem}rem. ${base.doc}`,
      '// ',
    ),
  );
  out.push(`const RadiusBase = ${num(base.mm)}`);
  out.push('');
}

/** A group of single-value tokens emitted as one keyed struct literal. */
function scale(tokens, out, { group, varName, typeName, unit, doc }) {
  if (!groupVisible(tokens, group, 'print')) return;
  out.push(...wrap(doc, '// '));
  out.push(`var ${varName} = ${typeName}{`);
  let first = true;
  for (const [name, tok] of groupEntries(tokens, group, 'print')) {
    if (!first) out.push('');
    first = false;
    const id = goName(name);
    out.push(...wrap(`${id} — ${tok.doc}`, '\t// '));
    out.push(`\t${id}: ${num(tok[unit])},`);
  }
  out.push('}');
  out.push('');
}

function palette(tokens, out) {
  if (!groupVisible(tokens, 'chart', 'print')) return;
  out.push(
    ...wrap(
      'ChartPalette is the categorical series palette, ordered by series index. Every ' +
        'entry sits on a rung of a CIE L* ladder so the palette survives a black-and-white ' +
        'printer and deuteranopia; tokens.json states the method.',
      '// ',
    ),
  );
  out.push('var ChartPalette = []Color{');
  for (const tok of tokens.chart.palette) {
    out.push(`\t${goColor(tok.hex)}, // ${tok.hex} · ${tok.doc} · L* ${tok.lStar.toFixed(1)}`);
  }
  out.push('}');
  out.push('');
}

/** A composite literal inside []Color needs no type name — `gofmt -s` agrees. */
function goColor(hex) {
  const { r, g, b } = hexToRgb(hex);
  const h = (v) => `0x${v.toString(16).toUpperCase().padStart(2, '0')}`;
  return `{R: ${h(r)}, G: ${h(g)}, B: ${h(b)}}`;
}

/** Token numbers are written as they appear in tokens.json: 18, not 18.0. */
function num(n) {
  return String(n);
}

function goName(name) {
  return GO_NAMES[name] ?? pascal(name);
}

/** Greedy word wrap to WRAP columns, each line carrying the comment prefix. */
function wrap(text, prefix) {
  const lines = [];
  let line = '';
  for (const word of text.split(/\s+/)) {
    const candidate = line ? `${line} ${word}` : word;
    // Tabs count as one character here; the backend wraps by eye anyway, and a
    // stable rule beats an exactly-right one that nobody can reproduce.
    if (line && prefix.length + candidate.length > WRAP) {
      lines.push(prefix + line);
      line = word;
    } else {
      line = candidate;
    }
  }
  if (line) lines.push(prefix + line);
  return lines;
}
