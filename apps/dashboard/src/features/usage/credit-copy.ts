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
