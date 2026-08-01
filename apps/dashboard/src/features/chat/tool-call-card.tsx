import {
  Database,
  BarChart3,
  CalendarClock,
  ExternalLink,
  FileText,
  Loader2,
  Plug,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { Link } from "@tanstack/react-router";
import cronstrue from "cronstrue";
import { cn } from "@/lib/utils";

const TOOL_META: Record<string, { icon: LucideIcon; label: string }> = {
  run_sql: { icon: Database, label: "SQL query" },
  get_schema: { icon: FileText, label: "Schema lookup" },
  create_visualization: { icon: BarChart3, label: "Chart" },
  create_dashboard: { icon: ExternalLink, label: "Dashboard" },
  schedule_task: { icon: CalendarClock, label: "Schedule task" },
};

/** MCP_PREFIX is the namespace the backend gives every tenant MCP tool
 *  (tools/mcp.NamePrefix): `mcp__<serverslug>__<tool>`. */
const MCP_PREFIX = "mcp__";

/** mcpMeta turns a namespaced MCP tool name into a label that names the server,
 *  not just the raw string (T-M3). The server is prettified from its slug —
 *  which the backend derived from the server's own name — so "Helpdesk ·
 *  search tickets" reads as what happened rather than as an internal id. Returns
 *  null for every non-MCP tool, which falls through to TOOL_META. */
function mcpMeta(name: string): { icon: LucideIcon; label: string } | null {
  if (!name.startsWith(MCP_PREFIX)) return null;
  const rest = name.slice(MCP_PREFIX.length);
  const sep = rest.indexOf("__");
  if (sep < 0) return { icon: Plug, label: prettifySlug(rest) };
  const server = prettifySlug(rest.slice(0, sep));
  const tool = prettifySlug(rest.slice(sep + 2));
  return { icon: Plug, label: `${server} · ${tool}` };
}

/** prettifySlug turns a `lower_snake` slug into "Title Case" words. Best-effort:
 *  the slug lost the original casing, so this recovers a readable approximation,
 *  not the exact server name. */
function prettifySlug(slug: string): string {
  const words = slug.replace(/_/g, " ").trim();
  if (!words) return slug;
  return words.replace(/\b\w/g, (c) => c.toUpperCase());
}

function humanCron(expr: string): string | null {
  try {
    return cronstrue.toString(expr, { use24HourTimeFormat: true });
  } catch {
    return null;
  }
}

export function ToolCallCard({
  name,
  payload,
  loading,
}: {
  name: string;
  payload: unknown;
  loading?: boolean;
}) {
  const meta = mcpMeta(name) ?? TOOL_META[name] ?? { icon: Database, label: name };
  const Icon = meta.icon;

  let summary: string | null = null;
  let dashboardURL: string | null = null;
  let scheduleTaskId: string | null = null;
  let scheduleName: string | null = null;
  let scheduleCronText: string | null = null;

  if (payload && typeof payload === "object") {
    const p = payload as Record<string, unknown>;
    if (typeof p.sql === "string") summary = p.sql.slice(0, 240);
    if (typeof p.dashboard_url === "string") dashboardURL = p.dashboard_url;
    if (typeof p.url === "string") dashboardURL = p.url;

    if (name === "schedule_task") {
      if (typeof p.task_id === "string") scheduleTaskId = p.task_id;
      else if (typeof p.id === "string") scheduleTaskId = p.id;
      if (typeof p.name === "string") scheduleName = p.name;
      const cronExpr =
        typeof p.cron_expression === "string" ? p.cron_expression : null;
      if (cronExpr) {
        scheduleCronText = humanCron(cronExpr) ?? cronExpr;
      }
    }
  }

  return (
    <div className={cn("rounded-md border bg-muted/30 px-3 py-2 text-xs")}>
      <div className="flex items-center gap-1.5 font-medium text-muted-foreground mb-1">
        {loading ? <Loader2 className="h-3 w-3 animate-spin" /> : <Icon className="h-3 w-3" />}
        {meta.label}
      </div>
      {summary && (
        <pre className="whitespace-pre-wrap font-mono text-[11px] text-muted-foreground">{summary}</pre>
      )}
      {dashboardURL && (
        <a
          href={dashboardURL}
          target="_blank"
          rel="noreferrer"
          className="inline-flex items-center gap-1 text-primary underline mt-1"
        >
          Open dashboard <ExternalLink className="h-3 w-3" />
        </a>
      )}
      {name === "schedule_task" && (scheduleName || scheduleCronText) && (
        <div className="text-[11px] text-muted-foreground space-y-0.5 mt-0.5">
          {scheduleName && <div className="font-medium text-foreground">{scheduleName}</div>}
          {scheduleCronText && <div>{scheduleCronText}</div>}
        </div>
      )}
      {name === "schedule_task" && (
        <Link
          to="/scheduled-tasks"
          search={{ taskId: scheduleTaskId ?? undefined }}
          className="inline-flex items-center gap-1 text-primary underline mt-1"
        >
          View task <ExternalLink className="h-3 w-3" />
        </Link>
      )}
    </div>
  );
}
