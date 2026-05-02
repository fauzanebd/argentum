import { Database, BarChart3, ExternalLink, FileText } from "lucide-react";
import { cn } from "@/lib/utils";

const TOOL_META: Record<string, { icon: any; label: string }> = {
  run_sql: { icon: Database, label: "SQL query" },
  get_schema: { icon: FileText, label: "Schema lookup" },
  create_visualization: { icon: BarChart3, label: "Chart" },
  create_dashboard: { icon: ExternalLink, label: "Dashboard" },
};

export function ToolCallCard({ name, payload }: { name: string; payload: unknown }) {
  const meta = TOOL_META[name] ?? { icon: Database, label: name };
  const Icon = meta.icon;

  let summary: string | null = null;
  let dashboardURL: string | null = null;

  if (payload && typeof payload === "object") {
    const p = payload as Record<string, unknown>;
    if (typeof p.sql === "string") summary = p.sql.slice(0, 240);
    if (typeof p.dashboard_url === "string") dashboardURL = p.dashboard_url;
    if (typeof p.url === "string") dashboardURL = p.url;
  }

  return (
    <div className={cn("rounded-md border bg-muted/30 px-3 py-2 text-xs")}>
      <div className="flex items-center gap-1.5 font-medium text-muted-foreground mb-1">
        <Icon className="h-3 w-3" />
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
    </div>
  );
}
