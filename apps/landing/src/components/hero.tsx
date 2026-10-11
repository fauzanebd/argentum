import { ArrowRight } from "lucide-react";
import { motion, useReducedMotion } from "framer-motion";
import { Button } from "@/components/ui/button";
import { ChatDemo } from "@/components/chat-demo";
import { DEMO_LINK } from "@/lib/contact";

const PROOF = [
  "Read-only by design",
  "PostgreSQL · MySQL · SQL Server",
  "English & Bahasa Indonesia",
  "Every query logged",
];

// Real question shapes the agent handles; mixed EN/ID on purpose.
const QUESTIONS = [
  "Which branch missed target last week?",
  "Penjualan per cabang bulan ini?",
  "Why did margin drop on Tuesday?",
  "Produk apa yang stoknya hampir habis?",
  "Top 10 customers by revenue, this quarter",
  "Bandingkan omzet Oktober vs September",
  "Turn this into a PDF for the board",
  "Kirim ringkasan harian ke WhatsApp jam 7 pagi",
  "Alert me if refunds jump more than 20%",
  "Which SKUs haven't sold in 30 days?",
];

export function Hero() {
  const reduce = useReducedMotion();
  const rise = (i: number) =>
    reduce
      ? {}
      : {
          initial: { opacity: 0, y: 22 },
          animate: { opacity: 1, y: 0 },
          transition: { duration: 0.8, delay: 0.08 * i, ease: [0.22, 1, 0.36, 1] as const },
        };

  return (
    <section className="relative overflow-hidden pt-28 sm:pt-36">
      {/* atmosphere: ledger rules + one coral glow, no blobs */}
      <div aria-hidden className="ledger-bg absolute inset-0 -z-10 [mask-image:linear-gradient(to_bottom,black_0%,black_55%,transparent_100%)]" />
      <div aria-hidden className="absolute -top-40 left-[8%] -z-10 h-[520px] w-[520px] rounded-full bg-[radial-gradient(circle,rgba(242,92,92,0.22),transparent_65%)] blur-2xl" />

      <div className="container-page">
        <motion.p
          {...rise(0)}
          className="flex items-center gap-2.5 text-[14.5px] font-medium text-white/60"
        >
          <span className="relative flex h-2 w-2">
            <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-[var(--color-signal)] opacity-70" />
            <span className="relative inline-flex h-2 w-2 rounded-full bg-[var(--color-signal)]" />
          </span>
          The AI analyst for your database, by SmartSoft
        </motion.p>

        <motion.h1
          {...rise(1)}
          className="mt-6 max-w-5xl font-display text-[42px] leading-[1.04] text-white sm:text-[60px] md:text-[76px]"
        >
          Ask your database.
          <br />
          <span className="text-white/55">Get the answer, </span>
          <em className="silver-text not-italic sm:italic">the chart,</em>
          <span className="text-white/55"> and </span>
          <em className="signal-text italic">the why.</em>
        </motion.h1>

        <div className="mt-9 grid gap-10 lg:grid-cols-[1.15fr_1fr] lg:items-end">
          <motion.p {...rise(2)} className="max-w-xl text-[17.5px] leading-relaxed text-white/65">
            Argentum reads your PostgreSQL, MySQL or SQL Server and answers in
            plain English or Bahasa Indonesia, with the chart, the reasoning
            and the SQL it ran. Then it keeps watching, and messages your team
            on WhatsApp, Discord or Lark the moment a number moves.
          </motion.p>

          <motion.div {...rise(3)} className="flex flex-wrap items-center gap-3 lg:justify-end">
            <Button asChild size="lg">
              <a href={DEMO_LINK} target="_blank" rel="noopener noreferrer">
                Book a demo
                <ArrowRight className="h-4 w-4" />
              </a>
            </Button>
            <Button asChild variant="outline" size="lg">
              <a href="#watchers">See it in action</a>
            </Button>
          </motion.div>
        </div>

        <motion.ul
          {...rise(4)}
          className="mt-10 flex flex-wrap gap-x-7 gap-y-2 border-t border-white/[0.07] pt-5 text-[14px] text-white/55"
        >
          {PROOF.map((p) => (
            <li key={p} className="flex items-center gap-2">
              <span className="h-1 w-1 rounded-full bg-[var(--color-silver)]" />
              {p}
            </li>
          ))}
        </motion.ul>

        <motion.div
          initial={reduce ? false : { opacity: 0, y: 40, scale: 0.98 }}
          animate={{ opacity: 1, y: 0, scale: 1 }}
          transition={{ duration: 1, delay: 0.45, ease: [0.22, 1, 0.36, 1] }}
          className="relative mt-16 sm:mt-20"
        >
          <ChatDemo />
          <p className="mt-4 text-center text-[13px] text-white/40">
            Interactive preview on a sample store database. Replies are scripted.
          </p>
        </motion.div>
      </div>

      {/* what people actually ask — a slow ticker */}
      <div className="relative mt-20 overflow-hidden border-y border-white/[0.06] py-5 [mask-image:linear-gradient(90deg,transparent,black_12%,black_88%,transparent)]">
        <div className="animate-marquee flex w-max gap-10 whitespace-nowrap">
          {[...QUESTIONS, ...QUESTIONS].map((q, i) => (
            <span key={i} className="flex items-center gap-10 text-[18px] font-medium italic text-white/45">
              “{q}”
              <span aria-hidden className="h-1.5 w-1.5 rotate-45 bg-[var(--color-signal)]/70" />
            </span>
          ))}
        </div>
      </div>
    </section>
  );
}
