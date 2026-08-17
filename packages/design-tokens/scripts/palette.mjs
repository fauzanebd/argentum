// Verifies the chart palettes are readable by the people who will read them.
//
// A categorical palette has one job: tell N series apart. It fails that job in
// ways nobody catches by looking at their own screen — a reader with a
// red/green deficiency (~8% of men), the office laser printer an enterprise
// report actually comes out of, and (since the dashboard started drawing
// charts) a dark background the palette was never measured against. All three
// are checked here, all three are gated, and the numbers are printed rather
// than asserted silently so an edit to tokens.json can see what headroom it has
// left.
//
//   pnpm --filter @argentum/design-tokens run palette          — print the report
//   pnpm --filter @argentum/design-tokens run palette --check  — exit 1 on a failure
//
// The method, stated once (T-R3's acceptance asks for it):
//
//   Greyscale — CIE L* is the greyscale axis, so the printed distance between
//   two series is |ΔL*|. Below ~5 the two are the same grey on paper.
//
//   Colour vision — Brettel/Viénot/Mollon (1999) LMS-space simulation of
//   deuteranopia and protanopia, then CIE76 ΔE*ab between the simulated pairs.
//   ~2.3 ΔE is a just-noticeable difference; a pair below ~12 is two series a
//   dichromat has to work to separate at the width of a chart line.
//
//   Contrast — WCAG relative luminance against the surface the marks are drawn
//   on. A chart mark is non-text, where the line is 3:1.
//
// **The two ramps are checked against different floors, and the difference is
// the argument.** The light ramp carries the greyscale floor because a document
// built from it gets printed. The dark ramp does not, because nothing prints a
// dark dashboard — and applying the floor anyway would push series into
// near-white to satisfy a reader who does not exist. What replaces it there is
// a normal-vision ΔE floor, which is what greyscale was standing in for at a
// monitor, plus the contrast check that is the reason the dark ramp exists.
//
// The thresholds are floors, not targets. Both palettes clear them with margin,
// and the report prints the margin so it stays that way.

import { deltaE76, greyscale, hexToRgb, lStar, simulate } from '../lib/color.mjs';
import { loadTokens } from '../lib/tokens.mjs';

const MIN_GREY_DELTA_L = 5;
const MIN_CVD_DELTA_E = 12;
// On screen, in place of the greyscale floor the dark ramp does not carry.
const MIN_NORMAL_DELTA_E = 15;
const MIN_CONTRAST = 3;

// The grounds the marks are drawn on: `--card` in each theme. The light one is
// tokens.json's `surface`; the dark one is hand-written in the dashboard's
// index.css, which is where the dark palette lives (documents are light-only,
// so tokens.json has no dark surface to read).
const LIGHT_SURFACE = '#FFFFFF';
const DARK_SURFACE = '#232427';

const check = process.argv.includes('--check');
const chart = loadTokens().chart;
const failures = [];
const warnings = [];

