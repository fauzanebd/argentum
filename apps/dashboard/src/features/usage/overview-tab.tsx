import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { lazy, Suspense } from "react";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { CompanyCredits, UsageSummary } from "@argentum/api-types";
import { EVENT_LABELS, microToUsd } from "./labels";

/**
 * recharts is ~390 kB and this tab is the only screen that draws a chart.
 * Imported eagerly it rode in the main chunk, which every route pays for —
 * including the chat page, where nobody is looking at a bar chart. Same
 * treatment as the syntax highlighter in T-U6.
 */
const BreakdownChart = lazy(() =>
  import("@/components/ui/chart").then((m) => ({ default: m.BreakdownChart })),
);

export function OverviewTab() {
  const { data: summary, isLoading } = useQuery({
    queryKey: ["usage-summary"],
    queryFn: async () => (await api.get<UsageSummary>("/usage/summary")).data,
  });

  const { data: credits } = useQuery({
    queryKey: ["usage-credits"],
    queryFn: async () => (await api.get<CompanyCredits>("/usage/credits")).data,
  });

  if (isLoading) return <div className="text-sm text-muted-foreground">Loading…</div>;
  if (!summary) return null;

  const balanceUsd = credits ? microToUsd(credits.balance_micro_usd) : null;
  const grantUsd = credits ? microToUsd(credits.monthly_grant_micro_usd) : null;
  const pct =
    balanceUsd != null && grantUsd != null && grantUsd > 0
      ? Math.max(0, Math.min(100, (balanceUsd / grantUsd) * 100))
      : null;

  const models = Object.entries(summary.cost_by_model_usd ?? {}).sort(
    (a, b) => b[1] - a[1],
  );

  return (
    <div className="space-y-6">
      <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
        <Card>
          <CardHeader className="pb-2">
            <CardDescription>Total cost</CardDescription>
            <CardTitle className="text-2xl">${summary.total_cost_usd.toFixed(2)}</CardTitle>
          </CardHeader>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardDescription>Tokens in</CardDescription>
            <CardTitle className="text-2xl">{summary.total_tokens_in.toLocaleString()}</CardTitle>
          </CardHeader>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardDescription>Tokens out</CardDescription>
            <CardTitle className="text-2xl">{summary.total_tokens_out.toLocaleString()}</CardTitle>
          </CardHeader>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardDescription>Credit balance</CardDescription>
            <CardTitle className="text-2xl">
              {balanceUsd != null ? `$${balanceUsd.toFixed(2)}` : "—"}
            </CardTitle>
          </CardHeader>
          {pct != null && (
            <CardContent className="pt-0">
              <div className="h-1.5 w-full rounded-full bg-muted overflow-hidden">
                <div
                  className="h-full bg-primary"
                  style={{ width: `${pct}%` }}
                />
              </div>
              <p className="text-xs text-muted-foreground mt-1.5">
                of ${grantUsd?.toFixed(2)} monthly grant
              </p>
            </CardContent>
          )}
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Activity breakdown</CardTitle>
          <CardDescription>
            {new Date(summary.from).toLocaleDateString()} –{" "}
            {new Date(summary.to).toLocaleDateString()}
          </CardDescription>
        </CardHeader>
        <CardContent>
          {Object.entries(summary.event_counts).length === 0 ? (
            <div className="text-sm text-muted-foreground py-4">No usage recorded yet.</div>
          ) : (
            <Suspense
              fallback={
                <Skeleton
                  style={{ height: Object.keys(summary.event_counts).length * 28 + 24 }}
                />
              }
            >
              <BreakdownChart
                data={Object.entries(summary.event_counts).map(([type, count]) => ({
                  label: EVENT_LABELS[type] ?? type,
                  value: summary.cost_by_event_type_usd[type] ?? 0,
                  hint: `${count.toLocaleString()} events`,
                }))}
                format={(v) => `$${v.toFixed(4)}`}
              />
            </Suspense>
          )}
        </CardContent>
      </Card>

      {models.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle>By model</CardTitle>
            <CardDescription>Cost and tokens per model.</CardDescription>
          </CardHeader>
          <CardContent className="px-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Model</TableHead>
                  <TableHead className="text-right">Tokens in</TableHead>
                  <TableHead className="text-right">Tokens out</TableHead>
                  <TableHead className="text-right">Cost</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {models.map(([model, cost]) => {
                  const tin = summary.tokens_in_by_model?.[model] ?? 0;
                  const tout = summary.tokens_out_by_model?.[model] ?? 0;
                  return (
                    <TableRow key={model}>
                      <TableCell className="font-mono">{model}</TableCell>
                      {/* tabular-nums so the digits line up column-wise; without
                          it a proportional font makes 1,000 and 9,999 different
                          widths and the column stops being scannable. */}
                      <TableCell className="text-right tabular-nums text-muted-foreground">
                        {tin.toLocaleString()}
                      </TableCell>
                      <TableCell className="text-right tabular-nums text-muted-foreground">
                        {tout.toLocaleString()}
                      </TableCell>
                      <TableCell className="text-right tabular-nums font-medium">
                        ${cost.toFixed(4)}
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      )}
    </div>
  );
}
