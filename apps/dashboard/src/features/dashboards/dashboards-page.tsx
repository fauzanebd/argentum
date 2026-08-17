import { lazy, Suspense } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { LayoutDashboard } from "lucide-react";
import type { Dashboard } from "@argentum/api-types";

import { api } from "@/lib/api";

// Lazy for the same reason the chat embed is: recharts is ~390 kB, and a static
// import here would put it back in the main bundle for every page that never
// draws a chart — which is what the code-split in the usage tab already avoids.
const DashboardView = lazy(() =>
  import("./dashboard-view").then((m) => ({ default: m.DashboardView })),
);

/**
 * The dashboards the agent has built for this company, and one opened.
 *
 * Deliberately small. The full authoring surface — editing a panel, dragging
 * the grid, sharing a link — is Track F and T-D13; what this page owes today is
 * that the link a chat reply hands somebody goes somewhere, and that a
 * dashboard is findable a week after the conversation that produced it.
 */
export function DashboardsPage() {
  const { data, isLoading } = useQuery({
    queryKey: ["dashboards"],
    queryFn: async () =>
      (await api.get<{ dashboards: Dashboard[] }>("/dashboards")).data.dashboards,
  });

  if (isLoading) {
    return <p className="p-6 text-sm text-muted-foreground">Loading…</p>;
  }
  const dashboards = data ?? [];
  if (dashboards.length === 0) {
    return (
      <div className="p-6">
        <h1 className="text-lg font-semibold text-foreground">Dashboards</h1>
        <p className="mt-2 max-w-prose text-sm text-muted-foreground">
          No dashboards yet. Ask a question in chat and say you want a chart —
          the answer will come back as one, and it will be here afterwards.
        </p>
      </div>
    );
  }

  return (
    <div className="p-6">
      <h1 className="mb-4 text-lg font-semibold text-foreground">Dashboards</h1>
      <ul className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
        {dashboards.map((d) => (
          <li key={d.id}>
            <Link
              to="/dashboards/$id"
              params={{ id: d.id }}
              className="flex items-start gap-2 rounded-lg border border-border bg-card p-3 hover:border-border-strong"
            >
              <LayoutDashboard className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
              <span className="min-w-0">
                <span className="block truncate text-sm font-medium text-foreground">
                  {d.title}
                </span>
                {d.description && (
                  <span className="mt-0.5 block truncate text-xs text-muted-foreground">
                    {d.description}
                  </span>
                )}
              </span>
            </Link>
          </li>
        ))}
      </ul>
    </div>
  );
}

/** One dashboard, at full size. */
export function DashboardDetailPage() {
  const { id } = useParams({ from: "/protected/dashboards/$id" });
  return (
    <div className="p-6">
      <Link
        to="/dashboards"
        className="text-xs text-muted-foreground underline-offset-2 hover:underline"
      >
        ← All dashboards
      </Link>
      <Suspense
        fallback={<p className="mt-3 text-sm text-muted-foreground">Loading…</p>}
      >
        <DashboardView id={id} className="mt-3" />
      </Suspense>
    </div>
  );
}
