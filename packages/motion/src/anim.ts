import { Easing, interpolate } from "remotion";

/**
 * One curve and two durations, for every component.
 *
 * T-V5's note says it out loud and it belongs here where it is enforced: a
 * component that invents a third timing is a review finding. Three components
 * with three timing feels is the "enterprise-grade has no exit condition"
 * failure in miniature, and it is the kind of thing nobody notices until the
 * whole thing feels cheap and no single frame is wrong.
 *
 * **Motion is pacing, not decoration.** Anything that moves for longer than its
 * scene's text takes to read is delaying the reader. When in doubt, cut the
 * animation and keep the duration — the duration is what the timing model in
 * internal/report/videoplan computed, and it is the part that matters.
 */

/** The curve. Standard ease-out: fast to start, settled before it lands. */
export const CURVE = Easing.bezier(0.16, 1, 0.3, 1);

/** Frames an entrance takes. 12 at 30fps is 0.4s. */
export const ENTER = 12;

/** Frames an exit takes. Shorter than an entrance: leaving is not an event. */
export const EXIT = 8;

/** Frames each item of a staggered group waits behind the one before it. */
export const STAGGER = 4;

/**
 * STILL_FRAME is the frame a still plan is drawn at (T-G4).
 *
 * A still plan's scenes are one frame long, and frame 0 of every scene is its
 * entrance at zero opacity — the contact-sheet-of-blank-rectangles failure
 * apps/render's fixture CLI already works around by picking a mid-scene frame.
 * A carousel has no mid-scene, so Report freezes each scene at this frame
 * instead: past the longest staggered entrance in the package, which is a
 * twelve-row table (`enter(frame, 2 + r)`) or a five-line statement, plus the
 * entrance itself. Nothing in this package animates past frame 60, and the
 * helpers in this file clamp, so a larger number would draw the same pixels.
 */
export const STILL_FRAME = ENTER + STAGGER * 12;

/**
 * enter is the 0→1 progress of an entrance that began `delay` frames into the
 * scene.
 */
export function enter(frame: number, delay = 0): number {
  return interpolate(frame, [delay, delay + ENTER], [0, 1], {
    easing: CURVE,
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
  });
}

/**
 * exit is the 1→0 progress of the scene leaving, given the scene's length.
 *
 * It returns 1 for every frame that is not in the last EXIT frames, so a caller
 * can multiply the two without special-casing the middle of the scene.
 */
export function exit(frame: number, durationInFrames: number): number {
  return interpolate(
    frame,
    [durationInFrames - EXIT, durationInFrames],
    [1, 0],
    { easing: CURVE, extrapolateLeft: "clamp", extrapolateRight: "clamp" },
  );
}

/** rise is the vertical offset that goes with an entrance, in pixels. */
export function rise(progress: number, distance = 24): number {
  return (1 - progress) * distance;
}

/**
 * reveal is the mask progress for a chart, over its own window.
 *
 * The window is fixed at 1.5s — videoplan's ChartRevealSeconds — because the
 * plan already added that time to the scene, and a reveal that scaled with the
 * scene's length would crawl on a long caption.
 */
export function reveal(frame: number, fps: number): number {
  return interpolate(frame, [0, Math.round(fps * 1.5)], [0, 1], {
    easing: CURVE,
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
  });
}
