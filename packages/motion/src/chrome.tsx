import React from "react";
import { AbsoluteFill, Img } from "remotion";

import { enter, rise, STAGGER } from "./anim";
import type { Brand, Metrics } from "./plan";

/**
 * The furniture every scene sits in: the ground, the title band, the footer
 * strip, and the one component that draws text.
 *
 * Nothing here chooses a colour, a size or a position. Every value comes off
 * `plan.metrics` and `plan.brand`, which were measured in Go against this exact
 * surface. A literal in this file is a defect, not a shortcut.
 */

export const FONT = "Space Grotesk";

/**
 * Lines draws pre-wrapped text.
 *
 * One element per line, `whiteSpace: pre`, no `width` on the text itself. That
 * is the whole point of the plan carrying lines rather than paragraphs: the
 * browser is never asked where to break, so it cannot break somewhere the Go
 * measurement did not. Handing it a paragraph and a width would put a second
 * layout engine in the loop and lose the guarantee.
 */
export const Lines: React.FC<{
  lines: string[] | undefined;
  size: number;
  leading: number;
  color: string;
  weight?: number;
  frame?: number;
  delay?: number;
  stagger?: boolean;
  align?: "left" | "center";
}> = ({
  lines,
  size,
  leading,
  color,
  weight = 400,
  frame,
  delay = 0,
  stagger = false,
  align = "left",
}) => {
  if (!lines || lines.length === 0) return null;
  return (
    <>
      {lines.map((line, i) => {
        const at = delay + (stagger ? i * STAGGER : 0);
        const p = frame === undefined ? 1 : enter(frame, at);
        return (
          <div
            key={`${i}-${line}`}
            style={{
              fontFamily: FONT,
              fontSize: size,
              fontWeight: weight,
              lineHeight: `${Math.round(size * leading)}px`,
              color,
              whiteSpace: "pre",
              textAlign: align,
              opacity: p,
              transform: `translateY(${rise(p, 16)}px)`,
            }}
          >
            {line}
          </div>
        );
      })}
    </>
  );
};

/**
 * Frame is the ground and the safe area. `dark` flips the cover, the dividers
 * and the closing scene onto the near-black ground, which is why the plan
 * carries `primary_on_dark` as well as `primary`.
 */
export const Frame: React.FC<{
  brand: Brand;
  metrics: Metrics;
  dark?: boolean;
  children?: React.ReactNode;
}> = ({ brand, metrics, dark = false, children }) => (
  <AbsoluteFill
    style={{
      backgroundColor: dark ? brand.dark : brand.background,
      fontFamily: FONT,
      fontKerning: "normal",
    }}
  >
    <AbsoluteFill
      style={{
        paddingLeft: metrics.margin_x,
        paddingRight: metrics.margin_x,
        paddingTop: metrics.margin_top,
        paddingBottom: metrics.margin_bottom,
      }}
    >
      {children}
    </AbsoluteFill>
  </AbsoluteFill>
);

/**
 * TitleBand is the heading and the rule under it, in the band the plan
 * measured. Its height is fixed so the body area starts at the same y on every
 * scene — a title that pushed the content down would make a run of scenes jitter
 * as the headings changed length.
 */
export const TitleBand: React.FC<{
  title: string[] | undefined;
  brand: Brand;
  metrics: Metrics;
  frame: number;
  continued?: boolean;
}> = ({ title, brand, metrics, frame, continued }) => {
  const p = enter(frame);
  return (
    <div style={{ height: metrics.title_band, position: "relative" }}>
      <Lines
        lines={title}
        size={metrics.type.h1}
        leading={metrics.leading}
        color={brand.foreground}
        weight={700}
        frame={frame}
      />
      {/* Top-right, not inline after the title.
          Inline, it is a span following a stack of block-level lines, so it
          wraps onto its own line and the brand rule is drawn straight through
          it — which is what the first render of the 200-row export looked like,
          and it is the same defect T-R4's gate found on the deck's cover. The
          band has a fixed height, so the one place the marker cannot collide
          with anything is the corner opposite the title's first line. */}
      {continued ? (
        <span
          style={{
            position: "absolute",
            top: 0,
            right: 0,
            fontFamily: FONT,
            fontSize: metrics.type.caption,
            lineHeight: `${Math.round(metrics.type.h1 * metrics.leading)}px`,
            color: brand.muted,
          }}
        >
          (cont.)
        </span>
      ) : null}
      <div
        style={{
          position: "absolute",
          bottom: 0,
          left: 0,
          width: metrics.title_rule_width * p,
          height: metrics.title_rule_thickness,
          backgroundColor: brand.primary,
        }}
      />
    </div>
  );
};

/**
 * Footer is the confidentiality label, the legal line, the credit and the mark.
 *
 * It carries no page number: a video has no pages, and the band stays anyway so
 * the body area is the same height as the deck's. That is not symmetry for its
 * own sake — it is what keeps a table that fits on a slide fitting in a frame.
 */
export const Footer: React.FC<{ brand: Brand; metrics: Metrics }> = ({
  brand,
  metrics,
}) => {
  const left = [brand.confidentiality, brand.footer_note]
    .filter(Boolean)
    .join("  ·  ");
  const logoHeight = Math.round(metrics.type.caption * 1.4);
  return (
    <div
      style={{
        position: "absolute",
        left: metrics.margin_x,
        right: metrics.margin_x,
        top: metrics.footer_top,
        height: metrics.footer_band,
        display: "flex",
        alignItems: "center",
        justifyContent: "space-between",
        fontFamily: FONT,
        fontSize: metrics.type.caption,
        color: brand.muted,
      }}
    >
      <span style={{ whiteSpace: "pre" }}>{left}</span>
      <span style={{ display: "flex", alignItems: "center", gap: 12 }}>
        {brand.credit ? <span>{brand.credit}</span> : null}
        {brand.logo_data_uri ? (
          <Img
            src={brand.logo_data_uri}
            style={{
              height: logoHeight,
              width: logoHeight * (brand.logo_aspect ?? 1),
              objectFit: "contain",
            }}
          />
        ) : null}
      </span>
    </div>
  );
};

/**
 * Body is the content area under the title band, at the height the plan
 * measured. Scenes centre their content in it rather than stacking from the
 * top: a KPI row and a two-line statement both read better on the optical
 * centre, and the band above them is what keeps the eye still.
 */
export const Body: React.FC<{
  metrics: Metrics;
  children: React.ReactNode;
  center?: boolean;
}> = ({ metrics, children, center = true }) => (
  <div
    style={{
      height: metrics.body_height,
      marginTop: metrics.spacing_md,
      display: "flex",
      flexDirection: "column",
      justifyContent: center ? "center" : "flex-start",
    }}
  >
    {children}
  </div>
);
