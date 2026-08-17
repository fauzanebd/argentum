import type { ReactNode } from "react";
import { Check, Loader2 } from "lucide-react";

import { cn } from "@/lib/utils";

/**
 * A question, the choices, and what each one will do.
 *
 * **Why this shape and not a pair of buttons.** Every decision this product
 * asks a human to make is one they are being asked to make *because* a machine
 * should not make it alone: approving an outbound action, or picking which
 * question to ask next. Two bare buttons state the verbs and leave the
 * consequences to be inferred — so the reader either already knew, or clicks
 * and finds out. A stacked option carries its own sentence, which is the whole
 * difference between authorising something and agreeing to something.
 *
 * **The lean is an attribution, not a default.** Where the agent has an opinion
 * it is named as one — *"Sales Analyst's lean"* — beside the option it would
 * take, in `positive`. It never pre-selects, never disables the others, and
 * never reorders itself into the only thing that looks clickable: the reader is
 * here because the choice is theirs. The tint and the check are two signals for
 * one state, so the marking survives a greyscale screenshot and a colour-blind
 * reader.
 *
 * Presentational only: no fetching, no mutation, no knowledge of what any
 * option does. Both callers — the approval card and the next-step list — own
 * their own verbs, and this owns how a decision looks.
 */

export type DecisionOption = {
  /** Stable across renders; also what `leanId` and `chosenId` name. */
  id: string;
  label: string;
  /** One line on what happens if this is chosen. */
  description?: ReactNode;
  onSelect?: () => void;
  disabled?: boolean;
  /** This option's own work is in flight. */
  busy?: boolean;
};

export function DecisionCard({
  question,
  options,
  /** The option the agent would take, if it has an opinion. */
  leanId,
  /** Whose lean it is. "Argentum's lean" when nothing better is known. */
  leanBy,
  /** Set once the decision is made: the card goes static and marks this one. */
  chosenId,
  /** Under the options — who decided and when, or why the reader may not. */
  footer,
  /** Above the options, under the question: an error, a caveat. */
  note,
  className,
  "aria-label": ariaLabel,
}: {
  question: ReactNode;
  options: DecisionOption[];
  leanId?: string;
  leanBy?: string;
  chosenId?: string;
  footer?: ReactNode;
  note?: ReactNode;
  className?: string;
  "aria-label"?: string;
}) {
  const settled = chosenId !== undefined;

  return (
    <section
      className={cn(
        "rounded-xl border border-border bg-card p-3 shadow-hairline",
        className,
      )}
      aria-label={ariaLabel}
    >
      <p className="text-sm font-semibold leading-snug text-foreground">
        {question}
      </p>
      {note && <p className="mt-1 text-xs text-muted-foreground">{note}</p>}

      <div
        className="mt-2.5 space-y-1.5"
        role={settled ? undefined : "group"}
        aria-label={settled ? undefined : "Choices"}
      >
        {options.map((option) => (
          <DecisionRow
            key={option.id}
            option={option}
            lean={option.id === leanId}
            leanBy={leanBy}
            chosen={option.id === chosenId}
            settled={settled}
          />
        ))}
      </div>

      {footer && (
        <p className="mt-2.5 text-[11px] text-muted-subtle">{footer}</p>
      )}
    </section>
  );
}

function DecisionRow({
  option,
  lean,
  leanBy,
  chosen,
  settled,
}: {
  option: DecisionOption;
  lean: boolean;
  leanBy?: string;
  chosen: boolean;
  settled: boolean;
}) {
  // Marked is "this row is the one being pointed at" — the agent's lean before
  // a decision, the reader's choice after it. One treatment for both, because
  // after the fact what matters is which row was taken, and the lean has
  // stopped being a suggestion.
  const marked = settled ? chosen : lean;

  const body = (
    <>
      <span className="flex items-center gap-1.5">
        {/* A shape, not only a colour. Reserved on every row so the labels
            line up whether or not anything is marked — a list that shifts
            sideways when a decision lands reads as two different lists. */}
        <span aria-hidden className="flex h-3.5 w-3.5 shrink-0 items-center justify-center">
          {option.busy ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin text-muted-foreground" />
          ) : marked ? (
            <Check className="h-3.5 w-3.5 text-positive-ink" />
          ) : null}
        </span>
        <span
          className={cn(
            "text-[13px] font-medium",
            settled && !chosen ? "text-muted-foreground" : "text-foreground",
          )}
        >
          {option.label}
        </span>
        {lean && !settled && (
          <span className="text-[11px] font-semibold text-positive-ink">
            {leanBy ? `${leanBy}'s lean` : "Agent's lean"}
          </span>
        )}
      </span>
      {option.description && (
        <span
          className={cn(
            "mt-0.5 block pl-5 text-xs leading-relaxed",
            settled && !chosen ? "text-muted-subtle" : "text-muted-foreground",
          )}
        >
          {option.description}
        </span>
      )}
    </>
  );

  const shell = cn(
    "block w-full rounded-lg border px-3 py-2 text-left transition-colors",
    marked
      ? "border-positive/40 bg-positive-tint"
      : "border-border bg-transparent",
  );

  if (settled || !option.onSelect) {
    return <div className={cn(shell, !marked && "opacity-80")}>{body}</div>;
  }

  return (
    <button
      type="button"
      onClick={option.onSelect}
      disabled={option.disabled || option.busy}
      className={cn(
        shell,
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
        marked
          ? "hover:border-positive/70"
          : "hover:border-border-strong hover:bg-muted",
        (option.disabled || option.busy) && "cursor-not-allowed opacity-60 hover:bg-transparent",
      )}
      aria-label={
        lean && leanBy ? `${option.label} — ${leanBy}'s lean` : undefined
      }
    >
      {body}
    </button>
  );
}
