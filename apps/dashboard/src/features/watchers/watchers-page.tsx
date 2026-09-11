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
import { freshnessSources } from "./watcher-model";
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

  // The connections list, for the freshness watcher's source picker (T-F3).
  // Shares the cache key with Settings → Data sources, so opening this page
  // after configuring a source shows it without a second request.
  const { data: connectionsData } = useQuery({
    queryKey: ["connections"],
    queryFn: async () =>
      (
        await api.get<{
          connections: {
            id: string;
            label?: string;
            db_type: string;
            freshness?: { sql?: string };
          }[];
        }>("/connections")
      ).data.connections,
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

  const sources = useMemo(
    () => freshnessSources(connectionsData ?? []),
    [connectionsData],
  );

  const metrics: MetricDefinition[] = (metricsData?.metrics ?? []).filter(
    (m): m is MetricDefinition => !!m,
  );
  const metricLabel = useMemo(() => {
    const byId = new Map(metrics.map((m) => [m.id, m.label]));
    // A freshness watcher has no metric id at all (T-F3), which is a different
    // thing from one whose metric was deleted — so it gets its own words rather
    // than the fallback for a missing row.
    return (id?: string) => (id ? (byId.get(id) ?? "the metric") : "data freshness");
  }, [metrics]);

  // A watcher needs *something* to watch, and since T-F3 a metric is not the
  // only candidate: a source with a freshness query is one too. Gating the
  // button on metrics alone would lock a tenant out of the feature they can
  // actually use.
  const nothingToWatch = metrics.length === 0 && sources.length === 0;
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
              disabled={nothingToWatch || !isAdmin}
              title={isAdmin ? undefined : "Only admins can create watchers"}
              className="shrink-0"
            >
              New watcher
            </Button>
          )}
        </div>

        {nothingToWatch && !showForm && (
          <p className="text-sm text-muted-foreground">
            A watcher needs something to watch. Define a metric on Settings → Metrics, or give a
            source a freshness query on Settings → Data sources to be told when its data stops
            arriving.
          </p>
        )}

        {showForm && (
          <WatcherForm
            editing={editing}
            metrics={metrics}
            sources={sources}
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
