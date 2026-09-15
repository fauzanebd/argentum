import { useCallback, useEffect, useRef, useState } from "react";
import {
  MIN_HOLD_MS,
  exceptionName,
  micProblemFor,
  recordingSupport,
  recordingType,
  type MicProblem,
} from "./voice";

/** A finished recording, ready to upload. */
export interface Recording {
  blob: Blob;
  /** Measured by this browser's clock. The route checks it and bills on the
   *  provider's own measure where there is one. */
  durationMs: number;
  type: string;
}

export type PushToTalkPhase = "idle" | "starting" | "recording";

/**
 * Something to tell the person that is not a problem with their microphone.
 *
 * `allowed` is the first press on a site: the browser's prompt makes everybody
 * let go of the button to click Allow, so that press records nothing, and the
 * next thing to say is that it will work now.
 */
export type PushToTalkNotice = "tap" | "allowed" | "failed" | null;

/** How long a press may wait on the browser before it is taken to have been a
 *  permission prompt rather than a tap. */
const PROMPT_MS = 1500;

interface Session {
  released: boolean;
  cancelled: boolean;
  pressedAt: number;
  startedAt: number;
  stream?: MediaStream;
  recorder?: MediaRecorder;
  chunks: Blob[];
  type: string;
  stopTimer?: ReturnType<typeof setTimeout>;
  meter?: { ctx: AudioContext; frame: number };
  onLeave?: (e: Event) => void;
}

/**
 * The level meter: a loudness between 0 and 1 from the stream, per frame, so a
 * person can see it is hearing them. Nothing where the browser has no Web Audio
 * — the recording works without it.
 */
