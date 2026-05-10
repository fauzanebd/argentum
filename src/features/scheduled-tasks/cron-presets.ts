export interface CronPreset {
  label: string;
  expr: string;
}

export const CRON_PRESETS: CronPreset[] = [
  { label: "Every 15 minutes", expr: "*/15 * * * *" },
  { label: "Hourly (on the hour)", expr: "0 * * * *" },
  { label: "Daily at 09:00", expr: "0 9 * * *" },
  { label: "Weekdays at 09:00", expr: "0 9 * * 1-5" },
  { label: "Every Monday at 07:00", expr: "0 7 * * 1" },
  { label: "1st of each month at 00:00", expr: "0 0 1 * *" },
];

export function defaultTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}
