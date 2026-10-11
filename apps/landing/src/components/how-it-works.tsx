import { SectionHeading } from "@/components/section-heading";
import { Reveal } from "@/components/reveal";

const STEPS = [
  {
    n: "01",
    title: "Connect",
    body: "Point Argentum at PostgreSQL, MySQL or SQL Server with read-only credentials. It tests the connection, then learns the schema on its own.",
    aside: "No modelling, no semantic layer to maintain.",
  },
  {
    n: "02",
    title: "Ask",
    body: "In English or Bahasa Indonesia, from the dashboard, WhatsApp, Discord, Lark or your own site. No SQL, no ticket to the data team.",
    aside: "Charts, tables and the SQL come with the answer.",
  },
  {
    n: "03",
    title: "Keep watching",
    body: "Pin it to a dashboard, schedule it, or set a watcher — and let Argentum tell you when something moves, with a proposed next step.",
    aside: "Nothing goes out until someone approves it.",
  },
];

export function HowItWorks() {
  return (
    <section id="how" className="relative py-28 sm:py-36">
      <div aria-hidden className="absolute inset-x-0 top-0 hairline" />
      <div className="container-page">
        <SectionHeading
          title={
            <>
              Three steps. <em className="silver-text italic">One afternoon.</em>
            </>
          }
        />

        <ol className="mt-16 grid gap-px overflow-hidden rounded-3xl border border-white/[0.07] bg-white/[0.07] md:grid-cols-3">
          {STEPS.map((s, i) => (
            <Reveal key={s.n} delay={0.08 * i} className="bg-[var(--color-ink)]">
              <li className="flex h-full flex-col p-7 sm:p-8">
                <span className="font-display text-[56px] leading-none text-white/[0.12]">{s.n}</span>
                <h3 className="mt-4 font-display text-[26px] leading-none text-white">{s.title}</h3>
                <p className="mt-4 text-[15px] leading-relaxed text-white/60">{s.body}</p>
                <p className="mt-auto pt-6 text-[13.5px] font-medium text-[var(--color-signal)]/85">
                  {s.aside}
                </p>
              </li>
            </Reveal>
          ))}
        </ol>
      </div>
    </section>
  );
}
