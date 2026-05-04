import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2, Star } from "lucide-react";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

interface Connection {
  id: string;
  db_type: string;
  label?: string;
  is_default: boolean;
  created_at: string;
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
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const { data: connections } = useQuery({
    queryKey: ["connections"],
    queryFn: async () => (await api.get<{ connections: Connection[] }>("/connections")).data.connections,
  });

  useEffect(() => {
    api.get("/meta/supported-databases").then((r) => setSupported(r.data.registered ?? r.data.supported ?? []));
  }, []);

  useEffect(() => {
    setPort(defaultPort(dbType));
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
    };
  }

  async function testConnection() {
    setTesting(true);
    setTestResult(null);
    try {
      const res = await api.post("/connections/test", payload());
      setTestResult(res.data.ok ? "ok" : res.data.error || "Failed");
    } catch (e: any) {
      setTestResult(e?.response?.data?.error || e.message);
    } finally {
      setTesting(false);
    }
  }

  async function add() {
    setError(null);
    try {
      await api.post("/connections", { ...payload(), is_default: connections?.length === 0 });
      setLabel("");
      setDsn("");
      setHost("");
      setPort(defaultPort(dbType));
      setUsername("");
      setPassword("");
      setDbname("");
      setTestResult(null);
      qc.invalidateQueries({ queryKey: ["connections"] });
    } catch (e: any) {
      setError(e?.response?.data?.error || e.message);
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
          {error && <p className="text-sm text-destructive">{error}</p>}
        </CardContent>
        <CardFooter className="gap-2">
          <Button variant="outline" disabled={testing || !ready} onClick={testConnection}>
            {testing ? "Testing…" : "Test"}
          </Button>
          <Button onClick={add} disabled={!ready}>
            Add database
          </Button>
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
                {!c.is_default && (
                  <Button variant="outline" size="sm" onClick={() => makeDefault(c.id)}>
                    Make default
                  </Button>
                )}
                <Button variant="ghost" size="icon" onClick={() => remove(c.id)} title="Delete">
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
