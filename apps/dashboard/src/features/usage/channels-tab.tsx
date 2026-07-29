import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import type { ChannelUsageRow } from "@argentum/api-types";
import { CHANNEL_LABELS, buildRangeParams } from "./labels";

interface Props {
  from: string;
  to: string;
}

export function ChannelsTab({ from, to }: Props) {
  const { data, isLoading } = useQuery({
    queryKey: ["usage-by-channel", from, to],
    queryFn: async () =>
      (
        await api.get<{ channels: ChannelUsageRow[] }>("/usage/by-channel", {
          params: buildRangeParams(from, to),
        })
      ).data.channels,
  });

  const channels = data ?? [];
  const total = channels.reduce((s, c) => s + c.cost_usd, 0);

  return (
    <Card>
      <CardHeader>
        <CardTitle>By channel</CardTitle>
        <CardDescription>
          Total ${total.toFixed(4)} across {channels.length} channel(s).
        </CardDescription>
      </CardHeader>
      <CardContent className="divide-y divide-border/50">
        {isLoading && (
          <div className="py-4 text-sm text-muted-foreground">Loading…</div>
        )}
        {!isLoading && channels.length === 0 && (
          <div className="py-4 text-sm text-muted-foreground">No activity.</div>
        )}
        {channels.map((c) => {
          const pct = total > 0 ? (c.cost_usd / total) * 100 : 0;
          return (
            <div key={c.channel} className="py-3">
              <div className="flex items-center justify-between mb-1.5">
                <div>
                  <div className="text-sm font-medium">
                    {CHANNEL_LABELS[c.channel] ?? c.channel}
                  </div>
                  <div className="text-xs text-muted-foreground">
                    {c.thread_count} threads · {c.event_count} events ·{" "}
                    {c.tokens_in.toLocaleString()} in ·{" "}
                    {c.tokens_out.toLocaleString()} out
                  </div>
                </div>
                <div className="text-right">
                  <div className="font-mono text-sm">${c.cost_usd.toFixed(4)}</div>
                  <div className="text-xs text-muted-foreground">
                    {pct.toFixed(1)}%
                  </div>
                </div>
              </div>
              <div className="h-1.5 w-full rounded-full bg-muted overflow-hidden">
                <div
                  className="h-full bg-primary"
                  style={{ width: `${pct}%` }}
                />
              </div>
            </div>
          );
        })}
      </CardContent>
    </Card>
  );
}
