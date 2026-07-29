import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil, Star, Trash2, X } from "lucide-react";
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
import { api } from "@/lib/api";
import { apiErrorMessage } from "@/lib/api-error";
import { useToast } from "@/hooks/use-toast";
import type { Agent, AgentsResponse, AgentToolInfo } from "@argentum/api-types";

interface Connection {
  id: string;
  db_type: string;
  label?: string;
  is_default: boolean;
}

/** Mirrors app.AgentInput. `enabled` is optional there too: an update that
 *  omits it leaves the flag alone. */
interface AgentDraft {
  name: string;
  description: string;
  persona_prompt: string;
  allowed_tools: string[];
  source_ids: string[];
  enabled?: boolean;
}

const EMPTY_DRAFT: AgentDraft = {
  name: "",
  description: "",
  persona_prompt: "",
  allowed_tools: [],
  source_ids: [],
  enabled: true,
};

function connectionLabel(c: Connection): string {
  return c.label?.trim() || `${c.db_type} database`;
}

/** The one sentence this panel exists to get right. An empty allowlist means
 *  *every* tool or *every* source — never none — and an empty checkbox group
 *  with no explanation reads as the opposite. */
function scopeSummary(selected: number, total: number, noun: string): string {
  if (selected === 0) return `All ${noun}`;
  return `${selected} of ${total} ${noun}`;
}

