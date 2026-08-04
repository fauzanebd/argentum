import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import type {
  Channel,
  MetricDefinition,
  MetricsResponse,
  Watcher,
  WatcherComparator,
  WatcherGrain,
  WatchersResponse,
} from "@argentum/api-types";
import { WatcherForm } from "./watcher-form";
import { WatcherRow } from "./watcher-row";
import { WatcherEventsSheet } from "./watcher-events-sheet";
import { useIsAdmin } from "@/store/auth";

export function WatchersPage() {
  const [showForm, setShowForm] = useState(false);
  const [editing, setEditing] = useState<Watcher | null>(null);
  const [eventsWatcher, setEventsWatcher] = useState<Watcher | null>(null);

  const { data, isLoading } = useQuery({
    queryKey: ["watchers"],
    queryFn: async () => (await api.get<WatchersResponse>("/watchers")).data,
  });

  const { data: metricsData } = useQuery({
    queryKey: ["metrics"],
    queryFn: async () => (await api.get<MetricsResponse>("/metrics")).data,
  });

  const watchers = ((data?.watchers ?? []) as (Watcher | undefined)[]).filter(
    (w): w is Watcher => !!w,
  );
  const grains = (data?.grains ?? ["day", "week", "month"]) as WatcherGrain[];
  const comparators = (data?.comparators ?? [
    "gt",
    "lt",
    "pct_change_gt",
    "pct_change_lt",
    "no_data",
  ]) as WatcherComparator[];
  const channels = (data?.channels ?? ["dashboard"]) as Channel[];
  const compareOptions = data?.compare_options ?? ["previous_period", "same_period_last_year"];

  const metrics: MetricDefinition[] = (metricsData?.metrics ?? []).filter(
    (m): m is MetricDefinition => !!m,
  );
  const metricLabel = useMemo(() => {
    const byId = new Map(metrics.map((m) => [m.id, m.label]));
    return (id: string) => byId.get(id) ?? "the metric";
  }, [metrics]);

  const noMetrics = metrics.length === 0;
  // Every watcher write is admin-only in the route policy, so a member is
  // offered the page (the list is theirs to read) with the writes disabled.
  const isAdmin = useIsAdmin();

  function openCreate() {
    setEditing(null);
    setShowForm(true);
  }

  function openEdit(w: Watcher) {
    setEditing(w);
    setShowForm(true);
  }

  function closeForm() {
    setShowForm(false);
    setEditing(null);
  }

  return (
    <div className="h-full overflow-y-auto">
      <div className="max-w-4xl mx-auto px-6 py-8 space-y-6">
        <div className="flex items-start justify-between gap-4">
          <div>
            <h1 className="text-2xl font-bold mb-1">Watchers</h1>
            <p className="text-sm text-muted-foreground">
              A watcher evaluates a metric on a schedule. When its condition breaches, the agent
              explains what moved and delivers the message — unprompted — to the channels you choose.
            </p>
          </div>
          {!showForm && (
            <Button
              onClick={openCreate}
              disabled={noMetrics || !isAdmin}
              title={isAdmin ? undefined : "Only admins can create watchers"}
              className="shrink-0"
            >
              New watcher
            </Button>
          )}
        </div>

        {noMetrics && !showForm && (
          <p className="text-sm text-muted-foreground">
            Define a metric on Settings → Metrics before creating a watcher — a watcher needs an
            authoritative number to watch.
          </p>
        )}

        {showForm && (
          <WatcherForm
            editing={editing}
            metrics={metrics}
            grains={grains}
            comparators={comparators}
            channels={channels}
            compareOptions={compareOptions}
            onDone={closeForm}
          />
        )}

        <Card>
          <CardHeader>
            <CardTitle>Your watchers</CardTitle>
          </CardHeader>
          <CardContent className="divide-y divide-border/50">
            {isLoading && (
              <div className="text-sm text-muted-foreground py-4">Loading…</div>
            )}
            {!isLoading && watchers.length === 0 && (
              <div className="text-sm text-muted-foreground py-4">
                No watchers yet. Create one above to have Argentum notice when a number moves.
              </div>
            )}
            {watchers.map((w) => (
              <WatcherRow
                key={w.id}
                watcher={w}
                metricLabel={metricLabel(w.metric_id)}
                onEdit={openEdit}
                onOpenEvents={setEventsWatcher}
              />
            ))}
          </CardContent>
        </Card>
      </div>

      <WatcherEventsSheet
        watcher={eventsWatcher}
        open={!!eventsWatcher}
        onOpenChange={(open) => !open && setEventsWatcher(null)}
      />
    </div>
  );
}
