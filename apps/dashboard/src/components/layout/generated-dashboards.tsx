import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { LayoutDashboard, Trash2, ExternalLink } from "lucide-react";
import {
  SidebarGroup,
  SidebarGroupLabel,
  SidebarGroupContent,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuAction,
} from "@/components/ui/sidebar";
import { api } from "@/lib/api";
import type { SavedDashboard } from "@argentum/api-types";

export function GeneratedDashboards() {
  const qc = useQueryClient();
  const { data } = useQuery({
    queryKey: ["dashboards"],
    queryFn: async () =>
      (await api.get<{ dashboards: SavedDashboard[] }>("/dashboards")).data.dashboards,
  });
  const dashboards = data ?? [];
  const [deletingId, setDeletingId] = useState<string | null>(null);

  async function remove(id: string) {
    if (!confirm("Delete this dashboard? It will be removed from Metabase as well.")) return;
    setDeletingId(id);
    try {
      await api.delete(`/dashboards/${id}`);
      qc.invalidateQueries({ queryKey: ["dashboards"] });
    } catch {
      // silently fail; user can retry
    } finally {
      setDeletingId(null);
    }
  }

  if (dashboards.length === 0) return null;

  return (
    <SidebarGroup className="pt-0 px-0">
      <SidebarGroupLabel className="text-xs font-semibold uppercase tracking-wider text-muted-foreground px-2 py-1.5">
        Generated Dashboards
      </SidebarGroupLabel>
      <SidebarGroupContent>
        <SidebarMenu>
          {dashboards.map((d) => (
            <SidebarMenuItem key={d.id} className="group/menu-item">
              <SidebarMenuButton asChild>
                <a
                  href={d.public_url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="flex items-center gap-2"
                >
                  <LayoutDashboard className="size-4 text-purple-600 shrink-0" />
                  <div className="grid flex-1 text-left text-sm leading-tight min-w-0">
                    <span className="truncate font-medium">{d.name}</span>
                    <span className="truncate text-xs text-muted-foreground">
                      Dashboard
                    </span>
                  </div>
                  <ExternalLink className="size-3 text-muted-foreground opacity-0 group-hover/menu-item:opacity-100 transition-opacity" />
                </a>
              </SidebarMenuButton>
              <SidebarMenuAction
                showOnHover
                onClick={() => remove(d.id)}
                disabled={deletingId === d.id}
                className="text-destructive hover:text-destructive"
              >
                <Trash2 className="size-4" />
              </SidebarMenuAction>
            </SidebarMenuItem>
          ))}
        </SidebarMenu>
      </SidebarGroupContent>
    </SidebarGroup>
  );
}
