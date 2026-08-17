import type { Message, NextStep } from "@argentum/api-types";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";

/**
 * What to ask next (T-U13).
 *
 * The agent that just answered is the one that has discovered what the data
 * supports, and the person reading the answer is the one least equipped to know
 * what this product can be asked next. T-Q10 writes the suggestions onto the
 * assistant message; this draws them.
 *
 * Three rules, and each is a decision rather than styling:
 *
 *   - **A click fills the composer. It does not send.** The same rule and the
 *     same reason as the starter questions: a turn that runs before the reader
 *     has read it teaches them nothing and spends a credit.
 *   - **The newest assistant message only.** Chips under every historical bubble
 *     turn a transcript into a wall of buttons, and a suggestion about a
 *     question from twenty minutes ago is not a next step.
 *   - **A pick is recorded.** A suggestion nobody clicks is worse than no
 *     suggestion, and the pick rate is the only evidence this feature works.
 *     Fire-and-forget: a failed write must never cost the reader their click.
 */

/**
 * nextStepsOf validates the shape at the boundary and answers nothing for
 * everything unexpected.
 *
 * `metadata` is `{[key: string]: unknown}` on the wire — the Go side is
 * `map[string]any` and tygo can say no more than that — so this is the one place
 * that knows what a next step looks like in the browser. A malformed blob must
 * render nothing rather than blank a transcript: the failure it would otherwise
 * cause is a conversation that will not draw because of a field nothing in the
 * conversation depends on.
 */
function nextStepsOf(message: Message): NextStep[] {
  const raw = message.metadata?.["next_steps"];
  if (!Array.isArray(raw)) return [];
  const out: NextStep[] = [];
  for (const item of raw) {
    if (!item || typeof item !== "object") continue;
    const s = item as Record<string, unknown>;
    if (typeof s.label !== "string" || typeof s.prompt !== "string") continue;
    if (!s.label.trim() || !s.prompt.trim()) continue;
    out.push({
      label: s.label,
      prompt: s.prompt,
      recommended: s.recommended === true,
      why: typeof s.why === "string" ? s.why : undefined,
    });
  }
  return out;
}

export function NextStepChips({
  message,
  onPick,
}: {
  message: Message;
  onPick: (prompt: string) => void;
}) {
  const messageId = message.id;
  const steps = nextStepsOf(message);
  // Absent, empty or malformed metadata renders exactly the screen as it is
  // today — the same every-branch-returns-null shape StarterQuestions uses.
  if (steps.length === 0 || !messageId) return null;

  function pick(step: NextStep, index: number) {
    onPick(step.prompt);
    // Fire and forget. The reader has already got what they clicked for — the
    // prompt is in the composer — and a telemetry write is not worth a spinner,
    // an error state or a click that does nothing while it is in flight.
    void api
      .post(`/messages/${messageId}/suggestion-picked`, { index })
      .catch(() => {});
  }

  // The recommended one leads. It is the agent's own answer to "what would you
  // do next", and burying it third makes the marking decorative.
  const ordered = [...steps].sort(
    (a, b) => Number(b.recommended) - Number(a.recommended),
  );

  return (
    <div
      className="mt-1.5 flex flex-wrap items-center gap-1.5"
      role="group"
      aria-label="Suggested next steps"
    >
      {ordered.map((step, i) => (
        <button
          key={`${step.label}-${i}`}
          type="button"
          onClick={() => pick(step, steps.indexOf(step))}
          title={step.recommended ? step.why || undefined : undefined}
          aria-label={
            step.recommended
              ? `Recommended: ${step.label}${step.why ? ` — ${step.why}` : ""}`
              : step.label
          }
          className={cn(
            // Same chip as StarterQuestions rather than a second chip look: they
            // are the same gesture in two places, and two treatments would read
            // as two features.
            "inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-[11px] shadow-hairline transition-colors",
            "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
            step.recommended
              ? // Border and dot rather than a fill: the chip sits under an
                // answer, and a filled button there competes with the answer for
                // the eye. Both halves of the distinction survive a colour-blind
                // reader and a greyscale screenshot, because the dot is a shape.
                "border-primary/60 bg-card text-foreground hover:border-primary"
              : "border-border bg-card text-muted-foreground hover:border-primary/50 hover:text-foreground",
          )}
        >
          {step.recommended && (
            <span
              aria-hidden
              className="h-1.5 w-1.5 shrink-0 rounded-full bg-primary"
            />
          )}
          {step.label}
        </button>
      ))}
      {/* The reason, once, beside the row rather than inside the chip. A chip
          long enough to hold its own justification is not a chip, and `title`
          alone is unreachable without a mouse. */}
      {ordered[0]?.recommended && ordered[0].why && (
        <span className="text-[11px] text-muted-foreground">
          — {ordered[0].why}
        </span>
      )}
    </div>
  );
}
