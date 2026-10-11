import { useState } from "react";
import { AnimatePresence, motion } from "framer-motion";
import { ArrowUpRight, Check, Copy } from "lucide-react";
import { Button } from "@/components/ui/button";
import { SectionHeading } from "@/components/section-heading";
import { Reveal } from "@/components/reveal";
import { DEVELOPER_LINK, DOCS_LINK } from "@/lib/contact";
import { cn } from "@/lib/utils";

/* Every snippet mirrors docs/api/quickstart.md, which CI runs against a real
 * tenant. If you change one here, check it still matches the quickstart. */
const TABS = [
  {
    id: "chat",
    label: "Ask · SSE",
    code: `curl -sSN -X POST "$ARGENTUM_BASE_URL/v1/chat" \\
  -H "Authorization: Bearer $ARGENTUM_API_KEY" \\
  -H "Accept: text/event-stream" \\
  -H "Idempotency-Key: $(uuidgen)" \\
  -d '{"message":"What was total revenue in December 2024?",
       "user_ref":"quickstart"}'`,
    out: `event: started
data: {"thread_id":"…","run_id":"…"}

event: tool_call
data: {"tool":"run_sql"}

event: delta
data: {"content":"Total revenue in December 2024 was "}

event: final
data: {"object":"turn","usage":{"cost_usd":0.0016}}`,
  },
  {
    id: "report",
    label: "Report · PDF",
    code: `curl -sS -X POST "$ARGENTUM_BASE_URL/v1/reports" \\
  -H "Authorization: Bearer $ARGENTUM_API_KEY" \\
  -H "Idempotency-Key: $(uuidgen)" \\
  -d '{"prompt":"Total revenue by month for 2024, with a bar chart.",
       "format":"pdf","user_ref":"quickstart"}'`,
    out: `HTTP/1.1 202 Accepted

# then pick one:
GET  /v1/reports/{id}          # poll until completed
GET  /v1/reports/{id}/events   # stream progress
callback_url                   # signed webhook`,
  },
  {
    id: "mcp",
    label: "MCP server",
    code: `# Argentum as tools for your own agents
list_sources      get_schema
run_sql           list_metrics
query_metric      create_dashboard`,
    out: `→ run_sql({ source: "pos", sql: "SELECT …" })
← read-only, guarded, audited — same rules
   as every other turn.`,
  },
] as const;

const CONTRACT = [
  "Idempotency key on every write",
  "Rate-limit headers on every response",
  "Scoped keys, fixed at creation",
  "Typed errors with a request id",
  "Additive contract — breaking means /v2",
  "OpenAPI spec, no key needed",
];

export function Developers() {
  const [active, setActive] = useState<(typeof TABS)[number]["id"]>("chat");
  const [copied, setCopied] = useState(false);
  const tab = TABS.find((t) => t.id === active)!;

  return (
    <section id="developers" className="relative py-28 sm:py-36">
      <div aria-hidden className="absolute inset-x-0 top-0 hairline" />
      <div className="container-page grid gap-14 lg:grid-cols-[0.9fr_1.2fr] lg:gap-16 [&>*]:min-w-0">
        <div>
          <SectionHeading
            compact
            title={
              <>
                Ten minutes from an empty directory <em className="silver-text italic">to a PDF.</em>
              </>
            }
            description="Put Argentum inside your own product. A versioned /v1 API for chat and reports, and an MCP server your agents can call — same guardrails, same audit trail."
          />
          <Reveal delay={0.1}>
            <ul className="mt-10 grid gap-x-6 gap-y-3 sm:grid-cols-2">
              {CONTRACT.map((c) => (
                <li key={c} className="flex items-start gap-2.5 text-[14px] text-white/65">
                  <Check className="mt-0.5 h-4 w-4 shrink-0 text-emerald-300/80" />
                  {c}
                </li>
              ))}
            </ul>
            <div className="mt-10 flex flex-wrap gap-3">
              <Button asChild>
                <a href={DOCS_LINK}>
                  Read the quickstart <ArrowUpRight className="h-4 w-4" />
                </a>
              </Button>
              <Button asChild variant="outline">
                <a href={DEVELOPER_LINK} target="_blank" rel="noopener noreferrer">
                  Ask for an API key
                </a>
              </Button>
            </div>
          </Reveal>
        </div>

        <Reveal delay={0.1}>
          <div className="overflow-hidden rounded-3xl border border-white/[0.09] bg-[#111315] shadow-[0_40px_120px_-50px_rgba(0,0,0,0.9)]">
            <div className="flex items-center justify-between border-b border-white/[0.06] px-3 py-2.5">
              <div className="flex gap-1" role="tablist" aria-label="API examples">
                {TABS.map((t) => (
                  <button
                    key={t.id}
                    type="button"
                    role="tab"
                    aria-selected={t.id === active}
                    onClick={() => {
                      setActive(t.id);
                      setCopied(false);
                    }}
                    className={cn(
                      "rounded-lg px-3 py-1.5 font-mono text-[11.5px] transition",
                      t.id === active ? "bg-white/[0.08] text-white" : "text-white/45 hover:text-white/75",
                    )}
                  >
                    {t.label}
                  </button>
                ))}
              </div>
              <button
                type="button"
                aria-label="Copy snippet"
                onClick={() => {
                  navigator.clipboard?.writeText(tab.code).then(() => setCopied(true), () => {});
                }}
                className="grid h-8 w-8 place-items-center rounded-lg text-white/45 hover:bg-white/[0.06] hover:text-white"
              >
                {copied ? <Check className="h-4 w-4 text-emerald-300" /> : <Copy className="h-4 w-4" />}
              </button>
            </div>
            <AnimatePresence mode="wait">
              <motion.div
                key={active}
                initial={{ opacity: 0 }}
                animate={{ opacity: 1 }}
                exit={{ opacity: 0 }}
                transition={{ duration: 0.25 }}
              >
                <pre className="overflow-x-auto px-5 py-5 font-mono text-[12.5px] leading-[1.7] text-[var(--color-silver)]">
                  <code>{tab.code}</code>
                </pre>
                <div className="border-t border-white/[0.06] bg-black/30 px-5 py-4">
                  <p className="mb-2 font-mono text-[10px] uppercase tracking-[0.16em] text-white/30">Response</p>
                  <pre className="overflow-x-auto font-mono text-[12px] leading-[1.65] text-white/55">
                    {tab.out.split("\n").map((line, i) => (
                      <motion.span
                        key={i}
                        className="block"
                        initial={{ opacity: 0 }}
                        animate={{ opacity: 1 }}
                        transition={{ delay: 0.15 + i * 0.06 }}
                      >
                        {line.startsWith("event:") ? (
                          <span className="text-[var(--color-signal)]">{line}</span>
                        ) : (
                          line || " "
                        )}
                      </motion.span>
                    ))}
                  </pre>
                </div>
              </motion.div>
            </AnimatePresence>
          </div>
        </Reveal>
      </div>
    </section>
  );
}
