import { useEffect, useRef, useState } from "react";
import { AnimatePresence, motion, useInView, useReducedMotion } from "framer-motion";
import { Activity, Check, Clock, RotateCcw, TrendingUp } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { SectionHeading } from "@/components/section-heading";
import { Reveal } from "@/components/reveal";
import { cn } from "@/lib/utils";

const KINDS: { icon: LucideIcon; title: string; body: string }[] = [
  {
    icon: Activity,
    title: "A threshold",
    body: "Stock under 50 units. Daily revenue under Rp 200 juta. Any number, any query.",
  },
  {
    icon: TrendingUp,
    title: "A sudden change",
    body: "Refunds up 20% against the usual. Argentum compares, so you don't have to.",
  },
  {
    icon: Clock,
    title: "Data going quiet",
    body: "The POS stopped syncing at 14:00? You hear about it before the report looks wrong.",
  },
];

// A refund-rate series that breaks out on the last day.
const POINTS = [38, 41, 36, 44, 40, 43, 39, 42, 45, 41, 44, 78];
const W = 420;
const H = 200;
const LIMIT = 60; // threshold, in the same units as POINTS
// Frame the interesting band (2.5%–9%) instead of 0–100%, so the line uses the height.
const Y_MIN = 25;
const Y_MAX = 90;
const y = (v: number) => H - ((v - Y_MIN) / (Y_MAX - Y_MIN)) * H;
const x = (i: number) => (i / (POINTS.length - 1)) * W;
const PATH = POINTS.map((v, i) => `${i === 0 ? "M" : "L"}${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(" ");

export function Watchers() {
  return (
    <section id="watchers" className="relative overflow-hidden py-28 sm:py-36">
      <div aria-hidden className="absolute inset-x-0 top-0 hairline" />
      <div className="container-page grid gap-14 lg:grid-cols-[1fr_1.1fr] lg:gap-16 [&>*]:min-w-0">
        <div>
          <SectionHeading
            title={
              <>
                It tells you <em className="signal-text italic">first.</em>
              </>
            }
            description="Point a watcher at any metric. Argentum checks it on a schedule, and when it breaks it doesn't just ping — it explains what moved and why, then proposes what to do. Nothing is sent until a person approves it."
          />
          <div className="mt-12 grid gap-px overflow-hidden rounded-2xl border border-white/[0.07] bg-white/[0.07]">
            {KINDS.map((k, i) => (
              <Reveal key={k.title} delay={0.06 * i}>
                <div className="flex gap-4 bg-[var(--color-ink)] p-5">
                  <k.icon className="mt-0.5 h-5 w-5 shrink-0 text-[var(--color-signal)]" />
                  <div>
                    <p className="font-semibold text-white">{k.title}</p>
                    <p className="mt-1 text-[14.5px] leading-relaxed text-white/55">{k.body}</p>
                  </div>
                </div>
              </Reveal>
            ))}
          </div>
          <Reveal delay={0.2}>
            <p className="mt-6 text-[14px] text-white/45">
              Delivered to WhatsApp, Lark, Discord, the dashboard or a signed webhook.
            </p>
          </Reveal>
        </div>

        <Reveal delay={0.1}>
          <WatcherScene />
        </Reveal>
      </div>
    </section>
  );
}

/**
 * The scene plays once when it scrolls into view:
 * 0 idle → 1 line draws → 2 breach pulses → 3 alert lands → 4 action proposed → 5 approved
 */
function WatcherScene() {
  const ref = useRef<HTMLDivElement>(null);
  const inView = useInView(ref, { once: true, margin: "-120px" });
  const reduce = useReducedMotion();
  const [step, setStep] = useState(0);
  const [run, setRun] = useState(0);

  useEffect(() => {
    if (!inView) return;
    if (reduce) {
      setStep(4);
      return;
    }
    setStep(1);
    const t = [
      setTimeout(() => setStep(2), 1700),
      setTimeout(() => setStep(3), 2400),
      setTimeout(() => setStep(4), 3500),
    ];
    return () => t.forEach(clearTimeout);
  }, [inView, run, reduce]);

  const bx = x(POINTS.length - 1);
  const by = y(POINTS[POINTS.length - 1]);

  return (
    <div ref={ref} className="glass-strong relative overflow-hidden rounded-3xl p-5 sm:p-7">
      <div className="flex items-center justify-between font-mono text-[10.5px] uppercase tracking-[0.16em] text-white/45">
        <span>Watcher · refund rate, daily</span>
        <span className={cn("flex items-center gap-2 transition-colors", step >= 2 ? "text-[var(--color-signal)]" : "text-emerald-300/70")}>
          <span className={cn("h-1.5 w-1.5 rounded-full", step >= 2 ? "bg-[var(--color-signal)]" : "bg-emerald-400")} />
          {step >= 2 ? "Breached" : "Watching"}
        </span>
      </div>

      {/* chart */}
      <div className="relative mt-5">
        <svg viewBox={`-8 -12 ${W + 16} ${H + 24}`} className="w-full overflow-visible">
          <line x1="0" x2={W} y1={y(LIMIT)} y2={y(LIMIT)} stroke="rgba(242,92,92,0.55)" strokeDasharray="5 6" />
          <text x={0} y={y(LIMIT) - 8} textAnchor="start" className="fill-[rgba(242,92,92,0.8)] font-mono text-[10px]">
            alert above 6.0%
          </text>
          <motion.path
            key={run}
            d={PATH}
            fill="none"
            stroke="url(#wline)"
            strokeWidth="2.5"
            strokeLinejoin="round"
            strokeLinecap="round"
            initial={{ pathLength: reduce ? 1 : 0 }}
            animate={{ pathLength: step >= 1 ? 1 : 0 }}
            transition={{ duration: 1.6, ease: "easeInOut" }}
          />
          <defs>
            <linearGradient id="wline" x1="0" x2="1" y1="0" y2="0">
              <stop offset="0%" stopColor="rgba(201,204,209,0.6)" />
              <stop offset="80%" stopColor="rgba(201,204,209,0.9)" />
              <stop offset="100%" stopColor="#F25C5C" />
            </linearGradient>
          </defs>
          {step >= 2 && (
            <>
              <motion.circle
                cx={bx}
                cy={by}
                r="6"
                fill="#F25C5C"
                initial={{ scale: 0 }}
                animate={{ scale: 1 }}
              />
              <motion.circle
                cx={bx}
                cy={by}
                r="6"
                fill="none"
                stroke="#F25C5C"
                initial={{ r: 6, opacity: 0.9 }}
                animate={{ r: 26, opacity: 0 }}
                transition={{ duration: 1.4, repeat: Infinity }}
              />
            </>
          )}
        </svg>
      </div>

      {/* the message that lands */}
      <div className="relative mt-6 min-h-[230px] rounded-2xl bg-[#0f1a14] p-4 ring-1 ring-white/[0.06]">
        <div className="flex items-center gap-2 border-b border-white/[0.06] pb-3">
          <span className="grid h-7 w-7 place-items-center rounded-full bg-[linear-gradient(135deg,#F25C5C,#FB7185)] text-[11px] font-bold text-[#212427]">A</span>
          <div className="leading-tight">
            <p className="text-[13px] font-semibold text-white">Argentum</p>
            <p className="text-[10.5px] text-emerald-300/70">WhatsApp · Ops team</p>
          </div>
        </div>

        <AnimatePresence>
          {step >= 3 && (
            <motion.div
              initial={{ opacity: 0, y: 12, scale: 0.98 }}
              animate={{ opacity: 1, y: 0, scale: 1 }}
              transition={{ duration: 0.45, ease: [0.22, 1, 0.36, 1] }}
              className="mt-3 max-w-[92%] rounded-xl rounded-tl-sm bg-[#1f2c24] px-3.5 py-2.5 text-[13.5px] leading-relaxed text-white/90"
            >
              <b className="text-white">Refunds hit 7.8% today</b> — the 14-day norm is 4.1%.
              <br />
              Most of it is one store: <b className="text-white">Depok, 41 refunds on Ember Mug</b>, all from batch 0921.
              <span className="mt-1 block text-right font-mono text-[10px] text-white/35">09:02</span>
            </motion.div>
          )}
        </AnimatePresence>

        <AnimatePresence>
          {step >= 4 && (
            <motion.div
              initial={{ opacity: 0, y: 12 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.45, ease: [0.22, 1, 0.36, 1] }}
              className="mt-2 max-w-[92%] rounded-xl bg-[#1f2c24] px-3.5 py-2.5 text-[13.5px] text-white/90"
            >
              Proposed: message the Depok store manager to pull batch 0921 from the shelf.
              <div className="mt-2.5 flex gap-2">
                {step === 5 ? (
                  <span className="flex items-center gap-1.5 rounded-lg bg-emerald-400/15 px-3 py-1.5 text-[12.5px] font-medium text-emerald-300">
                    <Check className="h-3.5 w-3.5" /> Approved · sent to Depok
                  </span>
                ) : (
                  <>
                    <button
                      type="button"
                      onClick={() => setStep(5)}
                      className="rounded-lg bg-white px-3 py-1.5 text-[12.5px] font-semibold text-[#16181a] transition hover:bg-white/85"
                    >
                      Approve
                    </button>
                    <button type="button" className="rounded-lg px-3 py-1.5 text-[12.5px] text-white/55 hover:text-white/80">
                      Not now
                    </button>
                  </>
                )}
              </div>
            </motion.div>
          )}
        </AnimatePresence>

        {step >= 4 && (
          <button
            type="button"
            onClick={() => {
              setStep(0);
              setRun((r) => r + 1);
            }}
            className="absolute bottom-3 right-3 flex items-center gap-1.5 font-mono text-[10px] uppercase tracking-[0.14em] text-white/35 hover:text-white/70"
          >
            <RotateCcw className="h-3 w-3" /> replay
          </button>
        )}
      </div>
    </div>
  );
}
