import React, { useEffect } from "react";
import {
  AbsoluteFill,
  continueRender,
  delayRender,
  getRemotionEnvironment,
  Sequence,
  staticFile,
} from "remotion";

import { STILL_FRAME } from "./anim";
import { FONT } from "./chrome";
import { StillFrame } from "./frame";
import { isPortrait, timeline, validate } from "./plan";
import type { Plan } from "./plan";
import { SceneView } from "./scenes/index";
import { TOKEN_COLOR } from "./tokens.generated";

/**
 * Report is the whole video: the scenes, in order, each for the number of
 * frames the plan gave it.
 *
 * There is no timing logic here. `videoplan` already decided how long each
 * scene is on screen, and a renderer that adjusted those would be the second
 * place a duration is decided — which is the failure this whole package's shape
 * exists to prevent.
 *
 * A still plan (`plan.still`, T-G4) is the same scenes with the time axis
 * removed: each is one frame long, and each is drawn at STILL_FRAME so the
 * frame is the *end* of its entrance rather than the start. The scene
 * components do not know: they ask `useSceneFrame()` where they used to ask
 * `useCurrentFrame()`, and the StillFrame context answers with the fixed one —
 * see frame.ts for why that is not Remotion's `<Freeze>`. The same component
 * draws the video and the slide.
 */
export const Report: React.FC<{ plan: Plan }> = ({ plan }) => {
  useFonts();

  const problem = validate(plan);
  if (problem) {
    // A plan that fails validation should never reach here — apps/render
    // checks it before it starts a browser — so this is the belt to that
    // brace, and it draws the reason rather than a blank frame. A blank frame
    // in a delivered video is the worst shape this failure can take.
    //
    // The one frame in this package that cannot read `plan.brand`, because the
    // plan is the broken thing. It reads the generated tokens instead of
    // naming colours, so it is still the product's dark ground rather than
    // somebody's idea of near-black.
    return (
      <AbsoluteFill
        style={{
          backgroundColor: TOKEN_COLOR.foreground,
          color: TOKEN_COLOR.background,
          fontFamily: FONT,
          fontSize: 32,
          padding: 96,
          justifyContent: "center",
        }}
      >
        {problem}
      </AbsoluteFill>
    );
  }

  const portrait = isPortrait(plan);
  return (
    <AbsoluteFill style={{ backgroundColor: plan.brand.background }}>
      {timeline(plan).map(({ scene, from }, i) => {
        const view = (
          <SceneView
            scene={scene}
            brand={plan.brand}
            metrics={plan.metrics}
            portrait={portrait}
          />
        );
        return (
          <Sequence
            key={`${i}-${scene.kind}`}
            from={from}
            durationInFrames={Math.max(1, scene.frames)}
            name={`${i + 1} ${scene.kind}`}
          >
            {plan.still ? (
              <StillFrame.Provider value={STILL_FRAME}>{view}</StillFrame.Provider>
            ) : (
              view
            )}
          </Sequence>
        );
      })}
      {plan.still && getRemotionEnvironment().isStudio ? (
        <SafeZones plan={plan} />
      ) : null}
    </AbsoluteFill>
  );
};

/**
 * SafeZones is the studio-only overlay of the bands Instagram's own UI covers
 * on a 4:5 post — roughly the top 120 px (the account name) and the bottom 150
 * px (caption and actions). It draws in Remotion Studio and nowhere else: a
 * render never sees it, because `getRemotionEnvironment().isStudio` is false
 * under `@remotion/renderer`. The bands are expressed as a fraction of the
 * frame rather than as 120 and 150 so a square or a story surface (T-G9) gets a
 * proportionate ring rather than a wrong one.
 */
const SafeZones: React.FC<{ plan: Plan }> = ({ plan }) => {
  const top = Math.round(plan.height * (120 / 1350));
  const bottom = Math.round(plan.height * (150 / 1350));
  // motion-color-ok: a translucent hatch for the studio's eyes only, never in
  // a delivered frame.
  const band = "repeating-linear-gradient(135deg, rgba(255,0,0,0.18) 0 8px, transparent 8px 16px)";
  return (
    <AbsoluteFill style={{ pointerEvents: "none" }}>
      <div style={{ position: "absolute", top: 0, left: 0, right: 0, height: top, background: band }} />
      <div style={{ position: "absolute", bottom: 0, left: 0, right: 0, height: bottom, background: band }} />
    </AbsoluteFill>
  );
};

/**
 * useFonts blocks frame 0 until Space Grotesk is loaded.
 *
 * Without the block, the first second of every video is set in a fallback face
 * and every line in it is the wrong width — the exact failure T-R1 vendored the
 * TTFs to prevent in the PDF, arrived at through a race instead of through a
 * missing file. `delayRender` is Remotion's mechanism for exactly this, and the
 * timeout is generous because a cold container is loading three files off disk.
 */
function useFonts(): void {
  const [handle] = React.useState(() =>
    delayRender("Loading Space Grotesk", { timeoutInMilliseconds: 30_000 }),
  );

  useEffect(() => {
    const faces = [
      new FontFace(FONT, `url(${staticFile("fonts/SpaceGrotesk-Regular.ttf")})`, {
        weight: "400",
      }),
      new FontFace(FONT, `url(${staticFile("fonts/SpaceGrotesk-Medium.ttf")})`, {
        weight: "500",
      }),
      new FontFace(FONT, `url(${staticFile("fonts/SpaceGrotesk-Bold.ttf")})`, {
        weight: "700",
      }),
    ];

    Promise.all(
      faces.map((f) => f.load().then((loaded) => document.fonts.add(loaded))),
    )
      .then(() => document.fonts.ready)
      .then(() => continueRender(handle))
      .catch((err: unknown) => {
        // Continue rather than hang. A video in a substituted face is wrong;
        // a render that never finishes is worse, and the wall clock in
        // apps/render would kill it with nothing to show for the minutes.
        // eslint-disable-next-line no-console
        console.error("[motion] font load failed, rendering substituted:", err);
        continueRender(handle);
      });
  }, [handle]);
}
