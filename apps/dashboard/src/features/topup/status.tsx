import type { ReactNode } from "react";
import type { TopupStatus } from "@argentum/api-types";
import { cn } from "@/lib/utils";
import { TOPUP_STATUS_LABELS, TOPUP_STATUS_TONES } from "./labels";

/** A pill, in the status palette the rest of the dashboard uses. */
export function Pill({ tone, children }: { tone: string; children: ReactNode }) {
  return (
    <span className={cn("inline-block rounded-full px-2 py-0.5 text-[11px] font-semibold", tone)}>
      {children}
    </span>
  );
}

export function TopupStatusBadge({ status }: { status: TopupStatus }) {
  return <Pill tone={TOPUP_STATUS_TONES[status]}>{TOPUP_STATUS_LABELS[status]}</Pill>;
}
