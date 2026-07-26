import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ExternalLink, Loader2 } from "lucide-react";
import { formatDistanceToNow, formatDistanceStrict } from "date-fns";
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
import type { RunStatus, ScheduledTask, TaskRun } from "./types";

const STATUS_VARIANT: Record<
  RunStatus,
  { variant: "default" | "secondary" | "destructive" | "outline"; label: string }
> = {
  running: { variant: "outline", label: "running" },
  succeeded: { variant: "secondary", label: "succeeded" },
  failed: { variant: "destructive", label: "failed" },
};

function duration(run: TaskRun): string | null {
  if (!run.finished_at) return null;
  try {
    return formatDistanceStrict(new Date(run.started_at), new Date(run.finished_at));
  } catch {
    return null;
  }
}

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

export function TaskRunsSheet({
  task,
  open,
  onOpenChange,
}: {
  task: ScheduledTask | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { data: runs, isLoading } = useQuery({
    enabled: open && !!task,
    queryKey: ["scheduled-task", task?.id, "runs"],
    queryFn: async () =>
      (
        await api.get<{ runs: TaskRun[] }>(
          `/scheduled-tasks/${task!.id}/runs?limit=50`,
        )
      ).data.runs,
  });

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="w-full overflow-y-auto sm:max-w-lg">
        <SheetHeader className="mb-4">
          <SheetTitle className="pr-8">{task?.name ?? "Runs"}</SheetTitle>
          <SheetDescription>Last 50 runs of this scheduled task.</SheetDescription>
          {task && (
            <Button asChild variant="outline" size="sm" className="mt-3 w-fit gap-1">
              <Link to="/chat/$threadId" params={{ threadId: task.thread_id }}>
                Open thread <ExternalLink className="h-3 w-3" />
              </Link>
            </Button>
          )}
        </SheetHeader>

        {isLoading && (
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" /> Loading runs…
          </div>
        )}

        {!isLoading && (runs?.length ?? 0) === 0 && (
          <p className="text-sm text-muted-foreground">No runs yet.</p>
        )}

        <div className="divide-y divide-border/50">
          {(runs ?? []).map((run) => {
            const meta = STATUS_VARIANT[run.status];
            const dur = duration(run);
            return (
              <div key={run.id} className="py-3 space-y-1">
                <div className="flex items-center justify-between gap-2">
                  <div className="flex items-center gap-2">
                    <Badge variant={meta.variant}>{meta.label}</Badge>
                    {dur && (
                      <span className="text-xs text-muted-foreground">{dur}</span>
                    )}
                  </div>
                  {task && run.assistant_msg_id && (
                    <Button asChild variant="ghost" size="sm" className="h-7 gap-1 px-2">
                      <Link
                        to="/chat/$threadId"
                        params={{ threadId: task.thread_id }}
                        hash={`msg-${run.assistant_msg_id}`}
                      >
                        Open <ExternalLink className="h-3 w-3" />
                      </Link>
                    </Button>
                  )}
                </div>
                <div
                  className="text-xs text-muted-foreground"
                  title={whenAbsolute(run.started_at)}
                >
                  Started {whenRelative(run.started_at)}
                </div>
                {run.error_message && (
                  <pre className="whitespace-pre-wrap rounded border border-destructive/30 bg-destructive/5 p-2 text-[11px] text-destructive">
                    {run.error_message}
                  </pre>
                )}
              </div>
            );
          })}
        </div>
      </SheetContent>
    </Sheet>
  );
}
