import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { BookText, Eye, Sparkles, Trash2, X } from "lucide-react";
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
import { cn } from "@/lib/utils";
import type {
  ConversationThread,
  Skill,
  SkillDraft,
  SkillPreview,
  SkillsResponse,
} from "@argentum/api-types";

/** Mirrors handlers.skillReq. `enabled` is a boolean here rather than the
 *  backend's pointer because the form always knows which state it is in — the
 *  pointer exists for an API caller who omits the field. */
interface SkillFormDraft {
  name: string;
  when_to_use: string;
  body: string;
  enabled: boolean;
}

const EMPTY_DRAFT: SkillFormDraft = { name: "", when_to_use: "", body: "", enabled: true };

/** How long the form waits before asking the server to render the preview.
 *
 *  The preview is a round trip per keystroke otherwise, and what it shows —
 *  the index line and the framed body — does not change meaningfully between
 *  two characters of the same word. The counters below the fields are local and
 *  update immediately, because those are what somebody typing near a cap is
 *  watching. */
const PREVIEW_DEBOUNCE_MS = 400;

/** Counted in code units rather than code points would be the wrong number:
 *  the backend counts runes, and a form that disagrees with the save about
 *  what "60 characters" means is a form that refuses at 59 or accepts at 61. */
function runeCount(s: string): number {
  return [...s].length;
}

function CharCounter({ value, max }: { value: string; max: number }) {
  const n = runeCount(value);
  return (
    <span
      className={cn(
        "text-xs tabular-nums",
        n > max ? "text-destructive font-medium" : "text-muted-foreground",
      )}
    >
      {n} / {max}
    </span>
  );
}

/**
 * Settings → Skills (T-K6).
 *
 * A skill is a procedure this workspace wrote down: how a period-over-period
 * comparison is done here, which rows a revenue figure excludes, what a
 * recurring report has to contain. The agent is shown one line about each on
 * every turn and opens the steps only on the turns where one applies.
 *
 * **The two preview panes are the point of this screen rather than a detail of
 * it.** What a tenant is writing is a prompt, and the most useful thing a
 * prompt author can be shown is the bytes — the line that rides every turn, and
 * the framed block `load_skill` returns. Both come from the server, because a
 * form that assembled them itself would be a second implementation of the two
 * things this feature is, and the day it drifted it would be reassuring
 * somebody about text nobody sends.
 *
 * The counters are live for a related reason: the backend refuses rather than
 * truncates, and a form that discovers a cap on submit is a form that loses a
 * paragraph.
 */
