import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
  FlaskConical,
  History,
  MessageSquare,
  Pause,
  Pencil,
  Play,
  Trash2,
} from "lucide-react";
import cronstrue from "cronstrue";
import { formatDistanceToNow } from "date-fns";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { toast } from "@/hooks/use-toast";
import { cn } from "@/lib/utils";
import { useIsAdmin } from "@/store/auth";
import { apiErrorMessage } from "@/lib/api-error";
import type { Watcher } from "@argentum/api-types";
import {
  conditionSummary,
  GRAIN_LABELS,
  hasFreshDryRun,
  watcherToDraft,
  type DryRunResult,
} from "./watcher-model";

function humanCron(expr: string): string {
  try {
    return cronstrue.toString(expr, { use24HourTimeFormat: true });
  } catch {
    return expr;
  }
}

function relative(ts: string | undefined): string {
  if (!ts) return "never";
  try {
    return formatDistanceToNow(new Date(ts), { addSuffix: true });
  } catch {
    return ts;
  }
}

export function WatcherRow({
  watcher,
  metricLabel,
  onEdit,
  onOpenEvents,
}: {
  watcher: Watcher;
  metricLabel: string;
  onEdit: (w: Watcher) => void;
  onOpenEvents: (w: Watcher) => void;
}) {
  const qc = useQueryClient();
  const isAdmin = useIsAdmin();
  const [dryRun, setDryRun] = useState<DryRunResult | null>(null);

  const runDryRun = useMutation({
    mutationFn: async () =>
      (await api.post<DryRunResult>(`/watchers/${watcher.id}/dry-run`)).data,
    onSuccess: (res) => {
      setDryRun(res);
      // last_dry_run_at just changed — refresh so the Enable gate unlocks.
      qc.invalidateQueries({ queryKey: ["watchers"] });
    },
    onError: (e: unknown) =>
      toast({
        variant: "destructive",
        title: "Dry-run failed",
        description: apiErrorMessage(e),
      }),
  });

  const toggle = useMutation({
    mutationFn: async (next: boolean) =>
      (await api.put<Watcher>(`/watchers/${watcher.id}`, watcherToDraft(watcher, next))).data,
    onSuccess: (_data, next) => {
      qc.invalidateQueries({ queryKey: ["watchers"] });
      toast({ title: next ? "Watcher enabled" : "Watcher paused" });
    },
    onError: (e: unknown) =>
      toast({
        variant: "destructive",
        title: "Could not update watcher",
        description: apiErrorMessage(e),
      }),
  });

  async function remove() {
    if (!confirm(`Delete "${watcher.name}"? Its event history will be removed.`)) return;
    try {
      await api.delete(`/watchers/${watcher.id}`);
      qc.invalidateQueries({ queryKey: ["watchers"] });
      toast({ title: "Watcher deleted" });
    } catch (e: unknown) {
      toast({
        variant: "destructive",
        title: "Delete failed",
        description: apiErrorMessage(e),
      });
    }
  }

  const fresh = hasFreshDryRun(watcher.last_dry_run_at);
  const canEnable = fresh && isAdmin;

  return (
    <div
      className={cn(
        "flex flex-col gap-2 py-3",
        !watcher.enabled && "opacity-70",
      )}
    >
      <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0 flex-1 space-y-1">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium truncate">{watcher.name}</span>
            {watcher.enabled ? (
              <Badge variant="secondary">enabled</Badge>
            ) : (
              <Badge variant="outline">off</Badge>
            )}
            <Badge variant="outline" className="font-mono text-[10px]">
              {watcher.timezone}
            </Badge>
          </div>
          <div className="text-xs text-muted-foreground">
            <span className="font-mono">{conditionSummary(watcher, metricLabel)}</span>
            {" · "}
            {GRAIN_LABELS[watcher.window_grain] ?? watcher.window_grain} window
          </div>
          <div className="text-xs text-muted-foreground">
            {humanCron(watcher.cron_expression)} · cooldown {watcher.cooldown_minutes}m · last fired{" "}
            {relative(watcher.last_fired_at)}
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2 sm:shrink-0">
          <Button
            variant="outline"
            size="sm"
            onClick={() => runDryRun.mutate()}
            disabled={runDryRun.isPending || !isAdmin}
            className="gap-1"
            title={
              isAdmin
                ? "Evaluate against recent history without firing"
                : "Only admins can run a dry-run — it runs the metric's SQL"
            }
          >
            <FlaskConical className="h-3.5 w-3.5" />
            {runDryRun.isPending ? "Running…" : "Dry-run"}
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => toggle.mutate(!watcher.enabled)}
            // Pausing is the same PUT as enabling, so it is the same permission.
            // Gating only the enable branch left a member a live Pause button on
            // every running watcher, which is what the 2026-08-04 gate found.
            disabled={toggle.isPending || !isAdmin || (!watcher.enabled && !canEnable)}
            className="gap-1"
            title={
              !isAdmin
                ? watcher.enabled
                  ? "Only admins can pause a watcher"
                  : "Only admins can enable a watcher"
                : watcher.enabled
                  ? "Pause this watcher"
                  : canEnable
                    ? "Enable this watcher"
                    : "Run a dry-run within the last 24h to enable"
            }
          >
            {watcher.enabled ? (
              <>
                <Pause className="h-3.5 w-3.5" /> Pause
              </>
            ) : (
              <>
                <Play className="h-3.5 w-3.5" /> Enable
              </>
            )}
          </Button>
          <Button variant="outline" size="sm" onClick={() => onOpenEvents(watcher)} className="gap-1">
            <History className="h-3.5 w-3.5" /> Events
          </Button>
          <Button
            variant="ghost"
            size="icon"
            onClick={() => onEdit(watcher)}
            disabled={!isAdmin}
            title={isAdmin ? "Edit" : "Only admins can edit watchers"}
          >
            <Pencil className="h-4 w-4" />
          </Button>
          <Button variant="ghost" size="icon" asChild title="Open thread">
            <Link to="/chat/$threadId" params={{ threadId: watcher.thread_id }}>
              <MessageSquare className="h-4 w-4" />
            </Link>
          </Button>
          <Button
            variant="ghost"
            size="icon"
            onClick={remove}
            disabled={!isAdmin}
            title={isAdmin ? "Delete" : "Only admins can delete watchers"}
          >
            <Trash2 className="h-4 w-4" />
          </Button>
        </div>
      </div>

      {/* The dry-run result: how often this condition would have fired over the
          trailing periods, shown before Enable unlocks. */}
      {dryRun && (
        <div className="rounded-md border bg-muted/30 p-3 text-xs">
          <p>
            Would have fired{" "}
            <span className="font-semibold">{dryRun.would_have_fired}</span> time
            {dryRun.would_have_fired === 1 ? "" : "s"} in the last {dryRun.periods_evaluated} periods.
          </p>
          {!watcher.enabled && (
            <p className="mt-1 text-muted-foreground">
              {canEnable
                ? "You can enable this watcher now."
                : "Enable is available to admins."}
            </p>
          )}
        </div>
      )}
    </div>
  );
}
