/**
 * The strings both halves of the harness need.
 *
 * Plain JS with no JSX so that `shoot.mjs` (node) and `fixtures.tsx` (vite) can
 * import the same literal. They must agree: the shooter types `FORM_NAME` into
 * the form, and the fixture answers the preview request as if the server had
 * composed *that* name. Two copies drifting would put a screenshot in the docs
 * showing a preview of a name nobody typed.
 */

/** 77 characters — seventeen past the cap, so the counter goes red and Save
 *  refuses before the server is asked. */
export const FORM_NAME =
  "Weekly revenue by branch, excluding retur, potongan and PPN on closed periods";

export const FORM_TRIGGER = "When someone asks for revenue broken down by branch for a period.";

export const FORM_BODY = [
  "1. Revenue excludes retur and potongan — join sales_returns and subtract.",
  "2. Branch is cabang.nama, not the free-text field on the order.",
  "3. A period is closed only when tutup_buku is set for every branch in it.",
].join("\n");