function relLum(hex) {
  const { r, g, b } = hexToRgb(hex);
  const f = (c) => {
    const v = c / 255;
    return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
}

function contrast(a, b) {
  const [hi, lo] = [relLum(a), relLum(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

/**
 * Reports the tightest pair under a transform, and every pair below the floor.
 *
 * Pairwise and not nearest-neighbour-in-order: a chart draws whichever series
 * the data has, so series 2 and series 7 sit beside each other as readily as
 * series 2 and series 3.
 */
function worstPair(palette, label, transform, distance, floor, unit) {
  let tightest = { d: Infinity, a: null, b: null };
  const below = [];
  for (let i = 0; i < palette.length; i++) {
    for (let j = i + 1; j < palette.length; j++) {
      const a = palette[i];
      const b = palette[j];
      const d = distance(transform(a.hex), transform(b.hex));
      if (d < tightest.d) tightest = { d, a, b };
      if (d < floor) below.push({ d, a, b });
    }
  }
  console.log(
    `\n${label}\n  tightest: ${tightest.a.hex} (${tightest.a.doc}) vs ${tightest.b.hex} (${tightest.b.doc})` +
      ` — ${unit} ${tightest.d.toFixed(1)}  [floor ${floor}]`,
  );
  for (const p of below) {
    console.log(`  BELOW FLOOR: ${p.a.hex} vs ${p.b.hex} — ${unit} ${p.d.toFixed(1)}`);
    failures.push(
      `${label}: ${p.a.hex} (${p.a.doc}) and ${p.b.hex} (${p.b.doc}) are ${unit} ${p.d.toFixed(1)} apart, floor is ${floor}`,
    );
  }
}

/**
 * Every series against the ground it is drawn on.
 *
 * `gate` is false for the light ramp, and that is a recorded debt rather than
 * an exemption. Three of its series are below 3:1 on white — amber 2.04, grey
 * 1.61, azure 2.58 — and have been since T-R3, because contrast against a
 * surface was never one of the checks that chose them. Raising them is a change
 * to the palette every delivered PDF was rendered with, and to a CIE L* ladder
 * tuned against greyscale and two deficiencies; it is its own ticket, taken
 * deliberately, not something the dark-mode change should smuggle in. So the
 * numbers print, loudly, and the build stays green until somebody decides.
 *
 * The dark ramp IS gated, because it is new: nothing was drawn with it before
 * this check existed, so there is no debt to inherit.
 */
function contrastAgainst(palette, label, surface, { gate }) {
  console.log(`\n${label} (surface ${surface})`);
  let worst = { c: Infinity, tok: null };
  for (const tok of palette) {
    const c = contrast(tok.hex, surface);
    if (c < worst.c) worst = { c, tok };
    if (c < MIN_CONTRAST) {
      const line = `${label}: ${tok.hex} (${tok.doc}) is ${c.toFixed(2)}:1 against ${surface}, floor is ${MIN_CONTRAST}`;
      console.log(`  ${gate ? 'BELOW FLOOR' : 'BELOW FLOOR (not gated)'}: ${tok.hex} — ${c.toFixed(2)}:1  ${tok.doc}`);
      (gate ? failures : warnings).push(line);
    }
  }
  console.log(
    `  weakest: ${worst.tok.hex} (${worst.tok.doc}) — ${worst.c.toFixed(2)}:1  [floor ${MIN_CONTRAST}]`,
  );
}

function table(palette, title) {
  console.log(`\n${title}`);
  console.log('idx  hex      L*     recorded  doc');
  for (const [i, tok] of palette.entries()) {
    const l = lStar(tok.hex);
    const drift = Math.abs(l - tok.lStar) > 0.05 ? `  ← tokens.json says ${tok.lStar}` : '';
    console.log(
      `${String(i + 1).padStart(3)}  ${tok.hex}  ${l.toFixed(1).padStart(5)}  ${String(tok.lStar).padStart(8)}  ${tok.doc}${drift}`,
    );
    if (drift) {
      failures.push(`lStar drift: ${tok.hex} is ${l.toFixed(1)}, tokens.json records ${tok.lStar}`);
    }
  }
}

const deltaL = (a, b) => Math.abs(lStar(a) - lStar(b));
const identity = (hex) => hex;

// ── The light ramp: paper, colour vision, and the white card ────────────────
table(chart.palette, 'Light ramp (documents and the light dashboard)');
worstPair(chart.palette, 'Greyscale (monochrome print)', greyscale, deltaL, MIN_GREY_DELTA_L, 'ΔL*');
worstPair(
  chart.palette,
  'Deuteranopia (Viénot 1999)',
  (hex) => simulate(hex, 'deuteranopia'),
  deltaE76,
  MIN_CVD_DELTA_E,
  'ΔE*ab',
);
worstPair(
  chart.palette,
  'Protanopia (Viénot 1999)',
  (hex) => simulate(hex, 'protanopia'),
  deltaE76,
  MIN_CVD_DELTA_E,
  'ΔE*ab',
);
contrastAgainst(chart.palette, 'Contrast on the light card', LIGHT_SURFACE, { gate: false });

// ── The dark ramp: colour vision, on-screen separation, and the dark card ───
if (chart.paletteDark) {
  if (chart.paletteDark.length !== chart.palette.length) {
    failures.push(
      `the dark ramp has ${chart.paletteDark.length} series and the light one has ${chart.palette.length}; ` +
        `a chart indexes both by series number, so they cannot differ in length`,
    );
  }
  table(chart.paletteDark, 'Dark ramp (the dashboard on a dark ground)');
  worstPair(
    chart.paletteDark,
    'Dark — normal vision',
    identity,
    deltaE76,
    MIN_NORMAL_DELTA_E,
    'ΔE*ab',
  );
  worstPair(
    chart.paletteDark,
    'Dark — deuteranopia (Viénot 1999)',
    (hex) => simulate(hex, 'deuteranopia'),
    deltaE76,
    MIN_CVD_DELTA_E,
    'ΔE*ab',
  );
  worstPair(
    chart.paletteDark,
    'Dark — protanopia (Viénot 1999)',
    (hex) => simulate(hex, 'protanopia'),
    deltaE76,
    MIN_CVD_DELTA_E,
    'ΔE*ab',
  );
  contrastAgainst(chart.paletteDark, 'Contrast on the dark card', DARK_SURFACE, { gate: true });
  // Deliberately no greyscale check here — see the note at the top and the
  // $paletteDarkComment in tokens.json.
}

if (warnings.length > 0) {
  console.log(`\n${warnings.length} warning(s) — recorded, not gated:`);
  for (const w of warnings) console.log(`  - ${w}`);
}

if (failures.length === 0) {
  console.log(`\nOK — ${chart.palette.length} series clear every gated floor in both ramps.`);
} else {
  console.log(`\n${failures.length} failure(s):`);
  for (const f of failures) console.log(`  - ${f}`);
  if (check) process.exit(1);
}
