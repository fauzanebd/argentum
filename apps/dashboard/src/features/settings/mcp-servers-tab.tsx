import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, Plug, RefreshCw, Trash2, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
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
  MCPServer,
  MCPServerResponse,
  MCPServersResponse,
  MCPToolView,
  MCPTransport,
} from "@argentum/api-types";

/** Mirrors app.MCPServerInput. `auth_token` is three-valued there and here:
 *  absent leaves the stored token alone, a string replaces it, and
 *  `clear_auth` removes it — the form cannot show a credential back, so an
 *  empty field must never mean "delete it". */
interface ServerDraft {
  name: string;
  description: string;
  url: string;
  transport: MCPTransport;
  auth_token?: string;
  clear_auth?: boolean;
  enabled?: boolean;
}

const EMPTY_DRAFT: ServerDraft = {
  name: "",
  description: "",
  url: "",
  transport: "http",
  enabled: true,
};

const TRANSPORT_LABEL: Record<string, string> = {
  http: "Streamable HTTP",
  sse: "HTTP + SSE",
};

function probeSummary(s: MCPServer): string {
  if (s.probe_error) return s.probe_error;
  if (!s.last_probed_at) return "Not contacted yet";
  return `Last checked ${new Date(s.last_probed_at).toLocaleString()}`;
}

/**
 * Settings → MCP servers (T-M1).
 *
 * The tenant registers their own MCP server — their ticketing system, their
 * CRM — and reviews the tools it offers. Nothing here runs a tool: approving
 * one is what makes it callable at all, and calling it is T-M2.
 *
 * The review list is the point of this screen rather than a detail of it.
 * Approving a tool is approving the description that will enter the agent's
 * context, so the description and the argument schema are both shown, and a
 * server that rewrites either after approval is flagged rather than adopted.
 */
