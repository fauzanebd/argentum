import { Link, useLocation } from "@tanstack/react-router";
import { CalendarClock } from "lucide-react";
import {
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar";

export function ScheduledTasksNav() {
  const { pathname } = useLocation();
  const isActive = pathname.startsWith("/scheduled-tasks");

  return (
    <div className="pb-2">
      <SidebarMenu>
        <SidebarMenuItem>
          <SidebarMenuButton asChild isActive={isActive}>
            <Link to="/scheduled-tasks" search={{ taskId: undefined }}>
              <CalendarClock className="size-4 shrink-0" />
              <span>Scheduled tasks</span>
            </Link>
          </SidebarMenuButton>
        </SidebarMenuItem>
      </SidebarMenu>
    </div>
  );
}
