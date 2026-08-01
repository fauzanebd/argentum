import { Link, useLocation } from "@tanstack/react-router";
import { Siren } from "lucide-react";
import {
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar";

export function WatchersNav() {
  const { pathname } = useLocation();
  const isActive = pathname.startsWith("/watchers");

  return (
    <div className="pb-2">
      <SidebarMenu>
        <SidebarMenuItem>
          <SidebarMenuButton asChild isActive={isActive}>
            <Link to="/watchers">
              <Siren className="size-4 shrink-0" />
              <span>Watchers</span>
            </Link>
          </SidebarMenuButton>
        </SidebarMenuItem>
      </SidebarMenu>
    </div>
  );
}
