import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Phone, Globe } from "lucide-react";
import { api } from "@/lib/api";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import type { Thread } from "./types";
import { formatRelative } from "./format";

export function ThreadsPage() {
  const { data, isLoading } = useQuery({
    queryKey: ["threads"],
    queryFn: async () => (await api.get<{ threads: Thread[] }>("/threads")).data.threads,
  });

  const grouped = useMemo(() => {
    const map = new Map<string, Thread[]>();
    for (const t of data ?? []) {
      const key = t.channel === "whatsapp" && t.phone_number ? t.phone_number : "Dashboard";
      const arr = map.get(key) ?? [];
      arr.push(t);
      map.set(key, arr);
    }
    return Array.from(map.entries()).sort(([, a], [, b]) =>
      a[0]?.last_message_at < b[0]?.last_message_at ? 1 : -1,
    );
  }, [data]);

  return (
    <div className="h-full overflow-y-auto">
      <div className="max-w-5xl mx-auto px-6 py-8">
        <h1 className="text-2xl font-bold mb-1">Conversations</h1>
        <p className="text-sm text-muted-foreground mb-6">
          Threads from WhatsApp are grouped by phone number. Dashboard chats are shown together.
        </p>
        {isLoading && <div className="text-sm text-muted-foreground">Loading…</div>}
        {grouped.length === 0 && !isLoading && (
          <div className="text-sm text-muted-foreground">No conversations yet.</div>
        )}
        <div className="space-y-6">
          {grouped.map(([key, threads]) => (
            <Card key={key}>
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-base">
                  {key === "Dashboard" ? (
                    <Globe className="h-4 w-4 text-blue-600" />
                  ) : (
                    <Phone className="h-4 w-4 text-green-600" />
                  )}
                  {key}
                </CardTitle>
                <CardDescription>
                  {threads.length} thread{threads.length === 1 ? "" : "s"}
                </CardDescription>
              </CardHeader>
              <CardContent className="divide-y divide-border/50">
                {threads.map((t) => (
                  <Link
                    key={t.id}
                    to="/chat/$threadId"
                    params={{ threadId: t.id }}
                    className="flex items-start justify-between py-3 -mx-2 px-2 rounded-md hover:bg-accent/40 transition-colors"
                  >
                    <div className="flex-1 min-w-0">
                      <div className="text-sm font-medium truncate">{t.title || "Untitled"}</div>
                      {t.summary && (
                        <div className="text-xs text-muted-foreground line-clamp-2 mt-0.5">{t.summary}</div>
                      )}
                    </div>
                    <div className="text-right ml-4 shrink-0">
                      <Badge variant={t.is_archived ? "outline" : "secondary"}>
                        {t.is_archived ? "archived" : "active"}
                      </Badge>
                      <div className="text-[11px] text-muted-foreground mt-1">
                        {formatRelative(t.last_message_at)}
                      </div>
                    </div>
                  </Link>
                ))}
              </CardContent>
            </Card>
          ))}
        </div>
      </div>
    </div>
  );
}
