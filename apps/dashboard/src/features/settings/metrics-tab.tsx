import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Gauge, Pencil, Play, Trash2, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
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
import { api } from "@/lib/api";
import { apiErrorMessage } from "@/lib/api-error";
import { useToast } from "@/hooks/use-toast";
import type {
  MetricDefinition,
  MetricGrain,
  MetricsResponse,
  MetricTestResponse,
  MetricUnit,
} from "@argentum/api-types";

interface Connection {
  id: string;
  db_type: string;
  label?: string;
  is_default: boolean;
}

/** Mirrors app.MetricInput. */
interface MetricDraft {
  source_id: string;
  key: string;
  label: string;
  description: string;
  sql_template: string;
  value_column: string;
  grain: MetricGrain;
  unit: MetricUnit;
  currency: string;
  higher_is_better: boolean;
  enabled: boolean;
}

const EMPTY_DRAFT: MetricDraft = {
  source_id: "",
  key: "",
  label: "",
  description: "",
  // A worked example, so an admin sees the {{from}}/{{to}} contract rather than
  // discovering it from an error. The window is bound as parameters, never
  // interpolated.
  sql_template:
    "SELECT COALESCE(SUM(total), 0) AS value\nFROM orders\nWHERE created_at >= {{from}} AND created_at < {{to}}",
  value_column: "value",
  grain: "month",
  unit: "currency",
  currency: "IDR",
  higher_is_better: true,
  enabled: true,
};

function connectionLabel(c: Connection): string {
  return c.label?.trim() || `${c.db_type} database`;
}

