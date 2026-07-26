import { useEffect, useRef, useState } from "react";
import { ArrowUp, Sparkles, Database, HelpCircle } from "lucide-react";
import {
  Bar,
  BarChart,
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import {
  FALLBACK_REPLY,
  matchScript,
  SUGGESTIONS,
  type Viz,
} from "@/data/chat-script";
import { cn } from "@/lib/utils";

type Message = {
  id: string;
  role: "user" | "assistant";
  content: string;
  viz?: Viz;
  streaming?: boolean;
};

const INITIAL: Message[] = [
  {
    id: "greet",
    role: "assistant",
    content:
      "Hi — I'm Argentum. I'm reading your demo_sales_db. Ask me anything, or pick a suggestion below.",
  },
];

export function ChatDemo() {
  const [messages, setMessages] = useState<Message[]>(INITIAL);
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [helpOpen, setHelpOpen] = useState(false);
  const threadRef = useRef<HTMLDivElement>(null);
  const helpRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    threadRef.current?.scrollTo({
      top: threadRef.current.scrollHeight,
      behavior: "smooth",
    });
  }, [messages]);

  useEffect(() => {
    if (!helpOpen) return;
    function onDoc(e: MouseEvent) {
      if (helpRef.current && !helpRef.current.contains(e.target as Node)) {
        setHelpOpen(false);
      }
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") setHelpOpen(false);
    }
    document.addEventListener("mousedown", onDoc);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDoc);
      document.removeEventListener("keydown", onKey);
    };
  }, [helpOpen]);

  async function send(text: string) {
    const trimmed = text.trim();
    if (!trimmed || busy) return;
    setBusy(true);
    setInput("");

    const userMsg: Message = {
      id: `u-${Date.now()}`,
      role: "user",
      content: trimmed,
    };
    setMessages((m) => [...m, userMsg]);

    // typing indicator
    const typingId = `t-${Date.now()}`;
    setMessages((m) => [
      ...m,
      { id: typingId, role: "assistant", content: "", streaming: true },
    ]);

    await wait(650);

    const reply = matchScript(trimmed) ?? FALLBACK_REPLY;
    const full = reply.reply;

    // stream chunks
    let acc = "";
    const chunks = full.match(/.{1,4}/g) ?? [full];
    for (const c of chunks) {
      acc += c;
      setMessages((m) =>
        m.map((x) =>
          x.id === typingId ? { ...x, content: acc, streaming: true } : x,
        ),
      );
      await wait(22);
    }

    setMessages((m) =>
      m.map((x) =>
        x.id === typingId
          ? { ...x, content: full, viz: reply.viz, streaming: false }
          : x,
      ),
    );
    setBusy(false);
  }

  return (
    <div className="glass-strong gradient-border relative mx-auto w-full max-w-3xl rounded-3xl p-1.5 shadow-[0_30px_120px_-30px_rgba(242,92,92,0.45)]">
      <div className="rounded-[calc(theme(borderRadius.3xl)-6px)] bg-[rgba(33,36,39,0.6)] backdrop-blur-xl">
        {/* header */}
        <div className="flex items-center justify-between border-b border-white/5 px-5 py-3">
          <div className="flex items-center gap-2.5">
            <span className="relative flex h-2 w-2">
              <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-60" />
              <span className="relative inline-flex h-2 w-2 rounded-full bg-emerald-400" />
            </span>
            <Database className="h-3.5 w-3.5 text-white/50" />
            <span className="font-mono text-xs text-white/60">
              argentum · connected to{" "}
              <span className="text-white/80">demo_sales_db</span>
            </span>
            <div ref={helpRef} className="relative">
              <button
                type="button"
                aria-label="What's in demo_sales_db?"
                aria-expanded={helpOpen}
                onClick={() => setHelpOpen((v) => !v)}
                className="flex h-4 w-4 items-center justify-center rounded-full text-white/40 transition hover:bg-white/10 hover:text-white/80 focus:outline-none focus:ring-2 focus:ring-red-400/40"
              >
                <HelpCircle className="h-3.5 w-3.5" />
              </button>
              {helpOpen && (
                <div
                  role="dialog"
                  className="absolute left-0 top-6 z-20 w-72 rounded-xl border border-white/10 bg-[rgba(42,45,49,0.95)] p-4 text-xs text-white/70 shadow-2xl backdrop-blur-xl sm:w-80"
                >
                  <div className="mb-2 flex items-center gap-2 text-white/90">
                    <Database className="h-3.5 w-3.5 text-red-300" />
                    <span className="font-mono text-[11px] uppercase tracking-wider">
                      demo_sales_db
                    </span>
                  </div>
                  <p className="mb-3 leading-relaxed">
                    A sample online-store database. It tracks{" "}
                    <span className="text-white/90">products</span>,{" "}
                    <span className="text-white/90">customers</span>, and{" "}
                    <span className="text-white/90">orders</span> over the last
                    6 months.
                  </p>
                  <p className="mb-2 text-white/60">Try asking:</p>
                  <ul className="space-y-1.5 text-white/70">
                    <li>· Which products sell the most?</li>
                    <li>· How is revenue trending?</li>
                    <li>· Which customers might leave soon?</li>
                  </ul>
                </div>
              )}
            </div>
          </div>
          <span className="hidden text-[10px] uppercase tracking-widest text-white/30 sm:inline">
            live demo
          </span>
        </div>

        {/* thread */}
        <div
          ref={threadRef}
          className="scrollbar-thin h-[380px] overflow-y-auto px-5 py-5 sm:h-[420px]"
        >
          <div className="flex flex-col gap-4">
            {messages.map((m) => (
              <MessageBubble key={m.id} message={m} />
            ))}
          </div>
        </div>

        {/* suggestions */}
        <div className="flex flex-wrap gap-2 px-5 pb-3">
          {SUGGESTIONS.map((s) => (
            <button
              key={s.id}
              type="button"
              disabled={busy}
              onClick={() => send(s.prompt)}
              className="group inline-flex items-center gap-1.5 rounded-full border border-white/10 bg-white/[0.03] px-3 py-1.5 text-xs text-white/75 transition hover:border-red-400/40 hover:bg-red-400/10 hover:text-white disabled:opacity-50"
            >
              <Sparkles className="h-3 w-3 text-red-300 transition group-hover:text-red-200" />
              {s.prompt}
            </button>
          ))}
        </div>

        {/* input */}
        <form
          className="flex items-center gap-2 border-t border-white/5 p-3"
          onSubmit={(e) => {
            e.preventDefault();
            send(input);
          }}
        >
          <input
            value={input}
            onChange={(e) => setInput(e.target.value)}
            placeholder="Ask Argentum about your data…"
            disabled={busy}
            className="flex-1 rounded-full bg-white/[0.04] px-5 py-2.5 text-sm text-white placeholder:text-white/35 focus:outline-none focus:ring-2 focus:ring-red-400/40 disabled:opacity-60"
          />
          <button
            type="submit"
            disabled={busy || !input.trim()}
            aria-label="Send"
            className="grid h-10 w-10 place-items-center rounded-full bg-[linear-gradient(120deg,#F25C5C,#FB7185)] text-white shadow-[0_8px_24px_-8px_rgba(242,92,92,0.7)] transition hover:brightness-110 disabled:opacity-40"
          >
            <ArrowUp className="h-4 w-4" />
          </button>
        </form>
      </div>
    </div>
  );
}

