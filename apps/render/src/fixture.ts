import { readFile, writeFile } from "node:fs/promises";
import { basename, join, resolve } from "node:path";

import { renderStill, selectComposition } from "@remotion/renderer";
import { COMPOSITION_ID, timeline } from "@argentum/motion";
import type { Plan } from "@argentum/motion";

import { render, serveUrl } from "./render";

/**
 * The fixture CLI: a plan in, an MP4 and a still per scene out.
 *
 * This is what makes the renderer reviewable without the backend, and it is
 * also the fastest loop for any future scene work — a still is a second where a
 * render is a minute. The stills are the golden images T-V5's perceptual gate
 * compares against, so this is not a debugging convenience that happens to be
 * committed; it is half of the gate.
 *
 *   pnpm --filter @argentum/render render:fixture <plan.json> [outDir]
 */

async function main(): Promise<void> {
  const args = process.argv.slice(2).filter((a) => a !== "--stills");
  const stillsOnly = process.argv.includes("--stills");
  const [planPath, outDir = "out"] = args;
  if (!planPath) {
    console.error("usage: render:fixture <plan.json> [outDir] [--stills]");
    process.exit(2);
  }

  const plan = JSON.parse(await readFile(planPath, "utf8")) as Plan;
  const name = basename(planPath).replace(/\.plan\.json$|\.json$/, "");
  const dir = resolve(outDir);

  console.log(
    `${name}: ${plan.scenes.length} scenes, ${plan.total_frames} frames, ` +
      `${(plan.total_frames / plan.fps).toFixed(1)}s`,
  );

  // Stills first. They are seconds, they prove the compositions draw, and a
  // failure in one of them is a failure worth seeing before waiting out an
  // encode.
  const url = await serveUrl();
  const composition = await selectComposition({
    serveUrl: url,
    id: COMPOSITION_ID,
    inputProps: { plan },
  });

  const scenes = timeline(plan);
  for (const [i, { scene, from }] of scenes.entries()) {
    // Mid-scene rather than on its first frame: the first frame of a scene is
    // its entrance at zero opacity, which makes a contact sheet of blank
    // rectangles and a gate that passes on all of them.
    const frame = from + Math.floor(scene.frames / 2);
    const out = join(dir, `${name}-${String(i).padStart(2, "0")}-${scene.kind}.png`);
    await renderStill({
      composition,
      serveUrl: url,
      output: out,
      frame,
      inputProps: { plan },
    });
    console.log(`  still ${out}`);
  }

  if (stillsOnly) {
    // The stills are the reviewable half and the gate's golden images; the
    // encode is minutes. Being able to stop here is what makes scene work a
    // seconds-long loop rather than a coffee break.
    console.log("  stills only, skipping the encode");
    return;
  }

  const result = await render({
    plan,
    onProgress: (p) => {
      if (Math.round(p * 100) % 10 === 0) {
        process.stdout.write(`\r  rendering ${Math.round(p * 100)}%   `);
      }
    },
  });

  const mp4 = join(dir, `${name}.mp4`);
  await writeFile(mp4, await readFile(result.outputPath));
  await result.cleanup();
  console.log(`\n  video ${mp4} (${result.seconds.toFixed(1)}s to render)`);
}

main().catch((err: unknown) => {
  console.error(err);
  process.exit(1);
});
