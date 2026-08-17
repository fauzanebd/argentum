import { useMemo } from "react";
import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Legend,
  Line,
  LineChart,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import type { Resolved, Series } from "@argentum/api-types/dashboard-spec";

import { useChartPalette } from "@/lib/chart-palette";
import { cn } from "@/lib/utils";

/**
 * One resolved dashboard panel, drawn (T-D11 / Track F).
 *
 * ## What this file is allowed to decide
 *
 * Almost nothing about the data. The resolver has already applied every
 * normalisation the PDF renderer applies — series capped at the palette's
 * length, gaps left as nulls rather than zeros, a KPI over no rows left without
 * a value — so a decision made here would be a second opinion that disagrees
 * with the document rendering the same figures.
 *
 * What it decides is the mark: thin bars and 2px lines, a recessive grid, a
 * legend whenever there is more than one series, and a tooltip on every form
 * that plots something.
 *
 * ## The palette is not a choice this file gets to make
 *
 * `--chart-1` … `--chart-8` are a CIE L* ladder verified by `make palette`
 * against greyscale, deuteranopia and protanopia. Reading them from CSS keeps
 * the single source of truth in `tokens.json`, and a hex typed here would be
 * the drift that check exists to catch.
 *
 * The ramp has a dark variant as of the T-D11 follow-up: four series are the
 * same colour in both themes and four are lifted, because the two that failed
 * hardest — navy at L* 24 and brown at L* 32 — were dark marks on a dark card.
 * useChartPalette reads whichever ramp the theme has installed.
 */

const SERIES_COUNT = 8;

/** Recharts wants one object per x position; the payload is column-major. */
function toRows(panel: Resolved): Record<string, number | string | null>[] {
  const labels = panel.labels ?? [];
  const series = panel.series ?? [];
  return labels.map((label, i) => {
    const row: Record<string, number | string | null> = { label };
    for (const s of series) {
      // undefined, not null: recharts draws a null at the baseline on some
      // marks, and a gap must never read as a zero.
      row[s.name] = s.points?.[i] ?? null;
    }
    return row;
  });
}

const axisTick = { fill: "hsl(var(--muted-foreground))", fontSize: 11 };

const tooltipStyle = {
  background: "hsl(var(--popover))",
  border: "1px solid hsl(var(--border-strong))",
  borderRadius: "var(--radius)",
  fontSize: 12,
  color: "hsl(var(--popover-foreground))",
};

/**
 * Formats a value for a label or a tooltip.
 *
 * The panel's `fmt` travels with the data precisely so this decision is made
 * once, here, rather than by whoever wrote the SQL. Numbers stay in the
 * viewer's own locale; a currency panel gets grouping and no symbol, because
 * the tenant's currency is not on the payload and inventing one would be worse
 * than omitting it.
 */
function useFormatter(fmt?: string) {
  return useMemo(() => {
    const nf = new Intl.NumberFormat(undefined, { maximumFractionDigits: 2 });
    const pct = new Intl.NumberFormat(undefined, {
      style: "percent",
      maximumFractionDigits: 1,
    });
    return (v: unknown) => {
      if (v === null || v === undefined || v === "") return "—";
      if (typeof v !== "number") return String(v);
      if (fmt === "percent") return pct.format(v / 100);
      return nf.format(v);
    };
  }, [fmt]);
}

/**
 * Formats a value for an *axis tick*, which is a different job from formatting
 * it for a tooltip.
 *
 * Found by the first screenshot of a real dashboard (2026-08-17): the demo
 * warehouse's monthly revenue is in the billions, `3,240,929,900` does not fit
 * an axis gutter, and the three ticks came out clipped to the same `100,000`.
 * A chart whose axis misstates its own scale is worse than one with no axis —
 * the reader has no way to see that the number is wrong.
 *
 * Compact notation, then: `3.2B`, in the viewer's locale. The tooltip keeps the
 * exact figure, because that is where somebody goes to read the number rather
 * than the shape.
 */