function MessageBubble({ message }: { message: Message }) {
  const isUser = message.role === "user";
  return (
    <div
      className={cn(
        "flex w-full",
        isUser ? "justify-end" : "justify-start",
      )}
    >
      <div
        className={cn(
          "max-w-[85%] rounded-2xl px-4 py-3 text-sm leading-relaxed",
          isUser
            ? "bg-[linear-gradient(120deg,rgba(242,92,92,0.7),rgba(251,113,133,0.65))] text-white"
            : "bg-white/[0.04] text-white/90 ring-1 ring-white/[0.06]",
        )}
      >
        {message.streaming && message.content === "" ? (
          <TypingDots />
        ) : (
          <>
            <p className="whitespace-pre-wrap">{message.content}</p>
            {message.viz && (
              <div className="mt-3">
                <VizRenderer viz={message.viz} />
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}

function TypingDots() {
  return (
    <span className="inline-flex items-center gap-1 py-1">
      {[0, 1, 2].map((i) => (
        <span
          key={i}
          className="inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-white/60"
          style={{ animationDelay: `${i * 120}ms` }}
        />
      ))}
    </span>
  );
}

function VizRenderer({ viz }: { viz: Viz }) {
  return (
    <div className="overflow-hidden rounded-xl border border-white/[0.06] bg-black/30 p-3">
      <p className="mb-2 font-mono text-[10px] uppercase tracking-widest text-white/40">
        {viz.title}
      </p>
      {viz.kind === "bar" && <BarViz data={viz.data} />}
      {viz.kind === "line" && <LineViz data={viz.data} />}
      {viz.kind === "table" && (
        <TableViz columns={viz.columns} rows={viz.rows} />
      )}
    </div>
  );
}

function BarViz({ data }: { data: { name: string; value: number }[] }) {
  return (
    <div className="h-44 w-full">
      <ResponsiveContainer width="100%" height="100%">
        <BarChart data={data} margin={{ top: 4, right: 8, left: -12, bottom: 0 }}>
          <defs>
            <linearGradient id="barGrad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="#FCA5A5" stopOpacity={1} />
              <stop offset="100%" stopColor="#FB7185" stopOpacity={0.7} />
            </linearGradient>
          </defs>
          <CartesianGrid strokeDasharray="3 3" stroke="rgba(255,255,255,0.06)" />
          <XAxis
            dataKey="name"
            tick={{ fill: "rgba(255,255,255,0.6)", fontSize: 10 }}
            stroke="rgba(255,255,255,0.1)"
            interval={0}
          />
          <YAxis
            tick={{ fill: "rgba(255,255,255,0.5)", fontSize: 10 }}
            stroke="rgba(255,255,255,0.1)"
          />
          <Tooltip
            contentStyle={{
              background: "#2A2D31",
              border: "1px solid rgba(255,255,255,0.1)",
              borderRadius: 8,
              fontSize: 12,
            }}
            cursor={{ fill: "rgba(242,92,92,0.08)" }}
          />
          <Bar dataKey="value" fill="url(#barGrad)" radius={[6, 6, 0, 0]} />
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}

function LineViz({ data }: { data: { name: string; value: number }[] }) {
  return (
    <div className="h-44 w-full">
      <ResponsiveContainer width="100%" height="100%">
        <LineChart data={data} margin={{ top: 4, right: 8, left: -12, bottom: 0 }}>
          <defs>
            <linearGradient id="lineGrad" x1="0" y1="0" x2="1" y2="0">
              <stop offset="0%" stopColor="#F25C5C" />
              <stop offset="100%" stopColor="#FB7185" />
            </linearGradient>
          </defs>
          <CartesianGrid strokeDasharray="3 3" stroke="rgba(255,255,255,0.06)" />
          <XAxis
            dataKey="name"
            tick={{ fill: "rgba(255,255,255,0.6)", fontSize: 10 }}
            stroke="rgba(255,255,255,0.1)"
          />
          <YAxis
            tick={{ fill: "rgba(255,255,255,0.5)", fontSize: 10 }}
            stroke="rgba(255,255,255,0.1)"
          />
          <Tooltip
            contentStyle={{
              background: "#2A2D31",
              border: "1px solid rgba(255,255,255,0.1)",
              borderRadius: 8,
              fontSize: 12,
            }}
          />
          <Line
            type="monotone"
            dataKey="value"
            stroke="url(#lineGrad)"
            strokeWidth={2.5}
            dot={{ fill: "#FCA5A5", r: 3 }}
            activeDot={{ r: 5 }}
          />
        </LineChart>
      </ResponsiveContainer>
    </div>
  );
}

function TableViz({
  columns,
  rows,
}: {
  columns: string[];
  rows: (string | number)[][];
}) {
  return (
    <div className="overflow-hidden rounded-lg">
      <table className="w-full text-xs">
        <thead>
          <tr className="border-b border-white/10 text-white/50">
            {columns.map((c) => (
              <th key={c} className="px-3 py-2 text-left font-medium">
                {c}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, i) => (
            <tr key={i} className="border-b border-white/[0.04] last:border-0">
              {row.map((cell, j) => (
                <td
                  key={j}
                  className={cn(
                    "px-3 py-2 text-white/80",
                    j === row.length - 1 && "font-mono text-red-300",
                  )}
                >
                  {cell}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function wait(ms: number) {
  return new Promise((r) => setTimeout(r, ms));
}
