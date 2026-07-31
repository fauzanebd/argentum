import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Bot, FilePlus2, Pencil, Star, Trash2, X } from "lucide-react";
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
  Agent,
  AgentBindingsResponse,
  AgentChannelBinding,
  AgentsResponse,
  AgentTemplate,
  AgentToolInfo,
  Channel,
} from "@argentum/api-types";

interface Connection {
  id: string;
  db_type: string;
  label?: string;
  /** Generated from the schema by the connection describer, so it is the
   *  closest thing the dashboard has to the table names a template's hints
   *  describe — see matchSources. */
  description?: string;
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
  /** Which gallery card this came from, or "" for the blank path. Sent on
   *  create and ignored on update — the backend treats it as provenance. */
  template_key: string;
  enabled?: boolean;
}

const EMPTY_DRAFT: AgentDraft = {
  name: "",
  description: "",
  persona_prompt: "",
  allowed_tools: [],
  source_ids: [],
  template_key: "",
  enabled: true,
};

function connectionLabel(c: Connection): string {
  return c.label?.trim() || `${c.db_type} database`;
}

/** Hints are matched at the *word start*, case-insensitively: "invoice" has to
 *  catch `invoices` and "fulfil" has to catch `fulfilment`, while "ops" must
 *  not quietly claim `shops`. */
function hintMatches(hint: string, haystack: string): boolean {
  const escaped = hint.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return new RegExp(`\\b${escaped}`, "i").test(haystack);
}

/**
 * Which databases a template pre-ticks, and the hint that ticked each one.
 *
 * The *why* is not decoration. Pre-ticking the wrong source silently scopes an
 * agent away from its own data, and an admin who cannot see which word matched
 * has no way to tell a good guess from a coincidence — so every tick is
 * labelled and one click clears the lot.
 *
 * The haystack is the connection's label and its generated description. Table
 * names would be the stronger signal the ticket describes, but no `/api` route
 * exposes a source's tables to the browser; the description is generated from
 * exactly those tables, which is the closest this screen can get without a
 * schema round trip per connection.
 */
function matchSources(hints: string[], sources: Connection[]): Map<string, string> {
  const out = new Map<string, string>();
  for (const c of sources) {
    const haystack = `${c.label ?? ""} ${c.description ?? ""} ${c.db_type}`;
    const hit = hints.find((h) => hintMatches(h, haystack));
    if (hit) out.set(c.id, hit);
  }
  return out;
}

/** A picked card becomes an ordinary draft. Everything it fills stays editable
 *  before the save, and what saves is a plain AgentDraft — nothing about the
 *  template survives except `template_key`. */
