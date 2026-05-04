import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { Phone, Globe, MoreVertical, Trash2, Plus } from "lucide-react";
import {
  SidebarGroup,
  SidebarGroupLabel,
  SidebarGroupContent,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuAction,
} from "@/components/ui/sidebar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { api } from "@/lib/api";
import type { Thread } from "@/features/chat/types";
import { formatRelative } from "@/features/chat/format";

export function RecentChats() {
  const { data: threadsData } = useQuery({
    queryKey: ["threads"],
    queryFn: async () =>
      (await api.get<{ threads: Thread[] }>("/threads")).data.threads,
  });
  const threads = threadsData ?? [];
  const params = useParams({ strict: false }) as { threadId?: string };
  const activeThreadId = params.threadId ?? null;
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [openMenuId, setOpenMenuId] = useState<string | null>(null);

  async function deleteThread(id: string) {
    const previousThreads = qc.getQueryData<Thread[]>(["threads"]) ?? [];
    qc.setQueryData<Thread[]>(
      ["threads"],
      previousThreads.filter((t) => t.id !== id),
    );
    if (id === activeThreadId) {
      navigate({ to: "/chat" });
    }
    try {
      await api.delete(`/threads/${id}`);
    } catch {
      qc.setQueryData<Thread[]>(["threads"], previousThreads);
    }
  }

  return (
    <>
      {/* Sticky "New Conversation" button at the top */}
      <div className="sticky top-0 z-10  pb-2">
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton
              asChild
              className="font-medium text-primary hover:bg-primary/10 hover:text-primary transition-colors"
            >
              <Link to="/chat">
                <Plus className="size-4 shrink-0" />
                <span>New Conversation</span>
              </Link>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </div>

      {/* Recent Chats list */}
      {threads.length > 0 && (
        <SidebarGroup className="pt-0 px-0">
          <SidebarGroupLabel className="text-xs font-semibold uppercase tracking-wider text-muted-foreground px-2 py-1.5">
            Recent Chats
          </SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              {threads.map((t) => (
                <SidebarMenuItem key={t.id} className="group/menu-item">
                  <SidebarMenuButton asChild isActive={t.id === activeThreadId}>
                    <Link to="/chat/$threadId" params={{ threadId: t.id }}>
                      {t.channel === "whatsapp" ? (
                        <Phone className="size-4 text-green-500 shrink-0" />
                      ) : (
                        <Globe className="size-4 text-muted-foreground shrink-0" />
                      )}
                      <div className="grid flex-1 text-left text-sm leading-tight min-w-0">
                        <span className="truncate font-medium">
                          {t.title || "New conversation"}
                        </span>
                        <span className="truncate text-xs text-muted-foreground">
                          {t.phone_number || "Dashboard"} ·{" "}
                          {formatRelative(t.last_message_at)}
                        </span>
                      </div>
                    </Link>
                  </SidebarMenuButton>
                  <DropdownMenu
                    open={openMenuId === t.id}
                    onOpenChange={(open) => setOpenMenuId(open ? t.id : null)}
                  >
                    <DropdownMenuTrigger asChild>
                      <SidebarMenuAction showOnHover>
                        <MoreVertical className="size-4" />
                      </SidebarMenuAction>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent side="right" align="start">
                      <DropdownMenuItem
                        className="text-destructive focus:text-destructive"
                        onClick={() => deleteThread(t.id)}
                      >
                        <Trash2 className="size-4" />
                        Delete chat
                      </DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </SidebarMenuItem>
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      )}
    </>
  );
}
