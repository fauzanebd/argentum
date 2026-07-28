import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Copy, Check, Trash2 } from "lucide-react";
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

type Status = "active" | "revoked" | "expired";

interface APIKey {
  id: string;
  name: string;
  key_prefix: string;
  scopes: string[];
  status: Status;
  last_used_at?: string;
  expires_at?: string;
  created_at: string;
}

interface ScopeInfo {
  scope: string;
  description: string;
  writes: boolean;
}

interface CreatedKey {
  key: APIKey;
  token: string;
}

/** Expiry choices, in days. 0 is "never", which is the sane default for a
 *  server-to-server credential: there is no rotation tooling behind this yet,
 *  so a key that expires unattended just breaks a tenant's integration at
 *  3am. */
const EXPIRY_CHOICES = [
  { value: "0", label: "Never" },
  { value: "30", label: "30 days" },
  { value: "90", label: "90 days" },
  { value: "365", label: "1 year" },
];

function statusBadge(status: Status) {
  switch (status) {
    case "revoked":
      return <Badge variant="secondary">Revoked</Badge>;
    case "expired":
      return <Badge variant="outline">Expired</Badge>;
    default:
      return null;
  }
}

function relative(iso?: string): string {
  if (!iso) return "never used";
  const then = new Date(iso).getTime();
  const mins = Math.round((Date.now() - then) / 60000);
  if (mins < 1) return "used just now";
  if (mins < 60) return `used ${mins}m ago`;
  if (mins < 60 * 24) return `used ${Math.round(mins / 60)}h ago`;
  return `used ${new Date(iso).toLocaleDateString()}`;
}

