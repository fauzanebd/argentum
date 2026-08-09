import assert from "node:assert/strict";
import test from "node:test";

import { partition, validate, timeline, SUPPORTED_VERSION } from "@argentum/motion";
import type { Plan } from "@argentum/motion";

/**
 * The checks that run before a browser is started.
 *
 * `node --test` rather than a framework: there are two modules worth testing in
 * this service and neither of them needs a runner with a plugin ecosystem. The
 * render itself is covered by the fixture CLI and by T-V5's still comparison —
 * a unit test that asserts an MP4 came out is a two-minute test that tells you
 * less than looking at one frame.
 */

function plan(overrides: Partial<Plan> = {}): Plan {
  const base: Plan = {
    version: SUPPORTED_VERSION,
    width: 1920,
    height: 1080,
    fps: 30,
    total_frames: 120,
    locale: "en",
    title: "Test",
    metrics: {
      margin_x: 136,
      margin_top: 96,
      margin_bottom: 68,
      content_width: 1648,
      body_top: 255,
      body_height: 717,
      title_band: 125,
      footer_band: 40,
      footer_top: 972,
      title_rule_width: 193,
      title_rule_thickness: 9,
      radius: 18,
      spacing_sm: 23,
      spacing_md: 34,
      spacing_lg: 57,
      leading: 1.45,
      type: { display: 86, h1: 58, h2: 47, body: 36, caption: 29 },
    },
    brand: {
      name: "",
      primary: "#F25C5C",
      primary_on_dark: "#F25C5C",
      foreground: "#0A0A0A",
      background: "#F5F5F0",
      muted: "#6B6B6B",
      border: "#E2E2DC",
      dark: "#0A0A0A",
      on_dark: "#F5F5F0",
      surface: "#FFFFFF",
      surface_subtle: "#ECECEA",
      positive: "#3F7A46",
      destructive: "#EF4343",
      tones: {},
    },
    scenes: [
      { kind: "cover", frames: 60, title: ["One"] },
      { kind: "closing", frames: 60, title: ["Two"] },
    ],
  };
  return { ...base, ...overrides };
}

test("a well-formed plan validates", () => {
  assert.equal(validate(plan()), null);
});

test("a version this renderer does not know is refused, and the message says which it draws", () => {
  const problem = validate(plan({ version: 99 }));
  assert.ok(problem);
  assert.match(problem, /version 99/);
  assert.match(problem, new RegExp(`version ${SUPPORTED_VERSION}`));
});

test("a plan whose frames do not add up is refused", () => {
  // The renderer sets the composition's length from total_frames, so a plan
  // where the two disagree renders a video that stops mid-scene or holds a
  // still at the end. Neither is visible until somebody watches the whole file.
  const problem = validate(plan({ total_frames: 999 }));
  assert.ok(problem);
  assert.match(problem, /120 frames.*declares 999/);
});

test("a plan with no scenes is refused", () => {
  assert.match(validate(plan({ scenes: [] })) ?? "", /no scenes/);
});

test("an unknown scene kind is reported, not refused", () => {
  // The rule, in both directions: refuse a version you do not know, ignore a
  // field you do not know. A newer backend sending a beat this bundle cannot
  // draw still gets the rest of its video.
  const p = plan({
    scenes: [
      { kind: "cover", frames: 60, title: ["One"] },
      { kind: "hologram", frames: 60 },
    ],
  });
  assert.equal(validate(p), null);
  const { known, unknown } = partition(p);
  assert.deepEqual(unknown, ["hologram"]);
  assert.deepEqual(known, ["cover"]);
});

test("the timeline lays scenes end to end", () => {
  const entries = timeline(plan());
  assert.deepEqual(
    entries.map((e) => e.from),
    [0, 60],
  );
});

test("a scene claiming zero frames still occupies one", () => {
  // Remotion refuses a Sequence of zero length, so a plan that somehow carries
  // one must not take the whole render down with it.
  const entries = timeline(
    plan({ scenes: [{ kind: "cover", frames: 0 }, { kind: "closing", frames: 60 }] }),
  );
  assert.deepEqual(
    entries.map((e) => e.from),
    [0, 1],
  );
});
