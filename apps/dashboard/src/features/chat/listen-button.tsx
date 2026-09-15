import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Loader2, Square, Volume2 } from "lucide-react";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";
import { useVoice } from "./use-voice";
import { listenFailureFor, type ListenFailure } from "./voice";

/**
 * The answer playing now, so pressing play on a second stops the first. Module
 * state rather than a store: it is a handle to a media element, which nothing
 * renders from, and two answers talking over each other is the one thing it
 * exists to prevent.
 */
let nowPlaying: { stop: () => void } | null = null;

/**
 * Read this answer aloud (T-W8's route, T-W9's button).
 *
 * **Absent, not broken**, where the deployment cannot read aloud or the person is
 * not granted voice — T-W9's acceptance. An ungranted person learns why from the
 * microphone, which is drawn disabled with its sentence; the same sentence beside
 * every answer on the page would be a wall of it.
 */
export function ListenButton({ messageId }: { messageId: string }) {
  const voice = useVoice();
  if (!voice.readAloud || !voice.granted) return null;
  return <Listen messageId={messageId} />;
}

function Listen({ messageId }: { messageId: string }) {
  const qc = useQueryClient();
  const [phase, setPhase] = useState<"idle" | "loading" | "playing">("idle");
  const [failure, setFailure] = useState<ListenFailure | null>(null);
  const stopRef = useRef<(() => void) | null>(null);

  useEffect(() => () => stopRef.current?.(), []);

  const play = async () => {
    if (phase === "playing") {
      stopRef.current?.();
      return;
    }
    if (phase === "loading" || failure?.refused) return;
    setFailure(null);
    setPhase("loading");

    let audio: Blob;
    try {
      // Fetched on the first press and kept: the route bills nothing for a
      // second press, and this cache means a second press does not even ask.
      audio = await qc.fetchQuery({
        queryKey: ["message-audio", messageId],
        queryFn: async () =>
          (await api.get<Blob>(`/messages/${messageId}/audio`, { responseType: "blob" })).data,
        staleTime: Infinity,
        gcTime: 30 * 60_000,
        retry: false,
      });
    } catch (e) {
      setFailure(await listenFailureFor(e));
      setPhase("idle");
      return;
    }

    nowPlaying?.stop();
    const url = URL.createObjectURL(audio);
    const el = new Audio(url);
    const handle = {
      stop: () => {
        el.pause();
        finish();
      },
    };
    const finish = () => {
      URL.revokeObjectURL(url);
      if (nowPlaying === handle) nowPlaying = null;
      if (stopRef.current === handle.stop) stopRef.current = null;
      setPhase("idle");
    };
    el.onended = finish;
    el.onerror = () => {
      finish();
      setFailure({ refused: false, message: "Could not play it. Try again." });
    };
    nowPlaying = handle;
    stopRef.current = handle.stop;
    try {
      await el.play();
      setPhase("playing");
    } catch {
      finish();
      setFailure({ refused: false, message: "Could not play it. Try again." });
    }
  };

  return (
    <span className="inline-flex items-center gap-1">
      <button
        type="button"
        onClick={() => void play()}
        disabled={phase === "loading" || !!failure?.refused}
        aria-label={phase === "playing" ? "Stop reading aloud" : "Read this answer aloud"}
        title={failure?.reason}
        className="rounded p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-50 disabled:hover:bg-transparent"
      >
        {phase === "loading" ? (
          <Loader2 className="h-3.5 w-3.5 animate-spin" />
        ) : phase === "playing" ? (
          <Square className="h-3.5 w-3.5" />
        ) : (
          <Volume2 className="h-3.5 w-3.5" />
        )}
      </button>
      {failure && (
        <span
          className={cn("text-[11px]", failure.refused ? "text-muted-foreground" : "text-destructive-ink")}
        >
          {failure.message}
        </span>
      )}
    </span>
  );
}
