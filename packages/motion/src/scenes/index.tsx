import React from "react";
import { AbsoluteFill, Img, useVideoConfig } from "remotion";

import { enter, reveal, rise, STAGGER } from "../anim";
import { Body, FONT, Footer, Frame, Lines, TitleBand } from "../chrome";
import { useSceneFrame } from "../frame";
import { KIND } from "../plan";
import type { Brand, Metrics, Scene } from "../plan";

/**
 * One component per scene kind, and one switch that picks between them.
 *
 * Every component reads only the fields its kind carries and ignores the rest,
 * which is what makes the plan's "one struct, no union" shape safe on this side:
 * a plan from a newer backend renders minus the beats this bundle does not know,
 * rather than crashing on the first field it has never seen.
 */

/**
 * `portrait` is Report's answer to plan.isPortrait, passed down rather than
 * re-derived because a scene holds metrics and not the frame. It changes
 * *arrangement* only — a row becomes a column — never a size or a position,
 * which stay the plan's.
 */
type Props = { scene: Scene; brand: Brand; metrics: Metrics; portrait?: boolean };

export const SceneView: React.FC<Props> = (props) => {
  switch (props.scene.kind) {
    case KIND.cover:
      return <Cover {...props} />;
    case KIND.closing:
      return <Closing {...props} />;
    case KIND.section:
      return <Divider {...props} />;
    case KIND.hero:
      return <Hero {...props} />;
    case KIND.promo:
      return <Promo {...props} />;
    case KIND.statement:
      return <Statement {...props} />;
    case KIND.quote:
      return <Quote {...props} />;
    case KIND.kpi:
      return <KPIRow {...props} />;
    case KIND.chart:
      return <ChartScene {...props} />;
    case KIND.table:
      return props.scene.table ? <TableScene {...props} /> : <Facts {...props} />;
    default:
      // An unknown kind draws the ground and nothing else. See plan.partition:
      // the caller is told which kinds it dropped, so this is a visible gap in
      // a report rather than a silent one.
      return <Frame brand={props.brand} metrics={props.metrics} />;
  }
};

/** The dark scenes: cover, divider, closing. */
const Cover: React.FC<Props> = ({ scene, brand, metrics }) => {
  const frame = useSceneFrame();
  return (
    <Frame brand={brand} metrics={metrics} dark>
      <div
        style={{
          height: "100%",
          display: "flex",
          flexDirection: "column",
          justifyContent: "center",
        }}
      >
        {scene.period ? (
          <div
            style={{
              fontSize: metrics.type.caption,
              letterSpacing: 2,
              color: brand.primary_on_dark,
              opacity: enter(frame),
              marginBottom: metrics.spacing_md,
              whiteSpace: "pre",
            }}
          >
            {scene.period}
          </div>
        ) : null}
        <Lines
          lines={scene.title}
          size={metrics.type.display}
          leading={metrics.leading}
          color={brand.on_dark}
          weight={700}
          frame={frame}
          delay={STAGGER}
          stagger
        />
        <div style={{ height: metrics.spacing_md }} />
        <Lines
          lines={scene.subtitle}
          size={metrics.type.h2}
          leading={metrics.leading}
          color={brand.on_dark}
          frame={frame}
          delay={STAGGER * 3}
        />
      </div>
      <FactStrip
        facts={scene.facts}
        brand={brand}
        metrics={metrics}
        frame={frame}
        onDark
      />
    </Frame>
  );
};

const Closing: React.FC<Props> = ({ scene, brand, metrics }) => {
  const frame = useSceneFrame();
  return (
    <Frame brand={brand} metrics={metrics} dark>
      <div
        style={{
          height: "100%",
          display: "flex",
          flexDirection: "column",
          justifyContent: "center",
        }}
      >
        <div
          style={{
            width: metrics.title_rule_width * enter(frame),
            height: metrics.title_rule_thickness,
            backgroundColor: brand.primary_on_dark,
            marginBottom: metrics.spacing_md,
          }}
        />
        <Lines
          lines={scene.title}
          size={metrics.type.h1}
          leading={metrics.leading}
          color={brand.on_dark}
          weight={700}
          frame={frame}
        />
      </div>
      <FactStrip
        facts={scene.facts}
        brand={brand}
        metrics={metrics}
        frame={frame}
        onDark
      />
    </Frame>
  );
};

