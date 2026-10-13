import type { CompanyCredits } from "@argentum/api-types";
import { freeCreditLines } from "./credit-copy";

/**
 * The credit card's free-credit lines (RIZ-89 R14), drawn the same way on Usage
 * → Overview and the top-up page's balance card, for credit-copy.ts's reason.
 *
 * Pending is amber, not red: nothing is wrong, the workspace is waiting.
 * Rejected is red, in the used-up line's palette, because it is the same wall.
 */
export function FreeCreditCardLines({ credits }: { credits: CompanyCredits | null | undefined }) {
  const lines = freeCreditLines(credits);
  if (!lines) return null;
  if (lines.status === "rejected") {
    return <p className="mt-1.5 text-xs text-destructive">{lines.headline}</p>;
  }
  return (
    <>
      <p className="mt-1.5 text-xs font-medium text-warning-ink">{lines.headline}</p>
      <p className="mt-1 text-xs text-muted-foreground">{lines.detail}</p>
    </>
  );
}
