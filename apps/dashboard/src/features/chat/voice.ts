import type { MyCapabilitiesResponse, SpokenQuestion } from "@argentum/api-types";
import { apiErrorMessage, apiErrorStatus } from "@/lib/api-error";

/**
 * The microphone's and the play button's rules (T-W9), kept apart from the
 * components so each is a pure function with a test beside it — the arrangement
 * `settings/access.ts` set for the access matrix.
 *
 * Nothing here decides who may speak. `capabilityPolicy` does, on every request;
 * these functions only have to agree with it about what a person is shown.
 */

/** What this deployment and this person can do with voice, from one read. */
export interface Voice {
  /** `POST /api/threads/:id/voice` is registered. */
  transcribe: boolean;
  /** `GET /api/messages/:id/audio` is registered. */
  readAloud: boolean;
  /** The person holds the `voice` capability. An admin needs it too. */
  granted: boolean;
  /** Where the microphone stops itself. */
  maxClipSeconds: number;
}

/** The limit the route defaults to, used until the read says otherwise. */
const DEFAULT_MAX_CLIP_SECONDS = 60;

/**
 * Voice from `GET /api/users/me/capabilities`. A read that has not arrived, or a
 * backend older than T-W9 that sends no `voice`, is voice unavailable — which
 * draws nothing, rather than a microphone that answers 404.
 */
export function voiceFrom(data: MyCapabilitiesResponse | undefined): Voice {
  const v = data?.voice;
  return {
    transcribe: !!v?.transcribe,
    readAloud: !!v?.read_aloud,
    granted: (data?.capabilities ?? []).some((g) => g.capability === "voice"),
    maxClipSeconds: v && v.max_clip_seconds > 0 ? v.max_clip_seconds : DEFAULT_MAX_CLIP_SECONDS,
  };
}

/**
 * Why the microphone cannot record. T-W9's acceptance is that *"permission
 * denied, no microphone, and an unsupported browser are three distinct messages,
 * because they have three distinct fixes"* — and a microphone another app holds
 * and a page on plain http are two more fixes of their own.
 */
export type MicProblem = "denied" | "no-microphone" | "busy" | "unsupported" | "insecure";

/** The reason, and what to do about it. Both are always on screen together. */
export const MIC_PROBLEM_COPY: Record<MicProblem, { reason: string; fix: string }> = {
  denied: {
    reason: "The browser is not allowed to use your microphone.",
    fix: "Allow the microphone for this site in the address bar's site settings, then hold the button again.",
  },
  "no-microphone": {
    reason: "No microphone was found.",
    fix: "Connect one, or check it is switched on, then try again.",
  },
  busy: {
    reason: "Your microphone is being used by another app.",
    fix: "Close the other app, then try again.",
  },
  unsupported: {
    reason: "This browser cannot record audio.",
    fix: "Use a current Chrome, Edge, Firefox or Safari — or type your question.",
  },
  insecure: {
    reason: "The microphone only works on a secure (https) address.",
    fix: "Open Argentum over https, or type your question.",
  },
};

/** Whether this browser, on this page, can record at all — asked before a
 *  permission prompt that could not lead anywhere. On plain http the browser
 *  hides `mediaDevices` entirely, so that case is checked first and named. */
export function recordingSupport(env: {
  isSecureContext: boolean;
  hasGetUserMedia: boolean;
  hasMediaRecorder: boolean;
}): MicProblem | null {
  if (!env.isSecureContext) return "insecure";
  if (!env.hasGetUserMedia || !env.hasMediaRecorder) return "unsupported";
  return null;
}

/** A thrown value's `name` — "NotAllowedError" — or "" when it has none. */
export function exceptionName(err: unknown): string {
  return typeof err === "object" && err !== null && "name" in err ? String((err as { name: unknown }).name) : "";
}

/**
 * What a refused `getUserMedia` means, by the exception's `name` — the one part
 * of it browsers agree on. Null for anything else, which the microphone reports
 * as a failure to start, naming the exception, rather than guessing a fix.
 *
 * `NotSupportedError` is a browser that has the API and will not capture audio
 * with it. It was found, not foreseen: headless Chromium answers every request
 * with it, and the first screenshot run of this microphone showed "could not
 * start" for a browser whose fix is the unsupported one.
 */
export function micProblemFor(err: unknown): MicProblem | null {
  switch (exceptionName(err)) {
    case "NotAllowedError":
    case "PermissionDeniedError":
    case "SecurityError":
      return "denied";
    case "NotFoundError":
    case "DevicesNotFoundError":
    case "OverconstrainedError":
      return "no-microphone";
    case "NotReadableError":
    case "TrackStartError":
      return "busy";
    case "NotSupportedError":
      return "unsupported";
    default:
      return null;
  }
}

