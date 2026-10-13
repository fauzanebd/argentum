import type { ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { X } from "lucide-react";
import type { BudgetState, CompanyCredits } from "@argentum/api-types";
import { cn } from "@/lib/utils";
import { microToUsd } from "@/features/usage/labels";
import { formatCreditUsd } from "@/features/topup/format";

/**
 * TopUpLink is the way out of a credit notice for an admin (RIZ-83 R20).
 *
 * Inline, after the sentence, in the link style the rest of the notice text
 * sits in — it continues the sentence ("… Top up before it runs out … Top
 * up") rather than being a second call to action competing with it.
 */
export function TopUpLink({ children }: { children: ReactNode }) {
  return (
    <Link
      to="/top-up"
      className="font-medium text-primary underline underline-offset-2 hover:text-primary/90"
    >
      {children}
    </Link>
  );
}

/* ── Credit warning banner ───────────────────────────────────────────── */
/** Shown when the workspace is close to the end of its credit grant. The turn
 *  already ran — this is a heads-up, not a refusal, so it is styled as a
 *  notice rather than with the destructive palette the error strip uses.
 *
 *  Moved here from chat-page.tsx (RIZ-83) so both roles can be rendered in a
 *  test without a whole chat page around it. `canTopUp` adds the link for an
 *  admin; a member sees exactly the sentence it always was. */
export function BudgetWarningBanner({
  warning,
  onDismiss,
  canTopUp = false,
  className,
}: {
  warning: BudgetState;
  onDismiss: () => void;
  canTopUp?: boolean;
  className?: string;
}) {
  const balance = microToUsd(warning.balance_micro_usd);
  return (
    <div
      role="status"
      className={cn(
        "flex items-start gap-3 rounded-lg border border-primary/30 bg-primary/5 px-4 py-2.5 text-sm",
        className,
      )}
    >
      <div className="flex-1 min-w-0">
        <span className="font-medium">
          {warning.remaining_pct}% of your Argentum credit is left
        </span>
        <span className="text-muted-foreground">
          {" "}
          — ${balance.toFixed(2)} remaining. Top up before it runs out to keep
          chat, scheduled reports and the API working.
        </span>
        {canTopUp && (
          <>
            {" "}
            <TopUpLink>Top up</TopUpLink>
          </>
        )}
      </div>
      <button
        type="button"
        onClick={onDismiss}
        aria-label="Dismiss credit warning"
        className="shrink-0 text-muted-foreground hover:text-foreground transition-colors"
      >
        <X className="size-4" />
      </button>
    </div>
  );
}

/* ── Free credit waiting for approval ───────────────────────────────── */
/** Shown on the new-chat screen while the workspace's free credit waits for an
 *  operator, or was not approved (RIZ-89 R13) — before anybody types, so the
 *  first thing a new customer learns is not a refusal.
 *
 *  Read from `/usage/credits`, which reports the state only while there is no
 *  grant; a workspace with credit, a BYO workspace, and an API older than
 *  RIZ-89 all report nothing, and get no notice. The notice palette, not the
 *  destructive one: nothing has gone wrong. An admin is offered the instant way
 *  out — topping up — and a member reads the same sentences without it. */
export function FreeCreditNotice({
  credits,
  canTopUp = false,
  className,
}: {
  credits: CompanyCredits | null | undefined;
  canTopUp?: boolean;
  className?: string;
}) {
  const status = credits?.free_credit_status;
  if (status !== "pending" && status !== "rejected") return null;
  return (
    <div
      role="status"
      className={cn(
        "flex items-start gap-3 rounded-lg border border-primary/30 bg-primary/5 px-4 py-2.5 text-sm",
        className,
      )}
    >
      {status === "pending" ? (
        <div className="flex-1 min-w-0">
          <span className="font-medium">Your free credit is waiting for approval.</span>
          <span className="text-muted-foreground">
            {" "}
            We approve the {formatCreditUsd(credits?.free_credit_micro_usd ?? 0)} free credit for each new
            workspace by hand.{" "}
            {canTopUp
              ? "Connect data and set up agents meanwhile — chat starts working as soon as it's approved."
              : "Chat starts working as soon as it's approved."}
          </span>
          {canTopUp && (
            <>
              {" "}
              <TopUpLink>Top up to start now</TopUpLink>
            </>
          )}
        </div>
      ) : (
        <div className="flex-1 min-w-0">
          <span className="font-medium">Your free credit was not approved.</span>
          <span className="text-muted-foreground"> Top up to use chat, scheduled reports and the API.</span>
          {canTopUp && (
            <>
              {" "}
              <TopUpLink>Top up credit</TopUpLink>
            </>
          )}
        </div>
      )}
    </div>
  );
}
