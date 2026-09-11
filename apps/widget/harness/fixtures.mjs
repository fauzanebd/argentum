/**
 * What `/api/embed` answers, for a harness that fakes the network rather than
 * the client.
 *
 * **Why interception and not a stubbed module.** The defect this arm exists for
 * (`widget.md` §6a) was the client reading `{config, agents}` as though it were
 * the config: `config.greeting` off the envelope, `undefined`, every tenant who
 * had filled in Settings → Widget shown Argentum's default instead of their own
 * words, for a month, with every server-side test passing. A harness that
 * stubbed `EmbedClient` would have stubbed out the exact line that was wrong.
 * So the bodies below are served to the real client over the real fetch, in the
 * real sandboxed frame, from the real built bundle.
 *
 * The shapes are `ConfigResponse` and `CurrentThreadResponse` from
 * `@argentum/api-types/embed`. They are written by hand here — a harness
 * fixture that disagreed with the contract would be the same species of defect
 * as the one it is checking, so what keeps them honest is that the widget is
 * now a consumer of the generated types and would not compile against a shape
 * that had moved.
 */

/** A tenant who filled the form in. Both fields are the arm: the greeting is
 *  their words, and the buttons are theirs. */
export const CONFIG_CONFIGURED = {
  config: {
    greeting: "Selamat datang di Gelael. Tanya apa saja soal penjualan toko Anda.",
    suggested_prompts: [
      "Berapa penjualan minggu ini?",
      "Cabang mana yang paling ramai kemarin?",
    ],
    locale: "id",
  },
  agents: [{ id: "ag-1", name: "Analis Penjualan", is_default: true }],
};

/** A tenant who filled in nothing. The point of photographing this beside the
 *  one above: for a month these two rendered identically, and that is precisely
 *  what nobody could see. `suggested_prompts` is present and empty rather than
 *  absent, which is what `domain.WidgetConfig`'s comment says "none" looks
 *  like. */
export const CONFIG_DEFAULT = {
  config: { suggested_prompts: [] },
  agents: [],
};

/** No conversation yet — the empty state is what both scenes are for. */
export const NO_THREAD = { thread: null };

/** A visitor who has been here before. The rows are what `embedwire.Transcript`
 *  serves: `role`, `content`, `created_at` and nothing else. There is
 *  deliberately no `tool_calls`, no `metadata` and no tool-role row here —
 *  §6b's leak was that the route served all three, and a fixture that included
 *  them would be photographing a server this one is not. */
export const RETURNING_THREAD = {
  thread: {
    id: "th-harness",
    messages: [
      { role: "user", content: "Berapa penjualan minggu ini?", created_at: "2026-09-11T02:00:00Z" },
      {
        role: "assistant",
        content: "Penjualan minggu ini **Rp 1,24 M**, naik 6,1% dari minggu lalu.",
        created_at: "2026-09-11T02:00:04Z",
      },
    ],
  },
};
