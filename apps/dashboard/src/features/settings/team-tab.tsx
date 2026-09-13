import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Copy, Check, ChevronDown, Trash2 } from "lucide-react";
import type { Agent, AgentsResponse } from "@argentum/api-types";
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
import { useAuthStore } from "@/store/auth";
import { useToast } from "@/hooks/use-toast";
import { cn } from "@/lib/utils";
import type { AccessResource } from "./access";
import { PersonAccessPanel } from "./person-access-panel";
import { ResourceAccessCard } from "./resource-access-card";
import { useResourceAccess } from "./use-access";

type Role = "admin" | "member";
type Status = "active" | "pending" | "deactivated";

interface Member {
  id: string;
  email: string;
  role: Role;
  status: Status;
  created_at: string;
  invite_sent_at?: string;
  invite_expires_at?: string;
}

interface InviteResponse {
  user: Member;
  token: string;
  expires_at: string;
  /** Whether the invitation was actually emailed (T-F6). False covers three
   *  situations an admin cannot tell apart and does not need to — no relay,
   *  no APP_BASE_URL, or a send that failed — and the answer to all three is
   *  the same: copy the link. */
  emailed?: boolean;
}

/** The link an invitee opens. Built here because the API has no idea what
 *  origin the dashboard is served from. */
function inviteURL(token: string): string {
  return `${window.location.origin}/accept-invite?token=${encodeURIComponent(token)}`;
}

function statusBadge(status: Status) {
  switch (status) {
    case "pending":
      return <Badge variant="outline">Invitation pending</Badge>;
    case "deactivated":
      return <Badge variant="secondary">Removed</Badge>;
    default:
      return null;
  }
}

