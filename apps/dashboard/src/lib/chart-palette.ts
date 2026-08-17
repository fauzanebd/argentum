import { useEffect, useState } from "react";

/**
 * The categorical chart series, read from CSS.
 *
 * `--chart-1` … `--chart-8` are a CIE L* ladder verified by `make palette`
 * against greyscale, deuteranopia and protanopia — and, since the dark ramp
 * landed, against the surface each is drawn on. Reading them from the custom
 * properties keeps the single source of truth in `tokens.json`, where CI diffs
 * it; a hex typed into a component would be exactly the drift that check exists
 * to catch.
 *
 * **It follows the theme, and it watches the class rather than the store.** The
 * dark ramp is not the light one lightened — four series are identical in both
 * themes and four are lifted — so a chart mounted in light mode and left there
 * while somebody flips the toggle would otherwise keep drawing series 2 as a
 * navy nobody can see on a dark card.
 *
 * The subscription is a MutationObserver on `<html class>` because of effect
 * ordering: ThemeProvider is an ancestor of every chart, and a parent's effect
 * runs *after* its children's. A hook keyed on the store's value would
 * therefore read getComputedStyle in the render before the class it depends on
 * was applied, and come back with the ramp it is trying to leave. The class is
 * the thing the custom properties actually resolve against, so it is the thing
 * to watch.
 */
const SERIES_COUNT = 8;

function readPalette(): string[] {
  if (typeof window === "undefined") return [];
  const cs = getComputedStyle(document.documentElement);
  return Array.from({ length: SERIES_COUNT }, (_, i) =>
    cs.getPropertyValue(`--chart-${i + 1}`).trim(),
  ).filter(Boolean);
}

export function useChartPalette(): string[] {
  const [palette, setPalette] = useState<string[]>(readPalette);

  useEffect(() => {
    // Read once on mount as well: the first paint can happen before the theme
    // class lands, and the initial useState ran during render.
    setPalette(readPalette());
    const observer = new MutationObserver(() => setPalette(readPalette()));
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["class"],
    });
    return () => observer.disconnect();
  }, []);

  return palette;
}
