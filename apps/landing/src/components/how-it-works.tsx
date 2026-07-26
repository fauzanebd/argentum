import { PlugZap, MessageSquareText, Rocket } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { SectionHeading } from "@/components/section-heading";

const STEPS: { icon: LucideIcon; title: string; body: string }[] = [
  {
    icon: PlugZap,
    title: "Connect",
    body: "Point Argentum at your database with read-only creds or OAuth. Schema introspected in seconds.",
  },
  {
    icon: MessageSquareText,
    title: "Ask",
    body: "Type or speak in plain language — from any channel. No SQL, no BI tool, no Jira ticket to the data team.",
  },
  {
    icon: Rocket,
    title: "Act",
    body: "Get charts, narratives, anomalies. Schedule recurring reports. Drill deeper. Share with one tap.",
  },
];

export function HowItWorks() {
  return (
    <section id="how" className="py-24 sm:py-32">
      <div className="container-page">
        <SectionHeading
          eyebrow="How it works"
          title="From query to insight in"
          highlight="one breath."
          description="Three steps. No data engineering. No semantic layer to maintain."
        />

        <div className="relative mt-14">
          <div className="pointer-events-none absolute left-0 right-0 top-[44px] hidden h-px bg-gradient-to-r from-transparent via-red-400/50 to-transparent md:block" />
          <div className="grid gap-6 md:grid-cols-3">
            {STEPS.map((s, i) => (
              <div key={s.title} className="glass relative rounded-2xl p-6">
                <div className="flex items-center gap-3">
                  <div className="grid h-11 w-11 place-items-center rounded-xl bg-[linear-gradient(135deg,#F25C5C,#FB7185)] text-white shadow-[0_8px_30px_-10px_rgba(242,92,92,0.7)]">
                    <s.icon className="h-5 w-5" />
                  </div>
                  <span className="font-mono text-xs text-white/40">
                    {String(i + 1).padStart(2, "0")}
                  </span>
                </div>
                <h3 className="mt-5 text-lg font-semibold">{s.title}</h3>
                <p className="mt-2 text-sm leading-relaxed text-white/60">
                  {s.body}
                </p>
              </div>
            ))}
          </div>
        </div>
      </div>
    </section>
  );
}
