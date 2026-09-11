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

// What each redaction policy means, in the terms the person choosing it thinks
// in. The values come from the API (`pii_redaction_modes`); the copy is here,
// because a mode with no explanation is a setting nobody dares change.
const PII_MODE_COPY: Record<string, { label: string; hint: string }> = {
  strict: {
    label: "Strict — redact all personal data",
    hint: "Emails, phone numbers, national IDs and card numbers are removed from every answer.",
  },
  contact_ok: {
    label: "Contact details allowed",
    hint: "Customer emails and phone numbers appear in answers, so a contact list is usable. National IDs and card numbers are still removed.",
  },
  off: {
    label: "Off — no redaction",
    hint: "Answers are returned as the agent wrote them. Choose this only if your warehouse holds no personal data you need protected.",
  },
};

/** The two conventions a money figure can be rounded under (T-W2).
 *
 *  Both are offered rather than one being assumed, because which one applies is
 *  an accounting policy and not a fact about the money — several standards
 *  require half-even precisely so a long ledger does not drift upward. The
 *  default is half-up because that is what every answer this product has given
 *  so far already did. */
const ROUNDING_MODES = [
  {
    value: "half_up",
    label: "Half up — 2.5 becomes 3",
    hint: "Rounds a half away from zero, the way a spreadsheet does. The right choice unless your accounting standard says otherwise.",
  },
  {
    value: "half_even",
    label: "Half even — 2.5 becomes 2, 3.5 becomes 4",
    hint: "Banker's rounding. Rounds a half to the nearest even digit, so a long ledger does not drift upward. Required by several accounting standards.",
  },
];

function decimalPlaces(n: number): string {
  if (n === 0) return "no decimal places";
  if (n === 1) return "1 decimal place";
  return `${n} decimal places`;
}

export function GeneralTab() {
  const [currency, setCurrency] = useState("USD");
  const [currencies, setCurrencies] = useState<string[]>([]);
  // How money is written, not merely which money it is (T-W2). The precision
  // comes from the server rather than a table in this file: a second copy is a
  // second place for IDR to quietly become two decimal places.
  const [precisions, setPrecisions] = useState<Record<string, number>>({});
  const [rounding, setRounding] = useState("half_up");
  const [piiMode, setPiiMode] = useState("strict");
  const [piiModes, setPiiModes] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [loaded, setLoaded] = useState(false);
  const { data: models } = useModels();

  useEffect(() => {
    api.get("/settings").then((r) => {
      setCurrency(r.data.default_currency ?? "USD");
      setCurrencies((r.data.supported_currencies ?? []).sort());
      setPrecisions(r.data.currency_precisions ?? {});
      setRounding(r.data.currency_rounding || "half_up");
      setPiiMode(r.data.pii_redaction_mode ?? "strict");
      setPiiModes(r.data.pii_redaction_modes ?? []);
      setLoaded(true);
    });
  }, []);

  async function save() {
    setSaving(true);
    setSaved(false);
    setError(null);
    try {
      await api.put("/settings", {
        default_currency: currency,
        currency_rounding: rounding,
        pii_redaction_mode: piiMode,
      });
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
              {precisions[currency] !== undefined && ` ${currency} is written with ${decimalPlaces(precisions[currency])}.`}
            </p>
          </div>
          <div className="space-y-1.5 max-w-md">
            <Label>Rounding</Label>
            <Select value={rounding} onValueChange={setRounding}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {ROUNDING_MODES.map((m) => (
                  <SelectItem key={m.value} value={m.value}>
                    {m.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <p className="text-xs text-muted-foreground">
              {ROUNDING_MODES.find((m) => m.value === rounding)?.hint}
            </p>
          </div>
          {piiModes.length > 0 && (
            <div className="space-y-1.5 max-w-md">
              <Label>Personal data in answers</Label>
              <Select value={piiMode} onValueChange={setPiiMode}>
                <SelectTrigger>
                  <SelectValue placeholder="Select a policy" />
                </SelectTrigger>
                <SelectContent>
                  {piiModes.map((m) => (
                    <SelectItem key={m} value={m}>
                      {PII_MODE_COPY[m]?.label ?? m}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <p className="text-xs text-muted-foreground">
                {PII_MODE_COPY[piiMode]?.hint ??
                  "Controls which personal data the agent may include in an answer."}
              </p>
            </div>
          )}
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
