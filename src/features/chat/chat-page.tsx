import { useEffect, useMemo, useRef, useState, useCallback } from "react";
import { useParams, useNavigate } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Send, Bot, User, Plus, Phone, Globe, Loader2, MoreVertical, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import { DropdownMenu, DropdownMenuItem } from "@/components/ui/dropdown-menu";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";
import type { Thread, Message, ChatEvent } from "./types";
import { useThreadStream } from "./use-thread-stream";
import { ToolCallCard } from "./tool-call-card";
import { formatRelative } from "./format";

export function ChatPage() {
  const params = useParams({ strict: false }) as { threadId?: string };
  const navigate = useNavigate();
  const qc = useQueryClient();

  const { data: threadsData } = useQuery({
    queryKey: ["threads"],
    queryFn: async () => (await api.get<{ threads: Thread[] }>("/threads")).data.threads,
  });
  const threads = threadsData ?? [];

  const activeThreadId = params.threadId ?? null;
  const isNewChat = activeThreadId === null;

  const { data: messagesData } = useQuery({
    queryKey: ["messages", activeThreadId],
    queryFn: async () =>
      activeThreadId
        ? (await api.get<{ messages: Message[] }>(`/threads/${activeThreadId}/messages`)).data.messages
        : [],
    enabled: !!activeThreadId,
  });
  const persistedMessages = messagesData ?? [];

  // Optimistic messages: shown immediately before the server confirms.
  const [optimisticMessages, setOptimisticMessages] = useState<Message[]>([]);

  // Live assistant state streamed over the WebSocket.
  const [liveAssistant, setLiveAssistant] = useState<{
    jobId: string;
    content: string;
    thinking?: string;
    toolCalls?: Array<{ name: string; payload: unknown }>;
  } | null>(null);

  const [error, setError] = useState<string | null>(null);
  const [input, setInput] = useState("");
  const [sending, setSending] = useState(false);
  const [hoveredThreadId, setHoveredThreadId] = useState<string | null>(null);

  // Polling fallback for new threads (race-condition safety).
  const pollTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const finalReceivedRef = useRef(false);

  const stopPolling = useCallback(() => {
    if (pollTimerRef.current) {
      clearInterval(pollTimerRef.current);
      pollTimerRef.current = null;
    }
  }, []);

  const startPolling = useCallback(
    (threadId: string) => {
      finalReceivedRef.current = false;
      stopPolling();
      let attempts = 0;
      pollTimerRef.current = setInterval(async () => {
        if (finalReceivedRef.current) {
          stopPolling();
          return;
        }
        attempts++;
        if (attempts > 15) {
          stopPolling();
          return;
        }
        try {
          const res = await api.get<{ messages: Message[] }>(`/threads/${threadId}/messages`);
          const msgs = res.data.messages;
          const hasAssistant = msgs.some((m) => m.role === "assistant");
          if (hasAssistant) {
            qc.setQueryData(["messages", threadId], res.data);
            stopPolling();
          }
        } catch {
          // ignore polling errors
        }
      }, 2000);
    },
    [qc, stopPolling]
  );

  useEffect(() => {
    return () => stopPolling();
  }, [stopPolling]);

  useThreadStream(activeThreadId, (evt: ChatEvent) => {
    if (evt.type === "started") {
      finalReceivedRef.current = false;
      setLiveAssistant({ jobId: evt.job_id, content: "" });
    } else if (evt.type === "delta") {
      setLiveAssistant((prev) =>
        prev && prev.jobId === evt.job_id
          ? { ...prev, content: prev.content + (evt.content ?? "") }
          : { jobId: evt.job_id, content: evt.content ?? "" }
      );
    } else if (evt.type === "thinking") {
      setLiveAssistant((prev) =>
        prev && prev.jobId === evt.job_id
          ? { ...prev, thinking: evt.thinking_step }
          : { jobId: evt.job_id, content: "", thinking: evt.thinking_step }
      );
    } else if (evt.type === "tool_call" || evt.type === "tool_result") {
      setLiveAssistant((prev) => {
        if (!prev) return { jobId: evt.job_id, content: "" };
        const calls = prev.toolCalls ? [...prev.toolCalls] : [];
        if (evt.tool_call) {
          calls.push({
            name: evt.tool_call.name,
            payload:
              evt.type === "tool_call"
                ? evt.tool_call.arguments ?? {}
                : evt.tool_call.result ?? {},
          });
        }
        return { ...prev, toolCalls: calls };
      });
    } else if (evt.type === "final") {
      finalReceivedRef.current = true;
      setLiveAssistant(null);
      setOptimisticMessages((prev) => prev.filter((m) => m.thread_id !== evt.thread_id));
      qc.invalidateQueries({ queryKey: ["messages", evt.thread_id] });
      qc.invalidateQueries({ queryKey: ["threads"] });
      stopPolling();
    } else if (evt.type === "error") {
      finalReceivedRef.current = true;
      setLiveAssistant(null);
      setError(evt.error ?? "Something went wrong");
      stopPolling();
    }
  });

  async function createNewThread() {
    navigate({ to: "/chat" });
  }

  async function deleteThread(id: string) {
    // Optimistically remove from cache immediately.
    const previousThreads = qc.getQueryData<Thread[]>(["threads"]) ?? [];
    qc.setQueryData<Thread[]>(["threads"], previousThreads.filter((t) => t.id !== id));

    if (id === activeThreadId) {
      navigate({ to: "/chat" });
    }

    try {
      await api.delete(`/threads/${id}`);
    } catch (err: any) {
      // Restore on failure.
      qc.setQueryData<Thread[]>(["threads"], previousThreads);
      setError(err?.response?.data?.error || "Delete failed");
    }
  }

  async function send() {
    const text = input.trim();
    if (!text || sending) return;
    setError(null);
    setSending(true);

    let targetThreadId = activeThreadId;

    try {
      const res = await api.post<{ thread_id: string; is_new_thread: boolean; user_msg_id: string }>("/chat", {
        message: text,
        thread_id: targetThreadId ?? undefined,
      });

      const newThreadId = res.data.thread_id;
      const userMsgId = res.data.user_msg_id;

      // Optimistically append user message.
      setOptimisticMessages((prev) => [
        ...prev,
        {
          id: userMsgId,
          thread_id: newThreadId,
          role: "user",
          content: text,
          created_at: new Date().toISOString(),
        },
      ]);

      setInput("");

      if (newThreadId !== targetThreadId) {
        navigate({ to: "/chat/$threadId", params: { threadId: newThreadId } });
      }

      qc.invalidateQueries({ queryKey: ["threads"] });

      // Start polling as a safety net for new threads where the WS may
      // subscribe too late to catch the first final event.
      if (res.data.is_new_thread) {
        startPolling(newThreadId);
      }
    } catch (e: any) {
      setError(e?.response?.data?.error || "Send failed");
    } finally {
      setSending(false);
    }
  }

  const displayedMessages = useMemo(() => {
    // Merge persisted + optimistic messages for the active thread.
    const threadOptimistic = optimisticMessages.filter((m) => m.thread_id === activeThreadId);
    const merged = [...persistedMessages, ...threadOptimistic];
    // Deduplicate by id.
    const map = new Map<string, Message>();
    for (const m of merged) map.set(m.id, m);
    return Array.from(map.values()).sort(
      (a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime()
    );
  }, [persistedMessages, optimisticMessages, activeThreadId]);

  return (
    <div className="grid grid-cols-[280px_1fr] h-full">
      <aside className="border-r border-border flex flex-col overflow-hidden">
        <div className="p-3 border-b border-border flex items-center justify-between">
          <div className="text-sm font-semibold">Threads</div>
          <Button size="sm" variant="ghost" onClick={createNewThread} title="New conversation">
            <Plus className="h-4 w-4" />
          </Button>
        </div>
        <div className="flex-1 overflow-y-auto">
          {isNewChat && (
            <div className="px-3 py-3 bg-accent border-b border-border/50">
              <div className="text-sm font-medium truncate">New conversation</div>
              <div className="text-xs text-muted-foreground mt-0.5">Dashboard</div>
            </div>
          )}
          {threads.map((t) => (
            <div
              key={t.id}
              className={cn(
                "flex items-center gap-2 px-3 py-3 border-b border-border/50 hover:bg-accent transition-colors",
                t.id === activeThreadId && "bg-accent"
              )}
              onMouseEnter={() => setHoveredThreadId(t.id)}
              onMouseLeave={() => setHoveredThreadId(null)}
            >
              <div
                className="flex-1 min-w-0 cursor-pointer"
                onClick={() => navigate({ to: "/chat/$threadId", params: { threadId: t.id } })}
              >
                <div className="flex items-center gap-2">
                  {t.channel === "whatsapp" ? (
                    <Phone className="h-3 w-3 text-green-600 shrink-0" />
                  ) : (
                    <Globe className="h-3 w-3 text-blue-600 shrink-0" />
                  )}
                  <div className="text-sm font-medium truncate">
                    {t.title || "New conversation"}
                  </div>
                </div>
                <div className="text-xs text-muted-foreground truncate mt-0.5">
                  {t.phone_number ? t.phone_number : "Dashboard"} · {formatRelative(t.last_message_at)}
                </div>
              </div>
              <DropdownMenu
                trigger={
                  <div
                    className={cn(
                      "p-1 rounded hover:bg-accent text-muted-foreground hover:text-foreground transition-opacity",
                      hoveredThreadId === t.id ? "opacity-100" : "opacity-0"
                    )}
                  >
                    <MoreVertical className="h-3.5 w-3.5" />
                  </div>
                }
              >
                <DropdownMenuItem destructive onClick={() => deleteThread(t.id)}>
                  <Trash2 className="h-4 w-4" />
                  Delete chat
                </DropdownMenuItem>
              </DropdownMenu>
            </div>
          ))}
        </div>
      </aside>

      <section className="flex flex-col h-full overflow-hidden">
        <ChatHeader thread={threads.find((t) => t.id === activeThreadId)} isNew={isNewChat} />
        <ChatTimeline messages={displayedMessages} live={liveAssistant} />
        {error && (
          <div className="px-6 py-2 text-sm text-destructive border-t border-destructive/30 bg-destructive/5">
            {error}
          </div>
        )}
        <ChatComposer value={input} onChange={setInput} onSend={send} disabled={sending} />
      </section>
    </div>
  );
}

function ChatHeader({ thread, isNew }: { thread?: Thread; isNew?: boolean }) {
  if (!thread || isNew) {
    return (
      <header className="border-b border-border px-6 py-4">
        <div className="text-sm font-medium">New conversation</div>
        <div className="text-xs text-muted-foreground mt-0.5">
          Ask Argentum about your business metrics.
        </div>
      </header>
    );
  }
  return (
    <header className="border-b border-border px-6 py-4">
      <div className="flex items-center justify-between">
        <div>
          <div className="text-sm font-medium">{thread.title || "Conversation"}</div>
          <div className="text-xs text-muted-foreground mt-0.5">
            {thread.channel === "whatsapp" ? `WhatsApp · ${thread.phone_number}` : "Dashboard"}
          </div>
        </div>
        <Badge variant="outline" className="capitalize">
          {thread.channel}
        </Badge>
      </div>
    </header>
  );
}

function ChatTimeline({
  messages,
  live,
}: {
  messages: Message[];
  live: { jobId: string; content: string; thinking?: string; toolCalls?: Array<{ name: string; payload: unknown }> } | null;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    ref.current?.scrollTo({ top: ref.current.scrollHeight, behavior: "smooth" });
  }, [messages.length, live?.content, live?.thinking, live?.toolCalls?.length]);

  const items = useMemo(() => {
    const out: Array<Message | { id: string; pending: true; content: string; thinking?: string; toolCalls?: Array<{ name: string; payload: unknown }> }> = [...messages];
    if (live) {
      out.push({ id: `live-${live.jobId}`, pending: true, content: live.content, thinking: live.thinking, toolCalls: live.toolCalls });
    }
    return out;
  }, [messages, live]);

  return (
    <div ref={ref} className="flex-1 overflow-y-auto px-6 py-6">
      <div className="max-w-3xl mx-auto space-y-6">
        {items.length === 0 && (
          <div className="text-center text-muted-foreground py-12 text-sm">
            No messages yet. Ask something like "revenue by month for the last 6 months" to get started.
          </div>
        )}
        {items.map((m) => {
          if ("pending" in m) {
            return <PendingBubble key={m.id} content={m.content} thinking={m.thinking} toolCalls={m.toolCalls} />;
          }
          return <MessageBubble key={m.id} message={m} />;
        })}
      </div>
    </div>
  );
}

