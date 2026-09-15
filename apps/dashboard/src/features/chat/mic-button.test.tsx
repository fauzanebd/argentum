// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

/**
 * T-W9's microphone, driven through the real component. The browser's
 * microphone and recorder are stand-ins — jsdom has neither — and the clock is
 * this file's, so "held for 2.4 seconds" is a fact the test states rather than a
 * sleep it waits out.
 */

const post = vi.fn();
vi.mock("@/lib/api", () => ({ api: { post: (...args: unknown[]) => post(...args) } }));

import { MicButton } from "./mic-button";
import { MIC_PROBLEM_COPY, VOICE_NOT_GRANTED, type MicProblem, type Voice } from "./voice";

class FakeRecorder {
  static made: FakeRecorder[] = [];
  static isTypeSupported(type: string) {
    return type === "audio/webm;codecs=opus";
  }
  state: "inactive" | "recording" = "inactive";
  mimeType: string;
  ondataavailable: ((e: { data: Blob }) => void) | null = null;
  onstop: (() => void) | null = null;
  constructor(_stream: unknown, opts?: { mimeType?: string }) {
    this.mimeType = opts?.mimeType ?? "";
    FakeRecorder.made.push(this);
  }
  start() {
    this.state = "recording";
  }
  stop() {
    this.state = "inactive";
    const bytes = new Uint8Array([0x1a, 0x45, 0xdf, 0xa3, 1, 2, 3]);
    this.ondataavailable?.({ data: new Blob([bytes], { type: this.mimeType }) });
    this.onstop?.();
  }
}

const GRANTED: Voice = { transcribe: true, readAloud: true, granted: true, maxClipSeconds: 60 };
const track = { stop: vi.fn() };
const getUserMedia = vi.fn();
let now = 0;

