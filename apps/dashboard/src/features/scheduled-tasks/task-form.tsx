import { useMemo, useState } from "react";
import cronstrue from "cronstrue";
import { useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { toast } from "@/hooks/use-toast";
import { CRON_PRESETS, defaultTimezone } from "./cron-presets";
import type { ScheduledTask } from "./types";
import { apiErrorMessage } from "@/lib/api-error";

const CUSTOM_PRESET = "__custom__";

export function TaskForm() {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [prompt, setPrompt] = useState("");
  const [cron, setCron] = useState("0 9 * * *");
  const [timezone, setTimezone] = useState(defaultTimezone());
  const [presetValue, setPresetValue] = useState<string>("0 9 * * *");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const cronPreview = useMemo(() => {
    if (!cron.trim()) return { ok: false, text: "Enter a cron expression." };
    try {
      return {
        ok: true,
        text: cronstrue.toString(cron, { use24HourTimeFormat: true }),
      };
    } catch (e: unknown) {
      // cronstrue throws a bare string, not an Error. Handle both rather than
      // reaching for `.message` on a value the compiler cannot vouch for.
      const text =
        typeof e === "string" ? e : e instanceof Error ? e.message : "Invalid cron.";
      return { ok: false, text };
    }
  }, [cron]);

  const ready =
    name.trim().length > 0 &&
    prompt.trim().length > 0 &&
    cron.trim().length > 0 &&
    timezone.trim().length > 0 &&
    cronPreview.ok;

  function onPresetChange(value: string) {
    setPresetValue(value);
    if (value !== CUSTOM_PRESET) setCron(value);
  }

  function onCronChange(value: string) {
    setCron(value);
    const match = CRON_PRESETS.find((p) => p.expr === value);
    setPresetValue(match ? match.expr : CUSTOM_PRESET);
  }

  async function submit() {
    setError(null);
    setSubmitting(true);
    try {
      const res = await api.post<ScheduledTask>("/scheduled-tasks", {
        name: name.trim(),
        prompt: prompt.trim(),
        cron_expression: cron.trim(),
        timezone: timezone.trim(),
      });
      setName("");
      setPrompt("");
      qc.invalidateQueries({ queryKey: ["scheduled-tasks"] });
      toast({
        title: "Task scheduled",
        description: `${res.data.name} — ${cronPreview.text}`,
      });
    } catch (e: unknown) {
      setError(apiErrorMessage(e));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>New scheduled task</CardTitle>
        <CardDescription>
          The agent runs the prompt on the cron schedule and writes results to a dedicated thread.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="space-y-1.5">
          <Label>Name</Label>
          <Input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Weekly sales report"
          />
        </div>

        <div className="space-y-1.5">
          <Label>Prompt</Label>
          <Textarea
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            placeholder="Show me sales totals for last week, grouped by product."
            rows={4}
          />
        </div>

        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-1.5">
            <Label>Preset</Label>
            <Select value={presetValue} onValueChange={onPresetChange}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {CRON_PRESETS.map((p) => (
                  <SelectItem key={p.expr} value={p.expr}>
                    {p.label}
                  </SelectItem>
                ))}
                <SelectItem value={CUSTOM_PRESET}>Custom…</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1.5">
            <Label>Timezone (IANA)</Label>
            <Input
              value={timezone}
              onChange={(e) => setTimezone(e.target.value)}
              placeholder="Asia/Jakarta"
            />
          </div>
        </div>

        <div className="space-y-1.5">
          <Label>Cron expression</Label>
          <Input
            value={cron}
            onChange={(e) => onCronChange(e.target.value)}
            placeholder="minute hour day-of-month month day-of-week"
            className="font-mono"
          />
          <p
            className={
              cronPreview.ok
                ? "text-xs text-muted-foreground"
                : "text-xs text-destructive"
            }
          >
            {cronPreview.text}
          </p>
        </div>

        {error && <p className="text-sm text-destructive">{error}</p>}
      </CardContent>
      <CardFooter>
        <Button onClick={submit} disabled={!ready || submitting}>
          {submitting ? "Scheduling…" : "Schedule task"}
        </Button>
      </CardFooter>
    </Card>
  );
}
