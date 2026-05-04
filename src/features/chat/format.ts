import { format, formatDistanceToNow, isSameDay } from "date-fns";

export function formatRelative(iso: string): string {
  try {
    return formatDistanceToNow(new Date(iso), { addSuffix: true });
  } catch {
    return iso;
  }
}

/** Time under a chat bubble: clock time if today, else "Mon, 4 May 15:20". */
export function formatMessageTimestamp(iso: string): string {
  try {
    const d = new Date(iso);
    const now = new Date();
    if (isSameDay(d, now)) return format(d, "HH:mm");
    return format(d, "EEE, d MMM HH:mm");
  } catch {
    return iso;
  }
}

/** API latency shown in seconds. */
export function formatLatencySeconds(ms: number): string {
  const s = ms / 1000;
  const n = new Intl.NumberFormat(undefined, {
    maximumFractionDigits: s >= 10 ? 0 : 2,
    minimumFractionDigits: 0,
  }).format(s);
  return `${n} s`;
}
