import { describe, expect, it } from "vitest";
import type { MyCapabilitiesResponse } from "@argentum/api-types";
import {
  MIC_PROBLEM_COPY,
  VOICE_NOT_GRANTED,
  appendTranscript,
  listenFailureFor,
  micProblemFor,
  recordingFileName,
  recordingSupport,
  recordingType,
  spokenCaption,
  transcriptionErrorMessage,
  voiceClipIdsForSend,
  voiceFrom,
} from "./voice";

/** T-W9's rules for the microphone and the play button, as pure functions. */

describe("voiceFrom", () => {
  it("is nothing until the read arrives, and nothing from a backend that sends no voice", () => {
    for (const data of [undefined, { capabilities: [] } as unknown as MyCapabilitiesResponse]) {
      const v = voiceFrom(data);
      expect(v.transcribe).toBe(false);
      expect(v.readAloud).toBe(false);
      expect(v.granted).toBe(false);
    }
  });

  it("takes the grant from the list and what is possible from the deployment — two different facts", () => {
    const v = voiceFrom({
      capabilities: [{ user_id: "u-1", capability: "voice", granted_at: "" }],
      voice: { transcribe: true, read_aloud: false, max_clip_seconds: 45 },
    });
    expect(v).toEqual({ transcribe: true, readAloud: false, granted: true, maxClipSeconds: 45 });

    const other = voiceFrom({
      capabilities: [{ user_id: "u-1", capability: "export_data", granted_at: "" }],
      voice: { transcribe: true, read_aloud: true, max_clip_seconds: 0 },
    });
    expect(other.granted).toBe(false);
    expect(other.maxClipSeconds).toBe(60);
  });
});

describe("the microphone's problems", () => {
  it("names a refused getUserMedia by the exception's name", () => {
    expect(micProblemFor({ name: "NotAllowedError" })).toBe("denied");
    expect(micProblemFor({ name: "SecurityError" })).toBe("denied");
    expect(micProblemFor({ name: "NotFoundError" })).toBe("no-microphone");
    expect(micProblemFor({ name: "OverconstrainedError" })).toBe("no-microphone");
    expect(micProblemFor({ name: "NotReadableError" })).toBe("busy");
    // Found by the first screenshot run: headless Chromium says this, and it is
    // a browser that will not record, not a microphone that failed.
    expect(micProblemFor({ name: "NotSupportedError" })).toBe("unsupported");
    expect(micProblemFor(new Error("something else"))).toBeNull();
    expect(micProblemFor("nope")).toBeNull();
  });

  it("says plain http before it says unsupported, because the browser hides the microphone there", () => {
    expect(recordingSupport({ isSecureContext: false, hasGetUserMedia: false, hasMediaRecorder: false })).toBe("insecure");
    expect(recordingSupport({ isSecureContext: true, hasGetUserMedia: true, hasMediaRecorder: false })).toBe("unsupported");
    expect(recordingSupport({ isSecureContext: true, hasGetUserMedia: false, hasMediaRecorder: true })).toBe("unsupported");
    expect(recordingSupport({ isSecureContext: true, hasGetUserMedia: true, hasMediaRecorder: true })).toBeNull();
  });

  // T-W9: "three distinct messages, because they have three distinct fixes".
  it("gives every problem its own reason and its own fix", () => {
    const copies = Object.values(MIC_PROBLEM_COPY);
    expect(new Set(copies.map((c) => c.reason)).size).toBe(copies.length);
    expect(new Set(copies.map((c) => c.fix)).size).toBe(copies.length);
  });
});

describe("recording", () => {
  it("asks for Opus in WebM first, and takes Safari's MP4 when that is all there is", () => {
    expect(recordingType(() => true)).toBe("audio/webm;codecs=opus");
    expect(recordingType((t) => t === "audio/mp4")).toBe("audio/mp4");
    expect(recordingType(() => false)).toBe("");
    expect(recordingFileName("audio/mp4")).toBe("question.m4a");
    expect(recordingFileName("audio/ogg;codecs=opus")).toBe("question.ogg");
    expect(recordingFileName("")).toBe("question.webm");
  });
});

describe("the transcript in the box", () => {
  it("goes after what is already typed, never over it", () => {
    expect(appendTranscript("", " berapa stok ")).toBe("berapa stok");
    expect(appendTranscript("   ", "berapa stok")).toBe("berapa stok");
    expect(appendTranscript("Untuk gudang barat,  ", "berapa stok")).toBe("Untuk gudang barat, berapa stok");
  });

  it("sends its clip ids only to a conversation that exists, and only when there are some", () => {
    expect(voiceClipIdsForSend("th-1", ["c-1"])).toEqual(["c-1"]);
    expect(voiceClipIdsForSend("th-1", [])).toBeUndefined();
    expect(voiceClipIdsForSend(null, ["c-1"])).toBeUndefined();
  });

  it("captions a question that was dictated, and says whether it went out as heard", () => {
    expect(spokenCaption({ voice: { clip_ids: ["c-1"], verbatim: true } })).toBe("Spoken");
    expect(spokenCaption({ voice: { clip_ids: ["c-1", "c-2"], verbatim: false } })).toBe("Spoken, then edited");
    expect(spokenCaption(undefined)).toBeUndefined();
    expect(spokenCaption({ next_steps: [] })).toBeUndefined();
    expect(spokenCaption({ voice: { clip_ids: [], verbatim: true } })).toBeUndefined();
    expect(spokenCaption({ voice: "spoken" })).toBeUndefined();
  });
});

describe("what a failure says", () => {
  it("tells a person whose grant was revoked who to ask, and otherwise uses the route's own sentence", () => {
    expect(transcriptionErrorMessage({ response: { status: 403, data: { error: "an admin has not granted you this" } } })).toBe(
      VOICE_NOT_GRANTED,
    );
    expect(
      transcriptionErrorMessage({ response: { status: 413, data: { error: "a recording must be 60 seconds or shorter" } } }),
    ).toBe("a recording must be 60 seconds or shorter");
    expect(transcriptionErrorMessage({})).toBe("Could not transcribe that recording. Try again, or type your question.");
  });

  it("reads a refused answer's reason out of the blob the audio request got back", async () => {
    const body = JSON.stringify({
      error: "this answer cannot be read aloud; read it instead",
      reason: "spoken 2 million, nearest written 1,234,567",
    });
    const refused = await listenFailureFor({ response: { status: 422, data: new Blob([body]) } });
    expect(refused).toEqual({
      refused: true,
      message: "This answer can't be read aloud — read it instead.",
      reason: "spoken 2 million, nearest written 1,234,567",
    });

    const down = await listenFailureFor({ response: { status: 502, data: new Blob(["<html>bad gateway</html>"]) } });
    expect(down).toEqual({ refused: false, message: "Could not read it aloud. Try again." });

    expect((await listenFailureFor({ response: { status: 403, data: new Blob(["{}"]) } })).message).toBe(VOICE_NOT_GRANTED);
  });
});