/**
 * starPoints is the burst behind the product: a polygon alternating between
 * two radii, as a `clip-path` percentage list.
 *
 * Geometry rather than an asset, for the same reason the sunburst is a
 * gradient: an SVG or a PNG here would be a file that has to be shipped,
 * scaled and colour-matched to a tenant's accent, and this is eleven lines
 * that do all three.
 */
function starPoints(spikes: number, outer: number, inner: number): string {
  // motion-color-ok: these are clip-path coordinates, not a figure. The guard
  // forbids `toFixed` because a component that reformats a number produces a
  // video whose figures disagree with the PDF beside it; a polygon vertex is
  // read by the compositor and never by a person, and rounding it to two
  // decimals is what keeps the path string short rather than what decides
  // what it says.
  const pts: string[] = [];
  for (let i = 0; i < spikes * 2; i++) {
    const r = i % 2 === 0 ? outer : inner;
    const a = (Math.PI * i) / spikes - Math.PI / 2;
    pts.push(`${(50 + r * Math.cos(a)).toFixed(2)}% ${(50 + r * Math.sin(a)).toFixed(2)}%`);
  }
  return `polygon(${pts.join(", ")})`;
}

/**
 * Promo is a retail promotion card (T-G12): a badge, a product photograph,
 * the product's name, the price before struck through and the price now.
 *
 * **It is the only scene here that draws its own ground**, and the reason is
 * what the card is: a sunburst is full-bleed by definition, and every other
 * scene's ground is a flat colour that the frame's own padding sits on. The
 * safe zones still bind the *content* — the padded layer below is the same
 * one `Frame` applies — so nothing readable moves under the app's chrome.
 *
 * Every colour is `brand.promo`, derived in Go from the tenant's accent. A
 * hex literal in this package is a failing build (`make motion-guards`), and
 * that guard is why a promotion for a shop with a green brand is green rather
 * than red with their logo in the corner.
 */
