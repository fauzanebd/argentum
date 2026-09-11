/**
 * The widget's own chrome, in the tenant's language.
 *
 * `domain.WidgetConfig.Locale` has said what it is for since `T-23`: *"the
 * default language of the widget's own chrome. The agent answers in the
 * language it was asked in regardless — this is the label on the composer, not
 * an instruction to the model."* The server validates it to `en` or `id`,
 * defaults it to `en`, stores it and serves it. **Nothing read it.** A tenant
 * who set Indonesian got an Indonesian greeting they had typed themselves,
 * above an English composer they could not change — found by the browser gate
 * on 2026-09-11, the same sitting and the same species as the envelope defect
 * in `widget.md` §6a: a field an admin filled in that never reached a visitor.
 *
 * **Strings rather than an i18n library.** There are nine of them, in two
 * languages. Measured: the bundle went from 32.75 KB gzipped to 33.07, so this
 * table costs **0.32 KB against an 80 KB budget** — `pnpm size` reads 33.6 KB
 * including the stylesheet. The smallest runtime that does plurals and
 * interpolation is two orders of magnitude more than that, and nothing here has
 * a plural or an interpolation. When something does, that is the moment to
 * reconsider — not before.
 *
 * The greeting is deliberately absent from this table. It is the tenant's own
 * words when they wrote one, and `DefaultWidgetGreeting` when they did not —
 * both decided server-side, where the copy already lives.
 */

export interface Strings {
  /** The composer, idle. This is the string `Locale`'s own comment names. */
  placeholder: string;
  /** The composer, while a turn is in flight. */
  thinking: string;
  closeChat: string;
  message: string;
  send: string;
  sessionExpired: string;
  stopped: string;
  couldNotSend: string;
  wentWrong: string;
}

const EN: Strings = {
  placeholder: "Ask about your data…",
  thinking: "Thinking…",
  closeChat: "Close chat",
  message: "Message",
  send: "Send",
  sessionExpired: "Session expired. Reloading identity…",
  stopped: "The agent stopped before answering.",
  couldNotSend: "Could not send that.",
  wentWrong: "Something went wrong.",
};

const ID: Strings = {
  placeholder: "Tanya soal data Anda…",
  thinking: "Sedang berpikir…",
  closeChat: "Tutup obrolan",
  message: "Pesan",
  send: "Kirim",
  sessionExpired: "Sesi berakhir. Memuat ulang identitas…",
  stopped: "Agen berhenti sebelum menjawab.",
  couldNotSend: "Tidak dapat mengirim pesan itu.",
  wentWrong: "Terjadi kesalahan.",
};

const TABLES: Record<string, Strings> = { en: EN, id: ID };

/**
 * Picks a table. Unknown and absent both fall back to English, which is what
 * `WithDefaults` does server-side — a widget that rendered blank labels because
 * a locale arrived that this bundle predates would be worse than one that
 * renders the wrong language.
 */
export function stringsFor(locale?: string): Strings {
  return TABLES[(locale ?? "").toLowerCase()] ?? EN;
}
