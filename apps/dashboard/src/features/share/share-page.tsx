import { useEffect, useMemo, useRef, useState } from "react";
import axios from "axios";
import { Player, type PlayerRef } from "@remotion/player";
import { Report, SUPPORTED_VERSION, type Plan } from "@argentum/motion";
import { Download, Pause, Play } from "lucide-react";

/**
 * The shared report player (T-V4).
 *
 * A logged-out visitor with a link. There is no session, no tenant and no
 * `api` client here — this page deliberately uses a bare axios call to
 * `/share/:token`, because the shared client attaches an access token and
 * refreshes it on a 401, and neither is right for somebody who has neither.
 *
 * It plays the **plan**, not the video. The same compositions the render
 * service draws headlessly run here in the browser, which is why a link opens
 * instantly whether or not an mp4 was ever rendered — and why the frames a
 * visitor sees are the frames the file would have contained.
 */

type ShareView = {
  title: string;
  filename: string;
  format: string;
  plan: Plan;
  download_url?: string;
  expires_at: string;
};

export function SharePage({ token }: { token: string }) {
  const [state, setState] = useState<
    { kind: "loading" } | { kind: "gone" } | { kind: "ready"; view: ShareView }
  >({ kind: "loading" });

  useEffect(() => {
    let cancelled = false;
    axios
      .get<ShareView>(`/share/${encodeURIComponent(token)}`)
      .then((res) => {
        if (!cancelled) setState({ kind: "ready", view: res.data });
      })
      .catch(() => {
        // One outcome for every failure, matching the API: a visitor cannot
        // tell an expired link from a wrong one, and neither can this page.
        if (!cancelled) setState({ kind: "gone" });
      });
    return () => {
      cancelled = true;
    };
  }, [token]);

  if (state.kind === "loading") {
    return <Centered>Opening…</Centered>;
  }
  if (state.kind === "gone") {
    return (
      <Centered>
        <h1 className="text-xl font-semibold">This link is not available.</h1>
        <p className="mt-2 max-w-md text-sm text-muted-foreground">
          It may have expired, or been revoked by whoever shared it. Ask them
          for a new one.
        </p>
      </Centered>
    );
  }
  return <Deck view={state.view} />;
}

function Deck({ view }: { view: ShareView }) {
  const playerRef = useRef<PlayerRef>(null);
  const [playing, setPlaying] = useState(false);
  const [frame, setFrame] = useState(0);

  const plan = view.plan;

  // A plan from a future version renders the scenes it understands and says
  // so. The same rule the render service follows, one consumer further out: a
  // blank frame is the worst shape this failure can take, and a page that
  // refuses outright makes a link somebody sent look broken.
  const unknownVersion = plan?.version !== SUPPORTED_VERSION;

  const scene = useMemo(() => {
    if (!plan?.scenes?.length) return null;
    let at = 0;
    for (const s of plan.scenes) {
      if (frame < at + s.frames) return s;
      at += s.frames;
    }
    return plan.scenes[plan.scenes.length - 1];
  }, [plan, frame]);

  useEffect(() => {
    const p = playerRef.current;
    if (!p) return;
    // The player's own listener types, not DOM EventListener: `frameupdate`
    // carries a payload rather than an Event, and casting through
    // EventListener is what the compiler correctly refuses.
    const onFrame: Parameters<typeof p.addEventListener<"frameupdate">>[1] = (
      e,
    ) => setFrame(e.detail.frame);
    const onPlay = () => setPlaying(true);
    const onPause = () => setPlaying(false);
    p.addEventListener("frameupdate", onFrame);
    p.addEventListener("play", onPlay);
    p.addEventListener("pause", onPause);
    return () => {
      p.removeEventListener("frameupdate", onFrame);
      p.removeEventListener("play", onPlay);
      p.removeEventListener("pause", onPause);
    };
  }, []);

  if (!plan?.scenes?.length) {
    return <Centered>This report has nothing to play.</Centered>;
  }

  return (
    <div className="min-h-screen bg-background text-foreground">
      <div className="mx-auto flex max-w-6xl flex-col gap-6 px-6 py-10">
        <header className="flex flex-wrap items-end justify-between gap-4">
          <div>
            <h1 className="text-2xl font-semibold tracking-tight">
              {plan.title || view.filename}
            </h1>
            {plan.brand?.name ? (
              <p className="text-sm text-muted-foreground">{plan.brand.name}</p>
            ) : null}
          </div>
          {view.download_url ? (
            <a
              href={view.download_url}
              className="inline-flex items-center gap-2 rounded-md border px-3 py-2 text-sm hover:bg-muted"
            >
              <Download className="h-4 w-4" />
              Download video
            </a>
          ) : null}
        </header>

        {unknownVersion ? (
          <p className="rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm">
            This report was made with a newer version of Argentum. The parts
            below are the ones this page understands.
          </p>
        ) : null}

        <div className="overflow-hidden rounded-lg border bg-black">
          <Player
            ref={playerRef}
            component={Report}
            inputProps={{ plan }}
            durationInFrames={Math.max(1, plan.total_frames)}
            compositionWidth={plan.width}
            compositionHeight={plan.height}
            fps={plan.fps}
            style={{ width: "100%" }}
            controls
            doubleClickToFullscreen
          />
        </div>

        {/* The narrative beside the frame rather than buried in speaker notes,
            which is the half of T-R4's deck a PDF reader never saw. */}
        <section className="grid gap-4 md:grid-cols-[2fr_1fr]">
          <div className="rounded-lg border p-4">
            <h2 className="text-sm font-medium text-muted-foreground">
              What this scene says
            </h2>
            <p className="mt-2 whitespace-pre-wrap text-sm leading-relaxed">
              {scene?.notes?.trim() ||
                "No commentary was written for this scene."}
            </p>
          </div>
          <div className="rounded-lg border p-4 text-sm text-muted-foreground">
            <button
              type="button"
              onClick={() =>
                playing ? playerRef.current?.pause() : playerRef.current?.play()
              }
              className="mb-3 inline-flex items-center gap-2 rounded-md border px-3 py-2 text-foreground hover:bg-muted"
            >
              {playing ? (
                <Pause className="h-4 w-4" />
              ) : (
                <Play className="h-4 w-4" />
              )}
              {playing ? "Pause" : "Play"}
            </button>
            <dl className="space-y-1">
              <div className="flex justify-between gap-4">
                <dt>Scenes</dt>
                <dd>{plan.scenes.length}</dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt>Length</dt>
                <dd>{formatDuration(plan.total_frames, plan.fps)}</dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt>Link expires</dt>
                <dd>{new Date(view.expires_at).toLocaleDateString()}</dd>
              </div>
            </dl>
          </div>
        </section>
      </div>
    </div>
  );
}

function formatDuration(frames: number, fps: number) {
  const total = Math.round(frames / Math.max(1, fps));
  const m = Math.floor(total / 60);
  const s = total % 60;
  return `${m}:${String(s).padStart(2, "0")}`;
}

function Centered({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex min-h-screen flex-col items-center justify-center bg-background px-6 text-center text-foreground">
      {children}
    </div>
  );
}