function MessageBubble({ message }: { message: Message }) {
  const isUser = message.role === "user";
  return (
    <div className={cn("flex gap-3", isUser ? "flex-row-reverse" : "flex-row")}>
      <div
        className={cn(
          "h-8 w-8 rounded-md flex items-center justify-center shrink-0",
          isUser ? "bg-primary text-primary-foreground" : "bg-secondary text-secondary-foreground"
        )}
      >
        {isUser ? <User className="h-4 w-4" /> : <Bot className="h-4 w-4" />}
      </div>
      <div className={cn("flex-1", isUser && "text-right")}>
        <div
          className={cn(
            "inline-block max-w-[75%] rounded-lg px-4 py-2.5 text-sm",
            isUser ? "bg-primary text-primary-foreground" : "bg-card border border-border"
          )}
        >
          <div className="whitespace-pre-wrap leading-relaxed">{message.content}</div>
          {message.tool_calls && (
            <div className="mt-3 pt-3 border-t border-border/30 space-y-2 text-left">
              {Object.entries(message.tool_calls).map(([key, value]) => (
                <ToolCallCard key={key} name={key} payload={value} />
              ))}
            </div>
          )}
        </div>
        <div className={cn("text-[11px] text-muted-foreground mt-1 px-1", isUser && "text-right")}>
          {message.latency_ms ? `${message.latency_ms} ms · ` : ""}
          {formatRelative(message.created_at)}
        </div>
      </div>
    </div>
  );
}