function draftFromTemplate(t: AgentTemplate, sources: Connection[]): AgentDraft {
  return {
    name: t.name,
    description: t.description,
    persona_prompt: t.persona,
    allowed_tools: [...t.suggested_tools],
    // Nothing matched means nothing ticked, which the backend reads as every
    // source — the same rule an empty allowlist has always carried.
    source_ids: [...matchSources(t.source_hints, sources).keys()],
    template_key: t.key,
    enabled: true,
  };
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
  // Creating starts at the gallery and moves to the form once a card — or the
  // blank card — is picked. Editing goes straight to the form: an agent that
  // exists came from somewhere already.
  const [showForm, setShowForm] = useState(false);
  // Which source each hint ticked, for the "matched `invoice`" line beside it.
  // Cleared the moment the admin touches the group, because after that the
  // ticks are theirs and attributing them to a template would be a lie.
  const [hintedSources, setHintedSources] = useState<Map<string, string>>(new Map());

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
  // The gallery, like the tool list, is the backend's: it is a file the API
  // loads at boot, already narrowed to the tools this deployment runs. An empty
  // one leaves only the blank path, which is the screen as it was before
  // templates existed.
  const templates: AgentTemplate[] = (data?.templates ?? []).filter(
    (t): t is AgentTemplate => !!t,
  );
  const sources: Connection[] = connections ?? [];

  function resetForm() {
    setEditing(null);
    setDraft(EMPTY_DRAFT);
    setError(null);
    setShowForm(false);
    setHintedSources(new Map());
  }

  function startEdit(a: Agent) {
    setEditing(a.id);
    setDraft({
      name: a.name,
      description: a.description,
      persona_prompt: a.persona_prompt,
      allowed_tools: [...a.allowed_tools],
      source_ids: [...a.source_ids],
      template_key: a.template_key,
      enabled: a.enabled,
    });
    setError(null);
    setShowForm(true);
    setHintedSources(new Map());
  }

  function startFromTemplate(t: AgentTemplate) {
    setEditing(null);
    setDraft(draftFromTemplate(t, sources));
    setHintedSources(matchSources(t.source_hints, sources));
    setError(null);
    setShowForm(true);
  }

  /** The blank path is a supported way to create an agent, not a fallback —
   *  it opens exactly the form that existed before the gallery did. */
  function startFromBlank() {
    setEditing(null);
    setDraft(EMPTY_DRAFT);
    setHintedSources(new Map());
    setError(null);
    setShowForm(true);
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
    if (key === "source_ids" && hintedSources.has(value)) {
      // The tick is the admin's from here on, so stop crediting a template
      // for it.
      setHintedSources((prev) => {
        const next = new Map(prev);
        next.delete(value);
        return next;
      });
    }
    setDraft((prev) => {
      const has = prev[key].includes(value);
      return {
        ...prev,
        [key]: has ? prev[key].filter((v) => v !== value) : [...prev[key], value],
      };
    });
  }

  if (!showForm) {
    return (
      <div className="space-y-6">
        <TemplateGallery
          templates={templates}
          onPick={startFromTemplate}
          onBlank={startFromBlank}
        />
        <AgentRoster
          agents={agents}
          tools={tools}
          sources={sources}
          isLoading={isLoading}
          onEdit={startEdit}
          onDelete={(id) => remove.mutate(id)}
          onMakeDefault={(id) => makeDefault.mutate(id)}
          makeDefaultPending={makeDefault.isPending}
        />
        <BindingsCard agents={agents} />
      </div>
    );
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
                  {/* Why this one is ticked. A pre-ticked source that scopes an
                      agent away from its data is the failure mode here, and an
                      admin can only catch it if the guess shows its working. */}
                  {hintedSources.has(c.id) && (
                    <Badge variant="outline" className="ml-2 font-normal">
                      matched <code className="ml-1">{hintedSources.get(c.id)}</code>
                    </Badge>
                  )}
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
          <Button variant="ghost" onClick={resetForm}>
            <X className="h-4 w-4 mr-1" />
            Cancel
          </Button>
        </CardFooter>
      </Card>

      <AgentRoster
        agents={agents}
        tools={tools}
        sources={sources}
        isLoading={isLoading}
        onEdit={startEdit}
        onDelete={(id) => remove.mutate(id)}
        onMakeDefault={(id) => makeDefault.mutate(id)}
        makeDefaultPending={makeDefault.isPending}
      />

      <BindingsCard agents={agents} />
    </div>
  );
}

/**
 * The gallery (T-B3): six starting points and a blank one.
 *
 * This is the ask stated plainly — a customer must be able to create a useful
 * agent without writing a prompt, and must still be able to write one from
 * scratch. So **Start from blank** is a card of the same size and weight as the
 * six, not a link underneath them: it is a supported way to create an agent,
 * and the difference between a settings page and a feature is that neither path
 * is the consolation prize.
 *
 * With no templates loaded there is nothing to choose between, so the blank
 * card stands alone and this screen is the one that existed before.
 */