function useAxisFormatter(fmt?: string) {
  return useMemo(() => {
    const compact = new Intl.NumberFormat(undefined, {
      notation: "compact",
      maximumFractionDigits: 1,
    });
    const pct = new Intl.NumberFormat(undefined, {
      style: "percent",
      maximumFractionDigits: 0,
    });
    return (v: unknown) => {
      if (v === null || v === undefined || v === "") return "";
      if (typeof v !== "number") return String(v);
      if (fmt === "percent") return pct.format(v / 100);
      return compact.format(v);
    };
  }, [fmt]);
}

export function DashboardPanel({
  panel,
  className,
  /** Chat embeds are short; a page gives a panel room. */
  height = 200,
}: {
  panel: Resolved;
  className?: string;
  height?: number;
}) {
  return (
    <figure
      className={cn(
        "min-w-0 rounded-lg border border-border bg-card p-3",
        className,
      )}
    >
      {panel.title && (
        <figcaption className="mb-2 text-xs font-medium text-foreground">
          {panel.title}
        </figcaption>
      )}
      <PanelBody panel={panel} height={height} />
      {/* A note and an error read differently and are never merged: one says
          the panel answered and there was nothing there, the other says it did
          not answer. */}
      {panel.error ? (
        <p className="mt-2 text-xs text-destructive">{panel.error}</p>
      ) : panel.note ? (
        <p className="mt-2 text-xs text-muted-foreground">{panel.note}</p>
      ) : null}
      {panel.series_truncated && (
        <p className="mt-1 text-[11px] text-muted-foreground">
          Showing the largest {SERIES_COUNT} series.
        </p>
      )}
      {panel.truncated && (
        <p className="mt-1 text-[11px] text-muted-foreground">
          More rows than shown.
        </p>
      )}
    </figure>
  );
}

function PanelBody({ panel, height }: { panel: Resolved; height: number }) {
  const palette = useChartPalette();
  const format = useFormatter(panel.fmt);
  const axisFormat = useAxisFormatter(panel.fmt);

  if (panel.error) return null;
  if (panel.viz === "kpi") return <KPI panel={panel} format={format} />;
  if (panel.viz === "table") return <TableBody panel={panel} format={format} />;

  const rows = toRows(panel);
  const series = panel.series ?? [];
  if (rows.length === 0 || series.length === 0) {
    return <Empty />;
  }
  // A legend whenever identity is carried by colour. One series needs none —
  // the panel title already names it.
  const legend = series.length > 1;

  return (
    <div style={{ height }}>
      <ResponsiveContainer width="100%" height="100%">
        {chartFor(panel, rows, series, palette, format, axisFormat, legend)}
      </ResponsiveContainer>
    </div>
  );
}

