import { useState } from "react";
import { Check, Loader2, ShieldQuestion, X } from "lucide-react";
import type { ActionInvocation } from "@argentum/api-types";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { useDecideAction, usePendingActions } from "./use-actions";

/** describe renders the human sentence for a proposal from its redacted params.
 *  The backend's Action.Describe is authoritative, but the pending payload does
 *  not carry it, so the card reconstructs a faithful summary from action_kind +
 *  params. send_message is the one shipped kind (T-12a). */
function describe(inv: ActionInvocation): string {
  const p = (inv.params_redacted ?? {}) as Record<string, unknown>;
  if (inv.action_kind === "send_message") {
    const channel = typeof p.channel === "string" ? p.channel : "a channel";
    const target = typeof p.target_ref === "string" ? p.target_ref : "a recipient";
    const body = typeof p.body === "string" ? p.body : "";
    return `Send a ${channel} message to ${target}${body ? `: “${body}”` : ""}`;
  }
  return inv.action_kind;
}

/** ApprovalCard is the inline propose→approve→reject control the agent's
 *  proposal renders as in the chat stream (T-11). It mirrors ToolCallCard's
 *  styling so a proposal reads as part of the same conversation, not a modal
 *  bolted on beside it. Approve executes the action exactly once (the backend's
 *  state machine, T-10); reject is terminal. */
export function ApprovalCard({ invocation }: { invocation: ActionInvocation }) {
  const decide = useDecideAction();
  const [error, setError] = useState<string | null>(null);
  const settled = invocation.status !== "proposed";

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

  return (
    <div className="rounded-md border border-amber-300/60 bg-amber-50/60 px-3 py-2 text-xs dark:border-amber-500/30 dark:bg-amber-500/10">
      <div className="mb-1 flex items-center gap-1.5 font-medium text-amber-700 dark:text-amber-400">
        <ShieldQuestion className="h-3.5 w-3.5" />
        Approval needed
      </div>
      <p className="text-foreground">{describe(invocation)}</p>

      {settled ? (
        <p
          className={cn(
            "mt-1.5 font-medium",
            invocation.status === "executed" && "text-emerald-600 dark:text-emerald-400",
            invocation.status === "rejected" && "text-muted-foreground",
            invocation.status === "failed" && "text-destructive",
          )}
        >
          {invocation.status === "executed" && "Approved and sent."}
          {invocation.status === "rejected" && "Rejected — nothing was done."}
          {invocation.status === "failed" &&
            `Failed: ${invocation.error_text ?? "the action could not be carried out"}.`}
          {invocation.status === "approved" && "Approved — running…"}
          {invocation.status === "expired" && "This proposal expired before it was decided."}
        </p>
      ) : (
        <div className="mt-2 flex items-center gap-2">
          <Button
            size="sm"
            className="h-7 px-2"
            disabled={decide.isPending}
            onClick={() => run("approve")}
          >
            {decide.isPending ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <Check className="h-3.5 w-3.5" />
            )}
            Approve
          </Button>
          <Button
            size="sm"
            variant="outline"
            className="h-7 px-2"
            disabled={decide.isPending}
            onClick={() => run("reject")}
          >
            <X className="h-3.5 w-3.5" />
            Reject
          </Button>
        </div>
      )}
      {error && <p className="mt-1.5 text-destructive">{error}</p>}
    </div>
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
