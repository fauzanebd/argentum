// Verifies the chart palette is readable by the people who will read it.
//
// A categorical palette has one job: tell N series apart. It fails that job in
// two ways that nobody catches by looking at a screen — a reader with a
// red/green deficiency (~8% of men), and the office laser printer an enterprise
// report actually comes out of. Both are checked here, both are gated, and the
// numbers are printed rather than asserted silently so an edit to tokens.json
// can see what headroom it has left.
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
// The thresholds are floors, not targets. The current palette clears them with
// margin, and the report prints the margin so it stays that way.

import { deltaE76, greyscale, lStar, simulate } from '../lib/color.mjs';
import { loadTokens } from '../lib/tokens.mjs';

// Floors. Raising one is a design decision; lowering one to make a new colour
// fit is how a palette stops being verified.
const MIN_GREY_DELTA_L = 5;
const MIN_CVD_DELTA_E = 12;

const check = process.argv.includes('--check');
const palette = loadTokens().chart.palette;
const failures = [];

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

/**
 * Reports the tightest pair under a transform, and every pair below the floor.
 *
 * Pairwise and not nearest-neighbour-in-order: a chart draws whichever series
 * the data has, so series 2 and series 7 sit beside each other as readily as
 * series 2 and series 3.
 */
function worstPair(label, transform, distance, floor, unit) {
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

const deltaL = (a, b) => Math.abs(lStar(a) - lStar(b));

worstPair('Greyscale (monochrome print)', (hex) => greyscale(hex), deltaL, MIN_GREY_DELTA_L, 'ΔL*');
worstPair('Deuteranopia (Viénot 1999)', (hex) => simulate(hex, 'deuteranopia'), deltaE76, MIN_CVD_DELTA_E, 'ΔE*ab');
worstPair('Protanopia (Viénot 1999)', (hex) => simulate(hex, 'protanopia'), deltaE76, MIN_CVD_DELTA_E, 'ΔE*ab');

if (failures.length === 0) {
  console.log(`\nOK — ${palette.length} series clear every floor.`);
} else {
  console.log(`\n${failures.length} failure(s):`);
  for (const f of failures) console.log(`  - ${f}`);
  if (check) process.exit(1);
}