function startMeter(stream: MediaStream, set: (level: number) => void): Session["meter"] {
  const Ctx =
    window.AudioContext ?? (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
  if (!Ctx || typeof requestAnimationFrame !== "function") return undefined;
  try {
    const ctx = new Ctx();
    const analyser = ctx.createAnalyser();
    analyser.fftSize = 512;
    ctx.createMediaStreamSource(stream).connect(analyser);
    const samples = new Uint8Array(analyser.fftSize);
    const meter = { ctx, frame: 0 };
    const tick = () => {
      analyser.getByteTimeDomainData(samples);
      let sum = 0;
      for (const s of samples) {
        const x = (s - 128) / 128;
        sum += x * x;
      }
      // ×4: speech at a normal distance sits around 0.05–0.2 RMS, which would
      // barely move a meter drawn to 1.
      set(Math.min(1, Math.sqrt(sum / samples.length) * 4));
      meter.frame = requestAnimationFrame(tick);
    };
    tick();
    return meter;
  } catch {
    return undefined;
  }
}

/**
 * Hold to record, let go to finish (T-W9).
 *
 * **A recording never outlives the person's attention.** It stops at the route's
 * limit, when the tab is hidden and when the window loses focus — T-W9's *"a
 * recording that keeps running after the user has left is a bill and a privacy
 * incident"*. Those stops upload what was said, as a release would; only
 * `cancel` throws a recording away.
 *
 * `onRecorded` is read through a ref, so a recording finishing after a re-render
 * reaches the callback of the render it finished in, not the one it started in.
 */
export function usePushToTalk({
  maxSeconds,
  onRecorded,
}: {
  maxSeconds: number;
  onRecorded: (r: Recording) => void;
}) {
  const [phase, setPhase] = useState<PushToTalkPhase>("idle");
  const [level, setLevel] = useState(0);
  const [problem, setProblem] = useState<MicProblem | null>(null);
  const [notice, setNotice] = useState<PushToTalkNotice>(null);
  /** With a `failed` notice, the exception's name when there was one. Nothing
   *  on the client logs, so a person reading "could not start (AbortError)" to
   *  support is the only way that name reaches anyone. */
  const [failedWith, setFailedWith] = useState("");
  const session = useRef<Session | null>(null);
  const recorded = useRef(onRecorded);
  recorded.current = onRecorded;

  const teardown = useCallback((s: Session) => {
    if (s.stopTimer) clearTimeout(s.stopTimer);
    if (s.meter) {
      cancelAnimationFrame(s.meter.frame);
      void s.meter.ctx.close().catch(() => {});
    }
    if (s.onLeave) {
      document.removeEventListener("visibilitychange", s.onLeave);
      window.removeEventListener("blur", s.onLeave);
    }
    // The tracks, not just the recorder: a stopped recorder over a live track
    // leaves the browser's "microphone in use" light on.
    s.stream?.getTracks().forEach((t) => t.stop());
    if (session.current === s) session.current = null;
    setPhase("idle");
    setLevel(0);
  }, []);

  const stop = useCallback(() => {
    const s = session.current;
    if (!s) return;
    s.released = true;
    if (s.recorder && s.recorder.state !== "inactive") s.recorder.stop();
  }, []);

  const cancel = useCallback(() => {
    const s = session.current;
    if (!s) return;
    s.cancelled = true;
    stop();
  }, [stop]);

  const start = useCallback(async () => {
    if (session.current) return;
    setNotice(null);
    setFailedWith("");
    const unsupported = recordingSupport({
      isSecureContext: window.isSecureContext,
      hasGetUserMedia: typeof navigator.mediaDevices?.getUserMedia === "function",
      hasMediaRecorder: typeof MediaRecorder !== "undefined",
    });
    if (unsupported) {
      setProblem(unsupported);
      return;
    }
    setProblem(null);
    const s: Session = { released: false, cancelled: false, pressedAt: Date.now(), startedAt: 0, chunks: [], type: "" };
    session.current = s;
    setPhase("starting");

    try {
      s.stream = await navigator.mediaDevices.getUserMedia({ audio: true });
    } catch (err) {
      teardown(s);
      const p = micProblemFor(err);
      if (p) {
        setProblem(p);
      } else {
        setFailedWith(exceptionName(err));
        setNotice("failed");
      }
      return;
    }
    if (s.released || s.cancelled) {
      // Let go before the browser answered. Nothing was recorded, so nothing is
      // uploaded — and a wait this long was the permission prompt.
      teardown(s);
      if (!s.cancelled) setNotice(Date.now() - s.pressedAt >= PROMPT_MS ? "allowed" : "tap");
      return;
    }

    const type = recordingType((t) => MediaRecorder.isTypeSupported?.(t) ?? false);
    let recorder: MediaRecorder;
    try {
      recorder = type ? new MediaRecorder(s.stream, { mimeType: type }) : new MediaRecorder(s.stream);
    } catch (err) {
      teardown(s);
      setFailedWith(exceptionName(err));
      setNotice("failed");
      return;
    }
    s.recorder = recorder;
    s.type = recorder.mimeType || type;
    recorder.ondataavailable = (e) => {
      if (e.data && e.data.size > 0) s.chunks.push(e.data);
    };
    recorder.onstop = () => {
      const durationMs = Date.now() - s.startedAt;
      teardown(s);
      if (s.cancelled) return;
      if (durationMs < MIN_HOLD_MS) {
        setNotice("tap");
        return;
      }
      const blob = new Blob(s.chunks, { type: s.type || "audio/webm" });
      if (blob.size === 0) {
        setNotice("failed");
        return;
      }
      recorded.current({ blob, durationMs, type: blob.type });
    };
    s.onLeave = (e) => {
      if (e.type === "blur" || document.visibilityState === "hidden") stop();
    };

    recorder.start();
    s.startedAt = Date.now();
    // Half a second inside the limit: the route refuses a declared length over
    // it, and this clock and the timer's are not the same clock.
    s.stopTimer = setTimeout(stop, Math.max(1, maxSeconds - 0.5) * 1000);
    document.addEventListener("visibilitychange", s.onLeave);
    window.addEventListener("blur", s.onLeave);
    s.meter = startMeter(s.stream, setLevel);
    setPhase("recording");
  }, [maxSeconds, stop, teardown]);

  // Leaving the page mid-recording throws it away: the person is gone, and the
  // component that would put the transcript somewhere is gone with them.
  useEffect(
    () => () => {
      const s = session.current;
      if (!s) return;
      s.cancelled = true;
      s.released = true;
      if (s.recorder && s.recorder.state !== "inactive") s.recorder.stop();
    },
    [],
  );

  return { phase, level, problem, notice, failedWith, start, stop, cancel };
}