export function AgentsTab() {
  const qc = useQueryClient();
  const { toast } = useToast();

  // editing is null when the form is creating, or the agent id being edited.
  const [editing, setEditing] = useState<string | null>(null);
  const [draft, setDraft] = useState<AgentDraft>(EMPTY_DRAFT);
  const [error, setError] = useState<string | null>(null);

  const { data, isLoading } = useQuery({
    queryKey: ["agents"],
    queryFn: async () => (await api.get<AgentsResponse>("/agents")).data,
  });

  const { data: connections } = useQuery({
    queryKey: ["connections"],
    queryFn: async () =>
      (await api.get<{ connections: Connection[] }>("/connections")).data.connections ?? [],
  });

  const agents: Agent[] = (data?.agents ?? []).filter((a): a is Agent => !!a);
  // The tool vocabulary comes from the API rather than a constant here: it is
  // the registry this deployment actually runs, so a tool added on the backend
  // — or one absent because there is no object storage — is right without a
  // frontend change.
  const tools: AgentToolInfo[] = data?.tools ?? [];
  const sources: Connection[] = connections ?? [];

  function resetForm() {
    setEditing(null);
    setDraft(EMPTY_DRAFT);
    setError(null);
  }

  function startEdit(a: Agent) {
    setEditing(a.id);
    setDraft({
      name: a.name,
      description: a.description,
      persona_prompt: a.persona_prompt,
      allowed_tools: [...a.allowed_tools],
      source_ids: [...a.source_ids],
      enabled: a.enabled,
    });
    setError(null);
  }

  const save = useMutation({
    mutationFn: async () => {
      const body = { ...draft, name: draft.name.trim() };
      if (editing) return (await api.put<{ agent: Agent }>(`/agents/${editing}`, body)).data;
      return (await api.post<{ agent: Agent }>("/agents", body)).data;
    },
    onSuccess: () => {
      resetForm();
      qc.invalidateQueries({ queryKey: ["agents"] });
    },
    onError: (e: unknown) => setError(apiErrorMessage(e, "Could not save that agent")),
  });

  const remove = useMutation({
    mutationFn: async (id: string) => api.delete(`/agents/${id}`),
    onSuccess: (_r, id) => {
      if (editing === id) resetForm();
      qc.invalidateQueries({ queryKey: ["agents"] });
    },
    onError: (e: unknown) =>
      toast({ title: "Nothing deleted", description: apiErrorMessage(e), variant: "destructive" }),
  });

  const makeDefault = useMutation({
    mutationFn: async (id: string) => api.put(`/agents/${id}/default`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["agents"] }),
    onError: (e: unknown) =>
      toast({
        title: "Default unchanged",
        description: apiErrorMessage(e),
        variant: "destructive",
      }),
  });

  function toggleIn(key: "allowed_tools" | "source_ids", value: string) {
    setDraft((prev) => {
      const has = prev[key].includes(value);
      return {
        ...prev,
        [key]: has ? prev[key].filter((v) => v !== value) : [...prev[key], value],
      };
    });
  }

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>{editing ? "Edit agent" : "Create an agent"}</CardTitle>
          <CardDescription>
            An agent is a persona plus the tools and databases it may use. Give each job its own —
            a Finance agent that only sees the finance warehouse answers better than one prompt
            trying to serve everybody.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {/* The limitation, stated where it is decided rather than discovered.
              An agent named "HR" implies an access boundary this version does
              not draw, and a customer who assumes otherwise scopes an agent
              instead of scoping permissions. */}
          <div className="rounded-md border border-border bg-muted/40 p-3 text-xs text-muted-foreground">
            <span className="font-medium text-foreground">
              An agent scopes what it can reach, not who can use it.
            </span>{" "}
            Everyone in this workspace can open every agent. A Finance agent cannot query the HR
            database, but any member can still open it and ask what it has access to. Per-agent
            permissions are not available yet — keep anything that must stay private out of the
            databases you connect.
          </div>

          <div className="grid grid-cols-[1fr_1.4fr] gap-4">
            <div className="space-y-1.5">
              <Label htmlFor="agent-name">Name</Label>
              <Input
                id="agent-name"
                value={draft.name}
                onChange={(e) => setDraft({ ...draft, name: e.target.value })}
                placeholder="Finance"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="agent-description">Description</Label>
              <Input
                id="agent-description"
                value={draft.description}
                onChange={(e) => setDraft({ ...draft, description: e.target.value })}
                placeholder="Revenue, margin and cash questions"
              />
            </div>
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="agent-persona">Instructions</Label>
            <Textarea
              id="agent-persona"
              rows={5}
              value={draft.persona_prompt}
              onChange={(e) => setDraft({ ...draft, persona_prompt: e.target.value })}
              placeholder="Answer in the finance team's vocabulary. Revenue means recognised revenue, not bookings."
            />
            <p className="text-xs text-muted-foreground">
              Added to the agent's standing instructions — it does not replace them. The rules that
              keep answers grounded in your data still apply.
            </p>
          </div>

          <ScopeGroup
            title="Tools"
            summary={scopeSummary(draft.allowed_tools.length, tools.length, "tools")}
            hint="Nothing ticked means every tool this workspace has, including ones added later."
            onClear={
              draft.allowed_tools.length > 0
                ? () => setDraft({ ...draft, allowed_tools: [] })
                : undefined
            }
          >
            {tools.map((t) => (
              <label key={t.name} className="flex items-start gap-2 text-sm">
                <input
                  type="checkbox"
                  className="mt-0.5"
                  checked={draft.allowed_tools.includes(t.name)}
                  onChange={() => toggleIn("allowed_tools", t.name)}
                />
                <span>
                  {t.label}
                  <code className="block text-xs text-muted-foreground">{t.name}</code>
                </span>
              </label>
            ))}
          </ScopeGroup>

          <ScopeGroup
            title="Databases"
            summary={scopeSummary(draft.source_ids.length, sources.length, "databases")}
            hint="Nothing ticked means every database this workspace connects, including ones added later."
            onClear={
              draft.source_ids.length > 0 ? () => setDraft({ ...draft, source_ids: [] }) : undefined
            }
          >
            {sources.length === 0 && (
              <p className="text-sm text-muted-foreground">
                No databases connected yet. Add one on the Databases tab.
              </p>
            )}
            {sources.map((c) => (
              <label key={c.id} className="flex items-start gap-2 text-sm">
                <input
                  type="checkbox"
                  className="mt-0.5"
                  checked={draft.source_ids.includes(c.id)}
                  onChange={() => toggleIn("source_ids", c.id)}
                />
                <span>
                  {connectionLabel(c)}
                  <code className="block text-xs text-muted-foreground">{c.db_type}</code>
                </span>
              </label>
            ))}
          </ScopeGroup>

          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={draft.enabled !== false}
              onChange={(e) => setDraft({ ...draft, enabled: e.target.checked })}
            />
            Enabled
          </label>

          {error && <p className="text-sm text-destructive">{error}</p>}
        </CardContent>
        <CardFooter className="gap-2">
          <Button onClick={() => save.mutate()} disabled={!draft.name.trim() || save.isPending}>
            {save.isPending ? "Saving…" : editing ? "Save changes" : "Create agent"}
          </Button>
          {editing && (
            <Button variant="ghost" onClick={resetForm}>
              <X className="h-4 w-4 mr-1" />
              Cancel
            </Button>
          )}
        </CardFooter>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Agents</CardTitle>
          <CardDescription>
            The default answers anything that does not name an agent. A workspace always keeps at
            least one, and the default has to be handed over before it can be deleted.
          </CardDescription>
        </CardHeader>
        <CardContent className="divide-y divide-border/50">
          {isLoading && <div className="text-sm text-muted-foreground py-4">Loading…</div>}
          {!isLoading && agents.length === 0 && (
            <div className="text-sm text-muted-foreground py-4">No agents yet.</div>
          )}
          {agents.map((a) => (
            <div key={a.id} className="flex items-start justify-between gap-4 py-3">
              <div className="min-w-0 space-y-1">
                <div className="text-sm font-medium flex items-center gap-2">
                  <span className="truncate">{a.name}</span>
                  {a.is_default && <Badge variant="secondary">Default</Badge>}
                  {!a.enabled && <Badge variant="outline">Disabled</Badge>}
                </div>
                {a.description && (
                  <div className="text-xs text-muted-foreground">{a.description}</div>
                )}
                <div className="text-xs text-muted-foreground">
                  {scopeSummary(a.allowed_tools.length, tools.length, "tools")} ·{" "}
                  {scopeSummary(a.source_ids.length, sources.length, "databases")}
                </div>
              </div>
              <div className="flex shrink-0 items-center gap-1">
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={`Make ${a.name} the default`}
                  disabled={a.is_default || !a.enabled || makeDefault.isPending}
                  onClick={() => makeDefault.mutate(a.id)}
                >
                  <Star className={a.is_default ? "h-4 w-4 fill-current" : "h-4 w-4"} />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={`Edit ${a.name}`}
                  onClick={() => startEdit(a)}
                >
                  <Pencil className="h-4 w-4" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label={`Delete ${a.name}`}
                  onClick={() => {
                    if (confirm(`Delete ${a.name}? Threads that used it keep their history.`)) {
                      remove.mutate(a.id);
                    }
                  }}
                >
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>
            </div>
          ))}
        </CardContent>
      </Card>
    </div>
  );
}

/** ScopeGroup is the allowlist widget both halves of the form use. The summary
 *  line is the point: an empty group has to say "All", because an empty box
 *  with no explanation reads as "nothing", which is the opposite of what the
 *  backend does with it. */
function ScopeGroup({
  title,
  summary,
  hint,
  onClear,
  children,
}: {
  title: string;
  summary: string;
  hint: string;
  onClear?: () => void;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <Label>{title}</Label>
        <Badge variant="outline">{summary}</Badge>
        {onClear && (
          <Button variant="ghost" size="sm" className="h-6 px-2 text-xs" onClick={onClear}>
            Use all
          </Button>
        )}
      </div>
      <p className="text-xs text-muted-foreground">{hint}</p>
      <div className="space-y-2">{children}</div>
    </div>
  );
}
