// @vitest-environment jsdom
import { fireEvent, render, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * Settings → Integrations (RIZ-100).
 *
 * The rule this file pins is the one the tab broke for months: **every row is a
 * channel that works today, and every button on it leads somewhere real.**
 * Telegram used to sit here with a "Coming soon" badge and a Configure button
 * that opened a form with every input disabled, beside a "Notify me when
 * ready" button that showed a toast and stored nothing. So the assertions are
 * not "a list renders"; they are the exact rows, in order, and a click on each
 * row's one button that has to land on a config panel or hand off to Phone
 * numbers. A row added without either fails here.
 *
 * The three config panels are mocked: they fetch, and what matters on this
 * screen is that Configure opens *its own* panel, not what the panel does.
 */

let admin = true;
vi.mock("@/store/auth", () => ({ useIsAdmin: () => admin }));

vi.mock("./slack-tab", () => ({ SlackTab: () => <div>slack config panel</div> }));
vi.mock("./discord-tab", () => ({ DiscordTab: () => <div>discord config panel</div> }));
vi.mock("./lark-tab", () => ({ LarkTab: () => <div>lark config panel</div> }));

// Only for the SettingsPage test at the bottom, which needs the real tab strip
// and nothing behind the other panels.
vi.mock("./general-tab", () => ({ GeneralTab: () => <div>general panel</div> }));
vi.mock("./data-sources-tab", () => ({ DataSourcesTab: () => null }));
vi.mock("./metrics-tab", () => ({ MetricsTab: () => null }));
vi.mock("./agents-tab", () => ({ AgentsTab: () => null }));
vi.mock("./skills-tab", () => ({ SkillsTab: () => null }));
vi.mock("./webhooks-tab", () => ({ WebhooksTab: () => null }));
vi.mock("./phones-tab", () => ({ PhonesTab: () => <div>phone numbers panel</div> }));
vi.mock("./team-tab", () => ({ TeamTab: () => null }));
vi.mock("./reports-tab", () => ({ ReportsTab: () => null }));
vi.mock("./images-tab", () => ({ ImagesTab: () => null }));
vi.mock("./api-keys-tab", () => ({ APIKeysTab: () => null }));
vi.mock("./embed-tab", () => ({ EmbedTab: () => null }));
vi.mock("./about-tab", () => ({ AboutTab: () => null }));

import { AdminGate } from "@/components/layout/admin-gate";
import { IntegrationsTab } from "./integrations-tab";
import { SettingsPage } from "./settings-page";

const onOpenPhones = vi.fn();

function renderTab() {
  return render(
    <AdminGate>
      <IntegrationsTab onOpenPhones={onOpenPhones} />
    </AdminGate>,
  );
}

function rows() {
  return within(screen.getByRole("list")).getAllByRole("listitem");
}

/** Text that only a stub or a promise would put on this tab. */
const promises = [/telegram/i, /coming soon/i, /notify me/i, /in development/i];

function expectNoPromises() {
  for (const p of promises) expect(screen.queryByText(p)).not.toBeInTheDocument();
}

beforeEach(() => {
  admin = true;
  onOpenPhones.mockReset();
});

describe("the integrations list", () => {
  it("lists exactly the channels that work, WhatsApp first", () => {
    renderTab();
    expect(rows().map((r) => r.getAttribute("aria-label"))).toEqual([
      "WhatsApp",
      "Slack",
      "Discord",
      "Lark",
    ]);
    expect(
      screen.getByText("Chat with the Argentum bot from WhatsApp, from numbers you authorise."),
    ).toBeInTheDocument();
  });

  it("says nothing about Telegram, a release date or a waiting list", () => {
    renderTab();
    expectNoPromises();
  });

  it("gives every row exactly one button, and every button does something", () => {
    renderTab();
    for (const name of ["WhatsApp", "Slack", "Discord", "Lark"]) {
      const row = rows().find((r) => r.getAttribute("aria-label") === name)!;
      const buttons = within(row).getAllByRole("button");
      expect(buttons).toHaveLength(1);
      fireEvent.click(buttons[0]);

      // Either it handed off to Phone numbers, or it opened a config panel.
      // Nothing else counts: a toast, a disabled form, a no-op.
      const back = screen.queryByRole("button", { name: /back to integrations/i });
      if (back) {
        expect(screen.getByText(/config panel$/)).toBeInTheDocument();
        expectNoPromises();
        fireEvent.click(back);
      } else {
        expect(onOpenPhones).toHaveBeenCalledTimes(1);
      }
    }
    expect(rows()).toHaveLength(4);
  });

  it("hands WhatsApp off to Phone numbers rather than opening a detail of its own", () => {
    renderTab();
    fireEvent.click(screen.getByRole("button", { name: "Manage numbers" }));
    expect(onOpenPhones).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("button", { name: /back to integrations/i })).not.toBeInTheDocument();
    expect(rows()).toHaveLength(4);
  });

  it.each([
    ["Slack", "slack config panel"],
    ["Discord", "discord config panel"],
    ["Lark", "lark config panel"],
  ])("opens %s's own config panel from Configure, and Back returns to the list", (name, panel) => {
    renderTab();
    const row = rows().find((r) => r.getAttribute("aria-label") === name)!;
    fireEvent.click(within(row).getByRole("button", { name: "Configure" }));

    expect(screen.getByRole("heading", { name })).toBeInTheDocument();
    expect(screen.getByText(panel)).toBeInTheDocument();
    // Exactly one panel: Configure on Slack must not also mount Discord's.
    expect(screen.getAllByText(/config panel$/)).toHaveLength(1);
    expect(onOpenPhones).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: /back to integrations/i }));
    expect(rows()).toHaveLength(4);
  });

  it("shows a member the same four rows read-only, with every button disabled", () => {
    admin = false;
    renderTab();

    expect(screen.getByText(/you have member access/i)).toBeInTheDocument();
    expect(rows()).toHaveLength(4);
    const buttons = screen.getAllByRole("button");
    expect(buttons).toHaveLength(4);
    for (const b of buttons) expect(b).toBeDisabled();
  });
});

describe("Settings → Integrations → Manage numbers", () => {
  it("switches Settings to the Phone numbers tab", () => {
    render(<SettingsPage />);
    // Radix activates a tab on mousedown, not click.
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Integrations" }));
    expect(screen.getByRole("tab", { name: "Integrations" })).toHaveAttribute(
      "aria-selected",
      "true",
    );

    fireEvent.click(screen.getByRole("button", { name: "Manage numbers" }));

    expect(screen.getByRole("tab", { name: "Phone numbers" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByText("phone numbers panel")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Manage numbers" })).not.toBeInTheDocument();
  });
});
