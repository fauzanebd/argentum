import type { Plan } from "@argentum/motion";

/**
 * The two things this service can make from a plan (T-G5): one MP4, or one
 * JPEG per scene. Both come off the same route, the same job store and the
 * same browser; what differs is the loop inside the render and the shape of
 * the result — one file, or N pages fetched one at a time.
 */
export const OUTPUTS = ["video", "stills"] as const;
export type Output = (typeof OUTPUTS)[number];

/** The page image: Instagram takes JPEG and nothing else, so nothing else is offered. */
export const PAGE_FORMAT = "jpeg" as const;
export const PAGE_QUALITY = 90;

/**
 * parseOutput reads the request's `output`, defaulting to video so every
 * caller written before stills existed keeps getting what it asked for.
 */
export function parseOutput(value: unknown): Output | string {
  if (value === undefined || value === null) return "video";
  if (typeof value === "string" && (OUTPUTS as readonly string[]).includes(value)) {
    return value as Output;
  }
  return `unknown output ${JSON.stringify(value)}; use one of ${OUTPUTS.join(", ")}`;
}

/**
 * checkOutput refuses a plan built for the other output.
 *
 * A plan is measured for one of them: a still plan has one frame a scene and
 * its entrances frozen (`still: true`), a video plan has durations and
 * animates. Rendering a still plan as a video is a seven-frame clip at 1 fps;
 * rendering a video plan as stills is N blank first frames. Both are wrong
 * artifacts that take a minute to notice, and a sentence now is cheaper.
 */
export function checkOutput(plan: Plan, output: Output): string | null {
  if (output === "stills" && !plan.still) {
    return "a stills render needs a still plan (`still: true`, one frame a scene); this plan is a video";
  }
  if (output === "video" && plan.still) {
    return "a video render needs a video plan; this plan is a still (`still: true`) — request `output: \"stills\"`";
  }
  return null;
}

/** pageName is the file a page is written to: 01.jpg … NN.jpg, 1-based. */
export function pageName(page: number): string {
  return `${String(page).padStart(2, "0")}.jpg`;
}

/**
 * parseJobPath reads the three job routes off a path.
 *
 * Kept out of server.ts so it can be tested without starting a listener —
 * that module listens on import, which is right for a service and wrong for a
 * test.
 */
export function parseJobPath(
  path: string,
): { id: string; result: boolean; page?: number } | null {
  const m = path.match(/^\/v1\/jobs\/([0-9a-f-]{36})(?:(\/result)(?:\/(\d{1,3}))?)?$/);
  if (!m) return null;
  const [, id, result, page] = m;
  return {
    id,
    result: Boolean(result),
    ...(page !== undefined ? { page: Number(page) } : {}),
  };
}
