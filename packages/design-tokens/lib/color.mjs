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
  const { r, g, b } = hexToRgb(hex);
  const lin = (v) => {
    const c = v / 255;
    return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
  };
  const y = 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
  return y <= 216 / 24389 ? y * 24389 / 27 : Math.cbrt(y) * 116 - 16;
}

function round1(n) {
  // Avoid "-0" and "96.0": both are stable, but "96" is what a human writes.
  const r = Math.round(n * 10) / 10;
  return Object.is(r, -0) ? 0 : r;
}
