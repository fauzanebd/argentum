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
import type { UserUsageRow } from "@argentum/api-types";
import { CHANNEL_LABELS, USER_KIND_LABELS, buildRangeParams } from "./labels";

interface Props {
  from: string;
  to: string;
}

export function UsersTab({ from, to }: Props) {
  const { data, isLoading } = useQuery({
    queryKey: ["usage-by-user", from, to],
    queryFn: async () =>
      (
        await api.get<{ users: UserUsageRow[] }>("/usage/by-user", {
          params: buildRangeParams(from, to),
        })
      ).data.users,
  });

  const users = (data ?? []).slice().sort((a, b) => b.cost_usd - a.cost_usd);

  return (
    <Card>
      <CardHeader>
        <CardTitle>By user</CardTitle>
        <CardDescription>
          End-user identity varies by channel — phone for WhatsApp, account for
          dashboard, provider id for Discord/Lark.
        </CardDescription>
      </CardHeader>
      <CardContent className="divide-y divide-border/50">
        {isLoading && (
          <div className="py-4 text-sm text-muted-foreground">Loading…</div>
        )}
        {!isLoading && users.length === 0 && (
          <div className="py-4 text-sm text-muted-foreground">No users.</div>
        )}
        {users.map((u) => (
          <div
            key={`${u.channel}-${u.user_key}`}
            className="flex items-center justify-between gap-3 py-3"
          >
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2">
                <span className="font-mono text-sm truncate">{u.user_key}</span>
                <Badge variant="secondary" className="shrink-0">
                  {USER_KIND_LABELS[u.user_key_kind] ?? u.user_key_kind}
                </Badge>
              </div>
              <div className="text-xs text-muted-foreground mt-0.5">
                {CHANNEL_LABELS[u.channel] ?? u.channel} · {u.thread_count} threads ·{" "}
                {u.event_count} events ·{" "}
                {u.tokens_in.toLocaleString()} in ·{" "}
                {u.tokens_out.toLocaleString()} out
              </div>
            </div>
            <span className="font-mono text-sm shrink-0">
              ${u.cost_usd.toFixed(4)}
            </span>
          </div>
        ))}
      </CardContent>
    </Card>
  );
}
