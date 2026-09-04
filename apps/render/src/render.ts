import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

import { bundle } from "@remotion/bundler";
import { renderMedia, renderStill, selectComposition } from "@remotion/renderer";
import { COMPOSITION_ID, timeline, validate } from "@argentum/motion";
import type { Plan } from "@argentum/motion";

import { PAGE_FORMAT, PAGE_QUALITY, pageName } from "./output";

/**
 * Rendering one plan to one MP4 — or, since T-G5, to one JPEG per scene.
 *
 * Everything about this file is a decision about blast radius: what the browser
 * can reach, what happens when it hangs, and what it costs when nobody is
 * rendering.
 */

const here = dirname(fileURLToPath(import.meta.url));

/** The motion package's entry point and its public directory. */
const MOTION_ROOT = join(here, "../../../packages/motion");
const ENTRY = join(MOTION_ROOT, "src/Root.tsx");
const PUBLIC_DIR = join(MOTION_ROOT, "public");

/**
 * Encoding settings.
 *
 * H.264 High in yuv420p because that is the pair every player decodes; CRF 20
 * because the content is flat colour and type, where a lower number buys file
 * size and nothing visible; and `+faststart` so the file plays before it has
 * finished downloading — which matters more than every other setting here, on a
 * link someone opens on a phone.
 */
export const CODEC = "h264" as const;
export const CRF = 20;

/** How long one frame may take before the render is abandoned. */
export const FRAME_TIMEOUT_MS = 30_000;

/**
 * The bundle, built once and reused.
 *
 * Bundling is seconds of esbuild; doing it per request would put that on every
 * tenant's latency for a bundle that cannot have changed — the compositions are
 * baked into the image. It is a promise rather than a value so concurrent first
 * requests wait on one build instead of racing three.
 */
let bundlePromise: Promise<string> | null = null;

export function serveUrl(): Promise<string> {
  bundlePromise ??= bundle({
    entryPoint: ENTRY,
    publicDir: PUBLIC_DIR,
    // No webpack override: the compositions import nothing this bundler does
    // not already handle, and an override here is a second build configuration
    // to keep in step with the dashboard's.
  });
  return bundlePromise;
}

export type RenderResult = {
  outputPath: string;
  cleanup: () => Promise<void>;
  frames: number;
  seconds: number;
};

export type RenderOptions = {
  plan: Plan;
  onProgress?: (progress: number) => void;
  signal?: AbortSignal;
};

/**
 * render draws a plan and returns the path to the MP4.
 *
 * The caller owns the file and must call cleanup — the job store does, on
 * collection and on its TTL sweep. Writing to a temp directory rather than
 * returning a buffer is deliberate: a ten-minute 1080p video is tens of
 * megabytes, and holding several of those in a Node heap is how a renderer
 * dies of something other than rendering.
 */
export async function render(opts: RenderOptions): Promise<RenderResult> {
  const problem = validate(opts.plan);
  if (problem) {
    throw new PlanError(problem);
  }

  const dir = await mkdtemp(join(tmpdir(), "argentum-render-"));
  const outputPath = join(dir, "report.mp4");
  const cleanup = () => rm(dir, { recursive: true, force: true });

  try {
    const serveUrlValue = await serveUrl();
    const composition = await selectComposition({
      serveUrl: serveUrlValue,
      id: COMPOSITION_ID,
      inputProps: { plan: opts.plan },
    });

    const started = Date.now();
    await renderMedia({
      composition,
      serveUrl: serveUrlValue,
      codec: CODEC,
      crf: CRF,
      outputLocation: outputPath,
      inputProps: { plan: opts.plan },
      timeoutInMilliseconds: FRAME_TIMEOUT_MS,
      // One job at a time per process, and inside it as many cores as the pod
      // has minus one. Remotion's default is every core, which starves the
      // event loop that is meant to be answering the progress poll.
      concurrency: Math.max(1, (navigatorCores() ?? 2) - 1),
      onProgress: ({ progress }) => opts.onProgress?.(progress),
      cancelSignal: opts.signal ? toCancelSignal(opts.signal) : undefined,
      chromiumOptions: {
        // The sandbox stays on. If a deployment cannot support it, the answer
        // is a seccomp profile and the NetworkPolicy — not this flag flipped
        // quietly. See the ticket's Do list.
        gl: "swangle",
      },
    });

    return {
      outputPath,
      cleanup,
      frames: composition.durationInFrames,
      seconds: (Date.now() - started) / 1000,
    };
  } catch (err) {
    await cleanup();
    throw err;
  }
}

