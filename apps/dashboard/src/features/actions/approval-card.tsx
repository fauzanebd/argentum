import { useState } from "react";
import {
  AlertTriangle,
  Check,
  Clock,
  Loader2,
  ShieldQuestion,
  X,
} from "lucide-react";
import type { ActionInvocation } from "@argentum/api-types";
import { cn } from "@/lib/utils";
import { DecisionCard } from "@/components/ui/decision-card";
import { useAuthStore } from "@/store/auth";
import { useComposerStore } from "@/store/composer";
import { useDecideAction, usePendingActions } from "./use-actions";

/** describe is the sentence the approver reads. It comes from the backend, which
 *  builds it with the action's own Describe — the same code that knows what
 *  Execute will do.
 *
 *  This function used to write it a second time, and only knew how: it
 *  special-cased send_message and fell back to the bare action_kind. So the
 *  human authorising an outbound authenticated HTTP call saw "http_action" —
 *  not the endpoint, not the values — while HTTPAction.Describe had been
 *  writing that exact sentence since T-12b. The kind stays as the fallback for a
 *  proposal whose kind this deployment no longer registers; every other case is
 *  the backend's sentence. */
function describe(inv: ActionInvocation): string {
  return inv.description || inv.action_kind;
}

/** ApprovalCard is the inline propose→approve→reject control the agent's
 *  proposal renders as in the chat stream (T-11). Approve executes the action
 *  exactly once (the backend's state machine, T-10); reject is terminal.
 *
 *  **Drawn as a decision card since 2026-08-18**, where it used to be a
 *  sentence over an Approve/Reject button pair. Two verbs state what the buttons
 *  are called and leave what they *do* to be inferred — on an outbound action
 *  that cannot be undone, the reader either already knew or found out by
 *  clicking. Each choice now carries its own line.
 *
 *  **And there is deliberately no lean on this card.** The agent proposing an
 *  action is not the same as it recommending you authorise one, and tinting
 *  "Go ahead" green before a human has decided is a nudge toward the
 *  irreversible half of the choice. The mark appears after the decision, on
 *  whichever option was taken. The next-step list, where the agent's lean is
 *  real data it produced, is where that marking belongs. */
/** The sentence a viewer who may not decide reads instead of a 403. Which roles
 *  may decide is per company per kind (company_actions.allowed_roles), so this
 *  says who to ask rather than naming a role the reader is not. */
const NOT_YOURS = "Someone with permission for this action has to approve it.";

