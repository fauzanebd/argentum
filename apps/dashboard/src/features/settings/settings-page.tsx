import { useState } from "react";
import * as Tabs from "@radix-ui/react-tabs";
import { GeneralTab } from "./general-tab";
import { DataSourcesTab } from "./data-sources-tab";
import { MetricsTab } from "./metrics-tab";
import { AgentsTab } from "./agents-tab";
import { SkillsTab } from "./skills-tab";
import { WebhooksTab } from "./webhooks-tab";
import { PhonesTab } from "./phones-tab";
import { IntegrationsTab } from "./integrations-tab";
import { TeamTab } from "./team-tab";
import { ReportsTab } from "./reports-tab";
import { ImagesTab } from "./images-tab";
import { APIKeysTab } from "./api-keys-tab";
import { EmbedTab } from "./embed-tab";
import { AboutTab } from "./about-tab";
import { AdminGate } from "@/components/layout/admin-gate";
import { useIsAdmin } from "@/store/auth";
import { settingsTabs } from "./tabs";
import { cn } from "@/lib/utils";

export function SettingsPage() {
  const [tab, setTab] = useState("general");
  const isAdmin = useIsAdmin();

  // Which tabs exist, and which a member is offered, live in `tabs.ts` — the
  // rule is "a panel whose GET is admin-only is hidden rather than read-only",
  // and it is pinned by a test there rather than by this comment.
  const tabs = settingsTabs(isAdmin);

  return (
    <div className="h-full overflow-y-auto">
      <div className="max-w-6xl mx-auto px-6 py-8">
        <h1 className="text-2xl font-bold mb-1">Settings</h1>
        <p className="text-sm text-muted-foreground mb-6">
          Manage your company preferences, data sources, authorised phone numbers and team.
        </p>
        <Tabs.Root value={tab} onValueChange={setTab}>
          {/* w-full + horizontal scroll rather than inline-flex: the strip is
              wider than the panels below it on a narrow window, and an
              inline-flex list wraps its labels and spills past the cards
              instead of staying inside the same column. */}
          <Tabs.List className="flex w-full overflow-x-auto border-b border-border mb-6">
            {tabs.map((t) => (
              <Tabs.Trigger
                key={t.id}
                value={t.id}
                className={cn(
                  "shrink-0 whitespace-nowrap px-4 py-2 text-sm border-b-2 transition-colors",
                  tab === t.id
                    ? "border-primary text-foreground"
                    : "border-transparent text-muted-foreground hover:text-foreground",
                )}
              >
                {t.label}
              </Tabs.Trigger>
            ))}
          </Tabs.List>
          <Tabs.Content value="general">
            <AdminGate>
              <GeneralTab />
            </AdminGate>
          </Tabs.Content>
          {/* Gating lives inside: databases render read-only for a member,
              MCP servers are not offered to one at all. */}
          <Tabs.Content value="data-sources">
            <DataSourcesTab />
          </Tabs.Content>
          {/* Reads are member-level, but defining and testing a metric is admin
              (Test runs tenant SQL), so the panel is gated like the others. */}
          <Tabs.Content value="metrics">
            <AdminGate>
              <MetricsTab />
            </AdminGate>
          </Tabs.Content>
          {/* Reads are member-level here — T-S3 puts the roster in the chat
              picker — but every write is admin, so the panel is gated the same
              way the other configuration panels are. */}
          <Tabs.Content value="agents">
            <AdminGate>
              <AgentsTab />
            </AdminGate>
          </Tabs.Content>
          {isAdmin && (
            <Tabs.Content value="webhooks">
              <WebhooksTab />
            </Tabs.Content>
          )}
          {/* Admin on every route including the read, like MCP servers: a
              procedure is text the agents follow as an instruction. Not
              rendered for a member rather than wrapped in AdminGate, which is
              what it was: the gate disables the form and cannot make
              `GET /api/skills` return anything, so a member read this
              workspace's procedures as "No procedures yet". */}
          {isAdmin && (
            <Tabs.Content value="skills">
              <SkillsTab />
            </Tabs.Content>
          )}
          <Tabs.Content value="phones">
            <AdminGate>
              <PhonesTab />
            </AdminGate>
          </Tabs.Content>
          <Tabs.Content value="integrations">
            <AdminGate>
              <IntegrationsTab />
            </AdminGate>
          </Tabs.Content>
          <Tabs.Content value="images">
            <ImagesTab />
          </Tabs.Content>
          {isAdmin && (
            <Tabs.Content value="reports">
              <ReportsTab />
            </Tabs.Content>
          )}
          {isAdmin && (
            <Tabs.Content value="api-keys">
              <APIKeysTab />
            </Tabs.Content>
          )}
          {isAdmin && (
            <Tabs.Content value="embed">
              <EmbedTab />
            </Tabs.Content>
          )}
          {isAdmin && (
            <Tabs.Content value="team">
              <TeamTab />
            </Tabs.Content>
          )}
          <Tabs.Content value="about">
            <AboutTab />
          </Tabs.Content>
        </Tabs.Root>
      </div>
    </div>
  );
}