export function MetricsTab() {
  const qc = useQueryClient();
  const { toast } = useToast();

  const [editing, setEditing] = useState<string | null>(null);
  const [draft, setDraft] = useState<MetricDraft>(EMPTY_DRAFT);
  const [showForm, setShowForm] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // The last Test result: the SQL the backend rendered and the number it
  // returned, so an admin sees the metric work before saving it.
  const [tested, setTested] = useState<MetricTestResponse | null>(null);

  const { data, isLoading } = useQuery({
    queryKey: ["metrics"],
    queryFn: async () => (await api.get<MetricsResponse>("/metrics")).data,
  });

  const { data: connections } = useQuery({
    queryKey: ["connections"],
    queryFn: async () =>
      (await api.get<{ connections: Connection[] }>("/connections")).data.connections ?? [],
  });

  const metrics: MetricDefinition[] = (data?.metrics ?? []).filter(
    (m): m is MetricDefinition => !!m,
  );
  const grains: MetricGrain[] = data?.grains ?? ["day", "week", "month", "quarter", "year"];
  const units: MetricUnit[] = data?.units ?? ["currency", "count", "percent", "ratio"];
  const sources: Connection[] = connections ?? [];

  function resetForm() {
    setEditing(null);
    setDraft(EMPTY_DRAFT);
    setShowForm(false);
    setError(null);
    setTested(null);
  }

  function startCreate() {
    setEditing(null);
    setDraft({ ...EMPTY_DRAFT, source_id: sources.find((s) => s.is_default)?.id ?? sources[0]?.id ?? "" });
    setShowForm(true);
    setError(null);
    setTested(null);
  }

  function startEdit(m: MetricDefinition) {
    setEditing(m.id);
    setDraft({
      source_id: m.source_id,
      key: m.key,
      label: m.label,
      description: m.description,
      sql_template: m.sql_template,
      value_column: m.value_column,
      grain: m.grain,
      unit: m.unit,
      currency: m.currency ?? "",
      higher_is_better: m.higher_is_better,
      enabled: m.enabled,
    });
    setShowForm(true);
    setError(null);
    setTested(null);
  }

  const save = useMutation({
    mutationFn: async () => {
      const body = { ...draft, key: draft.key.trim() };
      if (editing) return (await api.put(`/metrics/${editing}`, body)).data;
      return (await api.post("/metrics", body)).data;
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["metrics"] });
      resetForm();
    },
    onError: (e: unknown) => setError(apiErrorMessage(e)),
  });

  const test = useMutation({
    mutationFn: async () =>
      (await api.post<MetricTestResponse>("/metrics/test", { ...draft, key: draft.key.trim() })).data,
    onSuccess: (r) => {
      setTested(r);
      setError(null);
    },
    onError: (e: unknown) => {
      setTested(null);
      setError(apiErrorMessage(e));
    },
  });

  const remove = useMutation({
    mutationFn: async (id: string) => api.delete(`/metrics/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["metrics"] }),
    onError: (e: unknown) =>
      toast({ title: "Not deleted", description: apiErrorMessage(e), variant: "destructive" }),
  });

  if (isLoading) {
    return <p className="text-sm text-muted-foreground">Loading metrics…</p>;
  }

  if (showForm) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>{editing ? "Edit metric" : "New metric"}</CardTitle>
          <CardDescription>
            A metric is a single SELECT that aggregates to one number. Reference the date window with{" "}
            <code>{"{{from}}"}</code> and <code>{"{{to}}"}</code> — they are bound as parameters, never
            pasted into the SQL. It must return exactly one row, and <code>value_column</code> must name
            the numeric column that carries the number.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1">
              <Label>Key</Label>
              <Input
                placeholder="revenue"
                value={draft.key}
                onChange={(e) => setDraft({ ...draft, key: e.target.value })}
              />
              <p className="text-xs text-muted-foreground">
                Lowercase identifier the agent names — e.g. <code>revenue</code>,{" "}
                <code>active_customers</code>.
              </p>
            </div>
            <div className="space-y-1">
              <Label>Label</Label>
              <Input
                placeholder="Monthly revenue"
                value={draft.label}
                onChange={(e) => setDraft({ ...draft, label: e.target.value })}
              />
            </div>
          </div>

          <div className="space-y-1">
            <Label>Description</Label>
            <Textarea
              placeholder="Net revenue: paid orders, excluding refunds."
              value={draft.description}
              onChange={(e) => setDraft({ ...draft, description: e.target.value })}
            />
            <p className="text-xs text-muted-foreground">
              What the agent reads to decide whether this metric answers a question. Be specific.
            </p>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1">
              <Label>Database</Label>
              <Select
                value={draft.source_id}
                onValueChange={(v) => setDraft({ ...draft, source_id: v })}
              >
                <SelectTrigger>
                  <SelectValue placeholder="Choose a database" />
                </SelectTrigger>
                <SelectContent>
                  {sources.map((s) => (
                    <SelectItem key={s.id} value={s.id}>
                      {connectionLabel(s)} ({s.db_type})
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1">
              <Label>Value column</Label>
              <Input
                placeholder="value"
                value={draft.value_column}
                onChange={(e) => setDraft({ ...draft, value_column: e.target.value })}
              />
            </div>
          </div>

          <div className="space-y-1">
            <Label>SQL template</Label>
            <Textarea
              className="font-mono text-xs min-h-[140px]"
              value={draft.sql_template}
              onChange={(e) => setDraft({ ...draft, sql_template: e.target.value })}
            />
          </div>

          <div className="grid grid-cols-3 gap-3">
            <div className="space-y-1">
              <Label>Grain</Label>
              <Select
                value={draft.grain}
                onValueChange={(v) => setDraft({ ...draft, grain: v as MetricGrain })}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {grains.map((g) => (
                    <SelectItem key={g} value={g}>
                      {g}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1">
              <Label>Unit</Label>
              <Select
                value={draft.unit}
                onValueChange={(v) => setDraft({ ...draft, unit: v as MetricUnit })}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {units.map((u) => (
                    <SelectItem key={u} value={u}>
                      {u}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            {draft.unit === "currency" && (
              <div className="space-y-1">
                <Label>Currency</Label>
                <Input
                  placeholder="IDR"
                  value={draft.currency}
                  onChange={(e) => setDraft({ ...draft, currency: e.target.value })}
                />
              </div>
            )}
          </div>

          <div className="flex items-center gap-6 text-sm">
            <label className="flex items-center gap-2">
              <input
                type="checkbox"
                checked={draft.higher_is_better}
                onChange={(e) => setDraft({ ...draft, higher_is_better: e.target.checked })}
              />
              Higher is better
            </label>
            <label className="flex items-center gap-2">
              <input
                type="checkbox"
                checked={draft.enabled}
                onChange={(e) => setDraft({ ...draft, enabled: e.target.checked })}
              />
              Enabled
            </label>
          </div>

          {/* The Test result: proof the metric renders, runs, and returns one
              number over the trailing-7-day window before it is saved. */}
          {tested && (
            <div className="rounded-md border bg-muted/30 p-3 text-sm space-y-2">
              <div>
                <span className="text-muted-foreground">Value over the last 7 days: </span>
                <span className="font-medium">{tested.value}</span>
              </div>
              <pre className="whitespace-pre-wrap font-mono text-[11px] text-muted-foreground">
                {tested.rendered_sql}
              </pre>
            </div>
          )}

          {error && <p className="text-sm text-destructive">{error}</p>}
        </CardContent>
        <CardFooter className="gap-2">
          <Button onClick={() => save.mutate()} disabled={!draft.key.trim() || save.isPending}>
            {save.isPending ? "Saving…" : editing ? "Save changes" : "Create metric"}
          </Button>
          <Button variant="outline" onClick={() => test.mutate()} disabled={test.isPending}>
            <Play className="h-4 w-4 mr-1" />
            {test.isPending ? "Testing…" : "Test"}
          </Button>
          <Button variant="ghost" onClick={resetForm}>
            <X className="h-4 w-4 mr-1" />
            Cancel
          </Button>
        </CardFooter>
      </Card>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold">Metrics</h2>
          <p className="text-sm text-muted-foreground">
            Named, validated numbers the agent prefers over ad-hoc SQL — so the same question returns
            the same answer.
          </p>
        </div>
        <Button onClick={startCreate} disabled={sources.length === 0}>
          New metric
        </Button>
      </div>

      {sources.length === 0 && (
        <p className="text-sm text-muted-foreground">
          Connect a database on the Databases tab before defining a metric.
        </p>
      )}

      {metrics.length === 0 ? (
        <p className="text-sm text-muted-foreground">No metrics defined yet.</p>
      ) : (
        <div className="space-y-2">
          {metrics.map((m) => (
            <div
              key={m.id}
              className="flex items-start justify-between rounded-md border p-3 text-sm"
            >
              <div className="space-y-0.5">
                <div className="flex items-center gap-2">
                  <Gauge className="h-4 w-4 text-muted-foreground" />
                  <span className="font-medium">{m.label}</span>
                  <code className="text-xs text-muted-foreground">{m.key}</code>
                  <Badge variant="outline" className="font-normal">
                    {m.unit}, per {m.grain}
                  </Badge>
                  {!m.enabled && (
                    <Badge variant="outline" className="font-normal">
                      disabled
                    </Badge>
                  )}
                </div>
                {m.description && (
                  <p className="text-xs text-muted-foreground">{m.description}</p>
                )}
              </div>
              <div className="flex items-center gap-1">
                <Button variant="ghost" size="sm" onClick={() => startEdit(m)}>
                  <Pencil className="h-4 w-4" />
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => remove.mutate(m.id)}
                  disabled={remove.isPending}
                >
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