export type StillsResult = {
  /** The directory holding 01.jpg … NN.jpg. */
  dir: string;
  pages: number;
  cleanup: () => Promise<void>;
  seconds: number;
};

/**
 * renderStills draws a still plan one scene at a time and returns the
 * directory the pages are in.
 *
 * It is `render` with the encoder removed: the same bundle, the same
 * composition, the same browser flags, and `renderStill` at each scene's first
 * frame — which, for a still plan, is the scene frozen at the end of its
 * entrance (packages/motion/src/frame.ts). Pages are JPEG at quality 90 because
 * that is what the platform takes (T-G3, decision 3), and they are files in a
 * temp directory for the same reason the MP4 is: ten 1080×1350 JPEGs are a few
 * megabytes, and the caller collects them one at a time.
 *
 * **There is no zip here, on purpose** (decision 5). Node has no zip in its
 * standard library, and this service's posture is that it has almost nothing
 * in it. Go has `archive/zip` and builds the download.
 */
export async function renderStills(opts: RenderOptions): Promise<StillsResult> {
  const problem = validate(opts.plan);
  if (problem) {
    throw new PlanError(problem);
  }

  const dir = await mkdtemp(join(tmpdir(), "argentum-stills-"));
  const cleanup = () => rm(dir, { recursive: true, force: true });

  try {
    const serveUrlValue = await serveUrl();
    const composition = await selectComposition({
      serveUrl: serveUrlValue,
      id: COMPOSITION_ID,
      inputProps: { plan: opts.plan },
    });

    const started = Date.now();
    const scenes = timeline(opts.plan);
    for (const [i, { from }] of scenes.entries()) {
      // Between pages rather than inside one: Remotion's still has no cancel
      // signal, and a page is a second, so the wall clock is honoured to
      // within one page.
      if (opts.signal?.aborted) {
        throw new Error("cancelled");
      }
      await renderStill({
        composition,
        serveUrl: serveUrlValue,
        output: join(dir, pageName(i + 1)),
        frame: from,
        imageFormat: PAGE_FORMAT,
        jpegQuality: PAGE_QUALITY,
        inputProps: { plan: opts.plan },
        timeoutInMilliseconds: FRAME_TIMEOUT_MS,
        chromiumOptions: {
          // The same browser, the same sandbox. See render().
          gl: "swangle",
        },
      });
      opts.onProgress?.((i + 1) / scenes.length);
    }

    return {
      dir,
      pages: scenes.length,
      cleanup,
      seconds: (Date.now() - started) / 1000,
    };
  } catch (err) {
    await cleanup();
    throw err;
  }
}

/**
 * PlanError is a caller's mistake rather than ours, and the service answers it
 * with a 400 instead of a 500. It is a distinct type because those two are the
 * difference between an integrator fixing their spec and an integrator opening
 * a ticket.
 */
export class PlanError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "PlanError";
  }
}

function navigatorCores(): number | null {
  // os.availableParallelism is Node 18.14+; the image pins 22, and the fallback
  // keeps `tsx src/server.ts` working on an older developer machine.
  try {
    // eslint-disable-next-line @typescript-eslint/no-var-requires
    return require("node:os").availableParallelism();
  } catch {
    return null;
  }
}

/**
 * Remotion's cancel signal is its own shape. Bridging rather than adopting it
 * keeps AbortController — which the job store, the wall clock and the HTTP
 * layer all already speak — as the one cancellation vocabulary in this service.
 */
function toCancelSignal(signal: AbortSignal): Parameters<
  typeof renderMedia
>[0]["cancelSignal"] {
  let fire: (() => void) | null = null;
  signal.addEventListener("abort", () => fire?.(), { once: true });
  return (cancel: () => void) => {
    fire = cancel;
    if (signal.aborted) cancel();
  };
}
