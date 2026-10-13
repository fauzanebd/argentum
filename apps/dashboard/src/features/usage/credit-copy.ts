import type { CompanyCredits } from "@argentum/api-types";
import { microToUsd } from "./labels";

/**
 * The credit card's two sentences, shared by Usage → Overview and the top-up
 * page so the same balance is never described two ways (RIZ-83 R19).
 *
 * Which pair depends on whether the workspace has ever topped up. Before, the
 * grant is the free credit signup wrote (RIZ-79) and both lines read exactly
 * as they did. After, an approval resets the grant to the balance it left, so
 * the bar measures from the last top-up and the caption has to say so — "free
 * credit" under a balance somebody paid for would be wrong twice.
 */

export function toppedUp(credits: Pick<CompanyCredits, "last_topup_at"> | null | undefined): boolean {
  return credits?.last_topup_at != null && credits.last_topup_at !== "";
}

export function grantCaption(credits: CompanyCredits): string {
  const grant = microToUsd(credits.monthly_grant_micro_usd).toFixed(2);
  return toppedUp(credits) ? `of $${grant} at your last top-up` : `of $${grant} free credit`;
}

export function usedUpLine(credits: CompanyCredits): string {
  return toppedUp(credits)
    ? "Credit used up. New chats, scheduled reports and API calls are paused."
    : "Free credit used up. New chats, scheduled reports and API calls are paused.";
}

/**
 * The card's lines while free credit waits for an operator (RIZ-89 R14). There
 * is no grant yet, so there is no bar and no caption; these say why the
 * balance is $0.00 and when that changes. Null when the API reports no
 * free-credit state, which is every workspace that holds a grant — and every
 * answer from an API older than RIZ-89.
 */
export type FreeCreditLines =
  | { status: "pending"; headline: string; detail: string }
  | { status: "rejected"; headline: string };

export const FREE_CREDIT_PENDING_DETAIL =
  "Chat, scheduled reports and API calls start as soon as it's approved.";
export const FREE_CREDIT_REJECTED_LINE =
  "Free credit not approved. Top up to use chat, scheduled reports and the API.";

export function freeCreditLines(credits: CompanyCredits | null | undefined): FreeCreditLines | null {
  switch (credits?.free_credit_status) {
    case "pending": {
      const amount = microToUsd(credits.free_credit_micro_usd ?? 0).toFixed(2);
      return {
        status: "pending",
        headline: `$${amount} free credit waiting for approval`,
        detail: FREE_CREDIT_PENDING_DETAIL,
      };
    }
    case "rejected":
      return { status: "rejected", headline: FREE_CREDIT_REJECTED_LINE };
    default:
      return null;
  }
}
