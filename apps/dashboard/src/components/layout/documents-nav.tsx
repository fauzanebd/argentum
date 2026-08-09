import { Link, useLocation } from "@tanstack/react-router";
import { FileText } from "lucide-react";
import {
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar";

export function DocumentsNav() {
  const { pathname } = useLocation();
  const isActive = pathname.startsWith("/documents");

  return (
    <div className="pb-2">
      <SidebarMenu>
        <SidebarMenuItem>
          <SidebarMenuButton asChild isActive={isActive}>
            <Link to="/documents">
              <FileText className="size-4 shrink-0" />
              <span>Documents</span>
            </Link>
          </SidebarMenuButton>
        </SidebarMenuItem>
      </SidebarMenu>
    </div>
  );
}
