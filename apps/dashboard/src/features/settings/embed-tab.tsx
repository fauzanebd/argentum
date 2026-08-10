import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Copy, Check, Trash2, Pencil } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
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

// Embed keys (T-19). The credential a tenant's own website holds.
//
// The tab has one job the API keys tab does not: teaching. An API key is
// pasted into an env var and works; an embed key does nothing until somebody
// writes the signing endpoint, and the snippets below are the whole difference
// between an integration that finishes and one that stalls on day one. They are
// generated from the key on screen rather than shown as a generic example,
// because a snippet an admin has to edit is a snippet an admin gets wrong.

type Status = "active" | "disabled" | "revoked";

interface EmbedKey {
  id: string;
  name: string;
  client_key: string;
  allowed_origins: string[];
  enabled: boolean;
  status: Status;
  last_used_at?: string;
  created_at: string;
}

interface EmbedKeysBody {
  keys: EmbedKey[];
  session_ttl_seconds: number;
  max_signature_lifetime_secs: number;
}

interface CreatedEmbedKey {
  key: EmbedKey;
  secret: string;
}

/** The widget's own look and words (T-23). Every field is optional: an unset
 *  one means "whatever Argentum's default is", resolved when the widget reads
 *  it rather than stored here — so a default that changes reaches every tenant
 *  who never overrode it. */
interface WidgetConfig {
  greeting?: string;
  suggested_prompts?: string[];
  locale?: string;
  primary?: string;
  radius?: number;
  mode?: "light" | "dark" | "auto";
  launcher?: "bubble" | "none";
  position?: "bottom-right" | "bottom-left";
}

type Language = "node" | "go" | "python" | "php";

const LANGUAGES: { id: Language; label: string }[] = [
  { id: "node", label: "Node" },
  { id: "go", label: "Go" },
  { id: "python", label: "Python" },
  { id: "php", label: "PHP" },
];

/** The signing snippets.
 *
 *  Each one is the **whole** server-side flow, not a fragment: read the signed-in
 *  user, choose a deadline, compute the HMAC, hand all four values to the page.
 *  A partial snippet is what makes an integrator invent the missing half, and the
 *  half they invent is usually "send the secret to the browser".
 *
 *  The signed string is `<user_ref>:<exp>` and nothing else. It has to match
 *  internal/auth/embedkey.go exactly — if that changes, these change with it. */
function snippet(lang: Language, clientKey: string, ttlSecs: number): string {
  const key = clientKey || "argw_pub_…";
  const life = Math.min(ttlSecs, 3600);
  switch (lang) {
    case "node":
      return `// Runs on YOUR server. The secret must never reach the browser.
import { createHmac } from "node:crypto";

app.get("/argentum-identity", requireLogin, (req, res) => {
  const userRef = String(req.user.id);            // your id for this person
  const exp = Math.floor(Date.now() / 1000) + ${life};  // seconds, max 24h ahead

  const sig = createHmac("sha256", process.env.ARGENTUM_EMBED_SECRET)
    .update(\`\${userRef}:\${exp}\`)
    .digest("hex");

  res.json({ clientKey: "${key}", user: { ref: userRef, exp, sig } });
});`;
    case "go":
      return `// Runs on YOUR server. The secret must never reach the browser.
func argentumIdentity(w http.ResponseWriter, r *http.Request) {
	userRef := currentUser(r).ID           // your id for this person
	exp := time.Now().Add(${Math.round(life / 60)} * time.Minute).Unix()  // max 24h ahead

	mac := hmac.New(sha256.New, []byte(os.Getenv("ARGENTUM_EMBED_SECRET")))
	fmt.Fprintf(mac, "%s:%d", userRef, exp)
	sig := hex.EncodeToString(mac.Sum(nil))

	json.NewEncoder(w).Encode(map[string]any{
		"clientKey": "${key}",
		"user":      map[string]any{"ref": userRef, "exp": exp, "sig": sig},
	})
}`;
    case "python":
      return `# Runs on YOUR server. The secret must never reach the browser.
import hashlib, hmac, os, time

@app.get("/argentum-identity")
@login_required
def argentum_identity():
    user_ref = str(current_user.id)        # your id for this person
    exp = int(time.time()) + ${life}            # seconds, max 24h ahead

    sig = hmac.new(
        os.environ["ARGENTUM_EMBED_SECRET"].encode(),
        f"{user_ref}:{exp}".encode(),
        hashlib.sha256,
    ).hexdigest()

    return {"clientKey": "${key}", "user": {"ref": user_ref, "exp": exp, "sig": sig}}`;
    case "php":
      return `<?php
// Runs on YOUR server. The secret must never reach the browser.
$userRef = (string) $currentUser->id;     // your id for this person
$exp = time() + ${life};                        // seconds, max 24h ahead

$sig = hash_hmac('sha256', "{$userRef}:{$exp}", getenv('ARGENTUM_EMBED_SECRET'));

header('Content-Type: application/json');
echo json_encode([
    'clientKey' => '${key}',
    'user' => ['ref' => $userRef, 'exp' => $exp, 'sig' => $sig],
]);`;
  }
}

