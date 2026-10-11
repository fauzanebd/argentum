import { motion } from "framer-motion";
import { BookOpenText, CalendarClock, Hash, Languages, Plug, Users } from "lucide-react";
import { SectionHeading } from "@/components/section-heading";
import { Reveal } from "@/components/reveal";
import { cn } from "@/lib/utils";

/** A bento of the things that make Argentum more than text-to-SQL. Shipped only. */
export function Capabilities() {
  return (
    <section className="relative py-28 sm:py-36">
      <div aria-hidden className="absolute inset-x-0 top-0 hairline" />
      <div className="container-page">
        <SectionHeading
          title={
            <>
              More than text-to-SQL. <em className="silver-text italic">A colleague.</em>
            </>
          }
          description="It learns how your business talks, reads the documents beside your database, and keeps to a schedule."
        />

        <div className="mt-14 grid gap-4 md:grid-cols-6">
          {/* bilingual — wide */}
          <Tile className="md:col-span-4" delay={0}>
            <Head icon={Languages} title="English and Bahasa Indonesia, natively" />
            <p className="mt-2 max-w-md text-[14.5px] text-white/55">
              Ask in either language and get answers that speak in Juta and Miliar, not
              millions and billions.
            </p>
            <div className="mt-6 grid gap-3 sm:grid-cols-2">
              <Bubble lang="EN" q="Revenue this week vs last?" a="Rp 1,42 M, up 8% on last week." />
              <Bubble lang="ID" q="Omzet minggu ini vs minggu lalu?" a="Rp 1,42 M, naik 8% dari minggu lalu." />
            </div>
          </Tile>

          {/* schedule */}
          <Tile className="md:col-span-2" delay={0.06}>
            <Head icon={CalendarClock} title="Keeps a schedule" />
            <p className="mt-2 text-[14.5px] text-white/55">
              “Every weekday at 7, send the ops team yesterday’s summary.” Cron and
              timezone, set in a sentence.
            </p>
            <div className="mt-5 space-y-1.5 font-mono text-[11px]">
              {["Mon 07:00 WIB", "Tue 07:00 WIB", "Wed 07:00 WIB"].map((d, i) => (
                <motion.div
                  key={d}
                  initial={{ opacity: 0, x: -6 }}
                  whileInView={{ opacity: 1, x: 0 }}
                  viewport={{ once: true }}
                  transition={{ delay: 0.2 + i * 0.1 }}
                  className="flex items-center justify-between rounded-md bg-white/[0.04] px-2.5 py-1.5 text-white/60"
                >
                  {d}
                  <span className="text-emerald-300/80">sent</span>
                </motion.div>
              ))}
            </div>
          </Tile>

          {/* pdf knowledge */}
          <Tile className="md:col-span-2" delay={0.08}>
            <Head icon={BookOpenText} title="Reads your PDFs" />
            <p className="mt-2 text-[14.5px] text-white/55">
              Upload the price list or the annual report. Its tables become queryable,
              and answers cite the page.
            </p>
            <div className="mt-5 flex items-center gap-2 rounded-lg border border-white/10 bg-white/[0.03] px-3 py-2 font-mono text-[11px] text-white/60">
              <span className="rounded bg-[var(--color-signal)]/20 px-1.5 text-[var(--color-signal)]">PDF</span>
              price-list-2026.pdf
              <span className="ml-auto text-white/35">p. 14</span>
            </div>
          </Tile>

          {/* agents & skills */}
          <Tile className="md:col-span-2" delay={0.1}>
            <Head icon={Users} title="A roster, not a chatbot" />
            <p className="mt-2 text-[14.5px] text-white/55">
              Finance, Ops, Support — each agent with its own persona, sources and tools.
              Teach them procedures as skills, drafted straight from a conversation.
            </p>
            <div className="mt-5 flex -space-x-2">
              {[
                ["F", "#F25C5C"],
                ["O", "#c9ccd1"],
                ["S", "#FB7185"],
              ].map(([l, c]) => (
                <span
                  key={l}
                  className="grid h-9 w-9 place-items-center rounded-full border-2 border-[var(--color-ink)] text-[12px] font-bold text-[#16181a]"
                  style={{ background: c }}
                >
                  {l}
                </span>
              ))}
            </div>
          </Tile>

          {/* metrics */}
          <Tile className="md:col-span-2" delay={0.12}>
            <Head icon={Hash} title="Define a number once" />
            <p className="mt-2 text-[14.5px] text-white/55">
              “Net revenue” means one thing. Register it, and every answer, dashboard and
              watcher uses the same definition.
            </p>
            <div className="mt-5 rounded-lg bg-black/30 px-3 py-2 font-mono text-[11px] text-white/55">
              <span className="text-[var(--color-silver)]">net_revenue</span> = gross − refunds − discounts
            </div>
          </Tile>

          {/* mcp sources — wide */}
          <Tile className="md:col-span-6" delay={0.14}>
            <div className="grid gap-6 md:grid-cols-[1fr_1.2fr] md:items-center">
              <div>
                <Head icon={Plug} title="Plug in the systems around your database" />
                <p className="mt-2 max-w-md text-[14.5px] text-white/55">
                  Already run an MCP server for your CRM or ticketing? Register it, and the
                  agent can read from it alongside your SQL — in the same answer.
                </p>
              </div>
              <div className="flex flex-wrap items-center gap-3 font-mono text-[11.5px]">
                {["PostgreSQL", "MySQL", "SQL Server", "PDF knowledge", "Your MCP servers"].map((s, i) => (
                  <motion.span
                    key={s}
                    initial={{ opacity: 0, scale: 0.9 }}
                    whileInView={{ opacity: 1, scale: 1 }}
                    viewport={{ once: true }}
                    transition={{ delay: 0.1 + i * 0.07 }}
                    className={cn(
                      "rounded-full border px-3.5 py-2",
                      i === 4
                        ? "border-[var(--color-signal)]/40 bg-[var(--color-signal)]/10 text-white"
                        : "border-white/12 bg-white/[0.03] text-white/70",
                    )}
                  >
                    {s}
                  </motion.span>
                ))}
              </div>
            </div>
          </Tile>
        </div>
      </div>
    </section>
  );
}

function Tile({
  children,
  className,
  delay,
}: {
  children: React.ReactNode;
  className?: string;
  delay: number;
}) {
  return (
    <Reveal delay={delay} className={cn("group relative overflow-hidden rounded-3xl border border-white/[0.07] bg-white/[0.02] p-6 transition hover:border-white/15 hover:bg-white/[0.035] sm:p-7", className)}>
      {children}
    </Reveal>
  );
}

function Head({ icon: Icon, title }: { icon: React.ComponentType<{ className?: string }>; title: string }) {
  return (
    <div className="flex items-center gap-3">
      <Icon className="h-5 w-5 text-[var(--color-signal)]" />
      <h3 className="font-display text-[19px] leading-tight text-white">{title}</h3>
    </div>
  );
}

function Bubble({ lang, q, a }: { lang: string; q: string; a: string }) {
  return (
    <div className="rounded-2xl border border-white/[0.07] bg-black/20 p-3.5 text-[13px]">
      <span className="font-mono text-[10px] tracking-[0.16em] text-white/35">{lang}</span>
      <p className="mt-1.5 text-white/60">{q}</p>
      <p className="mt-1.5 text-white">{a}</p>
    </div>
  );
}
