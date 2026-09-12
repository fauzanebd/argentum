import { useQuery } from "@tanstack/react-query";
import { Calculator } from "lucide-react";
import type { DerivedFiguresResponse } from "@argentum/api-types";
import { api } from "@/lib/api";

/** The window, matching the coverage panel above it. The server clamps both
 *  with one function, so the two halves of this page read the same month. */
const WINDOW_DAYS = 30;

/** Below this many *checked* turns the percentage is not a measurement — the
 *  coverage panel's threshold, for its reason. Checked, not answered: an
 *  answering turn with no grounding record says nothing about what it stated. */
const MIN_TURNS_TO_JUDGE = 20;

export function useDerivedFigures() {
  return useQuery({
    queryKey: ["quality", "derived-figures", WINDOW_DAYS],
    queryFn: async () => {
      const res = await api.get<DerivedFiguresResponse>("/quality/derived-figures", {
        params: { days: WINDOW_DAYS },
      });
      return res.data;
    },
  });
}

/**
 * How often an answer stated a figure no tool returned (T-W3).
 *
 * `compute` makes a derived figure exact and puts its working on the record.
 * This panel says how much arithmetic still happens in the sentence — before
 * `compute` is reached for, and after it was and was not enough. The second of
 * those decides whether a sandbox for model-written programs is worth building,
 * so the panel is careful above everything about which turns it may count.
 */
export function DerivedFiguresPanel() {
  const { data, isLoading, isError } = useDerivedFigures();

  // Nothing while it loads or if it fails, for the coverage panel's reason: a
  // secondary measurement should not take more of the screen than the page.
  if (isLoading || isError || !data) return null;

  return <DerivedFiguresView data={data} />;
}

/** The panel with its data in hand, split out so its empty states are testable
 *  without a QueryClient. */
export function DerivedFiguresView({ data }: { data: DerivedFiguresResponse }) {
  const pct = Math.round(data.unaccounted_percent);

  return (
    <section className="mb-8 rounded-lg border border-border">
      <div className="flex items-start gap-3 border-b border-border px-4 py-3">
        <Calculator className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
        <div className="min-w-0 flex-1">
          <h2 className="text-sm font-medium">Figures worked out in the reply</h2>
          <p className="mt-0.5 text-xs text-muted-foreground">
            Over the last {data.window_days} days. A figure the agent computed with a tool can be
            traced to the numbers it came from; one worked out while writing the answer cannot.
          </p>
        </div>
        <div className="text-right">
          <div className="text-2xl font-semibold tabular-nums">
            {data.checked === 0 ? "—" : `${pct}%`}
          </div>
          <div className="text-[11px] text-muted-foreground">
            of {data.checked} checked {data.checked === 1 ? "turn" : "turns"}
          </div>
        </div>
      </div>

      <div className="grid grid-cols-4 divide-x divide-border border-b border-border text-center">
        <Cell label="Worked out in the reply" value={data.composed} />
        <Cell label="Computed" value={data.computed} />
        <Cell label="Computed, then more worked out" value={data.residue} />
        <Cell label="Across two sources" value={data.cross_source} />
      </div>

      <p className="px-4 py-3 text-xs text-muted-foreground">{footnote(data)}</p>
    </section>
  );
}

/**
 * The sentence under the numbers, and the one place the panel says what it
 * could not see. Every branch is a different claim: nobody asked, nothing could
 * be checked, too little was checked, or here is how much was left out.
 */
function footnote(data: DerivedFiguresResponse): string {
  if (data.answered === 0) {
    return `Nobody has asked this workspace a data question in the last ${data.window_days} days.`;
  }
  if (data.checked === 0) {
    // Said rather than rendered as 0%. Every turn before this measurement
    // shipped is unchecked, and "none of them stated a derived figure" is a
    // claim the product has no evidence for.
    return `None of the ${data.answered} answering ${plural(data.answered, "turn")} could be checked. A turn is checked from the day this measurement shipped, and only when its tools returned numbers to compare the reply against.`;
  }
  if (data.checked < MIN_TURNS_TO_JUDGE) {
    return `Too few checked turns to read much into yet — this becomes meaningful past about ${MIN_TURNS_TO_JUDGE}.`;
  }
  if (data.unchecked > 0) {
    return `${data.unchecked} answering ${plural(data.unchecked, "turn")} could not be checked and ${data.unchecked === 1 ? "is" : "are"} not in the percentage.`;
  }
  return "Every answering turn in this window was checked.";
}

function plural(n: number, word: string): string {
  return n === 1 ? word : `${word}s`;
}

function Cell({ label, value }: { label: string; value: number }) {
  return (
    <div className="px-3 py-2.5">
      <div className="text-lg font-semibold tabular-nums">{value}</div>
      <div className="text-[11px] text-muted-foreground">{label}</div>
    </div>
  );
}