function statusBadge(status: Status) {
  switch (status) {
    case "revoked":
      return <Badge variant="secondary">Revoked</Badge>;
    case "disabled":
      return <Badge variant="outline">Paused</Badge>;
    default:
      return null;
  }
}

function relative(iso?: string): string {
  if (!iso) return "never used";
  const mins = Math.round((Date.now() - new Date(iso).getTime()) / 60000);
  if (mins < 1) return "used just now";
  if (mins < 60) return `used ${mins}m ago`;
  if (mins < 60 * 24) return `used ${Math.round(mins / 60)}h ago`;
  return `used ${new Date(iso).toLocaleDateString()}`;
}

/** One line per origin: the format an admin can paste a list into, and the one
 *  a comma in a URL cannot corrupt. */
function parseOrigins(raw: string): string[] {
  return raw
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean);
}

export function EmbedTab() {
  const qc = useQueryClient();
  const { toast } = useToast();

  const [name, setName] = useState("");
  const [origins, setOrigins] = useState("");
  const [error, setError] = useState<string | null>(null);
  // The secret exists in exactly one response, ever. It stays on screen until
  // dismissed rather than in a toast that vanishes — the same reasoning as the
  // API key token and the team invite link.
  const [issued, setIssued] = useState<CreatedEmbedKey | null>(null);
  const [copied, setCopied] = useState<string | null>(null);
  const [editing, setEditing] = useState<string | null>(null);
  const [draftOrigins, setDraftOrigins] = useState("");
  const [lang, setLang] = useState<Language>("node");
  const [config, setConfig] = useState<WidgetConfig>({});
  const [prompts, setPrompts] = useState("");
  const [configError, setConfigError] = useState<string | null>(null);

  const { data, isLoading } = useQuery({
    queryKey: ["embed-keys"],
    queryFn: async () => (await api.get<EmbedKeysBody>("/embed-keys")).data,
  });

  useQuery({
    queryKey: ["embed-config"],
    queryFn: async () => {
      const body = (await api.get<{ config: WidgetConfig }>("/embed-config")).data;
      const cfg = body.config ?? {};
      setConfig(cfg);
      setPrompts((cfg.suggested_prompts ?? []).join("\n"));
      return body;
    },
  });

  const saveConfig = useMutation({
    mutationFn: async () =>
      (
        await api.put<{ config: WidgetConfig }>("/embed-config", {
          ...config,
          suggested_prompts: parseOrigins(prompts),
        })
      ).data,
    onSuccess: () => {
      setConfigError(null);
      toast({ title: "Widget updated", description: "Live on the next open — no redeploy." });
      qc.invalidateQueries({ queryKey: ["embed-config"] });
    },
    onError: (e: unknown) => setConfigError(apiErrorMessage(e, "Could not save that")),
  });

  const create = useMutation({
    mutationFn: async () =>
      (
        await api.post<CreatedEmbedKey>("/embed-keys", {
          name: name.trim(),
          allowed_origins: parseOrigins(origins),
        })
      ).data,
    onSuccess: (res) => {
      setIssued(res);
      setCopied(null);
      setName("");
      setOrigins("");
      setError(null);
      qc.invalidateQueries({ queryKey: ["embed-keys"] });
    },
    onError: (e: unknown) => setError(apiErrorMessage(e, "Could not create that embed key")),
  });

  const update = useMutation({
    mutationFn: async (vars: { id: string; origins: string[]; enabled: boolean }) =>
      api.put(`/embed-keys/${vars.id}`, {
        allowed_origins: vars.origins,
        enabled: vars.enabled,
      }),
    onSuccess: () => {
      setEditing(null);
      qc.invalidateQueries({ queryKey: ["embed-keys"] });
    },
    onError: (e: unknown) =>
      toast({ title: "Nothing changed", description: apiErrorMessage(e), variant: "destructive" }),
  });

  const revoke = useMutation({
    mutationFn: async (id: string) => api.delete(`/embed-keys/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["embed-keys"] }),
    onError: (e: unknown) =>
      toast({ title: "Nothing revoked", description: apiErrorMessage(e), variant: "destructive" }),
  });

  async function copy(value: string, what: string) {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(what);
    } catch {
      // Clipboard access is refused outside a secure context. Everything here
      // is on screen and selectable, so this is not worth an error state.
      setCopied(null);
    }
  }

  const keys = data?.keys ?? [];
  const ttl = data?.session_ttl_seconds ?? 900;
  const active = keys.find((k) => k.status === "active");
  const snippetKey = issued?.key.client_key ?? active?.client_key ?? "";

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Put Argentum inside your own site</CardTitle>
          <CardDescription>
            An embed key lets a page you run ask Argentum questions as one of your own people. The
            key's public half ships in your page source; its signing secret stays on your server and
            is what proves who the visitor is. Sessions last{" "}
            {Math.round(ttl / 60)} minutes and your page re-signs to continue.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="embed-name">Name</Label>
            <Input
              id="embed-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Staff intranet"
            />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="embed-origins">Allowed sites</Label>
            <Textarea
              id="embed-origins"
              value={origins}
              onChange={(e) => setOrigins(e.target.value)}
              placeholder={"https://intranet.acme.com\nhttp://localhost:3000"}
              rows={3}
              className="font-mono text-xs"
            />
            <p className="text-xs text-muted-foreground">
              One per line, exact — scheme, host and port. A wildcard is not accepted, and neither
              is an empty list: this is the check that stops a page nobody authorised minting
              sessions for your workspace. <code>http://</code> is allowed for localhost only.
            </p>
          </div>

          {error && <p className="text-sm text-destructive">{error}</p>}

          {issued && (
            <div className="rounded-md border border-border bg-muted/40 p-3.5 space-y-3">
              <p className="text-sm font-medium">Key for {issued.key.name}</p>
              <div className="space-y-1.5">
                <Label className="text-xs">Client key — public, goes in your page</Label>
                <div className="flex items-center gap-2">
                  <Input readOnly value={issued.key.client_key} className="font-mono text-xs" />
                  <Button
                    variant="outline"
                    size="icon"
                    onClick={() => copy(issued.key.client_key, "client")}
                    aria-label="Copy client key"
                  >
                    {copied === "client" ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
                  </Button>
                </div>
              </div>
              <div className="space-y-1.5">
                <Label className="text-xs">Signing secret — server-side only</Label>
                <div className="flex items-center gap-2">
                  <Input readOnly value={issued.secret} className="font-mono text-xs" />
                  <Button
                    variant="outline"
                    size="icon"
                    onClick={() => copy(issued.secret, "secret")}
                    aria-label="Copy signing secret"
                  >
                    {copied === "secret" ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
                  </Button>
                </div>
                <p className="text-xs text-muted-foreground">
                  Copy it now — this is the only time it is shown. Put it in an environment variable
                  on your server. Anyone who can read it can ask Argentum questions as any of your
                  people, so it must never reach a browser.
                </p>
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
            disabled={!name.trim() || parseOrigins(origins).length === 0 || create.isPending}
          >
            {create.isPending ? "Creating key…" : "Create embed key"}
          </Button>
        </CardFooter>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>What the widget looks like</CardTitle>
          <CardDescription>
            The greeting and prompts a visitor sees before they type, and the colour the launcher
            uses. Saved here, read by the widget on its next open — no redeploy of your site.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="widget-greeting">Greeting</Label>
            <Input
              id="widget-greeting"
              value={config.greeting ?? ""}
              placeholder="Ask me about your data."
              onChange={(e) => setConfig({ ...config, greeting: e.target.value })}
            />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="widget-prompts">Suggested prompts</Label>
            <Textarea
              id="widget-prompts"
              value={prompts}
              rows={3}
              placeholder={"Revenue by store last month\nWhich products sell best on weekends?"}
              onChange={(e) => setPrompts(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">
              One per line, up to five. These are the first thing a visitor reads, so they are also
              the clearest statement of what this agent is for.
            </p>
          </div>

          <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <div className="space-y-1.5">
              <Label htmlFor="widget-primary">Accent</Label>
              <div className="flex items-center gap-2">
                <input
                  id="widget-primary"
                  type="color"
                  className="h-9 w-12 rounded border border-border bg-transparent"
                  value={config.primary ?? "#e11d48"}
                  onChange={(e) => setConfig({ ...config, primary: e.target.value })}
                />
                <Input
                  value={config.primary ?? ""}
                  placeholder="#e11d48"
                  onChange={(e) => setConfig({ ...config, primary: e.target.value })}
                />
              </div>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="widget-radius">Corner radius</Label>
              <Input
                id="widget-radius"
                type="number"
                min={0}
                max={32}
                value={config.radius ?? ""}
                placeholder="12"
                onChange={(e) =>
                  setConfig({
                    ...config,
                    radius: e.target.value === "" ? undefined : Number(e.target.value),
                  })
                }
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="widget-mode">Theme</Label>
              <Select
                value={config.mode ?? "auto"}
                onValueChange={(v) => setConfig({ ...config, mode: v as WidgetConfig["mode"] })}
              >
                <SelectTrigger id="widget-mode">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="auto">Match the visitor</SelectItem>
                  <SelectItem value="light">Light</SelectItem>
                  <SelectItem value="dark">Dark</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="widget-position">Launcher</Label>
              <Select
                value={config.launcher === "none" ? "none" : (config.position ?? "bottom-right")}
                onValueChange={(v) =>
                  setConfig(
                    v === "none"
                      ? { ...config, launcher: "none" }
                      : { ...config, launcher: "bubble", position: v as WidgetConfig["position"] },
                  )
                }
              >
                <SelectTrigger id="widget-position">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="bottom-right">Bubble, bottom right</SelectItem>
                  <SelectItem value="bottom-left">Bubble, bottom left</SelectItem>
                  <SelectItem value="none">None — I render my own</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>

          {/* The preview is the real thing, not a picture of it: the same
              greeting, the same prompts, the same accent, drawn with the values
              in the form above rather than with what is saved. An admin who has
              to press Save to see a colour presses Save with the wrong one. */}
          <div className="space-y-1.5">
            <Label>Preview</Label>
            <div
              className="rounded-md border border-border p-3.5 space-y-2"
              style={{ borderRadius: `${config.radius ?? 12}px` }}
            >
              <p className="text-sm">{config.greeting || "Ask me about your data."}</p>
              {parseOrigins(prompts)
                .slice(0, 5)
                .map((p) => (
                  <div
                    key={p}
                    className="rounded border border-border px-3 py-2 text-sm"
                    style={{ borderRadius: `${Math.max((config.radius ?? 12) - 4, 0)}px` }}
                  >
                    {p}
                  </div>
                ))}
              <div className="flex items-center gap-2 pt-1">
                <span
                  className="inline-flex h-8 w-8 items-center justify-center rounded-full text-white"
                  style={{ background: config.primary || "#e11d48" }}
                >
                  💬
                </span>
                <span className="text-xs text-muted-foreground">
                  {config.launcher === "none"
                    ? "No launcher — your page calls Argentum.open()"
                    : `Launcher, ${config.position === "bottom-left" ? "bottom left" : "bottom right"}`}
                </span>
              </div>
            </div>
          </div>

          {configError && <p className="text-sm text-destructive">{configError}</p>}
        </CardContent>
        <CardFooter>
          <Button onClick={() => saveConfig.mutate()} disabled={saveConfig.isPending}>
            {saveConfig.isPending ? "Saving…" : "Save widget settings"}
          </Button>
        </CardFooter>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Your signing endpoint</CardTitle>
          <CardDescription>
            The one piece you have to write. It runs on your server, reads whoever is signed in, and
            hands the page a signature over <code>&lt;user_ref&gt;:&lt;exp&gt;</code>. Argentum
            trusts the person that names, so it must never be reachable by someone who is not
            signed in to your site.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="inline-flex border-b border-border">
            {LANGUAGES.map((l) => (
              <button
                key={l.id}
                onClick={() => setLang(l.id)}
                className={
                  lang === l.id
                    ? "px-3 py-1.5 text-sm border-b-2 border-primary"
                    : "px-3 py-1.5 text-sm border-b-2 border-transparent text-muted-foreground hover:text-foreground"
                }
              >
                {l.label}
              </button>
            ))}
          </div>
          <pre className="rounded-md bg-muted/50 p-3 text-xs overflow-x-auto">
            <code>{snippet(lang, snippetKey, ttl)}</code>
          </pre>
          <Button
            variant="outline"
            size="sm"
            onClick={() => copy(snippet(lang, snippetKey, ttl), "snippet")}
          >
            {copied === "snippet" ? (
              <Check className="h-4 w-4 mr-1.5" />
            ) : (
              <Copy className="h-4 w-4 mr-1.5" />
            )}
            Copy snippet
          </Button>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Embed keys</CardTitle>
          <CardDescription>
            Pausing a key stops new sessions and can be undone. Revoking is permanent — sessions
            already issued keep working until they expire, at most {Math.round(ttl / 60)} minutes.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <p className="text-sm text-muted-foreground">Loading…</p>
          ) : keys.length === 0 ? (
            <p className="text-sm text-muted-foreground">No embed keys yet.</p>
          ) : (
            <ul className="divide-y divide-border">
              {keys.map((k) => (
                <li key={k.id} className="py-3 space-y-2">
                  <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                      <p className="text-sm font-medium flex items-center gap-2">
                        {k.name} {statusBadge(k.status)}
                      </p>
                      <p className="text-xs font-mono text-muted-foreground select-all break-all">
                        {k.client_key}
                      </p>
                      <p className="text-xs text-muted-foreground">{relative(k.last_used_at)}</p>
                    </div>
                    {k.status !== "revoked" && (
                      <div className="flex items-center gap-1.5 shrink-0">
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={() =>
                            update.mutate({
                              id: k.id,
                              origins: k.allowed_origins,
                              enabled: !k.enabled,
                            })
                          }
                          disabled={update.isPending}
                        >
                          {k.enabled ? "Pause" : "Resume"}
                        </Button>
                        <Button
                          variant="outline"
                          size="icon"
                          aria-label="Edit allowed sites"
                          onClick={() => {
                            setEditing(k.id);
                            setDraftOrigins(k.allowed_origins.join("\n"));
                          }}
                        >
                          <Pencil className="h-4 w-4" />
                        </Button>
                        <Button
                          variant="outline"
                          size="icon"
                          aria-label="Revoke embed key"
                          onClick={() => revoke.mutate(k.id)}
                          disabled={revoke.isPending}
                        >
                          <Trash2 className="h-4 w-4" />
                        </Button>
                      </div>
                    )}
                  </div>

                  {editing === k.id ? (
                    <div className="space-y-2">
                      <Textarea
                        value={draftOrigins}
                        onChange={(e) => setDraftOrigins(e.target.value)}
                        rows={3}
                        className="font-mono text-xs"
                      />
                      <div className="flex gap-2">
                        <Button
                          size="sm"
                          onClick={() =>
                            update.mutate({
                              id: k.id,
                              origins: parseOrigins(draftOrigins),
                              enabled: k.enabled,
                            })
                          }
                          disabled={update.isPending || parseOrigins(draftOrigins).length === 0}
                        >
                          Save sites
                        </Button>
                        <Button variant="ghost" size="sm" onClick={() => setEditing(null)}>
                          Cancel
                        </Button>
                      </div>
                    </div>
                  ) : (
                    <div className="flex flex-wrap gap-1.5">
                      {k.allowed_origins.map((o) => (
                        <code
                          key={o}
                          className="text-xs rounded bg-muted px-1.5 py-0.5 text-muted-foreground"
                        >
                          {o}
                        </code>
                      ))}
                    </div>
                  )}
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
