export type VizBar = {
  kind: "bar";
  title: string;
  data: { name: string; value: number }[];
};

export type VizLine = {
  kind: "line";
  title: string;
  data: { name: string; value: number }[];
};

export type VizTable = {
  kind: "table";
  title: string;
  columns: string[];
  rows: (string | number)[][];
};

export type Viz = VizBar | VizLine | VizTable;

export type ScriptedReply = {
  reply: string;
  viz?: Viz;
};

export type ScriptedQuestion = {
  id: string;
  prompt: string;
  keywords: string[];
  response: ScriptedReply;
};

export const SUGGESTIONS: ScriptedQuestion[] = [
  {
    id: "top-products",
    prompt: "Top 5 selling products this month",
    keywords: ["top", "product", "selling", "best", "month", "sales"],
    response: {
      reply:
        "April so far — Aurora Headset leads with 1,284 units, up 18% vs March. Lumen Lamp is your dark horse (+47% MoM). Full ranking:",
      viz: {
        kind: "bar",
        title: "Units sold · April 2026",
        data: [
          { name: "Aurora Headset", value: 1284 },
          { name: "Nimbus Speaker", value: 982 },
          { name: "Lumen Lamp", value: 871 },
          { name: "Ember Mug", value: 654 },
          { name: "Pulse Watch", value: 512 },
        ],
      },
    },
  },
  {
    id: "revenue-6m",
    prompt: "Revenue trend last 6 months",
    keywords: ["revenue", "trend", "month", "6", "growth", "sales over"],
    response: {
      reply:
        "Steady climb. $412K → $698K over 6 months, 9.1% avg MoM. Q1 dip in Feb traced to a payment-gateway outage (Feb 12–14).",
      viz: {
        kind: "line",
        title: "Revenue · Nov 2025 – Apr 2026 (USD K)",
        data: [
          { name: "Nov", value: 412 },
          { name: "Dec", value: 489 },
          { name: "Jan", value: 521 },
          { name: "Feb", value: 498 },
          { name: "Mar", value: 612 },
          { name: "Apr", value: 698 },
        ],
      },
    },
  },
  {
    id: "churn-risk",
    prompt: "Customers at churn risk",
    keywords: ["churn", "risk", "customer", "lost", "leaving", "retention"],
    response: {
      reply:
        "3 high-risk accounts flagged — combined ARR $84K. All 3 dropped weekly logins below the 30-day threshold. Want me to draft re-engagement emails?",
      viz: {
        kind: "table",
        title: "High churn risk · last 30 days",
        columns: ["Customer", "Last order", "ARR", "Risk"],
        rows: [
          ["Northwind Co.", "42 days ago", "$38,400", "92%"],
          ["Lumen Studio", "31 days ago", "$26,800", "81%"],
          ["Pioneer Labs", "27 days ago", "$18,900", "74%"],
        ],
      },
    },
  },
];

export const FALLBACK_REPLY: ScriptedReply = {
  reply:
    "I'm running on a sample sales dataset for this demo. Connect your own database and I'll answer questions like that with your real data — try one of the suggestions below to see me in action.",
};

export function matchScript(input: string): ScriptedReply {
  const q = input.toLowerCase();
  let best: { score: number; reply: ScriptedReply } | null = null;
  for (const s of SUGGESTIONS) {
    const score = s.keywords.reduce(
      (acc, kw) => (q.includes(kw) ? acc + 1 : acc),
      0,
    );
    if (score > 0 && (!best || score > best.score)) {
      best = { score, reply: s.response };
    }
  }
  return best?.reply ?? FALLBACK_REPLY;
}
