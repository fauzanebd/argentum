import { Link, useLocation } from "@tanstack/react-router";
import { LayoutDashboard } from "lucide-react";
import {
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar";

/**
 * The dashboards this product runs itself (T-D10).
 *
 * A plain nav entry rather than a list of rows. It sat beside one until
 * T-D15: the Metabase sidebar list existed because a Metabase dashboard could
 * only be reached by its own link, so every one of them had to be on screen.
 * A native dashboard is reachable from the chat reply that produced it and
 * from here, and the page is where somebody goes when the conversation has
 * scrolled away.
 */
export function DashboardsNav() {
  const { pathname } = useLocation();
  const isActive = pathname.startsWith("/dashboards");

  return (
    <div className="pb-2">
      <SidebarMenu>
        <SidebarMenuItem>
          <SidebarMenuButton asChild isActive={isActive}>
            <Link to="/dashboards">
              <LayoutDashboard className="size-4 shrink-0" />
              <span>Dashboards</span>
            </Link>
          </SidebarMenuButton>
        </SidebarMenuItem>
      </SidebarMenu>
    </div>
  );
}
