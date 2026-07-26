import { Link } from "@tanstack/react-router";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarHeader,
  SidebarInset,
  SidebarProvider,
  SidebarTrigger,
  useSidebar,
} from "@/components/ui/sidebar";
import { RecentChats } from "@/components/layout/recent-chats";
import { ScheduledTasksNav } from "@/components/layout/scheduled-tasks-nav";
import { GeneratedDashboards } from "@/components/layout/generated-dashboards";
import { NavUser } from "@/components/layout/nav-user";
import { useThemeStore } from "@/store/theme";
import { cn } from "@/lib/utils";

export function AppShell({ children }: { children: React.ReactNode }) {
  return (
    <SidebarProvider>
      <div className="relative flex h-dvh w-full">
        <AppSidebar />
        <SidebarInset className="flex flex-col overflow-hidden">
          {/* Mobile sidebar trigger */}
          <div className="md:hidden flex items-center gap-2 px-3 py-2 border-b border-border shrink-0">
            <SidebarTrigger />
            <img
              src="/images/completeLogo_black.svg"
              alt="Argentum"
              className="argentum-logo h-4"
            />
          </div>
          {children}
        </SidebarInset>
      </div>
    </SidebarProvider>
  );
}

function AppSidebar() {
  const { state } = useSidebar();
  const isCollapsed = state === "collapsed";
  const theme = useThemeStore((s) => s.theme);
  const isDark = theme === "dark";

  return (
    <Sidebar variant="floating" collapsible="icon">
      {/* Header */}
      <SidebarHeader
        className={cn(
          "flex shrink-0",
          isCollapsed
            ? "flex-row items-center justify-between gap-y-4 md:flex-col md:items-center md:justify-start"
            : "flex-row items-center justify-between",
        )}
      >
        <Link to="/chat" className="flex items-center gap-2">
          <img
            src="/images/shortLogo_black.svg"
            alt="Argentum"
            className={cn(
              "w-8 h-8 argentum-logo",
              isDark && "invert brightness-110",
            )}
          />
          {!isCollapsed && (
            <img
              src="/images/textLogo_black.svg"
              alt="Argentum"
              className={cn(
                "min-w-24 h-auto pt-1 argentum-logo",
                isDark && "invert brightness-110",
              )}
            />
          )}
        </Link>
        <div className={cn(isCollapsed && "mt-1")}>
          <SidebarTrigger />
        </div>
      </SidebarHeader>

      {/* Content — split into two scrollable regions */}
      <SidebarContent className="flex flex-col overflow-hidden p-0">
        {/* Top region: Scheduled Tasks + New Conversation + Recent Chats — grows and scrolls */}
        <div className="flex-1 overflow-y-auto overflow-x-hidden min-h-0 px-2 py-2">
          <ScheduledTasksNav />
          <RecentChats />
        </div>

        {/* Bottom region: Generated Dashboards — capped height, scrolls */}
        <div className="max-h-[38%] overflow-y-auto overflow-x-hidden shrink-0 px-2 pb-2">
          <GeneratedDashboards />
        </div>
      </SidebarContent>

      {/* Footer */}
      <SidebarFooter className="px-2 shrink-0">
        <NavUser />
        {!isCollapsed && (
          <p className="px-2 pb-1 text-[10px] text-muted-foreground/70 text-center">
            by Smartsoft
          </p>
        )}
      </SidebarFooter>
    </Sidebar>
  );
}
