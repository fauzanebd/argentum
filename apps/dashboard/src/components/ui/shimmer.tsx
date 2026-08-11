import { cn } from "@/lib/utils";
import { useShimmer } from "@/lib/motion";
import { useElapsedSeconds } from "@/hooks/use-elapsed";

/**
 * The loading vocabulary (T-U3).
 *
 * Two pieces that are always used together: a surface that looks like it is
 * working, and a number that proves it still is. The number is the important
 * half. A spinner says "something is happening" for exactly as long as a hung
 * request does, and Argentum's turns legitimately run for tens of seconds while
 * the agent takes another tool-calling round — so the honest signal is elapsed
 * time, not motion.
 */

/**
 * A surface that reads as loading.
 *
 * `bg-[length:200%_100%]` is at the call site rather than in the Tailwind config
 * because `backgroundImage` and `backgroundSize` share the `bg-*` namespace
 * there and would collide on the name `shimmer`.
 */
export function Shimmer({ className }: { className?: string }) {
  const animate = useShimmer();
  return (
    <span
      aria-hidden
      className={cn(
        "block rounded-sm bg-muted bg-shimmer bg-[length:200%_100%]",
        animate,
        className,
      )}
    />
  );
}

/**
 * Seconds since `startedAt`, ticking.
 *
 * Tenths below ten seconds and whole seconds above: a turn that takes 4.2s and
 * one that takes 4.9s feel different and the tenth is the only thing that says
 * so, while "127.4s" is false precision on a number nobody is timing that
 * closely.
 *
 * `startedAt` is a client clock reading. The backend sends no start timestamp
 * with the `started` event, and adding one would be a wire change for a figure
 * whose only consumer is this caption — the moment the browser learned the turn
 * had begun is the honest thing to count from anyway, because it is also the
 * moment the reader started waiting.
 */
export function Elapsed({
  startedAt,
  className,
}: {
  startedAt: number;
  className?: string;
}) {
  const seconds = useElapsedSeconds(startedAt);
  const text = seconds < 10 ? `${seconds.toFixed(1)}s` : `${Math.round(seconds)}s`;
  return (
    <span
      className={cn("tabular-nums text-muted-subtle", className)}
      // The elapsed figure updates ten times a second. Announcing each one would
      // make a screen reader unusable for the whole turn, so the live region is
      // off and the surrounding status line carries the announcement instead.
      aria-hidden
    >
      {text}
    </span>
  );
}

