import { useState } from "react";
import { AnimatePresence, motion } from "framer-motion";
import { FileText, LayoutDashboard, Lock, MessageSquare, Play, Share2 } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { SectionHeading } from "@/components/section-heading";
import { Reveal } from "@/components/reveal";
import { cn } from "@/lib/utils";

type Tab = {
  id: string;
  icon: LucideIcon;
  title: string;
  body: string;
  facts: string[];
};

const TABS: Tab[] = [
  {
    id: "chat",
    icon: MessageSquare,
    title: "An answer, in the conversation",
    body: "Plain language in, a direct answer out — with the chart drawn right in the thread and the SQL one tap away.",
    facts: ["Streams as it thinks", "Charts inline", "SQL shown"],
  },
  {
    id: "dashboard",
    icon: LayoutDashboard,
    title: "A dashboard you can share",
    body: "Pin answers to native dashboards. Share a public link with a password and an expiry, and revoke it whenever you like.",
    facts: ["8 panel types", "Password + expiry", "Revocable links"],
  },
  {
    id: "report",
    icon: FileText,
    title: "A report someone can forward",
    body: "Ask for the board pack and get a branded document with the numbers and the written analysis — not a raw export.",
    facts: ["PDF", "PPTX", "XLSX", "CSV"],
  },
  {
    id: "video",
    icon: Play,
    title: "A report that plays",
    body: "Turn a report into a short video with a link that plays it, or into a carousel ready for social. Runs on the render service.",
    facts: ["MP4 video", "Player link", "Carousel"],
  },
];

export function Outputs() {
  const [active, setActive] = useState(TABS[0].id);
  const tab = TABS.find((t) => t.id === active)!;

  return (
    <section id="outputs" className="relative py-28 sm:py-36">
      <div className="container-page">
        <SectionHeading
          title={
            <>
              One question. <em className="silver-text italic">Every format</em> your team reads.
            </>
          }
          description="The same question can come back as a sentence, a dashboard, a document or a video. Pick the one the next person actually opens."
        />

        <div className="mt-16 grid gap-8 lg:grid-cols-[0.95fr_1.25fr] lg:gap-12 [&>*]:min-w-0">
          <Reveal>
            <ul className="flex flex-col" role="tablist" aria-label="Output formats">
              {TABS.map((t, i) => {
                const on = t.id === active;
                return (
                  <li key={t.id}>
                    <button
                      type="button"
                      role="tab"
                      aria-selected={on}
                      onClick={() => setActive(t.id)}
                      onMouseEnter={() => setActive(t.id)}
                      className={cn(
                        "group relative w-full border-t border-white/[0.07] py-6 pl-12 pr-2 text-left transition",
                        i === TABS.length - 1 && "border-b",
                      )}
                    >
                      <span
                        className={cn(
                          "absolute left-0 top-[27px] text-[13px] font-semibold tabular-nums transition",
                          on ? "text-[var(--color-signal)]" : "text-white/30",
                        )}
                      >
                        0{i + 1}
                      </span>
                      <span
                        className={cn(
                          "block font-display text-[21px] leading-tight transition",
                          on ? "text-white" : "text-white/45 group-hover:text-white/70",
                        )}
                      >
                        {t.title}
                      </span>
                      <AnimatePresence initial={false}>
                        {on && (
                          <motion.div
                            initial={{ height: 0, opacity: 0 }}
                            animate={{ height: "auto", opacity: 1 }}
                            exit={{ height: 0, opacity: 0 }}
                            transition={{ duration: 0.35, ease: [0.22, 1, 0.36, 1] }}
                            className="overflow-hidden"
                          >
                            <p className="mt-3 max-w-md text-[15px] leading-relaxed text-white/60">
                              {t.body}
                            </p>
                            <div className="mt-4 flex flex-wrap gap-2">
                              {t.facts.map((f) => (
                                <span
                                  key={f}
                                  className="rounded-full border border-white/12 px-2.5 py-1 text-[12.5px] text-white/65"
                                >
                                  {f}
                                </span>
                              ))}
                            </div>
                          </motion.div>
                        )}
                      </AnimatePresence>
                      {on && (
                        <motion.span
                          layoutId="outputs-bar"
                          className="absolute left-0 top-0 h-px w-full bg-[linear-gradient(90deg,var(--color-signal),transparent)]"
                        />
                      )}
                    </button>
                  </li>
                );
              })}
            </ul>
          </Reveal>

          <Reveal delay={0.1}>
            <div className="glass-strong relative aspect-[4/3.1] overflow-hidden rounded-3xl p-5 sm:p-7">
              <div aria-hidden className="ledger-bg absolute inset-0 opacity-60" />
              <div className="relative flex items-center justify-between font-mono text-[10.5px] uppercase tracking-[0.16em] text-white/40">
                <span className="flex items-center gap-2">
                  <tab.icon className="h-3.5 w-3.5 text-[var(--color-signal)]" />
                  {tab.id === "chat" && "Thread · web"}
                  {tab.id === "dashboard" && "Dashboard · Sales overview"}
                  {tab.id === "report" && "Report · Weekly trading"}
                  {tab.id === "video" && "Report player"}
                </span>
                <span>“How did October go?”</span>
              </div>
              <div className="relative mt-5 h-[calc(100%-2.5rem)]">
                <AnimatePresence mode="wait">
                  <motion.div
                    key={active}
                    initial={{ opacity: 0, y: 14, filter: "blur(6px)" }}
                    animate={{ opacity: 1, y: 0, filter: "blur(0px)" }}
                    exit={{ opacity: 0, y: -10, filter: "blur(6px)" }}
                    transition={{ duration: 0.4, ease: [0.22, 1, 0.36, 1] }}
                    className="h-full"
                  >
                    {active === "chat" && <ChatPreview />}
                    {active === "dashboard" && <DashboardPreview />}
                    {active === "report" && <ReportPreview />}
                    {active === "video" && <VideoPreview />}
                  </motion.div>
                </AnimatePresence>
              </div>
            </div>
          </Reveal>
        </div>
      </div>
    </section>
  );
}