export function ApprovalCard({ invocation }: { invocation: ActionInvocation }) {
  const decide = useDecideAction();
  const prefill = useComposerStore((s) => s.prefill);
  const me = useAuthStore((s) => s.user);
  const [error, setError] = useState<string | null>(null);
  const settled = invocation.status !== "proposed";
  // can_decide comes from the backend, which computes it from the same
  // allowed_roles the decide endpoint enforces — a role check written here
  // would be a second copy of a per-company rule, and it would be wrong for
  // every kind whose allowed_roles is not ["admin"]. An older backend that does
  // not send the field leaves the buttons live, which is the pre-2026-08-04
  // behaviour rather than a lockout.
  const canDecide = invocation.can_decide !== false;

  const run = (decision: "approve" | "reject") => {
    setError(null);
    decide.mutate(
      { id: invocation.id, decision },
      {
        onError: (e: unknown) => {
          const resp = (e as { response?: { data?: { error?: string } } })?.response;
          setError(resp?.data?.error ?? "Could not complete that decision.");
        },
      },
    );
  };

  const tone = TONES[invocation.status] ?? TONES.proposed;

  return (
    <div className="space-y-2">
      {/* The state line, kept above the card and in its own tone (T-U1). It is
          the one thing a reader scanning a thread needs before they read
          anything else: is this waiting on me, or is it history? */}
      <div className={cn("flex items-center gap-1.5 text-xs font-medium", tone.ink)}>
        <tone.Icon
          className={cn(
            "h-3.5 w-3.5",
            // `approved` is the one state that is still moving: the backend has
            // accepted the decision and the action is running.
            invocation.status === "approved" && "animate-spin",
          )}
        />
        {tone.title}
      </div>

      {/* Why this card exists at all, on a kind the workspace runs without
          asking (T-H9). Above the card rather than inside it: an admin who
          switched this kind to automatic and then finds a proposal waiting has
          to be able to tell a policy from a bug BEFORE they read the action,
          and the two readings lead to opposite decisions. Rendered text, not a
          title attribute — the same rule the next-step chips' reason follows,
          and for the same reason. */}
      {invocation.approval_forced_reason && (
        <p className="flex items-start gap-1.5 rounded-md border border-warning/40 bg-warning-tint px-2 py-1.5 text-xs text-warning-ink">
          <AlertTriangle className="mt-px h-3.5 w-3.5 shrink-0" aria-hidden />
          <span>
            Held for a decision because {invocation.approval_forced_reason}. This
            workspace normally runs this action without asking.
          </span>
        </p>
      )}

      <DecisionCard
        aria-label="Action approval"
        question={describe(invocation)}
        note={settled ? outcomeOf(invocation) : undefined}
        // "" rather than undefined for an expired proposal: the card goes
        // static — nobody may decide it now — with no option marked, because
        // running out of time is not a choice anybody made.
        chosenId={settled ? chosenIdOf(invocation.status) : undefined}
        options={[
          {
            id: "approve",
            label: "Go ahead",
            description:
              "Runs this once, exactly as described. It cannot be taken back.",
            busy: decide.isPending,
            disabled: !canDecide,
            onSelect: () => run("approve"),
          },
          {
            id: "adjust",
            label: "Adjust",
            description:
              "Puts it back in the composer to say what should change. Nothing runs, and the proposal stays open.",
            onSelect: () =>
              prefill(`About the proposed action — ${describe(invocation)}\n\nChange: `),
          },
          {
            id: "reject",
            label: "Drop it",
            description: "Rejects the proposal. Nothing runs, and it cannot be reopened.",
            disabled: !canDecide,
            onSelect: () => run("reject"),
          },
        ]}
        footer={
          settled ? (
            decidedLine(invocation, me?.id)
          ) : canDecide ? (
            <StateTrail status={invocation.status} />
          ) : (
            NOT_YOURS
          )
        }
      />

      {error && <p className="text-xs text-destructive-ink">{error}</p>}
    </div>
  );
}

/** Which option the decision landed on, for the card's static state. An
 *  expired proposal marks none: it is the one terminal status nobody chose. */
function chosenIdOf(status: string): string {
  if (status === "rejected") return "reject";
  if (status === "expired") return "";
  return "approve";
}

/** What happened, in the words the old card used. */
function outcomeOf(inv: ActionInvocation): string {
  switch (inv.status) {
    case "executed":
      return "Approved and sent.";
    case "rejected":
      return "Rejected — nothing was done.";
    case "failed":
      return `Failed: ${inv.error_text ?? "the action could not be carried out"}.`;
    case "approved":
      return "Approved — running…";
    case "expired":
      return "This proposal expired before it was decided.";
    default:
      return "";
  }
}

/**
 * Who decided, and when.
 *
 * `decided_by` is a user id, and this card has no directory to resolve it
 * against — so it says "you" when the id is the reader's own and "a teammate"
 * otherwise. Neither is a name, and inventing one from an id would be the
 * product asserting something it does not know about an audited decision.
 */
function decidedLine(inv: ActionInvocation, myId?: string): string {
  if (!inv.decided_at) return "";
  const who = inv.decided_by
    ? inv.decided_by === myId
      ? "you"
      : "a teammate"
    : "this deployment's policy";
  return `Decided by ${who} · ${whenWord(inv.decided_at)}`;
}

/** A weekday inside the last six days, a date before that. "Thursday" is what
 *  somebody remembers about a decision they made this week; "12 August" is
 *  what they need about one from last month. */
function whenWord(iso: string): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return "";
  const days = (Date.now() - at.getTime()) / 86_400_000;
  if (days < 1) return "today";
  if (days < 6) return at.toLocaleDateString(undefined, { weekday: "long" });
  return at.toLocaleDateString(undefined, { day: "numeric", month: "long" });
}