export function TeamTab() {
  const qc = useQueryClient();
  const { toast } = useToast();
  const me = useAuthStore((s) => s.user);

  const [email, setEmail] = useState("");
  const [role, setRole] = useState<Role>("member");
  const [error, setError] = useState<string | null>(null);
  // The plaintext token comes back once and is never readable again, so it
  // stays on screen until the admin dismisses it rather than living in a toast
  // that disappears after four seconds.
  const [issued, setIssued] = useState<InviteResponse | null>(null);
  const [copied, setCopied] = useState(false);
  // Whose access panel is open (T-Z7). One at a time: each panel reads that
  // person's capabilities, and a page of forty open panels is forty requests
  // nobody asked to see.
  const [openAccess, setOpenAccess] = useState<string | null>(null);

  const { data: members, isLoading } = useQuery({
    queryKey: ["team"],
    queryFn: async () => (await api.get<{ users: Member[] }>("/users")).data.users ?? [],
  });

  // The roster, under the key Settings → Agents and the chat already share. An
  // admin is sent every agent here — restricted ones included — which is what
  // the access matrix needs: an agent the admin may not talk to is still one
  // they manage (T-Z4).
  const { data: agentsData } = useQuery({
    queryKey: ["agents"],
    queryFn: async () => (await api.get<AgentsResponse>("/agents")).data,
  });
  const agents: AccessResource[] = (agentsData?.agents ?? [])
    .filter((a): a is Agent => !!a)
    .map((a) => ({ id: a.id, name: a.name, disabled: !a.enabled }));

  // Dashboards by the name the access list carries, **not** from
  // `GET /api/dashboards` (T-Z5). That list is narrowed by grant for an admin
  // too, so a dashboard the admin restricted and is not granted would be missing
  // from it — and so from the one screen where they could open it again.
  const dashboards: AccessResource[] = useResourceAccess("dashboard").resources.map((v) => ({
    id: v.resource_id,
    name: v.name,
  }));
  // Sources and documents by the access list's names too (T-Z6). The document
  // list is narrowed by grant for an admin, like the dashboards list, and the
  // access read is the one both halves of the matrix already draw from.
  const connections: AccessResource[] = useResourceAccess("connection").resources.map((v) => ({
    id: v.resource_id,
    name: v.name,
  }));
  const documents: AccessResource[] = useResourceAccess("document").resources.map((v) => ({
    id: v.resource_id,
    name: v.name,
  }));

  const invite = useMutation({
    mutationFn: async () =>
      (await api.post<InviteResponse>("/users/invite", { email: email.trim(), role })).data,
    onSuccess: (res) => {
      setIssued(res);
      setCopied(false);
      setEmail("");
      setError(null);
      qc.invalidateQueries({ queryKey: ["team"] });
    },
    onError: (e: unknown) => setError(apiErrorMessage(e, "Could not send that invitation")),
  });

  const changeRole = useMutation({
    mutationFn: async ({ id, next }: { id: string; next: Role }) =>
      api.patch(`/users/${id}`, { role: next }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["team"] }),
    onError: (e: unknown) =>
      toast({ title: "Role unchanged", description: apiErrorMessage(e), variant: "destructive" }),
  });

  const remove = useMutation({
    mutationFn: async (id: string) => api.delete(`/users/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["team"] }),
    onError: (e: unknown) =>
      toast({ title: "Nothing removed", description: apiErrorMessage(e), variant: "destructive" }),
  });

  async function copyLink(token: string) {
    try {
      await navigator.clipboard.writeText(inviteURL(token));
      setCopied(true);
    } catch {
      // Clipboard access is refused outside a secure context; the link is on
      // screen and selectable, so this is not worth an error state.
      setCopied(false);
    }
  }

  const list = members ?? [];

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Invite someone</CardTitle>
          <CardDescription>
            Admins manage databases, integrations and the team. Members can chat with the agent, see
            saved dashboards and read usage.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid grid-cols-[1fr_10rem] gap-4">
            <div className="space-y-1.5">
              <Label htmlFor="invite-email">Email</Label>
              <Input
                id="invite-email"
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="colleague@company.com"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="invite-role">Role</Label>
              <Select value={role} onValueChange={(v) => setRole(v as Role)}>
                <SelectTrigger id="invite-role">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="member">Member</SelectItem>
                  <SelectItem value="admin">Admin</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}

          {issued && (
            <div className="rounded-md border border-border bg-muted/40 p-3.5 space-y-2">
              <p className="text-sm font-medium">
                {issued.emailed
                  ? `Invitation sent to ${issued.user.email}`
                  : `Invitation link for ${issued.user.email}`}
              </p>
              <p className="text-xs text-muted-foreground">
                {issued.emailed
                  ? "They should have it in a moment. The link below is the same one — keep it if you want to pass it on another way."
                  : "Argentum could not email this, so pass it on yourself."}{" "}
                It works once, expires {new Date(issued.expires_at).toLocaleDateString()}, and
                cannot be shown again.
              </p>
              <div className="flex items-center gap-2">
                <Input readOnly value={inviteURL(issued.token)} className="font-mono text-xs" />
                <Button
                  variant="outline"
                  size="icon"
                  onClick={() => copyLink(issued.token)}
                  aria-label="Copy invitation link"
                >
                  {copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
                </Button>
              </div>
              <Button variant="ghost" size="sm" onClick={() => setIssued(null)}>
                Done
              </Button>
            </div>
          )}
        </CardContent>
        <CardFooter>
          <Button onClick={() => invite.mutate()} disabled={!email.trim() || invite.isPending}>
            {invite.isPending ? "Creating invitation…" : "Create invitation"}
          </Button>
        </CardFooter>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Team</CardTitle>
        </CardHeader>
        <CardContent className="divide-y divide-border/50">
          {isLoading && <div className="text-sm text-muted-foreground py-4">Loading…</div>}
          {!isLoading && list.length === 0 && (
            <div className="text-sm text-muted-foreground py-4">Nobody here yet.</div>
          )}
          {list.map((m) => {
            const isMe = m.id === me?.id;
            const expanded = openAccess === m.id;
            return (
              <div key={m.id} className="py-3">
                <div className="flex items-center justify-between gap-4">
                  <div className="min-w-0">
                    <div className="text-sm font-medium truncate">
                      {m.email}
                      {isMe && <span className="text-muted-foreground font-normal"> (you)</span>}
                    </div>
                    <div className="flex items-center gap-2 mt-1">
                      {statusBadge(m.status)}
                      {m.status === "pending" && m.invite_expires_at && (
                        <span className="text-xs text-muted-foreground">
                          expires {new Date(m.invite_expires_at).toLocaleDateString()}
                        </span>
                      )}
                    </div>
                  </div>
                  <div className="flex items-center gap-2 shrink-0">
                    {/* A removed person reaches nothing whatever they hold, so
                        their panel would be a list of grants that open nothing. */}
                    <Button
                      variant="ghost"
                      size="sm"
                      disabled={m.status === "deactivated"}
                      aria-expanded={expanded}
                      aria-label={`Access for ${m.email}`}
                      onClick={() => setOpenAccess(expanded ? null : m.id)}
                    >
                      Access
                      <ChevronDown className={cn("h-4 w-4 transition-transform", expanded && "rotate-180")} />
                    </Button>
                    <Select
                      value={m.role}
                      onValueChange={(v) => changeRole.mutate({ id: m.id, next: v as Role })}
                      disabled={m.status === "deactivated"}
                    >
                      <SelectTrigger className="w-32" aria-label={`Role for ${m.email}`}>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="member">Member</SelectItem>
                        <SelectItem value="admin">Admin</SelectItem>
                      </SelectContent>
                    </Select>
                    <Button
                      variant="ghost"
                      size="icon"
                      disabled={m.status === "deactivated"}
                      aria-label={m.status === "pending" ? "Revoke invitation" : "Remove member"}
                      onClick={() => {
                        const what =
                          m.status === "pending"
                            ? `Revoke the invitation for ${m.email}?`
                            : `Remove ${m.email}? They lose access within 15 minutes.`;
                        if (confirm(what)) remove.mutate(m.id);
                      }}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </div>
                </div>
                {expanded && (
                  <PersonAccessPanel
                    person={m}
                    agents={agents}
                    dashboards={dashboards}
                    connections={connections}
                    documents={documents}
                    meId={me?.id}
                  />
                )}
              </div>
            );
          })}
        </CardContent>
      </Card>

      {/* The other direction of the panels above, from the same reads (T-Z7, T-Z5, T-Z6). */}
      <ResourceAccessCard kind="agent" resources={agents} people={list} meId={me?.id} />
      <ResourceAccessCard kind="dashboard" resources={dashboards} people={list} meId={me?.id} />
      <ResourceAccessCard kind="connection" resources={connections} people={list} meId={me?.id} />
      <ResourceAccessCard kind="document" resources={documents} people={list} meId={me?.id} />
    </div>
  );
}
