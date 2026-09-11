/**
 * The harness entry: one scene per `?scene=` value, mounted from the real
 * components with `@/lib/api` and `@/store/auth` aliased to stubs.
 *
 * Scenes are listed in `SCENES` and shot by `shoot.mjs`. A scene that needs an
 * interaction to reach its state (opening the form, typing past a cap) is
 * driven by the shooter rather than faked here — clicking the product's own
 * button is the difference between photographing the screen and photographing
 * a reconstruction of it.
 */
import ReactDOM from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ToolCallCard } from "@/features/chat/tool-call-card";
import { ParticipantBar } from "@/features/chat/participant-bar";
import { MentionMenu } from "@/features/chat/mention-menu";
import { agentColorIndex } from "@/features/chat/agent-colors";
import { SkillsTab } from "@/features/settings/skills-tab";
import { SettingsPage } from "@/features/settings/settings-page";
import { SharePage } from "@/features/share/share-page";
import { Scene, makeAPI, setHarnessAdmin, SKILLS_OK, SKILLS_OVERFLOW } from "./fixtures";
import { setFixtures } from "./stub-api";
import "@/index.css";

const scene = new URLSearchParams(location.search).get("scene") ?? "chat-chips";

function ChatChips() {
  return (
    <Scene title="Chat timeline — the chips a turn that opened a procedure leaves behind">
      <div className="max-w-2xl space-y-3 rounded-lg border border-border bg-card p-4">
        <p className="text-sm">
          Revenue by branch for August was <strong>Rp 1.24 bn</strong>, 6.1% above July.
        </p>
        <div className="flex flex-wrap gap-2">
          {/* The pair a single load_skill leaves: the call, which carries the
              name, and the result, which carries an empty map. */}
          <ToolCallCard name="load_skill" payload={{ name: "Weekly revenue by branch" }} />
          <ToolCallCard name="load_skill" payload={{}} />
          <ToolCallCard name="run_sql" payload={{ sql: "SELECT cabang, SUM(total) FROM …" }} />
          {/* The fallback, deliberately unchanged: a tool nobody has written
              copy for shows its real name rather than a guess at a nice one. */}
          <ToolCallCard name="some_new_tool" payload={{}} />
        </div>
      </div>
    </Scene>
  );
}

/* ── The room (T-N4) ──────────────────────────────────────────────────── */

/** A roster of four with one disabled, and a room holding three of them. The
 *  disabled one is what the add menu has to show greyed with a reason rather
 *  than hide, so an admin can see why the agent they want is not offered. */
const ROOM_ROSTER = [
  { id: "ag-fin", name: "Finance", enabled: true },
  { id: "ag-ops", name: "Ops", enabled: true },
  { id: "ag-people", name: "People", enabled: true },
  { id: "ag-legal", name: "Legal", enabled: false },
] as never[];

const ROOM_PARTICIPANTS = [
  { id: "tp-1", thread_id: "th-1", agent_id: "ag-fin", agent_name: "Finance", added_at: "" },
  { id: "tp-2", thread_id: "th-1", agent_id: "ag-ops", agent_name: "Ops", added_at: "" },
  { id: "tp-3", thread_id: "th-1", agent_id: "ag-people", agent_name: "People", added_at: "" },
] as never[];

const ROOM_COLORS = agentColorIndex(["ag-fin", "ag-ops", "ag-people", "ag-legal"]);

function RoomBar({ grayscale = false }: { grayscale?: boolean }) {
  return (
    <Scene
      title={
        grayscale
          ? "The room in grayscale — the name carries the attribution, the colour only reinforces it"
          : "The room — who is in this conversation, who answers an unaddressed message"
      }
    >
      <div className={grayscale ? "grayscale" : undefined}>
        <ParticipantBar
          participants={ROOM_PARTICIPANTS}
          roster={ROOM_ROSTER}
          colorIndex={ROOM_COLORS}
          defaultSpeakerID="ag-fin"
          onAdd={() => {}}
          onRemove={() => {}}
        />
      </div>
    </Scene>
  );
}

function RoomMentions() {
  return (
    <Scene title="Addressing — the @ menu offers participants only, never the whole roster">
      {/* The menu is `absolute bottom-full`, so it needs the same anchor the
          real composer gives it: a `relative` box with room above. The first
          run of this scene put it on a wrapper instead and photographed the
          menu half off the top of the frame. */}
      <div className="flex h-56 w-full max-w-xl items-end">
        <div className="relative w-full rounded-xl border border-border bg-card p-3">
          <MentionMenu
            participants={ROOM_PARTICIPANTS}
            query={{ at: 0, query: "" }}
            colorIndex={ROOM_COLORS}
            onPick={() => {}}
          />
          <span className="text-sm text-muted-subtle">@</span>
        </div>
      </div>
    </Scene>
  );
}

function render(node: React.ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  ReactDOM.createRoot(document.getElementById("root")!).render(
    <QueryClientProvider client={qc}>{node}</QueryClientProvider>,
  );
}

switch (scene) {
  case "chat-chips":
    render(<ChatChips />);
    break;

  case "room-bar":
    render(<RoomBar />);
    break;

  case "room-bar-grayscale":
    render(<RoomBar grayscale />);
    break;

  case "room-mentions":
    render(<RoomMentions />);
    break;

  case "settings-admin":
    setHarnessAdmin(true);
    render(
      <Scene title="Settings, as an admin — Procedures is offered">
        <SettingsPage />
      </Scene>,
    );
    break;

  case "settings-member":
    setHarnessAdmin(false);
    render(
      <Scene title="Settings, as a member — Procedures is absent, not empty">
        <SettingsPage />
      </Scene>,
    );
    break;

  case "skills-form":
    setHarnessAdmin(true);
    setFixtures(makeAPI(SKILLS_OK));
    render(
      <Scene title="Settings → Procedures — the counters and the two preview panes">
        <SkillsTab />
      </Scene>,
    );
    break;

  case "skills-overflow":
    setHarnessAdmin(true);
    setFixtures(makeAPI(SKILLS_OVERFLOW));
    render(
      <Scene title="Settings → Procedures — the index is over its bound">
        <SkillsTab />
      </Scene>,
    );
    break;

  // The shared report player (T-V4). No stub module is involved: `share-page`
  // deliberately uses a bare axios call rather than `@/lib/api`, because its
  // visitor has no session — so the shooter fulfils `/share/:token` at the
  // network, and what runs here is the page a logged-out visitor gets.
  case "share-player":
  case "share-player-future":
    render(<SharePage token="harness" />);
    break;

  default:
    document.body.textContent = `unknown scene: ${scene}`;
}