/**
 * How each state looks. One row per status the backend can report, so a state
 * the card does not style is a missing key rather than a silent fallthrough to
 * whatever the last `&&` produced.
 *
 * Every colour is a token from `T-U1`. This file used to carry
 * `border-amber-300/60`, `bg-amber-50/60`, `text-amber-700`, `text-emerald-600`
 * and their four dark-mode variants — a palette invented here, in the one
 * component where "is this waiting on me?" has to be legible at a glance.
 */
const TONES: Record<
  string,
  { Icon: typeof ShieldQuestion; title: string; border: string; bg: string; ink: string }
> = {
  proposed: {
    Icon: ShieldQuestion,
    title: "Approval needed",
    border: "border-warning/30",
    bg: "bg-warning-tint",
    ink: "text-warning-ink",
  },
  approved: {
    Icon: Loader2,
    title: "Approved",
    border: "border-warning/30",
    bg: "bg-warning-tint",
    ink: "text-warning-ink",
  },
  executed: {
    Icon: Check,
    title: "Done",
    border: "border-positive/30",
    bg: "bg-positive-tint",
    ink: "text-positive-ink",
  },
  rejected: {
    Icon: X,
    title: "Rejected",
    border: "border-border",
    bg: "bg-muted",
    ink: "text-muted-foreground",
  },
  failed: {
    Icon: AlertTriangle,
    title: "Failed",
    border: "border-destructive/30",
    bg: "bg-destructive-tint",
    ink: "text-destructive-ink",
  },
  expired: {
    Icon: Clock,
    title: "Expired",
    border: "border-border",
    bg: "bg-muted",
    ink: "text-muted-foreground",
  },
};

/**
 * Where this proposal got to (T-U7).
 *
 * Three fixed stops, because that is the shape of the backend's state machine:
 * every invocation is proposed, then decided, then — if it was approved — run.
 * The outcome stop is the only one that varies, so it takes its label from the
 * terminal status rather than from a fourth branch of markup.
 *
 * Rendered as the card's footer while a proposal is open, where it says how far
 * the machinery has got. Once a decision lands the footer says who made it and
 * when, which is the more useful sentence and the one an audit asks for.
 */
function StateTrail({ status }: { status: string }) {
  const open = status === "proposed";
  const decided = !open && status !== "expired";
  const ran = status === "executed" || status === "failed";
  // The third stop is where the status varies. On an open proposal it is what
  // has *not* happened yet, which is the honest label for a rail whose whole
  // job is saying where this got to.
  const outcome = open
    ? "Not run"
    : status === "executed"
      ? "Executed"
      : status === "failed"
        ? "Failed"
        : status === "rejected"
          ? "Rejected"
          : "Expired";

  const stops: Array<{ label: string; done: boolean }> = [
    { label: "Proposed", done: true },
    { label: decided ? "Decided" : "Not decided", done: decided },
    { label: outcome, done: ran || status === "rejected" || status === "expired" },
  ];

  return (
    <span className="inline-flex items-center gap-1.5 text-[10px] text-muted-subtle">
      {stops.map((stop, i) => (
        <span key={stop.label} className="inline-flex items-center gap-1.5">
          {i > 0 && <span aria-hidden className="h-px w-3 bg-border-strong" />}
          <span className={cn(stop.done && "text-muted-foreground")}>
            {stop.label}
          </span>
        </span>
      ))}
    </span>
  );
}

/** PendingApprovals is the strip above the composer that surfaces every proposal
 *  awaiting a decision for the active thread. Kept thread-scoped so a proposal
 *  raised in another conversation does not appear here; the app-shell badge is
 *  the company-wide count. */
export function PendingApprovals({ threadId }: { threadId: string | null }) {
  const { pending } = usePendingActions();
  const forThread = pending.filter(
    (p) => p.status === "proposed" && (!threadId || p.thread_id === threadId),
  );
  if (forThread.length === 0) return null;
  return (
    <div className="space-y-2 px-1 pb-2">
      {forThread.map((inv) => (
        <ApprovalCard key={inv.id} invocation={inv} />
      ))}
    </div>
  );
}
