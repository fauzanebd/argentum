import React from "react";
import { Composition } from "remotion";

import { Report } from "./Report";
import type { Plan } from "./plan";
import { DEFAULT_BRAND_COLORS } from "./tokens.generated";

/**
 * The composition: one entry, whose size and length come from the plan it is
 * given.
 *
 * `calculateMetadata` is why there is only one composition rather than one per
 * report. The plan states its own width, height, fps and total frames — all
 * measured in Go — so the composition reads them off the props instead of
 * declaring numbers that would then be a second opinion.
 */

export const COMPOSITION_ID = "report";

/**
 * DEFAULT_PLAN is what Remotion Studio opens with when no props are passed. It
 * is deliberately a plan that says so, rather than a plausible-looking sample:
 * a studio that opens on fake figures is how a fake figure ends up in a
 * screenshot.
 */
const DEFAULT_PLAN: Plan = {
  version: 1,
  width: 1920,
  height: 1080,
  fps: 30,
  total_frames: 90,
  locale: "en",
  title: "No plan supplied",
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
    ...DEFAULT_BRAND_COLORS,
    tones: {},
  },
  scenes: [
    {
      kind: "section",
      frames: 90,
      title: ["Pass a plan with --props"],
    },
  ],
};

export const RemotionRoot: React.FC = () => (
  <Composition
    id={COMPOSITION_ID}
    component={Report}
    defaultProps={{ plan: DEFAULT_PLAN }}
    width={DEFAULT_PLAN.width}
    height={DEFAULT_PLAN.height}
    fps={DEFAULT_PLAN.fps}
    durationInFrames={DEFAULT_PLAN.total_frames}
    calculateMetadata={({ props }) => ({
      width: props.plan.width,
      height: props.plan.height,
      fps: props.plan.fps,
      durationInFrames: Math.max(1, props.plan.total_frames),
    })}
  />
);
