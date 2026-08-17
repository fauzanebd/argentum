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
 *   - **A click fills the composer. It does not send.** The same rule and the
 *     same reason as the starter questions: a turn that runs before the reader
 *     has read it teaches them nothing and spends a credit.
 *   - **The newest assistant message only.** Options under every historical
 *     bubble turn a transcript into a wall of buttons, and a suggestion about a
 *     question from twenty minutes ago is not a next step.
 *   - **A pick is recorded.** A suggestion nobody clicks is worse than no
 *     suggestion, and the pick rate is the only evidence this feature works.
 *     Fire-and-forget: a failed write must never cost the reader their click.
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
  onPick,
}: {
  message: Message;
  agentName?: string;
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
        onSelect: () => pick(step, steps.indexOf(step)),
      }))}
      footer="Picking one writes it into the composer. Nothing is sent until you send it."
    />
  );
}
