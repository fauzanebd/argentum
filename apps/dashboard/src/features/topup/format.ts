/**
 * How the top-up screens spell rupiah, dollars, rates and times (RIZ-83).
 *
 * Every number here arrives from the API as an integer — rupiah in whole
 * rupiah, credit in micro-USD, a rate as rupiah per dollar × 10 000 — and the
 * one computation the page does itself, the credit preview, is the server's
 * own integer formula done in BigInt. A float would be close; close is how a
 * page promises $8.93 and the waiting card shows $8.92.
 */

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** Groups digits by thousands with a dot, the Indonesian way: 150000 → "150.000". */
export function groupDigits(n: number | string): string {
  const digits = String(n).replace(/\D/g, "");
  if (digits === "") return "";
  return digits.replace(/^0+(?=\d)/, "").replace(/\B(?=(\d{3})+(?!\d))/g, ".");
}

/** "Rp 100.347". */
export function formatIdr(n: number): string {
  return `Rp ${groupDigits(n)}`;
}

/** Micro-USD as dollars, rounded down to the cent: 5763831 → "$5.76". Down, like
 *  the credit itself, so the page never shows a cent the balance will not. */
export function formatCreditUsd(micro: number): string {
  const cents = Math.floor(micro / 10_000);
  const dollars = Math.floor(cents / 100);
  return `$${dollars}.${String(cents % 100).padStart(2, "0")}`;
}

/** Whatever was typed, as a whole number of rupiah — digits only. Null when
 *  nothing is left, which reads as "no amount yet" rather than zero. */
export function parseAmountInput(text: string): number | null {
  const digits = text.replace(/\D/g, "");
  if (digits === "") return null;
  // Beyond Number's exact range is beyond any maximum, and is refused as one.
  return digits.length > 15 ? Number.MAX_SAFE_INTEGER : Number(digits);
}

/** The amount limits the options call reports. */
export interface AmountLimits {
  min_idr: number;
  max_idr: number;
  step_idr: number;
}

/**
 * The sentence an amount is refused with, or null when it is acceptable —
 * the same three sentences, in the same order, as the server (R4), so a
 * customer reads the same words whether the browser or the API said no.
 */
export function amountError(amount: number | null, limits: AmountLimits): string | null {
  if (amount == null) return "Enter an amount in whole thousands of rupiah.";
  if (amount < limits.min_idr) return `The minimum top-up is ${formatIdr(limits.min_idr)}.`;
  if (amount > limits.max_idr) return `The maximum top-up is ${formatIdr(limits.max_idr)}.`;
  if (amount % (limits.step_idr || 1000) !== 0) return "Enter an amount in whole thousands of rupiah.";
  return null;
}

/** The server's credit formula: ⌊amount × 10¹⁰ ÷ rate_e4⌋ micro-USD. BigInt,
 *  because Rp 5.000.000 × 10¹⁰ is past Number's exact range. */
export function previewCreditMicro(amountIdr: number, rateE4: number): number {
  if (rateE4 <= 0 || amountIdr <= 0) return 0;
  return Number((BigInt(amountIdr) * 10_000_000_000n) / BigInt(rateE4));
}

/** An account number in groups of four for reading: "1234 5678 9012 345".
 *  Copy takes the digits, not this. */
export function groupAccountNumber(s: string): string {
  const digits = s.replace(/\s+/g, "");
  return digits.replace(/(.{4})(?=.)/g, "$1 ");
}

/** What Copy puts on the clipboard: the digits alone. */
export function digitsOnly(s: string): string {
  return s.replace(/\D/g, "");
}

function pad(n: number): string {
  return String(n).padStart(2, "0");
}

/** "15 Oct 2026, 14:20", in the browser's own time zone. */
export function formatDateTime(iso: string | null | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return `${d.getDate()} ${MONTHS[d.getMonth()]} ${d.getFullYear()}, ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** "12 Oct", from a timestamp in local time. */
export function formatDayMonth(iso: string | null | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return `${d.getDate()} ${MONTHS[d.getMonth()]}`;
}

/** "12 Oct" from a calendar date "2026-10-12" — read as the date it names,
 *  not through Date, which would shift it a day west of UTC. */
export function formatRateDate(day: string | null | undefined): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(day ?? "");
  if (!m) return "";
  return `${Number(m[3])} ${MONTHS[Number(m[2]) - 1]}`;
}

/** A rate × 10 000 as rupiah with two decimals, Indonesian style:
 *  165234000 → "16.523,40". Truncated past the second decimal. */
export function formatRateE4(e4: number): string {
  const cents = Math.floor(e4 / 100);
  return `${groupDigits(Math.floor(cents / 100))},${pad(cents % 100)}`;
}

/** Basis points as the percentage an operator set: 500 → "5", 250 → "2,5". */
export function formatSpreadPct(bp: number): string {
  const whole = Math.floor(bp / 100);
  const frac = bp % 100;
  if (frac === 0) return String(whole);
  return `${whole},${String(frac).padStart(2, "0").replace(/0$/, "")}`;
}

/** Bytes as the drop zone shows a chosen file: "7.4 MB", "312 KB". */
export function formatFileSize(bytes: number): string {
  if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  return `${Math.max(1, Math.round(bytes / 1024))} KB`;
}
