import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { ChevronRight } from "lucide-react";
import {
  CHANNEL_LABELS,
  buildRangeParams,
  type ThreadRow,
} from "./types";
import { ThreadDetailSheet } from "./thread-detail-sheet";

interface Props {
  from: string;
  to: string;
}

const PAGE_SIZE = 25;

export function ThreadsTab({ from, to }: Props) {
  const [offset, setOffset] = useState(0);
  const [selected, setSelected] = useState<ThreadRow | null>(null);

  const { data, isLoading } = useQuery({
    queryKey: ["usage-threads", from, to, offset],
    queryFn: async () =>
      (
        await api.get<{ threads: ThreadRow[] }>("/usage/threads", {
          params: { ...buildRangeParams(from, to), limit: PAGE_SIZE, offset },
        })
      ).data.threads,
  });

  const threads = data ?? [];
  const hasMore = threads.length === PAGE_SIZE;

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle>Threads by cost</CardTitle>
          <CardDescription>
            Click a row to drill into per-message events.
          </CardDescription>
        </CardHeader>
        <CardContent className="divide-y divide-border/50">
          {isLoading && (
            <div className="py-4 text-sm text-muted-foreground">Loading…</div>
          )}
          {!isLoading && threads.length === 0 && (
            <div className="py-4 text-sm text-muted-foreground">
              No threads in this window.
            </div>
          )}
          {threads.map((t) => (
            <button
              key={t.thread_id}
              onClick={() => setSelected(t)}
              className="w-full flex items-center justify-between gap-3 py-3 text-left hover:bg-muted/40 -mx-2 px-2 rounded-sm transition-colors"
            >
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className="text-sm font-medium truncate">
                    {t.title || "Untitled"}
                  </span>
                  <Badge variant="secondary" className="shrink-0">
                    {CHANNEL_LABELS[t.channel] ?? t.channel}
                  </Badge>
                </div>
                <div className="text-xs text-muted-foreground mt-0.5">
                  {t.event_count} events ·{" "}
                  {t.tokens_in.toLocaleString()} in ·{" "}
                  {t.tokens_out.toLocaleString()} out ·{" "}
                  {new Date(t.last_message_at).toLocaleDateString()}
                </div>
              </div>
              <div className="flex items-center gap-2 shrink-0">
                <span className="font-mono text-sm">${t.cost_usd.toFixed(4)}</span>
                <ChevronRight className="h-4 w-4 text-muted-foreground" />
              </div>
            </button>
          ))}
        </CardContent>
        {(offset > 0 || hasMore) && (
          <div className="px-6 pb-4 flex items-center justify-between">
            <Button
              variant="outline"
              size="sm"
              disabled={offset === 0}
              onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}
            >
              Previous
            </Button>
            <span className="text-xs text-muted-foreground">
              {offset + 1}–{offset + threads.length}
            </span>
            <Button
              variant="outline"
              size="sm"
              disabled={!hasMore}
              onClick={() => setOffset(offset + PAGE_SIZE)}
            >
              Next
            </Button>
          </div>
        )}
      </Card>

      <ThreadDetailSheet
        thread={selected}
        from={from}
        to={to}
        onClose={() => setSelected(null)}
      />
    </>
  );
}