export function SkillsTab() {
  const qc = useQueryClient();
  const { toast } = useToast();

  const [editing, setEditing] = useState<string | null>(null);
  const [draft, setDraft] = useState<SkillFormDraft>(EMPTY_DRAFT);
  const [showForm, setShowForm] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [draftSource, setDraftSource] = useState<SkillDraft | null>(null);
  const [threadID, setThreadID] = useState<string>("");

  const { data, isLoading } = useQuery({
    queryKey: ["skills"],
    queryFn: async () => (await api.get<SkillsResponse>("/skills")).data,
  });

  const skills: Skill[] = (data?.skills ?? []).filter((s): s is Skill => !!s);
  // The caps are the server's numbers. A hard-coded 60 here is the copy that
  // disagrees with the CHECK constraint after somebody widens it.
  const limits = data?.limits ?? {
    name_chars: 60,
    when_to_use_chars: 200,
    body_chars: 8000,
    per_company: 200,
  };
  const index = data?.index;

  // Recent conversations, for "draft from a conversation". Same query key the
  // chat surfaces use, so React Query serves one cached list rather than a
  // second fetch of the same rows.
  const { data: threads } = useQuery({
    queryKey: ["threads"],
    queryFn: async () => (await api.get<{ threads: ConversationThread[] }>("/threads")).data.threads,
    enabled: showForm,
  });

  // The preview, debounced. Keyed on the three fields so React Query caches a
  // draft somebody scrolls back to, and disabled until there is something to
  // render — an empty form previewing an empty frame is noise.
  const [previewKey, setPreviewKey] = useState<SkillFormDraft>(EMPTY_DRAFT);
  useEffect(() => {
    const t = setTimeout(() => setPreviewKey(draft), PREVIEW_DEBOUNCE_MS);
    return () => clearTimeout(t);
  }, [draft]);

  const hasContent = previewKey.name.trim() !== "" || previewKey.body.trim() !== "";
  const { data: preview } = useQuery({
    queryKey: ["skill-preview", previewKey.name, previewKey.when_to_use, previewKey.body],
    queryFn: async () =>
      (
        await api.post<SkillPreview>("/skills/preview", {
          name: previewKey.name,
          when_to_use: previewKey.when_to_use,
          body: previewKey.body,
        })
      ).data,
    enabled: showForm && hasContent,
  });

  function resetForm() {
    setEditing(null);
    setDraft(EMPTY_DRAFT);
    setShowForm(false);
    setError(null);
    setDraftSource(null);
  }

  function startAdd() {
    setEditing(null);
    setDraft(EMPTY_DRAFT);
    setShowForm(true);
    setError(null);
    setDraftSource(null);
  }

  function startEdit(s: Skill) {
    setEditing(s.id);
    setDraft({
      name: s.name,
      when_to_use: s.when_to_use,
      body: s.body,
      enabled: s.enabled,
    });
    setShowForm(true);
    setError(null);
    setDraftSource(null);
  }

  const save = useMutation({
    mutationFn: async () => {
      const body = {
        name: draft.name.trim(),
        when_to_use: draft.when_to_use.trim(),
        body: draft.body.trim(),
        enabled: draft.enabled,
      };
      if (editing) return (await api.put<Skill>(`/skills/${editing}`, body)).data;
      return (await api.post<Skill>("/skills", body)).data;
    },
    onSuccess: () => {
      resetForm();
      qc.invalidateQueries({ queryKey: ["skills"] });
    },
    onError: (e: unknown) => setError(apiErrorMessage(e, "Could not save that procedure")),
  });

  // Enable/disable is a save of the whole row rather than its own route,
  // because the backend has one update path and off is a first-class state on
  // it — a procedure being revised stops being offered without being deleted.
  const toggle = useMutation({
    mutationFn: async (s: Skill) =>
      api.put(`/skills/${s.id}`, {
        name: s.name,
        when_to_use: s.when_to_use,
        body: s.body,
        enabled: !s.enabled,
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["skills"] }),
    onError: (e: unknown) =>
      toast({ title: "Nothing changed", description: apiErrorMessage(e), variant: "destructive" }),
  });

  const remove = useMutation({
    mutationFn: async (id: string) => api.delete(`/skills/${id}`),
    onSuccess: (_r, id) => {
      if (editing === id) resetForm();
      qc.invalidateQueries({ queryKey: ["skills"] });
    },
    onError: (e: unknown) =>
      toast({ title: "Nothing removed", description: apiErrorMessage(e), variant: "destructive" }),
  });

  // "Draft from a conversation" (T-K7). What comes back lands in the fields,
  // editable, and is saved by the button below like anything else somebody
  // typed — pressing this writes nothing.
  const drafting = useMutation({
    mutationFn: async () =>
      (await api.post<SkillDraft>("/skills/draft", { thread_id: threadID })).data,
    onSuccess: (d) => {
      setDraft({ name: d.name, when_to_use: d.when_to_use, body: d.body, enabled: true });
      setDraftSource(d);
      setError(null);
    },
    onError: (e: unknown) => setError(apiErrorMessage(e, "Could not draft from that conversation")),
  });

  const overCap =
    runeCount(draft.name) > limits.name_chars ||
    runeCount(draft.when_to_use) > limits.when_to_use_chars ||
    runeCount(draft.body) > limits.body_chars;

  const canSave =
    draft.name.trim() !== "" &&
    draft.when_to_use.trim() !== "" &&
    draft.body.trim() !== "" &&
    !overCap &&
    !save.isPending;

  const sorted = useMemo(
    () => [...skills].sort((a, b) => a.name.localeCompare(b.name)),
    [skills],
  );

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Procedures</CardTitle>
          <CardDescription>
            Write down how your business does a thing — which rows a revenue figure excludes, how a
            month is closed, what your weekly report has to contain — and say when it applies. Your
            agents see one line about each procedure on every question, and open the steps only when
            one fits.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {/* Said where it is decided rather than discovered. A skill body
              reaches the model as an instruction from this workspace, unlike a
              document or a database row, and an admin writing one should know
              that is what they are doing. */}
          <div className="rounded-md border border-border bg-muted/40 p-3 text-xs text-muted-foreground">
            <span className="font-medium text-foreground">
              A procedure is an instruction, not a document.
            </span>{" "}
            Your agents follow what you write here as if you had said it to them. That is different
            from an uploaded file or a database row, which they read as information and never as an
            order. Only administrators can write one.
          </div>

          <IndexCostNotice index={index} count={skills.length} limit={limits.per_company} />

          {isLoading && <div className="py-2 text-sm text-muted-foreground">Loading…</div>}
          {!isLoading && sorted.length === 0 && (
            <div className="py-2 text-sm text-muted-foreground">
              No procedures yet. Your agents already follow the two Argentum ships — how to answer a
              period-over-period comparison, and how to structure a recurring report — and anything
              you add here is about your business rather than about method.
            </div>
          )}

          <div className="divide-y divide-border">
            {sorted.map((s) => (
              <div key={s.id} className="flex items-start gap-3 py-3">
                <BookText className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="truncate font-medium">{s.name}</span>
                    {!s.enabled && (
                      <Badge variant="outline" className="font-normal">
                        off
                      </Badge>
                    )}
                  </div>
                  <p className="text-sm text-muted-foreground">{s.when_to_use}</p>
                </div>
                <div className="flex shrink-0 items-center gap-1">
                  <Button variant="ghost" size="sm" onClick={() => toggle.mutate(s)}>
                    {s.enabled ? "Turn off" : "Turn on"}
                  </Button>
                  <Button variant="ghost" size="sm" onClick={() => startEdit(s)}>
                    Edit
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      if (
                        confirm(
                          `Delete “${s.name}”? Agents stop being offered it, and any agent bound only to it falls back to every enabled procedure.`,
                        )
                      ) {
                        remove.mutate(s.id);
                      }
                    }}
                  >
                    <Trash2 className="h-4 w-4" />
                  </Button>
                </div>
              </div>
            ))}
          </div>
        </CardContent>
        {!showForm && (
          <CardFooter>
            <Button onClick={startAdd}>Write a procedure</Button>
          </CardFooter>
        )}
      </Card>

      {showForm && (
        <Card>
          <CardHeader>
            <CardTitle>{editing ? "Edit procedure" : "New procedure"}</CardTitle>
            <CardDescription>
              The name and the trigger sentence travel in every question your agents answer. The
              steps do not — they are fetched only when the agent decides the procedure applies.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <DraftFromConversation
              threads={threads ?? []}
              threadID={threadID}
              onPick={setThreadID}
              onDraft={() => drafting.mutate()}
              pending={drafting.isPending}
              source={draftSource}
            />

            <div className="space-y-1.5">
              <div className="flex items-center justify-between">
                <Label htmlFor="skill-name">Name</Label>
                <CharCounter value={draft.name} max={limits.name_chars} />
              </div>
              <Input
                id="skill-name"
                value={draft.name}
                onChange={(e) => setDraft({ ...draft, name: e.target.value })}
                placeholder="Weekly revenue by branch"
              />
              <p className="text-xs text-muted-foreground">
                What the agent calls this procedure. It is capped tightly because it rides in every
                question, beside the sentence below.
              </p>
            </div>

            <div className="space-y-1.5">
              <div className="flex items-center justify-between">
                <Label htmlFor="skill-when">When to use it</Label>
                <CharCounter value={draft.when_to_use} max={limits.when_to_use_chars} />
              </div>
              <Input
                id="skill-when"
                value={draft.when_to_use}
                onChange={(e) => setDraft({ ...draft, when_to_use: e.target.value })}
                placeholder="When someone asks for revenue broken down by branch for a period."
              />
              <p className="text-xs text-muted-foreground">
                The only thing the agent reads before deciding to open this. Describe the shape of
                the question, not one particular question — a sentence that never matches is a
                procedure that is never used, and nothing will tell you.
              </p>
            </div>

            <div className="space-y-1.5">
              <div className="flex items-center justify-between">
                <Label htmlFor="skill-body">The procedure</Label>
                <CharCounter value={draft.body} max={limits.body_chars} />
              </div>
              <Textarea
                id="skill-body"
                rows={12}
                value={draft.body}
                onChange={(e) => setDraft({ ...draft, body: e.target.value })}
                placeholder={"1. …\n2. …\n3. …"}
                className="font-mono text-sm"
              />
              <p className="text-xs text-muted-foreground">
                Numbered steps, in order. Name the real tables and filters, and write down what is
                excluded — that is the part a reader cannot work out for themselves.
              </p>
            </div>

            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={draft.enabled}
                onChange={(e) => setDraft({ ...draft, enabled: e.target.checked })}
              />
              Enabled
            </label>

            <PreviewPanes preview={preview} visible={hasContent} />

            {error && <p className="text-sm text-destructive">{error}</p>}
          </CardContent>
          <CardFooter className="gap-2">
            <Button onClick={() => save.mutate()} disabled={!canSave}>
              {save.isPending ? "Saving…" : editing ? "Save changes" : "Save procedure"}
            </Button>
            <Button variant="ghost" onClick={resetForm}>
              <X className="mr-1 h-4 w-4" />
              Cancel
            </Button>
          </CardFooter>
        </Card>
      )}
    </div>
  );
}