const Promo: React.FC<Props> = ({ scene, brand, metrics }) => {
  const frame = useSceneFrame();
  const p = enter(frame);
  const promo = brand.promo;
  // A plan from a backend that does not know promotions still draws: the
  // ground falls back to the flat brand colours, which is the same
  // "render minus the beats this bundle does not know" rule the switch above
  // keeps.
  const ground = promo?.ground ?? brand.primary;
  const ray = promo?.ray ?? brand.primary;
  const burst = promo?.burst ?? brand.primary;
  const badgeFill = promo?.badge ?? brand.dark;
  const priceFill = promo?.price_block ?? brand.dark;

  return (
    <AbsoluteFill style={{ backgroundColor: ground, fontFamily: FONT }}>
      {/* The rays. `from 0deg at 50% 42%` puts the vanishing point behind the
          product rather than at the centre of the frame, so the wedges spread
          from the thing being sold. */}
      <AbsoluteFill
        style={{
          background: `repeating-conic-gradient(from 0deg at 50% 42%, ${ground} 0deg 14deg, ${ray} 14deg 28deg)`,
          opacity: p,
        }}
      />
      <AbsoluteFill
        style={{
          paddingLeft: metrics.margin_x,
          paddingRight: metrics.margin_x,
          paddingTop: metrics.margin_top,
          paddingBottom: metrics.margin_bottom,
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
        }}
      >
        {scene.badge ? (
          <div
            style={{
              backgroundColor: badgeFill,
              color: brand.on_dark,
              fontSize: metrics.type.h2,
              fontWeight: 700,
              letterSpacing: 3,
              padding: `${metrics.spacing_sm}px ${metrics.spacing_md}px`,
              borderRadius: metrics.radius,
              transform: `rotate(-3deg) scale(${0.9 + 0.1 * p})`,
              opacity: p,
              whiteSpace: "pre",
            }}
          >
            {scene.badge}
          </div>
        ) : null}

        {/* The photograph, and the star behind it. `flex: 1` gives the product
            every pixel the badge and the price block do not want, which is
            what makes one template work on a 1:1 card and a 9:16 story. */}
        <div
          style={{
            flex: 1,
            // `min-height: 0` and not tidiness: a flex item's default
            // `min-height: auto` refuses to shrink below its content, so the
            // photograph pushed the product's name and the price panel off
            // the bottom of the card on every surface shorter than a story.
            // The first render of this component showed a promotion whose
            // price was not on it.
            minHeight: 0,
            width: "100%",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            position: "relative",
            marginTop: metrics.spacing_md,
            marginBottom: metrics.spacing_md,
          }}
        >
          <div
            style={{
              position: "absolute",
              width: "92%",
              height: "92%",
              backgroundColor: burst,
              clipPath: starPoints(16, 50, 40),
              transform: `rotate(${-8 + 8 * p}deg) scale(${0.85 + 0.15 * p})`,
              opacity: p,
            }}
          />
          {scene.image ? (
            <Img
              src={scene.image.data_uri}
              style={{
                position: "relative",
                maxWidth: "82%",
                maxHeight: "100%",
                objectFit: "contain",
                opacity: p,
                transform: `scale(${0.94 + 0.06 * p})`,
              }}
            />
          ) : null}
        </div>

        <Lines
          lines={scene.title}
          size={metrics.type.h1}
          leading={metrics.leading}
          color={brand.on_dark}
          weight={700}
          frame={frame}
          delay={STAGGER}
          align="center"
        />

        {/* The prices. The one before is struck through and small; the one now
            is the largest thing on the card, on the panel that makes it read
            as a label rather than as a caption. */}
        <div
          style={{
            display: "flex",
            alignItems: "flex-end",
            gap: metrics.spacing_md,
            marginTop: metrics.spacing_sm,
            opacity: enter(frame, STAGGER * 2),
          }}
        >
          {scene.was ? (
            <span
              style={{
                fontSize: metrics.type.h2,
                fontWeight: 700,
                color: brand.on_dark,
                textDecoration: "line-through",
                whiteSpace: "pre",
                paddingBottom: metrics.spacing_sm,
              }}
            >
              {scene.was}
            </span>
          ) : null}
          <span
            style={{
              backgroundColor: priceFill,
              color: brand.on_dark,
              fontSize: metrics.type.display,
              fontWeight: 700,
              lineHeight: 1.1,
              padding: `${metrics.spacing_sm}px ${metrics.spacing_md}px`,
              borderRadius: metrics.radius,
              whiteSpace: "pre",
            }}
          >
            {scene.price}
          </span>
          {scene.unit ? (
            <span
              style={{
                fontSize: metrics.type.caption,
                fontWeight: 700,
                color: brand.on_dark,
                whiteSpace: "pre",
                paddingBottom: metrics.spacing_sm,
              }}
            >
              {scene.unit}
            </span>
          ) : null}
        </div>

        <Lines
          lines={scene.lines}
          size={metrics.type.caption}
          leading={metrics.leading}
          color={brand.on_dark}
          frame={frame}
          delay={STAGGER * 3}
          align="center"
        />
      </AbsoluteFill>
    </AbsoluteFill>
  );
};

/**
 * Hero is one statement on the whole frame (T-G11): the kicker, the headline
 * at display size, and one supporting line.
 *
 * It is a divider that carries copy, and the differences from one are the
 * point. There is no title band, because the headline *is* the title and a
 * band would set it at h1 in a fixed-height strip. There is no footer, so a
 * promotion does not carry the timestamp a report wants. The kicker sits
 * above the rule in the brand's on-dark accent, which is the same device the
 * cover uses for its period label — a hero is a cover for a post.
 *
 * Every size, colour and gap is the plan's, like every other component here.
 */
const Hero: React.FC<Props> = ({ scene, brand, metrics }) => {
  const frame = useSceneFrame();
  const p = enter(frame);
  return (
    <Frame brand={brand} metrics={metrics} dark>
      <div
        style={{
          height: "100%",
          display: "flex",
          flexDirection: "column",
          justifyContent: "center",
        }}
      >
        {scene.subtitle && scene.subtitle.length > 0 ? (
          <div
            style={{
              fontSize: metrics.type.caption,
              letterSpacing: 2,
              color: brand.primary_on_dark,
              opacity: p,
              marginBottom: metrics.spacing_sm,
              whiteSpace: "pre",
            }}
          >
            {scene.subtitle.join(" ")}
          </div>
        ) : null}
        <div
          style={{
            width: metrics.title_rule_width * p,
            height: metrics.title_rule_thickness,
            backgroundColor: brand.primary_on_dark,
            marginBottom: metrics.spacing_md,
          }}
        />
        <Lines
          lines={scene.title}
          size={metrics.type.display}
          leading={metrics.leading}
          color={brand.on_dark}
          weight={700}
          frame={frame}
          delay={STAGGER}
          stagger
        />
        {scene.lines && scene.lines.length > 0 ? (
          <>
            <div style={{ height: metrics.spacing_md }} />
            <Lines
              lines={scene.lines}
              size={metrics.type.h2}
              leading={metrics.leading}
              color={brand.on_dark}
              frame={frame}
              delay={STAGGER * 3}
            />
          </>
        ) : null}
      </div>
    </Frame>
  );
};

