import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2 } from "lucide-react";
import type { ChatModelOption, ChatModelSetting, ModelInfo, ModelsResponse } from "@argentum/api-types";
import { api } from "@/lib/api";
import { apiErrorMessage } from "@/lib/api-error";
import { MODELS_QUERY_KEY } from "@/lib/use-models";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

/**
 * Settings → General → AI Models (RIZ-97).
 *
 * The chat model is the one model a workspace chooses for itself: it writes
 * every answer, for every agent on every channel. Light and Classifier stay
 * read-only rows under it — guardrails are tuned to them, so they are
 * Argentum's to set.
 *
 * Its own Save, separate from Company preferences', so one failing does not
 * block the other. Members get the same card disabled by the AdminGate around
 * the General tab; nothing here checks the role, and the server decides.
 *
 * Prices are written per 1M tokens. The API sends the pricing table's per-1K
 * rates, and DeepSeek's per-1K input rate reads as "$0.00015" — a number
 * nobody can compare at a glance.
 */
export function ChatModelCard({ models }: { models: ModelsResponse }) {
  const setting = models.chat_model;
  return (
    <Card>
      <CardHeader>
        <CardTitle>AI Models</CardTitle>
        <CardDescription>
          {setting
            ? "The chat model writes every answer in this workspace, for every agent and every channel. The models below it run summaries and safety checks, and are set by Argentum."
            : "Models currently powering Argentum's analytics agent. Configured server-side."}
        </CardDescription>
      </CardHeader>
      {setting ? (
        <ChatModelPicker setting={setting} models={models} />
      ) : (
        // An API older than the picker (a rolling deploy): today's read-only
        // rows, the chat model among them.
        <CardContent>
          <ModelRow label="Primary" info={models.primary} first />
          <ModelRow label="Light" info={models.light} />
          <ModelRow label="Classifier" info={models.classifier} />
        </CardContent>
      )}
    </Card>
  );
}

