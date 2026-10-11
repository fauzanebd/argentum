import { useEffect, useRef, useState } from "react";
import { AnimatePresence, motion, useInView } from "framer-motion";
import { SectionHeading } from "@/components/section-heading";
import { Reveal } from "@/components/reveal";
import { cn } from "@/lib/utils";

/* T-18 still applies: this list is what ships, not what is planned.
 * Slack is wired but its live test is owed, so it is labelled beta.
 * Telegram, Teams and inbound email are backlog — not listed. */
type ChannelId = "web" | "whatsapp" | "discord" | "lark" | "widget";

const CHANNELS: { id: ChannelId; label: string; note: string }[] = [
  { id: "web", label: "Web dashboard", note: "Streaming chat, charts, history" },
  { id: "whatsapp", label: "WhatsApp", note: "Meta Cloud API or Twilio" },
  { id: "discord", label: "Discord", note: "Mention it in any channel" },
  { id: "lark", label: "Lark / Feishu", note: "Group chats and DMs" },
  { id: "widget", label: "Your website", note: "1.6 KB embed, sandboxed" },
];

const Q_EN = "Which items are about to run out?";
const Q_ID = "Stok apa yang hampir habis?";
const A_EN = "6 SKUs are under their safety level. Most urgent: Ember Mug — 12 left in Depok, and Lumen Lamp — 18 left in Bintaro.";
const A_ID = "Ada 6 SKU di bawah batas aman. Paling kritis: Ember Mug — sisa 12 di Depok, dan Lumen Lamp — sisa 18 di Bintaro.";

export function Channels() {
  const [active, setActive] = useState<ChannelId>("whatsapp");
  const [auto, setAuto] = useState(true);
  const ref = useRef<HTMLDivElement>(null);
  const inView = useInView(ref, { margin: "-100px" });

  useEffect(() => {
    if (!auto || !inView) return;
    const t = setInterval(() => {
      setActive((cur) => {
        const i = CHANNELS.findIndex((c) => c.id === cur);
        return CHANNELS[(i + 1) % CHANNELS.length].id;
      });
    }, 3600);
    return () => clearInterval(t);
  }, [auto, inView]);

  return (
    <section id="channels" className="relative py-28 sm:py-36">
      <div aria-hidden className="absolute inset-x-0 top-0 hairline" />
      <div className="container-page">
        <SectionHeading
          align="center"
          title={
            <>
              Lives where your team <em className="silver-text italic">already talks.</em>
            </>
          }
          description="One agent, one memory of your data, every surface. Ask on WhatsApp in the warehouse and pick it up on the dashboard at your desk."
        />

        <div ref={ref} className="mt-14 grid gap-8 lg:grid-cols-[0.8fr_1.2fr] lg:items-center [&>*]:min-w-0">
          <Reveal>
            <ul className="flex flex-col gap-1.5" role="tablist" aria-label="Channels">
              {CHANNELS.map((c) => {
                const on = c.id === active;
                return (
                  <li key={c.id}>
                    <button
                      type="button"
                      role="tab"
                      aria-selected={on}
                      onClick={() => {
                        setActive(c.id);
                        setAuto(false);
                      }}
                      className={cn(
                        "relative flex w-full items-center justify-between overflow-hidden rounded-xl border px-4 py-3.5 text-left transition",
                        on
                          ? "border-white/15 bg-white/[0.05]"
                          : "border-transparent hover:bg-white/[0.03]",
                      )}
                    >
                      <span>
                        <span className={cn("block font-semibold transition", on ? "text-white" : "text-white/55")}>
                          {c.label}
                        </span>
                        <span className="mt-0.5 block text-[13px] text-white/40">
                          {c.note}
                        </span>
                      </span>
                      <span className={cn("h-1.5 w-1.5 rounded-full transition", on ? "bg-[var(--color-signal)]" : "bg-white/15")} />
                      {on && auto && (
                        <motion.span
                          key={`${c.id}-bar`}
                          className="absolute bottom-0 left-0 h-px bg-[var(--color-signal)]"
                          initial={{ width: "0%" }}
                          animate={{ width: "100%" }}
                          transition={{ duration: 3.6, ease: "linear" }}
                        />
                      )}
                    </button>
                  </li>
                );
              })}
            </ul>
            <p className="mt-5 px-4 text-[13.5px] leading-relaxed text-white/45">
              Also: Slack <span className="rounded bg-white/[0.07] px-1.5 py-0.5 text-[12px] text-white/60">beta</span>, email
              alerts, signed webhooks, and the{" "}
              <a href="#developers" className="underline decoration-white/25 underline-offset-2 hover:text-white/70">/v1 API and MCP</a>.
            </p>
          </Reveal>

          <Reveal delay={0.1}>
            <div className="relative flex h-[440px] items-center justify-center overflow-hidden rounded-3xl border border-white/[0.07] bg-[radial-gradient(ellipse_at_top,rgba(242,92,92,0.10),transparent_60%)]">
              <div aria-hidden className="ledger-bg absolute inset-0 opacity-50" />
              <AnimatePresence mode="wait">
                <motion.div
                  key={active}
                  initial={{ opacity: 0, y: 18, scale: 0.97 }}
                  animate={{ opacity: 1, y: 0, scale: 1 }}
                  exit={{ opacity: 0, y: -12, scale: 0.98 }}
                  transition={{ duration: 0.45, ease: [0.22, 1, 0.36, 1] }}
                  className="relative w-full px-5 sm:px-10"
                >
                  {active === "web" && <WebView />}
                  {active === "whatsapp" && <WhatsAppView />}
                  {active === "discord" && <DiscordView />}
                  {active === "lark" && <LarkView />}
                  {active === "widget" && <WidgetView />}
                </motion.div>
              </AnimatePresence>
            </div>
          </Reveal>
        </div>
      </div>
    </section>
  );
}

