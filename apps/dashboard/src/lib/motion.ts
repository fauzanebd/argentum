/**
 * One curve and three durations, for every component (T-U2).
 *
 * The values here are not new. `packages/motion/src/anim.ts` fixed them for the
 * video renderer under `T-V5`, and this file states the same curve and the same
 * three durations in the unit a browser counts in — seconds rather than frames
 * at 30fps. A customer who watches a generated video and then opens the
 * dashboard is looking at one product, and two timing systems is exactly how
 * that stops being true.
 *
 * `T-V5`'s note applies here word for word: **motion is pacing, not
 * decoration.** Anything that moves for longer than its text takes to read is
 * delaying the reader. A component that invents a fourth duration is a review
 * finding.
 *
 * ## Reduced motion
 *
 * Every export below is a hook, not an object, and that is deliberate. A
 * component that imports a plain `variants` constant has to remember to check
 * `useReducedMotion()` and swap it, and the one that forgets is the one nobody
 * tests. Importing `useEnter()` makes the check unskippable — there is no
 * un-gated variant to reach for.
 *
 * Two further layers sit behind it, because a preference this cheap to honour
 * should not depend on one file being used correctly:
 *
 *   * `index.css` carries a global `prefers-reduced-motion: reduce` block that
 *     collapses every CSS animation and transition, including ones written in
 *     Tailwind utilities that never touch this file.
 *   * Wrapping the app in framer's `<MotionConfig reducedMotion="user">` turns
 *     off transform and layout animation library-wide. That is a change to
 *     `main.tsx` and belongs to the first ticket that actually animates
 *     something — `T-U3`.
 *
 * What "reduced" means here: spatial movement is removed, opacity is kept. A
 * fade carries no vestibular risk and is what makes an arriving element legible
 * as *arriving*; a slide is the part that causes harm. This is the common
 * reading of the media query, not a literal "no animation at all".
 */

import { useReducedMotion, type Transition, type Variants } from "framer-motion";

/**
 * The curve. Standard ease-out: fast to start, settled before it lands.
 *
 * Identical to `CURVE` in `packages/motion/src/anim.ts`, where it is expressed
 * as `Easing.bezier(0.16, 1, 0.3, 1)` for Remotion.
 */
export const CURVE: [number, number, number, number] = [0.16, 1, 0.3, 1];

/**
 * The three durations, in seconds.
 *
 * The video counts these in frames at 30fps — 12, 8 and 4. An exit is shorter
 * than an entrance because leaving is not an event.
 */
export const DURATION = {
  /** An element arriving. 12 frames. */
  enter: 0.4,
  /** An element leaving. 8 frames. */
  exit: 0.27,
  /** What each item of a staggered group waits behind the one before it. 4 frames. */
  stagger: 0.13,
} as const;

/**
 * The one spring, for the one thing a curve cannot do: a control that should
 * feel physical under the pointer — a chip appearing, a button settling.
 *
 * Tuned to settle inside `DURATION.enter` so it does not become a fourth
 * duration by the back door.
 */
export const SPRING: Transition = {
  type: "spring",
  stiffness: 400,
  damping: 32,
  mass: 1,
};

const ENTER_TRANSITION: Transition = { duration: DURATION.enter, ease: CURVE };
const EXIT_TRANSITION: Transition = { duration: DURATION.exit, ease: CURVE };

/** How far an entrance travels, in pixels. `rise()` in the video's anim.ts. */
const RISE = 8;

/**
 * still strips the spatial half of a variant set, keeping the opacity half.
 *
 * It rewrites rather than replaces so a component gets the same variant *names*
 * in both modes — `initial="hidden"` keeps working, it simply has nothing left
 * to move.
 */
function still(variants: Variants): Variants {
  const out: Variants = {};
  for (const [name, variant] of Object.entries(variants)) {
    if (typeof variant !== "object" || variant === null) {
      out[name] = variant;
      continue;
    }
    const { x, y, scale, rotate, transition, ...rest } =
      variant as Record<string, unknown>;
    // x/y/scale/rotate are destructured out rather than read: naming them is
    // what removes them, and referencing them again would only re-add them.
    void x;
    void y;
    void scale;
    void rotate;
    void transition;
    out[name] = { ...rest, transition: { duration: DURATION.exit, ease: CURVE } };
  }
  return out;
}

/** useVariants gates any variant set on the user's motion preference. */
export function useVariants(variants: Variants): Variants {
  const reduced = useReducedMotion();
  return reduced ? still(variants) : variants;
}

/**
 * An element arriving: fades up and settles.
 *
 * The workhorse. A card, a status row, an approval decision landing in place.
 */
export function useEnter(): Variants {
  return useVariants({
    hidden: { opacity: 0, y: RISE },
    visible: { opacity: 1, y: 0, transition: ENTER_TRANSITION },
    exit: { opacity: 0, y: RISE, transition: EXIT_TRANSITION },
  });
}

/**
 * A group whose children arrive one behind the next.
 *
 * Pair `container` on the wrapper with `item` on each child. Under reduced
 * motion the stagger delay goes with the movement — a sequence of fades is
 * still a sequence, and waiting through one is the same delay for a reader who
 * asked not to be moved.
 */
export function useStagger(): { container: Variants; item: Variants } {
  const reduced = useReducedMotion();
  const item = useEnter();
  return {
    container: {
      hidden: {},
      visible: {
        transition: {
          staggerChildren: reduced ? 0 : DURATION.stagger,
          delayChildren: 0,
        },
      },
    },
    item,
  };
}

/**
 * A single streamed token appearing.
 *
 * Deliberately the shortest thing here. A token fade that runs for
 * `DURATION.enter` would still be fading when the next three have arrived, so
 * this one is a third of it and does not move.
 */
export function useTokenFade(): Variants {
  return useVariants({
    hidden: { opacity: 0 },
    visible: { opacity: 1, transition: { duration: 0.13, ease: CURVE } },
  });
}

/**
 * The class that sweeps a shimmer across a loading surface, or "" when the user
 * asked for less motion.
 *
 * A sweep is a continuous background-position animation with no state, which is
 * a CSS keyframe's job and not React's — `animate-shimmer` and its gradient
 * live in `tailwind.config.ts`. This hook exists so the *decision* to run it
 * stays in the same file as every other motion decision, rather than becoming a
 * class name a component applies unconditionally.
 */
export function useShimmer(): string {
  const reduced = useReducedMotion();
  return reduced ? "" : "animate-shimmer";
}