function TemplateGallery({
  templates,
  onPick,
  onBlank,
}: {
  templates: AgentTemplate[];
  onPick: (t: AgentTemplate) => void;
  onBlank: () => void;
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Create an agent</CardTitle>
        <CardDescription>
          Start from a job this agent will do, or from nothing. Either way you get an ordinary
          agent you can edit — a template fills the form in and then gets out of the way.
        </CardDescription>
      </CardHeader>
      <CardContent className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {templates.map((t) => (
          <button
            key={t.key}
            type="button"
            onClick={() => onPick(t)}
            className="rounded-lg border border-border bg-card p-4 text-left transition-colors hover:border-primary/60 hover:bg-accent/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            <div className="flex items-center gap-2 text-sm font-medium">
              <Bot className="h-4 w-4 shrink-0 opacity-70" />
              {t.name}
            </div>
            <p className="mt-1.5 text-xs text-muted-foreground">{t.description}</p>
          </button>
        ))}
        {/* Same border, same padding, same card — only the icon differs. A
            dashed outline here read as the consolation prize, which is the one
            thing this card must not be. */}
        <button
          type="button"
          onClick={onBlank}
          className="rounded-lg border border-border bg-card p-4 text-left transition-colors hover:border-primary/60 hover:bg-accent/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          <div className="flex items-center gap-2 text-sm font-medium">
            <FilePlus2 className="h-4 w-4 shrink-0 opacity-70" />
            Start from blank
          </div>
          <p className="mt-1.5 text-xs text-muted-foreground">
            An empty form. Write the name, the instructions and the scope yourself.
          </p>
        </button>
      </CardContent>
    </Card>
  );
}

/** The roster list. Extracted unchanged when the gallery landed, because it
 *  now renders on both screens — the gallery and the form. */