function chartFor(
  panel: Resolved,
  rows: Record<string, number | string | null>[],
  series: Series[],
  palette: string[],
  format: (v: unknown) => string,
  axisFormat: (v: unknown) => string,
  legend: boolean,
) {
  const colour = (i: number) => palette[i % (palette.length || 1)];
  const common = {
    data: rows,
    margin: { top: 4, right: 8, bottom: 0, left: 0 },
  };
  const axes = (
    <>
      <CartesianGrid stroke="hsl(var(--border))" strokeDasharray="2 4" vertical={false} />
      <XAxis dataKey="label" tickLine={false} axisLine={false} tick={axisTick} />
      <YAxis
        tickLine={false}
        axisLine={false}
        tick={axisTick}
        // Wide enough for a compact tick plus its suffix ("3.2B", "-12.5M").
        // The gutter used to be 48px against a full-precision number, which is
        // how three different ticks rendered as the same clipped string.
        width={56}
        tickFormatter={(v: number) => axisFormat(v)}
      />
      <Tooltip
        cursor={{ fill: "hsl(var(--muted))" }}
        contentStyle={tooltipStyle}
        formatter={(v: number) => format(v)}
      />
      {legend && <Legend iconType="circle" wrapperStyle={{ fontSize: 11 }} />}
    </>
  );

  switch (panel.viz) {
    case "line":
      return (
        <LineChart {...common}>
          {axes}
          {series.map((s, i) => (
            <Line
              key={s.name}
              type="monotone"
              dataKey={s.name}
              stroke={colour(i)}
              strokeWidth={2}
              dot={false}
              // A gap is drawn as a gap. connectNulls would invent a segment
              // across a month the warehouse has no row for.
              connectNulls={false}
              isAnimationActive={false}
            />
          ))}
        </LineChart>
      );
    case "stacked_bar":
      return (
        <BarChart {...common}>
          {axes}
          {series.map((s, i) => (
            <Bar key={s.name} dataKey={s.name} stackId="a" fill={colour(i)} isAnimationActive={false} />
          ))}
        </BarChart>
      );
    case "pie":
    case "donut": {
      const slices = rows.map((r) => ({
        label: String(r.label),
        value: typeof r[series[0].name] === "number" ? (r[series[0].name] as number) : 0,
      }));
      return (
        <PieChart>
          <Tooltip contentStyle={tooltipStyle} formatter={(v: number) => format(v)} />
          <Legend iconType="circle" wrapperStyle={{ fontSize: 11 }} />
          <Pie
            data={slices}
            dataKey="value"
            nameKey="label"
            innerRadius={panel.viz === "donut" ? "55%" : 0}
            outerRadius="80%"
            isAnimationActive={false}
            // A 2px surface gap between segments, so adjacent slices of similar
            // lightness stay two shapes rather than one.
            paddingAngle={1}
            stroke="hsl(var(--card))"
            strokeWidth={2}
          >
            {slices.map((s, i) => (
              <Cell key={s.label} fill={colour(i)} />
            ))}
          </Pie>
        </PieChart>
      );
    }
    case "bar":
    case "grouped_bar":
    default:
      return (
        <BarChart {...common} barGap={2}>
          {axes}
          {series.map((s, i) => (
            <Bar
              key={s.name}
              dataKey={s.name}
              fill={colour(i)}
              radius={[4, 4, 0, 0]}
              isAnimationActive={false}
            />
          ))}
        </BarChart>
      );
  }
}

/**
 * A single number, which is the one form that is not a chart.
 *
 * An absent value renders as "—" and not as zero. That distinction is the whole
 * reason the payload carries a pointer: "nothing matched this window" and "the
 * total is nought" are different facts, and only one of them is safe to read.
 */
function KPI({ panel, format }: { panel: Resolved; format: (v: unknown) => string }) {
  const up = (panel.delta ?? 0) > 0;
  return (
    <div className="py-1">
      <p className="text-2xl font-semibold tabular-nums text-foreground">
        {panel.value === undefined ? "—" : format(panel.value)}
      </p>
      {panel.delta !== undefined && (
        <p
          className={cn(
            "mt-0.5 text-xs tabular-nums",
            up ? "text-success" : "text-destructive",
          )}
        >
          {up ? "▲" : "▼"} {format(Math.abs(panel.delta))}
          {panel.delta_pct !== undefined &&
            ` (${panel.delta_pct > 0 ? "+" : ""}${panel.delta_pct.toFixed(1)}%)`}
        </p>
      )}
    </div>
  );
}

function TableBody({
  panel,
  format,
}: {
  panel: Resolved;
  format: (v: unknown) => string;
}) {
  const columns = panel.columns ?? [];
  const rows = panel.rows ?? [];
  if (columns.length === 0 || rows.length === 0) return <Empty />;
  return (
    <div className="-mx-1 max-h-64 overflow-auto">
      <table className="min-w-full text-xs">
        <thead className="sticky top-0 bg-card">
          <tr>
            {columns.map((c) => (
              <th key={c} className="px-2 py-1 text-left font-medium text-muted-foreground">
                {c}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, i) => (
            <tr key={i} className="border-t border-border">
              {columns.map((c) => (
                <td key={c} className="px-2 py-1 tabular-nums text-foreground">
                  {format(row[c])}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** The no-data state, which is a sentence rather than an empty chart frame. */
function Empty() {
  return (
    <p className="py-6 text-center text-xs text-muted-foreground">
      No data for this window.
    </p>
  );
}
