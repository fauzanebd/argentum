import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ExternalLink, RefreshCw } from "lucide-react";
import type { Result } from "@argentum/api-types/dashboard";

import { api } from "@/lib/api";
import { cn } from "@/lib/utils";
import { DashboardPanel } from "./panel";

/**
 * A dashboard, resolved and drawn.
 *
 * Used twice: inline in a chat transcript, where the agent has just built one,
 * and on its own page. Both call the same endpoint and draw the same panels —
 * the embed is not a preview of the page, it is the page at a smaller size,
 * which is what keeps a chart from meaning two different things depending on
 * where somebody looked at it.
 *
 * **It fetches rather than receiving a payload.** A dashboard stores a question,
 * so the numbers are whatever the warehouse says now; a payload frozen into a
 * chat message would be a screenshot of the moment the agent answered, and the
 * whole point of replacing the Metabase card was that a card cannot say when it
 * last ran.
 */
export function DashboardView({
  id,
  /** Chat embeds get fewer, shorter panels; the page gives them room. */
  compact = false,
  className,
}: {
  id: string;
  compact?: boolean;
  className?: string;
}) {
  const [nonce, setNonce] = useState(0);
  const { data, isLoading, isFetching, error } = useQuery({
    queryKey: ["dashboard-data", id, nonce],
    queryFn: async () =>
      (await api.get<Result>(`/dashboards/${id}/data`)).data,
    // The panels run real queries against a tenant warehouse. Refetching on
    // every window focus would put a customer's replica under a load pattern
    // nobody agreed to; the refresh control below is the deliberate path.
    refetchOnWindowFocus: false,
    staleTime: 60_000,
  });

  if (isLoading) {
    return (
      <div className={cn("rounded-lg border border-border bg-card/50 p-4", className)}>
        <p className="text-xs text-muted-foreground">Loading dashboard…</p>
      </div>
    );
  }
  if (error || !data) {
    return (
      <div className={cn("rounded-lg border border-border bg-card/50 p-4", className)}>
        <p className="text-xs text-destructive">This dashboard could not be loaded.</p>
      </div>
    );
  }

  // The Go slice is of pointers, so a panel can in principle arrive null. It
  // does not today — the resolver fills every slot, failures included — and
  // dropping the empty ones is still the right reading: a tile with nothing in
  // it says less than no tile.
  const panels = (data.panels ?? []).filter((p) => p !== undefined && p !== null);
  const window = Object.entries(data.applied_filters ?? {});

  return (
    <section
      className={cn(
        "not-prose my-2 rounded-xl border border-border bg-card/40 p-3",
        className,
      )}
    >
      <header className="mb-2 flex items-center justify-between gap-2">
        <div className="min-w-0">
          <h3 className="truncate text-sm font-medium text-foreground">{data.title}</h3>
          {/* Which window answered. A dashboard that does not say this leaves
              the reader to assume it is the one they picked last week. */}
          {window.length > 0 && (
            <p className="mt-0.5 truncate text-[11px] text-muted-foreground">
              {window.map(([name, applied]) => `${name}: ${applied}`).join(" · ")}
            </p>
          )}
        </div>
        <div className="flex shrink-0 items-center gap-1">
          <button
            type="button"
            onClick={() => setNonce((n) => n + 1)}
            className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
            aria-label="Refresh"
            title="Re-run the panels"
          >
            <RefreshCw className={cn("h-3.5 w-3.5", isFetching && "animate-spin")} />
          </button>
          {compact && (
            <a
              href={`/dashboards/${id}`}
              className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
              aria-label="Open dashboard"
              title="Open full size"
            >
              <ExternalLink className="h-3.5 w-3.5" />
            </a>
          )}
        </div>
      </header>

      {panels.length === 0 ? (
        <p className="py-4 text-center text-xs text-muted-foreground">
          This dashboard has no panels.
        </p>
      ) : (
        <div
          className={cn(
            "grid gap-2",
            compact ? "sm:grid-cols-2" : "sm:grid-cols-2 lg:grid-cols-3",
          )}
        >
          {panels.map((panel) => (
            <DashboardPanel
              key={panel.panel_id}
              panel={panel}
              height={compact ? 160 : 240}
              // A KPI is one number and does not want half a row of white
              // under it; everything else fills its cell.
              className={panel.viz === "kpi" ? "sm:col-span-1" : "sm:col-span-2"}
            />
          ))}
        </div>
      )}
    </section>
  );
}
