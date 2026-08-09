// Verifies that packages/motion decides nothing for itself (T-V5).
//
//   node scripts/motion-guards.mjs           — print every finding
//   node scripts/motion-guards.mjs --check   — exit 1 if any is unexplained
//
// Two guards, one rule behind them: **a component draws what the plan says and
// chooses nothing.** Colours come from `plan.brand` and sizes from
// `plan.metrics`, both filled in Go from the same `tokens.json` the PDF's
// theme and the dashboard's CSS come from; every string on the plan is already
// wrapped and already formatted. A component that reaches past any of that is
// a second opinion about what the report says, running in a browser, in a
// language away from every test that checks the first one.
//
// **Why this exists at all.** The video's components run in a browser, three
// packages away from `tokens.json`, and CSS makes a colour literal the path of
// least resistance — `color: "#0A0A0A"` is shorter than reading it off the
// plan and does the right thing on the fixture in front of you. It is then
// wrong for every tenant with a brand colour, and it is wrong silently: the
// frame renders, the video encodes, and the only symptom is that a customer's
// deck is not in their colours.
//
// Every colour a scene draws comes from `plan.brand`, which `videoplan` fills
// from the same `tokens.json` the PDF's theme and the dashboard's CSS come
// from — including `T-R5`'s contrast floor, so a pale brand colour is lifted
// once, in Go, for all three formats. A literal in a component is that whole
// chain bypassed.
//
// **The exemption, and why it is in the source.** One thing in this package
// legitimately writes a colour value: an alpha mask, where `#000` in a
// gradient means "opaque" and never reaches a pixel. It is marked in the
// source with `motion-color-ok: <reason>`, which covers the block below it.
// An opt-out written where the code is, with its reason, is auditable in a
// diff; an allowlist in this script is where exemptions accumulate unread by
// anybody working on the component.
//
// The frame drawn when a plan fails validation is *not* exempt, and that is
// the point: it cannot read `plan.brand` — the plan is the broken thing — so
// it reads `tokens.generated.ts` instead, which is generated from the same
// `tokens.json` as everything else.

import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { REPO_ROOT } from '../lib/tokens.mjs';

const ROOT = join(REPO_ROOT, 'packages/motion/src');

// Hex literals, rgb()/rgba()/hsl()/hsla(), and the CSS named colours that turn
// up in real code. Not the full 147-name list: `papayawhip` has never been
// typed by accident, and matching every name means matching `tan`, which is
// also a maths function.
const PATTERNS = [
  /#[0-9a-fA-F]{3,8}\b/g,
  /\b(?:rgb|rgba|hsl|hsla)\s*\(/g,
  /\b(?:black|white|red|green|blue|grey|gray|silver|navy|teal|olive|maroon|aqua|fuchsia|lime|yellow|orange|purple)\b(?=\s*[",;)])/g,
];

// The second guard: a component formatting a figure.
//
// This is the failure T-V5's agreement gate exists to catch, caught one step
// earlier and at the place it would be written. The Go gate proves the PDF,
// the deck and the plan carry the same characters; nothing in it can see a
// component calling `toLocaleString` between the plan and the pixel, and the
// result would be a video whose figures disagree with the PDF attached to the
// same email — grouped in the reader's locale rather than the tenant's.
//
// `videoplan` has already formatted every figure with the tenant's locale and
// currency and wrapped every line to a measured box. There is nothing left
// here to decide.
const FORMATTING = [
  /\.toLocaleString\s*\(/g,
  /\bIntl\.(?:NumberFormat|DateTimeFormat)\b/g,
  /\.toFixed\s*\(/g,
  /\.toLocaleDateString\s*\(/g,
];

const EXEMPT = /motion-color-ok:/;

function walk(dir) {
  const out = [];
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) {
      out.push(...walk(path));
    } else if (/\.(tsx?|css)$/.test(path)) {
      // The generated token module is skipped: it *is* the tokens, and it is
      // already gated — the tokens job regenerates it and fails on any diff,
      // which is a stricter check than this one.
      if (path.endsWith('tokens.generated.ts')) continue;
      out.push(path);
    }
  }
  return out;
}

/**
 * exempted reports whether the marker covers this line.
 *
 * The marker covers everything from the comment down to the next blank line —
 * the block it is written above. Line-and-one-above was the first rule and it
 * was too tight: a multi-line ternary puts the marker four lines from the
 * literal it explains, and a rule that forces the comment onto the same line
 * as the code is a rule that produces `// eslint-disable`-shaped noise.
 */
function exempted(lines, i) {
  for (let j = i; j >= 0; j--) {
    if (EXEMPT.test(lines[j])) return true;
    if (j < i && lines[j].trim() === '') return false;
  }
  return false;
}

const check = process.argv.includes('--check');
const findings = [];
let exemptCount = 0;

for (const path of walk(ROOT).sort()) {
  const lines = readFileSync(path, 'utf8').split('\n');
  lines.forEach((line, i) => {
    const hits = [...PATTERNS, ...FORMATTING].flatMap((p) => line.match(p) ?? []);
    if (hits.length === 0) return;
    if (exempted(lines, i)) {
      exemptCount += hits.length;
      return;
    }
    findings.push({ path: relative(REPO_ROOT, path), line: i + 1, hits, text: line.trim() });
  });
}

for (const f of findings) {
  console.error(`${f.path}:${f.line}  ${f.hits.join(', ')}`);
  console.error(`    ${f.text.slice(0, 100)}`);
}

if (findings.length === 0) {
  console.log(
    `ok      packages/motion picks no colour and formats no figure (${exemptCount} exempted)`,
  );
  process.exit(0);
}

console.error(
  `\n${findings.length} finding(s) in packages/motion.\n\n` +
    'Colours come from plan.brand, which videoplan fills from tokens.json with\n' +
    "T-R5's contrast floor already applied. Figures are formatted once, in Go, in\n" +
    "the tenant's locale — a component that reformats one produces a video whose\n" +
    'numbers disagree with the PDF attached to the same email.\n\n' +
    'If this genuinely decides nothing — an alpha mask reads only the alpha\n' +
    'channel — mark the block `motion-color-ok: <reason>`.',
);
process.exit(check ? 1 : 0);