/**
 * What this workspace's procedures cost every question, and — the part that
 * matters — what did not fit.
 *
 * The index is bounded twice and whichever bound binds first binds. Over it,
 * procedures are simply not offered; the agent ranks by relevance to the
 * question so the most useful ones survive, but some are lost on every turn and
 * nothing else on this screen would say so.
 */
function IndexCostNotice({
  index,
  count,
  limit,
}: {
  index: SkillsResponse["index"];
  count: number;
  limit: number;
}) {
  if (!index) return null;
  const dropped = index.dropped ?? [];
  if (dropped.length === 0) {
    if (index.lines === 0) return null;
    return (
      <p className="text-xs text-muted-foreground">
        {index.lines} {index.lines === 1 ? "procedure is" : "procedures are"} offered on every
        question, costing {index.chars.toLocaleString()} of {index.max_chars.toLocaleString()}{" "}
        characters. {count} of {limit} procedures written.
      </p>
    );
  }
  return (
    <div className="rounded-md border border-amber-300 bg-amber-50 p-3 text-xs text-amber-900 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-200">
      <span className="font-medium">
        {dropped.length} {dropped.length === 1 ? "procedure is" : "procedures are"} over the limit
        and not offered to your agents.
      </span>{" "}
      The index carries {index.lines} of them, at {index.chars.toLocaleString()} of{" "}
      {index.max_chars.toLocaleString()} characters. Your agents pick the ones closest to each
      question, so which are lost varies — but these were left out just now:{" "}
      {dropped.join(", ")}. Turn one off or merge two to get back under the limit.
    </div>
  );
}

