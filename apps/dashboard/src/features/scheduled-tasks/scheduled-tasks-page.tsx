import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { api } from "@/lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { TaskForm } from "./task-form";
import { TaskRow } from "./task-row";
import { TaskRunsSheet } from "./task-runs-sheet";
import type { ScheduledTask } from "@argentum/api-types";

export function ScheduledTasksPage() {
  const search = useSearch({ strict: false }) as { taskId?: string };
  const navigate = useNavigate();
  const [openTask, setOpenTask] = useState<ScheduledTask | null>(null);

  const { data: tasks, isLoading } = useQuery({
    queryKey: ["scheduled-tasks"],
    queryFn: async () =>
      (await api.get<{ tasks: ScheduledTask[] }>("/scheduled-tasks")).data.tasks,
  });

  // Auto-open runs sheet when navigated with ?taskId=…
  useEffect(() => {
    if (!search.taskId || !tasks) return;
    const t = tasks.find((x) => x.id === search.taskId);
    if (t) setOpenTask(t);
  }, [search.taskId, tasks]);

  function closeSheet(open: boolean) {
    if (open) return;
    setOpenTask(null);
    if (search.taskId) {
      navigate({
        to: "/scheduled-tasks",
        search: { taskId: undefined },
        replace: true,
      });
    }
  }

  return (
    <div className="h-full overflow-y-auto">
      <div className="max-w-4xl mx-auto px-6 py-8 space-y-6">
        <div>
          <h1 className="text-2xl font-bold mb-1">Scheduled tasks</h1>
          <p className="text-sm text-muted-foreground">
            Saved prompts the agent runs automatically on a cron schedule. Each task writes its
            results into a dedicated chat thread.
          </p>
        </div>

        <TaskForm />

        <Card>
          <CardHeader>
            <CardTitle>Your tasks</CardTitle>
          </CardHeader>
          <CardContent className="divide-y divide-border/50">
            {isLoading && (
              <div className="text-sm text-muted-foreground py-4">Loading…</div>
            )}
            {!isLoading && (tasks ?? []).length === 0 && (
              <div className="text-sm text-muted-foreground py-4">
                No scheduled tasks yet. Create your first above, or ask the agent in chat to
                schedule one for you.
              </div>
            )}
            {(tasks ?? []).map((t) => (
              <TaskRow key={t.id} task={t} onOpenRuns={setOpenTask} />
            ))}
          </CardContent>
        </Card>
      </div>

      <TaskRunsSheet
        task={openTask}
        open={!!openTask}
        onOpenChange={closeSheet}
      />
    </div>
  );
}
