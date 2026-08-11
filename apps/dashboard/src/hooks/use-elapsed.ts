import { useEffect, useRef, useState } from "react";

/**
 * Seconds since `startedAt`, ticking (T-U3).
 *
 * Lives here rather than beside `<Elapsed>` in components/ui/shimmer.tsx for
 * the reason the lint rule gives: a file that exports both components and a
 * hook breaks fast refresh for everything in it. `src/hooks/` is where the two
 * existing hooks of this shape already are.
 *
 * Ticks every 100ms while under ten seconds and every second after. Past ten
 * seconds the tenths digit is not displayed, so nine of every ten renders would
 * change nothing on screen.
 */
export function useElapsedSeconds(startedAt: number): number {
  const [now, setNow] = useState(() => Date.now());

  // Read through a ref so the effect depends on `startedAt` alone. With `now`
  // in the dependency list the timeout would be torn down and rebuilt on every
  // tick, which is how a 100ms timer becomes a 100ms render loop.
  const elapsedRef = useRef(0);
  elapsedRef.current = (now - startedAt) / 1000;

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout>;
    const tick = () => {
      setNow(Date.now());
      timer = setTimeout(tick, elapsedRef.current < 10 ? 100 : 1000);
    };
    timer = setTimeout(tick, 100);
    return () => clearTimeout(timer);
  }, [startedAt]);

  // Clamped: `startedAt` is a client clock reading, and a system clock that
  // steps backwards mid-turn would otherwise render a negative age.
  return Math.max(0, (now - startedAt) / 1000);
}
