import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, Sparkles } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Textarea } from "@/components/ui/textarea";
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
import type {
  CompanyProfileResponse,
  ProfileSuggestionResponse,
} from "@argentum/api-types";

/** Mirrors app.ProfileInput. Every field is replaced on save — this is a form
 *  with four inputs, not a patch API. */
interface ProfileDraft {
  industry: string;
  description: string;
  context_notes: string;
  fiscal_year_start_month: number;
}

const EMPTY_DRAFT: ProfileDraft = {
  industry: "",
  description: "",
  context_notes: "",
  fiscal_year_start_month: 1,
};

const MONTHS = [
  "January", "February", "March", "April", "May", "June",
  "July", "August", "September", "October", "November", "December",
];

/** Dismissal lives in the browser, not in a column (T-B2).
 *
 *  "I have seen this suggestion and do not want it" is a fact about a person
 *  looking at a screen, not about the company — and the server has nowhere to
 *  put it for a workspace that has never saved a profile. The key carries the
 *  draft's timestamp so a re-scan that produces a newer draft asks again, which
 *  is the whole point of pressing Re-scan. */
const dismissKey = (inferredAt?: string) => `argentum.profile-suggestion.${inferredAt ?? "none"}`;

/** Business profile — Settings → General (T-B1).
 *
 *  What the workspace does, in the tenant's own words, read by every agent on
 *  every turn. The rendered block is shown back verbatim: this text lands in
 *  the most privileged part of the request, and a prompt fragment nobody can
 *  read is a prompt fragment nobody can debug. */
