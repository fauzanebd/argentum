/**
 * WCAG 2.1 contrast, for the live readout beside the report accent colour.
 *
 * The server is the authority — `PUT /api/reports/branding` rejects anything
 * below the floor with the measured ratio in the message, and this file cannot
 * be trusted because a browser can be edited. It exists so the customer sees
 * the number move as they drag the colour picker rather than after a round
 * trip, which is the difference between choosing a colour and guessing one.
 *
 * Kept deliberately identical in definition to
 * apps/backend/internal/report/theme/contrast.go. Two answers to "is this
 * readable" is worse than one strict one.
 */

export interface Rgb {
  r: number;
  g: number;
  b: number;
}

/** Parses `#RRGGBB` or `RRGGBB`. Returns null for anything else — three-digit
 *  shorthand included, because the API stores exactly what it is sent. */
export function parseHex(input: string): Rgb | null {
  const raw = input.trim().replace(/^#/, "");
  if (!/^[0-9a-fA-F]{6}$/.test(raw)) return null;
  return {
    r: parseInt(raw.slice(0, 2), 16),
    g: parseInt(raw.slice(2, 4), 16),
    b: parseInt(raw.slice(4, 6), 16),
  };
}

function relativeLuminance({ r, g, b }: Rgb): number {
  const lin = (v: number) => {
    const s = v / 255;
    return s <= 0.04045 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  };
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
}

/** Contrast ratio between two colours, 1 to 21. */
export function contrastRatio(a: Rgb, b: Rgb): number {
  const [hi, lo] = [relativeLuminance(a), relativeLuminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

/** Contrast against white — the surface a report is printed on. */
export function contrastOnWhite(hex: string): number | null {
  const rgb = parseHex(hex);
  if (!rgb) return null;
  return contrastRatio(rgb, { r: 255, g: 255, b: 255 });
}
