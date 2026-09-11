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
import { SkillsTab } from "@/features/settings/skills-tab";
import { SettingsPage } from "@/features/settings/settings-page";
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

  default:
    document.body.textContent = `unknown scene: ${scene}`;
}
