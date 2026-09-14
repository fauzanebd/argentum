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
import { TeamTab } from "@/features/settings/team-tab";
import { AgentsTab, BindingsCard } from "@/features/settings/agents-tab";
import { MessageBubble } from "@/features/chat/chat-page";
import { colorForAgent } from "@/features/chat/agent-colors";
import { SharePage } from "@/features/share/share-page";
import { Scene, makeAPI, setHarnessAdmin, SKILLS_OK, SKILLS_OVERFLOW, TEAM } from "./fixtures";
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

/* ── The room's own lines, and a hand-off (T-N6, T-N7) ───────────────────── */

/** A transcript holding one of everything a room writes about itself: Ops' question
 *  to Finance, Finance's answer, a colleague that passed, Ops handing the next
 *  question to Finance, a question the budget refused and one whose recipient
 *  left. Rows as the API returns them — `metadata.room_event` and
 *  `metadata.handed_off_to` — drawn by the page's own bubble. */
const ROOM_TRANSCRIPT = [
  { id: "m1", role: "user", content: "We're short on SKU 4471 — what happened?" },
  { id: "m2", role: "assistant", agent_id: "ag-ops", agent_name: "Ops",
    content: "→ Finance: Was a goods-in posted for SKU 4471 after Monday?", metadata: { room_event: "nudge" } },
  { id: "m3", role: "assistant", agent_id: "ag-ops", agent_name: "Ops",
    content: "Stock for SKU 4471 shows 0 since Tuesday's count. I asked Finance whether a goods-in was posted after Monday." },
  { id: "m4", role: "assistant", agent_id: "ag-fin", agent_name: "Finance",
    content: "One goods-in on Tuesday: 200 units, posted to bin C-14 instead of A-02.", metadata: { asked_by: "ag-ops" } },
  { id: "m5", role: "assistant", agent_id: "ag-people", agent_name: "People",
    content: "People had nothing to add to the question from Ops.", metadata: { room_event: "settle" } },
  { id: "m6", role: "user", content: "And what did we write off for it last quarter?" },
  { id: "m7", role: "assistant", agent_id: "ag-ops", agent_name: "Ops",
    content: "Passed to Finance: Write-offs are booked in Finance's ledger.", metadata: { handed_off_to: "ag-fin" } },
  { id: "m8", role: "assistant", agent_id: "ag-fin", agent_name: "Finance",
    content: "Rp 3.200.000 was written off for SKU 4471 in Q2 — two damaged pallets.", metadata: { asked_by: "ag-ops" } },
  { id: "m9", role: "assistant", agent_id: "ag-fin", agent_name: "Finance",
    content: 'Finance\'s question to Ops went unasked — conversation turn budget spent (6 of 6 agent turns from one message): "Which bin were the pallets moved to?"',
    metadata: { room_event: "unasked" } },
  { id: "m10", role: "assistant",
    content: 'Legal left this conversation before answering the question from Ops: "Is the Q2 write-off reportable?"',
    metadata: { room_event: "withdrawn" } },
];

function RoomLines({ grayscale = false }: { grayscale?: boolean }) {
  const colors = agentColorIndex(["ag-fin", "ag-ops", "ag-people", "ag-legal"]);
  return (
    <Scene
      title={
        grayscale
          ? "A room's lines in grayscale — a limit, a settle and a withdrawn question are told apart by their words"
          : "A room's transcript — a question to a colleague, a settle, a hand-off, a limit and a withdrawn question"
      }
    >
      <div className={grayscale ? "grayscale" : undefined}>
        <div className="max-w-3xl space-y-5 rounded-lg border border-border bg-background p-6">
          {ROOM_TRANSCRIPT.map((m) => (
            <MessageBubble
              key={m.id}
              message={{ thread_id: "th-1", created_at: "2026-09-14T09:00:00Z", ...m } as never}
              showAuthor
              authorColor={colorForAgent(colors.get(m.agent_id ?? "") ?? -1)}
            />
          ))}
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

  case "room-lines":
    render(<RoomLines />);
    break;

  case "room-lines-grayscale":
    render(<RoomLines grayscale />);
    break;

  // Settings → Agents' form, for its "May ask other agents" flag (T-N6), whose
  // copy T-N7 widened. The shooter ticks it the way an admin would.
  case "agent-form-nudge": {
    setHarnessAdmin(true);
    // TEAM's agents carry only what the access screens read. The roster reads
    // every field an agent row has, so this scene serves whole rows.
    const base = makeAPI(SKILLS_OK);
    const row = (id: string, name: string, isDefault: boolean) => ({
      id, company_id: "co-1", name, description: "", persona_prompt: "", template_key: "",
      allowed_tools: [], source_ids: [], mcp_server_ids: [], skill_ids: [],
      can_nudge: false, is_default: isDefault, enabled: true,
      created_at: "2026-09-01T00:00:00Z", updated_at: "2026-09-01T00:00:00Z",
    });
    setFixtures({
      ...base,
      get: (path: string) =>
        path === "/agents"
          ? Promise.resolve({ data: { agents: [row("ag-ops", "Ops", true), row("ag-fin", "Finance", false)], tools: [], templates: [] } })
          : base.get(path),
    });
    render(
      <Scene title="Settings → Agents — the flag that lets an agent ask a colleague, or pass a question on">
        <div className="max-w-4xl">
          <AgentsTab />
        </div>
      </Scene>,
    );
    break;
  }

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

  // Settings → Team's access matrix (T-Z7, T-Z5). One screen, three scenes: the
  // shooter presses a different one of its buttons in each.
  case "team-access":
  case "team-access-restrict":
  case "team-access-dashboard-restrict":
  case "team-access-document-restrict":
    setHarnessAdmin(true);
    render(
      <Scene
        title={
          scene === "team-access"
            ? "Settings → Team — one person's access, and who can reach each agent, dashboard, source and document, drawn from one read per kind"
            : scene === "team-access-restrict"
              ? "Settings → Team — restricting an agent warns, names who loses access, and waits for a confirm"
              : scene === "team-access-dashboard-restrict"
                ? "Settings → Team — restricting a dashboard also counts the live share links it will revoke"
                : "Settings → Team — restricting a document says what an agent searching for someone will stop finding"
        }
      >
        <TeamTab />
      </Scene>,
    );
    break;

  // Settings → Agents' channel bindings (T-Z8): an acknowledged binding to a
  // restricted agent, a silenced one with its Acknowledge button, and the form's
  // acknowledgement, which the shooter opens by choosing HR the way an admin
  // would.
  case "agent-bindings":
    setHarnessAdmin(true);
    render(
      <Scene title="Settings → Agents — a restricted agent answers in a channel only where an admin acknowledged it">
        <div className="max-w-4xl">
          <BindingsCard agents={TEAM.agents as never[]} />
        </div>
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
