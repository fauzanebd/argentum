import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Layers } from "lucide-react";
import type { MetricCoverageResponse } from "@argentum/api-types";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";

/** The window the panel opens on. Thirty days, matching the server's default —
 *  a registry accumulates over months, and a week of a quiet tenant is a
 *  handful of turns whose percentage swings on one question. */
const WINDOW_DAYS = 30;

/** Below this, the percentage is not a measurement. Eight turns at 50% and
 *  eighty turns at 50% are different claims, and a screen that renders them the
 *  same way invites an admin to act on the first one. */
const MIN_TURNS_TO_JUDGE = 20;

export function useMetricCoverage() {
  return useQuery({
    queryKey: ["quality", "metric-coverage", WINDOW_DAYS],
    queryFn: async () => {
      const res = await api.get<MetricCoverageResponse>("/quality/metric-coverage", {
        params: { days: WINDOW_DAYS },
      });
      return res.data;
    },
  });
}

/**
 * Is the metric layer actually accumulating (T-F4)?
 *
 * `03-gap-analysis.md` argued before the registry existed that the metric layer
 * is the moat — a competitor can clone a chat UI in a week and cannot clone a
 * customer's curated definitions. `T-06`/`T-07` built it and the agent prefers
 * it. Nothing has ever measured whether it grows, so nobody could say whether a
 * given tenant's answers are certified or re-derived.
 *
 * **The list is the actionable half, not the percentage.** A number tells an
 * admin they have a problem; the questions underneath tell them what to define
 * this afternoon.
 */
export function MetricCoveragePanel() {
  const { data, isLoading, isError } = useMetricCoverage();

  // Nothing at all while it loads or if it fails. This panel is context on a
  // page that works without it — a skeleton or an error box here would give a
  // secondary measurement more of the screen than the verdicts below it.
  if (isLoading || isError || !data) return null;

  return <MetricCoverageView data={data} />;
}

/**
 * The panel with its data already in hand.
 *
 * Split from the container purely so it is testable without a QueryClient: the
 * interesting logic here is the three *empty* states and the too-few-turns
 * guard, and a component that had to be tested through a mocked fetch would
 * have those exercised by nothing.
 */
export function MetricCoverageView({ data }: { data: MetricCoverageResponse }) {
  const judgeable = data.answered >= MIN_TURNS_TO_JUDGE;
  const pct = Math.round(data.percent);

  return (
    <section className="mb-8 rounded-lg border border-border">
      <div className="flex items-start gap-3 border-b border-border px-4 py-3">
        <Layers className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
        <div className="min-w-0 flex-1">
          <h2 className="text-sm font-medium">Answers from defined metrics</h2>
          <p className="mt-0.5 text-xs text-muted-foreground">
            Over the last {data.window_days} days. A defined metric returns the same number
            every time it is asked; SQL written for the occasion does not.
          </p>
        </div>
        <div className="text-right">
          <div
            className={cn(
              "text-2xl font-semibold tabular-nums",
              judgeable && pct < 50 && "text-destructive",
            )}
          >
            {data.answered === 0 ? "—" : `${pct}%`}
          </div>
          <div className="text-[11px] text-muted-foreground">
            of {data.answered} answering {data.answered === 1 ? "turn" : "turns"}
          </div>
        </div>
      </div>

      <div className="grid grid-cols-3 divide-x divide-border border-b border-border text-center">
        <Cell label="From a metric" value={data.certified} />
        <Cell label="Both" value={data.mixed} />
        <Cell label="Ad-hoc SQL" value={data.ad_hoc} />
      </div>

      {data.answered === 0 ? (
        <p className="px-4 py-3 text-xs text-muted-foreground">
          Nobody has asked this workspace a data question in the last {data.window_days} days.
        </p>
      ) : !judgeable ? (
        // Said rather than hidden. A percentage off nine turns is a number, and
        // an admin who acts on it has been misled by the screen, not by the
        // data.
        <p className="px-4 py-3 text-xs text-muted-foreground">
          Too few answers to read much into yet — this becomes meaningful past about{" "}
          {MIN_TURNS_TO_JUDGE} questions.
        </p>
      ) : data.ad_hoc_top.length === 0 ? (
        <p className="px-4 py-3 text-xs text-muted-foreground">
          Every answering turn reached a defined metric.
        </p>
      ) : (
        <div className="px-4 py-3">
          <div className="mb-2 text-xs font-medium text-muted-foreground">
            Asked most often without a metric
          </div>
          <ul className="space-y-1.5">
            {data.ad_hoc_top.map((q) => (
              <li key={q.question} className="flex items-baseline gap-3 text-xs">
                <span className="w-8 shrink-0 tabular-nums text-muted-foreground">
                  {q.turns}×
                </span>
                <span className="min-w-0 flex-1 truncate">{q.question}</span>
              </li>
            ))}
          </ul>
          <p className="mt-3 text-xs text-muted-foreground">
            Each of these re-derives its SQL every time it is asked, so two people can get two
            defensible and different numbers.{" "}
            <Link to="/settings" search={{ tab: "metrics" }} className="underline">
              Define a metric
            </Link>{" "}
            and every future answer to that question agrees with itself.
          </p>
        </div>
      )}
    </section>
  );
}

function Cell({ label, value }: { label: string; value: number }) {
  return (
    <div className="px-3 py-2.5">
      <div className="text-lg font-semibold tabular-nums">{value}</div>
      <div className="text-[11px] text-muted-foreground">{label}</div>
    </div>
  );
}