function AgentRoster({
  agents,
  tools,
  sources,
  isLoading,
  onEdit,
  onDelete,
  onMakeDefault,
  makeDefaultPending,
}: {
  agents: Agent[];
  tools: AgentToolInfo[];
  sources: Connection[];
  isLoading: boolean;
  onEdit: (a: Agent) => void;
  onDelete: (id: string) => void;
  onMakeDefault: (id: string) => void;
  makeDefaultPending: boolean;
}) {
  return (
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
              {a.description && <div className="text-xs text-muted-foreground">{a.description}</div>}
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
                disabled={a.is_default || !a.enabled || makeDefaultPending}
                onClick={() => onMakeDefault(a.id)}
              >
                <Star className={a.is_default ? "h-4 w-4 fill-current" : "h-4 w-4"} />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                aria-label={`Edit ${a.name}`}
                onClick={() => onEdit(a)}
              >
                <Pencil className="h-4 w-4" />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                aria-label={`Delete ${a.name}`}
                onClick={() => {
                  if (confirm(`Delete ${a.name}? Threads that used it keep their history.`)) {
                    onDelete(a.id);
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
  );
}

/** What each channel calls the thing an admin has to paste, and where to find
 *  it. "external_id" is a database column; nobody has one of those. */
const CHANNEL_COPY: Record<string, { label: string; field: string; hint: string }> = {
  discord: {
    label: "Discord",
    field: "Channel id",
    hint: "Right-click the channel → Copy Channel ID (Developer Mode must be on).",
  },
  lark: {
    label: "Lark",
    field: "Chat id",
    hint: "The chat id from the Lark group, usually starting oc_.",
  },
  whatsapp: {
    label: "WhatsApp",
    field: "Phone number",
    hint: "The sender's number in E.164 form, e.g. +6281234567890.",
  },
};

function channelCopy(c: Channel | string) {
  return CHANNEL_COPY[c] ?? { label: String(c), field: "Identifier", hint: "" };
}

/** BindingsCard is T-S4: which agent answers in which Discord channel, Lark
 *  chat or WhatsApp number. Everything else in this tab configures an agent;
 *  this is the only thing that decides which one a message reaches. */
function BindingsCard({ agents }: { agents: Agent[] }) {
  const qc = useQueryClient();
  const { toast } = useToast();
  const [channel, setChannel] = useState<Channel | "">("");
  const [externalId, setExternalId] = useState("");
  const [agentId, setAgentId] = useState("");
  const [error, setError] = useState<string | null>(null);

  const { data, isLoading } = useQuery({
    queryKey: ["agent-bindings"],
    queryFn: async () => (await api.get<AgentBindingsResponse>("/agent-bindings")).data,
  });

  // The channel list comes from the API, like the tool checkboxes above: which
  // channels can be bound at all is the backend's decision.
  const channels: Channel[] = data?.channels ?? [];
  const bindings: AgentChannelBinding[] = (data?.bindings ?? []).filter(
    (b): b is AgentChannelBinding => !!b,
  );
  const copy = channel ? channelCopy(channel) : null;

  const create = useMutation({
    mutationFn: async () =>
      api.post("/agent-bindings", {
        channel,
        external_id: externalId.trim(),
        agent_id: agentId,
      }),
    onSuccess: () => {
      setExternalId("");
      setError(null);
      qc.invalidateQueries({ queryKey: ["agent-bindings"] });
    },
    onError: (e: unknown) => setError(apiErrorMessage(e, "Could not save that binding")),
  });

  const remove = useMutation({
    mutationFn: async (id: string) => api.delete(`/agent-bindings/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["agent-bindings"] }),
    onError: (e: unknown) =>
      toast({ title: "Nothing removed", description: apiErrorMessage(e), variant: "destructive" }),
  });

  return (
    <Card>
      <CardHeader>
        <CardTitle>Channel bindings</CardTitle>
        <CardDescription>
          Send a whole Discord channel, Lark chat or WhatsApp number to one agent. Anything not
          bound here is answered by the default agent, which is what every channel does today.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid grid-cols-[10rem_1fr_12rem_auto] items-end gap-3">
          <div className="space-y-1.5">
            <Label htmlFor="binding-channel">Channel</Label>
            <Select value={channel} onValueChange={(v) => setChannel(v as Channel)}>
              <SelectTrigger id="binding-channel">
                <SelectValue placeholder="Choose" />
              </SelectTrigger>
              <SelectContent>
                {channels.map((c) => (
                  <SelectItem key={c} value={c}>
                    {channelCopy(c).label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="binding-ref">{copy?.field ?? "Identifier"}</Label>
            <Input
              id="binding-ref"
              value={externalId}
              disabled={!channel}
              onChange={(e) => setExternalId(e.target.value)}
              placeholder={channel === "whatsapp" ? "+6281234567890" : "1234567890123456789"}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="binding-agent">Agent</Label>
            <Select value={agentId} onValueChange={setAgentId}>
              <SelectTrigger id="binding-agent">
                <SelectValue placeholder="Choose" />
              </SelectTrigger>
              <SelectContent>
                {agents
                  .filter((a) => a.enabled)
                  .map((a) => (
                    <SelectItem key={a.id} value={a.id}>
                      {a.name}
                    </SelectItem>
                  ))}
              </SelectContent>
            </Select>
          </div>
          <Button
            onClick={() => create.mutate()}
            disabled={!channel || !externalId.trim() || !agentId || create.isPending}
          >
            {create.isPending ? "Binding…" : "Bind"}
          </Button>
        </div>
        {copy?.hint && <p className="text-xs text-muted-foreground">{copy.hint}</p>}
        {error && <p className="text-sm text-destructive">{error}</p>}

        <div className="divide-y divide-border/50 border-t border-border/50">
          {isLoading && <div className="text-sm text-muted-foreground py-4">Loading…</div>}
          {!isLoading && bindings.length === 0 && (
            <div className="text-sm text-muted-foreground py-4">
              No bindings. Every channel answers as the default agent.
            </div>
          )}
          {bindings.map((b) => (
            <div key={b.id} className="flex items-center justify-between gap-4 py-3">
              <div className="min-w-0 space-y-1">
                <div className="text-sm font-medium flex items-center gap-2">
                  <Badge variant="outline">{channelCopy(b.channel).label}</Badge>
                  <code className="truncate text-xs">{b.external_id}</code>
                </div>
                <div className="text-xs text-muted-foreground">
                  Answered by {b.agent_name ?? "an agent"}
                </div>
              </div>
              <Button
                variant="ghost"
                size="icon"
                aria-label={`Remove the binding for ${b.external_id}`}
                onClick={() => remove.mutate(b.id)}
              >
                <Trash2 className="h-4 w-4" />
              </Button>
            </div>
          ))}
        </div>
      </CardContent>
    </Card>
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
