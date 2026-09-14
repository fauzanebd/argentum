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
import { Send, Loader2, X, Copy, Check } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { Shimmer, Elapsed } from "@/components/ui/shimmer";
import { useElapsedSeconds } from "@/hooks/use-elapsed";
import { ThinkingTrace } from "./thinking-trace";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";
import { useAuthStore } from "@/store/auth";
import { useComposerStore } from "@/store/composer";
import { microToUsd } from "@/features/usage/labels";
import type {
  BudgetState,
  ChatEvent,
  ConversationThread,
  Message,
  SendMessageResponse,
  ThreadParticipant,
} from "@argentum/api-types";
import { useModels } from "@/lib/use-models";
import { useAgents } from "./use-agents";
import { AgentBadge, AgentPicker } from "./agent-picker";
import { ParticipantBar } from "./participant-bar";
import { useParticipants } from "./use-participants";
import { agentColorIndex, colorForAgent } from "./agent-colors";
import { MentionMenu, mentionQueryAt, applyMention, type MentionQuery } from "./mention-menu";
import { useThreadStream } from "./use-thread-stream";
import { ToolCallCard } from "./tool-call-card";
import { MessageFeedback } from "./message-feedback";
import { NextStepChips } from "./next-steps";
import { PendingApprovals } from "@/features/actions/approval-card";
import { PENDING_ACTIONS_KEY } from "@/features/actions/use-actions";
import { MarkdownRenderer } from "./markdown-renderer";
import { formatLatencySeconds, formatMessageTimestamp } from "./format";
import { apiErrorMessage } from "@/lib/api-error";

/**
 * The assistant turn currently streaming, as this component holds it.
 *
 * Every field except `jobId` and `startedAt` is filled by a different event
 * type, and any of them can arrive first — a turn that opens with a tool call
 * never sends `started` before it. `blankTurn` is what every handler falls back
 * to for that reason: one place that knows the shape of an empty turn, so a new
 * field cannot be forgotten by five call sites independently.
 */
type LiveTurn = {
  jobId: string;
  /** Which agent is producing this turn (T-N1), and what to call it.
   *
   *  In a room, N turns of one user message share a `jobId` — it is the user
   *  message id — and are told apart only by this. It is "" for a turn with no
   *  roster agent, which is what `turnKey` falls back to the job id for. */
  agentId: string;
  agentName: string;
  content: string;
  /** Every thinking step of this turn, in arrival order (T-U4).
   *
   *  This was a single `thinking?: string` that each `thinking` event
   *  overwrote, so the trace could only ever show the agent's most recent
   *  sentence — the earlier steps arrived, were rendered for a moment, and were
   *  dropped. They were always on the wire; nothing kept them. */
  thinkingSteps: string[];
  toolCalls?: Array<{ name: string; payload: unknown }>;
  /** Tool-calling round the agent is on, and how many it may spend. A
   *  multi-step turn can go quiet for tens of seconds between deltas; this is
   *  what distinguishes "still working" from "stalled". */
  iteration?: { current: number; max: number };
  /** When the browser learned this turn had started, for the elapsed caption
   *  (T-U3). A client clock reading: the `started` event carries no timestamp,
   *  and this is the moment the reader began waiting anyway. */
  startedAt: number;
};

function blankTurn(jobId: string, agentId = "", agentName = ""): LiveTurn {
  return { jobId, agentId, agentName, content: "", thinkingSteps: [], startedAt: Date.now() };
}

/**
 * The key a live turn is held under (T-N4).
 *
 * The agent, falling back to the job id. A room's N concurrent turns share one
 * job id and differ by agent, so keying on the job id alone would make the last
 * event win and three agents would render as one flickering bubble. A
 * single-agent thread has one turn either way, and an unscoped turn — the eval
 * harness, a company whose roster never seeded — carries no agent and falls
 * back to exactly the behaviour it had before this ticket.
 */
function turnKey(evt: { agent_id?: string; job_id: string }): string {
  return evt.agent_id || evt.job_id;
}

/**
 * The turn that was already running when this socket opened (T-U12).
 *
 * A thread's WebSocket closes when the reader opens another conversation, so
 * everything the agent did while they were away arrived on a socket nobody was
 * holding. The one that opens on the way back is greeted with this — the same
 * turn, as far as it has got — instead of staying silent until the agent's next
 * token, which on a turn waiting for a slow tool was tens of seconds of a
 * screen that looked like nothing had been asked.
 *
 * `started_at` is the server's clock here, unlike a turn watched from the
 * start, and that is the point: the elapsed caption should say how long the
 * agent has been working, not how long this browser has been looking at it.
 */
