import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { RefreshCw, Trash2, Star, PlugZap, Boxes, Search, ScanSearch } from "lucide-react";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { toast } from "@/hooks/use-toast";
import { apiErrorMessage } from "@/lib/api-error";
import { certVerificationHint, defaultSslMode, sslModeOptions, supportsSslMode } from "@/lib/ssl-modes";

interface Connection {
  id: string;
  db_type: string;
  label?: string;
  is_default: boolean;
  created_at: string;
}

interface RagHit {
  table: string;
  distance: number;
}

interface RagResult {
  query: string;
  source_id: string;
  top_k: number;
  hits: RagHit[];
  total_tables: number;
  indexed_tables: number;
  filtered_tables: number;
  schema_preview: string;
  embed_duration_ms: number;
  topk_duration_ms: number;
  schema_duration_ms: number;
}

const NOISE_TABLE_RE = /^(Backup_|Deleted_|Temp_)/i;

function indexHealthHint(indexed: number) {
  if (indexed <= 1) return { text: "Reindex required.", tone: "destructive" as const };
  if (indexed < 50) return { text: "Sparse index — reindex may help.", tone: "destructive" as const };
  return { text: "Index healthy.", tone: "muted" as const };
}

type ConnMode = "fields" | "raw";

function defaultPort(dbType: string) {
  if (dbType === "mysql") return "3306";
  return "5432";
}

function isFormReady(
  mode: ConnMode,
  dsn: string,
  host: string,
  port: string,
  dbname: string
) {
  if (mode === "raw") return dsn.trim().length > 0;
  return host.trim().length > 0 && port.trim().length > 0 && dbname.trim().length > 0;
}

