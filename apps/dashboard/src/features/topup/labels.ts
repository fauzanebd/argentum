import type { TopupStatus } from "@argentum/api-types";

/**
 * What a top-up's status is called on screen (R17). "Added", not "Approved":
 * the customer cares that the credit is in their balance, not that somebody
 * clicked a button.
 */
export const TOPUP_STATUS_LABELS: Record<TopupStatus, string> = {
  awaiting_payment: "Waiting for payment",
  in_review: "In review",
  approved: "Added",
  rejected: "Rejected",
  expired: "Expired",
  cancelled: "Cancelled",
};

/** Each status in the dashboard's status palette: tint ground, ink text. */
export const TOPUP_STATUS_TONES: Record<TopupStatus, string> = {
  awaiting_payment: "bg-warning-tint text-warning-ink",
  in_review: "bg-primary-tint text-primary-ink",
  approved: "bg-positive-tint text-positive-ink",
  rejected: "bg-destructive-tint text-destructive-ink",
  expired: "bg-muted text-muted-foreground",
  cancelled: "bg-muted text-muted-foreground",
};

/** The operator's Fallback badge, in waiting-for-payment's amber. */
export const WARNING_TONE = TOPUP_STATUS_TONES.awaiting_payment;