const Divider: React.FC<Props> = ({ scene, brand, metrics }) => {
  const frame = useSceneFrame();
  const p = enter(frame);
  return (
    <Frame brand={brand} metrics={metrics} dark>
      <div
        style={{
          height: "100%",
          display: "flex",
          flexDirection: "column",
          justifyContent: "center",
        }}
      >
        <div
          style={{
            width: metrics.title_rule_width * p,
            height: metrics.title_rule_thickness,
            backgroundColor: brand.primary_on_dark,
            marginBottom: metrics.spacing_md,
          }}
        />
        <Lines
          lines={scene.title}
          size={metrics.type.display}
          leading={metrics.leading}
          color={brand.on_dark}
          weight={700}
          frame={frame}
        />
      </div>
    </Frame>
  );
};

/** The light scenes. */
const Statement: React.FC<Props> = ({ scene, brand, metrics }) => {
  const frame = useSceneFrame();
  return (
    <Frame brand={brand} metrics={metrics}>
      <TitleBand
        title={scene.title}
        brand={brand}
        metrics={metrics}
        frame={frame}
        continued={scene.continued}
      />
      <Body metrics={metrics}>
        <Lines
          lines={scene.lines}
          size={metrics.type.h2}
          leading={metrics.leading}
          color={brand.foreground}
          frame={frame}
          delay={STAGGER}
          stagger
        />
      </Body>
      <Footer brand={brand} metrics={metrics} />
    </Frame>
  );
};

const Quote: React.FC<Props> = ({ scene, brand, metrics }) => {
  const frame = useSceneFrame();
  const p = enter(frame);
  const tone = brand.tones?.[scene.tone ?? "info"] ?? brand.tones?.info;
  return (
    <Frame brand={brand} metrics={metrics}>
      <TitleBand
        title={scene.title}
        brand={brand}
        metrics={metrics}
        frame={frame}
      />
      <Body metrics={metrics}>
        <div
          style={{
            backgroundColor: tone?.fill,
            borderLeft: `${metrics.title_rule_thickness}px solid ${tone?.accent}`,
            borderRadius: metrics.radius,
            padding: metrics.spacing_lg,
            opacity: p,
            transform: `translateY(${rise(p)}px)`,
          }}
        >
          <Lines
            lines={scene.subtitle}
            size={metrics.type.h1}
            leading={metrics.leading}
            color={tone?.accent ?? brand.foreground}
            weight={700}
            frame={frame}
            delay={STAGGER}
          />
          <div style={{ height: metrics.spacing_sm }} />
          <Lines
            lines={scene.lines}
            size={metrics.type.h2}
            leading={metrics.leading}
            color={brand.foreground}
            frame={frame}
            delay={STAGGER * 2}
            stagger
          />
        </div>
      </Body>
      <Footer brand={brand} metrics={metrics} />
    </Frame>
  );
};

/**
 * KPIRow is a row of cards on the wide surface and a column of them on a
 * portrait one (T-G4).
 *
 * The column is not the row rotated. A wide card stacks label, value and delta
 * because it is narrow and tall; four of those stacked on the 4:5 surface run
 * to ~1250 px against an 874 px body. So a portrait card is one line shorter:
 * the delta sits beside the value rather than under it, and the four cards the
 * surface allows (`canvas.Portrait.MaxKPICards`) come to ~850 px at the
 * display size, inside the body with the spacing the plan measured. The value
 * keeps the display size on both — a big number is the point of the card.
 */
