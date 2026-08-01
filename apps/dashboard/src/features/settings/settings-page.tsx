import { useState } from "react";
import * as Tabs from "@radix-ui/react-tabs";
import { GeneralTab } from "./general-tab";
import { ConnectionsTab } from "./connections-tab";
import { AgentsTab } from "./agents-tab";
import { MCPServersTab } from "./mcp-servers-tab";
import { PhonesTab } from "./phones-tab";
import { IntegrationsTab } from "./integrations-tab";
import { TeamTab } from "./team-tab";
import { ReportsTab } from "./reports-tab";
import { APIKeysTab } from "./api-keys-tab";
import { AboutTab } from "./about-tab";
import { AdminGate } from "@/components/layout/admin-gate";
import { useIsAdmin } from "@/store/auth";
import { cn } from "@/lib/utils";

export function SettingsPage() {
  const [tab, setTab] = useState("general");
  const isAdmin = useIsAdmin();

  // Team, Reports and API keys are hidden rather than read-only: every route
  // behind them is admin-only, including the GET, so a member would see an
  // empty panel and a 403. The other panels have member-readable GETs, so they
  // render disabled instead.
  const tabs = [
    { id: "general", label: "General" },
    { id: "connections", label: "Databases" },
    { id: "agents", label: "Agents" },
    // Admin-only on every route, including the read — a server is a credential
    // plus an egress destination — so the tab is hidden rather than disabled,
    // like Team and API keys above.
    ...(isAdmin ? [{ id: "mcp-servers", label: "MCP servers" }] : []),
    { id: "phones", label: "Phone numbers" },
    { id: "integrations", label: "Integrations" },
    ...(isAdmin ? [{ id: "reports", label: "Reports" }] : []),
    ...(isAdmin ? [{ id: "api-keys", label: "API keys" }] : []),
    ...(isAdmin ? [{ id: "team", label: "Team" }] : []),
    { id: "about", label: "About" },
  ];

  return (
    <div className="h-full overflow-y-auto">
      <div className="max-w-4xl mx-auto px-6 py-8">
        <h1 className="text-2xl font-bold mb-1">Settings</h1>
        <p className="text-sm text-muted-foreground mb-6">
          Manage your company preferences, databases, authorised phone numbers and team.
        </p>
        <Tabs.Root value={tab} onValueChange={setTab}>
          <Tabs.List className="inline-flex border-b border-border mb-6">
            {tabs.map((t) => (
              <Tabs.Trigger
                key={t.id}
                value={t.id}
                className={cn(
                  "px-4 py-2 text-sm border-b-2 transition-colors",
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
          <Tabs.Content value="connections">
            <AdminGate>
              <ConnectionsTab />
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
            <Tabs.Content value="mcp-servers">
              <MCPServersTab />
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
