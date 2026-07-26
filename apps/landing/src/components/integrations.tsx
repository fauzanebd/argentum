import {
  Globe,
  MessageCircle,
  Send,
  Slack,
  Mail,
  Hash,
  Database,
  Layers,
  CloudCog,
  Snowflake,
  Leaf,
  CircleDot,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { SectionHeading } from "@/components/section-heading";

const CHANNELS: { icon: LucideIcon; label: string }[] = [
  { icon: Globe, label: "Web widget" },
  { icon: MessageCircle, label: "WhatsApp" },
  { icon: Send, label: "Telegram" },
  { icon: Slack, label: "Slack" },
  { icon: Hash, label: "Discord" },
  { icon: Mail, label: "Email" },
];

const DATABASES: { icon: LucideIcon; label: string }[] = [
  { icon: Database, label: "PostgreSQL" },
  { icon: Layers, label: "MySQL" },
  { icon: CloudCog, label: "BigQuery" },
  { icon: Snowflake, label: "Snowflake" },
  { icon: Leaf, label: "MongoDB" },
  { icon: CircleDot, label: "Redshift" },
];

export function Integrations() {
  return (
    <section id="integrations" className="py-24 sm:py-32">
      <div className="container-page">
        <SectionHeading
          eyebrow="Integrations"
          title="Connect everything"
          highlight="in minutes."
          description="Pipe Argentum into the channels your team lives in, and the warehouses your data lives in. No glue code."
        />

        <div className="mt-14 grid gap-6 lg:grid-cols-2">
          <LogoBlock title="Channels" items={CHANNELS} accent="red" />
          <LogoBlock title="Databases & warehouses" items={DATABASES} accent="rose" />
        </div>
      </div>
    </section>
  );
}

function LogoBlock({
  title,
  items,
  accent,
}: {
  title: string;
  items: { icon: LucideIcon; label: string }[];
  accent: "red" | "rose";
}) {
  const ringColor =
    accent === "red"
      ? "ring-red-400/20 hover:ring-red-400/40"
      : "ring-rose-400/20 hover:ring-rose-400/40";
  const iconColor =
    accent === "red" ? "text-red-200" : "text-rose-200";

  return (
    <div className="glass rounded-2xl p-6">
      <p className="font-mono text-[10px] uppercase tracking-[0.2em] text-white/40">
        {title}
      </p>
      <div className="mt-5 grid grid-cols-2 gap-3 sm:grid-cols-3">
        {items.map((it) => (
          <div
            key={it.label}
            className={`flex items-center gap-2.5 rounded-xl bg-white/[0.02] px-3 py-2.5 ring-1 transition ${ringColor}`}
          >
            <it.icon className={`h-4 w-4 ${iconColor}`} />
            <span className="text-sm text-white/85">{it.label}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
