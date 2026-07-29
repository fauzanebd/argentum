import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import type { CompanyCredits, UsageSummary } from "@argentum/api-types";
import { EVENT_LABELS, microToUsd } from "./labels";

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
        <CardContent className="divide-y divide-border/50">
          {Object.entries(summary.event_counts).length === 0 && (
            <div className="text-sm text-muted-foreground py-4">No usage recorded yet.</div>
          )}
          {Object.entries(summary.event_counts).map(([type, count]) => {
            const cost = summary.cost_by_event_type_usd[type] ?? 0;
            return (
              <div key={type} className="flex items-center justify-between py-3">
                <div>
                  <div className="text-sm font-medium">{EVENT_LABELS[type] ?? type}</div>
                  <div className="text-xs text-muted-foreground">{count.toLocaleString()} events</div>
                </div>
                <Badge variant="secondary">${cost.toFixed(4)}</Badge>
              </div>
            );
          })}
        </CardContent>
      </Card>

      {models.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle>By model</CardTitle>
            <CardDescription>Cost and tokens per model.</CardDescription>
          </CardHeader>
          <CardContent className="divide-y divide-border/50">
            {models.map(([model, cost]) => {
              const tin = summary.tokens_in_by_model?.[model] ?? 0;
              const tout = summary.tokens_out_by_model?.[model] ?? 0;
              return (
                <div key={model} className="flex items-center justify-between py-3">
                  <div>
                    <div className="text-sm font-medium font-mono">{model}</div>
                    <div className="text-xs text-muted-foreground">
                      {tin.toLocaleString()} in · {tout.toLocaleString()} out
                    </div>
                  </div>
                  <Badge variant="secondary">${cost.toFixed(4)}</Badge>
                </div>
              );
            })}
          </CardContent>
        </Card>
      )}
    </div>
  );
}
