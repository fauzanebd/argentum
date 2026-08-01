import type {
  Watcher,
  WatcherChannel,
  WatcherComparator,
  WatcherGrain,
  Channel,
} from "@argentum/api-types";

/**
 * WatcherDraft mirrors app.WatcherInput — the create/update body the backend
 * binds. It is a hand-written interface rather than a generated one because
 * WatcherInput lives in the `app` package, which tygo does not scan (only
 * `domain` structs cross into @argentum/api-types). Keep the field names in
 * lockstep with WatcherInput's JSON tags.
 */
export interface WatcherDraft {
  metric_id: string;
  name: string;
  window_grain: WatcherGrain;
  comparator: WatcherComparator;
  threshold: number;
  compare_to: string;
  cron_expression: string;
  timezone: string;
  channels: WatcherChannel[];
  cooldown_minutes: number;
  // Honoured only on update, and only against a fresh dry-run. Create always
  // makes a disabled watcher regardless.
  enabled?: boolean;
}

/**
 * DryRunResult mirrors app.DryRunResult — what the Dry-run button shows before
 * the Enable toggle unlocks. Also in the `app` package, so also hand-written.
 */
export interface DryRunResult {
  periods_evaluated: number;
  would_have_fired: number;
  samples: DryRunSample[];
}

export interface DryRunSample {
  from: string;
  to: string;
  value?: number;
  delta_pct?: number;
  breached: boolean;
  no_data: boolean;
}

/**
 * watcherToDraft maps a stored watcher back to the update body, preserving its
 * values verbatim. Used by the Enable/Disable toggle, which must re-send the
 * whole condition (PUT binds a full WatcherInput) with only `enabled` changed.
 */
export function watcherToDraft(w: Watcher, enabled?: boolean): WatcherDraft {
  return {
    metric_id: w.metric_id,
    name: w.name,
    window_grain: w.window_grain,
    comparator: w.comparator,
    threshold: w.threshold,
    compare_to: w.compare_to ?? "",
    cron_expression: w.cron_expression,
    timezone: w.timezone,
    channels: w.channels,
    cooldown_minutes: w.cooldown_minutes,
    enabled,
  };
}

/** enableRequiresDryRunWithin mirrors the backend's 24h freshness gate. */
export const DRY_RUN_FRESHNESS_MS = 24 * 60 * 60 * 1000;

/** A dry-run recent enough that the backend will let the watcher be enabled. */
export function hasFreshDryRun(
  lastDryRunAt: string | undefined,
  now: number = Date.now(),
): boolean {
  if (!lastDryRunAt) return false;
  const t = new Date(lastDryRunAt).getTime();
  if (Number.isNaN(t)) return false;
  return now - t <= DRY_RUN_FRESHNESS_MS;
}

export const GRAIN_LABELS: Record<WatcherGrain, string> = {
  day: "daily",
  week: "weekly",
  month: "monthly",
};

/** How each comparator reads in the form's dropdown. */
export const COMPARATOR_LABELS: Record<WatcherComparator, string> = {
  gt: "is greater than",
  lt: "is less than",
  pct_change_gt: "rises by more than (%)",
  pct_change_lt: "falls past (% change below)",
  no_data: "returns no data",
};

export const COMPARE_LABELS: Record<string, string> = {
  previous_period: "the previous period",
  same_period_last_year: "the same period last year",
};

/** Channels ride along on WatchersResponse; this is only their display text. */
export const CHANNEL_LABELS: Record<string, string> = {
  dashboard: "Dashboard thread",
  whatsapp: "WhatsApp",
  discord: "Discord",
  lark: "Lark",
  api: "API",
};

/** The per-channel ref field's placeholder, or "" for channels with no ref. */
export function channelRefPlaceholder(channel: Channel): string {
  switch (channel) {
    case "whatsapp":
      return "Phone number, e.g. +6281234567890";
    case "discord":
      return "Channel id";
    case "lark":
      return "Chat id";
    default:
      return "";
  }
}

/** pct_change comparators read a comparison window and so need compare_to. */
export function needsComparison(c: WatcherComparator): boolean {
  return c === "pct_change_gt" || c === "pct_change_lt";
}

/** no_data ignores the threshold entirely — the breach is the absence of a row. */
export function usesThreshold(c: WatcherComparator): boolean {
  return c !== "no_data";
}

/**
 * A compact, accurate one-line summary of what a watcher fires on, e.g.
 * "revenue < 999999999" or "orders % change > 20% vs the previous period".
 */
export function conditionSummary(
  w: Pick<Watcher, "comparator" | "threshold" | "compare_to">,
  metricLabel: string,
): string {
  const t = formatThreshold(w.threshold);
  switch (w.comparator) {
    case "gt":
      return `${metricLabel} > ${t}`;
    case "lt":
      return `${metricLabel} < ${t}`;
    case "pct_change_gt":
      return `${metricLabel} % change > ${t}% vs ${compareText(w.compare_to)}`;
    case "pct_change_lt":
      return `${metricLabel} % change < ${t}% vs ${compareText(w.compare_to)}`;
    case "no_data":
      return `${metricLabel} returns no data`;
    default:
      return metricLabel;
  }
}

function compareText(compareTo?: string): string {
  if (!compareTo) return "the comparison period";
  return COMPARE_LABELS[compareTo] ?? compareTo;
}

function formatThreshold(n: number): string {
  if (!Number.isFinite(n)) return String(n);
  // Group digits so a nine-digit revenue threshold is legible, but keep any
  // decimals a percentage threshold carries.
  return n.toLocaleString(undefined, { maximumFractionDigits: 4 });
}