/* ——— previews: drawn, not screenshotted, so they stay crisp ——— */

const BARS = [62, 78, 54, 91, 70, 84];

function MiniBars({ className }: { className?: string }) {
  return (
    <div className={cn("flex h-full items-end gap-2", className)}>
      {BARS.map((h, i) => (
        <motion.div
          key={i}
          initial={{ height: 0 }}
          animate={{ height: `${h}%` }}
          transition={{ duration: 0.7, delay: 0.05 * i, ease: [0.22, 1, 0.36, 1] }}
          className={cn(
            "flex-1 rounded-t-md",
            i === 3
              ? "bg-[linear-gradient(180deg,#FB7185,#F25C5C)]"
              : "bg-[linear-gradient(180deg,rgba(201,204,209,0.55),rgba(201,204,209,0.15))]",
          )}
        />
      ))}
    </div>
  );
}

function ChatPreview() {
  return (
    <div className="flex h-full flex-col justify-end gap-3">
      <div className="ml-auto max-w-[70%] rounded-2xl bg-[linear-gradient(120deg,rgba(242,92,92,0.7),rgba(251,113,133,0.65))] px-4 py-2.5 text-[14px] text-white">
        How did October go?
      </div>
      <div className="max-w-[88%] rounded-2xl bg-white/[0.05] px-4 py-3 text-[14px] leading-relaxed text-white/85 ring-1 ring-white/[0.07]">
        Best month this year: <b className="text-white">Rp 6,38 M</b>, up 11% on
        September. Week 4 carried it — the payday promo lifted basket size 18%.
        <div className="mt-3 h-28 rounded-xl bg-black/30 p-3">
          <MiniBars />
        </div>
        <div className="mt-3 rounded-lg bg-black/40 px-3 py-2 font-mono text-[11px] text-white/50">
          SELECT date_trunc('week', created_at), SUM(total) FROM orders …
        </div>
      </div>
    </div>
  );
}

function DashboardPreview() {
  return (
    <div className="grid h-full grid-cols-3 grid-rows-[auto_1fr_auto] gap-3">
      {[
        ["Revenue", "Rp 6,38 M", "+11%"],
        ["Orders", "48.210", "+6%"],
        ["Avg basket", "Rp 132 rb", "+4%"],
      ].map(([k, v, d]) => (
        <div key={k} className="rounded-xl border border-white/[0.07] bg-black/25 p-3">
          <p className="font-mono text-[9.5px] uppercase tracking-[0.14em] text-white/40">{k}</p>
          <p className="mt-1.5 text-[17px] font-semibold text-white">{v}</p>
          <p className="font-mono text-[10.5px] text-emerald-300/80">{d}</p>
        </div>
      ))}
      <div className="col-span-2 rounded-xl border border-white/[0.07] bg-black/25 p-3">
        <MiniBars />
      </div>
      <div className="grid place-items-center rounded-xl border border-white/[0.07] bg-black/25 p-3">
        <svg viewBox="0 0 42 42" className="h-24 w-24 -rotate-90">
          <circle cx="21" cy="21" r="15.9" fill="none" stroke="rgba(255,255,255,0.08)" strokeWidth="6" />
          <motion.circle
            cx="21"
            cy="21"
            r="15.9"
            fill="none"
            stroke="#F25C5C"
            strokeWidth="6"
            strokeDasharray="100"
            initial={{ strokeDashoffset: 100 }}
            animate={{ strokeDashoffset: 38 }}
            transition={{ duration: 0.9, ease: [0.22, 1, 0.36, 1] }}
            pathLength={100}
          />
          <circle cx="21" cy="21" r="15.9" fill="none" stroke="rgba(201,204,209,0.5)" strokeWidth="6" strokeDasharray="24 76" strokeDashoffset="-62" pathLength={100} />
        </svg>
      </div>
      <div className="col-span-3 flex flex-wrap items-center gap-2 rounded-xl border border-dashed border-white/15 px-3 py-2.5 font-mono text-[10.5px] text-white/55">
        <Share2 className="h-3.5 w-3.5 text-[var(--color-signal)]" />
        /share/oct-sales
        <span className="ml-auto flex items-center gap-1.5 rounded-full bg-white/[0.06] px-2 py-0.5">
          <Lock className="h-3 w-3" /> password
        </span>
        <span className="rounded-full bg-white/[0.06] px-2 py-0.5">expires 7d</span>
      </div>
    </div>
  );
}

