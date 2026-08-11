import { useMemo } from "react";
import {
  Bar,
  BarChart,
  Cell,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { cn } from "@/lib/utils";

/**
 * Charts, bound to the token palette (T-U11).
 *
 * ## The palette is not a choice this file gets to make
 *
 * `tokens.json` carries an eight-series categorical ramp whose every entry sits
 * on a rung of a CIE L* ladder, verified by `make palette` against greyscale,
 * deuteranopia and protanopia with hard floors. It existed for the PDF renderer
 * alone until `T-U11`, because the dashboard had no charts — so the first chart
 * drawn here would have picked its colours by eye and disagreed with the
 * document rendering the same figures.
 *
 * `--chart-1` … `--chart-8` are that ladder, in source order. Reading them from
 * CSS rather than importing hexes keeps the single source of truth in
 * `tokens.json` where CI diffs it.
 */

const SERIES_COUNT = 8;

/** The palette, read once from the custom properties the token pipeline emits. */
function useChartPalette(): string[] {
  return useMemo(() => {
    if (typeof window === "undefined") return [];
    const cs = getComputedStyle(document.documentElement);
    return Array.from({ length: SERIES_COUNT }, (_, i) =>
      cs.getPropertyValue(`--chart-${i + 1}`).trim(),
    ).filter(Boolean);
  }, []);
}

export type BreakdownDatum = {
  /** The category, as the axis label. */
  label: string;
  value: number;
  /** Rendered under the bar's tooltip. Optional detail, never the figure. */
  hint?: string;
};

/**
 * A horizontal breakdown of one categorical measure.
 *
 * Horizontal rather than vertical because every category here is a name — a
 * model id, an event type — and names read badly rotated or truncated under a
 * vertical axis.
 *
 * **This is not a trend.** `UsageSummary` carries totals and per-category
 * aggregates and no time series at all, so there is nothing to plot a
 * sparkline from; that needs a bucketed usage endpoint and is its own ticket.
 * Drawing a shape that looks like time out of data that has none would be the
 * worse option.
 */
export function BreakdownChart({
  data,
  /** Formats the value for the axis and the tooltip. */
  format,
  className,
}: {
  data: BreakdownDatum[];
  format: (value: number) => string;
  className?: string;
}) {
  const palette = useChartPalette();
  if (data.length === 0) return null;

  // 28px a row plus the axis. A fixed height would crush eight models or leave
  // a lake of white under two.
  const height = data.length * 28 + 24;

  return (
    <div className={cn("w-full", className)} style={{ height }}>
      <ResponsiveContainer width="100%" height="100%">
        <BarChart
          data={data}
          layout="vertical"
          margin={{ top: 0, right: 8, bottom: 0, left: 0 }}
        >
          <XAxis type="number" hide />
          <YAxis
            type="category"
            dataKey="label"
            width={130}
            tickLine={false}
            axisLine={false}
            tick={{
              fill: "hsl(var(--muted-foreground))",
              fontSize: 11,
            }}
          />
          <Tooltip
            cursor={{ fill: "hsl(var(--muted))" }}
            contentStyle={{
              background: "hsl(var(--popover))",
              border: "1px solid hsl(var(--border-strong))",
              borderRadius: "var(--radius)",
              fontSize: 12,
              color: "hsl(var(--popover-foreground))",
            }}
            formatter={(value: number, _name, entry) => [
              format(value),
              (entry?.payload as BreakdownDatum | undefined)?.hint ?? "",
            ]}
          />
          <Bar dataKey="value" radius={[0, 4, 4, 0]} barSize={14}>
            {data.map((d, i) => (
              // Series colour by index, wrapping past eight. The ladder is
              // ordered so the first few are the most separable, which is what
              // a two- or three-category chart gets.
              <Cell key={d.label} fill={palette[i % palette.length]} />
            ))}
          </Bar>
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}