function resumedTurn(live: NonNullable<ChatEvent["live"]>, agentId = "", agentName = ""): LiveTurn {
  const startedAt = Date.parse(live.started_at);
  return {
    jobId: live.job_id,
    agentId,
    agentName,
    content: live.content ?? "",
    thinkingSteps: live.thinking_steps ?? [],
    toolCalls: (live.tool_calls ?? []).map((tc) => ({
      name: tc.name,
      // A tool_result was recorded with its result and a tool_call with its
      // arguments; whichever the snapshot kept is the payload for the card.
      payload: tc.result ?? tc.arguments ?? {},
    })),
    iteration: live.iteration
      ? { current: live.iteration, max: live.max_iterations ?? 0 }
      : undefined,
    startedAt: Number.isNaN(startedAt) ? Date.now() : startedAt,
  };
}

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

  /**
   * Every turn currently streaming, keyed by `turnKey` (T-N4).
   *
   * This was one `LiveTurn | null`, which is the right shape for a thread that
   * can only ever have one turn in flight. A room addressing two agents has
   * two, concurrently, on one socket and under one job id — so the container
   * had to become a map. A single-agent thread holds exactly one entry and
   * renders identically to before.
   */
  const [liveTurns, setLiveTurns] = useState<Record<string, LiveTurn>>({});
  const liveList = useMemo(() => Object.values(liveTurns), [liveTurns]);
  const anyLive = liveList.length > 0;
  /** A mirror of `liveTurns` readable inside the event handler, which runs from
   *  a socket callback and cannot see the state it is about to change. Used for
   *  one question only: is the turn that just ended the last one running? */
  const liveTurnsRef = useRef<Record<string, LiveTurn>>({});
  useEffect(() => {
    liveTurnsRef.current = liveTurns;
  }, [liveTurns]);

  /** upsertTurn applies `fn` to one turn, creating it if this is the first
   *  event of it. Every handler below goes through here so that "which turn"
   *  is decided in one place rather than in eight. */
  const upsertTurn = useCallback(
    (evt: ChatEvent, fn: (prev: LiveTurn) => LiveTurn) => {
      setLiveTurns((prev) => {
        const key = turnKey(evt);
        const base =
          prev[key] ?? blankTurn(evt.job_id, evt.agent_id ?? "", evt.agent_name ?? "");
        return { ...prev, [key]: fn(base) };
      });
    },
    [],
  );

  const dropTurn = useCallback((evt: ChatEvent) => {
    setLiveTurns((prev) => {
      const key = turnKey(evt);
      if (!(key in prev)) return prev;
      const next = { ...prev };
      delete next[key];
      return next;
    });
  }, []);

  const [error, setError] = useState<string | null>(null);
  const [input, setInput] = useState("");
  const [sending, setSending] = useState(false);

  /**
   * Text put in the composer by something other than typing — a starter
   * question, "Ask for a change" on a dashboard.
   *
   * Both fill and never send, and the browser gate of 2026-08-17 found that
   * they also left `document.activeElement` on `<body>`: the sentence arrived
   * in the box and the cursor did not, so the reader's next keystroke went
   * nowhere and T-D23's own "the cursor lands after the link" was untrue.
   *
   * The next-step options are the exception as of 2026-08-18 — see
   * `sendNextStep` below.
   *
   * The counter is the signal rather than the text, because filling the
   * composer with the same string twice is a real gesture — clicking one chip,
   * editing it, clicking it again — and an effect keyed on the value would sit
   * out the second click.
   */
  const [focusSignal, setFocusSignal] = useState(0);
  const fillComposer = useCallback((text: string) => {
    setInput(text);
    setFocusSignal((n) => n + 1);
  }, []);

  /**
   * Which agent the next new conversation opens on (T-S3).
   *
   * Only meaningful on the new-chat screen: once the thread exists its agent
   * is fixed, and the send below stops passing this. null means "whatever the
   * company's default is", resolved by the backend per turn rather than frozen
   * here — so a roster that still has one agent behaves exactly as it did.
   */
  const agents = useAgents();
  const room = useParticipants(activeThreadId ?? null);
  const isRoom = room.isRoom;
  /** Roster-position colours, computed once for every surface that names an
   *  agent: the participant bar, the `@` menu, the streaming bubbles and the
   *  message bubbles. One map so they cannot disagree.
   *
   *  Below `useAgents`, necessarily — it reads the roster. */
  const colorIndex = useMemo(
    () => agentColorIndex([...agents.byId.keys()]),
    [agents.byId],
  );
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
  /** The one event a resumed socket can be told twice (T-U12). The server
   *  subscribes before it reads the snapshot it greets us with, so an event
   *  published in that window is both counted in the snapshot and delivered
   *  after it — appending it again would double a word or a tool card. Held in
   *  a ref because events arrive faster than state settles. */
  const resumeEchoRef = useRef<{ jobId: string; ts: string } | null>(null);
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
  /** Whether this turn called update_dashboard (T-D23). A ref rather than state:
   *  nothing renders from it, and re-rendering the transcript mid-stream to
   *  record a fact only `final` reads would be a wasted pass over every bubble. */
  const dashboardEditedRef = useRef(false);

  useEffect(() => {
    return () => stopPolling();
  }, [stopPolling]);

  /**
   * A message another surface asked us to open with (T-D23).
   *
   * "Ask for a change" on a dashboard lands here with the dashboard's markdown
   * link already in the composer and the cursor after it. Prefilled, never
   * sent — T-U13's rule and the starter questions': a dashboard edit that fires
   * from a button press is an action nobody approved.
   *
   * Read-and-clear, so switching threads afterwards does not re-seed over
   * whatever the reader has started typing.
   */
  const takePrefill = useComposerStore((s) => s.take);
  useEffect(() => {
    const pending = takePrefill();
    if (pending) fillComposer(pending);
  }, [takePrefill, fillComposer]);

  /** Leaving a thread closes the WS (useThreadStream cleanup → ws.close), but
   *  the live turns would keep showing the previous thread’s stream without this.
   *  Do not stop fallback polling when opening a thread from new chat (null → id). */
  useLayoutEffect(() => {
    setLiveTurns({});
    setError(null);
    resumeEchoRef.current = null;

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

    const echo = resumeEchoRef.current;
    if (echo && evt.job_id === echo.jobId && evt.timestamp === echo.ts) {
      resumeEchoRef.current = null;
      return;
    }

    if (evt.type === "state") {
      // Nothing running: the socket opened between turns, which is every
      // ordinary connection and needs no state on the screen.
      if (!evt.live) {
        resumeEchoRef.current = null;
        return;
      }
      resumeEchoRef.current = {
        jobId: evt.live.job_id,
        ts: evt.live.last_event_at,
      };
      finalReceivedRef.current = false;
      setLiveTurns({
        [turnKey(evt)]: resumedTurn(evt.live, evt.agent_id ?? "", evt.agent_name ?? ""),
      });
    } else if (evt.type === "started") {
      finalReceivedRef.current = false;
      upsertTurn(evt, () => blankTurn(evt.job_id, evt.agent_id ?? "", evt.agent_name ?? ""));
    } else if (evt.type === "delta") {
      upsertTurn(evt, (prev) => ({ ...prev, content: prev.content + (evt.content ?? "") }));
    } else if (evt.type === "thinking") {
      const step = evt.thinking_step?.trim();
      if (step) {
        // Append, but never twice in a row for the same sentence. The backend
        // re-emits the current step alongside some iteration events, and a
        // trace that lists "Checking the schema" four times reads as a stuck
        // agent rather than a working one.
        upsertTurn(evt, (prev) =>
          prev.thinkingSteps[prev.thinkingSteps.length - 1] === step
            ? prev
            : { ...prev, thinkingSteps: [...prev.thinkingSteps, step] },
        );
      }
    } else if (evt.type === "iteration") {
      const current = Number(evt.metadata?.iteration ?? 0);
      const max = Number(evt.metadata?.max_iterations ?? 0);
      if (current > 0) {
        upsertTurn(evt, (prev) => ({ ...prev, iteration: { current, max } }));
      }
    } else if (evt.type === "tool_call" || evt.type === "tool_result") {
      // A dashboard on screen has just been edited (T-D23). Noted here and acted
      // on at `final`, so the panels redraw without a manual reload — and only
      // then: a blanket invalidation on every turn would re-run every embedded
      // dashboard's queries against the tenant's warehouse for turns that
      // touched nothing, which is the load pattern DashboardView's own
      // refetchOnWindowFocus:false exists to avoid.
      if (evt.tool_call?.name === "update_dashboard") {
        dashboardEditedRef.current = true;
      }
      upsertTurn(evt, (prev) => {
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
      // **One agent has finished, not necessarily the turn.** A room addressing
      // three agents publishes three `final` events under one job id, and the
      // other two are still working when the first arrives — so the backstop
      // poll and the "we have an answer" flag wait for the last of them, and
      // only this agent's bubble goes away now.
      const lastOne = Object.keys(liveTurnsRef.current).filter(
        (k) => k !== turnKey(evt),
      ).length === 0;
      dropTurn(evt);
      if (lastOne) {
        finalReceivedRef.current = true;
        resumeEchoRef.current = null;
        stopPolling();
      }
      setOptimisticMessages((prev) =>
        prev.filter((m) => m.thread_id !== evt.thread_id),
      );
      qc.invalidateQueries({ queryKey: ["messages", evt.thread_id] });
      qc.invalidateQueries({ queryKey: ["threads"] });
      if (dashboardEditedRef.current) {
        dashboardEditedRef.current = false;
        // Both the list and every resolved dashboard: a title change shows in
        // one and a panel change in the other, and the turn may have done either.
        qc.invalidateQueries({ queryKey: ["dashboard-data"] });
        qc.invalidateQueries({ queryKey: ["dashboards"] });
      }
    } else if (evt.type === "room_event") {
      // A room's own line (T-N6): an agent's question to a colleague, a limit
      // that left a question unasked, a colleague with nothing to add. Not a
      // `final` — the asking agent is usually still streaming, and a `final`
      // under its id would close its bubble — so no live turn is touched and the
      // transcript is simply read again.
      qc.invalidateQueries({ queryKey: ["messages", evt.thread_id] });
    } else if (evt.type === "action_proposed") {
      // The agent proposed a write-capable action (T-11). Refresh the pending
      // list so its approval card appears in the strip above the composer live,
      // without waiting for the 60s backstop poll.
      qc.invalidateQueries({ queryKey: PENDING_ACTIONS_KEY });
    } else if (evt.type === "error") {
      // One agent failed. The others keep going — a room where Finance errors
      // must still deliver Ops's answer — so the same last-one-out rule applies
      // as for `final`. The error banner names the agent when there is more
      // than one, because "Something went wrong" in a room of three does not
      // say whose answer is missing.
      const lastOne = Object.keys(liveTurnsRef.current).filter(
        (k) => k !== turnKey(evt),
      ).length === 0;
      const who = evt.agent_name ? `${evt.agent_name}: ` : "";
      dropTurn(evt);
      if (lastOne) {
        finalReceivedRef.current = true;
        resumeEchoRef.current = null;
        stopPolling();
      }
      setError(who + (evt.error ?? "Something went wrong"));
    }
  });

  /**
   * Send, either what is in the composer or a sentence handed straight in.
   *
   * The argument exists for the next-step options, which send on tap (see
   * `sendNextStep`). It is passed rather than routed through `setInput` because
   * state written in an event handler is not readable in the same tick — the
   * send would go out with whatever the composer held *before* the tap.
   */
  async function send(override?: string) {
    const text = (override ?? input).trim();
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

  /**
   * A tapped next-step suggestion runs (2026-08-18, owner's call).
   *
   * It reverses T-U13's original rule — fill the composer, never send — which
   * was the starter questions' rule applied to a second surface. The two are
   * not the same gesture: a starter question is the *first* thing somebody
   * sees, with no answer above it to judge it against, while a next step is
   * offered under an answer the reader has just read, in the agent's own words,
   * with its reasoning beside it. By the time it is tapped the decision has
   * been made, and sending it back to the composer asks for it a second time.
   *
   * The credit argument T-U13 made survives as the reason the options are not
   * on every bubble and never while a turn is in flight: what stops an
   * accidental turn is that there is one card, under the newest answer only.
   */
  // Not memoised, deliberately: `send` is redeclared every render and closes
  // over the thread, the composer and the in-flight flag, so a cached callback
  // is a cached copy of all four. Nothing downstream is memoised on its
  // identity — the card is drawn from the message, not from this.
  const sendNextStep = (prompt: string) => {
    void send(prompt);
  };

  const displayedMessages = useMemo(() => {
    const threadOptimistic = optimisticMessages.filter(
      (m) => m.thread_id === activeThreadId,
    );
    const merged = [...persistedMessages, ...threadOptimistic]
      // role="tool" rows are the agent's memory of its own work (T-Q6), not
      // transcript: one row per turn holding the digests a later turn reads.
      // The assistant message beside them already carries the tool cards a
      // reader wants, and MessageBubble treats anything that is not "user" as
      // an assistant turn — so leaving them in would render a block of JSON
      // in the middle of the conversation.
      .filter((m) => m.role !== "tool");
    const map = new Map<string, Message>();
    for (const m of merged) map.set(m.id, m);
    return Array.from(map.values()).sort(
      (a, b) =>
        new Date(a.created_at).getTime() - new Date(b.created_at).getTime(),
    );
  }, [persistedMessages, optimisticMessages, activeThreadId]);

  /**
   * Does this conversation have more than one agent in it? (T-N1)
   *
   * Attribution renders only when the answer is yes, so a single-agent thread —
   * which is every thread until T-N2 ships participants — looks exactly as it
   * did before this ticket. The signal is the transcript's own `agent_id`s
   * rather than a participant count, deliberately: the participants table does
   * not exist yet, and a flag wired to `false` awaiting a later ticket is a
   * branch nobody can see working. Two distinct authors in the log is the thing
   * that actually makes a name necessary, and it will keep being true when the
   * room arrives.
   */
  /**
   * Does this conversation name its speakers? (T-N1, narrowed by T-N4)
   *
   * Membership is the signal now that there is one — a room names its agents
   * from the first message, before two of them have answered. `T-N1` shipped
   * this derived from the transcript's own distinct `agent_id`s, because
   * participants did not exist yet; that fallback is kept for a thread whose
   * membership has not loaded, and for the window after an agent is removed
   * while its earlier answers are still on screen.
   */
  const showAuthors = useMemo(() => {
    if (isRoom) return true;
    const ids = new Set(
      displayedMessages
        .filter((m) => m.role !== "user" && m.agent_id)
        .map((m) => m.agent_id),
    );
    return ids.size > 1;
  }, [isRoom, displayedMessages]);

  /**
   * How far every live turn has got, as one string.
   *
   * The scroll effect used to depend on the single live turn's three growing
   * fields. A room has several streaming at once and the timeline should follow
   * whichever produced the newest line, so the dependency is their combined
   * progress — extracted to a variable rather than computed in the dependency
   * array, which eslint cannot check statically and a reader cannot either.
   */
  const liveProgress = useMemo(
    () =>
      liveList
        .map((t) => `${t.content.length}:${t.thinkingSteps.length}:${t.toolCalls?.length ?? 0}`)
        .join("|"),
    [liveList],
  );

  useEffect(() => {
    timelineRef.current?.scrollTo({
      top: timelineRef.current.scrollHeight,
      behavior: "smooth",
    });
  }, [displayedMessages.length, liveProgress]);

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
            {/* Since T-U8 the agent picker and the starter questions are slots
                inside the bar rather than two blocks stacked above it. Both are
                still new-chat-only: the picker because a thread's agent is
                fixed once it exists, the questions because they are what to ask
                before the conversation has started. */}
            <ChatComposer
              value={input}
              onChange={setInput}
              onSend={send}
              disabled={sending}
              focusSignal={focusSignal}
              context={
                <AgentPicker
                  agents={agents.selectable}
                  value={pickedAgentId ?? agents.fallback?.id ?? null}
                  onChange={setPickedAgentId}
                />
              }
              suggestions={
                /* What this agent was made to be asked (T-B3). Only an agent
                   created from a template has any, and clicking one fills the
                   composer rather than sending: the first turn a customer runs
                   is the one they should have read first. */
                <StarterQuestions
                  questions={agents.starterQuestionsFor(newChatAgent)}
                  onPick={fillComposer}
                />
              }
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
              {displayedMessages.length === 0 && !anyLive && (
                <div className="text-center text-muted-foreground py-12 text-sm">
                  No messages yet — start the conversation below.
                </div>
              )}
              {displayedMessages.map((m, i) => (
                <MessageBubble
                  key={m.id}
                  message={m}
                  // Whose lean the next-step card attributes. The same name the
                  // header shows, resolved once here rather than looked up per
                  // bubble.
                  agentName={agentNameFor(
                    threads.find((t) => t.id === activeThreadId),
                    agents,
                  )}
                  showAuthor={showAuthors}
                  authorColor={colorForAgent(colorIndex.get(m.agent_id ?? "") ?? -1)}
                  // A room's chips prefill with the agent they came from, so a
                  // click continues with the same voice rather than silently
                  // falling to the default speaker (T-N3's routing rule, made
                  // visible).
                  mentionPrefix={
                    showAuthors && m.agent_name ? `@${m.agent_name} ` : undefined
                  }
                  // The newest assistant message only, and not while a turn is
                  // in flight: options beside a streaming answer invite a click
                  // that would queue a second question behind the first.
                  onPickNextStep={
                    i === displayedMessages.length - 1 && !anyLive
                      ? sendNextStep
                      : undefined
                  }
                  sending={sending}
                />
              ))}
              {/* One bubble per agent still working (T-N4). Ordered by when
                  each turn started, so a room reads in the order the agents
                  began rather than reshuffling as deltas arrive. */}
              {liveList
                .slice()
                .sort((a, b) => a.startedAt - b.startedAt)
                .map((turn) => (
                  <PendingBubble
                    key={`live-${turn.jobId}-${turn.agentId}`}
                    content={turn.content}
                    thinkingSteps={turn.thinkingSteps}
                    toolCalls={turn.toolCalls}
                    iteration={turn.iteration}
                    startedAt={turn.startedAt}
                    authorName={isRoom ? turn.agentName || "Agent" : undefined}
                    authorColor={
                      isRoom ? colorForAgent(colorIndex.get(turn.agentId) ?? -1) : undefined
                    }
                  />
                ))}
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
          <div className="mx-3 sm:mx-4 shrink-0">
            <PendingApprovals threadId={activeThreadId} />
          </div>
          <ChatComposer
            value={input}
            onChange={setInput}
            onSend={send}
            disabled={sending}
            focusSignal={focusSignal}
            // Participants only (T-N3 refuses an @ naming an agent that is not
            // in the room, so offering one would be a menu of refusals).
            mentionable={room.participants}
            colorIndex={colorIndex}
            context={
              /* Who is in this conversation (T-N4). The AgentPicker above
                 belongs to the *other* composer — it sets which agent a new
                 conversation opens on and is gone by the time this one renders,
                 which is deliberate and argued in agent-picker.tsx. */
              <ParticipantBar
                participants={room.participants}
                roster={agents.addable}
                colorIndex={colorIndex}
                defaultSpeakerID={
                  threads.find((t) => t.id === activeThreadId)?.agent_id ?? ""
                }
                onAdd={(id) => room.add.mutate(id)}
                onRemove={(id) => room.remove.mutate(id)}
                busy={room.add.isPending || room.remove.isPending}
              />
            }
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
    <div className="flex flex-wrap gap-1.5">
      {questions.map((q) => (
        <button
          key={q}
          type="button"
          onClick={() => onPick(q)}
          className="rounded-full border border-border bg-card px-2.5 py-1 text-[11px] text-muted-foreground shadow-hairline transition-colors hover:border-primary/50 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          {q}
        </button>
      ))}
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
/** Exported for the screenshot harness alone, which draws a room's transcript
 *  from it; nothing else in the app renders a bubble outside this page. */
export function MessageBubble({
  message,
  agentName,
  authorColor,
  mentionPrefix,
  /** Name the agent that wrote this bubble (T-N1).
   *
   *  False on a single-agent thread, which is every thread until T-N2, so the
   *  common case renders exactly as it did before attribution existed. The list
   *  decides — a bubble cannot see the rest of the conversation, and "is there
   *  more than one voice here" is a property of the conversation. */
  showAuthor,
  /** A turn is already going out; the next-step options stop taking taps. */
  sending,
  /** Suggestions render under the NEWEST assistant message only (T-U13).
   *
   *  Options on every historical bubble turn a transcript into a wall of
   *  buttons, and a suggestion about a question from twenty minutes ago is not
   *  a next step. The list decides which bubble is last; the bubble does not
   *  guess. */
  onPickNextStep,
}: {
  message: Message;
  agentName?: string;
  /** The agent's colour from the shared ramp. Redundant with the name by
   *  design — see agent-colors.ts: colour is never the attribution. */
  authorColor?: string;
  /** Prepended to a next-step chip's prompt so the follow-up reaches the same
   *  agent that suggested it. */
  mentionPrefix?: string;
  showAuthor?: boolean;
  sending?: boolean;
  onPickNextStep?: (prompt: string) => void;
}) {
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
  // A room's own line is nobody's message (T-N6). After the hooks above, which
  // must run on every render whatever this bubble turns out to be.
  const roomKind = isUser ? undefined : roomLineKind(message);
  if (roomKind) return <RoomLine message={message} kind={roomKind} />;
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
            {/* The author, when there is more than one (T-N1).
                `agent_name` comes back on the message so a renamed agent
                renames its past turns; `agentName` is the thread's agent and
                the fallback for a row written before the column existed.
                Text, not colour — a reader who cannot distinguish two hues
                must lose nothing, which is the rule T-R3's palette gate set. */}
            {showAuthor && (
              <div className="mb-1 flex items-center gap-1.5">
                <span
                  aria-hidden
                  className="size-2 shrink-0 rounded-full"
                  style={{ backgroundColor: authorColor }}
                />
                <span className="text-[11px] font-semibold text-muted-foreground">
                  {message.agent_name || agentName || "Assistant"}
                </span>
              </div>
            )}
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
        {/* Only the assistant's turns can be rated — a user rating their own
            question is meaningless, and the API refuses it (T-Q2). Copy sits
            beside it in one row (T-U5) rather than in a second strip: they are
            the same gesture, "do something with this answer". */}
        {!isUser && message.id && (
          <div className="mt-1.5">
            <MessageFeedback
              messageId={message.id}
              leading={<CopyAnswer content={message.content} />}
            />
          </div>
        )}
        {/* What to ask next (T-U13). Below the feedback row and on the same
            1.5 gap: "rate this answer" is about the answer above, and the chips
            are about the turn after it, so they read in that order. */}
        {!isUser && onPickNextStep && (
          <NextStepChips
            message={message}
            agentName={message.agent_name || agentName}
            sending={sending}
            onPick={(prompt) => onPickNextStep((mentionPrefix ?? "") + prompt)}
          />
        )}
      </div>
    </div>
  );
}

/* ── A room's own line (T-N6) ───────────────────────────────────────── */
/** The room lines that are the product talking about the conversation rather
 *  than an agent answering in it. An agent's question to a colleague is not one
 *  of these — it is the asking agent's message and renders as its bubble. */
const ROOM_LINES = ["unasked", "settle", "withdrawn"] as const;
type RoomLineKind = (typeof ROOM_LINES)[number];

function roomLineKind(message: Message): RoomLineKind | undefined {
  const kind = message.metadata?.["room_event"];
  return ROOM_LINES.find((k) => k === kind);
}

/**
 * A quiet centred line instead of a bubble: no avatar, no rating, no next steps,
 * because nobody answered anything.
 *
 * A settle and a limit end a chain in opposite ways — a colleague with nothing
 * to add, against a question the budget refused — so they must not read the
 * same (roadmap 09, decision 9). The difference is carried in text, not colour,
 * the rule T-R3's palette gate set: the limit and the withdrawn question name
 * what happened before the sentence does.
 */
function RoomLine({ message, kind }: { message: Message; kind: RoomLineKind }) {
  const lead = kind === "unasked" ? "Limit reached" : kind === "withdrawn" ? "Not asked" : undefined;
  return (
    <div role="note" className="flex justify-center px-6">
      <p className="max-w-[78%] text-center text-xs leading-relaxed text-muted-foreground">
        {lead && <span className="font-semibold">{lead} · </span>}
        {message.content}
      </p>
    </div>
  );
}

/* ── Pending (streaming) Bubble ─────────────────────────────────────── */
/**
 * The turn in flight (T-U3, T-U4, T-U5).
 *
 * Three signals, in the order a waiting reader wants them: that work is
 * happening at all (the shimmer), how long it has been happening (the elapsed
 * figure), and what it is doing (the trace, the tools, the text). The elapsed
 * figure is the one that earns its place — a spinner looks identical whether
 * the agent is on its fourth tool-calling round or the socket died.
 */
function PendingBubble({
  content,
  thinkingSteps,
  toolCalls,
  iteration,
  startedAt,
  authorName,
  authorColor,
}: {
  content: string;
  thinkingSteps: string[];
  toolCalls?: Array<{ name: string; payload: unknown }>;
  iteration?: { current: number; max: number };
  startedAt: number;
  /** Who is producing this turn (T-N4). Undefined outside a room, which is
   *  every single-agent thread, so the common case renders unchanged. */
  authorName?: string;
  authorColor?: string;
}) {
  const elapsed = useElapsedSeconds(startedAt);
  /* The step number without its ceiling, deliberately. "Step 3 of 8" reads as
     a progress bar, and it is not one: 8 is a tuning constant (the tool-calling
     cap in internal/agentbudget), most turns finish in three or four, and a
     turn that ends at 5 has not stopped 62% of the way through anything. What
     the reader needs is that work is still happening and how long it has taken,
     which is this number and the elapsed figure beside it. The ceiling stays on
     the `iteration` event for the logs, where it answers a real question. */
  const progress =
    iteration && iteration.current > 1 ? `Step ${iteration.current}` : "Working";

  return (
    <div className="flex gap-3 items-start">
      <div className="mt-0.5 h-7 w-7 rounded-full flex items-center justify-center shrink-0 bg-muted text-muted-foreground border border-border">
        <Loader2 className="h-3.5 w-3.5 animate-spin" />
      </div>
      <div className="flex-1 min-w-0 space-y-2">
        {/* The status line, and the only element that speaks to a screen
            reader. `Elapsed` is aria-hidden precisely because it changes ten
            times a second; this sentence is what gets announced. */}
        <div
          role="status"
          aria-live="polite"
          className="flex items-center gap-2 text-xs"
        >
          {authorName && (
            <>
              <span
                aria-hidden
                className="size-2 shrink-0 rounded-full"
                style={{ backgroundColor: authorColor }}
              />
              {/* In the status line, not above it: this element is the one a
                  screen reader announces, and "Finance is working, 4s" is the
                  whole sentence a reader of a room needs. */}
              <span className="font-semibold text-foreground">{authorName}</span>
            </>
          )}
          <span className="font-medium text-muted-foreground">{progress}</span>
          <Elapsed startedAt={startedAt} />
        </div>

        {thinkingSteps.length > 0 && (
          <ThinkingTrace steps={thinkingSteps} elapsed={elapsed} live />
        )}

        {content ? (
          <div className="text-sm leading-relaxed text-foreground">
            <MarkdownRenderer content={content} />
            <StreamingCaret />
          </div>
        ) : (
          // Nothing to read yet. Two shimmer rules stand in for the answer, at
          // the width text actually arrives in — a full-width block would
          // promise more than most first lines deliver.
          <div className="space-y-1.5 pt-0.5">
            <Shimmer className="h-3 w-[62%]" />
            <Shimmer className="h-3 w-[38%]" />
          </div>
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
  );
}

/**
 * Copy the answer as the markdown the agent wrote (T-U5).
 *
 * The source text, not the rendered DOM: someone copying an answer is usually
 * moving it into a document or a ticket, and a table pasted as run-together
 * cells is worse than useless. Confirmation is the label changing for a beat —
 * a toast for something this small is a notification nobody asked for.
 */
function CopyAnswer({ content }: { content: string }) {
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(content);
      setCopied(true);
      setTimeout(() => setCopied(false), 1600);
    } catch {
      // Clipboard access can be refused outright — an insecure origin, or a
      // permission the user declined. Staying silent is right here: the button
      // is a convenience, and the text is still selectable.
    }
  };

  return (
    <button
      type="button"
      onClick={() => void copy()}
      aria-label={copied ? "Answer copied" : "Copy answer"}
      className="rounded p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
    >
      {copied ? (
        <Check className="h-3.5 w-3.5 text-positive-ink" />
      ) : (
        <Copy className="h-3.5 w-3.5" />
      )}
    </button>
  );
}

/**
 * The block that trails streamed text.
 *
 * Rendered inline after the markdown rather than inside it: the renderer emits
 * block elements, and a caret appended to the markup would sit on its own line
 * under the paragraph it is supposed to be trailing.
 */
function StreamingCaret() {
  return (
    <span
      aria-hidden
      className="ml-0.5 inline-block h-3.5 w-[2px] translate-y-[2px] animate-pulse rounded-full bg-primary align-baseline"
    />
  );
}

/* ── Unified Chat Composer (used in both new-chat and thread view) ───── */
/**
 * The prompt bar (T-U8).
 *
 * Everything that describes the turn about to be sent now lives inside the bar
 * rather than stacked above it: which agent will answer, what it can be asked,
 * and how to send. Those were three separate blocks competing for the same
 * vertical space on the new-chat screen, and the composer — the only one the
 * customer came to use — was the one at the bottom.
 *
 * `context` and `suggestions` are slots because both are new-chat-only. Once a
 * thread exists its agent is fixed and its starter questions have been answered
 * or ignored, so on that screen the bar is the input and nothing else.
 */
function ChatComposer({
  value,
  onChange,
  onSend,
  disabled,
  context,
  suggestions,
  mentionable,
  colorIndex,
  focusSignal = 0,
  className,
}: {
  value: string;
  onChange: (v: string) => void;
  onSend: () => void;
  disabled: boolean;
  context?: React.ReactNode;
  suggestions?: React.ReactNode;
  /** Who `@` may address (T-N4): the room's participants, and nobody else.
   *  Empty outside a room, which turns the whole menu off. */
  mentionable?: ThreadParticipant[];
  colorIndex?: Map<string, number>;
  /** Bumped when something else filled the composer; see `fillComposer`. */
  focusSignal?: number;
  className?: string;
}) {
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const { data: models } = useModels();

  /** The `@…` the caret is inside, or null. Recomputed on every keystroke and
   *  on every caret move, because the menu has to close when the caret leaves
   *  a mention as well as when the mention stops matching. */
  const [mention, setMention] = useState<MentionQuery | null>(null);
  const participants = mentionable ?? [];

  const syncMention = (el: HTMLTextAreaElement) => {
    if (participants.length < 2) {
      setMention(null);
      return;
    }
    setMention(mentionQueryAt(el.value, el.selectionStart ?? el.value.length));
  };

  const pickMention = (name: string) => {
    const el = textareaRef.current;
    if (!el || !mention) return;
    const next = applyMention(el.value, mention, name);
    onChange(next);
    setMention(null);
    // The caret goes after the inserted name and its trailing space, so the
    // next character typed is the question rather than another mention.
    const caret = mention.at + name.length + 2;
    requestAnimationFrame(() => {
      el.focus();
      el.setSelectionRange(caret, caret);
    });
  };

  const resize = (el: HTMLTextAreaElement) => {
    el.style.height = "auto";
    el.style.height = `${el.scrollHeight}px`;
  };

  const handleChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    onChange(e.target.value);
    syncMention(e.target);
    if (textareaRef.current) resize(textareaRef.current);
  };

  /**
   * Somebody else filled the box: put the cursor in it, at the end.
   *
   * The end rather than the start because every filler is a *beginning* of a
   * sentence the reader finishes — T-D23 prefills `Change [dashboard](…):\n`
   * precisely so the next character typed is the complaint.
   *
   * The resize is here for the same reason the focus is: `handleChange` grows
   * the box as somebody types, and a prefill is never typed, so a two-line
   * prefill used to arrive in a one-line box.
   */
  useEffect(() => {
    if (!focusSignal) return;
    const el = textareaRef.current;
    if (!el) return;
    el.focus();
    el.setSelectionRange(el.value.length, el.value.length);
    resize(el);
  }, [focusSignal]);

  // Unchanged from before T-U8, deliberately. Enter/Shift+Enter is muscle
  // memory and every rewrite of this pair reintroduces the same newline bug.
  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    // Escape closes the mention menu and nothing else — it must not reach the
    // Enter branch below, and it must not be the only way out of the menu
    // either, which is why clicking elsewhere clears it through syncMention.
    if (mention && e.key === "Escape") {
      e.preventDefault();
      setMention(null);
      return;
    }
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
        className="w-full max-w-3xl mx-auto space-y-2"
      >
        {suggestions}
        <div className="w-full rounded-xl border border-border bg-card p-2.5 shadow-card transition-all focus-within:border-primary/50 focus-within:ring-2 focus-within:ring-primary/30">
          {context && (
            <div className="mb-1.5 flex flex-wrap items-center gap-1.5 px-1">
              {context}
            </div>
          )}
          <div className="relative flex min-h-10 items-center px-1">
            {mention && participants.length > 1 && (
              <MentionMenu
                participants={participants}
                query={mention}
                colorIndex={colorIndex ?? new Map()}
                onPick={pickMention}
              />
            )}
            <div className="max-h-48 flex-1 overflow-auto">
              <Textarea
                ref={textareaRef}
                value={value}
                onChange={handleChange}
                onKeyDown={handleKeyDown}
                onKeyUp={(e) => syncMention(e.currentTarget)}
                onClick={(e) => syncMention(e.currentTarget)}
                onBlur={() => setMention(null)}
                placeholder="Ask about your business…"
                className="min-h-0 resize-none rounded-none border-0 bg-transparent p-0 text-[13px] placeholder:text-muted-subtle focus-visible:ring-0 focus-visible:ring-offset-0"
                rows={1}
              />
            </div>
          </div>
          <div className="flex items-center justify-between gap-2 px-1 pt-1">
            <p className="min-w-0 truncate text-[11px] text-muted-subtle">
              <kbd className="rounded border border-border bg-secondary px-1 font-sans text-[10px] text-muted-foreground">
                Enter
              </kbd>{" "}
              to send ·{" "}
              <kbd className="rounded border border-border bg-secondary px-1 font-sans text-[10px] text-muted-foreground">
                Shift+Enter
              </kbd>{" "}
              for a newline
              {models?.primary.model && (
                <span className="ml-1.5">· {models.primary.model}</span>
              )}
            </p>
            <Button
              type="submit"
              size="icon"
              className="h-8 w-8 shrink-0 rounded-lg bg-primary text-primary-foreground shadow-sm hover:bg-primary/90"
              disabled={disabled || !value.trim()}
              aria-label="Send"
            >
              <Send className="size-3.5" />
            </Button>
          </div>
        </div>
      </form>
    </div>
  );
}
