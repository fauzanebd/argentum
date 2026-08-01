import { useMemo, useState } from "react";
import cronstrue from "cronstrue";
import { useQueryClient } from "@tanstack/react-query";
import { X } from "lucide-react";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
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
import { apiErrorMessage } from "@/lib/api-error";
import { CRON_PRESETS, defaultTimezone } from "@/features/scheduled-tasks/cron-presets";
import type {
  Channel,
  MetricDefinition,
  Watcher,
  WatcherChannel,
  WatcherComparator,
  WatcherGrain,
} from "@argentum/api-types";
import {
  channelRefPlaceholder,
  CHANNEL_LABELS,
  COMPARATOR_LABELS,
  COMPARE_LABELS,
  GRAIN_LABELS,
  needsComparison,
  usesThreshold,
  type WatcherDraft,
} from "./watcher-model";

const CUSTOM_PRESET = "__custom__";

function emptyDraft(metricID: string): WatcherDraft {
  return {
    metric_id: metricID,
    name: "",
    window_grain: "day",
    comparator: "lt",
    threshold: 0,
    compare_to: "previous_period",
    cron_expression: "0 9 * * *",
    timezone: defaultTimezone(),
    channels: [{ channel: "dashboard", ref: "" }],
    cooldown_minutes: 720,
  };
}

function draftFrom(w: Watcher): WatcherDraft {
  return {
    metric_id: w.metric_id,
    name: w.name,
    window_grain: w.window_grain,
    comparator: w.comparator,
    threshold: w.threshold,
    compare_to: w.compare_to || "previous_period",
    cron_expression: w.cron_expression,
    timezone: w.timezone,
    channels: w.channels.length ? w.channels : [{ channel: "dashboard", ref: "" }],
    cooldown_minutes: w.cooldown_minutes,
  };
}

