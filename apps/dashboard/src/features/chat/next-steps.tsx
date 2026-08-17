import type { Message, NextStep } from "@argentum/api-types";
import { api } from "@/lib/api";
import { DecisionCard } from "@/components/ui/decision-card";

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
 *   - **A tap sends.** Reversed on 2026-08-18 from T-U13's original rule, which
 *     was the starter questions' rule applied to a second surface. They are not
 *     the same gesture: a starter question is the first thing somebody sees,
 *     with nothing above it to judge it against, while a next step sits under an
 *     answer the reader has just read, in the agent's words, with its reasoning
 *     beside it. By then the decision has been made, and putting the sentence
 *     back in the composer asks for it twice.
 *   - **The newest assistant message only.** Options under every historical
 *     bubble turn a transcript into a wall of buttons, and a suggestion about a
 *     question from twenty minutes ago is not a next step. This is also what
 *     keeps the rule above from being expensive: one card, under the newest
 *     answer, never while a turn is in flight.
 *   - **A pick is recorded.** A suggestion nobody taps is worse than no
 *     suggestion, and the pick rate is the only evidence this feature works.
 *     Fire-and-forget, and written before the send rather than after: the turn
 *     navigates and unmounts this component, and a request started in a dead
 *     tree is one nobody can be sure went out.
 *
 * **Drawn as a decision card since 2026-08-18, where it used to be a chip row.**
 * The chips could not carry `why`: it was a `title` attribute — unreachable
 * without a mouse — plus one trailing sentence for whichever suggestion led, so
 * the agent's reasoning about the other two was written, stored, and never
 * shown. A row that can hold its own sentence shows all of them, and the lean
 * becomes an attribution the reader can weigh (*"Sales Analyst's lean"*) rather
 * than a dot they have to interpret.
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
  /** Whose lean it is. Absent on a thread whose agent has since been deleted,
   *  which the card renders as "Agent's lean" rather than as nobody's. */
  agentName,
  /** A turn is already going out. The options stop taking taps for the moment
   *  between the tap and the stream starting — after that the card unmounts,
   *  because a turn in flight is not the newest answered message. */
  sending,
  onPick,
}: {
  message: Message;
  agentName?: string;
  sending?: boolean;
  onPick: (prompt: string) => void;
}) {
  const messageId = message.id;
  const steps = nextStepsOf(message);
  // Absent, empty or malformed metadata renders exactly the screen as it is
  // today — the same every-branch-returns-null shape StarterQuestions uses.
  if (steps.length === 0 || !messageId) return null;

  function pick(step: NextStep, index: number) {
    // Recorded first, sent second. The send navigates and unmounts this
    // component, and a fetch fired from a tree that is going away is one
    // nobody can be sure left. Still fire-and-forget: a telemetry write is not
    // worth a spinner, an error state, or a tap that does nothing while it is
    // in flight.
    void api
      .post(`/messages/${messageId}/suggestion-picked`, { index })
      .catch(() => {});
    onPick(step.prompt);
  }

  // The recommended one leads. It is the agent's own answer to "what would you
  // do next", and burying it third makes the marking decorative. The index sent
  // to the API is the stored one, never the display one — the two differ by
  // exactly this sort, and a pick table keyed on display order would record a
  // suggestion nobody clicked.
  const ordered = [...steps].sort(
    (a, b) => Number(b.recommended) - Number(a.recommended),
  );
  const leadingIsLean = ordered[0]?.recommended === true;

  return (
    <DecisionCard
      className="mt-2"
      aria-label="Suggested next steps"
      question="What next?"
      leanId={leadingIsLean ? "step-0" : undefined}
      leanBy={agentName}
      options={ordered.map((step, i) => ({
        id: `step-${i}`,
        label: step.label,
        description: step.why,
        disabled: sending,
        onSelect: () => pick(step, steps.indexOf(step)),
      }))}
      footer="Tap one to ask it."
    />
  );
}
