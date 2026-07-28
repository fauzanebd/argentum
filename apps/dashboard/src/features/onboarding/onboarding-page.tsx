import { useState, useEffect } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { api } from "@/lib/api";
import { CheckCircle2, XCircle } from "lucide-react";
import { apiErrorMessage } from "@/lib/api-error";

const schema = z.object({
  db_type: z.string().min(1, "Pick a database type"),
  label: z.string().optional(),
  default_currency: z.string().min(1, "Pick a currency"),
});

type ConnMode = "fields" | "raw";

function defaultPort(dbType: string) {
  if (dbType === "mysql") return "3306";
  return "5432";
}

type FormValues = z.infer<typeof schema>;

export function OnboardingPage() {
  const navigate = useNavigate();
  const [supported, setSupported] = useState<string[]>([]);
  const [currencies, setCurrencies] = useState<string[]>([]);
  const [mode, setMode] = useState<ConnMode>("fields");
  const [dsn, setDsn] = useState("");
  const [host, setHost] = useState("");
  const [port, setPort] = useState(defaultPort("postgres"));
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [dbname, setDbname] = useState("");
  const [testStatus, setTestStatus] = useState<"idle" | "ok" | "error" | "testing">("idle");
  const [testError, setTestError] = useState<string | null>(null);

  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: { db_type: "postgres", label: "Production analytics", default_currency: "USD" },
  });

  const dbType = form.watch("db_type");

  useEffect(() => {
    api.get("/meta/supported-databases").then((r) => setSupported(r.data.registered ?? r.data.supported ?? []));
    api.get("/settings").then((r) => {
      setCurrencies((r.data.supported_currencies ?? []).sort());
      if (r.data.default_currency) {
        form.setValue("default_currency", r.data.default_currency);
      }
    }).catch(() => {
      setCurrencies(["USD", "EUR", "GBP", "IDR", "SGD", "JPY"].sort());
    });
  }, []);

  useEffect(() => {
    setPort(defaultPort(dbType));
  }, [dbType]);

  function connectionPayload() {
    if (mode === "raw") {
      return { db_type: dbType, dsn };
    }
    return {
      db_type: dbType,
      host,
      port,
      username,
      password,
      dbname,
    };
  }

  function validateConnection(): string | null {
    if (mode === "raw") {
      if (!dsn.trim()) return "DSN is required";
      return null;
    }
    if (!host.trim()) return "Host is required";
    if (!port.trim()) return "Port is required";
    if (!dbname.trim()) return "Database name is required";
    return null;
  }

  async function testConnection() {
    const err = validateConnection();
    if (err) {
      setTestStatus("error");
      setTestError(err);
      return;
    }
    setTestStatus("testing");
    setTestError(null);
    try {
      const res = await api.post("/connections/test", connectionPayload());
      if (res.data.ok) {
        setTestStatus("ok");
      } else {
        setTestStatus("error");
        setTestError(res.data.error || "Connection test failed");
      }
    } catch (e: unknown) {
      setTestStatus("error");
      setTestError(apiErrorMessage(e));
    }
  }

  async function onSubmit(values: FormValues) {
    const err = validateConnection();
    if (err) {
      setTestStatus("error");
      setTestError(err);
      return;
    }
    await api.post("/connections", { ...connectionPayload(), label: values.label, is_default: true });
    await api.put("/settings", { default_currency: values.default_currency });
    navigate({ to: "/chat" });
  }

  return (
    <div className="h-full overflow-y-auto">
      <div className="max-w-2xl mx-auto py-12 px-6">
        <div className="mb-8">
          <h1 className="text-3xl font-bold">Connect your database</h1>
          <p className="text-muted-foreground mt-1">
            Argentum needs read-only access to your analytics database. We'll never write to it — every query
            is wrapped in a read-only transaction.
          </p>
        </div>

        <Card>
          <form onSubmit={form.handleSubmit(onSubmit)}>
            <CardHeader>
              <CardTitle>Database connection</CardTitle>
              <CardDescription>You can change or add more connections later in Settings.</CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="space-y-1.5">
                <Label>Database type</Label>
                <Select
                  value={form.watch("db_type")}
                  onValueChange={(v) => form.setValue("db_type", v)}
                >
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
                <Label htmlFor="label">Label (optional)</Label>
                <Input id="label" {...form.register("label")} />
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
                  <Label htmlFor="dsn">Connection string (DSN)</Label>
                  <Input
                    id="dsn"
                    placeholder="postgres://user:pass@host:5432/db?sslmode=require"
                    value={dsn}
                    onChange={(e) => setDsn(e.target.value)}
                  />
                </div>
              )}

              <p className="text-xs text-muted-foreground">
                Stored encrypted at rest with AES-256-GCM. Only the database type is plaintext.
              </p>

              <div className="flex items-center gap-3">
                <Button type="button" variant="outline" onClick={testConnection} disabled={testStatus === "testing"}>
                  {testStatus === "testing" ? "Testing..." : "Test connection"}
                </Button>
                {testStatus === "ok" && (
                  <Badge variant="secondary" className="gap-1">
                    <CheckCircle2 className="h-3 w-3 text-green-600" /> Connected
                  </Badge>
                )}
                {testStatus === "error" && (
                  <Badge variant="destructive" className="gap-1">
                    <XCircle className="h-3 w-3" /> {testError ?? "Failed"}
                  </Badge>
                )}
              </div>

              <div className="border-t border-border pt-4 space-y-1.5">
                <Label>Default currency</Label>
                <Select
                  value={form.watch("default_currency")}
                  onValueChange={(v) => form.setValue("default_currency", v)}
                >
                  <SelectTrigger>
                    <SelectValue placeholder="Select currency" />
                  </SelectTrigger>
                  <SelectContent>
                    {currencies.map((c) => (
                      <SelectItem key={c} value={c}>
                        {c}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                {form.formState.errors.default_currency && (
                  <p className="text-xs text-destructive">{form.formState.errors.default_currency.message}</p>
                )}
                <p className="text-xs text-muted-foreground">
                  The agent uses this to format monetary values in responses.
                  You can change this later in Settings.
                </p>
              </div>
            </CardContent>
            <CardFooter>
              <Button type="submit" disabled={testStatus !== "ok"}>
                Save and continue
              </Button>
            </CardFooter>
          </form>
        </Card>
      </div>
    </div>
  );
}