/**
 * The two panes: the bytes.
 *
 * Rendered from the server's response rather than assembled here, which is the
 * whole reason the endpoint exists. `refusal` is the sentence the save would
 * answer with, shown here so a form and an API never word the same rule twice.
 */
function PreviewPanes({ preview, visible }: { preview?: SkillPreview; visible: boolean }) {
  if (!visible || !preview) return null;
  return (
    <div className="space-y-3 rounded-md border border-border bg-muted/30 p-3">
      <div className="flex items-center gap-2 text-xs font-medium text-foreground">
        <Eye className="h-3.5 w-3.5" />
        What your agents will see
      </div>

      <div className="space-y-1">
        <p className="text-xs text-muted-foreground">
          In every question — this line, and nothing else:
        </p>
        <pre className="overflow-x-auto whitespace-pre-wrap break-words rounded bg-background p-2 font-mono text-xs">
          {preview.index_line}
        </pre>
        <p className="text-[11px] text-muted-foreground">
          {preview.index_line_chars} characters on every turn.
        </p>
      </div>

      <div className="space-y-1">
        <p className="text-xs text-muted-foreground">
          Only when the agent decides this applies — the steps, marked as an instruction from this
          workspace:
        </p>
        <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words rounded bg-background p-2 font-mono text-xs">
          {preview.framed_body}
        </pre>
      </div>

      {preview.refusal && (
        <p className="text-xs font-medium text-destructive">{preview.refusal}</p>
      )}
    </div>
  );
}

