/**
 * Display strings and unit conversions for the usage tabs.
 *
 * This file used to be `types.ts` and held hand-written mirrors of the Go
 * structs beside these labels. The types are generated now
 * (`@argentum/api-types`, T-02b); what is left here is the half that is
 * genuinely the dashboard's: how a machine-readable value is spelled for a
 * person, which the backend has no opinion about.
 */

export const EVENT_LABELS: Record<string, string> = {
  llm_call: "LLM calls",
  sql_query: "SQL queries",
  metabase_card: "Charts",
  metabase_dashboard: "Dashboards",
  topic_classify: "Topic classification",
  document_generated: "Documents",
  // A call to one of the tenant's own MCP servers (T-M2). Labelled by what the
  // tenant connected rather than by the protocol — nobody reading a spend
  // breakdown is thinking about MCP, they are thinking about the ticketing
  // system they plugged in.
  mcp_call: "Connected tools",
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