function ReportPreview() {
  return (
    <div className="relative flex h-full items-center justify-center">
      {/* stacked pages */}
      <div className="absolute h-[92%] w-[62%] translate-x-6 rotate-3 rounded-lg bg-white/[0.06]" />
      <div className="absolute h-[92%] w-[62%] translate-x-3 rotate-[1.5deg] rounded-lg bg-white/[0.09]" />
      <div className="relative h-[92%] w-[62%] rounded-lg bg-[#f4f3ef] p-5 text-[#1b1d20] shadow-2xl">
        <div className="flex items-center justify-between">
          <span className="grid h-5 w-5 place-items-center rounded bg-[#F25C5C] text-[10px] font-bold text-[#212427]">A</span>
          <span className="font-mono text-[8px] uppercase tracking-[0.18em] text-black/40">Weekly trading · Oct</span>
        </div>
        <p className="mt-4 font-display text-[16px] leading-[1.15]">October closed at a high.</p>
        <div className="mt-3 space-y-1.5">
          {[100, 92, 96, 70].map((w, i) => (
            <div key={i} className="h-1.5 rounded bg-black/12" style={{ width: `${w}%` }} />
          ))}
        </div>
        <div className="mt-4 h-[30%] rounded bg-black/[0.04] p-2">
          <MiniBars />
        </div>
        <div className="mt-3 space-y-1.5">
          {[88, 95, 60].map((w, i) => (
            <div key={i} className="h-1.5 rounded bg-black/12" style={{ width: `${w}%` }} />
          ))}
        </div>
      </div>
      <div className="absolute bottom-1 right-1 flex gap-1.5">
        {["PDF", "PPTX", "XLSX", "CSV"].map((f, i) => (
          <motion.span
            key={f}
            initial={{ opacity: 0, y: 8 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ delay: 0.25 + i * 0.07 }}
            className="rounded-md border border-white/15 bg-[#1b1d20] px-2 py-1 font-mono text-[10px] text-white/75"
          >
            {f}
          </motion.span>
        ))}
      </div>
    </div>
  );
}

function VideoPreview() {
  return (
    <div className="flex h-full items-center justify-center gap-5">
      <div className="relative h-full max-h-[340px] w-[44%] overflow-hidden rounded-2xl bg-[linear-gradient(160deg,#2a2d31,#16181a)] ring-1 ring-white/10">
        <div className="absolute inset-x-4 top-5">
          <p className="font-mono text-[9px] uppercase tracking-[0.16em] text-white/40">October in 30 seconds</p>
          <p className="mt-2 font-display text-[21px] leading-none text-white">
            Rp 6,38 M <span className="signal-text italic">+11%</span>
          </p>
        </div>
        <div className="absolute inset-x-4 bottom-14 h-[38%]">
          <MiniBars />
        </div>
        <div className="absolute inset-x-4 bottom-5">
          <div className="h-1 rounded-full bg-white/10">
            <motion.div
              className="h-1 rounded-full bg-[var(--color-signal)]"
              initial={{ width: "0%" }}
              animate={{ width: "64%" }}
              transition={{ duration: 2.4, ease: "linear" }}
            />
          </div>
        </div>
        <span className="absolute left-1/2 top-1/2 grid h-12 w-12 -translate-x-1/2 -translate-y-1/2 place-items-center rounded-full bg-white/90 text-[#16181a] shadow-xl">
          <Play className="ml-0.5 h-5 w-5 fill-current" />
        </span>
      </div>
      <div className="hidden flex-col gap-3 sm:flex">
        {["Top 5 products", "Best branch", "What's next"].map((t, i) => (
          <motion.div
            key={t}
            initial={{ opacity: 0, x: 14 }}
            animate={{ opacity: 1, x: 0 }}
            transition={{ delay: 0.15 + i * 0.1 }}
            className="grid h-24 w-24 place-content-center rounded-xl border border-white/10 bg-white/[0.04] p-2 text-center"
          >
            <span className="font-mono text-[9px] text-white/35">{i + 1}/3</span>
            <span className="mt-1 font-display text-[12.5px] leading-tight text-white/85">{t}</span>
          </motion.div>
        ))}
      </div>
    </div>
  );
}
