import { useEffect, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import {
  CalendarClock,
  FileText,
  MessageSquare,
  Settings,
  Sparkles,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import type {
  Agent,
  AgentsResponse,
  ConversationThread,
  ScheduledTask,
} from "@argentum/api-types";
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";

/**
 * ⌘K over everything the dashboard already has in memory (T-U10).
 *
 * ## Why it reads the cache rather than the API
 *
 * There is no search endpoint. Adding one is a backend ticket, and it would be
 * the wrong first move: nobody knows yet which surfaces people actually reach
 * for, and a client-side palette over the caches the sidebar has already
 * populated answers that for the cost of this file. When a customer with three
 * hundred threads finds the list is truncated, *that* is the evidence for the
 * endpoint.
 *
 * The consequence is stated rather than hidden: this searches what has been
 * loaded. Threads are the one list the sidebar always fetches in full, so they
 * are complete; the others are as complete as whatever page the user has
 * visited.
 */

type Entry = {
  id: string;
  label: string;
  hint?: string;
  icon: LucideIcon;
  go: () => void;
};

/** The settings tabs, which have no query to read — they are a fixed set that
 *  only this file and `settings-page.tsx` know about. Duplicated deliberately:
 *  the alternative is exporting a route table from a page component so a search
 *  box can walk it. */
const SETTINGS_TABS = [
  "general",
  "agents",
  "connections",
  "data sources",
  "integrations",
  "team",
  "api keys",
  "webhooks",
  "mcp servers",
  "metrics",
  "reports",
  "phones",
  "discord",
  "slack",
  "lark",
  "embed",
  "about",
];

export function CommandPalette() {
  const [open, setOpen] = useState(false);
  const navigate = useNavigate();
  const qc = useQueryClient();

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "k" || !(e.metaKey || e.ctrlKey)) return;
      // Not while the user is mid-selection in the composer: ⌘K is a text
      // shortcut in some editors and stealing it from a selection is worse than
      // not offering it for one keystroke.
      const el = document.activeElement;
      const typing =
        el instanceof HTMLTextAreaElement || el instanceof HTMLInputElement;
      if (typing && el.selectionStart !== el.selectionEnd) return;
      e.preventDefault();
      setOpen((v) => !v);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  // Read straight out of the cache. `getQueryData` rather than `useQuery` on
  // purpose: opening the palette must not fire five requests, and anything not
  // yet loaded simply has nothing to offer.
  //
  // The shapes below are each query's *cached* shape, which is what its
  // `queryFn` returns and not what its endpoint sends. `["scheduled-tasks"]`
  // unwraps to a bare array in scheduled-tasks-page.tsx while `["agents"]`
  // caches the whole `AgentsResponse` in use-agents.ts — reading either one the
  // other way round yields an empty palette and no error.
  const threads = qc.getQueryData<ConversationThread[]>(["threads"]) ?? [];
  const tasks = qc.getQueryData<ScheduledTask[]>(["scheduled-tasks"]) ?? [];
  const agents = (
    qc.getQueryData<AgentsResponse>(["agents"])?.agents ?? []
  ).filter((a): a is Agent => a !== undefined);

  const run = (fn: () => void) => {
    setOpen(false);
    fn();
  };

  const threadEntries: Entry[] = threads.slice(0, 50).map((t) => ({
    id: `thread-${t.id}`,
    label: t.title || "Untitled conversation",
    hint: t.channel === "whatsapp" ? "WhatsApp" : "Dashboard",
    icon: MessageSquare,
    go: () =>
      navigate({ to: "/chat/$threadId", params: { threadId: t.id } }),
  }));

  const taskEntries: Entry[] = tasks.map((t) => ({
    id: `task-${t.id}`,
    label: t.name,
    icon: CalendarClock,
    go: () => navigate({ to: "/scheduled-tasks", search: { taskId: t.id } }),
  }));

  const agentEntries: Entry[] = agents.map((a) => ({
    id: `agent-${a.id}`,
    label: a.name,
    hint: "Agent",
    icon: Sparkles,
    go: () => navigate({ to: "/settings", search: { tab: "agents" } }),
  }));

  const settingsEntries: Entry[] = SETTINGS_TABS.map((tab) => ({
    id: `settings-${tab}`,
    label: tab.replace(/\b\w/g, (c) => c.toUpperCase()),
    hint: "Settings",
    icon: Settings,
    go: () =>
      navigate({ to: "/settings", search: { tab: tab.replace(/ /g, "-") } }),
  }));

  const pageEntries: Entry[] = [
    {
      id: "page-chat",
      label: "New conversation",
      icon: MessageSquare,
      go: () => navigate({ to: "/chat" }),
    },
    {
      id: "page-documents",
      label: "Documents",
      icon: FileText,
      go: () => navigate({ to: "/documents" }),
    },
    {
      id: "page-tasks",
      label: "Scheduled tasks",
      icon: CalendarClock,
      go: () => navigate({ to: "/scheduled-tasks", search: { taskId: undefined } }),
    },
    {
      id: "page-usage",
      label: "Usage",
      icon: FileText,
      go: () => navigate({ to: "/usage" }),
    },
  ];

  const groups: Array<{ heading: string; entries: Entry[] }> = [
    { heading: "Go to", entries: pageEntries },
    { heading: "Conversations", entries: threadEntries },
    { heading: "Scheduled tasks", entries: taskEntries },
    { heading: "Agents", entries: agentEntries },
    { heading: "Settings", entries: settingsEntries },
  ];

  return (
    <CommandDialog open={open} onOpenChange={setOpen}>
      <CommandInput placeholder="Search conversations, tasks, settings…" />
      <CommandList>
        <CommandEmpty>Nothing matches that.</CommandEmpty>
        {groups
          .filter((g) => g.entries.length > 0)
          .map((group) => (
            <CommandGroup key={group.heading} heading={group.heading}>
              {group.entries.map((entry) => (
                <CommandItem
                  key={entry.id}
                  // cmdk matches on this string, not on the rendered children —
                  // without the hint in it, typing "whatsapp" would match
                  // nothing even though the label beside it says WhatsApp.
                  value={`${entry.label} ${entry.hint ?? ""}`}
                  onSelect={() => run(entry.go)}
                >
                  <entry.icon className="h-3.5 w-3.5 shrink-0 text-muted-subtle" />
                  <span className="min-w-0 flex-1 truncate">{entry.label}</span>
                  {entry.hint && (
                    <span className="shrink-0 text-[11px] text-muted-subtle">
                      {entry.hint}
                    </span>
                  )}
                </CommandItem>
              ))}
            </CommandGroup>
          ))}
      </CommandList>
    </CommandDialog>
  );
}
