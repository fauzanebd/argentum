import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Copy, Check, Trash2, ChevronDown, ChevronRight } from "lucide-react";
import type {
  APIKeyErrorsResponse,
  APIKeyRequestStats,
  APIRequestError,
} from "@argentum/api-types";
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

// Where the published quickstart lives — the landing app serves it at `/docs/`
// from `apps/landing/scripts/build-docs.mjs`. It is a full URL because the
// dashboard is a different host, and it has no default on purpose: a
// deployment that has not published its docs should show no link rather than
// one that 404s at the moment somebody has a fresh key and nothing to do
// with it.
const DOCS_URL = import.meta.env.VITE_DOCS_URL as string | undefined;

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

/** `GET /api/api-keys` carries the traffic beside the roster (T-A5). */
interface APIKeysBody {
  keys: APIKey[];
  stats?: Record<string, APIKeyRequestStats>;
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

/** A key with no traffic in the window has no stats row at all — "no calls" and
 *  "we cannot read the counters" are different facts and read differently. */
function traffic(stats?: APIKeyRequestStats): string {
  if (!stats) return "no calls in 24h";
  const window = `${stats.window_hours}h`;
  const calls = `${stats.requests} ${stats.requests === 1 ? "call" : "calls"} in ${window}`;
  if (stats.failed === 0) return `${calls} · no errors · ${stats.avg_latency_ms}ms avg`;
  return `${calls} · ${stats.error_rate_pct}% errors · ${stats.avg_latency_ms}ms avg`;
}

/** Errors get colour; a 429 is not the same conversation as a 500. */
function statusTone(status: number): string {
  if (status >= 500) return "text-destructive";
  if (status === 429) return "text-amber-600 dark:text-amber-500";
  return "text-muted-foreground";
}

function shortTime(iso: string): string {
  return new Date(iso).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

/** The failure list for one key.
 *
 *  Fetched when the row is expanded rather than with the roster: most keys are
 *  working, and nobody opens this tab to read fifty rows for a key that is
 *  fine. The request id is monospace and selectable because the entire point of
 *  showing it is that somebody pastes it. */
function KeyErrors({ keyId }: { keyId: string }) {
  const { data, isLoading } = useQuery({
    queryKey: ["api-key-errors", keyId],
    queryFn: async () =>
      (
        await api.get<APIKeyErrorsResponse>("/api-keys/errors", {
          params: { key_id: keyId },
        })
      ).data,
    // The recorder flushes on an interval, so a failure that just happened
    // arrives within seconds of it. Refetching while the panel is open is what
    // makes "trigger a 403 and watch it appear" true without a reload.
    refetchInterval: 15000,
  });

  const rows = (data?.errors ?? []) as APIRequestError[];

  if (isLoading) {
    return <div className="text-xs text-muted-foreground py-2">Loading recent failures…</div>;
  }
  if (rows.length === 0) {
    return (
      <div className="text-xs text-muted-foreground py-2">
        No failed calls recorded for this key.
      </div>
    );
  }
  return (
    <div className="space-y-1.5 py-2">
      <p className="text-xs text-muted-foreground">
        The last {data?.limit ?? rows.length} non-2xx responses, newest first. Quote the request id
        if you need help with one.
      </p>
      <div className="overflow-x-auto">
        <table className="w-full text-xs">
          <thead className="text-muted-foreground">
            <tr className="text-left">
              <th className="font-normal py-1 pr-3">When</th>
              <th className="font-normal py-1 pr-3">Route</th>
              <th className="font-normal py-1 pr-3">Status</th>
              <th className="font-normal py-1 pr-3">Code</th>
              <th className="font-normal py-1">Request id</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((e) => (
              <tr key={e.id} className="border-t border-border/40">
                <td className="py-1 pr-3 whitespace-nowrap">{shortTime(e.created_at)}</td>
                <td className="py-1 pr-3 font-mono whitespace-nowrap">
                  {e.method} {e.route}
                </td>
                <td className={`py-1 pr-3 font-mono ${statusTone(e.status)}`}>{e.status}</td>
                <td className="py-1 pr-3 font-mono">{e.error_code || "—"}</td>
                <td className="py-1 font-mono select-all whitespace-nowrap">{e.request_id}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
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
  // One key's failures at a time. Fifty rows per key, expanded for every key at
  // once, is a page nobody can read — and each panel is its own request.
  const [expanded, setExpanded] = useState<string | null>(null);

  // The vocabulary comes from the API rather than a constant here, so a scope
  // added on the backend shows up without a frontend change.
  const { data: scopes } = useQuery({
    queryKey: ["api-key-scopes"],
    queryFn: async () =>
      (await api.get<{ scopes: ScopeInfo[] }>("/api-keys/scopes")).data.scopes ?? [],
  });

  const { data: roster, isLoading } = useQuery({
    queryKey: ["api-keys"],
    queryFn: async () => (await api.get<APIKeysBody>("/api-keys")).data,
    // A key minted five minutes ago and used since should show that without a
    // reload. The counters are flushed on an interval anyway, so polling faster
    // than that would only ask the same question twice.
    refetchInterval: 30000,
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

  const list = roster?.keys ?? [];
  const stats = roster?.stats ?? {};
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
            {DOCS_URL && (
              <>
                {" "}
                The{" "}
                <a
                  className="underline underline-offset-2"
                  href={DOCS_URL}
                  target="_blank"
                  rel="noreferrer"
                >
                  quickstart
                </a>{" "}
                goes from this key to a rendered PDF in about ten minutes.
              </>
            )}
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
            Traffic is the last 24 hours. Open <span className="font-medium">Failures</span> on a key
            to see its last 50 non-2xx responses with the request id the caller was handed — that is
            what makes a 403 at 11pm answerable without us reading a log.
            <br />
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
            <div key={k.id} className="py-3">
              <div className="flex items-start justify-between gap-4">
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
                  <div className="text-xs text-muted-foreground">
                    {traffic(stats[k.id])}
                    {(stats[k.id]?.failed ?? 0) > 0 && (
                      <span className="text-destructive">
                        {" "}
                        · {stats[k.id]?.failed} failed
                      </span>
                    )}
                  </div>
                  <div className="flex flex-wrap gap-1 pt-0.5">
                    {k.scopes.map((s) => (
                      <Badge key={s} variant="outline" className="font-mono text-[10px]">
                        {s}
                      </Badge>
                    ))}
                  </div>
                </div>
                <div className="flex items-center gap-1 shrink-0">
                  <Button
                    variant="ghost"
                    size="sm"
                    className="text-xs"
                    aria-expanded={expanded === k.id}
                    onClick={() => setExpanded(expanded === k.id ? null : k.id)}
                  >
                    {expanded === k.id ? (
                      <ChevronDown className="h-3.5 w-3.5 mr-1" />
                    ) : (
                      <ChevronRight className="h-3.5 w-3.5 mr-1" />
                    )}
                    Failures
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
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
              </div>
              {expanded === k.id && <KeyErrors keyId={k.id} />}
            </div>
          ))}
        </CardContent>
      </Card>
    </div>
  );
}
