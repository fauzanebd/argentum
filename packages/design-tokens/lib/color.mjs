// Colour maths for the token generators. No dependencies: this package runs
// under bare `node`, in CI, before anything is installed.
//
// Hex is the canonical form in tokens.json because it is the form a designer
// reads and the form Go needs. HSL exists only because shadcn/ui's CSS
// variables are HSL triples without the `hsl()` wrapper.

/** @param {string} hex e.g. "#F25C5C" → {r,g,b} in 0..255 */
export function hexToRgb(hex) {
  const m = /^#([0-9a-f]{6})$/i.exec(hex);
  if (!m) throw new Error(`not a 6-digit hex colour: ${JSON.stringify(hex)}`);
  const n = parseInt(m[1], 16);
  return { r: (n >> 16) & 0xff, g: (n >> 8) & 0xff, b: n & 0xff };
}

/** @param {{r:number,g:number,b:number}} rgb → "#RRGGBB" (upper case) */
export function rgbToHex({ r, g, b }) {
  const h = (v) => Math.round(v).toString(16).padStart(2, '0').toUpperCase();
  return `#${h(r)}${h(g)}${h(b)}`;
}

/**
 * Hex → the shadcn CSS variable form: "H S% L%", one decimal place.
 *
 * One decimal is deliberate. Integers cost up to 1.3/255 per channel on the
 * cream and border tokens — small, but the whole point of this package is that
 * the document and the dashboard render the same colour, so the round trip is
 * kept tighter than the eye can see (< 0.5/255 on every token in tokens.json).
 */
export function hexToCssHsl(hex) {
  const { h, s, l } = hexToHsl(hex);
  return `${round1(h)} ${round1(s * 100)}% ${round1(l * 100)}%`;
}

/** @param {string} hex → {h: 0..360, s: 0..1, l: 0..1} */
export function hexToHsl(hex) {
  const { r, g, b } = hexToRgb(hex);
  const [rn, gn, bn] = [r / 255, g / 255, b / 255];
  const max = Math.max(rn, gn, bn);
  const min = Math.min(rn, gn, bn);
  const l = (max + min) / 2;
  const d = max - min;
  if (d === 0) return { h: 0, s: 0, l };
  const s = d / (1 - Math.abs(2 * l - 1));
  let h;
  if (max === rn) h = 60 * (((gn - bn) / d) % 6);
  else if (max === gn) h = 60 * ((bn - rn) / d + 2);
  else h = 60 * ((rn - gn) / d + 4);
  if (h < 0) h += 360;
  return { h, s, l };
}

/** Inverse of hexToHsl, used by the round-trip test. */
export function hslToHex({ h, s, l }) {
  const c = (1 - Math.abs(2 * l - 1)) * s;
  const hp = (((h % 360) + 360) % 360) / 60;
  const x = c * (1 - Math.abs((hp % 2) - 1));
  const [r1, g1, b1] =
    hp < 1 ? [c, x, 0] :
    hp < 2 ? [x, c, 0] :
    hp < 3 ? [0, c, x] :
    hp < 4 ? [0, x, c] :
    hp < 5 ? [x, 0, c] :
             [c, 0, x];
  const m = l - c / 2;
  return rgbToHex({ r: (r1 + m) * 255, g: (g1 + m) * 255, b: (b1 + m) * 255 });
}

/**
 * CIE L* (0 = black, 100 = white) — perceptual lightness.
 *
 * This is the greyscale axis: two chart series with the same L* are the same
 * grey on a black-and-white printer no matter how different their hues are.
 * `pnpm --filter @argentum/design-tokens run palette` prints the spread.
 */
export function lStar(hex) {
  return hexToLab(hex).L;
}

/** sRGB channel (0..255) → linear-light (0..1). */
function toLinear(v) {
  const c = v / 255;
  return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
}

/** Linear-light (0..1) → sRGB channel (0..255), clamped to the gamut. */
function fromLinear(c) {
  const v = c <= 0.0031308 ? c * 12.92 : 1.055 * Math.pow(c, 1 / 2.4) - 0.055;
  return Math.min(255, Math.max(0, v * 255));
}

/**
 * Hex → CIE L*a*b* under D65, the white point sRGB is defined against.
 *
 * Lab rather than RGB because "are these two colours distinguishable" is a
 * question about perception, and RGB distance answers a question about voltage.
 */
