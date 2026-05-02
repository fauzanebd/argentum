import { Link, useNavigate } from "@tanstack/react-router";
import {
  MessageSquare,
  ListTree,
  Settings,
  CircleDollarSign,
  LogOut,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { useAuthStore } from "@/store/auth";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";

export function AppShell({ children }: { children: React.ReactNode }) {
  const user = useAuthStore((s) => s.user);
  const clear = useAuthStore((s) => s.clear);
  const navigate = useNavigate();

  const navItems = [
    { to: "/chat", label: "Chat", icon: MessageSquare },
    { to: "/threads", label: "Threads", icon: ListTree },
    { to: "/usage", label: "Usage", icon: CircleDollarSign },
    { to: "/settings", label: "Settings", icon: Settings },
  ];

  async function logout() {
    try {
      await api.post("/auth/logout");
    } catch {
      // server-side logout best-effort
    }
    clear();
    navigate({ to: "/login" });
  }

  return (
    <div className="grid h-screen grid-cols-[240px_1fr] bg-background">
      <aside className="border-r border-border bg-card/50 flex flex-col">
        <div className="px-6 py-5 border-b border-border">
          <div className="text-lg font-bold tracking-tight">Argentum</div>
          <div className="text-xs text-muted-foreground truncate" title={user?.email}>
            {user?.email}
          </div>
        </div>
        <nav className="flex-1 px-3 py-4 space-y-1">
          {navItems.map((item) => (
            <Link
              key={item.to}
              to={item.to}
              className="flex items-center gap-2 rounded-md px-3 py-2 text-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
              activeProps={{ className: cn("text-foreground bg-accent font-medium") }}
            >
              <item.icon className="h-4 w-4" />
              {item.label}
            </Link>
          ))}
        </nav>
        <div className="px-3 py-3 border-t border-border">
          <Button variant="ghost" size="sm" className="w-full justify-start" onClick={logout}>
            <LogOut className="h-4 w-4" />
            Sign out
          </Button>
        </div>
      </aside>
      <main className="overflow-hidden">{children}</main>
    </div>
  );
}
