// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";

// Deliberately no static `import … from "./motion"` in this file. A static
// import pins one instance of the module past `vi.resetModules()`, so the
// re-import below hands back the first arm's copy and the reduced-motion arm
// silently asserts against the moving one. That is the second way this file
// found to fool itself; the first is in forceReducedMotion's comment.

/**
 * T-U2's gating, re-homed.
 *
 * The Sprint 3 delivery log records that this was "proved with a throwaway
 * script because the workspace has none", and lists "No test runner" as the
 * sprint's open item — every component in it untested in the sense CI would
 * mean. The script is gone; this is the same proof, kept.
 *
 * `useReducedMotion` reads `prefers-reduced-motion` through `matchMedia`, which
 * jsdom does not implement. Stubbing it is only half the job: **framer resolves
 * the preference once and caches it in module state**, so the first arm to run
 * fixes the answer for every arm after it — the first draft of this file
 * stubbed `matchMedia` per test and watched the reduced arm read the moving
 * arm's value. Each arm therefore stubs first and re-imports the module under
 * test, so framer initialises against the preference that arm is about.
 *
 * That caching is worth knowing beyond the test: the preference is read at
 * mount, so a user who changes the OS setting mid-session sees it apply on the
 * next load rather than immediately.
 */
function forceReducedMotion(reduce: boolean) {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    configurable: true,
    value: (query: string) => ({
      matches: query.includes("prefers-reduced-motion") ? reduce : false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  });
}

/** Stub the preference, then load the module fresh against it. */
async function motionUnder(reduce: boolean) {
  forceReducedMotion(reduce);
  vi.resetModules();
  return import("./motion");
}

describe("the motion preference is honoured, not remembered", () => {
  beforeEach(() => vi.resetModules());

  it("keeps the spatial half when motion is welcome", async () => {
    const { useEnter } = await motionUnder(false);
    const { result } = renderHook(() => useEnter());
    expect(result.current.hidden).toMatchObject({ opacity: 0, y: 8 });
    expect(result.current.visible).toMatchObject({ opacity: 1, y: 0 });
  });

  // still() rewrites rather than replaces, so a component's `initial="hidden"`
  // keeps resolving in both modes. A variant set that lost its names under
  // reduced motion would throw at the call site instead of simply not moving.
  it("keeps the variant names identical in both modes", async () => {
    const set = { hidden: { opacity: 0, y: 8 }, visible: { opacity: 1, y: 0 } };
    const moving = await motionUnder(false);
    const a = renderHook(() => moving.useVariants(set));
    const stillMod = await motionUnder(true);
    const b = renderHook(() => stillMod.useVariants(set));
    expect(Object.keys(b.result.current).sort()).toEqual(Object.keys(a.result.current).sort());
  });
});

/**
 * The durations are shared with the video renderer, expressed there in frames
 * at 30fps. Two timing systems is how "one product" stops being true, so the
 * conversion is asserted rather than trusted to a comment.
 */
describe("the dashboard and the video agree on timing", () => {
  it("states the video's 12 / 8 / 4 frames at 30fps, in seconds", async () => {
    const { DURATION } = await import("./motion");
    expect(Math.round(DURATION.enter * 30)).toBe(12);
    expect(Math.round(DURATION.exit * 30)).toBe(8);
    expect(Math.round(DURATION.stagger * 30)).toBe(4);
  });

  it("states packages/motion's curve", async () => {
    const { CURVE } = await import("./motion");
    expect(CURVE).toEqual([0.16, 1, 0.3, 1]);
  });

  // "A component that invents a fourth duration is a review finding."
  it("offers exactly three durations", async () => {
    const { DURATION } = await import("./motion");
    expect(Object.keys(DURATION)).toHaveLength(3);
  });
});
