import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";

interface UsageSummary {
  from: string;
  to: string;
  total_cost_usd: number;
  total_tokens_in: number;
  total_tokens_out: number;
  event_counts: Record<string, number>;
  cost_by_event_type_usd: Record<string, number>;
}

const EVENT_LABELS: Record<string, string> = {
  llm_call: "LLM calls",
  sql_query: "SQL queries",
  metabase_card: "Charts",
  metabase_dashboard: "Dashboards",
  topic_classify: "Topic classification",
};

export function UsagePage() {
  const { data, isLoading } = useQuery({
    queryKey: ["usage-summary"],
    queryFn: async () => (await api.get<UsageSummary>("/usage/summary")).data,
  });

  return (
    <div className="h-full overflow-y-auto">
      <div className="max-w-4xl mx-auto px-6 py-8">
        <h1 className="text-2xl font-bold mb-1">Usage</h1>
        <p className="text-sm text-muted-foreground mb-6">
          Cost and activity over the current period.
        </p>

        {isLoading && <div className="text-sm text-muted-foreground">Loading…</div>}
        {data && (
          <>
            <div className="grid grid-cols-3 gap-4 mb-6">
              <Card>
                <CardHeader className="pb-2">
                  <CardDescription>Total cost</CardDescription>
                  <CardTitle className="text-2xl">${data.total_cost_usd.toFixed(2)}</CardTitle>
                </CardHeader>
              </Card>
              <Card>
                <CardHeader className="pb-2">
                  <CardDescription>Tokens in</CardDescription>
                  <CardTitle className="text-2xl">{data.total_tokens_in.toLocaleString()}</CardTitle>
                </CardHeader>
              </Card>
              <Card>
                <CardHeader className="pb-2">
                  <CardDescription>Tokens out</CardDescription>
                  <CardTitle className="text-2xl">{data.total_tokens_out.toLocaleString()}</CardTitle>
                </CardHeader>
              </Card>
            </div>

            <Card>
              <CardHeader>
                <CardTitle>Activity breakdown</CardTitle>
                <CardDescription>
                  {new Date(data.from).toLocaleDateString()} – {new Date(data.to).toLocaleDateString()}
                </CardDescription>
              </CardHeader>
              <CardContent className="divide-y divide-border/50">
                {Object.entries(data.event_counts).length === 0 && (
                  <div className="text-sm text-muted-foreground py-4">No usage recorded yet.</div>
                )}
                {Object.entries(data.event_counts).map(([type, count]) => {
                  const cost = data.cost_by_event_type_usd[type] ?? 0;
                  return (
                    <div key={type} className="flex items-center justify-between py-3">
                      <div>
                        <div className="text-sm font-medium">{EVENT_LABELS[type] ?? type}</div>
                        <div className="text-xs text-muted-foreground">{count} events</div>
                      </div>
                      <Badge variant="secondary">${cost.toFixed(4)}</Badge>
                    </div>
                  );
                })}
              </CardContent>
            </Card>
          </>
        )}
      </div>
    </div>
  );
}
