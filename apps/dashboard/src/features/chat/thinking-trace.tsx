import { useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import {
  Brain,
  ChevronDown,
  Code2,
  Database,
  Search,
  Sparkles,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { cn } from "@/lib/utils";
import { useStagger, DURATION, CURVE } from "@/lib/motion";

/**
 * What the agent did before it answered (T-U4).
 *
 * This used to be one italic line behind a left border, showing only the most
 * recent `thinking` event — every earlier step was overwritten in state the
 * moment the next arrived. The steps were always on the wire; nothing kept
 * them. `chat-page.tsx` now accumulates them into an array and this renders the
 * whole trace.
 *
 * **No parsing.** An earlier design for this ticket read structure out of the
 * step text with regexes. Each `thinking` event already *is* one step, so the
 * shape comes from the stream rather than from a pattern that a reworded prompt
 * would silently break. `kindOf` below classifies a step to pick an icon, and
 * when it recognises nothing it says so by returning the neutral one — the text
 * always renders in full either way.
 */

/** A step's icon, chosen by what the sentence is about. Cosmetic by design:
 *  every branch renders the same text, so a miss costs an icon, not a step. */
const KINDS: Array<{ match: RegExp; icon: LucideIcon }> = [
  { match: /\b(sql|query|querying|select\b|from\b)/i, icon: Database },
  { match: /\b(search|searching|look(ing)? up|find(ing)?|retriev)/i, icon: Search },
  { match: /\b(schema|table|column|code|function)/i, icon: Code2 },
  { match: /\b(think|reason|consider|decid|plan)/i, icon: Brain },
];

function kindOf(step: string): LucideIcon {
  for (const { match, icon } of KINDS) if (match.test(step)) return icon;
  return Sparkles;
}

export function ThinkingTrace({
  steps,
  /** Seconds the turn has been running, for the collapsed summary. Omitted once
   *  the turn has settled and the caption becomes past tense. */
  elapsed,
  live,
  className,
}: {
  steps: string[];
  elapsed?: number;
  live?: boolean;
  className?: string;
}) {
  const [open, setOpen] = useState(false);
  const { container, item } = useStagger();

  // A turn with no thinking renders nothing at all — not an empty shell with a
  // chevron that opens onto nothing.
  if (steps.length === 0) return null;

  const summary = live
    ? elapsed !== undefined && elapsed >= 1
      ? `Thinking for ${Math.round(elapsed)}s`
      : "Thinking"
    : elapsed !== undefined && elapsed >= 1
      ? `Thought for ${Math.round(elapsed)}s`
      : "Thought about it";

  return (
    <div className={cn("text-xs", className)}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="group inline-flex items-center gap-1.5 rounded-md px-1.5 py-1 -ml-1.5 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <Sparkles className={cn("h-3.5 w-3.5", live && "text-primary")} />
        <span className="font-medium">{summary}</span>
        <span className="text-muted-subtle">
          {steps.length} step{steps.length === 1 ? "" : "s"}
        </span>
        <ChevronDown
          className={cn(
            "h-3.5 w-3.5 transition-transform",
            open && "rotate-180",
          )}
        />
      </button>

      <AnimatePresence initial={false}>
        {open && (
          <motion.div
            // Height is animated rather than `display` so the timeline below it
            // does not jump the scroll position by the full height of the trace
            // in a single frame.
            initial={{ height: 0, opacity: 0 }}
            animate={{ height: "auto", opacity: 1 }}
            exit={{ height: 0, opacity: 0 }}
            transition={{ duration: DURATION.exit, ease: CURVE }}
            className="overflow-hidden"
          >
            <motion.ol
              variants={container}
              initial="hidden"
              animate="visible"
              className="mt-1 space-y-1.5 rounded-md border border-border bg-inset px-3 py-2.5"
            >
              {steps.map((step, i) => {
                const Icon = kindOf(step);
                return (
                  <motion.li
                    // Steps are append-only and never reordered, so the index is
                    // a stable identity here. The text is not: two rounds of the
                    // same tool legitimately produce the same sentence twice.
                    key={i}
                    variants={item}
                    className="flex gap-2 text-muted-foreground"
                  >
                    <Icon className="mt-0.5 h-3 w-3 shrink-0 text-muted-subtle" />
                    <span className="min-w-0 flex-1 whitespace-pre-wrap break-words">
                      {step}
                    </span>
                  </motion.li>
                );
              })}
            </motion.ol>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}