beforeEach(() => {
  FakeRecorder.made = [];
  now = 10_000;
  vi.spyOn(Date, "now").mockImplementation(() => now);
  post.mockReset().mockResolvedValue({ data: { transcript: "berapa stok gudang barat", clip_id: "c-1", seconds: 2.4 } });
  track.stop.mockReset();
  getUserMedia.mockReset().mockResolvedValue({ getTracks: () => [track] });
  Object.defineProperty(window, "isSecureContext", { value: true, configurable: true });
  Object.defineProperty(navigator, "mediaDevices", { value: { getUserMedia }, configurable: true });
  vi.stubGlobal("MediaRecorder", FakeRecorder);
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function mount(voice: Partial<Voice> = {}, threadId: string | null = "th-1") {
  const onTranscript = vi.fn();
  const ensureThread = vi.fn().mockResolvedValue("th-new");
  render(
    <MicButton
      voice={{ ...GRANTED, ...voice }}
      threadId={threadId}
      ensureThread={ensureThread}
      onTranscript={onTranscript}
    />,
  );
  return { onTranscript, ensureThread, mic: () => screen.getByRole("button", { name: "Hold to speak a question" }) };
}

/** Press, wait until the page says it is listening, and let `ms` pass. */
async function holdFor(mic: HTMLElement, ms: number) {
  fireEvent.pointerDown(mic);
  await screen.findByText(/^Listening\./);
  now += ms;
}

describe("MicButton — T-W9's microphone", () => {
  it("is absent where this deployment cannot transcribe", () => {
    mount({ transcribe: false });
    expect(screen.queryByRole("button", { name: "Hold to speak a question" })).toBeNull();
  });

  it("is drawn disabled for a person without the grant, and says who to ask instead of recording", async () => {
    const { mic } = mount({ granted: false });
    expect(mic()).toHaveAttribute("aria-disabled", "true");
    fireEvent.pointerDown(mic());
    expect(await screen.findByText(VOICE_NOT_GRANTED)).toBeInTheDocument();
    expect(getUserMedia).not.toHaveBeenCalled();
    expect(post).not.toHaveBeenCalled();
  });

  it("uploads once on release, and hands back the transcript with its clip — it sends nothing", async () => {
    const { mic, onTranscript } = mount();
    await holdFor(mic(), 2400);
    fireEvent.pointerUp(mic());

    await waitFor(() => expect(onTranscript).toHaveBeenCalledWith("berapa stok gudang barat", "c-1", "th-1"));
    expect(post).toHaveBeenCalledTimes(1);
    const [path, form] = post.mock.calls[0] as [string, FormData];
    expect(path).toBe("/threads/th-1/voice");
    expect(form.get("duration_ms")).toBe("2400");
    expect(form.get("audio")).toBeInstanceOf(Blob);
    expect(FakeRecorder.made[0].mimeType).toBe("audio/webm;codecs=opus");
    expect(track.stop).toHaveBeenCalled();
  });

  it.each([
    ["Escape", () => fireEvent.keyDown(window, { key: "Escape" })],
    ["a cancelled pointer", () => fireEvent.pointerCancel(screen.getByRole("button", { name: "Hold to speak a question" }))],
  ])("uploads nothing when the recording is cancelled with %s", async (_how, cancel) => {
    const { mic, onTranscript } = mount();
    await holdFor(mic(), 3000);
    act(() => {
      cancel();
    });
    fireEvent.pointerUp(mic());
    await act(async () => {});
    expect(post).not.toHaveBeenCalled();
    expect(onTranscript).not.toHaveBeenCalled();
    expect(track.stop).toHaveBeenCalled();
  });

  // T-W9: "a recording that keeps running after the user has left is a bill and
  // a privacy incident". Leaving stops it, and what was said is kept.
  it("stops and uploads what was said when the window loses focus", async () => {
    const { mic, onTranscript } = mount();
    await holdFor(mic(), 1800);
    act(() => {
      window.dispatchEvent(new Event("blur"));
    });
    await waitFor(() => expect(onTranscript).toHaveBeenCalled());
    expect((post.mock.calls[0][1] as FormData).get("duration_ms")).toBe("1800");
  });

  it("stops and uploads what was said when the tab is hidden", async () => {
    const { mic } = mount();
    await holdFor(mic(), 1800);
    Object.defineProperty(document, "visibilityState", { value: "hidden", configurable: true });
    try {
      act(() => {
        document.dispatchEvent(new Event("visibilitychange"));
      });
      await waitFor(() => expect(post).toHaveBeenCalledTimes(1));
    } finally {
      Object.defineProperty(document, "visibilityState", { value: "visible", configurable: true });
    }
  });

  it("stops itself at the route's limit rather than recording into a refusal", async () => {
    const { onTranscript, mic } = mount({ maxClipSeconds: 1 });
    await holdFor(mic(), 900);
    await waitFor(() => expect(onTranscript).toHaveBeenCalled(), { timeout: 3000 });
    expect(post).toHaveBeenCalledTimes(1);
  });

  it("treats a tap as a tap: nothing is uploaded, and it says to hold", async () => {
    const { mic } = mount();
    await holdFor(mic(), 120);
    fireEvent.pointerUp(mic());
    expect(await screen.findByText("Hold the button while you speak, then let go.")).toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
  });

  it.each<[MicProblem, () => void]>([
    ["denied", () => getUserMedia.mockRejectedValue(new DOMException("blocked", "NotAllowedError"))],
    ["no-microphone", () => getUserMedia.mockRejectedValue(new DOMException("none", "NotFoundError"))],
    ["unsupported", () => vi.stubGlobal("MediaRecorder", undefined)],
    ["insecure", () => Object.defineProperty(window, "isSecureContext", { value: false, configurable: true })],
  ])("says the reason and the fix when the microphone is %s", async (problem, arrange) => {
    arrange();
    const { mic } = mount();
    fireEvent.pointerDown(mic());
    expect(await screen.findByText(MIC_PROBLEM_COPY[problem].reason)).toBeInTheDocument();
    expect(screen.getByText(MIC_PROBLEM_COPY[problem].fix)).toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
  });

  // A failure with no known fix names the exception: nothing on the client logs,
  // so the sentence a person reads out to support is the only record of it.
  it("names the exception when the microphone fails in a way that has no known fix", async () => {
    getUserMedia.mockRejectedValue(new DOMException("aborted", "AbortError"));
    const { mic } = mount();
    fireEvent.pointerDown(mic());
    expect(
      await screen.findByText("The microphone could not start (AbortError). Try again, or type your question."),
    ).toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
  });

  it("on the new-chat screen makes the conversation only once there is a recording, and files the clip under it", async () => {
    const { mic, ensureThread, onTranscript } = mount({}, null);
    await holdFor(mic(), 2000);
    expect(ensureThread).not.toHaveBeenCalled();
    fireEvent.pointerUp(mic());
    await waitFor(() => expect(onTranscript).toHaveBeenCalledWith("berapa stok gudang barat", "c-1", "th-new"));
    expect(ensureThread).toHaveBeenCalledTimes(1);
    expect(post.mock.calls[0][0]).toBe("/threads/th-new/voice");
  });

  it("shows the route's own sentence when it refuses the recording", async () => {
    post.mockRejectedValue({ response: { status: 413, data: { error: "a recording must be 60 seconds or shorter" } } });
    const { mic, onTranscript } = mount();
    await holdFor(mic(), 2000);
    fireEvent.pointerUp(mic());
    expect(await screen.findByText("a recording must be 60 seconds or shorter")).toBeInTheDocument();
    expect(onTranscript).not.toHaveBeenCalled();
  });
});
