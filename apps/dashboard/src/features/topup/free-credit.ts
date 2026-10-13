import type { OperatorFreeCredit, OperatorFreeCreditResponse } from "@argentum/api-types";
import { api } from "@/lib/api";

/**
 * The operator's free-credit queue (RIZ-89) as the two operator pages read it.
 * Out of the component files so the tabs and the page share one fetch under
 * one key — the count on the tab and the rows on the page cannot disagree.
 */

export const FREE_CREDIT_KEY = ["operator-free-credit"];
export const FREE_CREDIT_REVIEW_KEY = [...FREE_CREDIT_KEY, "review"];
export const FREE_CREDIT_REVIEWED_KEY = [...FREE_CREDIT_KEY, "reviewed"];

export async function fetchFreeCredit(view: "review" | "reviewed"): Promise<OperatorFreeCredit[]> {
  const res = await api.get<OperatorFreeCreditResponse>("/operator/free-credit", { params: { view } });
  return (res.data?.requests ?? []).filter((r): r is OperatorFreeCredit => r != null);
}

/** "2 other sign-ups from this IP in 7 days", or null when there are none —
 *  including every request a budget check filed, which has no IP at all. */
export function sameIpSignal(n: number): string | null {
  if (n <= 0) return null;
  return `${n} other sign-up${n === 1 ? "" : "s"} from this IP in 7 days`;
}
