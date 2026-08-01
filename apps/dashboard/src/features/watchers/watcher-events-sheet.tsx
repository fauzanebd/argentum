import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ExternalLink, Loader2 } from "lucide-react";
import { formatDistanceToNow } from "date-fns";
import { api } from "@/lib/api";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { Watcher, WatcherEvent, WatcherEventsResponse } from "@argentum/api-types";
import { CHANNEL_LABELS } from "./watcher-model";

function whenAbsolute(ts: string): string {
  try {
    return new Date(ts).toLocaleString();
  } catch {
    return ts;
  }
}

function whenRelative(ts: string): string {
  try {
    return formatDistanceToNow(new Date(ts), { addSuffix: true });
  } catch {
    return ts;
  }
}

function num(n: number | undefined): string {
  if (n === undefined || n === null) return "—";
  return n.toLocaleString(undefined, { maximumFractionDigits: 4 });
}

const DELIVERY_VARIANT: Record<string, "secondary" | "destructive" | "outline"> = {
  delivered: "secondary",
  failed: "destructive",
  skipped: "outline",
};

export function WatcherEventsSheet({
  watcher,
  open,
  onOpenChange,
}: {
  watcher: Watcher | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { data: events, isLoading } = useQuery({
    enabled: open && !!watcher,
    queryKey: ["watcher", watcher?.id, "events"],
    queryFn: async () =>
      (
        await api.get<WatcherEventsResponse>(`/watchers/${watcher!.id}/events?limit=50`)
      ).data.events.filter((e): e is WatcherEvent => !!e),
  });

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="w-full overflow-y-auto sm:max-w-lg">
        <SheetHeader className="mb-4">
          <SheetTitle className="pr-8">{watcher?.name ?? "Events"}</SheetTitle>
          <SheetDescription>
            The last 50 evaluations of this watcher — breached, silent, or suppressed.
          </SheetDescription>
          {watcher && (
            <Button asChild variant="outline" size="sm" className="mt-3 w-fit gap-1">
              <Link to="/chat/$threadId" params={{ threadId: watcher.thread_id }}>
                Open thread <ExternalLink className="h-3 w-3" />
              </Link>
            </Button>
          )}
        </SheetHeader>

        {isLoading && (
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" /> Loading events…
          </div>
        )}

        {!isLoading && (events?.length ?? 0) === 0 && (
          <p className="text-sm text-muted-foreground">
            No evaluations recorded yet. Once the watcher is enabled and its schedule ticks, every
            evaluation lands here.
          </p>
        )}

        <div className="divide-y divide-border/50">
          {(events ?? []).map((ev) => (
            <div key={ev.id} className="py-3 space-y-1.5">
              <div className="flex items-center justify-between gap-2">
                <div className="flex items-center gap-2">
                  {ev.breached ? (
                    ev.suppressed_reason ? (
                      <Badge variant="outline">suppressed</Badge>
                    ) : (
                      <Badge variant="destructive">breached</Badge>
                    )
                  ) : (
                    <Badge variant="secondary">ok</Badge>
                  )}
                  {ev.suppressed_reason && (
                    <span className="text-[11px] text-muted-foreground">
                      {ev.suppressed_reason}
                    </span>
                  )}
                </div>
                <span
                  className="text-xs text-muted-foreground"
                  title={whenAbsolute(ev.fired_at)}
                >
                  {whenRelative(ev.fired_at)}
                </span>
              </div>

              <div className="text-xs text-muted-foreground">
                value <span className="font-mono">{num(ev.metric_value)}</span>
                {ev.comparison_value !== undefined && (
                  <>
                    {" · vs "}
                    <span className="font-mono">{num(ev.comparison_value)}</span>
                  </>
                )}
                {ev.delta_pct !== undefined && (
                  <>
                    {" · Δ "}
                    <span className="font-mono">{num(ev.delta_pct)}%</span>
                  </>
                )}
              </div>

              {ev.delivery_status && ev.delivery_status.length > 0 && (
                <div className="flex flex-wrap items-center gap-1.5">
                  {ev.delivery_status.map((d, i) => (
                    <Badge
                      key={`${d.channel}-${i}`}
                      variant={DELIVERY_VARIANT[d.status] ?? "outline"}
                      className="font-normal"
                      title={d.error || undefined}
                    >
                      {CHANNEL_LABELS[d.channel] ?? d.channel}: {d.status}
                    </Badge>
                  ))}
                </div>
              )}
            </div>
          ))}
        </div>
      </SheetContent>
    </Sheet>
  );
}
