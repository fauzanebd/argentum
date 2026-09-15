import { create } from "zustand";

/**
 * A message another surface wants the chat composer to open with (T-D23).
 *
 * The case it exists for: somebody is looking at a dashboard whose window is
 * wrong, and their only route back to the conversation that built it is to find
 * that conversation. "Ask for a change" is the door — it lands them in chat with
 * a reference the agent already understands, and their cursor after it.
 *
 * **Deliberately not the URL.** A `?ask=` search param would put the whole
 * sentence in the address bar, re-prefill on every refresh of that URL, and add
 * a history entry somebody has to press Back through twice. And deliberately not
 * persisted: a prefill is an intention that expires with the click, so it is
 * gone on reload rather than resurrected days later.
 *
 * **What travels in the text is the dashboard's markdown link**, which
 * `markdown-renderer.tsx` already parses back to a uuid — the one channel that
 * works in every surface, including the widget and `/v1`, without a second piece
 * of state for anyone to keep in sync.
 *
 * **The clip ids travel beside it (T-W9).** A question spoken on the new-chat
 * screen makes its conversation first and is transcribed into it, and the page
 * then moves to that conversation — which mounts a fresh composer. The
 * transcript crosses that the way a dashboard's link does, and the recordings it
 * came from have to cross with it or the message forgets it was spoken.
 *
 * `take()` is a read-and-clear on purpose: seeding the composer twice from one
 * click would overwrite whatever the reader had started typing.
 */
export interface ComposerPrefill {
  text: string;
  voiceClipIds: string[];
}

interface ComposerState {
  pending: ComposerPrefill | null;
  prefill: (text: string, voiceClipIds?: string[]) => void;
  take: () => ComposerPrefill | null;
}

export const useComposerStore = create<ComposerState>((set, get) => ({
  pending: null,
  prefill: (text, voiceClipIds = []) => set({ pending: { text, voiceClipIds } }),
  take: () => {
    const { pending } = get();
    if (pending !== null) set({ pending: null });
    return pending;
  },
}));