/* ——— each surface, styled like itself ——— */

function WebView() {
  return (
    <div className="mx-auto max-w-lg rounded-2xl border border-white/10 bg-[#1b1d20] p-4 shadow-2xl">
      <div className="flex gap-1.5 pb-3">
        {[0, 1, 2].map((i) => (
          <span key={i} className="h-2.5 w-2.5 rounded-full bg-white/15" />
        ))}
      </div>
      <div className="ml-auto w-fit rounded-2xl bg-[linear-gradient(120deg,rgba(242,92,92,0.7),rgba(251,113,133,0.65))] px-3.5 py-2 text-[13.5px] text-white">
        {Q_EN}
      </div>
      <div className="mt-3 rounded-2xl bg-white/[0.05] px-3.5 py-3 text-[13.5px] leading-relaxed text-white/85 ring-1 ring-white/[0.06]">
        {A_EN}
        <table className="mt-3 w-full font-mono text-[11px]">
          <tbody>
            {[
              ["Ember Mug", "Depok", "12"],
              ["Lumen Lamp", "Bintaro", "18"],
              ["Pulse Watch", "Kemang", "21"],
            ].map((r) => (
              <tr key={r[0]} className="border-t border-white/[0.06] text-white/70">
                <td className="py-1.5">{r[0]}</td>
                <td>{r[1]}</td>
                <td className="text-right text-[var(--color-signal)]">{r[2]}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function WhatsAppView() {
  return (
    <div className="mx-auto max-w-[320px] rounded-[2rem] border-[6px] border-[#2a2d31] bg-[#0b141a] shadow-2xl">
      <div className="flex items-center gap-2 rounded-t-[1.6rem] bg-[#1f2c34] px-4 py-3">
        <span className="grid h-7 w-7 place-items-center rounded-full bg-[linear-gradient(135deg,#F25C5C,#FB7185)] text-[11px] font-bold text-[#212427]">A</span>
        <div className="leading-tight">
          <p className="text-[13px] font-semibold text-white">Argentum</p>
          <p className="text-[10.5px] text-white/45">online</p>
        </div>
      </div>
      <div className="space-y-2 px-3 py-4">
        <div className="ml-auto w-fit max-w-[80%] rounded-lg rounded-tr-sm bg-[#005c4b] px-3 py-2 text-[13px] text-white">
          {Q_ID}
          <span className="ml-2 font-mono text-[9.5px] text-white/50">07:41 ✓✓</span>
        </div>
        <div className="w-fit max-w-[88%] rounded-lg rounded-tl-sm bg-[#1f2c34] px-3 py-2 text-[13px] leading-relaxed text-white/90">
          {A_ID}
          <span className="block text-right font-mono text-[9.5px] text-white/40">07:41</span>
        </div>
      </div>
    </div>
  );
}

function DiscordView() {
  return (
    <div className="mx-auto max-w-lg rounded-xl bg-[#313338] p-4 shadow-2xl">
      <p className="mb-3 font-mono text-[11px] text-white/40"># ops-daily</p>
      <div className="flex gap-3">
        <span className="h-9 w-9 shrink-0 rounded-full bg-[#5865f2]" />
        <div>
          <p className="text-[13.5px]">
            <span className="font-semibold text-white">dimas</span>
            <span className="ml-2 text-[11px] text-white/35">Today at 07:41</span>
          </p>
          <p className="text-[13.5px] text-white/85">
            <span className="rounded bg-[#5865f2]/30 px-1 text-[#c9cdfb]">@Argentum</span> {Q_EN.toLowerCase()}
          </p>
        </div>
      </div>
      <div className="mt-4 flex gap-3">
        <span className="grid h-9 w-9 shrink-0 place-items-center rounded-full bg-[linear-gradient(135deg,#F25C5C,#FB7185)] text-[13px] font-bold text-[#212427]">A</span>
        <div>
          <p className="text-[13.5px]">
            <span className="font-semibold text-white">Argentum</span>
            <span className="ml-1.5 rounded bg-[#5865f2] px-1 text-[9.5px] font-semibold text-white">APP</span>
          </p>
          <p className="text-[13.5px] leading-relaxed text-white/85">{A_EN}</p>
        </div>
      </div>
    </div>
  );
}

function LarkView() {
  return (
    <div className="mx-auto max-w-lg rounded-xl bg-[#f5f6f7] p-4 text-[#1f2329] shadow-2xl">
      <p className="mb-3 text-[12px] font-semibold text-[#646a73]">Tim Gudang · 8 members</p>
      <div className="ml-auto w-fit max-w-[80%] rounded-lg bg-[#cce0ff] px-3 py-2 text-[13px]">{Q_ID}</div>
      <div className="mt-3 rounded-lg border border-[#dee0e3] bg-white p-3 shadow-sm">
        <p className="flex items-center gap-2 text-[12px] font-semibold text-[#3370ff]">
          <span className="grid h-5 w-5 place-items-center rounded bg-[#F25C5C] text-[10px] font-bold text-[#212427]">A</span>
          Argentum
        </p>
        <p className="mt-1.5 text-[13px] leading-relaxed text-[#1f2329]">{A_ID}</p>
      </div>
    </div>
  );
}

function WidgetView() {
  return (
    <div className="relative mx-auto h-[360px] max-w-lg overflow-hidden rounded-xl border border-white/10 bg-[#eceae4] shadow-2xl">
      <div className="flex items-center gap-2 border-b border-black/10 bg-white px-4 py-2.5">
        <span className="h-2.5 w-2.5 rounded-full bg-black/15" />
        <span className="font-mono text-[10.5px] text-black/40">portal.your-company.co.id</span>
      </div>
      <div className="space-y-2.5 p-5">
        <div className="h-4 w-1/2 rounded bg-black/10" />
        <div className="h-2.5 w-4/5 rounded bg-black/[0.07]" />
        <div className="h-2.5 w-3/5 rounded bg-black/[0.07]" />
        <div className="mt-4 grid grid-cols-3 gap-2">
          {[0, 1, 2].map((i) => (
            <div key={i} className="h-14 rounded bg-black/[0.06]" />
          ))}
        </div>
      </div>
      <div className="absolute bottom-4 right-4 w-64 rounded-xl bg-[#1b1d20] p-3 text-[12.5px] shadow-2xl ring-1 ring-white/10">
        <p className="mb-2 font-mono text-[9.5px] uppercase tracking-[0.14em] text-white/40">Ask our data</p>
        <div className="ml-auto w-fit rounded-lg bg-[#F25C5C]/80 px-2.5 py-1.5 text-white">{Q_EN}</div>
        <div className="mt-2 rounded-lg bg-white/[0.06] px-2.5 py-1.5 leading-relaxed text-white/85">
          6 SKUs are low. Ember Mug is the most urgent — 12 left in Depok.
        </div>
      </div>
    </div>
  );
}
