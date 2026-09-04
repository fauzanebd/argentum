import React from "react";
import { Img, useVideoConfig } from "remotion";

import { enter, reveal, rise, STAGGER } from "../anim";
import { Body, Footer, Frame, Lines, TitleBand } from "../chrome";
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