function ChatModelPicker({ setting, models }: { setting: ChatModelSetting; models: ModelsResponse }) {
  const queryClient = useQueryClient();
  const current = setting.model;
  const [selected, setSelected] = useState(
    setting.options.some((o) => o.model === current) ? current : "",
  );
  const [savedLabel, setSavedLabel] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const save = useMutation({
    mutationFn: async (model: string) =>
      (await api.put<ChatModelSetting>("/config/chat-model", { model })).data,
    onSuccess: (next) => {
      // Write the answer into the cache before refetching, so Save disables
      // the moment the server says yes rather than a round trip later; the
      // refetch then brings `primary`, which is what the chat header reads.
      queryClient.setQueryData<ModelsResponse>(MODELS_QUERY_KEY, (old) =>
        old ? { ...old, chat_model: next } : old,
      );
      void queryClient.invalidateQueries({ queryKey: MODELS_QUERY_KEY });
      setSavedLabel(labelOf(next.options, next.model));
      setError(null);
    },
    onError: (e: unknown) => {
      setSavedLabel(null);
      setError(apiErrorMessage(e).replace(/\.+$/, ""));
    },
  });

  if (setting.locked) {
    return (
      <CardContent>
        <div className="pb-4">
          <div className="flex items-start justify-between gap-4">
            <div>
              <p className="text-sm font-medium">Chat model</p>
              <p className="mt-0.5 font-mono text-xs text-muted-foreground">{current}</p>
            </div>
            <Badge variant="secondary">Own provider</Badge>
          </div>
          <p className="mt-1.5 text-xs text-muted-foreground">
            This workspace runs on its own model provider, set up with Argentum support. To change its
            chat model, contact support.
          </p>
        </div>
        <ModelRow label="Light" info={models.light} />
        <ModelRow label="Classifier" info={models.classifier} />
      </CardContent>
    );
  }

  const option = setting.options.find((o) => o.model === selected);
  const unchanged = selected === "" || selected === current;

  return (
    <>
      <CardContent>
        <div className="pb-4">
          <Label htmlFor="chat-model">Chat model</Label>
          <Select
            value={selected}
            onValueChange={(v) => {
              setSelected(v);
              setSavedLabel(null);
              setError(null);
            }}
          >
            <SelectTrigger id="chat-model" className="mt-1.5 max-w-md">
              <SelectValue placeholder={current} />
            </SelectTrigger>
            <SelectContent>
              {setting.options.map((o) => (
                <SelectItem key={o.model} value={o.model} description={optionLine(o)}>
                  {optionLabel(o)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {option?.hint && <p className="mt-1.5 text-xs text-muted-foreground">{option.hint}</p>}
          <p className="mt-1.5 text-xs text-muted-foreground">
            {option ? pricePerMillionLong(option) : "Pricing N/A"} ·{" "}
            <span className="font-mono">{option?.model ?? current}</span>
          </p>
          <p className="mt-1.5 text-xs text-muted-foreground">
            A change applies from the next message. Answers already written stay as they are.
          </p>
          {error && (
            <p className="mt-2.5 text-sm text-destructive">
              Could not save the chat model: {error}. This workspace still uses{" "}
              {labelOf(setting.options, current)}.
            </p>
          )}
        </div>
        <ModelRow label="Light" info={models.light} />
        <ModelRow label="Classifier" info={models.classifier} />
      </CardContent>
      <CardFooter className="gap-2">
        <Button onClick={() => save.mutate(selected)} disabled={save.isPending || unchanged}>
          {save.isPending ? "Saving…" : "Save"}
        </Button>
        {savedLabel && (
          <>
            <Badge variant="secondary" className="gap-1">
              <CheckCircle2 className="h-3 w-3 text-green-600" /> Saved
            </Badge>
            <span className="text-xs text-muted-foreground">
              New messages use {savedLabel} from now on.
            </span>
          </>
        )}
      </CardFooter>
    </>
  );
}

/** One read-only model row: Light and Classifier, or all three on an old API. */
function ModelRow({ label, info, first }: { label: string; info?: ModelInfo; first?: boolean }) {
  if (!info) return null;
  return (
    <div
      className={
        first
          ? "flex items-center justify-between gap-4 pb-3"
          : "flex items-center justify-between gap-4 border-t border-border py-3 last:pb-0"
      }
    >
      <div>
        <p className="text-sm font-medium">{label}</p>
        <p className="font-mono text-xs text-muted-foreground">{info.model}</p>
      </div>
      <div className="text-right">
        <Badge variant="secondary">{info.interface}</Badge>
        <p className="mt-1 text-xs text-muted-foreground">
          {info.pricing_known ? `${pricePerMillionShort(info)} per 1M tokens` : "Pricing N/A"}
        </p>
      </div>
    </div>
  );
}

type Rates = { input_per_1k_usd: number; output_per_1k_usd: number };

/** A per-1K rate as dollars per 1M tokens: 0.00015 → "$0.15". Two places is
 *  enough for every model on offer; a rate under a cent per 1M keeps its
 *  significant digits rather than rounding to "$0.00". */
export function perMillion(per1k: number): string {
  const v = per1k * 1000;
  if (v > 0 && v < 0.01) return `$${Number(v.toPrecision(2))}`;
  return `$${v.toFixed(2)}`;
}

/** "$0.15 in · $0.60 out" — the dropdown and the read-only rows. */
export function pricePerMillionShort(r: Rates): string {
  return `${perMillion(r.input_per_1k_usd)} in · ${perMillion(r.output_per_1k_usd)} out`;
}

/** "$0.15 per 1M input tokens · $0.60 per 1M output tokens" — under the select. */
function pricePerMillionLong(o: ChatModelOption): string {
  if (!o.pricing_known) return "Pricing N/A";
  return `${perMillion(o.input_per_1k_usd)} per 1M input tokens · ${perMillion(o.output_per_1k_usd)} per 1M output tokens`;
}

/** The option as the trigger and the list name it: the default says so. */
function optionLabel(o: ChatModelOption): string {
  return o.default ? `${o.label} (default)` : o.label;
}

/** The option's second line in the open list: its price and the first
 *  sentence of its hint. The whole hint sits under the select once chosen;
 *  the list only has room to tell the options apart. */
function optionLine(o: ChatModelOption): string {
  const price = o.pricing_known ? `${pricePerMillionShort(o)} per 1M tokens` : "Pricing N/A";
  const first = firstSentence(o.hint);
  return first ? `${price}. ${first}` : price;
}

export function firstSentence(s: string): string {
  const m = s.match(/^.*?[.!?](?=\s|$)/);
  return (m ? m[0] : s).trim();
}

/** The model's name in a sentence — "This workspace still uses Kimi K2.6." —
 *  without the "(default)" the list adds; its id when it is not on offer. */
function labelOf(options: ChatModelOption[], model: string): string {
  return options.find((o) => o.model === model)?.label ?? model;
}