export function MCPServersTab() {
  const qc = useQueryClient();
  const { toast } = useToast();

  const [editing, setEditing] = useState<string | null>(null);
  const [draft, setDraft] = useState<ServerDraft>(EMPTY_DRAFT);
  const [showForm, setShowForm] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Which server's tool list is open. One at a time: a page of six servers'
  // schemas is a page nobody reads to the end, and this is a screen where
  // reading to the end is the job.
  const [openServer, setOpenServer] = useState<string | null>(null);

  const { data, isLoading } = useQuery({
    queryKey: ["mcp-servers"],
    queryFn: async () => (await api.get<MCPServersResponse>("/mcp-servers")).data,
  });

  const servers: MCPServer[] = (data?.servers ?? []).filter((s): s is MCPServer => !!s);
  // Which transports this release speaks is the backend's fact, like the tool
  // vocabulary on the Agents tab. A hardcoded array here is the one somebody
  // eventually adds "stdio" to.
  const transports: MCPTransport[] = data?.transports ?? ["http", "sse"];
  // Whether this deployment accepts a plaintext http URL. The rule on screen has
  // to be the rule the save applies — "must be https" printed beside a backend
  // that accepts http is a sentence that costs an admin a support ticket.
  const allowsHTTP = data?.allows_insecure_http ?? false;

  function resetForm() {
    setEditing(null);
    setDraft(EMPTY_DRAFT);
    setShowForm(false);
    setError(null);
  }

  function startAdd() {
    setEditing(null);
    setDraft(EMPTY_DRAFT);
    setShowForm(true);
    setError(null);
  }

  function startEdit(s: MCPServer) {
    setEditing(s.id);
    setDraft({
      name: s.name,
      description: s.description,
      url: s.url,
      transport: s.transport,
      enabled: s.enabled,
    });
    setShowForm(true);
    setError(null);
  }

  const save = useMutation({
    mutationFn: async () => {
      const body: ServerDraft = { ...draft, name: draft.name.trim(), url: draft.url.trim() };
      // An empty token field on an edit is "unchanged", so it is not sent at
      // all. On a create there is nothing stored to preserve, and an empty
      // string would be a token of length zero.
      if (!body.auth_token) delete body.auth_token;
      if (editing) return (await api.put<MCPServerResponse>(`/mcp-servers/${editing}`, body)).data;
      return (await api.post<MCPServerResponse>("/mcp-servers", body)).data;
    },
    onSuccess: (res) => {
      resetForm();
      setOpenServer(res.server?.id ?? null);
      qc.invalidateQueries({ queryKey: ["mcp-servers"] });
      if (res.server?.probe_error) {
        // Saved, and it could not be reached. Both halves are true and the
        // second one is the one an admin needs to see now.
        toast({
          title: "Saved, but the server did not answer",
          description: res.server.probe_error,
          variant: "destructive",
        });
      }
    },
    onError: (e: unknown) => setError(apiErrorMessage(e, "Could not save that MCP server")),
  });

  const remove = useMutation({
    mutationFn: async (id: string) => api.delete(`/mcp-servers/${id}`),
    onSuccess: (_r, id) => {
      if (editing === id) resetForm();
      if (openServer === id) setOpenServer(null);
      qc.invalidateQueries({ queryKey: ["mcp-servers"] });
    },
    onError: (e: unknown) =>
      toast({ title: "Nothing removed", description: apiErrorMessage(e), variant: "destructive" }),
  });

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>MCP servers</CardTitle>
          <CardDescription>
            Connect a server that speaks the Model Context Protocol — your ticketing system, your
            CRM, an internal API — and your agents can call the tools you approve on it. Argentum
            reaches it with the token you provide; nothing on it is callable until an admin has read
            what it does and approved it.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {/* Said where it is decided rather than discovered. A tool's
              description is text written by whoever runs that server, and once
              approved it is read by the agent on every turn. */}
          <div className="rounded-md border border-border bg-muted/40 p-3 text-xs text-muted-foreground">
            <span className="font-medium text-foreground">
              Approving a tool approves its description.
            </span>{" "}
            The text below each tool is written by the server, not by Argentum, and your agents read
            it when they decide what to call. Approve tools from servers you run or trust, and read
            what they say they do. Tools that change their description after approval are flagged
            here rather than being adopted.
          </div>

          {isLoading && <div className="text-sm text-muted-foreground py-2">Loading…</div>}
          {!isLoading && servers.length === 0 && (
            <div className="text-sm text-muted-foreground py-2">
              No MCP servers connected. Your agents use the built-in tools only.
            </div>
          )}

          <div className="divide-y divide-border/50">
            {servers.map((s) => (
              <ServerRow
                key={s.id}
                server={s}
                open={openServer === s.id}
                onToggle={() => setOpenServer(openServer === s.id ? null : s.id)}
                onEdit={() => startEdit(s)}
                onDelete={() => {
                  if (confirm(`Remove ${s.name}? Agents lose access to its tools.`)) {
                    remove.mutate(s.id);
                  }
                }}
              />
            ))}
          </div>
        </CardContent>
        {!showForm && (
          <CardFooter>
            <Button onClick={startAdd}>
              <Plug className="mr-1 h-4 w-4" />
              Connect a server
            </Button>
          </CardFooter>
        )}
      </Card>

      {showForm && (
        <Card>
          <CardHeader>
            <CardTitle>{editing ? "Edit MCP server" : "Connect an MCP server"}</CardTitle>
            <CardDescription>
              {allowsHTTP
                ? "The URL may be http or https, and must resolve to a public address — a server on a private network, on localhost, or at a cloud metadata address is refused, because Argentum would be reaching it from inside its own network rather than yours. Plaintext http sends your token and the tool results unencrypted."
                : "The URL must be https and must resolve to a public address — a server on a private network, on localhost, or at a cloud metadata address is refused, because Argentum would be reaching it from inside its own network rather than yours."}
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="grid grid-cols-[1fr_1.4fr] gap-4">
              <div className="space-y-1.5">
                <Label htmlFor="mcp-name">Name</Label>
                <Input
                  id="mcp-name"
                  value={draft.name}
                  onChange={(e) => setDraft({ ...draft, name: e.target.value })}
                  placeholder="Helpdesk"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="mcp-description">Description</Label>
                <Input
                  id="mcp-description"
                  value={draft.description}
                  onChange={(e) => setDraft({ ...draft, description: e.target.value })}
                  placeholder="Our ticketing system"
                />
              </div>
            </div>

            <div className="grid grid-cols-[1fr_12rem] gap-4">
              <div className="space-y-1.5">
                <Label htmlFor="mcp-url">Server URL</Label>
                <Input
                  id="mcp-url"
                  value={draft.url}
                  onChange={(e) => setDraft({ ...draft, url: e.target.value })}
                  placeholder={allowsHTTP ? "http://mcp.example.com/v1" : "https://mcp.example.com/v1"}
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="mcp-transport">Transport</Label>
                <Select
                  value={draft.transport}
                  onValueChange={(v) => setDraft({ ...draft, transport: v as MCPTransport })}
                >
                  <SelectTrigger id="mcp-transport">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {transports.map((t) => (
                      <SelectItem key={t} value={t}>
                        {TRANSPORT_LABEL[t] ?? t}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="mcp-token">Bearer token</Label>
              <Input
                id="mcp-token"
                type="password"
                autoComplete="off"
                value={draft.auth_token ?? ""}
                onChange={(e) => setDraft({ ...draft, auth_token: e.target.value })}
                placeholder={editing ? "Leave blank to keep the stored token" : "Optional"}
              />
              <p className="text-xs text-muted-foreground">
                Stored encrypted and never shown again. Leave it blank if the server needs no
                credential.
                {editing && (
                  <>
                    {" "}
                    <button
                      type="button"
                      className="underline underline-offset-2 hover:text-foreground"
                      onClick={() => setDraft({ ...draft, clear_auth: true, auth_token: "" })}
                    >
                      Remove the stored token
                    </button>
                    {draft.clear_auth && <span className="ml-1">— will be removed on save.</span>}
                  </>
                )}
              </p>
            </div>

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
            <Button
              onClick={() => save.mutate()}
              disabled={!draft.name.trim() || !draft.url.trim() || save.isPending}
            >
              {save.isPending ? "Contacting the server…" : editing ? "Save changes" : "Connect"}
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

/** One server in the list, with its tool review folded underneath. */
function ServerRow({
  server,
  open,
  onToggle,
  onEdit,
  onDelete,
}: {
  server: MCPServer;
  open: boolean;
  onToggle: () => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const qc = useQueryClient();
  const { toast } = useToast();

  const { data } = useQuery({
    queryKey: ["mcp-server", server.id],
    queryFn: async () => (await api.get<MCPServerResponse>(`/mcp-servers/${server.id}`)).data,
    enabled: open,
  });
  const tools: MCPToolView[] = (data?.tools ?? []).filter((t): t is MCPToolView => !!t);
  const approved = tools.filter((t) => t.approved).length;
  const drifted = tools.filter((t) => t.drifted).length;

  const refresh = useMutation({
    mutationFn: async () =>
      (await api.post<MCPServerResponse>(`/mcp-servers/${server.id}/refresh`)).data,
    onSuccess: (res) => {
      qc.setQueryData(["mcp-server", server.id], res);
      qc.invalidateQueries({ queryKey: ["mcp-servers"] });
      if (res.server?.probe_error) {
        toast({
          title: "The server did not answer",
          description: res.server.probe_error,
          variant: "destructive",
        });
      }
    },
    onError: (e: unknown) =>
      toast({ title: "Refresh failed", description: apiErrorMessage(e), variant: "destructive" }),
  });

  const review = useMutation({
    mutationFn: async (v: { id: string; approved: boolean; read_only: boolean }) =>
      (
        await api.put<{ tools: MCPToolView[] }>(`/mcp-servers/${server.id}/tools/${v.id}`, {
          approved: v.approved,
          read_only: v.read_only,
        })
      ).data,
    onSuccess: (res) => {
      qc.setQueryData(["mcp-server", server.id], (prev: MCPServerResponse | undefined) =>
        prev ? { ...prev, tools: res.tools } : prev,
      );
    },
    onError: (e: unknown) =>
      toast({ title: "Not saved", description: apiErrorMessage(e), variant: "destructive" }),
  });

  return (
    <div className="py-3 space-y-2">
      <div className="flex items-start justify-between gap-4">
        <button type="button" className="min-w-0 space-y-1 text-left" onClick={onToggle}>
          <div className="text-sm font-medium flex items-center gap-2">
            <span className="truncate">{server.name}</span>
            <Badge variant="outline">{TRANSPORT_LABEL[server.transport] ?? server.transport}</Badge>
            {!server.enabled && <Badge variant="outline">Disabled</Badge>}
            {server.has_auth && <Badge variant="secondary">Token set</Badge>}
            {server.probe_error && <Badge variant="destructive">Unreachable</Badge>}
          </div>
          {server.description && (
            <div className="text-xs text-muted-foreground">{server.description}</div>
          )}
          <div className="text-xs text-muted-foreground">
            <code>{server.url}</code>
          </div>
          <div className="text-xs text-muted-foreground">{probeSummary(server)}</div>
        </button>
        <div className="flex shrink-0 items-center gap-1">
          <Button
            variant="ghost"
            size="icon"
            aria-label={`Refresh ${server.name}`}
            disabled={refresh.isPending}
            onClick={() => refresh.mutate()}
          >
            <RefreshCw className={refresh.isPending ? "h-4 w-4 animate-spin" : "h-4 w-4"} />
          </Button>
          <Button variant="ghost" size="sm" onClick={onEdit}>
            Edit
          </Button>
          <Button
            variant="ghost"
            size="icon"
            aria-label={`Remove ${server.name}`}
            onClick={onDelete}
          >
            <Trash2 className="h-4 w-4" />
          </Button>
        </div>
      </div>

      {open && (
        <div className="rounded-md border border-border bg-muted/20 p-3 space-y-3">
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <span>
              {tools.length === 0
                ? "No tools discovered."
                : `${approved} of ${tools.length} tools approved`}
            </span>
            {drifted > 0 && (
              <Badge variant="destructive" className="gap-1">
                <AlertTriangle className="h-3 w-3" />
                {drifted} changed since approval
              </Badge>
            )}
          </div>

          {tools.map((t) => (
            <div key={t.id} className="rounded border border-border/60 bg-background p-3 space-y-2">
              <div className="flex items-center justify-between gap-3">
                <code className="text-sm">{t.tool_name}</code>
                <div className="flex items-center gap-3 text-xs">
                  {/* Read-only is the admin's classification, not the
                      server's claim — so it is a box a human ticks, and it is
                      what T-M2 reads before it lets an agent call anything. */}
                  <label className="flex items-center gap-1.5">
                    <input
                      type="checkbox"
                      checked={t.read_only}
                      onChange={(e) =>
                        review.mutate({
                          id: t.id,
                          approved: t.approved,
                          read_only: e.target.checked,
                        })
                      }
                    />
                    Read-only
                  </label>
                  <label className="flex items-center gap-1.5">
                    <input
                      type="checkbox"
                      checked={t.approved}
                      onChange={(e) =>
                        review.mutate({
                          id: t.id,
                          approved: e.target.checked,
                          read_only: t.read_only,
                        })
                      }
                    />
                    Approved
                  </label>
                </div>
              </div>
              {t.drifted && (
                <p className="text-xs text-destructive">
                  This tool's description or arguments changed after you approved it. Read it again
                  and re-approve.
                </p>
              )}
              <p className="text-xs text-muted-foreground whitespace-pre-wrap">
                {t.description || "The server gave no description."}
              </p>
              <details className="text-xs text-muted-foreground">
                <summary className="cursor-pointer">Arguments</summary>
                <pre className="mt-1 overflow-x-auto rounded bg-muted/60 p-2">
                  {JSON.stringify(t.input_schema, null, 2)}
                </pre>
              </details>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