export function APIKeysTab() {
  const qc = useQueryClient();
  const { toast } = useToast();

  const [name, setName] = useState("");
  const [expiry, setExpiry] = useState("0");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [error, setError] = useState<string | null>(null);
  // The plaintext exists in exactly one response, ever. It stays on screen
  // until the admin dismisses it rather than living in a toast that vanishes
  // after four seconds — the same reasoning as the invite link in TeamTab.
  const [issued, setIssued] = useState<CreatedKey | null>(null);
  const [copied, setCopied] = useState(false);

  // The vocabulary comes from the API rather than a constant here, so a scope
  // added on the backend shows up without a frontend change.
  const { data: scopes } = useQuery({
    queryKey: ["api-key-scopes"],
    queryFn: async () =>
      (await api.get<{ scopes: ScopeInfo[] }>("/api-keys/scopes")).data.scopes ?? [],
  });

  const { data: keys, isLoading } = useQuery({
    queryKey: ["api-keys"],
    queryFn: async () => (await api.get<{ keys: APIKey[] }>("/api-keys")).data.keys ?? [],
  });

  const create = useMutation({
    mutationFn: async () =>
      (
        await api.post<CreatedKey>("/api-keys", {
          name: name.trim(),
          scopes: [...selected],
          expires_in_days: Number(expiry),
        })
      ).data,
    onSuccess: (res) => {
      setIssued(res);
      setCopied(false);
      setName("");
      setSelected(new Set());
      setError(null);
      qc.invalidateQueries({ queryKey: ["api-keys"] });
    },
    onError: (e: unknown) => setError(apiErrorMessage(e, "Could not create that key")),
  });

  const revoke = useMutation({
    mutationFn: async (id: string) => api.delete(`/api-keys/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["api-keys"] }),
    onError: (e: unknown) =>
      toast({ title: "Nothing revoked", description: apiErrorMessage(e), variant: "destructive" }),
  });

  function toggle(scope: string) {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(scope)) next.delete(scope);
      else next.add(scope);
      return next;
    });
  }

  async function copyToken(token: string) {
    try {
      await navigator.clipboard.writeText(token);
      setCopied(true);
    } catch {
      // Clipboard access is refused outside a secure context; the token is on
      // screen and selectable, so this is not worth an error state.
      setCopied(false);
    }
  }

  const list = keys ?? [];
  const reads = (scopes ?? []).filter((s) => !s.writes);
  const writes = (scopes ?? []).filter((s) => s.writes);

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Create an API key</CardTitle>
          <CardDescription>
            A key lets your own backend call Argentum over HTTP at <code>/v1</code>. It carries only
            the scopes you tick — they cannot be changed afterwards, so a key that needs more
            capabilities is a new key.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid grid-cols-[1fr_10rem] gap-4">
            <div className="space-y-1.5">
              <Label htmlFor="key-name">Name</Label>
              <Input
                id="key-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="Nightly report job"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="key-expiry">Expires</Label>
              <Select value={expiry} onValueChange={setExpiry}>
                <SelectTrigger id="key-expiry">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {EXPIRY_CHOICES.map((c) => (
                    <SelectItem key={c.value} value={c.value}>
                      {c.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>

          <div className="space-y-3">
            <Label>Scopes</Label>
            {[
              { title: "Read", items: reads },
              { title: "Write", items: writes },
            ].map((group) =>
              group.items.length === 0 ? null : (
                <div key={group.title} className="space-y-2">
                  <p className="text-xs uppercase tracking-wide text-muted-foreground">
                    {group.title}
                  </p>
                  {group.items.map((s) => (
                    <label key={s.scope} className="flex items-start gap-2 text-sm">
                      <input
                        type="checkbox"
                        checked={selected.has(s.scope)}
                        onChange={() => toggle(s.scope)}
                        className="mt-0.5"
                      />
                      <span>
                        <code className="text-xs">{s.scope}</code>
                        <span className="block text-xs text-muted-foreground">{s.description}</span>
                      </span>
                    </label>
                  ))}
                </div>
              ),
            )}
          </div>

          {error && <p className="text-sm text-destructive">{error}</p>}

          {issued && (
            <div className="rounded-md border border-border bg-muted/40 p-3.5 space-y-2">
              <p className="text-sm font-medium">Key for {issued.key.name}</p>
              <p className="text-xs text-muted-foreground">
                Copy it now. This is the only time it is shown — nothing can read it back, and a
                lost key has to be revoked and replaced.
              </p>
              <div className="flex items-center gap-2">
                <Input readOnly value={issued.token} className="font-mono text-xs" />
                <Button
                  variant="outline"
                  size="icon"
                  onClick={() => copyToken(issued.token)}
                  aria-label="Copy API key"
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
          <Button
            onClick={() => create.mutate()}
            disabled={!name.trim() || selected.size === 0 || create.isPending}
          >
            {create.isPending ? "Creating key…" : "Create key"}
          </Button>
        </CardFooter>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Keys</CardTitle>
          <CardDescription>
            Revoked keys stay listed: the audit log attributes calls to a key id, and a key nobody
            can name is a row nobody can explain.
          </CardDescription>
        </CardHeader>
        <CardContent className="divide-y divide-border/50">
          {isLoading && <div className="text-sm text-muted-foreground py-4">Loading…</div>}
          {!isLoading && list.length === 0 && (
            <div className="text-sm text-muted-foreground py-4">No keys yet.</div>
          )}
          {list.map((k) => (
            <div key={k.id} className="flex items-start justify-between gap-4 py-3">
              <div className="min-w-0 space-y-1">
                <div className="text-sm font-medium truncate">
                  {k.name} {statusBadge(k.status)}
                </div>
                <div className="text-xs text-muted-foreground font-mono">
                  arg_{k.key_prefix}_…
                </div>
                <div className="text-xs text-muted-foreground">
                  {relative(k.last_used_at)}
                  {k.expires_at &&
                    ` · expires ${new Date(k.expires_at).toLocaleDateString()}`}
                </div>
                <div className="flex flex-wrap gap-1 pt-0.5">
                  {k.scopes.map((s) => (
                    <Badge key={s} variant="outline" className="font-mono text-[10px]">
                      {s}
                    </Badge>
                  ))}
                </div>
              </div>
              <Button
                variant="ghost"
                size="icon"
                className="shrink-0"
                disabled={k.status !== "active"}
                aria-label={`Revoke ${k.name}`}
                onClick={() => {
                  if (
                    confirm(
                      `Revoke ${k.name}? Anything using it stops working immediately, and it cannot be restored.`,
                    )
                  ) {
                    revoke.mutate(k.id);
                  }
                }}
              >
                <Trash2 className="h-4 w-4" />
              </Button>
            </div>
          ))}
        </CardContent>
      </Card>
    </div>
  );
}