export function BusinessProfileCard() {
  const qc = useQueryClient();
  const [draft, setDraft] = useState<ProfileDraft>(EMPTY_DRAFT);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [dismissed, setDismissed] = useState(false);

  const { data, isLoading } = useQuery({
    queryKey: ["company-profile"],
    queryFn: async () => (await api.get<CompanyProfileResponse>("/company/profile")).data,
  });

  // The form binds to the server's copy once it arrives, and again after every
  // save — the response carries the stored profile, so what is on screen is
  // what the agent will read rather than what was typed at it.
  useEffect(() => {
    const p = data?.profile;
    if (!p) return;
    setDraft({
      industry: p.industry ?? "",
      description: p.description ?? "",
      context_notes: p.context_notes ?? "",
      fiscal_year_start_month: p.fiscal_year_start_month || 1,
    });
  }, [data]);

  const save = useMutation({
    mutationFn: async () =>
      (await api.put<CompanyProfileResponse>("/company/profile", draft)).data,
    onSuccess: (res) => {
      setError(null);
      setSaved(true);
      setTimeout(() => setSaved(false), 3000);
      qc.setQueryData(["company-profile"], res);
    },
    onError: (e: unknown) => setError(apiErrorMessage(e, "Could not save the business profile")),
  });

  // What the connected sources say the business is (T-B2). A draft, never the
  // profile: nothing here is what the agent reads until Apply writes it.
  const { data: suggestion } = useQuery({
    queryKey: ["company-profile-suggestion"],
    queryFn: async () =>
      (await api.get<ProfileSuggestionResponse>("/company/profile/suggestion")).data,
  });

  const apply = useMutation({
    mutationFn: async () =>
      (await api.post<CompanyProfileResponse>("/company/profile/suggestion/apply")).data,
    onSuccess: (res) => {
      setError(null);
      qc.setQueryData(["company-profile"], res);
      qc.invalidateQueries({ queryKey: ["company-profile-suggestion"] });
    },
    onError: (e: unknown) =>
      setError(apiErrorMessage(e, "Could not apply the suggested profile")),
  });

  // A draft dismissed in an earlier session stays dismissed; a newer draft —
  // different timestamp, different key — comes back.
  useEffect(() => {
    if (!suggestion?.draft) return;
    setDismissed(localStorage.getItem(dismissKey(suggestion.draft.inferred_at)) === "1");
  }, [suggestion]);

  function dismissSuggestion() {
    localStorage.setItem(dismissKey(suggestion?.draft?.inferred_at), "1");
    setDismissed(true);
  }

  if (isLoading) return null;

  // The panel is offered only while there is nothing of the tenant's to lose.
  // Apply refuses to overwrite a description somebody typed, and a button that
  // is going to be refused should not be on the screen.
  const profileHasText = Boolean(
    data?.profile?.industry || data?.profile?.description || data?.profile?.context_notes,
  );
  const ownWords = profileHasText && data?.profile?.source !== "inferred";
  // …and it retires once this exact draft *is* the profile. Matched on
  // inferred_at rather than on the source alone: a later re-scan produces a
  // different timestamp, and that suggestion is a new one worth offering.
  const appliedThisDraft =
    data?.profile?.source === "inferred" &&
    Boolean(suggestion?.draft?.inferred_at) &&
    data?.profile?.inferred_at === suggestion?.draft?.inferred_at;
  const showSuggestion =
    Boolean(suggestion?.draft) && !ownWords && !appliedThisDraft && !dismissed;

  const inferred = data?.profile?.source !== "human" && data?.exists;
  const inferredAt = data?.profile?.inferred_at
    ? new Date(data.profile.inferred_at).toLocaleDateString()
    : null;

  return (
    <Card>
      <CardHeader>
        <CardTitle>Business profile</CardTitle>
        <CardDescription>
          What this company does, in your words. Every agent reads it before answering, so it can
          talk about your stores, your seasons and your terms instead of table names.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {/* Suggested from the connected data (T-B2). It is a draft until the
            tenant presses Apply — nothing here is what the agent reads, and the
            block below the buttons is exactly what it would read if they did. */}
        {showSuggestion && (
          <div className="space-y-3 rounded-md border border-primary/30 bg-primary/5 p-3">
            <div className="flex items-start gap-2">
              <Sparkles className="mt-0.5 h-4 w-4 shrink-0 text-primary" />
              <div className="space-y-1">
                <p className="text-sm font-medium">Suggested from your data</p>
                <p className="text-xs text-muted-foreground">
                  We read the table and column names in your connected{" "}
                  {suggestion?.sources === 1 ? "source" : `${suggestion?.sources} sources`} — never
                  the rows — and drafted this. Nothing is saved until you apply it, and you can edit
                  every word afterwards.
                </p>
              </div>
            </div>

            <div className="space-y-1.5 text-xs">
              {suggestion?.draft?.industry && (
                <p>
                  <span className="text-muted-foreground">Industry: </span>
                  {suggestion.draft.industry}
                </p>
              )}
              {suggestion?.draft?.description && (
                <p className="whitespace-pre-wrap">{suggestion.draft.description}</p>
              )}
              {suggestion?.draft?.context_notes && (
                <pre className="max-h-40 overflow-auto whitespace-pre-wrap font-mono text-[11px] text-muted-foreground">
                  {suggestion.draft.context_notes}
                </pre>
              )}
            </div>

            <div className="flex items-center gap-2">
              <Button size="sm" onClick={() => apply.mutate()} disabled={apply.isPending}>
                {apply.isPending ? "Applying…" : "Apply"}
              </Button>
              <Button size="sm" variant="ghost" onClick={dismissSuggestion}>
                Dismiss
              </Button>
            </div>
          </div>
        )}

        {/* The balance is the one reason for silence a tenant can act on, and
            the only one they would otherwise read as a broken feature. */}
        {suggestion?.credits_exhausted && !suggestion?.draft && (
          <p className="rounded-md border border-border bg-muted/40 p-3 text-xs text-muted-foreground">
            We could not read your connected data to suggest a profile — this workspace is out of
            credit. Your sources still work; describe the business below in your own words, or top
            up and press Re-scan under Settings → Data sources.
          </p>
        )}

        {/* Provenance, when there is any to report. An inferred profile the
            tenant has never looked at must not read as something they wrote. */}
        {inferred && (
          <div className="flex items-start gap-2 rounded-md border border-border bg-muted/40 p-3 text-xs text-muted-foreground">
            <Sparkles className="mt-0.5 h-3.5 w-3.5 shrink-0" />
            <span>
              {data?.profile?.source === "inferred"
                ? `Suggested from your connected data${inferredAt ? ` on ${inferredAt}` : ""} — review it.`
                : "Suggested from your data and edited here."}
            </span>
          </div>
        )}

        <div className="grid grid-cols-[1fr_1fr] gap-4">
          <div className="space-y-1.5">
            <Label htmlFor="profile-industry">Industry</Label>
            <Input
              id="profile-industry"
              value={draft.industry}
              onChange={(e) => setDraft({ ...draft, industry: e.target.value })}
              placeholder="Grocery retail"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="profile-fiscal">Fiscal year starts</Label>
            <Select
              value={String(draft.fiscal_year_start_month)}
              onValueChange={(v) => setDraft({ ...draft, fiscal_year_start_month: Number(v) })}
            >
              <SelectTrigger id="profile-fiscal">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {MONTHS.map((m, i) => (
                  <SelectItem key={m} value={String(i + 1)}>
                    {m}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <p className="text-xs text-muted-foreground">
              Decides what "last quarter" means in an answer.
            </p>
          </div>
        </div>

        <div className="space-y-1.5">
          <Label htmlFor="profile-description">What the business does</Label>
          <Textarea
            id="profile-description"
            rows={4}
            value={draft.description}
            onChange={(e) => setDraft({ ...draft, description: e.target.value })}
            placeholder="We run 38 grocery stores across Java. Most revenue is in-store; the rest is a delivery app we launched last year."
          />
        </div>

        <div className="space-y-1.5">
          <Label htmlFor="profile-notes">Anything else the agent should know</Label>
          <Textarea
            id="profile-notes"
            rows={4}
            value={draft.context_notes}
            onChange={(e) => setDraft({ ...draft, context_notes: e.target.value })}
            placeholder="Basket size means items per order, not rupiah. December is our peak. A stock-out is what the ops lead asks about first."
          />
          <p className="text-xs text-muted-foreground">
            Facts about the business, not instructions for the agent — those belong on an agent's
            own instructions in Settings → Agents.
          </p>
        </div>

        {error && <p className="text-sm text-destructive">{error}</p>}

        {/* The block, exactly as composed for the model. */}
        {data?.rendered_block && (
          <div className="space-y-1.5">
            <div className="flex items-center gap-2">
              <Label>What the agent reads</Label>
              {data.truncated && (
                <Badge variant="destructive">
                  Truncated at {data.block_token_limit} tokens
                </Badge>
              )}
            </div>
            <pre className="max-h-56 overflow-auto whitespace-pre-wrap rounded-md border border-border bg-muted/40 p-3 font-mono text-xs text-muted-foreground">
              {data.rendered_block}
            </pre>
            {data.truncated && (
              <p className="text-xs text-destructive">
                Your profile is longer than the {data.block_token_limit}-token allowance. The
                agent sees the text above and nothing after it — shorten this to choose what
                survives.
              </p>
            )}
          </div>
        )}
      </CardContent>
      <CardFooter className="gap-2">
        <Button onClick={() => save.mutate()} disabled={save.isPending}>
          {save.isPending ? "Saving…" : "Save"}
        </Button>
        {saved && (
          <Badge variant="secondary" className="gap-1">
            <CheckCircle2 className="h-3 w-3 text-green-600" /> Saved
          </Badge>
        )}
      </CardFooter>
    </Card>
  );
}
