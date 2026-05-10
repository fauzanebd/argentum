import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { History, MessageSquare, Pause, Play, Trash2 } from "lucide-react";
import cronstrue from "cronstrue";
import { formatDistanceToNow } from "date-fns";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { toast } from "@/hooks/use-toast";
import { cn } from "@/lib/utils";
import type { ScheduledTask } from "./types";

function humanCron(expr: string): string {
  try {
    return cronstrue.toString(expr, { use24HourTimeFormat: true });
  } catch {
    return expr;
  }
}

function relative(ts: string | null): string {
  if (!ts) return "never";
  try {
    return formatDistanceToNow(new Date(ts), { addSuffix: true });
  } catch {
    return ts;
  }
}

export function TaskRow({
  task,
  onOpenRuns,
}: {
  task: ScheduledTask;
  onOpenRuns: (task: ScheduledTask) => void;
}) {
  const qc = useQueryClient();
  const [enabled, setEnabled] = useState(task.enabled);

  const toggle = useMutation({
    mutationFn: async (next: boolean) =>
      (
        await api.patch<ScheduledTask>(`/scheduled-tasks/${task.id}`, {
          enabled: next,
        })
      ).data,
    onMutate: (next) => {
      setEnabled(next);
    },
    onError: (e: any, _vars, _ctx) => {
      setEnabled(task.enabled);
      toast({
        variant: "destructive",
        title: "Could not update task",
        description: e?.response?.data?.error || e.message,
      });
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["scheduled-tasks"] });
    },
  });

  async function remove() {
    if (!confirm(`Delete "${task.name}"? Run history will be removed.`)) return;
    try {
      await api.delete(`/scheduled-tasks/${task.id}`);
      qc.invalidateQueries({ queryKey: ["scheduled-tasks"] });
      toast({ title: "Task deleted" });
    } catch (e: any) {
      toast({
        variant: "destructive",
        title: "Delete failed",
        description: e?.response?.data?.error || e.message,
      });
    }
  }

  return (
    <div
      className={cn(
        "flex flex-col gap-2 py-3 sm:flex-row sm:items-center sm:justify-between",
        !enabled && "opacity-60",
      )}
    >
      <div className="min-w-0 flex-1 space-y-1">
        <div className="flex items-center gap-2">
          <span className="text-sm font-medium truncate">{task.name}</span>
          <Badge variant="outline" className="font-mono text-[10px]">
            {task.timezone}
          </Badge>
          {!enabled && <Badge variant="secondary">paused</Badge>}
        </div>
        <div className="text-xs text-muted-foreground">
          {humanCron(task.cron_expression)}{" "}
          <span className="font-mono">({task.cron_expression})</span>
        </div>
        <div className="text-xs text-muted-foreground">
          Last run {relative(task.last_run_at)} · next {relative(task.next_run_at)}
        </div>
        <p className="text-xs text-muted-foreground line-clamp-2">{task.prompt}</p>
      </div>
      <div className="flex items-center gap-2 sm:shrink-0">
        <Button
          variant="outline"
          size="sm"
          onClick={() => toggle.mutate(!enabled)}
          disabled={toggle.isPending}
          className="gap-1"
          title={enabled ? "Pause task" : "Resume task"}
        >
          {enabled ? (
            <>
              <Pause className="h-3.5 w-3.5" /> Pause
            </>
          ) : (
            <>
              <Play className="h-3.5 w-3.5" /> Resume
            </>
          )}
        </Button>
        <Button
          variant="outline"
          size="sm"
          onClick={() => onOpenRuns(task)}
          className="gap-1"
        >
          <History className="h-3.5 w-3.5" /> Runs
        </Button>
        <Button variant="ghost" size="icon" asChild title="Open thread">
          <Link to="/chat/$threadId" params={{ threadId: task.thread_id }}>
            <MessageSquare className="h-4 w-4" />
          </Link>
        </Button>
        <Button variant="ghost" size="icon" onClick={remove} title="Delete">
          <Trash2 className="h-4 w-4" />
        </Button>
      </div>
    </div>
  );
}