export function ConnectionsTab() {
  const qc = useQueryClient();
  const [supported, setSupported] = useState<string[]>([]);
  const [mode, setMode] = useState<ConnMode>("fields");
  const [dbType, setDbType] = useState("postgres");
  const [label, setLabel] = useState("");
  const [dsn, setDsn] = useState("");
  const [host, setHost] = useState("");
  const [port, setPort] = useState(defaultPort("postgres"));
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [dbname, setDbname] = useState("");
  const [sslMode, setSslMode] = useState(defaultSslMode("postgres"));
  // Set when a save was refused because the database could not be opened. It
  // carries the driver's own error and turns Add into "Save anyway": a source
  // behind a VPN that is down right now is not a configuration error, but it is
  // not something to store silently either.
  const [unreachable, setUnreachable] = useState<string | null>(null);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [syncingId, setSyncingId] = useState<string | null>(null);
  const [testingId, setTestingId] = useState<string | null>(null);
  const [reindexingId, setReindexingId] = useState<string | null>(null);
  const [rescanningId, setRescanningId] = useState<string | null>(null);
  const [ragConn, setRagConn] = useState<Connection | null>(null);
  const [ragQuery, setRagQuery] = useState("");
  const [ragTopK, setRagTopK] = useState("8");
  const [ragLoading, setRagLoading] = useState(false);
  const [ragResult, setRagResult] = useState<RagResult | null>(null);
  const [ragError, setRagError] = useState<string | null>(null);

  const { data: connections } = useQuery({
    queryKey: ["connections"],
    queryFn: async () => (await api.get<{ connections: Connection[] }>("/connections")).data.connections,
  });

  useEffect(() => {
    api.get("/meta/supported-databases").then((r) => setSupported(r.data.registered ?? r.data.supported ?? []));
  }, []);

  useEffect(() => {
    setPort(defaultPort(dbType));
    // A mode is a driver's own word, not a shared one: carrying `require` from
    // postgres to mysql would mean a different handshake, and carrying
    // `skip-verify` the other way is not a word libpq has.
    setSslMode(defaultSslMode(dbType));
  }, [dbType]);

  function payload() {
    if (mode === "raw") {
      return { db_type: dbType, label, dsn };
    }
    return {
      db_type: dbType,
      label,
      host,
      port,
      username,
      password,
      dbname,
      ...(supportsSslMode(dbType) ? { ssl_mode: sslMode } : {}),
    };
  }

  async function testConnection() {
    setTesting(true);
    setTestResult(null);
    try {
      const res = await api.post("/connections/test", payload());
      setTestResult(res.data.ok ? "ok" : res.data.error || "Failed");
    } catch (e: unknown) {
      setTestResult(apiErrorMessage(e));
    } finally {
      setTesting(false);
    }
  }

  /** add saves the connection. The backend opens it first and refuses a source
   *  it cannot read — the failure that used to surface a turn later, as the
   *  agent reporting it had no access to the data. `force` is the second press:
   *  a database that is down right now is not a configuration error. */
  async function add(force = false) {
    setError(null);
    try {
      await api.post("/connections", {
        ...payload(),
        is_default: connections?.length === 0,
        ...(force ? { skip_test: true } : {}),
      });
      setLabel("");
      setDsn("");
      setHost("");
      setPort(defaultPort(dbType));
      setUsername("");
      setPassword("");
      setDbname("");
      setTestResult(null);
      setUnreachable(null);
      qc.invalidateQueries({ queryKey: ["connections"] });
    } catch (e: unknown) {
      const body = (e as { response?: { data?: { connection_error?: boolean; error?: string } } })
        ?.response?.data;
      if (body?.connection_error) {
        setUnreachable(body.error ?? "The database could not be reached.");
        return;
      }
      setError(apiErrorMessage(e));
    }
  }

  async function makeDefault(id: string) {
    await api.post(`/connections/${id}/default`);
    qc.invalidateQueries({ queryKey: ["connections"] });
  }

  async function remove(id: string) {
    if (!confirm("Remove this connection?")) return;
    await api.delete(`/connections/${id}`);
    qc.invalidateQueries({ queryKey: ["connections"] });
  }

  async function testExisting(id: string) {
    setTestingId(id);
    try {
      const res = await api.post<{ ok: boolean; error?: string }>(`/connections/${id}/test`);
      if (res.data.ok) {
        toast({ title: "Connection OK" });
      } else {
        toast({
          variant: "destructive",
          title: "Connection failed",
          description: res.data.error || "Unknown error",
        });
      }
    } catch (e: unknown) {
      toast({
        variant: "destructive",
        title: "Connection failed",
        description: apiErrorMessage(e),
      });
    } finally {
      setTestingId(null);
    }
  }

  async function reindexEmbeddings(id: string) {
    setReindexingId(id);
    try {
      const res = await api.post<{ tables: number; indexed_at: string }>(
        `/connections/${id}/reindex-embeddings`,
        undefined,
        { timeout: 300000 },
      );
      toast({
        title: "Embeddings reindexed",
        description: `${res.data.tables} tables indexed.`,
      });
    } catch (e: unknown) {
      toast({
        variant: "destructive",
        title: "Reindex failed",
        description: apiErrorMessage(e),
      });
    } finally {
      setReindexingId(null);
    }
  }

  function openRagTest(c: Connection) {
    setRagConn(c);
    setRagResult(null);
    setRagError(null);
  }

  async function runRagTest() {
    if (!ragConn || !ragQuery.trim()) return;
    setRagLoading(true);
    setRagError(null);
    setRagResult(null);
    const body: { query: string; top_k?: number } = { query: ragQuery.trim() };
    const parsedTopK = parseInt(ragTopK, 10);
    if (Number.isFinite(parsedTopK) && parsedTopK > 0) {
      body.top_k = parsedTopK;
    }
    try {
      const res = await api.post<RagResult>(
        `/connections/${ragConn.id}/test-rag`,
        body,
        { timeout: 60000 },
      );
      setRagResult(res.data);
    } catch (e: unknown) {
      const msg = apiErrorMessage(e);
      setRagError(msg);
      toast({ variant: "destructive", title: "RAG test failed", description: msg });
    } finally {
      setRagLoading(false);
    }
  }

  async function regenerateDescription(id: string) {
    setSyncingId(id);
    try {
      const res = await api.post<{ label?: string; description?: string }>(
        `/connections/${id}/regenerate-description`,
        undefined,
        { timeout: 95000 },
      );
      qc.invalidateQueries({ queryKey: ["connections"] });
      toast({
        title: "Description updated",
        description: res.data.description || `Synced ${res.data.label ?? "connection"}.`,
      });
    } catch (e: unknown) {
      toast({
        variant: "destructive",
        title: "Sync failed",
        description: apiErrorMessage(e),
      });
    } finally {
      setSyncingId(null);
    }
  }

  /** Re-read what this source says the business is (T-B2). The pass runs in the
   *  worker and no-ops when the schema has not changed, so the toast promises a
   *  queued job rather than a finished one. */
  async function rescanSource(id: string) {
    setRescanningId(id);
    try {
      await api.post(`/connections/${id}/rescan`);
      qc.invalidateQueries({ queryKey: ["company-profile-suggestion"] });
      toast({
        title: "Re-scanning this source",
        description:
          "We are reading the table names again. Any new suggestion shows up under Settings → General → Business profile.",
      });
    } catch (e: unknown) {
      toast({
        variant: "destructive",
        title: "Could not start the re-scan",
        description: apiErrorMessage(e),
      });
    } finally {
      setRescanningId(null);
    }
  }

  const ready = isFormReady(mode, dsn, host, port, dbname);

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Add database</CardTitle>
          <CardDescription>Argentum connects read-only — your data is never modified.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-1.5">
              <Label>Type</Label>
              <Select value={dbType} onValueChange={(v) => setDbType(v)}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {supported.map((t) => (
                    <SelectItem key={t} value={t}>
                      {t}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label>Label (optional)</Label>
              <Input value={label} onChange={(e) => setLabel(e.target.value)} />
            </div>
          </div>

          <div className="flex items-center gap-2">
            <Button
              type="button"
              variant={mode === "fields" ? "default" : "outline"}
              size="sm"
              onClick={() => setMode("fields")}
            >
              Connection details
            </Button>
            <Button
              type="button"
              variant={mode === "raw" ? "default" : "outline"}
              size="sm"
              onClick={() => setMode("raw")}
            >
              Raw DSN
            </Button>
          </div>

          {mode === "fields" ? (
            <div className="grid grid-cols-2 gap-4">
              <div className="space-y-1.5">
                <Label>Host</Label>
                <Input value={host} onChange={(e) => setHost(e.target.value)} placeholder="db.example.com" />
              </div>
              <div className="space-y-1.5">
                <Label>Port</Label>
                <Input value={port} onChange={(e) => setPort(e.target.value)} placeholder={defaultPort(dbType)} />
              </div>
              <div className="space-y-1.5">
                <Label>Username</Label>
                <Input value={username} onChange={(e) => setUsername(e.target.value)} />
              </div>
              <div className="space-y-1.5">
                <Label>Password</Label>
                <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
              </div>
              <div className="space-y-1.5 col-span-2">
                <Label>Database name</Label>
                <Input value={dbname} onChange={(e) => setDbname(e.target.value)} />
              </div>
              {supportsSslMode(dbType) && (
                <div className="space-y-1.5 col-span-2">
                  <Label>Encryption</Label>
                  <Select value={sslMode} onValueChange={setSslMode}>
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {sslModeOptions(dbType).map((m) => (
                        <SelectItem key={m.value} value={m.value}>
                          {m.label}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <p className="text-xs text-muted-foreground">
                    This form used to require TLS with no way to say otherwise, so a database that
                    does not speak it — or that holds a self-signed certificate — could be saved and
                    never reached.
                  </p>
                </div>
              )}
            </div>
          ) : (
            <div className="space-y-1.5">
              <Label>Connection string</Label>
              <Input
                value={dsn}
                onChange={(e) => setDsn(e.target.value)}
                placeholder="postgres://user:pass@host:5432/db?sslmode=require"
              />
            </div>
          )}

          <p className="text-xs text-muted-foreground">
            Stored encrypted at rest with AES-256-GCM. Only the database type is plaintext.
          </p>

          {testResult && (
            <Badge variant={testResult === "ok" ? "secondary" : "destructive"}>{testResult}</Badge>
          )}
          {/* A refused certificate is the one failure here that the form itself
              can fix, so it gets pointed at the control rather than left as the
              driver's x509 sentence. */}
          {mode === "fields" && certVerificationHint(testResult === "ok" ? null : testResult, dbType) && (
            <p className="text-xs text-muted-foreground">
              {certVerificationHint(testResult, dbType)}
            </p>
          )}
          {unreachable && (
            <div className="rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm">
              <p className="font-medium text-destructive">This database could not be opened</p>
              <p className="mt-1 font-mono text-xs text-muted-foreground">{unreachable}</p>
              <p className="mt-2 text-xs text-muted-foreground">
                Saving it anyway is fine if the database is temporarily down or behind a network
                the server reaches later — but until it opens, an agent asked about this data will
                spend a turn discovering it cannot read it.
              </p>
              {mode === "fields" && certVerificationHint(unreachable, dbType) && (
                <p className="mt-2 text-xs text-muted-foreground">
                  {certVerificationHint(unreachable, dbType)}
                </p>
              )}
            </div>
          )}
          {error && <p className="text-sm text-destructive">{error}</p>}
        </CardContent>
        <CardFooter className="gap-2">
          <Button variant="outline" disabled={testing || !ready} onClick={testConnection}>
            {testing ? "Testing…" : "Test"}
          </Button>
          <Button onClick={() => add()} disabled={!ready}>
            Add database
          </Button>
          {unreachable && (
            <Button variant="outline" onClick={() => add(true)} disabled={!ready}>
              Save anyway
            </Button>
          )}
        </CardFooter>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Existing databases</CardTitle>
        </CardHeader>
        <CardContent className="divide-y divide-border/50">
          {(connections ?? []).length === 0 && (
            <div className="text-sm text-muted-foreground py-4">No databases yet.</div>
          )}
          {(connections ?? []).map((c) => (
            <div key={c.id} className="flex items-center justify-between py-3">
              <div>
                <div className="text-sm font-medium flex items-center gap-2">
                  {c.label || c.db_type}
                  {c.is_default && (
                    <Badge variant="secondary" className="gap-1">
                      <Star className="h-3 w-3" /> default
                    </Badge>
                  )}
                </div>
                <div className="text-xs text-muted-foreground">{c.db_type}</div>
              </div>
              <div className="flex items-center gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => testExisting(c.id)}
                  disabled={testingId === c.id}
                >
                  <PlugZap className="h-4 w-4" />
                  {testingId === c.id ? "Testing…" : "Test"}
                </Button>
                {!c.is_default && (
                  <Button variant="outline" size="sm" onClick={() => makeDefault(c.id)}>
                    Make default
                  </Button>
                )}
                <Button
                  variant="ghost"
                  size="icon"
                  onClick={() => reindexEmbeddings(c.id)}
                  disabled={reindexingId === c.id}
                  title="Reindex embeddings"
                >
                  <Boxes className={`h-4 w-4 ${reindexingId === c.id ? "animate-pulse" : ""}`} />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  onClick={() => openRagTest(c)}
                  title="Test RAG"
                >
                  <Search className="h-4 w-4" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  onClick={() => rescanSource(c.id)}
                  disabled={rescanningId === c.id}
                  title="Re-scan for business context"
                >
                  <ScanSearch className={`h-4 w-4 ${rescanningId === c.id ? "animate-pulse" : ""}`} />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  onClick={() => regenerateDescription(c.id)}
                  disabled={syncingId === c.id}
                  title="Regenerate description"
                >
                  <RefreshCw className={`h-4 w-4 ${syncingId === c.id ? "animate-spin" : ""}`} />
                </Button>
                <Button variant="ghost" size="icon" onClick={() => remove(c.id)} title="Delete">
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>
            </div>
          ))}
        </CardContent>
      </Card>

      <Sheet open={!!ragConn} onOpenChange={(o) => !o && setRagConn(null)}>
        <SheetContent side="right" className="sm:max-w-2xl w-full overflow-y-auto">
          <SheetHeader>
            <SheetTitle>
              Test RAG — {ragConn?.label || ragConn?.db_type}
            </SheetTitle>
            <SheetDescription>
              Probe the embedding index. indexed_tables ≈ 250–300 healthy; 0–1 means reindex first.
            </SheetDescription>
          </SheetHeader>

          <div className="mt-6 space-y-4">
            <div className="space-y-1.5">
              <Label>Query</Label>
              <Textarea
                value={ragQuery}
                onChange={(e) => setRagQuery(e.target.value)}
                rows={3}
                autoFocus
                placeholder="berapa total penjualan bulan ini"
              />
            </div>
            <div className="flex items-end gap-3">
              <div className="space-y-1.5">
                <Label>top_k</Label>
                <Input
                  type="number"
                  min={1}
                  max={50}
                  value={ragTopK}
                  onChange={(e) => setRagTopK(e.target.value)}
                  className="w-24"
                />
              </div>
              <Button
                onClick={runRagTest}
                disabled={!ragQuery.trim() || ragLoading}
              >
                {ragLoading ? "Running…" : "Run test"}
              </Button>
            </div>

            {ragError && <p className="text-sm text-destructive">{ragError}</p>}

            {ragResult && (
              <div className="space-y-4 pt-2">
                <div>
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant="secondary">total {ragResult.total_tables}</Badge>
                    <Badge
                      variant={ragResult.indexed_tables <= 1 ? "destructive" : "secondary"}
                    >
                      indexed {ragResult.indexed_tables}
                    </Badge>
                    <Badge variant="secondary">filtered {ragResult.filtered_tables}</Badge>
                  </div>
                  {(() => {
                    const hint = indexHealthHint(ragResult.indexed_tables);
                    return (
                      <p
                        className={`mt-1.5 text-xs ${
                          hint.tone === "destructive" ? "text-destructive" : "text-muted-foreground"
                        }`}
                      >
                        {hint.text}
                      </p>
                    );
                  })()}
                </div>

                <div>
                  <div className="text-xs font-medium text-muted-foreground mb-1.5">
                    Hits ({ragResult.hits.length})
                  </div>
                  <ul className="divide-y divide-border/50 rounded border border-border/50">
                    {ragResult.hits.map((h, i) => {
                      const noise = NOISE_TABLE_RE.test(h.table);
                      return (
                        <li
                          key={`${h.table}-${i}`}
                          className="flex items-center justify-between gap-2 px-3 py-2 text-sm"
                        >
                          <div className="flex items-center gap-2 min-w-0">
                            <span className="font-mono truncate">{h.table}</span>
                            {noise && (
                              <Badge variant="destructive" className="shrink-0">noise</Badge>
                            )}
                          </div>
                          <span className="font-mono text-xs text-muted-foreground shrink-0">
                            dist {h.distance.toFixed(3)}
                          </span>
                        </li>
                      );
                    })}
                  </ul>
                </div>

                <div className="flex flex-wrap items-center gap-2">
                  <Badge
                    variant={ragResult.embed_duration_ms < 50 ? "destructive" : "secondary"}
                    title={
                      ragResult.embed_duration_ms < 50
                        ? "Suspiciously fast — embedding API may be cached"
                        : "Real embedding call"
                    }
                  >
                    embed {ragResult.embed_duration_ms}ms
                  </Badge>
                  <Badge variant="secondary">topk {ragResult.topk_duration_ms}ms</Badge>
                  <Badge variant="secondary">schema {ragResult.schema_duration_ms}ms</Badge>
                </div>

                <details className="group">
                  <summary className="cursor-pointer text-sm text-muted-foreground hover:text-foreground">
                    Schema preview ({ragResult.schema_preview.length} chars)
                  </summary>
                  <pre className="whitespace-pre-wrap font-mono text-xs text-muted-foreground bg-muted/30 rounded p-3 mt-2 max-h-96 overflow-auto">
                    {ragResult.schema_preview}
                  </pre>
                </details>
              </div>
            )}
          </div>
        </SheetContent>
      </Sheet>
    </div>
  );
}