/**
 * "Draft from a conversation" (T-K7).
 *
 * The draft lands in the fields above, editable. Nothing is written until
 * somebody presses Save — which is not a UI nicety but the trust boundary
 * itself: a conversation contains whatever your data and documents contained,
 * and a procedure is text the agent follows as an instruction. The save is
 * where a human takes responsibility for that.
 */
function DraftFromConversation({
  threads,
  threadID,
  onPick,
  onDraft,
  pending,
  source,
}: {
  threads: ConversationThread[];
  threadID: string;
  onPick: (id: string) => void;
  onDraft: () => void;
  pending: boolean;
  source: SkillDraft | null;
}) {
  const recent = threads.filter((t) => !t.is_archived).slice(0, 25);
  return (
    <div className="space-y-2 rounded-md border border-dashed border-border p-3">
      <div className="flex items-center gap-2 text-xs font-medium">
        <Sparkles className="h-3.5 w-3.5" />
        Start from a conversation
      </div>
      <p className="text-xs text-muted-foreground">
        Pick a conversation where this work was already done well, and Argentum will write a first
        draft from what was asked and what the agent actually ran. Read it before you save — it is a
        suggestion, and nothing is saved until you press the button below.
      </p>
      <div className="flex flex-wrap gap-2">
        <Select value={threadID} onValueChange={onPick}>
          <SelectTrigger className="w-full sm:w-[28rem]">
            <SelectValue placeholder="Choose a conversation…" />
          </SelectTrigger>
          <SelectContent>
            {recent.length === 0 && (
              <SelectItem value="none" disabled>
                No conversations yet
              </SelectItem>
            )}
            {recent.map((t) => (
              <SelectItem key={t.id} value={t.id}>
                {t.title || "Untitled conversation"}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Button variant="outline" onClick={onDraft} disabled={!threadID || pending}>
          {pending ? "Drafting…" : "Draft it"}
        </Button>
      </div>
      {source && (
        <p className="text-xs text-muted-foreground">
          Drafted from {source.messages} {source.messages === 1 ? "message" : "messages"}
          {source.tool_calls > 0
            ? ` and ${source.tool_calls} tool ${source.tool_calls === 1 ? "call" : "calls"}`
            : " and no tool calls, so it could not name any real tables"}
          . Edit anything below before saving.
        </p>
      )}
    </div>
  );
}