function PendingBubble({
  content,
  thinking,
  toolCalls,
}: {
  content: string;
  thinking?: string;
  toolCalls?: Array<{ name: string; payload: unknown }>;
}) {
  return (
    <div className="flex gap-3">
      <div className="h-8 w-8 rounded-md flex items-center justify-center shrink-0 bg-secondary text-secondary-foreground">
        <Loader2 className="h-4 w-4 animate-spin" />
      </div>
      <div className="flex-1">
        <div className="inline-block max-w-[75%] rounded-lg px-4 py-2.5 text-sm bg-card border border-border space-y-2">
          {thinking && (
            <div className="text-xs text-muted-foreground italic border-l-2 border-muted pl-2">
              Thinking… {thinking}
            </div>
          )}
          {content && <div className="whitespace-pre-wrap leading-relaxed">{content}</div>}
          {!content && !thinking && (
            <span className="text-muted-foreground italic">Thinking…</span>
          )}
          {toolCalls && toolCalls.length > 0 && (
            <div className="space-y-2">
              {toolCalls.map((tc, i) => (
                <ToolCallCard key={`${tc.name}-${i}`} name={tc.name} payload={tc.payload} />
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function ChatComposer({
  value,
  onChange,
  onSend,
  disabled,
}: {
  value: string;
  onChange: (v: string) => void;
  onSend: () => void;
  disabled: boolean;
}) {
  return (
    <div className="border-t border-border p-4">
      <div className="max-w-3xl mx-auto flex gap-2 items-end">
        <Textarea
          value={value}
          onChange={(e) => onChange(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              onSend();
            }
          }}
          placeholder="Ask about your business..."
          rows={2}
          className="resize-none"
        />
        <Button onClick={onSend} disabled={disabled || !value.trim()} size="icon" className="h-10 w-10">
          <Send className="h-4 w-4" />
        </Button>
      </div>
      <div className="max-w-3xl mx-auto mt-2 text-[11px] text-muted-foreground">
        Press Enter to send, Shift+Enter for newline.
      </div>
    </div>
  );
}
