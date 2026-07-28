export interface UsageSummary {
  from: string;
  to: string;
  total_cost_usd: number;
  total_tokens_in: number;
  total_tokens_out: number;
  event_counts: Record<string, number>;
  cost_by_event_type_usd: Record<string, number>;
  cost_by_model_usd?: Record<string, number>;
  tokens_in_by_model?: Record<string, number>;
  tokens_out_by_model?: Record<string, number>;
}

export interface CreditBalance {
  company_id: string;
  balance_micro_usd: number;
  monthly_grant_micro_usd: number;
  updated_at: string;
}

export interface ThreadRow {
  thread_id: string;
  channel: string;
  title: string;
  last_message_at: string;
  event_count: number;
  tokens_in: number;
  tokens_out: number;
  cache_create_tokens_in: number;
  cache_read_tokens_in: number;
  cost_usd: number;
}

export interface UsageEvent {
  id: string;
  company_id: string;
  thread_id: string;
  message_id: string | null;
  event_type: string;
  model: string | null;
  tokens_in: number;
  tokens_out: number;
  cache_create_tokens_in: number;
  cache_read_tokens_in: number;
  cost_micro_usd: number;
  metadata: Record<string, unknown> | null;
  created_at: string;
}

export interface ChannelRow {
  channel: string;
  thread_count: number;
  event_count: number;
  tokens_in: number;
  tokens_out: number;
  cost_usd: number;
}

export interface UserRow {
  channel: string;
  user_key: string;
  user_key_kind: string;
  thread_count: number;
  event_count: number;
  tokens_in: number;
  tokens_out: number;
  cost_usd: number;
}

export const EVENT_LABELS: Record<string, string> = {
  llm_call: "LLM calls",
  sql_query: "SQL queries",
  metabase_card: "Charts",
  metabase_dashboard: "Dashboards",
  topic_classify: "Topic classification",
  document_generated: "Documents",
};

export const CHANNEL_LABELS: Record<string, string> = {
  whatsapp: "WhatsApp",
  dashboard: "Dashboard",
  discord: "Discord",
  lark: "Lark",
  api: "API",
};

export const USER_KIND_LABELS: Record<string, string> = {
  user_id: "User",
  phone: "Phone",
  discord_user_id: "Discord",
  lark_open_id: "Lark",
  // The tenant's own reference for whoever their integration called on
  // behalf of — their user id, an email, whatever their system uses. We
  // never resolve it to a person, so it is labelled by where it came from.
  api_user_ref: "API user",
  unknown: "Unknown",
};

export function microToUsd(micro: number): number {
  return micro / 1_000_000;
}

export function buildRangeParams(from: string, to: string): Record<string, string> {
  const p: Record<string, string> = {};
  if (from) p.from = new Date(from).toISOString();
  if (to) p.to = new Date(to).toISOString();
  return p;
}
