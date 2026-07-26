import {
  Database,
  FileBarChart,
  MessagesSquare,
  ShieldCheck,
  Workflow,
  Zap,
} from "lucide-react";
import { Card } from "@/components/ui/card";
import { SectionHeading } from "@/components/section-heading";
import type { LucideIcon } from "lucide-react";

const FEATURES: { icon: LucideIcon; title: string; body: string }[] = [
  {
    icon: FileBarChart,
    title: "Sophisticated reports",
    body: "Beyond dashboards. Argentum writes the narrative, surfaces anomalies, and explains the why — in language your CEO will actually read.",
  },
  {
    icon: Database,
    title: "Plug into any database",
    body: "Postgres, MySQL, BigQuery, Snowflake, Mongo. Connect once with read-only creds — schema awareness baked in, no modeling required.",
  },
  {
    icon: MessagesSquare,
    title: "Lives where your team works",
    body: "Web widget, WhatsApp, Telegram, Slack, Discord. One brain across every channel. Ask from your phone, get the answer in your inbox.",
  },
  {
    icon: Zap,
    title: "Answers in seconds",
    body: "From query to chart to commentary in under five seconds — even on tables with hundreds of millions of rows.",
  },
  {
    icon: Workflow,
    title: "Schedules + automations",
    body: "Daily revenue digest? Weekly churn report? Argentum runs them on cron and ships the output to the channel of your choice.",
  },
  {
    icon: ShieldCheck,
    title: "Built for trust",
    body: "Read-only by default, row-level permissions, every query auditable. SOC 2 in flight, GDPR-aligned, your data never leaves your region.",
  },
];

export function Features() {
  return (
    <section id="features" className="py-24 sm:py-32">
      <div className="container-page">
        <SectionHeading
          eyebrow="What it does"
          title="The analyst that never sleeps,"
          highlight="never bills overtime."
          description="Six superpowers in one agent. Drop it into your stack and watch the questions answer themselves."
        />

        <div className="mt-14 grid gap-5 sm:grid-cols-2 lg:grid-cols-3">
          {FEATURES.map((f) => (
            <Card key={f.title}>
              <div className="grid h-11 w-11 place-items-center rounded-xl bg-[linear-gradient(135deg,rgba(242,92,92,0.25),rgba(251,113,133,0.18))] ring-1 ring-white/10">
                <f.icon className="h-5 w-5 text-red-200" />
              </div>
              <h3 className="mt-5 text-lg font-semibold">{f.title}</h3>
              <p className="mt-2 text-sm leading-relaxed text-white/60">
                {f.body}
              </p>
            </Card>
          ))}
        </div>
      </div>
    </section>
  );
}
