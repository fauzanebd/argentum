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

const schema = z.object({
  db_type: z.string().min(1, "Pick a database type"),
  label: z.string().optional(),
  dsn: z.string().min(8, "DSN required"),
  default_currency: z.string().min(1, "Pick a currency"),
});
type FormValues = z.infer<typeof schema>;

export function OnboardingPage() {
  const navigate = useNavigate();
  const [supported, setSupported] = useState<string[]>([]);
  const [currencies, setCurrencies] = useState<string[]>([]);
  const [testStatus, setTestStatus] = useState<"idle" | "ok" | "error" | "testing">("idle");
  const [testError, setTestError] = useState<string | null>(null);

  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: { db_type: "postgres", label: "Production analytics", default_currency: "USD" },
  });

  useEffect(() => {
    api.get("/meta/supported-databases").then((r) => setSupported(r.data.registered ?? r.data.supported ?? []));
    api.get("/settings").then((r) => {
      setCurrencies((r.data.supported_currencies ?? []).sort());
      if (r.data.default_currency) {
        form.setValue("default_currency", r.data.default_currency);
      }
    }).catch(() => {
      // Settings endpoint may fail if the company isn't fully set up yet.
      // Fall back to a reasonable default list.
      setCurrencies(["USD", "EUR", "GBP", "IDR", "SGD", "JPY"].sort());
    });
  }, []);

  async function testConnection() {
    setTestStatus("testing");
    setTestError(null);
    try {
      const res = await api.post("/connections/test", {
        db_type: form.getValues("db_type"),
        dsn: form.getValues("dsn"),
      });
      // Sometimes API returns { ok: true } even on 200 with bad creds.
      if (res.data.ok) {
        setTestStatus("ok");
      } else {
        setTestStatus("error");
        setTestError(res.data.error || "Connection test failed");
      }
    } catch (e: any) {
      setTestStatus("error");
      setTestError(e?.response?.data?.error || e.message);
    }
  }

  async function onSubmit(values: FormValues) {
    await api.post("/connections", { ...values, is_default: true });
    // Save the selected currency
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
              <div className="space-y-1.5">
                <Label htmlFor="dsn">Connection string (DSN)</Label>
                <Input
                  id="dsn"
                  placeholder="postgres://user:pass@host:5432/db?sslmode=require"
                  {...form.register("dsn")}
                />
                {form.formState.errors.dsn && (
                  <p className="text-xs text-destructive">{form.formState.errors.dsn.message}</p>
                )}
                <p className="text-xs text-muted-foreground">
                  Stored encrypted at rest with AES-256-GCM. Only the database type is plaintext.
                </p>
              </div>
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