/** The sentence an ungranted person gets on pressing the microphone. The control
 *  is drawn disabled rather than hidden — the 2026-08-04 decision: a missing
 *  control reads as a missing feature, a disabled one says who to ask. */
export const VOICE_NOT_GRANTED =
  "An admin has not given you voice. Ask one to turn it on for you in Settings → Team.";

/** A press shorter than this is a tap, not a question. Uploading it would bill a
 *  transcription of a click, and a transcript of nothing lands in the box. */
export const MIN_HOLD_MS = 500;

/** The formats asked of MediaRecorder, best first. Opus in WebM is what Chrome
 *  and Firefox write; Safari writes MP4. All four are ones the route accepts. */
const RECORDING_TYPES = ["audio/webm;codecs=opus", "audio/webm", "audio/ogg;codecs=opus", "audio/mp4"];

/** The first format this browser can record, or "" to let it choose. */
export function recordingType(isTypeSupported: (type: string) => boolean): string {
  return RECORDING_TYPES.find((t) => isTypeSupported(t)) ?? "";
}

/** The name a recording is uploaded under. The route decides the format from
 *  the declared type and the bytes, never from this; it is for the log. */
export function recordingFileName(type: string): string {
  if (type.includes("ogg")) return "question.ogg";
  if (type.includes("mp4")) return "question.m4a";
  return "question.webm";
}

/**
 * Where a transcript goes: after whatever is already in the box, never over it.
 * Somebody who typed half a question and dictated the rest has not asked for the
 * half they typed to be thrown away.
 */
export function appendTranscript(existing: string, transcript: string): string {
  const said = transcript.trim();
  if (!existing.trim()) return said;
  return `${existing.trimEnd()} ${said}`;
}

/** The clip ids a send carries: only to an existing conversation, which is the
 *  only place a clip can have been made, and only when there are some. */
export function voiceClipIdsForSend(threadId: string | null, ids: string[]): string[] | undefined {
  return threadId && ids.length > 0 ? ids : undefined;
}

function isSpokenQuestion(v: unknown): v is SpokenQuestion {
  if (typeof v !== "object" || v === null) return false;
  const s = v as Partial<SpokenQuestion>;
  return Array.isArray(s.clip_ids) && s.clip_ids.length > 0 && typeof s.verbatim === "boolean";
}

/** The caption under a question that was dictated, or undefined for one typed. */
export function spokenCaption(metadata: Record<string, unknown> | undefined): string | undefined {
  const v = metadata?.["voice"];
  if (!isSpokenQuestion(v)) return undefined;
  return v.verbatim ? "Spoken" : "Spoken, then edited";
}

/** What the microphone says when the recording could not be transcribed. A 403
 *  is a grant revoked since the page loaded, and says who to ask; everything
 *  else is the route's own sentence, which was written to be read here. */
export function transcriptionErrorMessage(e: unknown): string {
  if (apiErrorStatus(e) === 403) return VOICE_NOT_GRANTED;
  return apiErrorMessage(e, "Could not transcribe that recording. Try again, or type your question.");
}

function blobText(b: Blob): Promise<string> {
  if (typeof b.text === "function") return b.text();
  return new Promise((resolve) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result ?? ""));
    r.onerror = () => resolve("");
    r.readAsText(b);
  });
}

/** Why a press of the play button did not play. */
export interface ListenFailure {
  /** The answer cannot be read aloud, and asking again will not change that:
   *  the route remembers a refusal. The button stays off. */
  refused: boolean;
  message: string;
  /** The check's own reason — which figure it would not say — for the title. */
  reason?: string;
}

/**
 * What a failed `GET /api/messages/:id/audio` means. The request asked for audio,
 * so an error body arrives as a Blob and is read back as the JSON it is.
 */
export async function listenFailureFor(e: unknown): Promise<ListenFailure> {
  const status = apiErrorStatus(e);
  let body: { error?: unknown; reason?: unknown } = {};
  const data = (e as { response?: { data?: unknown } } | null)?.response?.data;
  try {
    if (data instanceof Blob) body = JSON.parse(await blobText(data));
    else if (typeof data === "object" && data !== null) body = data as typeof body;
  } catch {
    // Not JSON — a proxy's page. The status still says enough.
  }
  if (status === 422) {
    return {
      refused: true,
      message: "This answer can't be read aloud — read it instead.",
      reason: typeof body.reason === "string" ? body.reason : undefined,
    };
  }
  if (status === 403) return { refused: false, message: VOICE_NOT_GRANTED };
  if (status === 402 && typeof body.error === "string") return { refused: false, message: body.error };
  return { refused: false, message: "Could not read it aloud. Try again." };
}
