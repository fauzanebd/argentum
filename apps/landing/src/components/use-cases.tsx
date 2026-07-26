import { TrendingUp, Cog, Wallet, FlaskConical } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { SectionHeading } from "@/components/section-heading";

const CASES: {
  icon: LucideIcon;
  team: string;
  title: string;
  body: string;
  example: string;
}[] = [
  {
    icon: TrendingUp,
    team: "Sales",
    title: "Pipeline health on demand",
    body: "Forecasts, win-rate breakdowns, deal velocity — straight in Slack the moment your VP asks.",
    example: "“Which reps are below quota with 2 weeks left?”",
  },
  {
    icon: Cog,
    team: "Operations",
    title: "Spot bottlenecks before they bite",
    body: "Anomaly alerts on inventory, fulfillment, SLA breaches. Argentum tells you what changed and where.",
    example: "“Why did delivery times spike in Jakarta yesterday?”",
  },
  {
    icon: Wallet,
    team: "Finance",
    title: "Close the books with answers",
    body: "Spreadsheets are the past. Ask, reconcile, drill. Audit-ready exports, every time.",
    example: "“Variance vs budget by cost center this quarter.”",
  },
  {
    icon: FlaskConical,
    team: "Product",
    title: "Feature usage, retention, the truth",
    body: "Cohort analysis without a data scientist. Argentum runs the funnel and writes the takeaway.",
    example: "“Did the onboarding redesign move D7 retention?”",
  },
];

export function UseCases() {
  return (
    <section className="py-24 sm:py-32">
      <div className="container-page">
        <SectionHeading
          eyebrow="Use cases"
          title="Every team gets a"
          highlight="data co-pilot."
          description="Argentum speaks the language of every function. Pick the question, skip the dashboard."
        />

        <div className="mt-14 grid gap-5 sm:grid-cols-2">
          {CASES.map((c) => (
            <div key={c.team} className="glass group rounded-2xl p-6">
              <div className="flex items-center gap-3">
                <div className="grid h-10 w-10 place-items-center rounded-lg bg-white/[0.04] ring-1 ring-white/10">
                  <c.icon className="h-4 w-4 text-rose-200" />
                </div>
                <span className="font-mono text-[11px] uppercase tracking-[0.18em] text-white/45">
                  {c.team}
                </span>
              </div>
              <h3 className="mt-5 text-lg font-semibold">{c.title}</h3>
              <p className="mt-2 text-sm leading-relaxed text-white/60">
                {c.body}
              </p>
              <p className="mt-4 rounded-lg border border-white/[0.06] bg-black/30 px-3 py-2 font-mono text-xs text-red-200/90">
                {c.example}
              </p>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
