import { describe, expect, it } from "vitest";

import { contrastOnWhite, contrastRatio, parseHex } from "./contrast";

/**
 * This module exists to show a customer the contrast number moving as they drag
 * a colour picker, and it is deliberately identical in definition to
 * `apps/backend/internal/report/theme/contrast.go` — the server is the
 * authority and two answers to "is this readable" is worse than one strict one.
 *
 * So what is worth testing is exactly that agreement: the anchors WCAG fixes
 * for any correct implementation, which both sides must reproduce.
 */
describe("WCAG contrast, to the same definition the backend uses", () => {
  it("puts black on white at 21:1 and white on white at 1:1", () => {
    expect(contrastOnWhite("#000000")).toBeCloseTo(21, 5);
    expect(contrastOnWhite("#FFFFFF")).toBeCloseTo(1, 5);
  });

  it("is symmetric", () => {
    const a = { r: 18, g: 52, b: 86 };
    const b = { r: 255, g: 255, b: 255 };
    expect(contrastRatio(a, b)).toBeCloseTo(contrastRatio(b, a), 10);
  });

  // The three light-ramp series `make palette` warns about, as the palette
  // script measures them. If this file and that script ever disagree, one of
  // them is lying to somebody choosing a colour.
  it("reproduces the palette script's figures for the warned series", () => {
    expect(contrastOnWhite("#EAAA3E")).toBeCloseTo(2.04, 2);
    expect(contrastOnWhite("#CACCD1")).toBeCloseTo(1.61, 2);
    expect(contrastOnWhite("#5CA8E0")).toBeCloseTo(2.58, 2);
  });
});

describe("parseHex stores exactly what the API is sent", () => {
  it("accepts six digits with or without the hash", () => {
    expect(parseHex("#F25C5C")).toEqual({ r: 242, g: 92, b: 92 });
    expect(parseHex("F25C5C")).toEqual({ r: 242, g: 92, b: 92 });
  });

  // Three-digit shorthand is refused deliberately: the API stores the string it
  // is given, so accepting a form the server will not is a preview that lies.
  it("refuses shorthand and anything else", () => {
    for (const bad of ["#FFF", "", "#GGGGGG", "#12345", "rgb(1,2,3)"]) {
      expect(parseHex(bad)).toBeNull();
    }
  });
});
