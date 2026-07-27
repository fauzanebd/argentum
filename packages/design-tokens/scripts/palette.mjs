// Prints the chart palette's CIE L* ladder and its tightest pair.
//
// Run after editing chart.palette in tokens.json: the `lStar` values recorded
// there are documentation, and this is what recomputes them. T-R3 owns the
// formal colour-vision and greyscale verification; this is the quick check that
// the ladder is still a ladder.

import { lStar } from '../lib/color.mjs';
import { loadTokens } from '../lib/tokens.mjs';

const palette = loadTokens().chart.palette;

console.log('idx  hex      L*     recorded  doc');
for (const [i, tok] of palette.entries()) {
  const l = lStar(tok.hex);
  const drift = Math.abs(l - tok.lStar) > 0.05 ? `  ← tokens.json says ${tok.lStar}` : '';
  console.log(
    `${String(i + 1).padStart(3)}  ${tok.hex}  ${l.toFixed(1).padStart(5)}  ${String(tok.lStar).padStart(8)}  ${tok.doc}${drift}`,
  );
}

let tightest = { gap: Infinity, a: null, b: null };
for (let i = 0; i < palette.length; i++) {
  for (let j = i + 1; j < palette.length; j++) {
    const gap = Math.abs(lStar(palette[i].hex) - lStar(palette[j].hex));
    if (gap < tightest.gap) tightest = { gap, a: palette[i], b: palette[j] };
  }
}
console.log(
  `\ntightest pair: ${tightest.a.hex} vs ${tightest.b.hex} — ΔL* ${tightest.gap.toFixed(1)}`,
);
console.log('(below ~5 the two are the same grey on a monochrome printer)');
