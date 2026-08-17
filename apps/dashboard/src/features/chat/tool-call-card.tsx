import { useState } from "react";
import {
  Database,
  BarChart3,
  CalendarClock,
  ChevronDown,
  ExternalLink,
  FileText,
  Loader2,
  PencilLine,
  Plug,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { Link } from "@tanstack/react-router";
import cronstrue from "cronstrue";
import { cn } from "@/lib/utils";
import { CodeBlock } from "@/components/ui/code-block";

const TOOL_META: Record<string, { icon: LucideIcon; label: string }> = {
  run_sql: { icon: Database, label: "SQL query" },
  get_schema: { icon: FileText, label: "Schema lookup" },
  create_visualization: { icon: BarChart3, label: "Chart" },
  create_dashboard: { icon: ExternalLink, label: "Dashboard" },
  update_dashboard: { icon: PencilLine, label: "Dashboard edit" },
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

/**
 * A tool call, as a chip that expands (T-U6).
 *
 * It was a block at the same visual weight as the answer, so a three-tool turn
 * put three grey panels around two sentences of content. Collapsed it is now
 * one line; the detail is unchanged underneath, and everything that decides
 * *what* that detail says — `TOOL_META`, `mcpMeta`, `humanCron` — is exactly as
 * it was. This ticket moved presentation only.
 *
 * The chip opens by default for nothing: a reader who wants to audit the SQL
 * asks for it, and a reader who wants the answer should not have to scroll past
 * it first.
 */
export function ToolCallCard({
  name,
  payload,
  loading,
}: {
  name: string;
  payload: unknown;
  loading?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const meta = mcpMeta(name) ?? TOOL_META[name] ?? { icon: Database, label: name };
  const Icon = meta.icon;

  let sql: string | null = null;
  let dashboardURL: string | null = null;
  let scheduleTaskId: string | null = null;
  let scheduleName: string | null = null;
  let scheduleCronText: string | null = null;

  if (payload && typeof payload === "object") {
    const p = payload as Record<string, unknown>;
    // No longer truncated to 240 characters. It was truncated because it sat
    // open in the timeline; behind a disclosure the whole statement can show,
    // and half a query is not auditable.
    if (typeof p.sql === "string") sql = p.sql;
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

  const isSchedule = name === "schedule_task";
  const hasDetail =
    sql !== null || dashboardURL !== null || isSchedule;

  return (
    <div className="text-xs">
      <button
        type="button"
        disabled={!hasDetail}
        onClick={() => setOpen((v) => !v)}
        aria-expanded={hasDetail ? open : undefined}
        className={cn(
          "inline-flex max-w-full items-center gap-1.5 rounded-full border border-border bg-secondary px-2.5 py-1 font-medium text-muted-foreground transition-colors",
          hasDetail &&
            "hover:border-border-strong hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
        )}
      >
        {loading ? (
          <Loader2 className="h-3 w-3 shrink-0 animate-spin" />
        ) : (
          <Icon className="h-3 w-3 shrink-0" />
        )}
        <span className="truncate">{meta.label}</span>
        {hasDetail && (
          <ChevronDown
            className={cn(
              "h-3 w-3 shrink-0 transition-transform",
              open && "rotate-180",
            )}
          />
        )}
      </button>

      {open && hasDetail && (
        <div className="mt-1.5 space-y-1.5 rounded-md border border-border bg-inset px-3 py-2">
          {sql && <CodeBlock code={sql} lang="sql" className="my-0" />}

          {isSchedule && (scheduleName || scheduleCronText) && (
            <div className="space-y-0.5 text-[11px] text-muted-foreground">
              {scheduleName && (
                <div className="font-medium text-foreground">{scheduleName}</div>
              )}
              {scheduleCronText && <div>{scheduleCronText}</div>}
            </div>
          )}

          {dashboardURL && (
            <a
              href={dashboardURL}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-1 text-primary-ink underline underline-offset-2"
            >
              Open dashboard <ExternalLink className="h-3 w-3" />
            </a>
          )}

          {isSchedule && (
            <Link
              to="/scheduled-tasks"
              search={{ taskId: scheduleTaskId ?? undefined }}
              className="inline-flex items-center gap-1 text-primary-ink underline underline-offset-2"
            >
              View task <ExternalLink className="h-3 w-3" />
            </Link>
          )}
        </div>
      )}
    </div>
  );
}
