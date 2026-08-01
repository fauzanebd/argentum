import { Link } from "@tanstack/react-router";
import { ShieldQuestion } from "lucide-react";
import {
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar";
import { usePendingActions } from "@/features/actions/use-actions";

/** ApprovalsNav is the app-shell's pending-approvals badge (T-11): a company-wide
 *  count of proposals awaiting a decision. It renders nothing when there is
 *  nothing to decide, so the shell does not carry a permanent zero — the badge is
 *  a notification, not a menu entry. The cards themselves live in the chat
 *  composer strip, so the link points there. */
export function ApprovalsNav() {
  const { pending } = usePendingActions();
  const count = pending.filter((p) => p.status === "proposed").length;
  if (count === 0) return null;

  return (
    <div className="pb-2">
      <SidebarMenu>
        <SidebarMenuItem>
          <SidebarMenuButton asChild>
            <Link to="/chat" className="justify-between">
              <span className="flex items-center gap-2">
                <ShieldQuestion className="size-4 shrink-0 text-amber-600 dark:text-amber-400" />
                <span>Approvals</span>
              </span>
              <span className="ml-auto rounded-full bg-amber-500 px-1.5 py-0.5 text-[10px] font-semibold leading-none text-white">
                {count}
              </span>
            </Link>
          </SidebarMenuButton>
        </SidebarMenuItem>
      </SidebarMenu>
    </div>
  );
}
