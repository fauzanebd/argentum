import { useEffect, useState } from "react";
import { Loader2, Mic } from "lucide-react";
import type { VoiceTranscriptionResponse } from "@argentum/api-types";
import { api } from "@/lib/api";
import { apiErrorMessage } from "@/lib/api-error";
import { cn } from "@/lib/utils";
import { usePushToTalk, type Recording } from "./push-to-talk";
import {
  MIC_PROBLEM_COPY,
  VOICE_NOT_GRANTED,
  recordingFileName,
  transcriptionErrorMessage,
  type Voice,
} from "./voice";

type MicButtonProps = {
  voice: Voice;
  /** The conversation the clip is filed under, or null on the new-chat screen. */
  threadId: string | null;
  /** Makes the conversation a new-chat recording is filed under. Called only
   *  once there is a recording to file, so a cancelled press makes nothing. */
  ensureThread: () => Promise<string>;
  /** What was heard, the clip it was heard from, and the conversation it is in. */
  onTranscript: (text: string, clipId: string | undefined, threadId: string) => void;
  /** A message is going out; the microphone waits. */
  disabled?: boolean;
};

/**
 * The microphone (T-W9): hold to speak, let go, and what was heard lands in the
 * composer to be checked before it is sent.
 *
 * **Never sent for the person.** *"tiga ratus juta"* and *"tiga puluh juta"* are
 * one syllable and a factor of ten apart (roadmap 11, decision 13), so the
 * transcript goes in the box and the send button is the same one typing uses.
 *
 * **Absent where the deployment cannot transcribe**, as the play button is where
 * it cannot read aloud: no grant makes a missing provider work, so a disabled
 * microphone there would point at an admin who can do nothing. An ungranted
 * person on a deployment that can gets the control disabled, with the sentence.
 */
export function MicButton(props: MicButtonProps) {
  if (!props.voice.transcribe) return null;
  return <Microphone {...props} />;
}

function Microphone({ voice, threadId, ensureThread, onTranscript, disabled }: MicButtonProps) {
  const [uploading, setUploading] = useState(false);
  const [message, setMessage] = useState<string | null>(null);

  const upload = async (rec: Recording) => {
    setUploading(true);
    setMessage(null);
    try {
      let id = threadId;
      if (!id) {
        try {
          id = await ensureThread();
        } catch (e) {
          setMessage(apiErrorMessage(e, "Could not start a conversation. Try again."));
          return;
        }
      }
      const form = new FormData();
      form.append("audio", rec.blob, recordingFileName(rec.type));
      form.append("duration_ms", String(Math.round(rec.durationMs)));
      const res = await api.post<VoiceTranscriptionResponse>(`/threads/${id}/voice`, form);
      const text = res.data.transcript.trim();
      if (text) onTranscript(text, res.data.clip_id || undefined, id);
      else setMessage("Nothing was heard. Hold the button while you speak, or type your question.");
    } catch (e) {
      setMessage(transcriptionErrorMessage(e));
    } finally {
      setUploading(false);
    }
  };

  const { phase, level, problem, notice, failedWith, start, stop, cancel } = usePushToTalk({
    maxSeconds: voice.maxClipSeconds,
    onRecorded: (r) => void upload(r),
  });
  const recording = phase === "recording";
  const off = !voice.granted;
  const busy = uploading || !!disabled;

  // Escape cancels wherever focus is: the pointer holding the button is not on
  // the keyboard's side of the page.
  useEffect(() => {
    if (phase === "idle") return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        cancel();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [phase, cancel]);

  const press = () => {
    if (off) {
      setMessage(VOICE_NOT_GRANTED);
      return;
    }
    if (busy) return;
    setMessage(null);
    void start();
  };

  /** Let go on the button to finish; slide off it first to throw the recording
   *  away. A position the event does not carry counts as on the button. */
  const release = (e: React.PointerEvent<HTMLButtonElement>) => {
    if (off) return;
    const r = e.currentTarget.getBoundingClientRect();
    const x = e.clientX;
    const y = e.clientY;
    const known = Number.isFinite(x) && Number.isFinite(y) && (r.width > 0 || r.height > 0);
    const onButton = !known || (x >= r.left && x <= r.right && y >= r.top && y <= r.bottom);
    if (onButton) stop();
    else cancel();
  };

  const status = (() => {
    if (recording) {
      return (
        <div className="flex items-center gap-2.5">
          <LevelMeter level={level} />
          <span>Listening. Let go to put it in the box — slide off or press Esc to cancel.</span>
        </div>
      );
    }
    if (phase === "starting") return <span>Starting the microphone…</span>;
    if (uploading) return <span>Transcribing…</span>;
    if (problem) {
      return (
        <div className="space-y-0.5">
          <p className="font-medium text-foreground">{MIC_PROBLEM_COPY[problem].reason}</p>
          <p>{MIC_PROBLEM_COPY[problem].fix}</p>
        </div>
      );
    }
    if (message) return <span>{message}</span>;
    if (notice === "tap") return <span>Hold the button while you speak, then let go.</span>;
    if (notice === "allowed") return <span>The microphone is ready. Hold the button while you speak.</span>;
    if (notice === "failed") {
      return (
        <span>
          The microphone could not start{failedWith ? ` (${failedWith})` : ""}. Try again, or type your question.
        </span>
      );
    }
    return null;
  })();

  return (
    <span className="relative inline-flex">
      {status && (
        <div
          role="status"
          aria-live="polite"
          className="absolute bottom-full right-0 z-30 mb-2 w-72 rounded-lg border border-border bg-card p-2.5 text-left text-xs text-muted-foreground shadow-card"
        >
          {status}
        </div>
      )}
      <button
        type="button"
        aria-label="Hold to speak a question"
        aria-disabled={off || busy}
        aria-pressed={recording}
        onPointerDown={(e) => {
          if (e.button > 0) return;
          e.preventDefault();
          e.currentTarget.setPointerCapture?.(e.pointerId);
          press();
        }}
        onPointerUp={release}
        onPointerCancel={() => cancel()}
        onKeyDown={(e) => {
          if ((e.key === " " || e.key === "Enter") && !e.repeat) {
            e.preventDefault();
            press();
          }
        }}
        onKeyUp={(e) => {
          if (e.key === " " || e.key === "Enter") {
            e.preventDefault();
            stop();
          }
        }}
        // A long press on a phone opens a context menu over the button.
        onContextMenu={(e) => e.preventDefault()}
        className={cn(
          "inline-flex h-8 w-8 shrink-0 touch-none select-none items-center justify-center rounded-lg transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
          recording
            ? "bg-primary text-primary-foreground"
            : "text-muted-foreground hover:bg-muted hover:text-foreground",
          (off || busy) && !recording && "cursor-not-allowed opacity-50 hover:bg-transparent hover:text-muted-foreground",
        )}
      >
        {uploading || phase === "starting" ? (
          <Loader2 className="size-3.5 animate-spin" />
        ) : (
          <Mic className="size-3.5" />
        )}
      </button>
    </span>
  );
}

/** Five bars that rise with the voice. Decorative: the sentence beside it is
 *  what a screen reader announces. */
function LevelMeter({ level }: { level: number }) {
  return (
    <span aria-hidden className="flex h-4 shrink-0 items-end gap-0.5">
      {[0.35, 0.7, 1, 0.7, 0.35].map((weight, i) => (
        <span
          key={i}
          className="w-1 rounded-full bg-primary transition-[height] duration-75"
          style={{ height: `${Math.max(15, Math.min(100, level * weight * 100))}%` }}
        />
      ))}
    </span>
  );
}
