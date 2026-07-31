import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Badge } from "@/components/ui/badge";
import { CheckCircle2 } from "lucide-react";
import { useModels } from "@/lib/use-models";
import { apiErrorMessage } from "@/lib/api-error";
import { BusinessProfileCard } from "./business-profile-card";

export function GeneralTab() {
  const [currency, setCurrency] = useState("USD");
  const [currencies, setCurrencies] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [loaded, setLoaded] = useState(false);
  const { data: models } = useModels();

  useEffect(() => {
    api.get("/settings").then((r) => {
      setCurrency(r.data.default_currency ?? "USD");
      setCurrencies((r.data.supported_currencies ?? []).sort());
      setLoaded(true);
    });
  }, []);

  async function save() {
    setSaving(true);
    setSaved(false);
    setError(null);
    try {
      await api.put("/settings", { default_currency: currency });
      setSaved(true);
      setTimeout(() => setSaved(false), 3000);
    } catch (e: unknown) {
      setError(apiErrorMessage(e));
    } finally {
      setSaving(false);
    }
  }

  if (!loaded) return null;

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Company preferences</CardTitle>
          <CardDescription>
            These settings affect how the analytics agent formats responses.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="space-y-1.5 max-w-xs">
            <Label>Default currency</Label>
            <Select value={currency} onValueChange={setCurrency}>
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
            <p className="text-xs text-muted-foreground">
              ISO 4217 code used by the agent to format monetary values in responses.
            </p>
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
        </CardContent>
        <CardFooter className="gap-2">
          <Button onClick={save} disabled={saving}>
            {saving ? "Saving…" : "Save"}
          </Button>
          {saved && (
            <Badge variant="secondary" className="gap-1">
              <CheckCircle2 className="h-3 w-3 text-green-600" /> Saved
            </Badge>
          )}
        </CardFooter>
      </Card>

      <BusinessProfileCard />

      {models && (
        <Card>
          <CardHeader>
            <CardTitle>AI Models</CardTitle>
            <CardDescription>
              Models currently powering Argentum's analytics agent. Configured server-side.
            </CardDescription>
          </CardHeader>
          <CardContent className="divide-y divide-border">
            {(["primary", "light", "classifier"] as const).map((role) => {
              const m = models[role];
              if (!m) return null;
              return (
                <div
                  key={role}
                  className="flex items-center justify-between gap-4 py-3 first:pt-0 last:pb-0"
                >
                  <div>
                    <p className="text-sm font-medium capitalize">{role}</p>
                    <p className="text-xs text-muted-foreground font-mono">
                      {m.model}
                    </p>
                  </div>
                  <div className="text-right">
                    <Badge variant="secondary">{m.interface}</Badge>
                    <p className="text-xs text-muted-foreground mt-1">
                      {m.pricing_known
                        ? `$${m.input_per_1k_usd}/1k in · $${m.output_per_1k_usd}/1k out`
                        : "Pricing N/A"}
                    </p>
                  </div>
                </div>
              );
            })}
          </CardContent>
        </Card>
      )}
    </div>
  );
}
