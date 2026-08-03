import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { apiErrorMessage } from "@/lib/api-error";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import type { WebhooksResponse, WebhookSubscription } from "@argentum/api-types";

/** Settings → Webhooks (T-15).
 *
 *  What a tenant does here is name a URL and tick the events they want posted
 *  to it. Delivery is the backend's — signed, retried, and switched off after
 *  twenty consecutive failures — so the only things this screen owes are the
 *  form, the health of each subscription, and enough of the signing contract
 *  that somebody can verify a delivery without opening the API docs.
 *
 *  The event list and the disable threshold come from the API rather than being
 *  written here: both are facts about the backend, and a copy of either is a
 *  copy that goes stale the day a fourth event is published. */

const EVENT_COPY: Record<string, string> = {
  "watcher.breached": "A watcher's condition was met",
  "action.executed": "An approved action ran (or failed to)",
  "scheduled_task.completed": "A scheduled task finished",
};

export function WebhooksTab() {
  const qc = useQueryClient();
  const [url, setUrl] = useState("");
  const [events, setEvents] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);

  const { data, isLoading } = useQuery({
    queryKey: ["webhooks"],
    queryFn: async () => (await api.get<WebhooksResponse>("/webhooks")).data,
  });

  const subscriptions: WebhookSubscription[] = data?.subscriptions ?? [];
  const vocabulary = data?.events ?? [];

  const create = useMutation({
    mutationFn: async () => (await api.post("/webhooks", { url: url.trim(), events })).data,
    onSuccess: () => {
      setUrl("");
      setEvents([]);
      setError(null);
      void qc.invalidateQueries({ queryKey: ["webhooks"] });
    },
    onError: (e: unknown) => setError(apiErrorMessage(e)),
  });

  const toggleEnabled = useMutation({
    mutationFn: async (sub: WebhookSubscription) =>
      (await api.put(`/webhooks/${sub.id}`, {
        url: sub.url,
        events: sub.events,
        enabled: !sub.enabled,
      })).data,
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["webhooks"] }),
    onError: (e: unknown) => setError(apiErrorMessage(e)),
  });

  const remove = useMutation({
    mutationFn: async (id: string) => api.delete(`/webhooks/${id}`),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["webhooks"] }),
    onError: (e: unknown) => setError(apiErrorMessage(e)),
  });

  if (isLoading) return null;

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Send events to your own server</CardTitle>
          <CardDescription>
            Argentum posts a signed JSON body to your URL when one of these happens. Verify it with
            the <code className="text-xs">{data?.signature_header}</code> header — the signed
            message is {data?.signature_message}. Your workspace's secret is on the API keys tab.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="webhook-url">Endpoint URL</Label>
            <Input
              id="webhook-url"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="https://hooks.example.com/argentum"
            />
          </div>
          <div className="space-y-2">
            <Label>Events</Label>
            {vocabulary.map((e) => (
              <label key={e} className="flex items-start gap-2 text-sm">
                <input
                  type="checkbox"
                  className="mt-0.5"
                  checked={events.includes(e)}
                  onChange={() =>
                    setEvents((prev) =>
                      prev.includes(e) ? prev.filter((x) => x !== e) : [...prev, e],
                    )
                  }
                />
                <span>
                  {EVENT_COPY[e] ?? e}
                  <code className="block text-xs text-muted-foreground">{e}</code>
                </span>
              </label>
            ))}
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <Button
            onClick={() => create.mutate()}
            disabled={create.isPending || !url.trim() || events.length === 0}
          >
            {create.isPending ? "Adding…" : "Add subscription"}
          </Button>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Subscriptions</CardTitle>
          <CardDescription>
            A subscription switches itself off after {data?.disable_after ?? 20} consecutive failed
            deliveries. Fix the receiver, then turn it back on — the failure count resets.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          {subscriptions.length === 0 && (
            <p className="text-sm text-muted-foreground">Nothing subscribed yet.</p>
          )}
          {subscriptions.map((sub) => (
            <div
              key={sub.id}
              className="flex items-start justify-between gap-4 rounded border border-border p-3"
            >
              <div className="min-w-0 space-y-1">
                <p className="truncate font-mono text-sm">{sub.url}</p>
                <p className="text-xs text-muted-foreground">{sub.events.join(", ")}</p>
                {/* Two different sentences, deliberately: "you turned this off"
                    and "we did, and here is why" are not the same thing to the
                    admin looking at it. */}
                {!sub.enabled && sub.disabled_reason && (
                  <p className="text-xs text-destructive">{sub.disabled_reason}</p>
                )}
                {sub.consecutive_failures > 0 && sub.enabled && (
                  <p className="text-xs text-amber-600 dark:text-amber-500">
                    {sub.consecutive_failures} consecutive failed deliveries
                  </p>
                )}
              </div>
              <div className="flex shrink-0 items-center gap-2">
                <Badge variant={sub.enabled ? "secondary" : "outline"}>
                  {sub.enabled ? "Active" : "Off"}
                </Badge>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => toggleEnabled.mutate(sub)}
                  disabled={toggleEnabled.isPending}
                >
                  {sub.enabled ? "Disable" : "Enable"}
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => remove.mutate(sub.id)}
                  disabled={remove.isPending}
                >
                  Delete
                </Button>
              </div>
            </div>
          ))}
        </CardContent>
      </Card>
    </div>
  );
}
