import { useEffect, useMemo, useRef, useState } from "react";
import { useParams, useNavigate, Link } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Send, Bot, User, Plus, Phone, Globe, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
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

  const activeThreadId = params.threadId ?? threads[0]?.id ?? null;

  const { data: messagesData } = useQuery({
    queryKey: ["messages", activeThreadId],
    queryFn: async () =>
      activeThreadId
        ? (await api.get<{ messages: Message[] }>(`/threads/${activeThreadId}/messages`)).data.messages
        : [],
    enabled: !!activeThreadId,
  });
  const persistedMessages = messagesData ?? [];

  // Live messages from the WebSocket stream. We keep them separate from the
  // persisted history so we can render in-flight events without waiting for
  // the next refetch.
  const [liveAssistant, setLiveAssistant] = useState<{ jobId: string; content: string } | null>(null);
  const [error, setError] = useState<string | null>(null);

  useThreadStream(activeThreadId, (evt: ChatEvent) => {
    if (evt.type === "started") {
      setLiveAssistant({ jobId: evt.job_id, content: "" });
    } else if (evt.type === "delta") {
      setLiveAssistant((prev) =>
        prev && prev.jobId === evt.job_id
          ? { ...prev, content: prev.content + (evt.content ?? "") }
          : { jobId: evt.job_id, content: evt.content ?? "" },
      );
    } else if (evt.type === "final") {
      setLiveAssistant(null);
      qc.invalidateQueries({ queryKey: ["messages", evt.thread_id] });
      qc.invalidateQueries({ queryKey: ["threads"] });
    } else if (evt.type === "error") {
      setLiveAssistant(null);
      setError(evt.error ?? "Something went wrong");
    }
  });

  const [input, setInput] = useState("");
  const [sending, setSending] = useState(false);

  async function send() {
    const text = input.trim();
    if (!text || sending) return;
    setError(null);
    setSending(true);
    try {
      const res = await api.post<{ thread_id: string; is_new_thread: boolean }>("/chat", {
        message: text,
      });
      setInput("");
      if (res.data.thread_id !== activeThreadId) {
        navigate({ to: "/chat/$threadId", params: { threadId: res.data.thread_id } });
      }
      qc.invalidateQueries({ queryKey: ["threads"] });
      qc.invalidateQueries({ queryKey: ["messages", res.data.thread_id] });
    } catch (e: any) {
      setError(e?.response?.data?.error || "Send failed");
    } finally {
      setSending(false);
    }
  }

  return (
    <div className="grid grid-cols-[280px_1fr] h-full">
      <aside className="border-r border-border flex flex-col overflow-hidden">
        <div className="p-3 border-b border-border flex items-center justify-between">
          <div className="text-sm font-semibold">Threads</div>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => navigate({ to: "/chat" })}
            title="New conversation"
          >
            <Plus className="h-4 w-4" />
          </Button>
        </div>
        <div className="flex-1 overflow-y-auto">
          {threads.length === 0 && (
            <div className="p-4 text-xs text-muted-foreground">
              Send your first message to start a conversation.
            </div>
          )}
          {threads.map((t) => (
            <Link
              key={t.id}
              to="/chat/$threadId"
              params={{ threadId: t.id }}
              className={cn(
                "block px-3 py-3 border-b border-border/50 hover:bg-accent transition-colors",
                t.id === activeThreadId && "bg-accent",
              )}
            >
              <div className="flex items-center gap-2">
                {t.channel === "whatsapp" ? (
                  <Phone className="h-3 w-3 text-green-600" />
                ) : (
                  <Globe className="h-3 w-3 text-blue-600" />
                )}
                <div className="text-sm font-medium truncate flex-1">{t.title || "New conversation"}</div>
              </div>
              <div className="text-xs text-muted-foreground truncate mt-0.5">
                {t.phone_number ? t.phone_number : "Dashboard"} · {formatRelative(t.last_message_at)}
              </div>
            </Link>
          ))}
        </div>
      </aside>

      <section className="flex flex-col h-full overflow-hidden">
        <ChatHeader thread={threads.find((t) => t.id === activeThreadId)} />
        <ChatTimeline messages={persistedMessages} live={liveAssistant} />
        {error && (
          <div className="px-6 py-2 text-sm text-destructive border-t border-destructive/30 bg-destructive/5">
            {error}
          </div>
        )}
        <ChatComposer
          value={input}
          onChange={setInput}
          onSend={send}
          disabled={sending}
        />
      </section>
    </div>
  );
}

function ChatHeader({ thread }: { thread: Thread | undefined }) {
  if (!thread) {
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
  live: { jobId: string; content: string } | null;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    ref.current?.scrollTo({ top: ref.current.scrollHeight, behavior: "smooth" });
  }, [messages.length, live?.content]);

  const items = useMemo(() => {
    const out: Array<Message | { id: string; pending: true; content: string }> = [...messages];
    if (live) {
      out.push({ id: `live-${live.jobId}`, pending: true, content: live.content });
    }
    return out;
  }, [messages, live]);

  return (
    <div ref={ref} className="flex-1 overflow-y-auto px-6 py-6">
      <div className="max-w-3xl mx-auto space-y-6">
        {items.length === 0 && (
          <div className="text-center text-muted-foreground py-12 text-sm">
            No messages yet. Ask something like “revenue by month for the last 6 months” to get started.
          </div>
        )}
        {items.map((m) => {
          if ("pending" in m) {
            return <PendingBubble key={m.id} content={m.content} />;
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
          isUser ? "bg-primary text-primary-foreground" : "bg-secondary text-secondary-foreground",
        )}
      >
        {isUser ? <User className="h-4 w-4" /> : <Bot className="h-4 w-4" />}
      </div>
      <div className={cn("flex-1", isUser && "text-right")}>
        <div
          className={cn(
            "inline-block max-w-[75%] rounded-lg px-4 py-2.5 text-sm",
            isUser ? "bg-primary text-primary-foreground" : "bg-card border border-border",
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

function PendingBubble({ content }: { content: string }) {
  return (
    <div className="flex gap-3">
      <div className="h-8 w-8 rounded-md flex items-center justify-center shrink-0 bg-secondary text-secondary-foreground">
        <Loader2 className="h-4 w-4 animate-spin" />
      </div>
      <div className="flex-1">
        <div className="inline-block max-w-[75%] rounded-lg px-4 py-2.5 text-sm bg-card border border-border">
          {content || (
            <span className="text-muted-foreground italic">Thinking…</span>
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
