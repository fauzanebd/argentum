import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { cn } from "@/lib/utils";
import { FREE_CREDIT_REVIEW_KEY, fetchFreeCredit } from "./free-credit";
import { TOPUP_STATUS_TONES } from "./labels";

/**
 * The operator's two queues, side by side (RIZ-89 R10): top-ups to review and
 * free credit to approve. Both pages are outside the navigation, so these tabs
 * are the only way from one to the other.
 *
 * The free-credit count is the review list itself (free-credit.ts), so the
 * number on the tab and the rows on the page are one fetch and never disagree.
 * Nothing when it fails: a tab without a count still goes where it says.
 */

export function OperatorTabs({ active }: { active: "top-ups" | "free-credit" }) {
  const pending = useQuery({
    queryKey: FREE_CREDIT_REVIEW_KEY,
    queryFn: () => fetchFreeCredit("review"),
    retry: false,
  });
  const count = pending.data?.length ?? 0;
  const tab = (on: boolean) =>
    cn(
      "-mb-px px-4 py-2 text-[13px]",
      on ? "border-b-2 border-primary font-medium text-foreground" : "text-muted-foreground hover:text-foreground",
    );
  return (
    <nav aria-label="Operator queues" className="mb-5 inline-flex border-b">
      <Link to="/operator/top-ups" className={tab(active === "top-ups")}>
        Top-ups
      </Link>
      <Link to="/operator/free-credit" className={tab(active === "free-credit")}>
        Free credit
        {/* The space is for the accessible name ("Free credit 3"), which
            would otherwise run the count into the label. */}
        {count > 0 && " "}
        {count > 0 && (
          <span
            className={cn(
              "ml-0.5 inline-block rounded-full px-[7px] py-px text-[11px] font-semibold",
              TOPUP_STATUS_TONES.in_review,
            )}
          >
            {count}
          </span>
        )}
      </Link>
    </nav>
  );
}
