// Build identity, frozen at build time by `define` in vite.config.ts.
//
// APP_VERSION is `git describe --tags --always --dirty` — the newest backend
// release tag plus the distance to this commit (`v0.24.0-5-gf55ac04`), or a
// bare SHA where the checkout has no tags. It identifies the commit a deploy
// was built from; it is not a dashboard release number, because the dashboard
// has none.
export const APP_VERSION = __APP_VERSION__;

/** ISO-8601 instant the bundle was built. */
export const BUILD_DATE = __BUILD_DATE__;

/** The calendar date of the build, e.g. `2026-07-31`. */
export const BUILD_DAY = __BUILD_DATE__.slice(0, 10);
