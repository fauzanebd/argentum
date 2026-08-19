import { Link, useLocation } from "@tanstack/react-router";
import { BookOpen } from "lucide-react";
import {
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar";

/**
 * Knowledge: the PDFs a tenant uploaded (T-P7).
 *
 * A separate entry from Documents, one line below it, and the pairing is
 * deliberate — the two are opposites and the names are nearly the same. One is
 * what this product generated; this one is what somebody handed it.
 */
export function KnowledgeNav() {
  const { pathname } = useLocation();
  const isActive = pathname.startsWith("/knowledge");

  return (
    <div className="pb-2">
      <SidebarMenu>
        <SidebarMenuItem>
          <SidebarMenuButton asChild isActive={isActive}>
            <Link to="/knowledge">
              <BookOpen className="size-4 shrink-0" />
              <span>Knowledge</span>
            </Link>
          </SidebarMenuButton>
        </SidebarMenuItem>
      </SidebarMenu>
    </div>
  );
}
