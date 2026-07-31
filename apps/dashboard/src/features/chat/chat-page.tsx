import {
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useCallback,
} from "react";
import { useParams, useNavigate } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Send, Loader2, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";
import { useAuthStore } from "@/store/auth";
import { microToUsd } from "@/features/usage/labels";
import type {
  BudgetState,
  ChatEvent,
  ConversationThread,
  Message,
  SendMessageResponse,
} from "@argentum/api-types";
import { useModels } from "@/lib/use-models";
import { useAgents } from "./use-agents";
import { AgentBadge, AgentPicker } from "./agent-picker";
import { useThreadStream } from "./use-thread-stream";
import { ToolCallCard } from "./tool-call-card";
import { MarkdownRenderer } from "./markdown-renderer";
import { formatLatencySeconds, formatMessageTimestamp } from "./format";
import { apiErrorMessage } from "@/lib/api-error";

export function ChatPage() {
  const params = useParams({ strict: false }) as { threadId?: string };
  const navigate = useNavigate();
  const qc = useQueryClient();

  const { data: threadsData } = useQuery({
    queryKey: ["threads"],
    queryFn: async () =>
      (await api.get<{ threads: ConversationThread[] }>("/threads")).data.threads,
  });
  const threads = threadsData ?? [];

  const activeThreadId = params.threadId ?? null;
  const activeThreadIdRef = useRef<string | null>(activeThreadId);
  activeThreadIdRef.current = activeThreadId;
  const isNewChat = activeThreadId === null;

  const { data: messagesData } = useQuery({
    queryKey: ["messages", activeThreadId],
    queryFn: async () =>
      activeThreadId
        ? (
            await api.get<{ messages: Message[] }>(
              `/threads/${activeThreadId}/messages`,
            )
          ).data.messages
        : [],
    enabled: !!activeThreadId,
  });
  const persistedMessages = messagesData ?? [];

  const [optimisticMessages, setOptimisticMessages] = useState<Message[]>([]);

  const [liveAssistant, setLiveAssistant] = useState<{
    jobId: string;
    content: string;
    thinking?: string;
    toolCalls?: Array<{ name: string; payload: unknown }>;
    /** Tool-calling round the agent is on, and how many it may spend. A
     *  multi-step turn can go quiet for tens of seconds between deltas; this
     *  is what distinguishes "still working" from "stalled". */
    iteration?: { current: number; max: number };
  } | null>(null);

  const [error, setError] = useState<string | null>(null);
  const [input, setInput] = useState("");
  const [sending, setSending] = useState(false);

  /**
   * Which agent the next new conversation opens on (T-S3).
   *
   * Only meaningful on the new-chat screen: once the thread exists its agent
   * is fixed, and the send below stops passing this. null means "whatever the
   * company's default is", resolved by the backend per turn rather than frozen
   * here — so a roster that still has one agent behaves exactly as it did.
   */
  const agents = useAgents();
  const [pickedAgentId, setPickedAgentId] = useState<string | null>(null);
  // What a new conversation would run as — the picked agent, or the company
  // default when nothing is picked, which is the same resolution the backend
  // applies to a turn that names none.
  const newChatAgent = pickedAgentId
    ? (agents.byId.get(pickedAgentId) ?? null)
    : agents.fallback;

  /**
   * The credit warning lives in the query cache, not in component state.
   * `/chat` and `/chat/$threadId` are two routes rendering this same
   * component, so the very send that produces a warning also navigates —
   * unmounting one route and mounting the other, which resets every useState
   * in the file. The first send of a session is exactly the case that would
   * silently lose its banner. The QueryClient sits above the router, so it
   * survives; `queryFn` returning null is what the cache holds until a send
   * writes a real one.
   *
   * Dismissing is not sticky on purpose: the banner returns on the next send
   * that is still near the limit, which is the only moment it is worth
   * anything.
   */
  const { data: budgetWarning = null } = useQuery<BudgetState | null>({
    queryKey: ["budget-warning"],
    queryFn: () => null,
    staleTime: Infinity,
    gcTime: Infinity,
    refetchOnMount: false,
    refetchOnWindowFocus: false,
  });
  const setBudgetWarning = useCallback(
    (w: BudgetState | null) => qc.setQueryData(["budget-warning"], w),
    [qc],
  );

  const pollTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const finalReceivedRef = useRef(false);
  const timelineRef = useRef<HTMLDivElement>(null);

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
          const res = await api.get<{ messages: Message[] }>(
            `/threads/${threadId}/messages`,
          );
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
    [qc, stopPolling],
  );

  const prevThreadIdRef = useRef<string | null | undefined>(undefined);

  useEffect(() => {
    return () => stopPolling();
  }, [stopPolling]);

  /** Leaving a thread closes the WS (useThreadStream cleanup → ws.close), but
   *  liveAssistant would keep showing the previous thread’s stream without this.
   *  Do not stop fallback polling when opening a thread from new chat (null → id). */
  useLayoutEffect(() => {
    setLiveAssistant(null);
    setError(null);

    const prev = prevThreadIdRef.current;
    if (prev === undefined) {
      prevThreadIdRef.current = activeThreadId;
      return;
    }

    if (prev !== activeThreadId) {
      const switchedThread =
        prev !== null && activeThreadId !== null && prev !== activeThreadId;
      const leftForNewChat = prev !== null && activeThreadId === null;
      if (switchedThread || leftForNewChat) {
        finalReceivedRef.current = true;
        stopPolling();
      }
    }

    prevThreadIdRef.current = activeThreadId;
  }, [activeThreadId, stopPolling]);

  useThreadStream(activeThreadId, (evt: ChatEvent) => {
    if (evt.thread_id !== activeThreadIdRef.current) return;
    if (evt.type === "started") {
      finalReceivedRef.current = false;
      setLiveAssistant({ jobId: evt.job_id, content: "" });
    } else if (evt.type === "delta") {
      setLiveAssistant((prev) =>
        prev && prev.jobId === evt.job_id
          ? { ...prev, content: prev.content + (evt.content ?? "") }
          : { jobId: evt.job_id, content: evt.content ?? "" },
      );
    } else if (evt.type === "thinking") {
      setLiveAssistant((prev) =>
        prev && prev.jobId === evt.job_id
          ? { ...prev, thinking: evt.thinking_step }
          : { jobId: evt.job_id, content: "", thinking: evt.thinking_step },
      );
    } else if (evt.type === "iteration") {
      const current = Number(evt.metadata?.iteration ?? 0);
      const max = Number(evt.metadata?.max_iterations ?? 0);
      if (current > 0) {
        setLiveAssistant((prev) =>
          prev && prev.jobId === evt.job_id
            ? { ...prev, iteration: { current, max } }
            : { jobId: evt.job_id, content: "", iteration: { current, max } },
        );
      }
    } else if (evt.type === "tool_call" || evt.type === "tool_result") {
      setLiveAssistant((prev) => {
        if (!prev) return { jobId: evt.job_id, content: "" };
        const calls = prev.toolCalls ? [...prev.toolCalls] : [];
        if (evt.tool_call) {
          calls.push({
            name: evt.tool_call.name,
            payload:
              evt.type === "tool_call"
                ? (evt.tool_call.arguments ?? {})
                : (evt.tool_call.result ?? {}),
          });
        }
        return { ...prev, toolCalls: calls };
      });
    } else if (evt.type === "final") {
      finalReceivedRef.current = true;
      setLiveAssistant(null);
      setOptimisticMessages((prev) =>
        prev.filter((m) => m.thread_id !== evt.thread_id),
      );
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

  async function send() {
    const text = input.trim();
    if (!text || sending) return;
    setError(null);
    setSending(true);

    const targetThreadId = activeThreadId;

    try {
      const res = await api.post<SendMessageResponse>("/chat", {
        message: text,
        thread_id: targetThreadId ?? undefined,
        // Only on the send that opens a conversation. On an existing thread
        // the backend refuses an agent that disagrees with the one already on
        // it, and sending the same one back is a round trip that can only ever
        // agree with itself.
        agent_id: targetThreadId ? undefined : (pickedAgentId ?? undefined),
      });

      setBudgetWarning(res.data.budget_warning ?? null);

      const newThreadId = res.data.thread_id;
      const userMsgId = res.data.user_msg_id;

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

      if (res.data.is_new_thread) {
        startPolling(newThreadId);
      }
    } catch (e: unknown) {
      setError(apiErrorMessage(e, "Send failed"));
    } finally {
      setSending(false);
    }
  }

  const displayedMessages = useMemo(() => {
    const threadOptimistic = optimisticMessages.filter(
      (m) => m.thread_id === activeThreadId,
    );
    const merged = [...persistedMessages, ...threadOptimistic];
    const map = new Map<string, Message>();
    for (const m of merged) map.set(m.id, m);
    return Array.from(map.values()).sort(
      (a, b) =>
        new Date(a.created_at).getTime() - new Date(b.created_at).getTime(),
    );
  }, [persistedMessages, optimisticMessages, activeThreadId]);

  useEffect(() => {
    timelineRef.current?.scrollTo({
      top: timelineRef.current.scrollHeight,
      behavior: "smooth",
    });
  }, [
    displayedMessages.length,
    liveAssistant?.content,
    liveAssistant?.thinking,
    liveAssistant?.toolCalls?.length,
  ]);

  return (
    <section className="flex flex-col h-full overflow-hidden">
      {isNewChat ? (
        /* ── New Chat: Welcome screen ─────────────────────────────── */
        <div className="flex-1 flex flex-col items-center justify-center px-6">
          <div className="w-full max-w-2xl flex flex-col items-center gap-6">
            {/* Welcome text */}
            <div className="text-center space-y-2">
              <p className="text-sm font-medium text-primary tracking-wide uppercase">
                Hi, welcome to Argentum
              </p>
              <h1 className="text-3xl font-bold text-foreground tracking-tight">
                How can I help you today?
              </h1>
              <p className="text-sm text-muted-foreground max-w-sm mx-auto">
                Ask me anything about your business — sales, customers,
                inventory, or trends.
              </p>
            </div>
            {budgetWarning && (
              <BudgetWarningBanner
                warning={budgetWarning}
                onDismiss={() => setBudgetWarning(null)}
                className="w-full"
              />
            )}
            {/* Agent — pickable only here, before the thread exists */}
            <AgentPicker
              agents={agents.selectable}
              value={pickedAgentId ?? agents.fallback?.id ?? null}
              onChange={setPickedAgentId}
              className="self-start"
            />
            {/* What this agent was made to be asked (T-B3). Only an agent
                created from a template has any, and clicking one fills the
                composer rather than sending: the first turn a customer runs is
                the one they should have read first. */}
            <StarterQuestions
              questions={agents.starterQuestionsFor(newChatAgent)}
              onPick={setInput}
            />
            {/* Composer */}
            <ChatComposer
              value={input}
              onChange={setInput}
              onSend={send}
              disabled={sending}
            />
          </div>
        </div>
      ) : (
        <>
          {/* ── Existing thread ──────────────────────────────────────── */}
          <ChatHeader
            thread={threads.find((t) => t.id === activeThreadId)}
            agentName={agentNameFor(
              threads.find((t) => t.id === activeThreadId),
              agents,
            )}
            className="shrink-0 bg-background/95 backdrop-blur-md z-20"
          />
          <div ref={timelineRef} className="flex-1 overflow-y-auto px-3 sm:px-6 py-4">
            <div className="max-w-3xl mx-auto space-y-5">
              {displayedMessages.length === 0 && !liveAssistant && (
                <div className="text-center text-muted-foreground py-12 text-sm">
                  No messages yet — start the conversation below.
                </div>
              )}
              {displayedMessages.map((m) => (
                <MessageBubble key={m.id} message={m} />
              ))}
              {liveAssistant && (
                <PendingBubble
                  key={`live-${liveAssistant.jobId}`}
                  content={liveAssistant.content}
                  thinking={liveAssistant.thinking}
                  toolCalls={liveAssistant.toolCalls}
                  iteration={liveAssistant.iteration}
                />
              )}
            </div>
          </div>
          {budgetWarning && (
            <BudgetWarningBanner
              warning={budgetWarning}
              onDismiss={() => setBudgetWarning(null)}
              className="mx-3 sm:mx-4 mb-2 shrink-0"
            />
          )}
          {error && (
            <div className="px-6 py-2 text-sm text-destructive bg-destructive/5 rounded-lg mx-3 sm:mx-4 mb-2 shrink-0">
              {error}
            </div>
          )}
          <ChatComposer
            value={input}
            onChange={setInput}
            onSend={send}
            disabled={sending}
            className="shrink-0 bg-background/95 backdrop-blur-md z-20"
          />
        </>
      )}
    </section>
  );
}

/* ── Credit warning banner ───────────────────────────────────────────── */
/** Shown when the workspace is close to the end of its credit grant. The turn
 *  already ran — this is a heads-up, not a refusal, so it is styled as a
 *  notice rather than with the destructive palette the error strip uses. */
function BudgetWarningBanner({
  warning,
  onDismiss,
  className,
}: {
  warning: BudgetState;
  onDismiss: () => void;
  className?: string;
}) {
  const balance = microToUsd(warning.balance_micro_usd);
  return (
    <div
      role="status"
      className={cn(
        "flex items-start gap-3 rounded-lg border border-primary/30 bg-primary/5 px-4 py-2.5 text-sm",
        className,
      )}
    >
      <div className="flex-1 min-w-0">
        <span className="font-medium">
          {warning.remaining_pct}% of your Argentum credit is left
        </span>
        <span className="text-muted-foreground">
          {" "}
          — ${balance.toFixed(2)} remaining. Top up before it runs out to keep
          chat, scheduled reports and the API working.
        </span>
      </div>
      <button
        type="button"
        onClick={onDismiss}
        aria-label="Dismiss credit warning"
        className="shrink-0 text-muted-foreground hover:text-foreground transition-colors"
      >
        <X className="size-4" />
      </button>
    </div>
  );
}

/**
 * The name to caption a thread with (T-S3).
 *
 * A thread with no `agent_id` runs as whatever the company default is at the
 * time of each turn, so it is captioned with that name rather than left blank —
 * and it resolves live, which is correct: an unpinned conversation really does
 * follow the default when an admin moves it.
 *
 * Undefined means "say nothing": the roster has not loaded, or this deployment
 * has none. A caption is not worth a layout shift or a guess.
 */
function agentNameFor(
  thread: ConversationThread | undefined,
  agents: ReturnType<typeof useAgents>,
): string | undefined {
  if (!thread) return undefined;
  if (thread.agent_id) return agents.byId.get(thread.agent_id)?.name;
  return agents.fallback?.name;
}

/**
 * The starter questions of the template an agent was created from (T-B3).
 *
 * The cheapest possible proof that the agent works: a customer who has just
 * picked "Operations" out of a gallery has no idea what it can be asked, and
 * three concrete questions answer that faster than any description. They fill
 * the composer rather than sending — a first turn that runs before the customer
 * has read it teaches them nothing and spends a credit.
 *
 * Renders nothing for an agent created from blank, one created before templates
 * existed, or a deployment with no gallery. All three are the screen exactly as
 * it was.
 */
function StarterQuestions({
  questions,
  onPick,
}: {
  questions: string[];
  onPick: (q: string) => void;
}) {
  if (questions.length === 0) return null;
  return (
    <div className="w-full max-w-3xl self-start space-y-2">
      <p className="px-1 text-xs text-muted-foreground">Try one of these:</p>
      <div className="flex flex-wrap gap-2">
        {questions.map((q) => (
          <button
            key={q}
            type="button"
            onClick={() => onPick(q)}
            className="rounded-full border border-border/70 bg-card px-3 py-1.5 text-xs text-foreground shadow-sm transition-colors hover:border-primary/60 hover:bg-accent/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            {q}
          </button>
        ))}
      </div>
    </div>
  );
}

/* ── Chat Header — floating, no border ───────────────────────────────── */
function ChatHeader({
  thread,
  agentName,
  className,
}: {
  thread?: ConversationThread;
  agentName?: string;
  className?: string;
}) {
  if (!thread) return null;
  return (
    <header className={cn("px-3 sm:px-6 pt-5 pb-2 shrink-0", className)}>
      <div className="max-w-3xl mx-auto">
        <div className="text-base font-semibold leading-snug">
          {thread.title || "Conversation"}
        </div>
        <div className="text-xs text-muted-foreground mt-0.5 flex items-center gap-2">
          <span>
            {thread.channel === "whatsapp"
              ? `WhatsApp · ${thread.phone_number}`
              : "Dashboard"}
          </span>
          {agentName && (
            <>
              <span aria-hidden>·</span>
              <AgentBadge name={agentName} />
            </>
          )}
        </div>
      </div>
    </header>
  );
}

/* ── Message Bubble ──────────────────────────────────────────────────── */
function MessageBubble({ message }: { message: Message }) {
  const isUser = message.role === "user";
  const user = useAuthStore((s) => s.user);
  const userInitials = user?.name
    ? user.name
        .split(" ")
        .map((n) => n[0])
        .join("")
        .slice(0, 2)
        .toUpperCase()
    : "U";
  return (
    <div
      className={cn(
        "flex gap-3 items-end",
        isUser ? "flex-row-reverse" : "flex-row",
      )}
    >
      {/* Avatar */}
      <div
        className={cn(
          "h-7 w-7 rounded-full flex items-center justify-center shrink-0 text-[11px] font-bold",
          isUser
            ? "bg-[#212427] text-white dark:bg-white dark:text-[#212427]"
            : "bg-muted text-muted-foreground border border-border",
        )}
      >
        {isUser ? (
          userInitials
        ) : (
          <img
            src="/images/shortLogo_black.svg"
            alt="A"
            className="argentum-logo h-3.5 w-3.5"
          />
        )}
      </div>

      {/* Bubble */}
      <div
        className={cn(
          "flex-1 min-w-0",
          isUser && "flex flex-col items-end",
        )}
      >
        {isUser ? (
          <div className="inline-block max-w-[78%] rounded-3xl rounded-br-md px-4 py-3 text-sm leading-relaxed bg-[#212427] text-white dark:bg-white dark:text-[#212427]">
            <div className="whitespace-pre-wrap">{message.content}</div>
            {message.tool_calls && (
              <div className="mt-3 pt-3 border-t border-border/30 space-y-2 text-left">
                {Object.entries(message.tool_calls).map(([key, value]) => (
                  <ToolCallCard key={key} name={key} payload={value} />
                ))}
              </div>
            )}
          </div>
        ) : (
          <div className="text-sm leading-relaxed text-foreground">
            <MarkdownRenderer content={message.content} />
            {message.tool_calls && (
              <div className="mt-3 pt-3 border-t border-border/30 space-y-2 text-left">
                {Object.entries(message.tool_calls).map(([key, value]) => (
                  <ToolCallCard key={key} name={key} payload={value} />
                ))}
              </div>
            )}
          </div>
        )}
        <div
          className={cn(
            "text-[11px] text-muted-foreground mt-1 px-1",
            isUser && "text-right",
          )}
        >
          {message.latency_ms
            ? `${formatLatencySeconds(message.latency_ms)} · `
            : ""}
          {formatMessageTimestamp(message.created_at)}
        </div>
      </div>
    </div>
  );
}

/* ── Pending (streaming) Bubble ─────────────────────────────────────── */
function PendingBubble({
  content,
  thinking,
  toolCalls,
  iteration,
}: {
  content: string;
  thinking?: string;
  toolCalls?: Array<{ name: string; payload: unknown }>;
  iteration?: { current: number; max: number };
}) {
  const progress =
    iteration && iteration.current > 1
      ? iteration.max > 0
        ? `Step ${iteration.current} of ${iteration.max}…`
        : `Step ${iteration.current}…`
      : "Thinking…";
  return (
    <div className="flex gap-3 items-end">
      <div className="h-7 w-7 rounded-full flex items-center justify-center shrink-0 bg-muted text-muted-foreground border border-border text-[11px] font-bold">
        <Loader2 className="h-3.5 w-3.5 animate-spin" />
      </div>
      <div className="flex-1 min-w-0">
        <div className="text-sm text-foreground space-y-2">
          {thinking && (
            <div className="text-xs text-muted-foreground italic border-l-2 border-primary/40 pl-2">
              {progress} {thinking}
            </div>
          )}
          {content && <MarkdownRenderer content={content} />}
          {!content && !thinking && (
            <span className="text-muted-foreground italic text-xs">
              {progress}
            </span>
          )}
          {toolCalls && toolCalls.length > 0 && (
            <div className="space-y-2">
              {toolCalls.map((tc, i) => (
                <ToolCallCard
                  key={`${tc.name}-${i}`}
                  name={tc.name}
                  payload={tc.payload}
                />
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

/* ── Unified Chat Composer (used in both new-chat and thread view) ───── */
function ChatComposer({
  value,
  onChange,
  onSend,
  disabled,
  className,
}: {
  value: string;
  onChange: (v: string) => void;
  onSend: () => void;
  disabled: boolean;
  className?: string;
}) {
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const { data: models } = useModels();

  const handleChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    onChange(e.target.value);
    if (textareaRef.current) {
      textareaRef.current.style.height = "auto";
      textareaRef.current.style.height = `${textareaRef.current.scrollHeight}px`;
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      onSend();
    }
  };

  return (
    <div className={cn("px-3 sm:px-4 pb-4 pt-1 w-full shrink-0", className)}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          onSend();
        }}
        className="w-full max-w-3xl mx-auto"
      >
        <div className="w-full bg-card border border-border rounded-3xl p-3 shadow-sm focus-within:ring-2 focus-within:ring-primary/30 focus-within:border-primary/50 transition-all">
          <div className="flex min-h-12 items-center px-1.5">
            <div className="flex-1 overflow-auto max-h-48">
              <Textarea
                ref={textareaRef}
                value={value}
                onChange={handleChange}
                onKeyDown={handleKeyDown}
                placeholder="Ask about your business…"
                className="min-h-0 resize-none rounded-none border-0 p-0 text-sm placeholder:text-muted-foreground focus-visible:ring-0 focus-visible:ring-offset-0 bg-transparent"
                rows={1}
              />
            </div>
          </div>
          <div className="flex items-center justify-between px-1.5 pt-1">
            <p className="text-[11px] text-muted-foreground">
              Enter to send · Shift+Enter for newline
              {models?.primary.model && (
                <span className="ml-2 opacity-70">· {models.primary.model}</span>
              )}
            </p>
            <Button
              type="submit"
              size="icon"
              className="rounded-full h-8 w-8 bg-primary hover:bg-primary/90 text-primary-foreground shadow-sm"
              disabled={disabled || !value.trim()}
            >
              <Send className="size-3.5" />
            </Button>
          </div>
        </div>
      </form>
    </div>
  );
}
