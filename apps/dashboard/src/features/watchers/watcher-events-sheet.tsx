import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ChevronDown, ChevronRight, ExternalLink, Loader2 } from "lucide-react";
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

/** A run of consecutive suppressed evaluations, collapsed into one line.
 *
 *  A per-minute watcher inside a 12-hour cooldown writes an identical
 *  `suppressed` row every minute, so the 50 most recent evaluations were 50
 *  copies of "not now" and the delivery that started the cooldown was pushed off
 *  the screen that exists to show what the watcher did. Runs collapse here, and
 *  the "Fired only" filter below changes what the *query* returns, because past
 *  an hour of that the delivery is not merely off screen — it is not in the
 *  payload at all. */
type Row =
  | { kind: "event"; event: WatcherEvent }
  | { kind: "suppressed-run"; events: WatcherEvent[] };

function collapseSuppressed(events: WatcherEvent[]): Row[] {
  const rows: Row[] = [];
  for (const event of events) {
    const suppressed = !!event.suppressed_reason;
    const last = rows[rows.length - 1];
    if (suppressed && last?.kind === "suppressed-run") {
      last.events.push(event);
    } else if (suppressed) {
      rows.push({ kind: "suppressed-run", events: [event] });
    } else {
      rows.push({ kind: "event", event });
    }
  }
  return rows;
}

function SuppressedRun({ events }: { events: WatcherEvent[] }) {
  const [expanded, setExpanded] = useState(false);
  // One row is not a run — showing "1 evaluation suppressed" behind a toggle
  // hides more than it collapses.
  if (events.length === 1) return <EventRow event={events[0]} />;

  const newest = events[0];
  const oldest = events[events.length - 1];
  const reasons = [...new Set(events.map((e) => e.suppressed_reason).filter(Boolean))];

  return (
    <div className="py-3">
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        className="flex w-full items-center gap-2 text-left text-xs text-muted-foreground hover:text-foreground"
      >
        {expanded ? (
          <ChevronDown className="h-3.5 w-3.5 shrink-0" />
        ) : (
          <ChevronRight className="h-3.5 w-3.5 shrink-0" />
        )}
        <Badge variant="outline">{events.length} suppressed</Badge>
        <span className="truncate">
          {reasons.join(", ") || "suppressed"} · {whenRelative(oldest.fired_at)} –{" "}
          {whenRelative(newest.fired_at)}
        </span>
      </button>
      {expanded && (
        <div className="mt-1 divide-y divide-border/50 border-l border-border/50 pl-3">
          {events.map((e) => (
            <EventRow key={e.id} event={e} />
          ))}
        </div>
      )}
    </div>
  );
}

/** EventRow is one evaluation: what the metric read, and what was delivered. */
function EventRow({ event: ev }: { event: WatcherEvent }) {
  return (
    <div className="py-3 space-y-1.5">
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
            <span className="text-[11px] text-muted-foreground">{ev.suppressed_reason}</span>
          )}
        </div>
        <span className="text-xs text-muted-foreground" title={whenAbsolute(ev.fired_at)}>
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
  );
}

export function WatcherEventsSheet({
  watcher,
  open,
  onOpenChange,
}: {
  watcher: Watcher | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [firedOnly, setFiredOnly] = useState(false);
  const { data: events, isLoading } = useQuery({
    enabled: open && !!watcher,
    queryKey: ["watcher", watcher?.id, "events", firedOnly ? "fired" : "all"],
    queryFn: async () =>
      (
        await api.get<WatcherEventsResponse>(
          `/watchers/${watcher!.id}/events?limit=50${firedOnly ? "&fired=true" : ""}`,
        )
      ).data.events.filter((e): e is WatcherEvent => !!e),
  });
  const rows = collapseSuppressed(events ?? []);

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="w-full overflow-y-auto sm:max-w-lg">
        <SheetHeader className="mb-4">
          <SheetTitle className="pr-8">{watcher?.name ?? "Events"}</SheetTitle>
          <SheetDescription>
            {firedOnly
              ? "The last 50 evaluations of this watcher that actually delivered."
              : "The last 50 evaluations of this watcher — breached, silent, or suppressed."}
          </SheetDescription>
          <div className="flex items-center gap-1.5 pt-1">
            <Button
              variant={firedOnly ? "ghost" : "secondary"}
              size="sm"
              className="h-7 px-2 text-xs"
              onClick={() => setFiredOnly(false)}
            >
              All evaluations
            </Button>
            <Button
              variant={firedOnly ? "secondary" : "ghost"}
              size="sm"
              className="h-7 px-2 text-xs"
              onClick={() => setFiredOnly(true)}
            >
              Fired only
            </Button>
          </div>
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

        {!isLoading && (events?.length ?? 0) === 0 && !firedOnly && (
          <p className="text-sm text-muted-foreground">
            No evaluations recorded yet. Once the watcher is enabled and its schedule ticks, every
            evaluation lands here.
          </p>
        )}

        {!isLoading && (events?.length ?? 0) === 0 && firedOnly && (
          <p className="text-sm text-muted-foreground">
            This watcher has not delivered anything yet. Switch to “All evaluations” to see the
            evaluations that did not fire, and why.
          </p>
        )}

        <div className="divide-y divide-border/50">
          {rows.map((row) =>
            row.kind === "event" ? (
              <EventRow key={row.event.id} event={row.event} />
            ) : (
              <SuppressedRun key={row.events[0].id} events={row.events} />
            ),
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}
