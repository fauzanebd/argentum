import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
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

export interface SourceFreshness {
  sql?: string;
  warn_after_mins?: number;
  stale_after_mins?: number;
}

interface TestResult {
  verdict: "unknown" | "fresh" | "warn" | "stale";
  observed_at?: string;
  age_seconds?: number;
  note?: string;
  error?: string;
}

/** The verdict a reader should act on, and the tone that says so. `unknown` is
 *  deliberately not destructive: it means nothing is claimed, which is the state
 *  every source starts in and is not a failure. */
const VERDICT_TONE: Record<TestResult["verdict"], "secondary" | "destructive"> = {
  unknown: "secondary",
  fresh: "secondary",
  warn: "secondary",
  stale: "destructive",
};

/** Enough of a starting point to edit rather than to invent. Deliberately
 *  commented as an example and not pre-filled into the field — a default
 *  expression that silently referenced a table the tenant does not have would
 *  produce `unknown` forever and look like the feature is broken. */
const EXAMPLE = "SELECT max(loaded_at) FROM etl_runs";

function minsField(v?: number) {
  return v && v > 0 ? String(v) : "";
}

/**
 * SourceFreshnessSheet configures how a source reports when it last loaded, and
 * how old is too old (T-F1/T-F2).
 *
 * The Test button is not a convenience. It is the only place the *parsed*
 * instant is shown, which is how somebody in Jakarta discovers that their
 * timezone-naive `loaded_at` is being read as UTC — an age seven hours out,
 * found here rather than from a wrong caveat on an answer weeks later.
 */
export function SourceFreshnessSheet({
  sourceId,
  label,
  initial,
  open,
  onOpenChange,
  onSaved,
}: {
  sourceId: string | null;
  label: string;
  initial?: SourceFreshness;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSaved: () => void;
}) {
  const [sql, setSql] = useState("");
  const [warn, setWarn] = useState("");
  const [stale, setStale] = useState("");
  const [testing, setTesting] = useState(false);
  const [saving, setSaving] = useState(false);
  const [result, setResult] = useState<TestResult | null>(null);

  useEffect(() => {
    if (!open) return;
    setSql(initial?.sql ?? "");
    setWarn(minsField(initial?.warn_after_mins));
    setStale(minsField(initial?.stale_after_mins));
    setResult(null);
  }, [open, initial]);

  function body(): SourceFreshness {
    return {
      sql: sql.trim(),
      warn_after_mins: Number(warn) || 0,
      stale_after_mins: Number(stale) || 0,
    };
  }

  async function runTest() {
    if (!sourceId) return;
    setTesting(true);
    setResult(null);
    try {
      const res = await api.post<TestResult>(`/connections/${sourceId}/freshness/test`, body());
      setResult(res.data);
    } catch (e) {
      toast({ title: "Test failed", description: apiErrorMessage(e), variant: "destructive" });
    } finally {
      setTesting(false);
    }
  }

  async function save() {
    if (!sourceId) return;
    setSaving(true);
    try {
      await api.put(`/connections/${sourceId}/freshness`, body());
      toast({
        title: sql.trim() ? "Freshness saved" : "Freshness checking turned off",
        description: sql.trim()
          ? "Answers drawn from this source will say how old the data is."
          : "This source no longer reports when it last loaded.",
      });
      onSaved();
      onOpenChange(false);
    } catch (e) {
      toast({ title: "Could not save", description: apiErrorMessage(e), variant: "destructive" });
    } finally {
      setSaving(false);
    }
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="w-full sm:max-w-xl overflow-y-auto">
        <SheetHeader>
          <SheetTitle>Data freshness — {label}</SheetTitle>
          <SheetDescription>
            A query that answers “when did this data last load?”. Argentum runs it alongside
            the agent’s own queries and dates any answer drawn from a source that has not
            refreshed recently.
          </SheetDescription>
        </SheetHeader>

        <div className="mt-6 space-y-4">
          <div className="space-y-1.5">
            <Label>Freshness query</Label>
            <Textarea
              value={sql}
              onChange={(e) => setSql(e.target.value)}
              rows={3}
              spellCheck={false}
              className="font-mono text-xs"
              placeholder={EXAMPLE}
            />
            <p className="text-xs text-muted-foreground">
              Must be a single SELECT returning exactly one row and one column: a timestamp.
              Leave it empty to turn freshness checking off — answers will then say nothing
              about how current this source is.
            </p>
          </div>

          <div className="flex items-end gap-3">
            <div className="space-y-1.5">
              <Label>Date answers after</Label>
              <Input
                type="number"
                min={0}
                value={warn}
                onChange={(e) => setWarn(e.target.value)}
                className="w-28"
                placeholder="120"
              />
            </div>
            <div className="space-y-1.5">
              <Label>Warn users after</Label>
              <Input
                type="number"
                min={0}
                value={stale}
                onChange={(e) => setStale(e.target.value)}
                className="w-28"
                placeholder="1440"
              />
            </div>
            <div className="pb-2 text-xs text-muted-foreground">minutes</div>
          </div>
          <p className="text-xs text-muted-foreground">
            Past the first threshold an answer mentions when the data is from. Past the
            second, Argentum adds a note saying the source has not refreshed — so a recent
            period that looks empty is not read as a business result.
          </p>

          <div className="flex items-center gap-3 pt-1">
            <Button variant="outline" onClick={runTest} disabled={!sql.trim() || testing}>
              {testing ? "Running…" : "Test"}
            </Button>
            <Button onClick={save} disabled={saving}>
              {saving ? "Saving…" : "Save"}
            </Button>
          </div>

          {result && (
            <div className="space-y-2 rounded border border-border/50 p-3">
              <div className="flex items-center gap-2">
                <Badge variant={VERDICT_TONE[result.verdict]}>{result.verdict}</Badge>
                {result.observed_at && (
                  <span className="text-xs text-muted-foreground">
                    data as of {new Date(result.observed_at).toLocaleString()}
                  </span>
                )}
              </div>
              {result.error && <p className="text-sm text-destructive">{result.error}</p>}
              {result.note && <p className="text-xs text-muted-foreground">{result.note}</p>}
              {!result.error && result.verdict === "unknown" && (
                <p className="text-xs text-muted-foreground">
                  Nothing is claimed about this source. That is what every answer will say
                  until the query above returns a single timestamp.
                </p>
              )}
            </div>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}
