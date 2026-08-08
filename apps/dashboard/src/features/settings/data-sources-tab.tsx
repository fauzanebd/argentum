import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Database, Plug } from "lucide-react";
import { api } from "@/lib/api";
import { AdminGate } from "@/components/layout/admin-gate";
import { useIsAdmin } from "@/store/auth";
import { cn } from "@/lib/utils";
import { ConnectionsTab } from "./connections-tab";
import { MCPServersTab } from "./mcp-servers-tab";
import type { MCPServersResponse } from "@argentum/api-types";

/**
 * Settings → Data sources.
 *
 * A database and an MCP server are the same thing to the person configuring
 * them: a place an agent reads from, registered with a credential this
 * workspace owns. They were two sibling tabs, which asked an admin to know
 * which kind of thing they were looking for before they could look for it.
 * They are one tab now, with the kind chosen inside.
 *
 * The two panels keep their own components. What a database needs configured
 * (a DSN, an embedding index, a default) and what a server needs (a transport,
 * a per-tool approval) have nothing in common below the surface, and pretending
 * otherwise would cost more than the shared heading is worth.
 */

type SourceKind = "databases" | "mcp-servers";

export function DataSourcesTab() {
  const isAdmin = useIsAdmin();
  const [kind, setKind] = useState<SourceKind>("databases");

  const { data: connections } = useQuery({
    queryKey: ["connections"],
    queryFn: async () =>
      (await api.get<{ connections: unknown[] }>("/connections")).data.connections,
  });

  // Members cannot read the MCP roster at all — every route on it is admin-only,
  // including the GET — so the count is not fetched for them and the section is
  // not offered. Databases have a member-readable GET and render read-only.
  const { data: mcp } = useQuery({
    queryKey: ["mcp-servers"],
    queryFn: async () => (await api.get<MCPServersResponse>("/mcp-servers")).data,
    enabled: isAdmin,
  });

  const sections: {
    id: SourceKind;
    label: string;
    icon: typeof Database;
    count?: number;
  }[] = [
    {
      id: "databases",
      label: "Databases",
      icon: Database,
      count: connections?.length,
    },
    ...(isAdmin
      ? [
          {
            id: "mcp-servers" as const,
            label: "MCP servers",
            icon: Plug,
            count: mcp?.servers?.length,
          },
        ]
      : []),
  ];

  // The picker is noise when there is nothing to pick between.
  const active = sections.some((s) => s.id === kind) ? kind : "databases";

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-lg font-semibold">Data sources</h2>
        <p className="text-sm text-muted-foreground">
          Everywhere your agents read from. A database is queried read-only; an MCP server exposes
          tools you approve one at a time. Credentials for both are stored encrypted.
        </p>
      </div>

      {sections.length > 1 && (
        <div className="inline-flex rounded-md border border-border p-0.5">
          {sections.map((s) => {
            const Icon = s.icon;
            return (
              <button
                key={s.id}
                type="button"
                onClick={() => setKind(s.id)}
                className={cn(
                  "flex items-center gap-2 rounded px-3 py-1.5 text-sm transition-colors",
                  active === s.id
                    ? "bg-muted text-foreground"
                    : "text-muted-foreground hover:text-foreground",
                )}
              >
                <Icon className="h-4 w-4" />
                {s.label}
                {s.count !== undefined && (
                  <span className="text-xs text-muted-foreground">{s.count}</span>
                )}
              </button>
            );
          })}
        </div>
      )}

      {active === "databases" ? (
        <AdminGate>
          <ConnectionsTab />
        </AdminGate>
      ) : (
        <MCPServersTab />
      )}
    </div>
  );
}
