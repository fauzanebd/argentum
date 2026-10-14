import { useId, useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { ChevronDown, Loader2, Wrench } from "lucide-react";
import type { ToolCallEvent } from "@argentum/api-types";
import { cn } from "@/lib/utils";
import { DURATION, CURVE } from "@/lib/motion";
import { ToolCallCard, toolLabel } from "./tool-call-card";

/**
 * A turn's tool calls behind one line (RIZ-95).
 *
 * The chips used to stack as a flat column under the answer, one per stream
 * event — and a call and its result are two events, so a turn that ran seven
 * queries put fifteen chips between the reader and the conversation, half of
 * them without a chevron because a `tool_result` carries no `sql`. This folds
 * them into one line in the style of the thinking trace, and opening it shows
 * exactly the chips that were there before: `ToolCallCard` is untouched, so its
 * label, icon and detail panel still decide what a chip says.
 *
 * Nothing opens on its own, live or finished. The status line above already
 * says work is happening; the caption here says how much and on what.
 */

/** One stream event of a live turn, as `chat-page.tsx` keeps it. */
export type ToolEntry = {
  name: string;
  payload: unknown;
  kind: "call" | "result";
};

/** One real tool call: its call and its result, once both have arrived. */
export type ToolStep = {
  name: string;
  payload: unknown;
  /** True until the call's result arrives. Never set on a saved message,
   *  which only keeps finished calls. */
  running?: boolean;
};

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

/**
 * pairToolSteps turns the live event list into one step per real call.
 *
 * **By name and order, because there is nothing else to pair on.**
 * `ToolCallEvent` carries no call id, so a result is matched to the oldest call
 * of the same tool still waiting for one. That is the right call whenever a
 * tool's calls finish in the order they started; two parallel calls of one
 * tool finishing out of order would swap their results, which costs a chip
 * showing the wrong merged keys in the live view only — a saved message is
 * never paired.
 *
 * The payload is the arguments with the result laid over them. Neither alone is
 * the chip: the arguments hold `run_sql`'s statement and `load_skill`'s
 * procedure name, and the result holds `create_dashboard`'s link. Result keys
 * win where both have one, because what the tool says it did is the better
 * record than what it was asked to do.
 *
 * A result with no open call of its name — a socket that joined between the
 * two, a snapshot trimmed past the call — becomes a finished step of its own
 * rather than being dropped: it is still a tool that ran.
 */
export function pairToolSteps(entries: ToolEntry[]): ToolStep[] {
  const steps: ToolStep[] = [];
  for (const entry of entries) {
    if (entry.kind === "call") {
      steps.push({ name: entry.name, payload: entry.payload, running: true });
      continue;
    }
    const open = steps.find((s) => s.running && s.name === entry.name);
    if (!open) {
      steps.push({ name: entry.name, payload: entry.payload, running: false });
      continue;
    }
    // `open` is a step this function created, never a caller's object, so
    // updating it in place mutates nothing the caller holds.
    open.payload =
      isRecord(open.payload) && isRecord(entry.payload)
        ? { ...open.payload, ...entry.payload }
        : isRecord(entry.payload)
          ? entry.payload
          : open.payload;
    open.running = false;
  }
  return steps;
}

/**
 * snapshotToolEntries tags the tool events of a resumed turn's snapshot
 * (T-U12) as calls or results, so a reload pairs them exactly as the live
 * stream did and shows the same count.
 *
 * The snapshot stores each event's `tool_call` body and not its type, so the
 * type is read back off which field is set: `result` means a result,
 * `arguments` a call. **Neither is set on two different events**, because the
 * Go struct marshals both with `omitempty` and an empty map vanishes: a call
 * made with no arguments, and a result that parsed to nothing — every
 * `load_skill` result (a framed string, not JSON) and every tool that failed
 * with a plain error. Reading such an entry as a call would count a finished
 * `load_skill` twice and leave a spinner on it until the turn ends, so it
 * answers the oldest call of its name still open when there is one, and only
 * otherwise starts a step of its own. (An event too large for the snapshot is
 * not one of these: the server keeps it as a stub with `{truncated: true}`
 * under the field that says its kind, precisely so that field survives.)
 */
export function snapshotToolEntries(events: ToolCallEvent[]): ToolEntry[] {
  const open = new Map<string, number>();
  return events.map((tc) => {
    const waiting = open.get(tc.name) ?? 0;
    const kind: ToolEntry["kind"] =
      tc.result !== undefined
        ? "result"
        : tc.arguments !== undefined
          ? "call"
          : waiting > 0
            ? "result"
            : "call";
    open.set(tc.name, kind === "call" ? waiting + 1 : Math.max(0, waiting - 1));
    return {
      name: tc.name,
      // A result was recorded with its result and a call with its arguments;
      // whichever the snapshot kept is the payload for the card.
      payload: tc.result ?? tc.arguments ?? {},
      kind,
    };
  });
}

/**
 * savedToolSteps reads a saved answer's `tool_calls` into steps (RIZ-95).
 *
 * The api now serves a list, one record per call in run order, already paired
 * by the server: each record's payload is its arguments with its result laid
 * over them, exactly as `pairToolSteps` builds a live step, so a chip opens
 * onto the same thing before and after the reload.
 *
 * **It also reads the old by-name object**, which is what an api not yet
 * redeployed still serves. The dashboard ships first (the PRD's deploy order),
 * and an `Object.entries` over the new list would draw chips labelled `0` and
 * `1`, so this has to read both rather than trust the generated type, which
 * describes only the new api. Anything else is no steps.
 */
export function savedToolSteps(toolCalls: unknown): ToolStep[] {
  if (Array.isArray(toolCalls)) {
    return toolCalls.flatMap((rec): ToolStep[] => {
      if (!isRecord(rec) || typeof rec.name !== "string") return [];
      const args = isRecord(rec.arguments) ? rec.arguments : {};
      const result = isRecord(rec.result) ? rec.result : {};
      return [{ name: rec.name, payload: { ...args, ...result } }];
    });
  }
  if (isRecord(toolCalls)) {
    return Object.entries(toolCalls).map(([name, payload]) => ({ name, payload }));
  }
  return [];
}

export function ToolStepsGroup({
  steps,
  live,
  className,
}: {
  steps: ToolStep[];
  /** The turn is still running: the caption counts calls and names the one in
   *  progress. Otherwise it is past tense, counts calls and names every tool
   *  used. */
  live?: boolean;
  className?: string;
}) {
  const [open, setOpen] = useState(false);
  const panelId = useId();

  // Nothing to fold. Same rule as the thinking trace: no empty shell with a
  // chevron that opens onto nothing.
  if (steps.length === 0) return null;

  let summary: string;
  let caption: string;
  if (live) {
    // The newest call still waiting, since that is the one the reader is
    // waiting on; the newest call of all once every result is in.
    const current =
      [...steps].reverse().find((s) => s.running) ?? steps[steps.length - 1];
    summary = "Using tools";
    caption = `${steps.length} call${steps.length === 1 ? "" : "s"} · ${toolLabel(current.name)}`;
  } else {
    // The same count as the live line, and the same unit: the answer keeps
    // every call in run order (RIZ-95), so the number a reader watched rise is
    // the number they find after the reload. The tools are named once each,
    // in the order each first ran — eleven queries are one "SQL query".
    summary = "Used tools";
    const labels = [...new Set(steps.map((s) => toolLabel(s.name)))].join(", ");
    caption = `${steps.length} call${steps.length === 1 ? "" : "s"} · ${labels}`;
  }

  return (
    <div className={cn("text-xs", className)}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        aria-controls={panelId}
        className="group inline-flex max-w-full items-center gap-1.5 rounded-md px-1.5 py-1 -ml-1.5 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        {live ? (
          <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-primary" />
        ) : (
          <Wrench className="h-3.5 w-3.5 shrink-0" />
        )}
        <span className="shrink-0 whitespace-nowrap font-medium">{summary}</span>
        <span className="min-w-0 truncate text-muted-subtle">{caption}</span>
        <ChevronDown
          className={cn(
            "h-3.5 w-3.5 shrink-0 transition-transform",
            open && "rotate-180",
          )}
        />
      </button>

      <AnimatePresence initial={false}>
        {open && (
          <motion.div
            id={panelId}
            // Height rather than `display`, as in the thinking trace: the answer
            // and the turns below must not jump by the whole list in one frame.
            initial={{ height: 0, opacity: 0 }}
            animate={{ height: "auto", opacity: 1 }}
            exit={{ height: 0, opacity: 0 }}
            transition={{ duration: DURATION.exit, ease: CURVE }}
            className="overflow-hidden"
          >
            <div className="mt-1 space-y-1.5 rounded-md border border-border bg-inset px-3 py-2.5">
              {steps.map((step, i) => (
                <ToolCallCard
                  // Steps are append-only and never reordered, so the index is
                  // a stable identity. The name is not: one tool runs many
                  // times in a turn.
                  key={i}
                  name={step.name}
                  payload={step.payload}
                  loading={step.running}
                />
              ))}
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}