const KPIRow: React.FC<Props> = ({ scene, brand, metrics, portrait }) => {
  const frame = useSceneFrame();
  const cards = scene.kpis ?? [];
  const gap = portrait ? metrics.spacing_sm : metrics.spacing_md;
  return (
    <Frame brand={brand} metrics={metrics}>
      <TitleBand
        title={scene.title}
        brand={brand}
        metrics={metrics}
        frame={frame}
      />
      <Body metrics={metrics}>
        <div
          style={{
            display: "flex",
            flexDirection: portrait ? "column" : "row",
            gap,
          }}
        >
          {cards.map((card, i) => {
            const p = enter(frame, i * STAGGER);
            const delta = card.delta ? (
              <div
                style={{
                  fontSize: metrics.type.caption,
                  fontWeight: 700,
                  marginTop: portrait ? 0 : metrics.spacing_sm,
                  color: card.good ? brand.positive : brand.destructive,
                  whiteSpace: "pre",
                }}
              >
                {/* ↑ and ↓ rather than ▲ and ▼: Space Grotesk has the
                    arrows and not the triangles, and a missing glyph
                    renders as nothing at all. The deck learned this. */}
                {card.rising ? "↑" : "↓"} {card.delta}
              </div>
            ) : null;
            return (
              <div
                key={card.label}
                style={{
                  flex: portrait ? "none" : 1,
                  backgroundColor: brand.surface,
                  border: `1px solid ${brand.border}`,
                  borderRadius: metrics.radius,
                  padding: portrait ? metrics.spacing_sm : metrics.spacing_md,
                  opacity: p,
                  transform: `translateY(${rise(p)}px)`,
                }}
              >
                <div
                  style={{
                    fontSize: metrics.type.caption,
                    color: brand.muted,
                    whiteSpace: "pre",
                  }}
                >
                  {card.label}
                </div>
                <div
                  style={{
                    display: "flex",
                    alignItems: "baseline",
                    gap: metrics.spacing_md,
                    marginTop: metrics.spacing_sm,
                  }}
                >
                  <div
                    style={{
                      fontSize: metrics.type.display,
                      fontWeight: 700,
                      color: brand.foreground,
                      whiteSpace: "pre",
                    }}
                  >
                    {card.value}
                  </div>
                  {portrait ? delta : null}
                </div>
                {portrait ? null : delta}
              </div>
            );
          })}
        </div>
      </Body>
      <Footer brand={brand} metrics={metrics} />
    </Frame>
  );
};

/**
 * The chart scene. The image is the one internal/report/chart drew for the PDF
 * and the deck; the animation is a mask over it and never a redraw (locked
 * decision 6).
 */
const ChartScene: React.FC<Props> = ({ scene, brand, metrics }) => {
  const frame = useSceneFrame();
  const { fps } = useVideoConfig();
  const chart = scene.chart;
  const p = chart?.reveal === "none" ? 1 : reveal(frame, fps);

  // motion-color-ok: a mask reads only the alpha channel, so `#000` here means
  // "opaque" and never appears on screen. The pixels underneath are the Go
  // renderer's chart, drawn on the verified palette and never redrawn.
  const mask =
    chart?.reveal === "sweep"
      ? `conic-gradient(#000 ${p * 360}deg, transparent 0deg)`
      : chart?.reveal === "grow"
        ? `linear-gradient(to top, #000 ${p * 100}%, transparent 0%)`
        : `linear-gradient(to right, #000 ${p * 100}%, transparent 0%)`;

  return (
    <Frame brand={brand} metrics={metrics}>
      <TitleBand
        title={scene.title}
        brand={brand}
        metrics={metrics}
        frame={frame}
      />
      <Body metrics={metrics}>
        {chart ? (
          <div
            style={{
              width: chart.width,
              height: chart.height,
              WebkitMaskImage: mask,
              maskImage: mask,
              WebkitMaskSize: "100% 100%",
              maskSize: "100% 100%",
            }}
          >
            <Img
              src={chart.data_uri}
              style={{ width: chart.width, height: chart.height }}
            />
          </div>
        ) : null}
        <div style={{ height: metrics.spacing_sm }} />
        <Lines
          lines={scene.caption}
          size={metrics.type.caption}
          leading={metrics.leading}
          color={brand.muted}
          frame={frame}
          delay={Math.round(fps * 1.2)}
        />
      </Body>
      <Footer brand={brand} metrics={metrics} />
    </Frame>
  );
};

