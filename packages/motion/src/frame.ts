import { createContext, useContext } from "react";
import { useCurrentFrame } from "remotion";

/**
 * Where a scene's frame comes from (T-G4).
 *
 * A video scene reads the timeline: `useCurrentFrame()`, relative to its
 * Sequence, as every component always has. A still scene is one frame long and
 * that frame is its entrance at zero opacity, so a still has to be drawn at a
 * frame the timeline never reaches — STILL_FRAME, past the last staggered
 * entrance in the package.
 *
 * **Remotion's `<Freeze>` cannot do this, and the reason is worth recording.**
 * `useCurrentFrame()` clamps the timeline position to the *composition's*
 * `durationInFrames - 1` before subtracting the Sequence offset
 * (`clampFrameToCompositionRange` in timeline-position-state). A seven-slide
 * plan is seven frames long, so `<Freeze frame={60}>` inside slide i yields
 * `min(6, 60 + i) - i` = `6 - i`: the cover at frame 6, the closing at frame
 * 0, and every entrance with a stagger of more than a few frames invisible.
 * The first portrait render of the carousel fixture looked exactly like that —
 * one KPI card of three, a table with a header and no rows — and freezing at
 * 6, 60 and 1000 produced identical pixels, which is the clamp's signature.
 *
 * So the override is ours. It is a context rather than a prop threaded through
 * eight components because the frame is read in the leaves (`Lines`, the KPI
 * card, each table row), and both hooks are called unconditionally so the
 * rules of hooks hold on either path.
 */
export const StillFrame = createContext<number | null>(null);

/** The frame a scene draws at: the override when one is set, else the timeline. */
export function useSceneFrame(): number {
  const still = useContext(StillFrame);
  const current = useCurrentFrame();
  return still ?? current;
}