export function WatcherForm({
  editing,
  metrics,
  grains,
  comparators,
  channels,
  compareOptions,
  onDone,
}: {
  editing: Watcher | null;
  metrics: MetricDefinition[];
  grains: WatcherGrain[];
  comparators: WatcherComparator[];
  channels: Channel[];
  compareOptions: string[];
  onDone: () => void;
}) {
  const qc = useQueryClient();
  const [draft, setDraft] = useState<WatcherDraft>(
    editing ? draftFrom(editing) : emptyDraft(metrics[0]?.id ?? ""),
  );
  const [presetValue, setPresetValue] = useState<string>(() =>
    CRON_PRESETS.some((p) => p.expr === (editing?.cron_expression ?? "0 9 * * *"))
      ? editing?.cron_expression ?? "0 9 * * *"
      : CUSTOM_PRESET,
  );
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const cronPreview = useMemo(() => {
    if (!draft.cron_expression.trim()) return { ok: false, text: "Enter a cron expression." };
    try {
      return {
        ok: true,
        text: cronstrue.toString(draft.cron_expression, { use24HourTimeFormat: true }),
      };
    } catch (e: unknown) {
      const text =
        typeof e === "string" ? e : e instanceof Error ? e.message : "Invalid cron.";
      return { ok: false, text };
    }
  }, [draft.cron_expression]);

  const missingChannelRef = draft.channels.some(
    (ch) => ch.channel !== "dashboard" && !(ch.ref ?? "").trim(),
  );

  const ready =
    draft.metric_id !== "" &&
    draft.name.trim().length > 0 &&
    draft.cron_expression.trim().length > 0 &&
    draft.timezone.trim().length > 0 &&
    draft.channels.length > 0 &&
    !missingChannelRef &&
    cronPreview.ok;

  function set<K extends keyof WatcherDraft>(key: K, value: WatcherDraft[K]) {
    setDraft((d) => ({ ...d, [key]: value }));
  }

  function onPresetChange(value: string) {
    setPresetValue(value);
    if (value !== CUSTOM_PRESET) set("cron_expression", value);
  }

  function onCronChange(value: string) {
    set("cron_expression", value);
    const match = CRON_PRESETS.find((p) => p.expr === value);
    setPresetValue(match ? match.expr : CUSTOM_PRESET);
  }

  function toggleChannel(channel: Channel, on: boolean) {
    setDraft((d) => {
      if (on) {
        if (d.channels.some((c) => c.channel === channel)) return d;
        return { ...d, channels: [...d.channels, { channel, ref: "" }] };
      }
      return { ...d, channels: d.channels.filter((c) => c.channel !== channel) };
    });
  }

  function setChannelRef(channel: Channel, ref: string) {
    setDraft((d) => ({
      ...d,
      channels: d.channels.map((c) => (c.channel === channel ? { ...c, ref } : c)),
    }));
  }

  async function submit() {
    setError(null);
    setSubmitting(true);
    // no_data ignores the threshold; send a clean 0 rather than a stale number.
    const body: WatcherDraft = {
      ...draft,
      name: draft.name.trim(),
      cron_expression: draft.cron_expression.trim(),
      timezone: draft.timezone.trim(),
      threshold: usesThreshold(draft.comparator) ? draft.threshold : 0,
      compare_to: needsComparison(draft.comparator) ? draft.compare_to : "",
      channels: draft.channels.map((c) => ({
        channel: c.channel,
        ref: c.channel === "dashboard" ? "" : (c.ref ?? "").trim(),
      })),
    };
    try {
      if (editing) {
        await api.put(`/watchers/${editing.id}`, body);
      } else {
        await api.post("/watchers", body);
      }
      qc.invalidateQueries({ queryKey: ["watchers"] });
      toast({
        title: editing ? "Watcher updated" : "Watcher created",
        description: editing
          ? undefined
          : "Run a dry-run to check it against recent history, then enable it.",
      });
      onDone();
    } catch (e: unknown) {
      setError(apiErrorMessage(e));
    } finally {
      setSubmitting(false);
    }
  }

  const activeChannel = (c: Channel): WatcherChannel | undefined =>
    draft.channels.find((x) => x.channel === c);

  return (
    <Card>
      <CardHeader>
        <CardTitle>{editing ? "Edit watcher" : "New watcher"}</CardTitle>
        <CardDescription>
          A watcher evaluates a metric on a schedule and, when the condition breaches, has the
          agent explain what moved and delivers it to the channels you choose. A new or re-conditioned
          watcher stays off until a dry-run vouches for it.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-1.5">
            <Label>Name</Label>
            <Input
              value={draft.name}
              onChange={(e) => set("name", e.target.value)}
              placeholder="Revenue drop"
            />
          </div>
          <div className="space-y-1.5">
            <Label>Metric</Label>
            <Select value={draft.metric_id} onValueChange={(v) => set("metric_id", v)}>
              <SelectTrigger>
                <SelectValue placeholder="Choose a metric" />
              </SelectTrigger>
              <SelectContent>
                {metrics.map((m) => (
                  <SelectItem key={m.id} value={m.id}>
                    {m.label}
                    {!m.enabled ? " (disabled)" : ""}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>

        {/* The condition: comparator, and a threshold unless the comparator is
            no_data (which fires on the absence of a row, not a number). */}
        <div className="grid grid-cols-3 gap-4">
          <div className="space-y-1.5">
            <Label>Window</Label>
            <Select
              value={draft.window_grain}
              onValueChange={(v) => set("window_grain", v as WatcherGrain)}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {grains.map((g) => (
                  <SelectItem key={g} value={g}>
                    {GRAIN_LABELS[g] ?? g}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1.5">
            <Label>Condition</Label>
            <Select
              value={draft.comparator}
              onValueChange={(v) => set("comparator", v as WatcherComparator)}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {comparators.map((c) => (
                  <SelectItem key={c} value={c}>
                    {COMPARATOR_LABELS[c] ?? c}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          {usesThreshold(draft.comparator) && (
            <div className="space-y-1.5">
              <Label>{needsComparison(draft.comparator) ? "Threshold (%)" : "Threshold"}</Label>
              <Input
                type="number"
                value={Number.isNaN(draft.threshold) ? "" : draft.threshold}
                onChange={(e) => set("threshold", e.target.valueAsNumber)}
                className="font-mono"
              />
            </div>
          )}
        </div>

        {needsComparison(draft.comparator) && (
          <div className="space-y-1.5">
            <Label>Compare to</Label>
            <Select value={draft.compare_to} onValueChange={(v) => set("compare_to", v)}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {compareOptions.map((o) => (
                  <SelectItem key={o} value={o}>
                    {COMPARE_LABELS[o] ?? o}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        )}

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
              value={draft.timezone}
              onChange={(e) => set("timezone", e.target.value)}
              placeholder="Asia/Jakarta"
            />
          </div>
        </div>

        <div className="space-y-1.5">
          <Label>Cron expression</Label>
          <Input
            value={draft.cron_expression}
            onChange={(e) => onCronChange(e.target.value)}
            placeholder="minute hour day-of-month month day-of-week"
            className="font-mono"
          />
          <p
            className={
              cronPreview.ok ? "text-xs text-muted-foreground" : "text-xs text-destructive"
            }
          >
            {cronPreview.text}
          </p>
        </div>

        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-1.5">
            <Label>Cooldown (minutes)</Label>
            <Input
              type="number"
              min={0}
              value={Number.isNaN(draft.cooldown_minutes) ? "" : draft.cooldown_minutes}
              onChange={(e) => set("cooldown_minutes", e.target.valueAsNumber)}
            />
            <p className="text-xs text-muted-foreground">
              The shortest gap between two fires. A breach inside this window is recorded but stays
              silent.
            </p>
          </div>
        </div>

        <div className="space-y-2">
          <Label>Deliver to</Label>
          <div className="space-y-2">
            {channels.map((c) => {
              const active = activeChannel(c);
              const on = !!active;
              const needsRef = c !== "dashboard";
              return (
                <div key={c} className="space-y-1.5">
                  <label className="flex items-center gap-2 text-sm">
                    <input
                      type="checkbox"
                      checked={on}
                      onChange={(e) => toggleChannel(c, e.target.checked)}
                    />
                    {CHANNEL_LABELS[c] ?? c}
                  </label>
                  {on && needsRef && (
                    <Input
                      value={active?.ref ?? ""}
                      onChange={(e) => setChannelRef(c, e.target.value)}
                      placeholder={channelRefPlaceholder(c)}
                      className="ml-6 w-[calc(100%-1.5rem)]"
                    />
                  )}
                </div>
              );
            })}
          </div>
          {missingChannelRef && (
            <p className="text-xs text-destructive">
              Each non-dashboard channel needs a delivery target.
            </p>
          )}
        </div>

        {error && <p className="text-sm text-destructive">{error}</p>}
      </CardContent>
      <CardFooter className="gap-2">
        <Button onClick={submit} disabled={!ready || submitting}>
          {submitting ? "Saving…" : editing ? "Save changes" : "Create watcher"}
        </Button>
        <Button variant="ghost" onClick={onDone}>
          <X className="h-4 w-4 mr-1" /> Cancel
        </Button>
      </CardFooter>
    </Card>
  );
}