export function hexToLab(hex) {
  const { r, g, b } = hexToRgb(hex);
  const [rl, gl, bl] = [toLinear(r), toLinear(g), toLinear(b)];

  // sRGB → XYZ (D65), then normalised by the D65 white point.
  const x = (0.4124564 * rl + 0.3575761 * gl + 0.1804375 * bl) / 0.95047;
  const y = 0.2126729 * rl + 0.7151522 * gl + 0.072175 * bl;
  const z = (0.0193339 * rl + 0.119192 * gl + 0.9503041 * bl) / 1.08883;

  const f = (t) => (t > 216 / 24389 ? Math.cbrt(t) : (t * 24389 / 27 + 16) / 116);
  const [fx, fy, fz] = [f(x), f(y), f(z)];
  return { L: 116 * fy - 16, a: 500 * (fx - fy), b: 200 * (fy - fz) };
}

/**
 * CIE76 ΔE*ab between two colours: the straight-line distance in Lab.
 *
 * CIE76 and not CIEDE2000, which is more faithful and roughly sixty lines of
 * arithmetic with no reference implementation to check it against in a package
 * that deliberately has no dependencies. The two disagree most where CIE76
 * *over*-states a difference (saturated blues), so a palette that clears a
 * generous CIE76 threshold clears the CIEDE2000 one it is standing in for.
 * ~2.3 is the just-noticeable difference; the thresholds in palette.mjs are an
 * order of magnitude above it, which is the margin that makes the cruder metric
 * safe to use here.
 */
export function deltaE76(hexA, hexB) {
  const a = hexToLab(hexA);
  const b = hexToLab(hexB);
  return Math.hypot(a.L - b.L, a.a - b.a, a.b - b.b);
}

// Brettel/Viénot/Mollon (1999) dichromat simulation, in the LMS cone space.
//
// The three matrices below are the published sRGB-linear ↔ LMS pair and the
// projection that collapses one cone response onto the plane the other two
// span. Deuteranopia (no M cone) is the common form and the one T-R3's gate
// names; protanopia (no L cone) is simulated too because it is three numbers
// and because a red/green palette that survives one and not the other has not
// been verified, it has been sampled.
const RGB_TO_LMS = [
  [17.8824, 43.5161, 4.11935],
  [3.45565, 27.1554, 3.86714],
  [0.0299566, 0.184309, 1.46709],
];

const LMS_TO_RGB = [
  [0.080944, -0.130504, 0.116721],
  [-0.0102485, 0.0540194, -0.113615],
  [-0.000365294, -0.00412163, 0.693513],
];

const CVD = {
  // M is reconstructed from L and S: the deuteranope's confusion line.
  deuteranopia: [
    [1, 0, 0],
    [0.494207, 0, 1.24827],
    [0, 0, 1],
  ],
  // L is reconstructed from M and S.
  protanopia: [
    [0, 2.02344, -2.52581],
    [0, 1, 0],
    [0, 0, 1],
  ],
};

function apply(matrix, [x, y, z]) {
  return matrix.map((row) => row[0] * x + row[1] * y + row[2] * z);
}

/**
 * Simulates how a dichromat sees a colour. `type` is "deuteranopia" or
 * "protanopia".
 *
 * The result is what the *simulation* renders, not what the viewer experiences
 * — nobody can know that. What it is good for is the comparison this package
 * needs: two colours that map to the same simulated colour are two colours that
 * viewer cannot tell apart.
 */
export function simulate(hex, type) {
  const matrix = CVD[type];
  if (!matrix) throw new Error(`unknown colour-vision type: ${JSON.stringify(type)}`);
  const { r, g, b } = hexToRgb(hex);
  const lms = apply(RGB_TO_LMS, [toLinear(r), toLinear(g), toLinear(b)]);
  const [rl, gl, bl] = apply(LMS_TO_RGB, apply(matrix, lms));
  return rgbToHex({ r: fromLinear(rl), g: fromLinear(gl), b: fromLinear(bl) });
}

/** Hex → the greyscale a monochrome printer produces: L* re-encoded as sRGB. */
export function greyscale(hex) {
  const { L } = hexToLab(hex);
  const y = L > 8 ? Math.pow((L + 16) / 116, 3) : (L * 27) / 24389;
  const v = fromLinear(y);
  return rgbToHex({ r: v, g: v, b: v });
}

function round1(n) {
  // Avoid "-0" and "96.0": both are stable, but "96" is what a human writes.
  const r = Math.round(n * 10) / 10;
  return Object.is(r, -0) ? 0 : r;
}