const TableScene: React.FC<Props> = ({ scene, brand, metrics }) => {
  const frame = useSceneFrame();
  const table = scene.table!;
  const align = (a: string) =>
    a === "r" ? "right" : a === "ctr" ? "center" : "left";

  const cell = (
    value: string,
    i: number,
    weight: number,
    color: string,
  ): React.ReactNode => (
    <div
      key={i}
      style={{
        width: table.widths[i],
        textAlign: align(table.aligns[i] ?? "l"),
        fontSize: table.font_size,
        fontWeight: weight,
        color,
        whiteSpace: "pre",
        overflow: "hidden",
        paddingLeft: 6,
        paddingRight: 6,
        boxSizing: "border-box",
      }}
    >
      {value}
    </div>
  );

  return (
    <Frame brand={brand} metrics={metrics}>
      <TitleBand
        title={scene.title}
        brand={brand}
        metrics={metrics}
        frame={frame}
        continued={scene.continued}
      />
      <Body metrics={metrics} center={false}>
        <div style={{ opacity: enter(frame) }}>
          <div
            style={{
              display: "flex",
              height: table.header_height,
              alignItems: "center",
              backgroundColor: brand.surface_subtle,
            }}
          >
            {table.header.map((h, i) => cell(h, i, 700, brand.foreground))}
          </div>
          {table.rows?.map((row, r) => (
            <div
              key={r}
              style={{
                display: "flex",
                height: table.row_height,
                alignItems: "center",
                borderBottom: `1px solid ${brand.border}`,
                opacity: enter(frame, 2 + r),
              }}
            >
              {row.map((v, i) => cell(v, i, 400, brand.foreground))}
            </div>
          ))}
          {table.total && table.total.length > 0 ? (
            <div
              style={{
                display: "flex",
                height: table.row_height,
                alignItems: "center",
                borderTop: `${metrics.title_rule_thickness / 3}px solid ${brand.foreground}`,
              }}
            >
              {table.total.map((v, i) => cell(v, i, 700, brand.foreground))}
            </div>
          ) : null}
        </div>
        <div style={{ height: metrics.spacing_sm }} />
        <Lines
          lines={scene.caption}
          size={metrics.type.caption}
          leading={metrics.leading}
          color={brand.muted}
          frame={frame}
        />
      </Body>
      <Footer brand={brand} metrics={metrics} />
    </Frame>
  );
};

/** A key_value block: the invoice header, the parameters, the fact list. */
const Facts: React.FC<Props> = ({ scene, brand, metrics }) => {
  const frame = useSceneFrame();
  return (
    <Frame brand={brand} metrics={metrics}>
      <TitleBand
        title={scene.title}
        brand={brand}
        metrics={metrics}
        frame={frame}
        continued={scene.continued}
      />
      <Body metrics={metrics} center={false}>
        {(scene.facts ?? []).map((fact, i) => (
          <div
            key={fact.label}
            style={{
              display: "flex",
              gap: metrics.spacing_md,
              marginBottom: metrics.spacing_sm,
              opacity: enter(frame, i * 2),
            }}
          >
            <div
              style={{
                width: Math.round(metrics.content_width * 0.34),
                fontSize: metrics.type.body,
                color: brand.muted,
                whiteSpace: "pre",
              }}
            >
              {fact.label}
            </div>
            <div style={{ flex: 1 }}>
              <Lines
                lines={fact.value}
                size={metrics.type.body}
                leading={metrics.leading}
                color={brand.foreground}
                weight={700}
              />
            </div>
          </div>
        ))}
      </Body>
      <Footer brand={brand} metrics={metrics} />
    </Frame>
  );
};

/**
 * The strip of prepared-for / prepared-by / generated facts on a dark scene.
 * It wraps: three facts across 1648 px is one line, and across 921 px it is
 * two, which is the one place the portrait surface changes a dark scene.
 */
const FactStrip: React.FC<{
  facts: Scene["facts"];
  brand: Brand;
  metrics: Metrics;
  frame: number;
  onDark?: boolean;
}> = ({ facts, brand, metrics, frame, onDark }) => {
  if (!facts || facts.length === 0) return null;
  return (
    <div
      style={{
        position: "absolute",
        left: metrics.margin_x,
        right: metrics.margin_x,
        bottom: metrics.margin_bottom,
        display: "flex",
        flexWrap: "wrap",
        gap: metrics.spacing_lg,
        opacity: enter(frame, STAGGER * 4),
      }}
    >
      {facts.map((fact) => (
        <div key={fact.label}>
          <div
            style={{
              fontSize: metrics.type.caption,
              color: onDark ? brand.primary_on_dark : brand.muted,
              letterSpacing: 1,
              whiteSpace: "pre",
            }}
          >
            {fact.label}
          </div>
          <Lines
            lines={fact.value}
            size={metrics.type.caption}
            leading={metrics.leading}
            color={onDark ? brand.on_dark : brand.foreground}
            weight={700}
          />
        </div>
      ))}
    </div>
  );
};
