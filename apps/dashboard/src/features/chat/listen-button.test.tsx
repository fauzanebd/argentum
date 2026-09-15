// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * T-W9's play button over T-W8's route. jsdom plays no audio, so the media
 * element's play and pause are recorded rather than heard; what is under test is
 * when the page asks for audio, what it does with a refusal, and that it never
 * asks twice for an answer it already has.
 */

const get = vi.fn();
vi.mock("@/lib/api", () => ({ api: { get: (...args: unknown[]) => get(...args) } }));

import { ListenButton } from "./listen-button";

const ME = "/users/me/capabilities";
let capabilities: { capabilities: unknown[]; voice: { transcribe: boolean; read_aloud: boolean; max_clip_seconds: number } };
let audio: (path: string) => Promise<unknown>;
const play = vi.fn();
const pause = vi.fn();

beforeEach(() => {
  capabilities = {
    capabilities: [{ user_id: "u-1", capability: "voice", granted_at: "" }],
    voice: { transcribe: true, read_aloud: true, max_clip_seconds: 60 },
  };
  audio = async () => ({ data: new Blob([new Uint8Array([0xff, 0xfb, 0x90, 0x00])], { type: "audio/mpeg" }) });
  get.mockReset().mockImplementation((path: string) => (path === ME ? Promise.resolve({ data: capabilities }) : audio(path)));
  play.mockReset().mockResolvedValue(undefined);
  pause.mockReset();
  Object.defineProperty(HTMLMediaElement.prototype, "play", { value: play, configurable: true });
  Object.defineProperty(HTMLMediaElement.prototype, "pause", { value: pause, configurable: true });
  URL.createObjectURL = vi.fn(() => "blob:answer");
  URL.revokeObjectURL = vi.fn();
});

function mount(...ids: string[]) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      {(ids.length ? ids : ["m-1"]).map((id) => (
        <ListenButton key={id} messageId={id} />
      ))}
    </QueryClientProvider>,
  );
}

const audioRequests = () => get.mock.calls.filter(([path]) => path !== ME);
const refusedBody = (reason: string) =>
  new Blob([JSON.stringify({ error: "this answer cannot be read aloud; read it instead", reason })], {
    type: "application/json",
  });

describe("ListenButton — T-W9's play control", () => {
  it.each([
    ["this deployment cannot read aloud", () => (capabilities.voice.read_aloud = false)],
    ["the person is not granted voice", () => (capabilities.capabilities = [])],
  ])("is absent, not broken, when %s", async (_why, arrange) => {
    arrange();
    mount();
    await waitFor(() => expect(get).toHaveBeenCalledWith(ME));
    await act(async () => {});
    expect(screen.queryByRole("button", { name: "Read this answer aloud" })).toBeNull();
    expect(audioRequests()).toHaveLength(0);
  });

  it("asks for nothing until it is pressed, then plays — and a second press is served from what it has", async () => {
    mount();
    const button = await screen.findByRole("button", { name: "Read this answer aloud" });
    expect(audioRequests()).toHaveLength(0);

    fireEvent.click(button);
    await waitFor(() => expect(play).toHaveBeenCalledTimes(1));
    expect(audioRequests()).toEqual([["/messages/m-1/audio", { responseType: "blob" }]]);

    fireEvent.click(await screen.findByRole("button", { name: "Stop reading aloud" }));
    expect(pause).toHaveBeenCalledTimes(1);
    fireEvent.click(await screen.findByRole("button", { name: "Read this answer aloud" }));
    await waitFor(() => expect(play).toHaveBeenCalledTimes(2));
    expect(audioRequests()).toHaveLength(1);
  });

  it("says an answer the check refused cannot be read aloud, keeps the check's reason, and does not ask again", async () => {
    audio = () => Promise.reject({ response: { status: 422, data: refusedBody("spoken 2 million, nearest written 1,234,567") } });
    mount();
    fireEvent.click(await screen.findByRole("button", { name: "Read this answer aloud" }));

    expect(await screen.findByText("This answer can't be read aloud — read it instead.")).toBeInTheDocument();
    const button = screen.getByRole("button", { name: "Read this answer aloud" });
    expect(button).toBeDisabled();
    expect(button).toHaveAttribute("title", "spoken 2 million, nearest written 1,234,567");
    fireEvent.click(button);
    await act(async () => {});
    expect(audioRequests()).toHaveLength(1);
    expect(play).not.toHaveBeenCalled();
  });

  it("says to try again when the voice service failed, and the next press does", async () => {
    audio = () => Promise.reject({ response: { status: 502, data: new Blob(["{}"]) } });
    mount();
    fireEvent.click(await screen.findByRole("button", { name: "Read this answer aloud" }));
    expect(await screen.findByText("Could not read it aloud. Try again.")).toBeInTheDocument();

    audio = async () => ({ data: new Blob([new Uint8Array([0xff, 0xfb])], { type: "audio/mpeg" }) });
    fireEvent.click(screen.getByRole("button", { name: "Read this answer aloud" }));
    await waitFor(() => expect(play).toHaveBeenCalledTimes(1));
    expect(audioRequests()).toHaveLength(2);
  });

  it("stops the answer playing when another is started", async () => {
    mount("m-1", "m-2");
    const [first, second] = await screen.findAllByRole("button", { name: "Read this answer aloud" });
    fireEvent.click(first);
    await waitFor(() => expect(play).toHaveBeenCalledTimes(1));
    fireEvent.click(second);
    await waitFor(() => expect(play).toHaveBeenCalledTimes(2));
    expect(pause).toHaveBeenCalledTimes(1);
    expect(screen.getAllByRole("button", { name: "Read this answer aloud" })).toHaveLength(1);
    expect(screen.getAllByRole("button", { name: "Stop reading aloud" })).toHaveLength(1);
  });
});
