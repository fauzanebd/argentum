import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { Badge } from "@/components/ui/badge";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import {
  EVENT_LABELS,
  buildRangeParams,
  microToUsd,
  type ThreadRow,
  type UsageEvent,
  type UsageSummary,
} from "./types";

interface Props {
  thread: ThreadRow | null;
  from: string;
  to: string;
  onClose: () => void;
}

export function ThreadDetailSheet({ thread, from, to, onClose }: Props) {
  const enabled = !!thread;
  const threadId = thread?.thread_id;

  const { data: summary } = useQuery({
    queryKey: ["usage-thread-summary", threadId, from, to],
    enabled,
    queryFn: async () =>
      (
        await api.get<UsageSummary>(`/usage/threads/${threadId}`, {
          params: buildRangeParams(from, to),
        })
      ).data,
  });

  const { data: events } = useQuery({
    queryKey: ["usage-thread-events", threadId],
    enabled,
    queryFn: async () =>
      (
        await api.get<{ events: UsageEvent[] }>(
          `/usage/threads/${threadId}/events`,
          { params: { limit: 200 } },
        )
      ).data.events,
  });

  return (
    <Sheet open={!!thread} onOpenChange={(o) => !o && onClose()}>
      <SheetContent side="right" className="sm:max-w-2xl w-full overflow-y-auto">
        <SheetHeader>
          <SheetTitle className="truncate">
            {thread?.title || "Untitled thread"}
          </SheetTitle>
          <SheetDescription>
            {thread?.channel} · {thread?.thread_id.slice(0, 8)}…
          </SheetDescription>
        </SheetHeader>

        <div className="mt-6 space-y-6">
          {summary && (
            <div className="grid grid-cols-3 gap-3">
              <Stat label="Cost" value={`$${summary.total_cost_usd.toFixed(4)}`} />
              <Stat label="Tokens in" value={summary.total_tokens_in.toLocaleString()} />
              <Stat label="Tokens out" value={summary.total_tokens_out.toLocaleString()} />
            </div>
          )}

          {summary && Object.keys(summary.cost_by_event_type_usd).length > 0 && (
            <div>
              <h3 className="text-xs font-medium text-muted-foreground mb-2">
                By event type
              </h3>
              <div className="divide-y divide-border/50 rounded border border-border/50">
                {Object.entries(summary.cost_by_event_type_usd).map(([type, cost]) => (
                  <div
                    key={type}
                    className="flex items-center justify-between px-3 py-2 text-sm"
                  >
                    <span>{EVENT_LABELS[type] ?? type}</span>
                    <span className="font-mono text-xs">${cost.toFixed(4)}</span>
                  </div>
                ))}
              </div>
            </div>
          )}

          {summary?.cost_by_model_usd && Object.keys(summary.cost_by_model_usd).length > 0 && (
            <div>
              <h3 className="text-xs font-medium text-muted-foreground mb-2">By model</h3>
              <div className="divide-y divide-border/50 rounded border border-border/50">
                {Object.entries(summary.cost_by_model_usd).map(([model, cost]) => (
                  <div
                    key={model}
                    className="flex items-center justify-between px-3 py-2 text-sm"
                  >
                    <span className="font-mono text-xs truncate">{model}</span>
                    <span className="font-mono text-xs">${cost.toFixed(4)}</span>
                  </div>
                ))}
              </div>
            </div>
          )}

          {events && (
            <div>
              <h3 className="text-xs font-medium text-muted-foreground mb-2">
                Events ({events.length})
              </h3>
              {events.length === 0 ? (
                <p className="text-sm text-muted-foreground">No events.</p>
              ) : (
                <ul className="divide-y divide-border/50 rounded border border-border/50">
                  {events.map((e) => (
                    <li key={e.id} className="px-3 py-2 text-sm">
                      <div className="flex items-center justify-between gap-2">
                        <div className="flex items-center gap-2 min-w-0">
                          <Badge variant="secondary" className="shrink-0">
                            {EVENT_LABELS[e.event_type] ?? e.event_type}
                          </Badge>
                          {e.model && (
                            <span className="font-mono text-xs text-muted-foreground truncate">
                              {e.model}
                            </span>
                          )}
                        </div>
                        <span className="font-mono text-xs shrink-0">
                          ${microToUsd(e.cost_micro_usd).toFixed(6)}
                        </span>
                      </div>
                      <div className="mt-1 flex items-center gap-3 text-xs text-muted-foreground">
                        <span>{new Date(e.created_at).toLocaleString()}</span>
                        {(e.tokens_in > 0 || e.tokens_out > 0) && (
                          <span className="font-mono">
                            {e.tokens_in}↓ {e.tokens_out}↑
                          </span>
                        )}
                        {e.cache_read_tokens_in > 0 && (
                          <span className="font-mono">
                            cache {e.cache_read_tokens_in}
                          </span>
                        )}
                      </div>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded border border-border/50 px-3 py-2">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="text-sm font-semibold mt-0.5">{value}</div>
    </div>
  );
}
