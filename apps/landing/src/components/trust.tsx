import { useRef } from "react";
import { motion, useInView, useReducedMotion } from "framer-motion";
import { Eye, FileLock2, KeyRound, ScrollText, ShieldCheck, UserCog } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { SectionHeading } from "@/components/section-heading";
import { Reveal } from "@/components/reveal";

/* Only what ships. The survey of 2026-10 removed "SOC 2 in flight",
 * "GDPR-aligned", "your data never leaves your region" and "row-level
 * permissions" — none had code behind them. Add a line here only when it does. */
const GUARANTEES: { icon: LucideIcon; title: string; body: string }[] = [
  {
    icon: Eye,
    title: "Read-only by construction",
    body: "Queries run inside read-only transactions on PostgreSQL and MySQL, and every statement the model writes is checked before it runs — SQL Server included.",
  },
  {
    icon: ShieldCheck,
    title: "Guardrails both ways",
    body: "Prompt-injection, off-topic and PII screening. Whatever a tool returns is treated as data, never as instructions.",
  },
  {
    icon: ScrollText,
    title: "Every tool call on the record",
    body: "Each schema read, query and action lands in an audit log you can open. No answer without a trail.",
  },
  {
    icon: FileLock2,
    title: "Credentials sealed",
    body: "Database connection strings are encrypted at rest with AES-256-GCM and never shown back.",
  },
  {
    icon: KeyRound,
    title: "Your model key, if you want",
    body: "Bring your own LLM key per workspace, and route through zero-data-retention providers.",
  },
  {
    icon: UserCog,
    title: "People approve actions",
    body: "When the agent wants to message someone or call a webhook, it proposes. A human presses the button.",
  },
];

const TRACE: { tool: string; detail: string; meta: string; code?: string; ok?: boolean }[] = [
  { tool: "guard", detail: "injection · topic · PII", meta: "pass", ok: true },
  { tool: "get_schema", detail: "orders, order_items, stores", meta: "38 ms" },
  {
    tool: "run_sql",
    detail: "read-only transaction",
    meta: "412 ms",
    code: "SELECT s.name, SUM(oi.qty * oi.price) AS omzet\nFROM order_items oi JOIN orders o ON o.id = oi.order_id\nJOIN stores s ON s.id = o.store_id\nWHERE o.created_at >= date_trunc('month', now())\nGROUP BY s.name ORDER BY omzet DESC;",
  },
  { tool: "sqlguard", detail: "1 statement · SELECT only", meta: "pass", ok: true },
  { tool: "answer", detail: "written, charted, logged", meta: "agent_actions #18342", ok: true },
];

export function Trust() {
  return (
    <section id="trust" className="relative py-28 sm:py-36">
      <div aria-hidden className="absolute inset-x-0 top-0 hairline" />
      <div className="container-page">
        <div className="grid gap-14 lg:grid-cols-[1.05fr_1fr] lg:gap-16 [&>*]:min-w-0">
          <div>
            <SectionHeading
              compact
              title={
                <>
                  Every answer <em className="silver-text italic">shows its work.</em>
                </>
              }
              description="An analyst you can't audit is a rumour. Argentum keeps the receipts: what it looked at, what it ran, and what it decided — for every single turn."
            />
            <Reveal delay={0.1}>
              <Trace />
            </Reveal>
          </div>

          <div className="grid content-start gap-px overflow-hidden rounded-3xl border border-white/[0.07] bg-white/[0.07] sm:grid-cols-2 lg:mt-24">
            {GUARANTEES.map((g, i) => (
              <Reveal key={g.title} delay={0.05 * i} className="bg-[var(--color-ink)]">
                <div className="h-full p-6">
                  <g.icon className="h-5 w-5 text-[var(--color-silver)]" />
                  <p className="mt-4 font-semibold text-white">{g.title}</p>
                  <p className="mt-2 text-[14px] leading-relaxed text-white/55">{g.body}</p>
                </div>
              </Reveal>
            ))}
          </div>
        </div>
      </div>
    </section>
  );
}

function Trace() {
  const ref = useRef<HTMLDivElement>(null);
  const inView = useInView(ref, { once: true, margin: "-100px" });
  const reduce = useReducedMotion();

  return (
    <div
      ref={ref}
      className="mt-10 overflow-hidden rounded-2xl border border-white/[0.08] bg-[#111315] font-mono text-[12px]"
    >
      <div className="flex items-center justify-between border-b border-white/[0.06] px-4 py-2.5 text-[10.5px] uppercase tracking-[0.16em] text-white/40">
        <span>Turn trace</span>
        <span className="normal-case tracking-normal text-white/50">“Omzet per cabang bulan ini?”</span>
      </div>
      <ol className="space-y-0.5 p-3">
        {TRACE.map((t, i) => (
          <motion.li
            key={t.tool}
            initial={reduce ? false : { opacity: 0, x: -8 }}
            animate={inView ? { opacity: 1, x: 0 } : {}}
            transition={{ duration: 0.4, delay: 0.35 * i }}
            className="rounded-lg px-2 py-1.5 hover:bg-white/[0.03]"
          >
            <div className="flex items-baseline gap-3">
              <span className={t.ok ? "text-emerald-300/80" : "text-[var(--color-signal)]"}>
                {t.ok ? "✓" : "▸"}
              </span>
              <span className="w-[86px] shrink-0 text-white/85">{t.tool}</span>
              <span className="min-w-0 flex-1 truncate text-white/45">{t.detail}</span>
              <span className="shrink-0 text-white/35">{t.meta}</span>
            </div>
            {t.code && (
              <pre className="mt-2 overflow-x-auto whitespace-pre rounded-md bg-black/40 px-3 py-2.5 text-[11px] leading-relaxed text-[var(--color-silver)]/80">
                {t.code}
              </pre>
            )}
          </motion.li>
        ))}
      </ol>
    </div>
  );
}
